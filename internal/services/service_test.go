package services

import (
	"context"
	"errors"
	"github.com/ubuntu-vpn-gateway/ubuntu-vpn-gateway/internal/provider"
	"github.com/ubuntu-vpn-gateway/ubuntu-vpn-gateway/internal/proxy/subscription"
	"github.com/ubuntu-vpn-gateway/ubuntu-vpn-gateway/internal/proxy/xray"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

type memoryStore struct{ node Node }

func (m *memoryStore) GetNode(_ context.Context, id string) (Node, error) {
	if id != m.node.ID {
		return Node{}, errors.New("узел не найден")
	}
	return m.node, nil
}
func (m *memoryStore) ListNodes(context.Context) ([]Node, error)                     { return []Node{m.node}, nil }
func (m *memoryStore) UpsertNode(_ context.Context, n Node) error                    { m.node = n; return nil }
func (m *memoryStore) MarkSubscriptionStale(context.Context, string, []string) error { return nil }
func (m *memoryStore) GetSubscription(context.Context, string) (Subscription, error) {
	return Subscription{}, errors.New("нет подписки")
}
func (m *memoryStore) ListSubscriptions(context.Context) ([]Subscription, error)          { return nil, nil }
func (m *memoryStore) UpdateSubscription(context.Context, Subscription) error             { return nil }
func (m *memoryStore) ApplySubscriptionNodes(context.Context, Subscription, []Node) error { return nil }
func TestAWGConnectionUnavailable(t *testing.T) {
	m := &memoryStore{node: Node{ID: "awg", Kind: "awg", Enabled: true}}
	s := New(m, "/usr/bin/xray", 1080, 8080)
	if e := s.Connect(context.Background(), "awg"); !errors.Is(e, ErrAWGUnavailable) {
		t.Fatalf("ожидался явный отказ AWG: %v", e)
	}
}

type switchStore struct{ nodes map[string]Node }

func (m *switchStore) GetNode(_ context.Context, id string) (Node, error) {
	n, ok := m.nodes[id]
	if !ok {
		return Node{}, errors.New("узел не найден")
	}
	return n, nil
}
func (m *switchStore) ListNodes(context.Context) ([]Node, error)                     { return nil, nil }
func (m *switchStore) UpsertNode(_ context.Context, n Node) error                    { m.nodes[n.ID] = n; return nil }
func (m *switchStore) MarkSubscriptionStale(context.Context, string, []string) error { return nil }
func (m *switchStore) GetSubscription(context.Context, string) (Subscription, error) {
	return Subscription{}, errors.New("нет подписки")
}
func (m *switchStore) ListSubscriptions(context.Context) ([]Subscription, error)          { return nil, nil }
func (m *switchStore) UpdateSubscription(context.Context, Subscription) error             { return nil }
func (m *switchStore) ApplySubscriptionNodes(context.Context, Subscription, []Node) error { return nil }

type fakeProcess struct {
	done  chan struct{}
	mu    sync.Mutex
	stops int
}

func newFakeProcess() *fakeProcess           { return &fakeProcess{done: make(chan struct{})} }
func (p *fakeProcess) Done() <-chan struct{} { return p.done }
func (p *fakeProcess) Stop()                 { p.mu.Lock(); p.stops++; p.mu.Unlock() }
func (p *fakeProcess) stopCount() int        { p.mu.Lock(); defer p.mu.Unlock(); return p.stops }

func TestConnectFailedCandidateKeepsOldProcess(t *testing.T) {
	store := &switchStore{nodes: map[string]Node{
		"old": {ID: "old", Kind: "vless", Enabled: true, RawConfig: xrayTestURI("old.example")},
		"new": {ID: "new", Kind: "vless", Enabled: true, RawConfig: xrayTestURI("new.example")},
	}}
	s := New(store, "/usr/bin/xray", 1080, 8080)
	old, candidate := newFakeProcess(), newFakeProcess()
	s.proc, s.active, s.state = old, "old", provider.StateConnected
	s.startProcess = func(context.Context, xray.Node, int, int) (managedProcess, error) { return candidate, nil }
	s.probeProcess = func(context.Context, int) (provider.TestResult, error) {
		return provider.TestResult{}, errors.New("секретная причина endpoint")
	}
	if err := s.Connect(context.Background(), "new"); err == nil {
		t.Fatal("ожидался отказ кандидата")
	}
	if old.stopCount() != 0 {
		t.Fatal("старый процесс остановлен до успешной проверки кандидата")
	}
	if candidate.stopCount() != 1 {
		t.Fatal("процесс кандидата не остановлен")
	}
	if got := s.Detail(); got.ActiveNodeID != "old" || got.State != provider.StateConnected {
		t.Fatalf("старое соединение потеряно: %+v", got)
	}
}

func TestConnectRollbackIgnoresRequestCancellation(t *testing.T) {
	store := &switchStore{nodes: map[string]Node{
		"old": {ID: "old", Kind: "vless", Enabled: true, RawConfig: xrayTestURI("old.example")},
		"new": {ID: "new", Kind: "vless", Enabled: true, RawConfig: xrayTestURI("new.example")},
	}}
	s := New(store, "/usr/bin/xray", 1080, 8080)
	old, candidate, restored := newFakeProcess(), newFakeProcess(), newFakeProcess()
	s.proc, s.active, s.state = old, "old", provider.StateConnected
	ctx, cancel := context.WithCancel(context.Background())
	starts := 0
	s.startProcess = func(callCtx context.Context, _ xray.Node, socks, _ int) (managedProcess, error) {
		starts++
		switch starts {
		case 1:
			return candidate, nil
		case 2:
			cancel()
			return nil, errors.New("рабочий порт занят")
		case 3:
			if callCtx.Err() != nil {
				t.Fatal("rollback получил отменённый контекст запроса")
			}
			return restored, nil
		default:
			t.Fatalf("лишний запуск %d", starts)
			return nil, nil
		}
	}
	probes := 0
	s.probeProcess = func(context.Context, int) (provider.TestResult, error) {
		probes++
		return provider.TestResult{Internet: true, ExitIP: "203.0.113.1"}, nil
	}
	err := s.Connect(ctx, "new")
	if err == nil || !strings.Contains(err.Error(), "прежний узел восстановлен") {
		t.Fatalf("нет точного результата rollback: %v", err)
	}
	if old.stopCount() != 1 || candidate.stopCount() != 1 {
		t.Fatalf("неверный lifecycle: old=%d candidate=%d", old.stopCount(), candidate.stopCount())
	}
	if got := s.Detail(); got.ActiveNodeID != "old" || got.State != provider.StateConnected {
		t.Fatalf("rollback не восстановил состояние: %+v", got)
	}
}

func xrayTestURI(host string) string {
	return "vless://11111111-1111-4111-8111-111111111111@" + host + ":443?type=raw&security=reality&sni=" + host + "&fp=chrome&pbk=AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA&sid=aabbccdd&flow=xtls-rprx-vision#Test"
}

func TestSubscriptionScheduleDue(t *testing.T) {
	now := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	if !subscriptionDue("", now, 6*time.Hour) {
		t.Fatal("подписка без истории не запланирована")
	}
	if subscriptionDue(now.Add(-5*time.Hour).Format(time.RFC3339), now, 6*time.Hour) {
		t.Fatal("подписка запланирована раньше срока")
	}
	if !subscriptionDue(now.Add(-6*time.Hour).Format(time.RFC3339), now, 6*time.Hour) {
		t.Fatal("подписка не запланирована в срок")
	}
	for _, tc := range []struct {
		value string
		want  time.Duration
	}{{"6h", 6 * time.Hour}, {"12h", 12 * time.Hour}, {"24h", 24 * time.Hour}} {
		got, ok := subscriptionInterval(Subscription{Enabled: true, UpdateInterval: tc.value})
		if !ok || got != tc.want {
			t.Fatalf("интервал %s: %v, %t", tc.value, got, ok)
		}
	}
	if _, ok := subscriptionInterval(Subscription{Enabled: true, UpdateInterval: "disabled"}); ok {
		t.Fatal("disabled подписка запланирована")
	}
}

func TestActiveProcessExitPublishesFailure(t *testing.T) {
	s := New(&memoryStore{}, "/usr/bin/xray", 1080, 8080)
	p := newFakeProcess()
	s.proc, s.active, s.state = p, "active", provider.StateConnected
	events := make(chan string, 1)
	s.SetEventSink(func(kind, _ string) { events <- kind })
	s.watchActive(p, "active")
	close(p.done)
	select {
	case kind := <-events:
		if kind != "connection.failed" {
			t.Fatalf("неверное событие: %s", kind)
		}
	case <-time.After(time.Second):
		t.Fatal("завершение Xray не опубликовано")
	}
	status := s.Detail()
	if status.State != provider.StateFailed || status.ActiveNodeID != "" {
		t.Fatalf("неверный статус после crash: %+v", status)
	}
}
func TestRealSubscriptionProxy(t *testing.T) {
	path := os.Getenv("SUBSCRIPTION_TEST_FILE")
	bin := os.Getenv("XRAY_TEST_BINARY")
	if path == "" || bin == "" {
		t.Skip("тест реального proxy запускается явно")
	}
	b, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	nodes, e := subscription.Parse(b)
	if e != nil {
		t.Fatal(e)
	}
	m := &memoryStore{node: Node{ID: "live", Name: nodes[0].Name, Kind: "vless", RawConfig: nodes[0].RawURI, Enabled: true}}
	s := New(m, bin, 0, 0)
	result, e := s.Test(context.Background(), "live")
	if e != nil {
		t.Fatal(e)
	}
	if !result.Internet || !result.Endpoint || result.ExitIP == "" {
		t.Fatalf("уровни проверки не пройдены: service=%t endpoint=%t internet=%t exit_ip_present=%t", result.Service, result.Endpoint, result.Internet, result.ExitIP != "")
	}
}

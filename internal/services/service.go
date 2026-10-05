package services

import (
	"context"
	"errors"
	"fmt"
	"github.com/ubuntu-vpn-gateway/ubuntu-vpn-gateway/internal/provider"
	"github.com/ubuntu-vpn-gateway/ubuntu-vpn-gateway/internal/proxy/xray"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

type process struct {
	cmd  *exec.Cmd
	done chan struct{}
	path string
}
type managedProcess interface {
	Stop()
	Done() <-chan struct{}
}

func (p *process) Stop()                 { stopProcess(p) }
func (p *process) Done() <-chan struct{} { return p.done }

type startProcessFunc func(context.Context, xray.Node, int, int) (managedProcess, error)
type probeProcessFunc func(context.Context, int) (provider.TestResult, error)

type Service struct {
	opMu                sync.Mutex
	refreshMu           sync.Mutex
	mu                  sync.Mutex
	tests               chan struct{}
	store               Store
	executable          string
	socksPort, httpPort int
	proc                managedProcess
	active              string
	state               provider.State
	message, exitIP     string
	eventSink           func(string, string)
	startProcess        startProcessFunc
	probeProcess        probeProcessFunc
}

func New(store Store, executable string, socksPort, httpPort int, maxConcurrent ...int) *Service {
	limit := 5
	if len(maxConcurrent) > 0 && maxConcurrent[0] > 0 && maxConcurrent[0] <= 5 {
		limit = maxConcurrent[0]
	}
	s := &Service{store: store, executable: executable, socksPort: socksPort, httpPort: httpPort, tests: make(chan struct{}, limit), state: provider.StateDisconnected, message: "Провайдер не подключен"}
	s.startProcess = func(ctx context.Context, n xray.Node, socks, httpPort int) (managedProcess, error) {
		return s.start(ctx, n, socks, httpPort)
	}
	s.probeProcess = probe
	return s
}
func (s *Service) SetEventSink(sink func(string, string)) {
	s.mu.Lock()
	s.eventSink = sink
	s.mu.Unlock()
}
func (s *Service) publish(kind, message string) {
	s.mu.Lock()
	sink := s.eventSink
	s.mu.Unlock()
	if sink != nil {
		sink(kind, message)
	}
}
func (s *Service) Status(ctx context.Context) (provider.Status, error) {
	d := s.Detail()
	return d.Status, nil
}
func (s *Service) Detail() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reapLocked()
	return Status{Status: provider.Status{State: s.state, Message: s.message}, ActiveNodeID: s.active, ExitIP: s.exitIP}
}
func (s *Service) reapLocked() {
	if s.proc != nil {
		select {
		case <-s.proc.Done():
			s.proc = nil
			s.active = ""
			s.exitIP = ""
			s.state = provider.StateFailed
			s.message = "Процесс Xray завершился"
		default:
		}
	}
}
func (s *Service) set(state provider.State, msg string) {
	s.mu.Lock()
	s.state = state
	s.message = msg
	s.mu.Unlock()
	s.publish("connection.state", msg)
}
func (s *Service) Connect(ctx context.Context, id string) error {
	s.opMu.Lock()
	defer s.opMu.Unlock()
	n, e := s.store.GetNode(ctx, id)
	if e != nil {
		return e
	}
	if !n.Enabled {
		return errors.New("Узел отключён")
	}
	if n.Kind == "awg" {
		return ErrAWGUnavailable
	}
	if n.Kind != "vless" {
		return errors.New("Неизвестный тип узла")
	}
	v, e := xray.ParseURI(n.RawConfig)
	if e != nil {
		return e
	}
	s.mu.Lock()
	oldID, old := s.active, s.proc
	s.state, s.message = provider.StateConnecting, "Проверка нового узла"
	s.mu.Unlock()
	s.publish("connection.connecting", "Начато подключение к узлу "+id)
	candidate, candidateSocks, e := s.startTemporary(ctx, v)
	if e != nil {
		return s.candidateFailed(old, "запуск кандидата", e)
	}
	probeCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	_, pe := s.probeProcess(probeCtx, candidateSocks)
	cancel()
	if pe != nil {
		candidate.Stop()
		return s.candidateFailed(old, "проверка кандидата", pe)
	}
	select {
	case <-candidate.Done():
		candidate.Stop()
		return s.candidateFailed(old, "стабильность кандидата", errors.New("процесс завершился"))
	default:
	}
	if err := ctx.Err(); err != nil {
		candidate.Stop()
		return s.candidateFailed(old, "проверка кандидата", err)
	}

	// Переключение и восстановление не зависят от отмены HTTP-запроса.
	swapCtx, swapCancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer swapCancel()
	var oldNode xray.Node
	if old != nil {
		stored, err := s.store.GetNode(swapCtx, oldID)
		if err != nil {
			candidate.Stop()
			return s.candidateFailed(old, "подготовка отката", err)
		}
		oldNode, err = xray.ParseURI(stored.RawConfig)
		if err != nil {
			candidate.Stop()
			return s.candidateFailed(old, "подготовка отката", err)
		}
		s.mu.Lock()
		if s.proc == old {
			s.proc = nil
			s.active = ""
			s.exitIP = ""
		}
		s.mu.Unlock()
		old.Stop()
	}
	s.mu.Lock()
	s.proc = nil
	s.active = ""
	s.exitIP = ""
	s.mu.Unlock()
	fixed, e := s.startProcess(swapCtx, v, s.socksPort, s.httpPort)
	if e != nil {
		candidate.Stop()
		return s.rollbackAfterSwap(oldID, oldNode, old != nil, "запуск на рабочих портах", e)
	}
	checkCtx, checkCancel := context.WithTimeout(swapCtx, 15*time.Second)
	result, e := s.probeProcess(checkCtx, s.socksPort)
	checkCancel()
	if e != nil {
		fixed.Stop()
		candidate.Stop()
		return s.rollbackAfterSwap(oldID, oldNode, old != nil, "проверка рабочих портов", e)
	}
	select {
	case <-fixed.Done():
		candidate.Stop()
		return s.rollbackAfterSwap(oldID, oldNode, old != nil, "стабильность на рабочих портах", errors.New("процесс завершился"))
	default:
	}
	candidate.Stop()
	s.mu.Lock()
	s.proc = fixed
	s.active = id
	s.exitIP = result.ExitIP
	s.state = provider.StateConnected
	s.message = "Подключено"
	s.mu.Unlock()
	s.watchActive(fixed, id)
	s.publish("connection.connected", "Узел подключён: "+id)
	return nil
}
func (s *Service) candidateFailed(old managedProcess, stage string, cause error) error {
	msg := "Подключение отклонено на этапе «" + stage + "»: " + safeReason(cause)
	s.mu.Lock()
	oldActive := old != nil
	if old != nil {
		select {
		case <-old.Done():
			oldActive = false
			s.proc, s.active, s.exitIP = nil, "", ""
		default:
		}
	}
	if oldActive {
		s.state, s.message = provider.StateConnected, "Прежнее соединение сохранено"
	} else {
		s.state, s.message = provider.StateFailed, msg
	}
	s.mu.Unlock()
	s.publish("connection.failed", msg)
	return errors.New(msg)
}
func (s *Service) rollbackAfterSwap(oldID string, oldNode xray.Node, hasOld bool, stage string, cause error) error {
	msg := "Сбой на этапе «" + stage + "»: " + safeReason(cause)
	if !hasOld {
		s.set(provider.StateFailed, msg)
		return errors.New(msg)
	}
	ctx, cancelRollback := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancelRollback()
	p, err := s.startProcess(ctx, oldNode, s.socksPort, s.httpPort)
	if err != nil {
		failureMsg := msg + "; прежний узел не восстановлен: " + safeReason(err)
		s.set(provider.StateFailed, failureMsg)
		return errors.New(failureMsg)
	}
	checkCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	r, err := s.probeProcess(checkCtx, s.socksPort)
	cancel()
	if err != nil {
		p.Stop()
		failureMsg := msg + "; прежний узел не восстановлен: " + safeReason(err)
		s.set(provider.StateFailed, failureMsg)
		return errors.New(failureMsg)
	}
	select {
	case <-p.Done():
		failureMsg := msg + "; прежний узел не восстановлен: процесс Xray завершился"
		s.set(provider.StateFailed, failureMsg)
		return errors.New(failureMsg)
	default:
	}
	s.mu.Lock()
	s.proc = p
	s.active = oldID
	s.exitIP = r.ExitIP
	s.state = provider.StateConnected
	s.message = "Прежнее соединение восстановлено"
	rollbackMessage := s.message
	s.mu.Unlock()
	s.watchActive(p, oldID)
	s.publish("connection.rollback", rollbackMessage)
	return errors.New(msg + "; прежний узел восстановлен")
}
func (s *Service) watchActive(p managedProcess, id string) {
	go func() {
		<-p.Done()
		s.mu.Lock()
		if s.proc != p || s.active != id {
			s.mu.Unlock()
			return
		}
		s.proc, s.active, s.exitIP = nil, "", ""
		s.state = provider.StateFailed
		s.message = "Процесс Xray неожиданно завершился"
		message := s.message
		s.mu.Unlock()
		s.publish("connection.failed", message)
	}()
}
func safeReason(err error) string {
	if err == nil {
		return "неизвестная ошибка"
	}
	s := strings.ToLower(err.Error())
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return "истекло время ожидания"
	case errors.Is(err, context.Canceled):
		return "операция отменена"
	case strings.Contains(s, "порт"):
		return "локальный порт недоступен"
	case strings.Contains(s, "конфигурац"), strings.Contains(s, "xray отклонил"):
		return "Xray отклонил конфигурацию"
	case strings.Contains(s, "интернет"), strings.Contains(s, "провер"):
		return "проверка доступа через proxy не прошла"
	case strings.Contains(s, "процесс"):
		return "процесс Xray завершился"
	default:
		return "внутренняя ошибка запуска Xray"
	}
}
func (s *Service) startTemporary(ctx context.Context, n xray.Node) (managedProcess, int, error) {
	for i := 0; i < 10; i++ {
		socks, err := freePort()
		if err != nil {
			return nil, 0, err
		}
		httpPort, err := freePort()
		if err != nil {
			return nil, 0, err
		}
		if socks == httpPort {
			continue
		}
		p, err := s.startProcess(ctx, n, socks, httpPort)
		if err == nil {
			return p, socks, nil
		}
	}
	return nil, 0, errors.New("не удалось выделить временные порты")
}
func (s *Service) Disconnect(ctx context.Context) error {
	s.opMu.Lock()
	defer s.opMu.Unlock()
	s.mu.Lock()
	p := s.proc
	s.proc = nil
	s.active = ""
	s.exitIP = ""
	s.state = provider.StateDisconnecting
	s.message = "Отключение"
	s.mu.Unlock()
	if p != nil {
		p.Stop()
	}
	s.set(provider.StateDisconnected, "Отключено")
	return nil
}
func stopProcess(p *process) {
	if p == nil {
		return
	}
	if p.cmd.Process != nil {
		_ = p.cmd.Process.Kill()
	}
	<-p.done
}
func (s *Service) start(ctx context.Context, n xray.Node, socks, httpPort int) (*process, error) {
	if !filepath.IsAbs(s.executable) {
		return nil, errors.New("Путь Xray должен быть абсолютным")
	}
	b, e := xray.Config(n, socks, httpPort)
	if e != nil {
		return nil, e
	}
	for _, port := range []int{socks, httpPort} {
		listener, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
		if err != nil {
			return nil, errors.New("Порт локального proxy уже занят")
		}
		_ = listener.Close()
	}
	f, e := os.CreateTemp("", "gateway-xray-*.json")
	if e != nil {
		return nil, e
	}
	path := f.Name()
	keep := false
	defer func() {
		if !keep {
			_ = os.Remove(path)
		}
	}()
	_ = f.Chmod(0600)
	if _, e = f.Write(b); e != nil {
		_ = f.Close()
		return nil, e
	}
	if e = f.Close(); e != nil {
		return nil, e
	}
	test := exec.CommandContext(ctx, s.executable, "run", "-test", "-config", path)
	test.Stdout = io.Discard
	test.Stderr = io.Discard
	if e = test.Run(); e != nil {
		return nil, errors.New("Xray отклонил конфигурацию")
	}
	cmd := exec.Command(s.executable, "run", "-config", path)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if e = cmd.Start(); e != nil {
		return nil, fmt.Errorf("Не удалось запустить Xray: %w", e)
	}
	p := &process{cmd: cmd, done: make(chan struct{}), path: path}
	keep = true
	go func() { _ = cmd.Wait(); _ = os.Remove(path); close(p.done) }()
	readyCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	for {
		socksReady := portReady(socks)
		httpReady := portReady(httpPort)
		if socksReady && httpReady {
			select {
			case <-p.done:
				return nil, errors.New("Процесс Xray завершился до открытия proxy")
			default:
				return p, nil
			}
		}
		select {
		case <-p.done:
			return nil, errors.New("Процесс Xray завершился до открытия proxy")
		case <-readyCtx.Done():
			stopProcess(p)
			return nil, errors.New("Xray не открыл локальный proxy")
		case <-time.After(50 * time.Millisecond):
		}
	}
}
func portReady(port int) bool {
	conn, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), 150*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}
func probe(ctx context.Context, port int) (provider.TestResult, error) {
	proxyURL, _ := url.Parse("socks5://127.0.0.1:" + strconv.Itoa(port))
	tr := &http.Transport{Proxy: http.ProxyURL(proxyURL), DisableKeepAlives: true}
	defer tr.CloseIdleConnections()
	client := &http.Client{Transport: tr, Timeout: 8 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	result := provider.TestResult{Service: true}
	for _, target := range []string{"https://www.gstatic.com/generate_204", "https://api.ipify.org"} {
		req, _ := http.NewRequestWithContext(ctx, "GET", target, nil)
		started := time.Now()
		resp, e := client.Do(req)
		if e != nil {
			return result, errors.New("Проверка через proxy не прошла")
		}
		body, e := io.ReadAll(io.LimitReader(resp.Body, 128))
		_ = resp.Body.Close()
		if e != nil {
			return result, e
		}
		if strings.Contains(target, "generate_204") {
			if resp.StatusCode != 204 {
				return result, errors.New("Проверка интернета через proxy не прошла")
			}
			result.Endpoint = true
			result.Internet = true
			result.LatencyMS = int(time.Since(started).Milliseconds())
		} else if resp.StatusCode == 200 && net.ParseIP(strings.TrimSpace(string(body))) != nil {
			result.ExitIP = strings.TrimSpace(string(body))
		}
	}
	if !result.Internet || result.ExitIP == "" {
		return result, errors.New("Проверка интернета или внешнего IP через proxy не прошла")
	}
	return result, nil
}
func (s *Service) Test(ctx context.Context, id string) (provider.TestResult, error) {
	select {
	case s.tests <- struct{}{}:
		defer func() { <-s.tests }()
	case <-ctx.Done():
		return provider.TestResult{}, ctx.Err()
	}
	n, e := s.store.GetNode(ctx, id)
	if e != nil {
		return provider.TestResult{}, e
	}
	if n.Kind == "awg" {
		return provider.TestResult{}, ErrAWGUnavailable
	}
	v, e := xray.ParseURI(n.RawConfig)
	if e != nil {
		return provider.TestResult{}, e
	}
	for i := 0; i < 10; i++ {
		port, err := freePort()
		if err != nil {
			return provider.TestResult{}, err
		}
		httpPort, err := freePort()
		if err != nil {
			return provider.TestResult{}, err
		}
		if port == httpPort {
			continue
		}
		p, err := s.start(ctx, v, port, httpPort)
		if err != nil {
			return provider.TestResult{}, err
		}
		testCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		result, err := probe(testCtx, port)
		cancel()
		stopProcess(p)
		if current, readErr := s.store.GetNode(ctx, id); readErr == nil {
			current.LastTestAt = time.Now().UTC().Format(time.RFC3339)
			if err == nil {
				current.LatencyMS = result.LatencyMS
				current.LastError = ""
			} else {
				current.LastError = "Проверка через proxy не прошла"
			}
			_ = s.store.UpsertNode(ctx, current)
		}
		return result, err
	}
	return provider.TestResult{}, errors.New("Не удалось выбрать порт проверки")
}
func freePort() (int, error) {
	l, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		return 0, e
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

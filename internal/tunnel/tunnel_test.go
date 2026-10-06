package tunnel

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestNFTDNSOrderAndGuard(t *testing.T) {
	p := Plan{ID: strings.Repeat("a", 32), Endpoint: "203.0.113.9", Port: 443, Peers: []string{"10.9.1.9", "2001:db8::1"}, LAN4: []string{"10.5.2.0/24"}}
	stage := nftBatch(p)
	if !strings.Contains(stage, "ct state new,established,related counter accept") || strings.Contains(stage, "meta mark") || strings.Contains(stage, "tproxy ip") || strings.Contains(stage, " drop") {
		t.Fatal("Conntrack staging не является только tracking-стадией")
	}
	final := nftBatchWithFlows(p, nil)
	if strings.Index(final, "meta nfproto ipv6 drop") > strings.Index(final, "meta mark & 0xff00 == 0x0200") {
		t.Fatal("IPv6 bypass обходит guard")
	}
	if strings.Index(final, "th dport 53 meta mark") > strings.Index(final, "ip daddr @lan4 return") {
		t.Fatal("LAN DNS обходит VPN")
	}
	if !strings.Contains(final, "12346 accept") || !strings.Contains(final, "0xffff00ff") || !strings.Contains(final, "ct direction reply") {
		t.Fatal("Нарушен DNS/mark/management контракт")
	}
}

func TestTrackingChainJSONAllowsOnlyConntrackAcceptRule(t *testing.T) {
	valid := []byte(`{"nftables":[{"chain":{"family":"inet","table":"uvg_tproxy","name":"uvg_output","type":"route","hook":"output","prio":-150,"policy":"accept"}},{"rule":{"family":"inet","table":"uvg_tproxy","chain":"uvg_output","expr":[{"match":{"op":"in","left":{"ct":{"key":"state"}},"right":["new","established","related"]}},{"counter":null},{"accept":null}]}}]}`)
	if err := validateTrackingChainJSON(valid, "uvg_output"); err != nil {
		t.Fatalf("normalized nft JSON tracking hook was rejected: %v", err)
	}
	withMutation := []byte(`{"nftables":[{"chain":{"family":"inet","table":"uvg_tproxy","name":"uvg_output","type":"route","hook":"output","prio":-150,"policy":"accept"}},{"rule":{"family":"inet","table":"uvg_tproxy","chain":"uvg_output","expr":[{"match":{"op":"in","left":{"ct":{"key":"state"}},"right":["related","new","established"]}},{"counter":null},{"accept":null},{"drop":null}]}}]}`)
	if err := validateTrackingChainJSON(withMutation, "uvg_output"); err == nil {
		t.Fatal("tracking hook with an extra drop expression was accepted")
	}
	if err := validateTrackingChainJSON(valid, "uvg_prerouting"); err == nil {
		t.Fatal("OUTPUT hook accepted as PREROUTING tracking hook")
	}
	wrongFamily := []byte(strings.Replace(string(valid), `"family":"inet"`, `"family":"ip"`, 1))
	if err := validateTrackingChainJSON(wrongFamily, "uvg_output"); err == nil {
		t.Fatal("tracking hook with a different family was accepted")
	}
	extraMatchOperand := []byte(strings.Replace(string(valid), `"op":"in"`, `"op":"in","dir":"reply"`, 1))
	if err := validateTrackingChainJSON(extraMatchOperand, "uvg_output"); err == nil {
		t.Fatal("tracking rule with an extra match operand was accepted")
	}
}

func TestManagementFlowsPreserveOnlyExactExistingTuples(t *testing.T) {
	p := Plan{
		ID:      strings.Repeat("a", 32),
		Peers:   []string{"10.9.1.9", "2001:db8::9"},
		SSHPort: 22,
		UIPort:  8443,
	}
	output := strings.Join([]string{
		`ESTAB 0 0 10.5.2.70:8443 10.9.1.9:53123`,
		`ESTAB 0 0 10.5.2.70:22 10.9.1.9:53124`,
		`SYN-RECV 0 0 [2001:db8::70]:8443 [2001:db8::9]:53125`,
		`ESTAB 0 0 10.5.2.70:9000 10.9.1.9:53126`,
		`ESTAB 0 0 10.5.2.70:8443 10.9.1.10:53127`,
		`LISTEN 0 4096 0.0.0.0:8443 0.0.0.0:*`,
		`SYN-SENT 0 1 10.5.2.70:22 10.9.1.9:53128`,
		`TIME-WAIT 0 0 10.5.2.70:8443 10.9.1.9:53129`,
	}, "\n")
	flows, err := parseManagementFlows(output, p)
	if err != nil {
		t.Fatal(err)
	}
	if len(flows) != 3 {
		t.Fatalf("ожидались только три точных management tuple: %+v", flows)
	}
	b := nftBatchWithFlows(p, flows)
	for _, exact := range []string{
		"10.5.2.70 . 10.9.1.9 . 22 . 53124",
		"10.5.2.70 . 10.9.1.9 . 8443 . 53123",
		"2001:db8::70 . 2001:db8::9 . 8443 . 53125",
	} {
		if !strings.Contains(b, exact) {
			t.Fatalf("в nft batch отсутствует exact tuple %s", exact)
		}
	}
	for _, forbidden := range []string{"10.5.2.70 . 10.9.1.9 . 9000 . 53126", "10.5.2.70 . 10.9.1.10 . 8443 . 53127", "53128", "53129"} {
		if strings.Contains(b, forbidden) {
			t.Fatalf("произвольный трафик ошибочно получил bypass: %s", forbidden)
		}
	}
	exactRule := strings.Index(b, "@management_flows4 return")
	if exactRule < 0 || strings.Index(b[exactRule:], "ct direction reply return") < 0 {
		t.Fatal("exact management tuple должен проверяться до conntrack-зависимого правила")
	}
}

func TestRollbackContinuesAfterTableDeleteFailure(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("проверка flock выполняется в Linux")
	}
	root := t.TempDir()
	id := strings.Repeat("d", 32)
	dir, _ := transactionDir(root, id)
	if err := saveJSON(filepath.Join(dir, "state.json"), Record{ID: id, State: "pending"}); err != nil {
		t.Fatal(err)
	}
	original := rollbackRun
	defer func() { rollbackRun = original }()
	var calls []string
	rollbackRun = func(name string, args ...string) ([]byte, error) {
		call := name + " " + strings.Join(args, " ")
		calls = append(calls, call)
		switch call {
		case "nft -j list tables":
			return []byte(`{"nftables":[{"table":{"family":"inet","name":"uvg_tproxy"}}]}`), nil
		case "nft -j list table inet uvg_tproxy":
			return []byte(`{"comment":"uvg:` + id + `"}`), nil
		case "nft delete table inet uvg_tproxy":
			return nil, errors.New("искусственный сбой удаления")
		case "nft flush chain inet uvg_tproxy uvg_output":
			return nil, nil
		case "nft flush chain inet uvg_tproxy uvg_prerouting":
			return nil, nil
		case "nft -j list chain inet uvg_tproxy uvg_output", "nft -j list chain inet uvg_tproxy uvg_prerouting":
			return []byte(`{"chain":{"name":"empty"}}`), nil
		case "ip -N -j -4 rule show":
			return []byte(`[{"priority":10000,"fwmark":"0x100","fwmask":"0xff00","table":200}]`), nil
		case "ip -4 rule del priority 10000 fwmark 0x100/0xff00 lookup 200":
			return nil, nil
		case "ip -N -j -4 route show table 200":
			return []byte(`[{"dst":"default","type":"local","dev":"lo","protocol":186}]`), nil
		case "ip -4 route del local 0.0.0.0/0 dev lo table 200 proto 186":
			return nil, nil
		default:
			return nil, errors.New("неожиданная команда: " + call)
		}
	}
	if err := Rollback(root, id, "тест"); err == nil {
		t.Fatal("ошибка удаления таблицы потеряна")
	}
	joined := strings.Join(calls, "\n")
	for _, expected := range []string{"nft flush chain inet uvg_tproxy uvg_output", "nft flush chain inet uvg_tproxy uvg_prerouting", "ip -N -j -4 rule show", "ip -N -j -4 route show table 200", "ip -4 rule del", "ip -4 route del"} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("rollback прекратился до %s; вызовы:\n%s", expected, joined)
		}
	}
	r, err := readRecord(dir)
	if err != nil || r.State != "rollback_failed" {
		t.Fatalf("не сохранён rollback_failed: %+v, %v", r, err)
	}
}

func TestNumericProtocolOwnershipRejectsBGPName(t *testing.T) {
	if !numericValue(float64(186), 186) || !numericValue("186", 186) {
		t.Fatal("числовой protocol 186 не распознан")
	}
	if numericValue("bgp", 186) || containsOwnedProtocol([]byte(`[{"protocol":"bgp"}]`), 186) {
		t.Fatal("символьный bgp ошибочно принят без ip -N")
	}
	if !containsOwnedProtocol([]byte(`[{"protocol":186}]`), 186) {
		t.Fatal("числовой protocol из ip -N не распознан")
	}
}

func TestNumericRoutePayloadFromIPNIsOwned(t *testing.T) {
	payload := []byte(`[{"type":"2","dst":"default","dev":"lo","protocol":"186","scope":"254"}]`)
	var routes []struct {
		Dst, Dev string
		Type     any
		Protocol any
	}
	if err := json.Unmarshal(payload, &routes); err != nil {
		t.Fatal(err)
	}
	if len(routes) != 1 || !localRouteType(routes[0].Type) || !numericValue(routes[0].Protocol, 186) {
		t.Fatalf("VM payload не распознан как owned local route: %+v", routes)
	}
	if localRouteType("unicast") || localRouteType("1") {
		t.Fatal("не-local тип маршрута принят как owned")
	}
}

func TestRollbackFlushFailureKeepsXrayAndContinuesCleanup(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("проверка процессов и flock выполняется в Linux")
	}
	child := exec.Command("sleep", "30")
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = child.Process.Kill(); _, _ = child.Process.Wait() }()
	identity, err := ProcessIdentity(child.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	id := strings.Repeat("e", 32)
	dir, _ := transactionDir(root, id)
	config, snapshot := []byte("{}"), []byte("{}")
	if err = durable(filepath.Join(dir, "xray.json"), config); err != nil {
		t.Fatal(err)
	}
	if err = durable(filepath.Join(dir, "snapshot.json"), snapshot); err != nil {
		t.Fatal(err)
	}
	manifest := Manifest{Plan: Plan{ID: id}, Xray: identity, ConfigHash: digest(config), SnapshotHash: digest(snapshot)}
	manifestRaw, _ := json.Marshal(manifest)
	if err = durable(filepath.Join(dir, "manifest.json"), manifestRaw); err != nil {
		t.Fatal(err)
	}
	if err = durable(filepath.Join(dir, "manifest.sha256"), []byte(digest(manifestRaw))); err != nil {
		t.Fatal(err)
	}
	if err = saveJSON(filepath.Join(dir, "state.json"), Record{ID: id, State: "pending"}); err != nil {
		t.Fatal(err)
	}
	original := rollbackRun
	defer func() { rollbackRun = original }()
	var calls []string
	rollbackRun = func(name string, args ...string) ([]byte, error) {
		call := name + " " + strings.Join(args, " ")
		calls = append(calls, call)
		switch call {
		case "nft -j list tables":
			return []byte(`{"name":"uvg_tproxy"}`), nil
		case "nft -j list table inet uvg_tproxy":
			return []byte(`{"comment":"uvg:` + id + `"}`), nil
		case "nft delete table inet uvg_tproxy", "nft flush chain inet uvg_tproxy uvg_output", "nft flush chain inet uvg_tproxy uvg_prerouting":
			return nil, errors.New("искусственный сбой nft")
		case "ip -N -j -4 rule show":
			return []byte(`[{"priority":10000,"fwmark":"0x100","fwmask":"0xff00","table":200}]`), nil
		case "ip -4 rule del priority 10000 fwmark 0x100/0xff00 lookup 200":
			return nil, nil
		case "ip -N -j -4 route show table 200":
			return []byte(`[{"dst":"default","type":"local","dev":"lo","protocol":186}]`), nil
		case "ip -4 route del local 0.0.0.0/0 dev lo table 200 proto 186":
			return nil, nil
		default:
			return nil, errors.New("неожиданная команда: " + call)
		}
	}
	if err = Rollback(root, id, "тест unsafe intercept"); err == nil {
		t.Fatal("ошибки nft потеряны")
	}
	if !alive(identity) {
		t.Fatal("Xray завершён при неподтверждённом снятии intercept")
	}
	joined := strings.Join(calls, "\n")
	for _, expected := range []string{"nft flush chain inet uvg_tproxy uvg_output", "nft flush chain inet uvg_tproxy uvg_prerouting"} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("rollback не очистил цепочку %s; вызовы:\n%s", expected, joined)
		}
	}
	for _, forbidden := range []string{"ip -4 rule del", "ip -4 route del"} {
		if strings.Contains(joined, forbidden) {
			t.Fatalf("зависимость снята до доказанного отключения intercept: %s", forbidden)
		}
	}
}

func TestRecoverProcessesAllTransactionsAfterFirstFailure(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("проверка flock выполняется в Linux")
	}
	root := t.TempDir()
	for _, id := range []string{strings.Repeat("1", 32), strings.Repeat("2", 32)} {
		dir, _ := transactionDir(root, id)
		if err := saveJSON(filepath.Join(dir, "state.json"), Record{ID: id, State: "active"}); err != nil {
			t.Fatal(err)
		}
	}
	original := rollbackRun
	defer func() { rollbackRun = original }()
	tableQueries := 0
	rollbackRun = func(name string, args ...string) ([]byte, error) {
		call := name + " " + strings.Join(args, " ")
		switch call {
		case "nft -j list tables":
			tableQueries++
			if tableQueries == 1 {
				return nil, errors.New("первая транзакция повреждена")
			}
			return []byte(`{"nftables":[]}`), nil
		case "ip -N -j -4 rule show", "ip -N -j -4 route show table 200":
			return []byte(`[]`), nil
		default:
			return nil, errors.New("неожиданная команда: " + call)
		}
	}
	if err := Recover(root); err == nil {
		t.Fatal("ошибка первой транзакции потеряна")
	}
	if tableQueries != 2 {
		t.Fatalf("recovery остановился после первой ошибки: запросов=%d", tableQueries)
	}
}
func TestManifestRejectsTamper(t *testing.T) {
	root := t.TempDir()
	id := strings.Repeat("b", 32)
	dir, _ := transactionDir(root, id)
	for _, name := range []string{"snapshot.json", "xray.json"} {
		if err := durable(filepath.Join(dir, name), []byte("{}")); err != nil {
			t.Skip("fsync каталога недоступен на этой ОС")
		}
	}
	m := Manifest{Plan: Plan{ID: id}, SnapshotHash: digest([]byte("{}")), ConfigHash: digest([]byte("{}"))}
	b, _ := json.Marshal(m)
	if err := durable(filepath.Join(dir, "manifest.json"), b); err != nil {
		t.Fatal(err)
	}
	_ = durable(filepath.Join(dir, "manifest.sha256"), []byte(digest(b)))
	if _, _, err := loadManifest(root, id); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(dir, "snapshot.json"), []byte("{\"tampered\":true}"), 0600)
	if _, _, err := loadManifest(root, id); err == nil {
		t.Fatal("Повреждённый snapshot принят")
	}
	if _, err := transactionDir(root, "../escape"); err == nil {
		t.Fatal("Path traversal принят")
	}
}
func TestSessionBoundConfirmation(t *testing.T) {
	m := New(DefaultRoot, "/xray")
	m.plan = &Plan{ID: strings.Repeat("c", 32), Hash: "hash"}
	m.peer = "10.9.1.9"
	m.session = digest([]byte("session1"))
	m.token = "secret"
	m.expires = time.Now().Add(time.Minute)
	if !m.authorized(m.plan.ID, "hash", "secret", m.peer, "session1") {
		t.Fatal("Владелец не подтверждён")
	}
	if m.authorized(m.plan.ID, "hash", "secret", m.peer, "session2") || m.authorized(m.plan.ID, "hash", "secret", "10.9.1.10", "session1") || m.authorized(m.plan.ID, "hash", "wrong", m.peer, "session1") {
		t.Fatal("Чужая сессия/peer/token принят")
	}
}

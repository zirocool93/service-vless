package tunnel

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/zirocool93/service-vless/internal/proxy/xray"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

type Status struct {
	Available bool      `json:"available"`
	State     string    `json:"state"`
	Message   string    `json:"message"`
	ID        string    `json:"transaction_id,omitempty"`
	Deadline  time.Time `json:"deadline,omitempty"`
	Plan      *Plan     `json:"plan,omitempty"`
	Checks    Checks    `json:"checks"`
	ExitIP    string    `json:"exit_ip,omitempty"`
}
type Prepared struct {
	Plan  Plan   `json:"plan"`
	Token string `json:"token"`
}
type Manager struct {
	mu                         sync.Mutex
	root, executable, watchdog string
	plan                       *Plan
	manifest                   Manifest
	token, peer, session       string
	expires                    time.Time
	child                      *exec.Cmd
}

func New(root, executable string) *Manager {
	return &Manager{root: root, executable: executable, watchdog: "/usr/local/lib/ubuntu-vpn-gateway/uvg-watchdog"}
}
func (m *Manager) availability() error {
	if runtime.GOOS != "linux" || os.Geteuid() != 0 {
		return errors.New("Full Tunnel требует Ubuntu, root и systemd")
	}
	if m.root != DefaultRoot {
		return errors.New("Full Tunnel доступен только с production-каталогом данных")
	}
	for _, name := range []string{"nft", "ip", "ss", "systemctl"} {
		if _, e := exec.LookPath(name); e != nil {
			return fmt.Errorf("Не найдена команда %s", name)
		}
	}
	if _, e := os.Stat(m.watchdog); e != nil {
		return errors.New("Независимый watchdog не установлен")
	}
	if _, e := os.Stat("/etc/systemd/system/uvg-watchdog@.service"); e != nil {
		return errors.New("Systemd unit watchdog не установлен")
	}
	if e := verifyHostNamespaceProof(m.root); e != nil {
		return e
	}
	return nil
}
func (m *Manager) statusLocked() Status {
	s := Status{Available: true, State: "disabled", Message: "Full Tunnel выключен"}
	availabilityErr := m.availability()
	if availabilityErr != nil {
		s.Available = false
		s.Message = availabilityErr.Error()
	}
	if m.plan == nil {
		durableStatus, readErr := ReadOnlyStatus(m.root)
		if readErr != nil {
			return Status{Available: false, State: "rollback_failed", Message: fmt.Sprintf("Не удалось прочитать журнал сетевой транзакции: %v", readErr)}
		}
		if durableStatus.State != "disabled" {
			durableStatus.Available = false
			if durableStatus.Message == "" {
				durableStatus.Message = "Незавершённая транзакция требует повторного восстановления"
			}
			return durableStatus
		}
		if availabilityErr != nil {
			durableStatus.Available = false
			durableStatus.Message = availabilityErr.Error()
		}
		return durableStatus
	}
	p := *m.plan
	s.Plan = &p
	s.ID = p.ID
	dir, _ := transactionDir(m.root, p.ID)
	r, e := readRecord(dir)
	if e != nil {
		s.State = "failed"
		s.Message = "Журнал транзакции недоступен"
		return s
	}
	s.State = r.State
	s.Message = r.Message
	s.Deadline = r.Deadline
	s.Checks = r.Checks
	s.ExitIP = r.ExitIP
	return s
}
func (m *Manager) Status() Status { m.mu.Lock(); defer m.mu.Unlock(); return m.statusLocked() }
func randomHex(n int) (string, error) {
	b := make([]byte, n)
	_, e := rand.Read(b)
	return hex.EncodeToString(b), e
}
func unique(v []string) []string {
	seen := map[string]bool{}
	var r []string
	for _, s := range v {
		if !seen[s] {
			seen[s] = true
			r = append(r, s)
		}
	}
	return r
}
func (m *Manager) Prepare(ctx context.Context, node xray.Node, nodeID, peer, session string) (Prepared, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var empty Prepared
	if e := m.availability(); e != nil {
		return empty, e
	}
	if s := m.statusLocked(); !s.Available || s.State == "armed" || s.State == "tracking" || s.State == "sealing" || s.State == "sealed" || s.State == "pending" || s.State == "active" || s.State == "rollback_failed" || s.State == "failed" {
		return empty, errors.New("Сначала отключите текущий Full Tunnel")
	}
	if ip := net.ParseIP(peer); ip == nil {
		return empty, errors.New("Не удалось определить адрес управляющего клиента")
	} else {
		peer = ip.String()
	}
	if session == "" {
		return empty, errors.New("Сессия администратора отсутствует")
	}
	if e := conflicts(); e != nil {
		return empty, e
	}
	addresses, e := net.DefaultResolver.LookupIP(ctx, "ip4", node.Host)
	if e != nil || len(addresses) == 0 {
		return empty, errors.New("IPv4 VPN-сервера не разрешён")
	}
	endpoint := addresses[0].To4().String()
	if endpoint == "<nil>" {
		return empty, errors.New("У VPN-сервера нет IPv4")
	}
	lan4, e := connected("-4")
	if e != nil {
		return empty, e
	}
	lan6, e := connected("-6")
	if e != nil {
		return empty, e
	}
	id, e := randomHex(16)
	if e != nil {
		return empty, e
	}
	token, e := randomHex(32)
	if e != nil {
		return empty, e
	}
	p := Plan{ID: id, NodeID: nodeID, Endpoint: endpoint, Port: node.Port, Peers: unique(append([]string{peer}, sshPeers()...)), LAN4: lan4, LAN6: lan6, DNS: "1.1.1.1", IPv6: "Внешний IPv6 блокируется", LeaseSeconds: 120, SSHPort: 22, UIPort: 8443, FlowPolicy: "После запуска conntrack сохраняются полные 4-tuple текущих SSH/UI-соединений только для management peers"}
	public, _ := json.Marshal(p)
	p.Hash = digest(public)
	config, e := xray.FullTunnel(node, endpoint)
	if e != nil {
		return empty, e
	}
	dir, _ := transactionDir(m.root, id)
	if e = durable(filepath.Join(dir, "xray.json"), config); e != nil {
		return empty, e
	}
	// Секретная конфигурация не выводится в сообщения Xray или API.
	test := exec.CommandContext(ctx, m.executable, "run", "-test", "-config", filepath.Join(dir, "xray.json"))
	test.Stdout = io.Discard
	test.Stderr = io.Discard
	if e = test.Run(); e != nil {
		return empty, errors.New("Xray отклонил Full Tunnel конфигурацию")
	}
	if _, e = command(ctx, nftStageBatch(p), "nft", "--check", "-f", "-"); e != nil {
		return empty, e
	}
	if _, e = command(ctx, nftBatchWithFlows(p, nil), "nft", "--check", "-f", "-"); e != nil {
		return empty, e
	}
	for _, port := range []string{"12345", "12346"} {
		for _, network := range []string{"tcp", "udp"} {
			if network == "tcp" {
				l, err := net.Listen(network, "127.0.0.1:"+port)
				if err != nil {
					return empty, errors.New("Порт TPROXY уже используется")
				}
				l.Close()
			} else {
				l, err := net.ListenPacket(network, "127.0.0.1:"+port)
				if err != nil {
					return empty, errors.New("Порт DNS TPROXY уже используется")
				}
				l.Close()
			}
		}
	}
	snapshot := map[string]json.RawMessage{}
	for name, args := range map[string][]string{"rules4": {"ip", "-j", "-4", "rule"}, "rules6": {"ip", "-j", "-6", "rule"}, "routes4": {"ip", "-j", "-4", "route", "show", "table", "all"}, "routes6": {"ip", "-j", "-6", "route", "show", "table", "all"}, "nft": {"nft", "-j", "list", "ruleset"}} {
		b, err := command(ctx, "", args[0], args[1:]...)
		if err != nil {
			return empty, err
		}
		snapshot[name] = b
	}
	resolver, e := os.ReadFile("/etc/resolv.conf")
	if e != nil {
		return empty, e
	}
	rb, _ := json.Marshal(string(resolver))
	snapshot["resolv_conf"] = rb
	b, _ := json.MarshalIndent(snapshot, "", "  ")
	if e = durable(filepath.Join(dir, "snapshot.json"), b); e != nil {
		return empty, e
	}
	route, e := run("ip", "-j", "-4", "route", "get", endpoint)
	if e != nil {
		return empty, e
	}
	var routes []struct{ Gateway, Dev string }
	if e = json.Unmarshal(route, &routes); e != nil || len(routes) != 1 {
		return empty, errors.New("Не удалось определить исходный путь к endpoint")
	}
	existing, e := run("ip", "-j", "-4", "route", "show", endpoint+"/32")
	if e != nil {
		return empty, e
	}
	var existingRoutes []any
	if e = json.Unmarshal(existing, &existingRoutes); e != nil {
		return empty, e
	}
	owner, e := ProcessIdentity(os.Getpid())
	if e != nil {
		return empty, e
	}
	m.manifest = Manifest{Plan: p, Owner: owner, SnapshotHash: digest(b), ConfigHash: digest(config), Gateway: routes[0].Gateway, Device: routes[0].Dev, HostRoute: len(existingRoutes) == 0}
	if e = saveJSON(filepath.Join(dir, "state.json"), Record{ID: id, State: "prepared", Message: "План подготовлен; сеть не изменена"}); e != nil {
		return empty, e
	}
	m.plan = &p
	m.token = token
	m.peer = peer
	m.session = digest([]byte(session))
	m.expires = time.Now().Add(5 * time.Minute)
	return Prepared{Plan: p, Token: token}, nil
}
func (m *Manager) authorized(id, hash, token, peer, session string) bool {
	return m.plan != nil && id == m.plan.ID && hash == m.plan.Hash && subtle.ConstantTimeCompare([]byte(token), []byte(m.token)) == 1 && token != "" && peer == m.peer && digest([]byte(session)) == m.session
}
func (m *Manager) Apply(ctx context.Context, id, hash, token, peer, session string) (Status, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	release, guardErr := installGuard()
	if guardErr != nil {
		return m.statusLocked(), guardErr
	}
	defer release()
	if !m.authorized(id, hash, token, peer, session) || time.Now().After(m.expires) {
		return m.statusLocked(), errors.New("План устарел или не принадлежит этой сессии")
	}
	if m.statusLocked().State != "prepared" {
		return m.statusLocked(), errors.New("План уже использован")
	}
	if e := conflicts(); e != nil {
		return m.statusLocked(), e
	}
	dir, _ := transactionDir(m.root, id)
	child := exec.Command(m.executable, "run", "-config", filepath.Join(dir, "xray.json"))
	child.Stdout = io.Discard
	child.Stderr = io.Discard
	if e := child.Start(); e != nil {
		return m.statusLocked(), errors.New("Full Tunnel Xray не запущен")
	}
	m.child = child
	keep := false
	defer func() {
		if !keep {
			_ = child.Process.Kill()
		}
	}()
	go func() { _ = child.Wait() }()
	identity, e := ProcessIdentity(child.Process.Pid)
	if e != nil {
		_ = child.Process.Kill()
		return m.statusLocked(), e
	}
	m.manifest.Xray = identity
	b, e := json.MarshalIndent(m.manifest, "", "  ")
	if e != nil {
		return m.statusLocked(), e
	}
	if e = durable(filepath.Join(dir, "manifest.json"), b); e != nil {
		return m.statusLocked(), e
	}
	h := digest(b)
	if e = durable(filepath.Join(dir, "manifest.sha256"), []byte(h)); e != nil {
		return m.statusLocked(), e
	}
	failed := func(cause error) (Status, error) {
		rbErr := Rollback(m.root, id, "Применение не прошло проверку; выполнен аварийный откат")
		_, stopErr := run("systemctl", "stop", "uvg-watchdog@"+id+".service")
		safe, proofErr := interceptDisabled(id)
		if safe {
			_ = child.Process.Kill()
			m.child = nil
		} else {
			// Xray остаётся жив: завершать listener при неподтверждённом снятии
			// перехвата опаснее, чем оставить отдельный процесс до ручного recovery.
			keep = true
			if proofErr == nil {
				proofErr = errors.New("Снятие перехвата не подтверждено; Xray оставлен запущенным")
			}
		}
		return m.statusLocked(), errors.Join(cause, rbErr, stopErr, proofErr)
	}
	if _, e = run("systemctl", "start", "uvg-watchdog@"+id+".service"); e != nil {
		return failed(e)
	}
	var armed Record
	for i := 0; i < 50; i++ {
		raw, err := os.ReadFile(filepath.Join(dir, "armed.json"))
		if err == nil && json.Unmarshal(raw, &armed) == nil && armed.ID == id && armed.Hash == h && armed.State == "armed" {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if armed.ID != id || armed.Hash != h || !armed.Deadline.After(time.Now()) {
		return failed(errors.New("Независимый watchdog не подтвердил вооружение"))
	}
	if _, e = run("systemctl", "is-active", "--quiet", "uvg-watchdog@"+id+".service"); e != nil {
		return failed(errors.New("Watchdog не активен"))
	}
	// Готовность listener проверяется до network mutation.
	for _, port := range []string{"12345", "12346"} {
		ready := false
		for i := 0; i < 30; i++ {
			c, err := net.DialTimeout("tcp", "127.0.0.1:"+port, 100*time.Millisecond)
			if err == nil {
				c.Close()
				ready = true
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		if !ready {
			return failed(errors.New("Xray не открыл transparent listener"))
		}
	}
	flows, seal, e := stageAndSeal(m.root, id, h, m.manifest)
	if e != nil {
		return failed(e)
	}
	unlock, e := fileLock(filepath.Join(dir, "network.lock"))
	if e != nil {
		return failed(e)
	}
	current, checkErr := validateArmed(m.root, id, h, m.manifest.Owner, identity, "sealed")
	if checkErr != nil {
		e = checkErr
	}
	if e == nil {
		ackSeal, ackErr := validateFlowAck(dir, id, h, m.manifest.Plan)
		if ackErr != nil {
			e = ackErr
		} else if ackSeal.FlowHash != seal.FlowHash || current.FlowHash != seal.FlowHash {
			e = errors.New("Flow ACK не совпал с запечатанным management snapshot")
		}
	}
	if e == nil {
		e = conflictsWithTracking(id)
	}
	if e == nil {
		e = applyNetwork(m.manifest, flows)
	}
	if e == nil {
		current.State = "pending"
		current.FlowHash = seal.FlowHash
		current.Message = "Сеть применена; выполняются проверки"
		e = saveJSON(filepath.Join(dir, "state.json"), current)
	}
	unlock()
	if e != nil {
		return failed(e)
	}
	// Проверки используют обычные sockets без proxy и SO_MARK: это реальный Full Tunnel.
	checks, ip, e := probeNetwork(ctx)
	if e != nil {
		return failed(e)
	}
	unlock, e = fileLock(filepath.Join(dir, "network.lock"))
	if e != nil {
		return failed(e)
	}
	r, err := readRecord(dir)
	if err == nil && r.State == "pending" && time.Now().Before(r.Deadline) {
		r.Checks = checks
		r.ExitIP = ip
		r.Message = "Проверки прошли; подтвердите сохранение доступа"
		err = saveJSON(filepath.Join(dir, "state.json"), r)
	} else {
		err = errors.New("Watchdog уже откатил транзакцию")
	}
	unlock()
	if err != nil {
		return failed(err)
	}
	keep = true
	return m.statusLocked(), nil
}
func (m *Manager) Confirm(id, hash, token, peer, session string) (Status, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	release, guardErr := installGuard()
	if guardErr != nil {
		return m.statusLocked(), guardErr
	}
	defer release()
	if !m.authorized(id, hash, token, peer, session) {
		return m.statusLocked(), errors.New("Подтверждение не принадлежит этой сессии")
	}
	dir, _ := transactionDir(m.root, id)
	unlock, e := fileLock(filepath.Join(dir, "network.lock"))
	if e != nil {
		return m.statusLocked(), e
	}
	r, e := readRecord(dir)
	if e != nil {
		unlock()
		return m.statusLocked(), e
	}
	if r.State != "pending" || time.Now().After(r.Deadline) || !r.Checks.TCP || !r.Checks.DNSUDP || !r.Checks.DNSTCP || !r.Checks.IPv6Blocked {
		unlock()
		return m.statusLocked(), errors.New("Подтверждение невозможно: проверки или срок не выполнены")
	}
	seal, sealErr := validateFlowAck(dir, id, r.Hash, m.manifest.Plan)
	if sealErr != nil || seal.FlowHash != r.FlowHash {
		unlock()
		return m.statusLocked(), errors.New("Подтверждение невозможно: management flow seal/ACK повреждён")
	}
	if _, e = run("systemctl", "is-active", "--quiet", "uvg-watchdog@"+id+".service"); e != nil {
		unlock()
		return m.statusLocked(), errors.New("Watchdog не активен")
	}
	if !alive(m.manifest.Owner) || !alive(m.manifest.Xray) {
		unlock()
		return m.statusLocked(), errors.New("Процессы транзакции завершились")
	}
	r.State = "active"
	r.Message = "Full Tunnel включён; watchdog продолжает контроль"
	if e = saveJSON(filepath.Join(dir, "commit.json"), r); e != nil {
		unlock()
		return m.statusLocked(), e
	}
	if e = saveJSON(filepath.Join(dir, "state.json"), r); e != nil {
		unlock()
		return m.statusLocked(), e
	}
	for i := 0; i < 30; i++ {
		var ack Record
		b, err := os.ReadFile(filepath.Join(dir, "commit-ack.json"))
		if err == nil && json.Unmarshal(b, &ack) == nil && ack.ID == id && ack.Hash == r.Hash && ack.FlowHash == r.FlowHash && ack.Deadline.Equal(r.Deadline) && ack.State == "active" {
			m.token = ""
			unlock()
			return m.statusLocked(), nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	unlock()
	rbErr := Rollback(m.root, id, "Watchdog не подтвердил durable commit; выполнен откат")
	_, stopErr := run("systemctl", "stop", "uvg-watchdog@"+id+".service")
	m.token = ""
	return m.statusLocked(), errors.Join(errors.New("Watchdog не подтвердил durable commit"), rbErr, stopErr)
}
func (m *Manager) Disable() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.plan == nil {
		if runtime.GOOS != "linux" || os.Geteuid() != 0 || m.root != DefaultRoot {
			return nil
		}
		return RetryRecovery(m.root)
	}
	id := m.plan.ID
	dir, _ := transactionDir(m.root, id)
	r, e := readRecord(dir)
	if e == nil && r.State == "prepared" {
		r.State = "rolled_back"
		r.Message = "Подготовленный план отменён"
		return saveJSON(filepath.Join(dir, "state.json"), r)
	}
	if e = Rollback(m.root, id, "Full Tunnel отключён; исходная сеть сохранена"); e != nil {
		return e
	}
	_, _ = run("systemctl", "stop", "uvg-watchdog@"+id+".service")
	m.token = ""
	return nil
}

func probeNetwork(ctx context.Context) (Checks, string, error) {
	c := Checks{}
	for _, network := range []string{"udp", "tcp"} {
		for _, kind := range []uint16{1, 28} {
			if e := dnsProbe(ctx, network, kind); e != nil {
				return c, "", fmt.Errorf("DNS %s через туннель не прошёл: %w", network, e)
			}
		}
		if network == "udp" {
			c.DNSUDP = true
		} else {
			c.DNSTCP = true
		}
	}
	d := net.Dialer{Timeout: 5 * time.Second}
	tr := &http.Transport{Proxy: nil, DialContext: func(ctx context.Context, _, address string) (net.Conn, error) {
		return d.DialContext(ctx, "tcp4", address)
	}, DisableKeepAlives: true}
	defer tr.CloseIdleConnections()
	client := http.Client{Transport: tr, Timeout: 10 * time.Second}
	req, _ := http.NewRequestWithContext(ctx, "GET", "https://api.ipify.org", nil)
	resp, e := client.Do(req)
	if e != nil {
		return c, "", errors.New("HTTPS без proxy не прошёл через туннель")
	}
	defer resp.Body.Close()
	body, e := io.ReadAll(io.LimitReader(resp.Body, 128))
	ip := strings.TrimSpace(string(body))
	if e != nil || resp.StatusCode != 200 || net.ParseIP(ip) == nil || net.ParseIP(ip).To4() == nil {
		return c, "", errors.New("Выходной IPv4 не подтверждён")
	}
	c.TCP = true
	// Структурная проверка guard дополняется namespace/VM leak acceptance.
	nft, e := run("nft", "list", "chain", "inet", "uvg_tproxy", "uvg_output")
	if e != nil || !strings.Contains(string(nft), "meta nfproto ipv6 drop") {
		return c, "", errors.New("IPv6 guard не подтверждён")
	}
	c.IPv6Blocked = true
	return c, ip, nil
}

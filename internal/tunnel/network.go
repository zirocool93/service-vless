package tunnel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

func command(ctx context.Context, input string, name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	c := exec.CommandContext(ctx, name, args...)
	if input != "" {
		c.Stdin = strings.NewReader(input)
	}
	b, e := c.CombinedOutput()
	if e != nil {
		return b, fmt.Errorf("Команда %s не выполнена: %s", name, strings.TrimSpace(string(b)))
	}
	return b, nil
}
func run(name string, args ...string) ([]byte, error) {
	return command(context.Background(), "", name, args...)
}

var rollbackRun = run

func tableRoutes() ([]byte, error) {
	b, e := run("ip", "-N", "-j", "-4", "route", "show", "table", "200")
	if e != nil && strings.Contains(string(b), "FIB table does not exist") {
		return []byte("[]"), nil
	}
	return b, e
}
func conflicts() error {
	b, e := run("nft", "-j", "list", "tables")
	if e != nil {
		return e
	}
	if strings.Contains(string(b), `"uvg_tproxy"`) {
		return errors.New("Таблица nft uvg_tproxy уже существует")
	}
	b, e = run("ip", "-N", "-j", "-4", "rule", "show")
	if e != nil {
		return e
	}
	var rules []struct {
		Priority int `json:"priority"`
		FWMark   any `json:"fwmark"`
		Table    any `json:"table"`
	}
	if e = json.Unmarshal(b, &rules); e != nil {
		return e
	}
	for _, r := range rules {
		if r.Priority == 10000 || r.FWMark != nil || fmt.Sprint(r.Table) == "200" {
			return errors.New("Обнаружен конфликт policy rules или fwmark")
		}
	}
	b, e = tableRoutes()
	if e != nil {
		return e
	}
	var routes []any
	if e = json.Unmarshal(b, &routes); e != nil {
		return e
	}
	if len(routes) > 0 {
		return errors.New("Routing table 200 уже используется")
	}
	b, e = run("nft", "list", "ruleset")
	if e != nil {
		return e
	}
	ruleset := strings.ToLower(string(b))
	if strings.Contains(ruleset, "mark") {
		return errors.New("Существующий firewall использует marks; требуется отдельное согласование")
	}
	if strings.Contains(ruleset, "notrack") || strings.Contains(ruleset, "ct zone") || strings.Contains(ruleset, "flowtable") || strings.Contains(ruleset, "flow add") {
		return errors.New("Обнаружены notrack, conntrack zone или flow offload; Full Tunnel не применён")
	}
	return nil
}
func connected(family string) ([]string, error) {
	b, e := run("ip", "-j", family, "route", "show", "table", "main")
	if e != nil {
		return nil, e
	}
	var routes []struct{ Dst, Protocol, Scope string }
	if e = json.Unmarshal(b, &routes); e != nil {
		return nil, e
	}
	var result []string
	for _, r := range routes {
		if r.Protocol == "kernel" && (r.Scope == "link" || family == "-6") && r.Dst != "default" {
			_, n, err := net.ParseCIDR(r.Dst)
			if err == nil {
				result = append(result, n.String())
			}
		}
	}
	return result, nil
}
func sshPeers() []string {
	b, e := run("ss", "-Hnt", "state", "established", "( sport = :22 )")
	if e != nil {
		return nil
	}
	var peers []string
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Fields(line)
		if len(f) < 4 {
			continue
		}
		host, _, e := net.SplitHostPort(f[3])
		if e == nil {
			host = strings.Split(host, "%")[0]
			if ip := net.ParseIP(host); ip != nil {
				peers = append(peers, ip.String())
			}
		}
	}
	return peers
}

func managementFlows(p Plan) ([]ManagementFlow, error) {
	b, e := run("ss", "-Hnt", "state", "all")
	if e != nil {
		return nil, fmt.Errorf("не удалось прочитать текущие management-соединения: %w", e)
	}
	return parseManagementFlows(string(b), p)
}

func parseManagementFlows(output string, p Plan) ([]ManagementFlow, error) {
	peers := make(map[string]bool, len(p.Peers))
	for _, value := range p.Peers {
		if ip := net.ParseIP(value); ip != nil {
			peers[ip.String()] = true
		}
	}
	allowedPorts := map[int]bool{p.SSHPort: true, p.UIPort: true}
	seen := map[ManagementFlow]bool{}
	var flows []ManagementFlow
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 5 || !managementSocketState(fields[0]) {
			continue
		}
		localHost, localPort, localOK := socketAddress(fields[len(fields)-2])
		remoteHost, remotePort, remoteOK := socketAddress(fields[len(fields)-1])
		if !localOK || !remoteOK || !allowedPorts[localPort] || !peers[remoteHost] {
			continue
		}
		flow := ManagementFlow{LocalIP: localHost, RemoteIP: remoteHost, LocalPort: localPort, RemotePort: remotePort}
		if !seen[flow] {
			seen[flow] = true
			flows = append(flows, flow)
		}
	}
	sort.Slice(flows, func(i, j int) bool {
		if flows[i].LocalIP != flows[j].LocalIP {
			return flows[i].LocalIP < flows[j].LocalIP
		}
		if flows[i].RemoteIP != flows[j].RemoteIP {
			return flows[i].RemoteIP < flows[j].RemoteIP
		}
		if flows[i].LocalPort != flows[j].LocalPort {
			return flows[i].LocalPort < flows[j].LocalPort
		}
		return flows[i].RemotePort < flows[j].RemotePort
	})
	return flows, nil
}

func managementSocketState(value string) bool {
	switch strings.ToUpper(strings.TrimSuffix(value, ":")) {
	case "ESTAB", "ESTABLISHED", "SYN-RECV", "FIN-WAIT-1", "FIN-WAIT-2", "CLOSE-WAIT", "CLOSING", "LAST-ACK":
		return true
	default:
		return false
	}
}

func socketAddress(value string) (string, int, bool) {
	host, portText, e := net.SplitHostPort(value)
	if e != nil {
		return "", 0, false
	}
	host = strings.Split(host, "%")[0]
	ip := net.ParseIP(host)
	port, e := strconv.Atoi(portText)
	if e != nil || ip == nil || port < 1 || port > 65535 {
		return "", 0, false
	}
	return ip.String(), port, true
}
func nftBatch(p Plan) string {
	return nftStageBatch(p)
}

func nftStageBatch(p Plan) string {
	set := func(name, kind string, items []string, interval bool) string {
		flags := "constant"
		if interval {
			flags = "interval,constant"
		}
		elements := ""
		if len(items) > 0 {
			elements = " elements = { " + strings.Join(items, ", ") + " };"
		}
		return fmt.Sprintf("set %s { type %s; flags %s;%s }\n", name, kind, flags, elements)
	}
	var peers4, peers6 []string
	for _, v := range p.Peers {
		if net.ParseIP(v).To4() != nil {
			peers4 = append(peers4, v)
		} else {
			peers6 = append(peers6, v)
		}
	}
	b := "table inet uvg_tproxy {\n comment \"uvg:" + p.ID + "\";\n" + set("peers4", "ipv4_addr", peers4, false) + set("peers6", "ipv6_addr", peers6, false) + set("management_flows4", "ipv4_addr . ipv4_addr . inet_service . inet_service", nil, false) + set("management_flows6", "ipv6_addr . ipv6_addr . inet_service . inet_service", nil, false) + set("lan4", "ipv4_addr", p.LAN4, true) + set("lan6", "ipv6_addr", p.LAN6, true)
	b += `chain uvg_output {
 type route hook output priority mangle; policy accept;
 ct state new,established,related counter accept
}
chain uvg_prerouting {
 type filter hook prerouting priority mangle; policy accept;
 ct state new,established,related counter accept
}
}
`
	return b
}

func nftFinalRules(p Plan) (output, prerouting []string) {
	output = []string{
		`meta nfproto ipv6 ip6 daddr { ::1, fe80::/10, ff00::/8 } return`,
		`meta nfproto ipv6 ip6 daddr @lan6 return`,
		`meta nfproto ipv6 ip6 saddr . ip6 daddr . tcp sport . tcp dport @management_flows6 return`,
		`meta nfproto ipv6 ip6 daddr @peers6 tcp sport { 22, 8443 } ct state established,related ct direction reply return`,
		`meta nfproto ipv6 meta l4proto ipv6-icmp ct state related return`,
		`meta nfproto ipv6 drop`,
		`meta nfproto ipv4 meta mark & 0xff00 == 0x0200 return`,
		`ip saddr . ip daddr . tcp sport . tcp dport @management_flows4 return`,
		`ip daddr @peers4 tcp sport { 22, 8443 } ct state established,related ct direction reply return`,
		fmt.Sprintf(`ip daddr %s meta l4proto { tcp, udp } th dport %d return`, p.Endpoint, p.Port),
		`meta nfproto ipv4 meta l4proto { tcp, udp } th dport 53 meta mark set ((meta mark & 0xffff00ff) | 0x0100) return`,
		`ip daddr { 127.0.0.0/8, 169.254.0.0/16, 224.0.0.0/4, 255.255.255.255 } return`,
		`ip daddr @lan4 return`,
		`meta nfproto ipv4 meta l4proto { tcp, udp } meta mark set ((meta mark & 0xffff00ff) | 0x0100) return`,
		`meta nfproto ipv4 ip protocol icmp ct state related return`,
		`drop`,
	}
	prerouting = []string{
		`ct state new,established,related counter`,
		`iifname "lo" meta mark & 0xff00 == 0x0100 meta l4proto { tcp, udp } th dport 53 tproxy ip to 127.0.0.1:12346 accept`,
		`iifname "lo" meta mark & 0xff00 == 0x0100 meta l4proto { tcp, udp } tproxy ip to 127.0.0.1:12345 accept`,
	}
	return
}

func flowElements(flows []ManagementFlow) (v4, v6 []string, err error) {
	seen := map[ManagementFlow]bool{}
	for _, flow := range flows {
		local, remote := net.ParseIP(flow.LocalIP), net.ParseIP(flow.RemoteIP)
		if local == nil || remote == nil || (flow.LocalPort != 22 && flow.LocalPort != 8443) || flow.RemotePort < 1 || flow.RemotePort > 65535 {
			return nil, nil, errors.New("Некорректный management flow")
		}
		if (local.To4() == nil) != (remote.To4() == nil) || seen[flow] {
			return nil, nil, errors.New("Management flow имеет смешанное семейство или дубликат")
		}
		seen[flow] = true
		item := fmt.Sprintf("%s . %s . %d . %d", local, remote, flow.LocalPort, flow.RemotePort)
		if local.To4() != nil {
			v4 = append(v4, item)
		} else {
			v6 = append(v6, item)
		}
	}
	return v4, v6, nil
}

func nftBatchWithFlows(p Plan, flows []ManagementFlow) string {
	var peers4, peers6 []string
	for _, v := range p.Peers {
		if net.ParseIP(v).To4() != nil {
			peers4 = append(peers4, v)
		} else {
			peers6 = append(peers6, v)
		}
	}
	v4, v6, _ := flowElements(flows)
	set := func(name, kind string, items []string, interval bool) string {
		flags := "constant"
		if interval {
			flags = "interval,constant"
		}
		if len(items) == 0 {
			return fmt.Sprintf("set %s { type %s; flags %s; }\n", name, kind, flags)
		}
		return fmt.Sprintf("set %s { type %s; flags %s; elements = { %s }; }\n", name, kind, flags, strings.Join(items, ", "))
	}
	b := "table inet uvg_tproxy {\n comment \"uvg:" + p.ID + "\";\n" + set("peers4", "ipv4_addr", peers4, false) + set("peers6", "ipv6_addr", peers6, false) + set("management_flows4", "ipv4_addr . ipv4_addr . inet_service . inet_service", v4, false) + set("management_flows6", "ipv6_addr . ipv6_addr . inet_service . inet_service", v6, false) + set("lan4", "ipv4_addr", p.LAN4, true) + set("lan6", "ipv6_addr", p.LAN6, true)
	out, pre := nftFinalRules(p)
	b += "chain uvg_output { type route hook output priority mangle; policy accept;\n"
	for _, rule := range out {
		b += " " + rule + "\n"
	}
	b += "}\nchain uvg_prerouting { type filter hook prerouting priority mangle; policy accept;\n"
	for _, rule := range pre {
		b += " " + rule + "\n"
	}
	b += "}\n}\n"
	return b
}

func trackingTableOwned(id string) error {
	b, err := run("nft", "-j", "list", "tables")
	if err != nil {
		return err
	}
	if !strings.Contains(string(b), `"uvg_tproxy"`) {
		return errors.New("Tracking nft table отсутствует")
	}
	table, err := run("nft", "-j", "list", "table", "inet", "uvg_tproxy")
	if err != nil {
		return err
	}
	if !strings.Contains(string(table), `"uvg:`+id+`"`) {
		return errors.New("Владение tracking nft table не подтверждено")
	}
	for _, chain := range []string{"uvg_output", "uvg_prerouting"} {
		b, err := run("nft", "-j", "list", "chain", "inet", "uvg_tproxy", chain)
		if err != nil {
			return fmt.Errorf("Tracking chain %s отсутствует: %w", chain, err)
		}
		if err := validateTrackingChainJSON(b, chain); err != nil {
			return fmt.Errorf("Tracking chain %s содержит неразрешённую структуру: %w", chain, err)
		}
	}
	return nil
}

func validateTrackingChainJSON(data []byte, name string) error {
	var response struct {
		NFTables []map[string]json.RawMessage `json:"nftables"`
	}
	if err := json.Unmarshal(data, &response); err != nil {
		return err
	}
	wantType, wantHook := "route", "output"
	if name == "uvg_prerouting" {
		wantType, wantHook = "filter", "prerouting"
	} else if name != "uvg_output" {
		return errors.New("неизвестная tracking chain")
	}
	chainCount, ruleCount := 0, 0
	for _, item := range response.NFTables {
		if raw, ok := item["chain"]; ok {
			var chain map[string]any
			if err := json.Unmarshal(raw, &chain); err != nil {
				return err
			}
			if chain["name"] != name {
				continue
			}
			chainCount++
			prio, ok := chain["prio"].(float64)
			if !ok || chain["family"] != "inet" || chain["table"] != "uvg_tproxy" || chain["type"] != wantType || chain["hook"] != wantHook || prio != -150 || chain["policy"] != "accept" {
				return errors.New("тип hook, priority или policy не совпал")
			}
		}
		if raw, ok := item["rule"]; ok {
			var rule map[string]any
			if err := json.Unmarshal(raw, &rule); err != nil {
				return err
			}
			if rule["chain"] != name {
				continue
			}
			ruleCount++
			if rule["family"] != "inet" || rule["table"] != "uvg_tproxy" || !validTrackingRuleExpr(rule["expr"]) {
				return errors.New("правило не является только ct state accept с counter")
			}
		}
	}
	if chainCount != 1 || ruleCount != 1 {
		return fmt.Errorf("ожидалась одна цепочка и одно tracking правило, получено %d/%d", chainCount, ruleCount)
	}
	return nil
}

func validTrackingRuleExpr(value any) bool {
	exprs, ok := value.([]any)
	if !ok || len(exprs) != 3 {
		return false
	}
	match, ok := exprs[0].(map[string]any)
	if !ok || len(match) != 1 {
		return false
	}
	body, ok := match["match"].(map[string]any)
	if !ok || len(body) != 3 || body["op"] != "in" {
		return false
	}
	left, ok := body["left"].(map[string]any)
	if !ok || len(left) != 1 {
		return false
	}
	ct, ok := left["ct"].(map[string]any)
	if !ok || len(ct) != 1 || ct["key"] != "state" {
		return false
	}
	states, ok := body["right"].([]any)
	if !ok {
		if set, valid := body["right"].(map[string]any); valid {
			states, ok = set["set"].([]any)
		}
	}
	if !ok || len(states) != 3 {
		return false
	}
	want := map[string]bool{"new": true, "established": true, "related": true}
	for _, state := range states {
		name, ok := state.(string)
		if !ok || !want[name] {
			return false
		}
		delete(want, name)
	}
	if len(want) != 0 {
		return false
	}
	counter, okCounter := exprs[1].(map[string]any)
	_, hasCounter := counter["counter"]
	accept, okAccept := exprs[2].(map[string]any)
	_, hasAccept := accept["accept"]
	return okCounter && hasCounter && len(counter) == 1 && okAccept && hasAccept && len(accept) == 1
}

func installTracking(p Plan) error {
	_, err := command(context.Background(), nftStageBatch(p), "nft", "-f", "-")
	return err
}

func publishIntercept(p Plan, flows []ManagementFlow) error {
	v4, v6, err := flowElements(flows)
	if err != nil {
		return err
	}
	batch := nftPublishBatch(p, v4, v6)
	_, err = command(context.Background(), batch, "nft", "-f", "-")
	return err
}

func nftPublishBatch(p Plan, v4, v6 []string) string {
	lines := []string{"flush chain inet uvg_tproxy uvg_output", "flush chain inet uvg_tproxy uvg_prerouting", "delete set inet uvg_tproxy management_flows4", "delete set inet uvg_tproxy management_flows6"}
	makeSet := func(name, typ string, values []string) string {
		if len(values) == 0 {
			return fmt.Sprintf("add set inet uvg_tproxy %s { type %s; flags constant; }", name, typ)
		}
		return fmt.Sprintf("add set inet uvg_tproxy %s { type %s; flags constant; elements = { %s }; }", name, typ, strings.Join(values, ", "))
	}
	lines = append(lines, makeSet("management_flows4", "ipv4_addr . ipv4_addr . inet_service . inet_service", v4), makeSet("management_flows6", "ipv6_addr . ipv6_addr . inet_service . inet_service", v6))
	out, pre := nftFinalRules(p)
	for _, rule := range out {
		lines = append(lines, "add rule inet uvg_tproxy uvg_output "+rule)
	}
	for _, rule := range pre {
		lines = append(lines, "add rule inet uvg_tproxy uvg_prerouting "+rule)
	}
	return strings.Join(lines, "\n") + "\n"
}

func conflictsWithTracking(id string) error {
	if err := trackingTableOwned(id); err != nil {
		return err
	}
	b, err := run("ip", "-N", "-j", "-4", "rule", "show")
	if err != nil {
		return err
	}
	var rules []struct {
		Priority int `json:"priority"`
		FWMark   any `json:"fwmark"`
		Table    any `json:"table"`
	}
	if err = json.Unmarshal(b, &rules); err != nil {
		return err
	}
	for _, r := range rules {
		if r.Priority == 10000 || r.FWMark != nil || fmt.Sprint(r.Table) == "200" {
			return errors.New("После staging появился конфликт policy rule или fwmark")
		}
	}
	b, err = tableRoutes()
	if err != nil {
		return err
	}
	var routes []any
	if err = json.Unmarshal(b, &routes); err != nil {
		return err
	}
	if len(routes) != 0 {
		return errors.New("После staging routing table 200 занята")
	}
	b, err = run("nft", "list", "ruleset")
	if err != nil {
		return err
	}
	ruleset := strings.ToLower(string(b))
	if strings.Contains(ruleset, "mark") {
		return errors.New("После staging обнаружены firewall marks")
	}
	if strings.Contains(ruleset, "notrack") || strings.Contains(ruleset, "ct zone") || strings.Contains(ruleset, "flowtable") || strings.Contains(ruleset, "flow add") {
		return errors.New("После staging обнаружен notrack, conntrack zone или flow offload")
	}
	return nil
}

func validateArmed(root, id, manifestHash string, owner, xray Identity, states ...string) (Record, error) {
	dir, err := transactionDir(root, id)
	if err != nil {
		return Record{}, err
	}
	r, err := readRecord(dir)
	validState := r.State == "armed"
	for _, state := range states {
		if r.State == state {
			validState = true
		}
	}
	if err != nil || r.ID != id || r.Hash != manifestHash || !validState || !time.Now().Before(r.Deadline) || !alive(owner) || !alive(xray) {
		return Record{}, errors.New("Watchdog lease/процессы не подтверждены")
	}
	return r, nil
}

func stageAndSeal(root, id, manifestHash string, m Manifest) ([]ManagementFlow, FlowSeal, error) {
	dir, err := transactionDir(root, id)
	if err != nil {
		return nil, FlowSeal{}, err
	}
	unlock, err := fileLock(filepath.Join(dir, "network.lock"))
	if err != nil {
		return nil, FlowSeal{}, err
	}
	r, err := validateArmed(root, id, manifestHash, m.Owner, m.Xray)
	if err == nil {
		err = conflicts()
	}
	if err == nil {
		err = installTracking(m.Plan)
	}
	if err == nil {
		err = trackingTableOwned(id)
	}
	if err == nil {
		r.State = "tracking"
		r.Message = "Conntrack tracking включён; фиксируются management-соединения"
		err = saveJSON(filepath.Join(dir, "state.json"), r)
	}
	unlock()
	if err != nil {
		return nil, FlowSeal{}, err
	}
	flows, err := managementFlows(m.Plan)
	if err != nil {
		return nil, FlowSeal{}, err
	}
	unlock, err = fileLock(filepath.Join(dir, "network.lock"))
	if err != nil {
		return nil, FlowSeal{}, err
	}
	current, err := validateArmed(root, id, manifestHash, m.Owner, m.Xray, "tracking")
	if err == nil {
		err = trackingTableOwned(id)
	}
	var seal FlowSeal
	if err == nil {
		current.State = "sealing"
		current.Message = "Сохраняется точный набор management-соединений"
		err = saveJSON(filepath.Join(dir, "state.json"), current)
	}
	if err == nil {
		seal, err = writeFlowSeal(dir, id, manifestHash, m.Plan, flows)
	}
	if err == nil {
		current.State = "sealed"
		current.FlowHash = seal.FlowHash
		current.Message = "Watchdog проверяет management flow seal"
		err = saveJSON(filepath.Join(dir, "state.json"), current)
	}
	unlock()
	if err != nil {
		return nil, FlowSeal{}, err
	}
	seal, err = waitFlowAck(dir, id, manifestHash, m.Plan, r.Deadline)
	if err != nil {
		return nil, FlowSeal{}, err
	}
	return flows, seal, nil
}

func applyNetwork(m Manifest, flows []ManagementFlow) error {
	// Routes/rule follow the durable flow ACK; the interception rules publish last.
	args := []string{"-4", "route", "add", m.Plan.Endpoint + "/32", "dev", m.Device, "proto", "186"}
	if m.Gateway != "" {
		args = append(args, "via", m.Gateway)
	}
	if m.HostRoute {
		if _, e := run("ip", args...); e != nil {
			return e
		}
	}
	if _, e := run("ip", "-4", "route", "add", "local", "0.0.0.0/0", "dev", "lo", "table", "200", "proto", "186"); e != nil {
		return e
	}
	if _, e := run("ip", "-4", "rule", "add", "priority", "10000", "fwmark", "0x100/0xff00", "lookup", "200"); e != nil {
		return e
	}
	return publishIntercept(m.Plan, flows)
}

// Rollback сначала снимает intercept, затем только узнаваемые владельческие rules/routes.
func Rollback(root, id, reason string) error {
	dir, e := transactionDir(root, id)
	if e != nil {
		return e
	}
	unlock, e := fileLock(filepath.Join(dir, "network.lock"))
	if e != nil {
		return e
	}
	defer unlock()
	m, _, manifestErr := loadManifest(root, id)
	r, recordErr := readRecord(dir)
	if recordErr == nil && r.State == "rolled_back" {
		return nil
	}
	var failures []error
	if manifestErr != nil {
		failures = append(failures, fmt.Errorf("manifest недоступен: %w", manifestErr))
	}
	interceptGone := false
	b, listErr := rollbackRun("nft", "-j", "list", "tables")
	if listErr != nil {
		failures = append(failures, listErr)
	} else if !strings.Contains(string(b), `"uvg_tproxy"`) {
		interceptGone = true
	} else {
		table, tableErr := rollbackRun("nft", "-j", "list", "table", "inet", "uvg_tproxy")
		if tableErr != nil {
			failures = append(failures, tableErr)
		} else if !strings.Contains(string(table), `"uvg:`+id+`"`) {
			failures = append(failures, errors.New("Владение nft table не подтверждено; чужая таблица не изменена"))
		} else if _, deleteErr := rollbackRun("nft", "delete", "table", "inet", "uvg_tproxy"); deleteErr == nil {
			interceptGone = true
		} else {
			failures = append(failures, fmt.Errorf("таблица nft не удалена: %w", deleteErr))
			_, outputErr := rollbackRun("nft", "flush", "chain", "inet", "uvg_tproxy", "uvg_output")
			_, preroutingErr := rollbackRun("nft", "flush", "chain", "inet", "uvg_tproxy", "uvg_prerouting")
			if outputErr != nil || preroutingErr != nil {
				var flushFailures []error
				if outputErr != nil {
					flushFailures = append(flushFailures, fmt.Errorf("output chain: %w", outputErr))
				}
				if preroutingErr != nil {
					flushFailures = append(flushFailures, fmt.Errorf("prerouting chain: %w", preroutingErr))
				}
				failures = append(failures, errors.Join(flushFailures...))
			} else {
				interceptGone, _ = interceptDisabled(id)
				if !interceptGone {
					failures = append(failures, errors.New("После очистки не подтверждено отключение перехвата"))
				}
			}
		}
	}
	if !interceptGone {
		failures = append(failures, errors.New("Маршруты и Xray сохранены: отключение intercept не подтверждено"))
	}
	if interceptGone {
		// Exact selectors не могут удалить rule другого priority/mark/table.
		rules, err := rollbackRun("ip", "-N", "-j", "-4", "rule", "show")
		if err != nil {
			failures = append(failures, err)
		} else {
			var list []struct {
				Priority int    `json:"priority"`
				Mark     string `json:"fwmark"`
				Mask     string `json:"fwmask"`
				Table    any    `json:"table"`
			}
			if err = json.Unmarshal(rules, &list); err != nil {
				failures = append(failures, err)
			} else {
				for _, v := range list {
					if v.Priority == 10000 && v.Mark == "0x100" && v.Mask == "0xff00" && fmt.Sprint(v.Table) == "200" {
						if _, err = rollbackRun("ip", "-4", "rule", "del", "priority", "10000", "fwmark", "0x100/0xff00", "lookup", "200"); err != nil {
							failures = append(failures, err)
						}
					}
				}
			}
		}
		routes, err := rollbackRun("ip", "-N", "-j", "-4", "route", "show", "table", "200")
		if err != nil {
			if !strings.Contains(err.Error(), "FIB table does not exist") {
				failures = append(failures, err)
			}
		} else {
			var owned []struct {
				Dst, Dev string
				Type     any
				Protocol any
			}
			if err = json.Unmarshal(routes, &owned); err != nil {
				failures = append(failures, err)
			} else {
				for _, v := range owned {
					if localRouteType(v.Type) && (v.Dst == "default" || v.Dst == "0.0.0.0/0") && v.Dev == "lo" && numericValue(v.Protocol, 186) {
						if _, err = rollbackRun("ip", "-4", "route", "del", "local", "0.0.0.0/0", "dev", "lo", "table", "200", "proto", "186"); err != nil {
							failures = append(failures, err)
						}
					}
				}
			}
		}
		if manifestErr == nil && m.HostRoute {
			_, err = rollbackRun("ip", "-4", "route", "del", m.Plan.Endpoint+"/32", "dev", m.Device, "proto", "186")
			if err != nil {
				current, _ := rollbackRun("ip", "-N", "-j", "-4", "route", "show", m.Plan.Endpoint+"/32")
				if containsOwnedProtocol(current, 186) {
					failures = append(failures, err)
				}
			}
		}
	}
	if interceptGone && manifestErr == nil && alive(m.Xray) {
		if p, err := os.FindProcess(m.Xray.PID); err == nil {
			if err = p.Kill(); err != nil {
				failures = append(failures, err)
			}
		}
	}
	if recordErr != nil {
		r = Record{ID: id}
	}
	if len(failures) == 0 {
		r.State = "rolled_back"
	} else {
		r.State = "rollback_failed"
	}
	r.Message = reason
	if e = saveJSON(filepath.Join(dir, "state.json"), r); e != nil {
		failures = append(failures, e)
	}
	return errors.Join(failures...)
}

func numericValue(v any, expected int) bool {
	switch value := v.(type) {
	case float64:
		return value == float64(expected)
	case string:
		return value == fmt.Sprint(expected)
	default:
		return fmt.Sprint(value) == fmt.Sprint(expected)
	}
}

func localRouteType(v any) bool {
	return v == "local" || numericValue(v, 2) // RTN_LOCAL
}

func containsOwnedProtocol(raw []byte, protocol int) bool {
	var routes []struct {
		Protocol any `json:"protocol"`
	}
	if json.Unmarshal(raw, &routes) != nil {
		return false
	}
	for _, route := range routes {
		if numericValue(route.Protocol, protocol) {
			return true
		}
	}
	return false
}

func interceptDisabled(id string) (bool, error) {
	b, err := rollbackRun("nft", "-j", "list", "tables")
	if err != nil {
		return false, err
	}
	if !strings.Contains(string(b), `"uvg_tproxy"`) {
		return true, nil
	}
	table, err := rollbackRun("nft", "-j", "list", "table", "inet", "uvg_tproxy")
	if err != nil || !strings.Contains(string(table), `"uvg:`+id+`"`) {
		return false, errors.New("Владение nft table не подтверждено")
	}
	for _, name := range []string{"uvg_output", "uvg_prerouting"} {
		chain, chainErr := rollbackRun("nft", "-j", "list", "chain", "inet", "uvg_tproxy", name)
		if chainErr != nil {
			return false, chainErr
		}
		if strings.Contains(string(chain), `"rule":`) {
			return false, nil
		}
	}
	return true, nil
}

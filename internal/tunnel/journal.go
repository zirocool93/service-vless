package tunnel

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const DefaultRoot = "/var/lib/ubuntu-vpn-gateway/tunnel"

var validID = regexp.MustCompile(`^[a-f0-9]{32}$`)

type Identity struct {
	PID   int    `json:"pid"`
	Start string `json:"start"`
	Boot  string `json:"boot"`
}
type Plan struct {
	ID           string   `json:"transaction_id"`
	Hash         string   `json:"hash"`
	NodeID       string   `json:"node_id"`
	Endpoint     string   `json:"endpoint_ip"`
	Port         int      `json:"endpoint_port"`
	Peers        []string `json:"management_peers"`
	LAN4         []string `json:"lan4"`
	LAN6         []string `json:"lan6"`
	DNS          string   `json:"dns"`
	IPv6         string   `json:"ipv6"`
	LeaseSeconds int      `json:"deadline_seconds"`
	SSHPort      int      `json:"ssh_port"`
	UIPort       int      `json:"ui_port"`
	FlowPolicy   string   `json:"management_flow_policy"`
}
type ManagementFlow struct {
	LocalIP    string `json:"local_ip"`
	RemoteIP   string `json:"remote_ip"`
	LocalPort  int    `json:"local_server_port"`
	RemotePort int    `json:"remote_client_port"`
}
type FlowSeal struct {
	ID           string `json:"transaction_id"`
	ManifestHash string `json:"manifest_hash"`
	FlowHash     string `json:"flow_hash"`
}
type FlowAck struct {
	FlowSeal
	State string `json:"state"`
}
type Manifest struct {
	Plan            Plan             `json:"plan"`
	Owner           Identity         `json:"owner"`
	Xray            Identity         `json:"xray"`
	SnapshotHash    string           `json:"snapshot_hash"`
	ConfigHash      string           `json:"config_hash"`
	Gateway         string           `json:"gateway"`
	Device          string           `json:"device"`
	HostRoute       bool             `json:"host_route"`
	ManagementFlows []ManagementFlow `json:"management_flows"`
}
type Record struct {
	ID       string    `json:"transaction_id"`
	Hash     string    `json:"manifest_hash"`
	FlowHash string    `json:"flow_hash,omitempty"`
	State    string    `json:"state"`
	Deadline time.Time `json:"deadline"`
	Message  string    `json:"message"`
	Checks   Checks    `json:"checks"`
	ExitIP   string    `json:"exit_ip,omitempty"`
}
type Checks struct {
	TCP         bool `json:"tcp"`
	DNSUDP      bool `json:"dns_udp"`
	DNSTCP      bool `json:"dns_tcp"`
	IPv6Blocked bool `json:"ipv6_blocked"`
}

func digest(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
func transactionDir(root, id string) (string, error) {
	if !validID.MatchString(id) || !filepath.IsAbs(root) {
		return "", errors.New("Некорректный путь транзакции")
	}
	return filepath.Join(root, id), nil
}
func durable(path string, b []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".write-")
	if err != nil {
		return err
	}
	temp := f.Name()
	defer os.Remove(temp)
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(b)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err = os.Rename(temp, path); err != nil {
		return err
	}
	d, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}
func durableCreate(path string, b []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	if _, err = f.Write(b); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	d, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}
func saveJSON(path string, v any) error {
	b, e := json.MarshalIndent(v, "", "  ")
	if e != nil {
		return e
	}
	return durable(path, b)
}
func readRecord(dir string) (Record, error) {
	var r Record
	b, e := os.ReadFile(filepath.Join(dir, "state.json"))
	if e == nil {
		e = json.Unmarshal(b, &r)
	}
	return r, e
}
func loadManifest(root, id string) (Manifest, string, error) {
	var m Manifest
	dir, e := transactionDir(root, id)
	if e != nil {
		return m, "", e
	}
	b, e := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if e != nil {
		return m, "", e
	}
	h := digest(b)
	stored, e := os.ReadFile(filepath.Join(dir, "manifest.sha256"))
	if e != nil || strings.TrimSpace(string(stored)) != h {
		return m, "", errors.New("Контрольная сумма manifest не совпала")
	}
	if e = json.Unmarshal(b, &m); e != nil {
		return m, "", e
	}
	if m.Plan.ID != id {
		return m, "", errors.New("ID manifest не совпал")
	}
	for name, expected := range map[string]string{"snapshot.json": m.SnapshotHash, "xray.json": m.ConfigHash} {
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil || digest(raw) != expected {
			return m, "", fmt.Errorf("Контрольная сумма %s не совпала", name)
		}
	}
	return m, h, nil
}
func ProcessIdentity(pid int) (Identity, error) {
	var p Identity
	p.PID = pid
	boot, e := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if e != nil {
		return p, e
	}
	p.Boot = strings.TrimSpace(string(boot))
	b, e := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if e != nil {
		return p, e
	}
	end := strings.LastIndex(string(b), ") ")
	if end < 0 {
		return p, errors.New("Некорректный proc stat")
	}
	fields := strings.Fields(string(b)[end+2:])
	if len(fields) < 20 || fields[0] == "Z" {
		return p, errors.New("Процесс завершён")
	}
	p.Start = fields[19]
	if _, e = strconv.ParseUint(p.Start, 10, 64); e != nil {
		return p, e
	}
	return p, nil
}
func alive(p Identity) bool { now, e := ProcessIdentity(p.PID); return e == nil && now == p }

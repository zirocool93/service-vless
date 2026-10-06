package tunnel

import (
	"context"
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

func TestFlowSealWriteOnceAndDetectsTampering(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("проверка durable fsync выполняется в Linux")
	}
	dir := t.TempDir()
	id := strings.Repeat("f", 32)
	p := Plan{ID: id, Peers: []string{"10.9.1.9"}, SSHPort: 22, UIPort: 8443}
	flows := []ManagementFlow{{LocalIP: "10.5.2.70", RemoteIP: "10.9.1.9", LocalPort: 8443, RemotePort: 53123}}
	seal, err := writeFlowSeal(dir, id, "manifest-hash", p, flows)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = loadFlowSeal(dir, id, "manifest-hash", p); err != nil {
		t.Fatal(err)
	}
	if _, err = writeFlowSeal(dir, id, "manifest-hash", p, flows); err == nil {
		t.Fatal("повторная запись immutable flow snapshot принята")
	}
	flowsPath, _, ackPath := flowSnapshotPaths(dir)
	ack := FlowAck{FlowSeal: seal, State: "sealed"}
	if err = saveJSON(ackPath, ack); err != nil {
		t.Fatal(err)
	}
	if _, err = validateFlowAck(dir, id, "manifest-hash", p); err != nil {
		t.Fatal(err)
	}
	ack.FlowHash = "wrong"
	if err = saveJSON(ackPath, ack); err != nil {
		t.Fatal(err)
	}
	if _, err = validateFlowAck(dir, id, "manifest-hash", p); err == nil {
		t.Fatal("ACK с иным flow hash принят")
	}
	if err = os.WriteFile(flowsPath, []byte("[]"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err = loadFlowSeal(dir, id, "manifest-hash", p); err == nil {
		t.Fatal("изменённый flows.json принят")
	}
}

func TestWatchdogWaitsForSealedBarrierAndDetectsPostAckTamper(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("проверка watchdog и durable fsync выполняется в Linux")
	}
	root := t.TempDir()
	id := strings.Repeat("9", 32)
	dir, _ := transactionDir(root, id)
	child := execSleep(t)
	defer func() { _ = child.Process.Kill(); _, _ = child.Process.Wait() }()
	xray, err := ProcessIdentity(child.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	owner, err := ProcessIdentity(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	p := Plan{ID: id, Peers: []string{"10.9.1.9"}, SSHPort: 22, UIPort: 8443}
	snapshot, config := []byte("{}"), []byte("{}")
	if err = durable(filepath.Join(dir, "snapshot.json"), snapshot); err != nil {
		t.Fatal(err)
	}
	if err = durable(filepath.Join(dir, "xray.json"), config); err != nil {
		t.Fatal(err)
	}
	m := Manifest{Plan: p, Owner: owner, Xray: xray, SnapshotHash: digest(snapshot), ConfigHash: digest(config)}
	manifestRaw, _ := json.Marshal(m)
	if err = durable(filepath.Join(dir, "manifest.json"), manifestRaw); err != nil {
		t.Fatal(err)
	}
	h := digest(manifestRaw)
	if err = durable(filepath.Join(dir, "manifest.sha256"), []byte(h)); err != nil {
		t.Fatal(err)
	}
	original := rollbackRun
	defer func() { rollbackRun = original }()
	rollbackRun = func(name string, args ...string) ([]byte, error) {
		call := name + " " + strings.Join(args, " ")
		switch call {
		case "nft -j list tables":
			return []byte(`{"nftables":[]}`), nil
		case "ip -N -j -4 rule show", "ip -N -j -4 route show table 200":
			return []byte(`[]`), nil
		default:
			return nil, errors.New("неожиданный rollback вызов: " + call)
		}
	}
	result := make(chan error, 1)
	monitorCtx, cancelMonitor := context.WithCancel(context.Background())
	monitorDone := make(chan struct{})
	go func() { defer close(monitorDone); result <- Monitor(monitorCtx, root, id) }()
	defer func() {
		cancelMonitor()
		select {
		case <-monitorDone:
		case <-time.After(3 * time.Second):
			t.Error("watchdog test goroutine не завершилась")
		}
	}()
	var armed Record
	for i := 0; i < 100; i++ {
		raw, e := os.ReadFile(filepath.Join(dir, "armed.json"))
		if e == nil && json.Unmarshal(raw, &armed) == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if armed.ID != id || armed.Hash != h || !armed.Deadline.After(time.Now()) {
		t.Fatal("watchdog не durably подтвердил armed")
	}
	state := Record{ID: id, Hash: h, State: "tracking", Deadline: armed.Deadline}
	if err = saveJSON(filepath.Join(dir, "state.json"), state); err != nil {
		t.Fatal(err)
	}
	flowsPath, sealPath, ackPath := flowSnapshotPaths(dir)
	flowsRaw := []byte("[]")
	state.State = "sealing"
	unlock, err := fileLock(filepath.Join(dir, "network.lock"))
	if err != nil {
		t.Fatal(err)
	}
	if err = saveJSON(filepath.Join(dir, "state.json"), state); err != nil {
		unlock()
		t.Fatal(err)
	}
	if err = durableCreate(flowsPath, flowsRaw); err != nil {
		unlock()
		t.Fatal(err)
	}
	unlock()
	// Watchdog должен дождаться barrier при staged-состоянии и частичных файлах.
	time.Sleep(650 * time.Millisecond)
	select {
	case e := <-result:
		t.Fatalf("watchdog откатил до seal barrier: %v", e)
	default:
	}
	seal := FlowSeal{ID: id, ManifestHash: h, FlowHash: digest(flowsRaw)}
	sealRaw, _ := json.MarshalIndent(seal, "", "  ")
	if err = durableCreate(sealPath, sealRaw); err != nil {
		t.Fatal(err)
	}
	state.State = "sealed"
	state.FlowHash = seal.FlowHash
	unlock, err = fileLock(filepath.Join(dir, "network.lock"))
	if err != nil {
		t.Fatal(err)
	}
	if err = saveJSON(filepath.Join(dir, "state.json"), state); err != nil {
		unlock()
		t.Fatal(err)
	}
	unlock()
	for i := 0; i < 100; i++ {
		if _, e := os.Stat(ackPath); e == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if _, err = validateFlowAck(dir, id, h, p); err != nil {
		t.Fatalf("watchdog seal ACK отсутствует: %v", err)
	}
	if err = os.WriteFile(flowsPath, []byte("[{}]"), 0600); err != nil {
		t.Fatal(err)
	}
	select {
	case err = <-result:
		if err != nil {
			t.Fatalf("rollback после seal tamper: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("watchdog не обнаружил tamper после ACK")
	}
	record, err := readRecord(dir)
	if err != nil || record.State != "rolled_back" {
		t.Fatalf("tamper не привёл к durable rollback: %+v, %v", record, err)
	}
}

func TestNFTFinalPublishMutatesExistingHooksAtomically(t *testing.T) {
	p := Plan{ID: strings.Repeat("a", 32), Endpoint: "203.0.113.9", Port: 443}
	v4 := []string{"10.5.2.70 . 10.9.1.9 . 8443 . 53123"}
	batch := nftPublishBatch(p, v4, nil)
	for _, required := range []string{"flush chain inet uvg_tproxy uvg_output", "flush chain inet uvg_tproxy uvg_prerouting", "add set inet uvg_tproxy management_flows4", "add rule inet uvg_tproxy uvg_output ip saddr . ip daddr . tcp sport . tcp dport @management_flows4 return"} {
		if !strings.Contains(batch, required) {
			t.Fatalf("final batch не содержит %q:\n%s", required, batch)
		}
	}
	for _, forbidden := range []string{"delete table", "delete chain", "add chain", "type route hook", "type filter hook"} {
		if strings.Contains(batch, forbidden) {
			t.Fatalf("final batch пересоздаёт hooks/table: %q", forbidden)
		}
	}
}

func execSleep(t *testing.T) *exec.Cmd {
	t.Helper()
	child := exec.Command("sleep", "20")
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	return child
}

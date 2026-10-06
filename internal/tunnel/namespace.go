package tunnel

import (
	"errors"
	"os"
	"path/filepath"
	"time"
)

// NamespaceApply предназначен только для root acceptance в изолированном namespace.
func NamespaceApply(root, id string) error {
	ns, e := os.Readlink("/proc/self/ns/net")
	if e != nil {
		return e
	}
	host, e := os.Readlink("/proc/1/ns/net")
	if e != nil || ns == host {
		return errors.New("Namespace test запрещён в основной сети")
	}
	m, h, e := loadManifest(root, id)
	if e != nil {
		return e
	}
	dir, _ := transactionDir(root, id)
	r, e := readRecord(dir)
	if e != nil || r.ID != id || r.Hash != h || r.State != "armed" || !time.Now().Before(r.Deadline) || !alive(m.Owner) || !alive(m.Xray) {
		return errors.New("Namespace watchdog не вооружён")
	}
	flows, seal, e := stageAndSeal(root, id, h, m)
	if e != nil {
		return errors.Join(e, Rollback(root, id, "Namespace staging/seal не прошёл; выполнен откат"))
	}
	unlock, e := fileLock(filepath.Join(dir, "network.lock"))
	if e != nil {
		return errors.Join(e, Rollback(root, id, "Namespace lock недоступен; выполнен откат"))
	}
	current, e := validateArmed(root, id, h, m.Owner, m.Xray, "sealed")
	if e == nil {
		_, e = validateFlowAck(dir, id, h, m.Plan)
	}
	if e == nil {
		e = conflictsWithTracking(id)
	}
	if e == nil {
		e = applyNetwork(m, flows)
	}
	if e == nil {
		current.State = "pending"
		current.FlowHash = seal.FlowHash
		current.Message = "Namespace сеть применена"
		e = saveJSON(filepath.Join(dir, "state.json"), current)
	}
	unlock()
	if e != nil {
		return errors.Join(e, Rollback(root, id, "Namespace apply не прошёл; выполнен откат"))
	}
	return nil
}

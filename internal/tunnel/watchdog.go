package tunnel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// Monitor независим от backend и остаётся жив после commit.
func Monitor(ctx context.Context, root, id string) error {
	dir, e := transactionDir(root, id)
	if e != nil {
		return e
	}
	m, h, e := loadManifest(root, id)
	if e != nil {
		return e
	}
	if !alive(m.Owner) || !alive(m.Xray) {
		return errors.New("Процессы транзакции не подтверждены")
	}
	r := Record{ID: id, Hash: h, State: "armed", Deadline: time.Now().UTC().Add(120 * time.Second), Message: "Независимый watchdog вооружён"}
	if e = saveJSON(filepath.Join(dir, "state.json"), r); e != nil {
		return e
	}
	if e = saveJSON(filepath.Join(dir, "armed.json"), r); e != nil {
		return e
	}
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	deadline := r.Deadline
	acknowledged := false
	for {
		select {
		case <-ctx.Done():
			return Rollback(root, id, "Watchdog остановлен; сеть восстановлена")
		case <-ticker.C:
			_, hash, err := loadManifest(root, id)
			if err != nil || hash != h {
				return Rollback(root, id, "Manifest повреждён; аварийный откат")
			}
			r, err = readRecord(dir)
			if err != nil {
				return Rollback(root, id, "Журнал повреждён; аварийный откат")
			}
			if r.ID != id || r.Hash != h {
				return Rollback(root, id, "Журнал не совпал с manifest")
			}
			if !r.Deadline.Equal(deadline) {
				return Rollback(root, id, "Deadline изменён; аварийный откат")
			}
			if r.State == "rolled_back" {
				return nil
			}
			if !alive(m.Owner) || !alive(m.Xray) {
				return Rollback(root, id, "Backend или Xray завершился; сеть восстановлена")
			}
			flowsPath, sealPath, ackPath := flowSnapshotPaths(dir)
			_, flowsErr := os.Stat(flowsPath)
			_, sealStatErr := os.Stat(sealPath)
			_, ackStatErr := os.Stat(ackPath)
			flowArtifactsExist := flowsErr == nil || sealStatErr == nil || ackStatErr == nil
		validateFlowState:
			switch r.State {
			case "armed":
				if flowArtifactsExist {
					unlock, lockErr := fileLock(filepath.Join(dir, "network.lock"))
					if lockErr != nil {
						return Rollback(root, id, "Не удалось проверить переход flow state; сеть восстановлена")
					}
					latest, readErr := readRecord(dir)
					unlock()
					if readErr == nil && latest.ID == id && latest.Hash == h && latest.Deadline.Equal(deadline) && latest.State != r.State {
						r = latest
						goto validateFlowState
					}
					return Rollback(root, id, "Flow seal появился до стадии tracking; сеть восстановлена")
				}
			case "tracking":
				if flowArtifactsExist {
					unlock, lockErr := fileLock(filepath.Join(dir, "network.lock"))
					if lockErr != nil {
						return Rollback(root, id, "Не удалось проверить переход flow state; сеть восстановлена")
					}
					latest, readErr := readRecord(dir)
					unlock()
					if readErr == nil && latest.ID == id && latest.Hash == h && latest.Deadline.Equal(deadline) && latest.State != r.State {
						r = latest
						goto validateFlowState
					}
					return Rollback(root, id, "Flow seal появился до стадии sealing; сеть восстановлена")
				}
			case "sealing":
				// Потоковый snapshot пишется поэтапно; переход в sealed — барьер целостности.
			case "sealed", "pending", "active":
				seal, _, sealErr := loadFlowSeal(dir, id, h, m.Plan)
				if sealErr != nil {
					return Rollback(root, id, "Management flow seal повреждён; сеть восстановлена")
				}
				if r.FlowHash != seal.FlowHash {
					return Rollback(root, id, "Journal flow hash не совпал с seal; сеть восстановлена")
				}
				flowAck := FlowAck{FlowSeal: seal, State: "sealed"}
				ackData, ackErr := os.ReadFile(ackPath)
				if errors.Is(ackErr, os.ErrNotExist) && r.State == "sealed" {
					ackRaw, marshalErr := json.MarshalIndent(flowAck, "", "  ")
					if marshalErr != nil {
						return Rollback(root, id, "Flow ACK не сериализован; сеть восстановлена")
					}
					ackErr = durableCreate(ackPath, ackRaw)
					if errors.Is(ackErr, os.ErrExist) || ackErr == nil {
						ackData, ackErr = os.ReadFile(ackPath)
					}
				}
				if ackErr == nil {
					ackErr = json.Unmarshal(ackData, &flowAck)
				}
				if ackErr != nil || flowAck.ID != seal.ID || flowAck.ManifestHash != seal.ManifestHash || flowAck.FlowHash != seal.FlowHash || flowAck.State != "sealed" {
					return Rollback(root, id, "Management flow ACK отсутствует или повреждён; сеть восстановлена")
				}
			default:
				return Rollback(root, id, "Неизвестное состояние flow seal; сеть восстановлена")
			}
			if r.State == "active" {
				var committed Record
				b, err := os.ReadFile(filepath.Join(dir, "commit.json"))
				if err != nil || json.Unmarshal(b, &committed) != nil || committed.ID != id || committed.Hash != h || committed.State != "active" || committed.FlowHash != r.FlowHash || !committed.Deadline.Equal(deadline) {
					return Rollback(root, id, "Durable commit не подтверждён")
				}
				if !acknowledged {
					if err = saveJSON(filepath.Join(dir, "commit-ack.json"), committed); err != nil {
						return Rollback(root, id, "Commit ACK не сохранён")
					}
					acknowledged = true
				}
			}
			if r.State != "active" && time.Now().After(deadline) {
				return Rollback(root, id, "Подтверждение не получено за 120 секунд")
			}
		}
	}
}
func Recover(root string) error {
	if root == DefaultRoot {
		if e := os.MkdirAll(root, 0700); e != nil {
			return e
		}
		if e := invalidateHostNamespaceProof(root); e != nil {
			return fmt.Errorf("старое доказательство host namespace не удалено: %w", e)
		}
	}
	entries, e := os.ReadDir(root)
	if os.IsNotExist(e) && root != DefaultRoot {
		return nil
	}
	if e != nil {
		return e
	}
	type candidate struct {
		id       string
		priority int
	}
	var candidates []candidate
	for _, v := range entries {
		if !v.IsDir() || !validID.MatchString(v.Name()) {
			continue
		}
		r, err := readRecord(filepath.Join(root, v.Name()))
		if err == nil && (r.State == "rolled_back" || r.State == "prepared") {
			continue
		}
		priority := 1
		if err == nil && (r.State == "active" || r.State == "pending" || r.State == "armed" || r.State == "tracking" || r.State == "sealing" || r.State == "sealed" || r.State == "rollback_failed") {
			priority = 0
		}
		candidates = append(candidates, candidate{id: v.Name(), priority: priority})
	}
	sort.SliceStable(candidates, func(i, j int) bool { return candidates[i].priority < candidates[j].priority })
	var failures []error
	for _, item := range candidates {
		if err := Rollback(root, item.id, "Восстановление при запуске; Full Tunnel отключён"); err != nil {
			failures = append(failures, fmt.Errorf("транзакция %s: %w", item.id, err))
		}
	}
	if err := errors.Join(failures...); err != nil {
		return err
	}
	if root == DefaultRoot {
		if err := saveHostNamespaceProof(root); err != nil {
			return fmt.Errorf("host network namespace не подтверждён: %w", err)
		}
	}
	return nil
}

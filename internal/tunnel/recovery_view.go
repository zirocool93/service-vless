package tunnel

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// RecoveryView содержит только чтение самой новой durable-транзакции,
// которой ещё может требоваться recovery. Plan и Manifest заполняются лишь
// после полной проверки целостности; иначе ID остаётся в Status.ID.
type RecoveryView struct {
	Status   Status
	Manifest *Manifest
}

// DiscoverRecoveryView читает журналы, не изменяя файлы или состояние сети.
// Для отсутствующего каталога и при отсутствии незавершённых транзакций found=false.
func DiscoverRecoveryView(root string) (view RecoveryView, found bool, err error) {
	entries, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return RecoveryView{}, false, nil
	}
	if err != nil {
		return RecoveryView{}, false, err
	}

	type candidate struct {
		id       string
		record   Record
		recordOK bool
		deadline time.Time
	}
	var candidates []candidate
	for _, entry := range entries {
		if !entry.IsDir() || !validID.MatchString(entry.Name()) {
			continue
		}
		dir := filepath.Join(root, entry.Name())
		r, readErr := readRecord(dir)
		if readErr == nil {
			if r.ID != entry.Name() {
				readErr = errors.New("ID записи не совпадает с каталогом транзакции")
			} else if r.State == "prepared" || r.State == "rolled_back" {
				continue
			}
		}
		candidates = append(candidates, candidate{
			id:       entry.Name(),
			record:   r,
			recordOK: readErr == nil,
			deadline: r.Deadline,
		})
	}
	if len(candidates) == 0 {
		return RecoveryView{}, false, nil
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].deadline.Equal(candidates[j].deadline) {
			return candidates[i].id > candidates[j].id
		}
		return candidates[i].deadline.After(candidates[j].deadline)
	})

	selected := candidates[0]
	status := Status{Available: true, State: "rollback_failed", ID: selected.id}
	plan := Plan{ID: selected.id}
	manifest, manifestHash, manifestErr := loadManifest(root, selected.id)
	if manifestErr == nil {
		plan = manifest.Plan
		view.Manifest = &manifest
	} else {
		status.Message = fmt.Sprintf("Данные manifest транзакции недоступны; требуется recovery: %v", manifestErr)
	}
	if manifestErr == nil {
		status.Plan = &plan
	}

	switch {
	case !selected.recordOK:
		status.State = "rollback_failed"
		if status.Message == "" {
			status.Message = "Журнал транзакции повреждён или не совпадает с каталогом; требуется recovery"
		}
	case manifestErr != nil:
		status.State = "rollback_failed"
	case selected.record.Hash != "" && view.Manifest != nil:
		if selected.record.Hash != manifestHash {
			status.State = "rollback_failed"
			status.Message = "Hash журнала не совпадает с manifest; требуется recovery"
		} else if selected.record.State == "armed" || selected.record.State == "tracking" || selected.record.State == "sealing" || selected.record.State == "sealed" || selected.record.State == "pending" || selected.record.State == "active" || selected.record.State == "rollback_failed" || selected.record.State == "failed" {
			status.State = selected.record.State
			status.Message = selected.record.Message
		} else {
			status.State = "rollback_failed"
			status.Message = "Состояние журнала неизвестно; требуется recovery"
		}
	default:
		status.State = "rollback_failed"
		status.Message = "Журнал не содержит проверяемую идентичность manifest; требуется recovery"
	}
	status.Deadline = selected.record.Deadline
	status.Checks = selected.record.Checks
	status.ExitIP = selected.record.ExitIP
	return RecoveryView{Status: status, Manifest: view.Manifest}, true, nil
}

// ReadOnlyStatus возвращает durable-статус без изменения журналов и сети.
// Отсутствующий каталог или отсутствие незавершённых транзакций означает disabled.
func ReadOnlyStatus(root string) (Status, error) {
	view, found, err := DiscoverRecoveryView(root)
	if err != nil {
		return Status{}, err
	}
	if found {
		return view.Status, nil
	}
	return Status{Available: true, State: "disabled", Message: "Full Tunnel выключен"}, nil
}

// RetryRecovery запускает существующий идемпотентный recovery для вызывающего
// кода без in-memory плана Manager и возвращает все ошибки восстановления.
func RetryRecovery(root string) error { return Recover(root) }

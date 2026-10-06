package tunnel

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeRecoveryFixture(t *testing.T, root, id, state string, deadline time.Time, corruptManifest, corruptRecord bool) {
	t.Helper()
	dir, err := transactionDir(root, id)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	write := func(name string, data []byte) {
		t.Helper()
		if writeErr := os.WriteFile(filepath.Join(dir, name), data, 0600); writeErr != nil {
			t.Fatal(writeErr)
		}
	}
	if !corruptManifest {
		config, snapshot := []byte(`{"log":{"loglevel":"warning"}}`), []byte(`{"routes":[]}`)
		write("xray.json", config)
		write("snapshot.json", snapshot)
		manifest := Manifest{
			Plan:         Plan{ID: id, Hash: strings.Repeat("a", 64), NodeID: "node", Endpoint: "203.0.113.9", Port: 443},
			SnapshotHash: digest(snapshot),
			ConfigHash:   digest(config),
		}
		manifestRaw, marshalErr := json.Marshal(manifest)
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		write("manifest.json", manifestRaw)
		write("manifest.sha256", []byte(digest(manifestRaw)))
	} else {
		write("manifest.json", []byte(`{"broken":`))
	}
	if corruptRecord {
		write("state.json", []byte(`{"transaction_id":`))
		return
	}
	r := Record{ID: id, State: state, Deadline: deadline, Message: "durable test message"}
	if !corruptManifest {
		manifestRaw, readErr := os.ReadFile(filepath.Join(dir, "manifest.json"))
		if readErr != nil {
			t.Fatal(readErr)
		}
		r.Hash = digest(manifestRaw)
	}
	recordRaw, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	write("state.json", recordRaw)
}

func TestReadOnlyStatusSelectsLatestUnresolvedAndSkipsSafeStates(t *testing.T) {
	root := t.TempDir()
	older := strings.Repeat("1", 32)
	newer := strings.Repeat("2", 32)
	prepared := strings.Repeat("3", 32)
	rolledBack := strings.Repeat("4", 32)
	base := time.Now().UTC().Truncate(time.Second)
	writeRecoveryFixture(t, root, older, "active", base, false, false)
	writeRecoveryFixture(t, root, newer, "pending", base.Add(time.Minute), false, false)
	writeRecoveryFixture(t, root, prepared, "prepared", base.Add(2*time.Minute), false, false)
	writeRecoveryFixture(t, root, rolledBack, "rolled_back", base.Add(3*time.Minute), false, false)

	before, err := os.ReadFile(filepath.Join(root, newer, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	status, err := ReadOnlyStatus(root)
	if err != nil {
		t.Fatal(err)
	}
	if status.State != "pending" || status.ID != newer || status.Plan == nil || status.Plan.Endpoint != "203.0.113.9" {
		t.Fatalf("не выбрана последняя проверенная транзакция: %+v", status)
	}
	if !status.Deadline.Equal(base.Add(time.Minute)) || status.Message != "durable test message" {
		t.Fatalf("статус не собран из durable журнала: %+v", status)
	}
	after, err := os.ReadFile(filepath.Join(root, newer, "state.json"))
	if err != nil || string(before) != string(after) {
		t.Fatalf("read-only просмотр изменил журнал: err=%v", err)
	}
}

func TestReadOnlyStatusKeepsStagedAndFlowSealStatesVisible(t *testing.T) {
	for i, state := range []string{"tracking", "sealing", "sealed"} {
		t.Run(state, func(t *testing.T) {
			root := t.TempDir()
			id := strings.Repeat(string(rune('a'+i)), 32)
			writeRecoveryFixture(t, root, id, state, time.Now().UTC(), false, false)
			status, err := ReadOnlyStatus(root)
			if err != nil {
				t.Fatal(err)
			}
			if status.State != state || status.ID != id || status.Plan == nil {
				t.Fatalf("staged/flow-seal состояние потеряно или скрыто: %+v", status)
			}
		})
	}
}

func TestReadOnlyStatusSurfacesCorruptManifestAsSafeIDOnlyRollbackFailure(t *testing.T) {
	root := t.TempDir()
	id := strings.Repeat("a", 32)
	writeRecoveryFixture(t, root, id, "active", time.Now().UTC(), true, false)

	view, found, err := DiscoverRecoveryView(root)
	if err != nil || !found {
		t.Fatalf("corrupt manifest не обнаружен: found=%v err=%v", found, err)
	}
	if view.Status.State != "rollback_failed" || view.Status.ID != id || view.Status.Plan != nil {
		t.Fatalf("повреждённый manifest не представлен безопасным статусом: %+v", view.Status)
	}
	if view.Manifest != nil || !strings.Contains(view.Status.Message, "manifest") {
		t.Fatalf("непроверенные данные manifest попали в статус: %+v", view)
	}
}

func TestReadOnlyStatusSurfacesCorruptJournalAndMissingRootWithoutWrites(t *testing.T) {
	root := t.TempDir()
	id := strings.Repeat("b", 32)
	writeRecoveryFixture(t, root, id, "active", time.Time{}, false, true)
	statePath := filepath.Join(root, id, "state.json")
	before, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	status, err := ReadOnlyStatus(root)
	if err != nil {
		t.Fatal(err)
	}
	if status.State != "rollback_failed" || status.ID != id || status.Plan == nil || status.Plan.Endpoint != "203.0.113.9" {
		t.Fatalf("повреждённый журнал не виден оператору: %+v", status)
	}
	after, err := os.ReadFile(statePath)
	if err != nil || string(before) != string(after) {
		t.Fatalf("read-only просмотр изменил повреждённый журнал: err=%v", err)
	}

	missing := filepath.Join(t.TempDir(), "missing")
	status, err = ReadOnlyStatus(missing)
	if err != nil || status.State != "disabled" {
		t.Fatalf("отсутствующий root должен дать disabled без записи: status=%+v err=%v", status, err)
	}
	if _, err = os.Stat(missing); !os.IsNotExist(err) {
		t.Fatalf("read-only просмотр создал root: stat err=%v", err)
	}
}

func TestRetryRecoveryDelegatesToExistingRecovery(t *testing.T) {
	root := filepath.Join(t.TempDir(), "missing")
	if err := RetryRecovery(root); err != nil {
		t.Fatalf("Recover должен считать отсутствующий непроизводственный root пустым: %v", err)
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("Recovery view создал root: stat err=%v", err)
	}
}

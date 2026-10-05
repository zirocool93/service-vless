package database

import (
	"context"
	"os"
	"strings"
	"testing"
)

func TestMigrationAndEncryptedRecord(t *testing.T) {
	dir := t.TempDir()
	db, err := OpenWithSecrets(dir, dir+"/keys")
	if err != nil {
		t.Fatal(err)
	}
	var version int
	if err := db.SQL.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != 1 {
		t.Fatalf("версия миграции=%d, err=%v", version, err)
	}
	if err := db.PutRecord(context.Background(), "node", "one", []byte(`{"name":"Тест"}`), []byte(`{"uuid":"секрет"}`)); err != nil {
		t.Fatal(err)
	}
	var raw []byte
	if err := db.SQL.QueryRow("SELECT secret_json FROM records WHERE kind='node' AND id='one'").Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if string(raw) == `{"uuid":"секрет"}` {
		t.Fatal("секрет сохранён открытым текстом")
	}
	_, secret, err := db.GetRecord(context.Background(), "node", "one")
	if err != nil || string(secret) != `{"uuid":"секрет"}` {
		t.Fatalf("расшифрование=%q, err=%v", secret, err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenWithSecrets(dir, dir+"/keys")
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
}

func TestOpenRefusesToReplaceLostMasterKey(t *testing.T) {
	dir := t.TempDir()
	keys := dir + "/keys"
	db, err := OpenWithSecrets(dir, keys)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.PutRecord(context.Background(), "node", "one", []byte("{}"), []byte("secret")); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	_ = db.Close()
	if err := os.Remove(keys + "/master.key"); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenWithSecrets(dir, keys); err == nil || !strings.Contains(err.Error(), "утрачен") {
		t.Fatalf("ожидалась ошибка утраты ключа, получено %v", err)
	}
}

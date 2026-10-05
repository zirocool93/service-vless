package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/ubuntu-vpn-gateway/ubuntu-vpn-gateway/internal/secrets"
	_ "modernc.org/sqlite"
)

type DB struct {
	SQL *sql.DB
	Box *secrets.Box
}

func Open(dataDir string) (*DB, error) {
	return OpenWithSecrets(dataDir, filepath.Join(dataDir, "secrets"))
}
func OpenWithSecrets(dataDir, secretsDir string) (*DB, error) {
	if err := os.MkdirAll(dataDir, 0700); err != nil {
		return nil, err
	}
	if err := os.Chmod(dataDir, 0700); err != nil {
		return nil, err
	}
	databasePath := filepath.Join(dataDir, "gateway.sqlite")
	if _, dbErr := os.Stat(databasePath); dbErr == nil {
		if _, keyErr := os.Stat(filepath.Join(secretsDir, "master.key")); errors.Is(keyErr, os.ErrNotExist) {
			return nil, errors.New("ключ шифрования утрачен; восстановите master.key из резервной копии")
		}
	}
	box, err := secrets.Open(secretsDir)
	if err != nil {
		return nil, err
	}
	path := databasePath
	sqlDB, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	sqlDB.SetMaxOpenConns(1)
	store := &DB{SQL: sqlDB, Box: box}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err = sqlDB.ExecContext(ctx, "PRAGMA foreign_keys=ON"); err != nil {
		sqlDB.Close()
		return nil, err
	}
	if _, err = sqlDB.ExecContext(ctx, "PRAGMA busy_timeout=5000"); err != nil {
		sqlDB.Close()
		return nil, err
	}
	if err = store.migrate(ctx); err != nil {
		sqlDB.Close()
		return nil, err
	}
	if err = os.Chmod(path, 0600); err != nil {
		sqlDB.Close()
		return nil, err
	}
	return store, nil
}

func (d *DB) Close() error { return d.SQL.Close() }

func (d *DB) migrate(ctx context.Context) error {
	tx, err := d.SQL.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var version int
	if err := tx.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return err
	}
	if version > 1 {
		return fmt.Errorf("версия базы данных %d новее приложения", version)
	}
	if version == 0 {
		for _, statement := range []string{
			`CREATE TABLE users (id INTEGER PRIMARY KEY, username TEXT NOT NULL UNIQUE, password_hash TEXT NOT NULL, created_at INTEGER NOT NULL)`,
			`CREATE TABLE sessions (token_hash BLOB PRIMARY KEY, user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE, csrf_hash BLOB NOT NULL, expires_at INTEGER NOT NULL)`,
			`CREATE INDEX sessions_expires_at ON sessions(expires_at)`,
			`CREATE TABLE records (kind TEXT NOT NULL, id TEXT NOT NULL, public_json BLOB NOT NULL, secret_json BLOB NOT NULL, updated_at INTEGER NOT NULL, PRIMARY KEY(kind,id))`,
			`PRAGMA user_version=1`,
		} {
			if _, err := tx.ExecContext(ctx, statement); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

func (d *DB) UserCount(ctx context.Context) (int, error) {
	var n int
	err := d.SQL.QueryRowContext(ctx, "SELECT count(*) FROM users").Scan(&n)
	return n, err
}
func (d *DB) CreateUser(ctx context.Context, username, hash string) error {
	_, err := d.SQL.ExecContext(ctx, "INSERT INTO users(username,password_hash,created_at) VALUES(?,?,?)", username, hash, time.Now().Unix())
	return err
}
func (d *DB) PasswordHash(ctx context.Context, username string) (int64, string, error) {
	var id int64
	var hash string
	err := d.SQL.QueryRowContext(ctx, "SELECT id,password_hash FROM users WHERE username=?", username).Scan(&id, &hash)
	return id, hash, err
}
func (d *DB) CreateSession(ctx context.Context, tokenHash, csrfHash []byte, userID int64, expiry time.Time) error {
	_, err := d.SQL.ExecContext(ctx, "INSERT INTO sessions(token_hash,user_id,csrf_hash,expires_at) VALUES(?,?,?,?)", tokenHash, userID, csrfHash, expiry.Unix())
	return err
}
func (d *DB) Session(ctx context.Context, tokenHash []byte) (string, []byte, error) {
	var username string
	var csrfHash []byte
	err := d.SQL.QueryRowContext(ctx, `SELECT u.username,s.csrf_hash FROM sessions s JOIN users u ON u.id=s.user_id WHERE s.token_hash=? AND s.expires_at>?`, tokenHash, time.Now().Unix()).Scan(&username, &csrfHash)
	return username, csrfHash, err
}
func (d *DB) DeleteSession(ctx context.Context, tokenHash []byte) error {
	_, err := d.SQL.ExecContext(ctx, "DELETE FROM sessions WHERE token_hash=?", tokenHash)
	return err
}
func (d *DB) PutRecord(ctx context.Context, kind, id string, publicJSON, secretJSON []byte) error {
	if kind == "" || id == "" {
		return errors.New("вид и идентификатор обязательны")
	}
	sealed, err := d.Box.SealFor(secretJSON, []byte(kind+"\x00"+id))
	if err != nil {
		return err
	}
	_, err = d.SQL.ExecContext(ctx, `INSERT INTO records(kind,id,public_json,secret_json,updated_at) VALUES(?,?,?,?,?) ON CONFLICT(kind,id) DO UPDATE SET public_json=excluded.public_json,secret_json=excluded.secret_json,updated_at=excluded.updated_at`, kind, id, publicJSON, sealed, time.Now().Unix())
	return err
}
func (d *DB) GetRecord(ctx context.Context, kind, id string) ([]byte, []byte, error) {
	var pub, sec []byte
	err := d.SQL.QueryRowContext(ctx, "SELECT public_json,secret_json FROM records WHERE kind=? AND id=?", kind, id).Scan(&pub, &sec)
	if err != nil {
		return nil, nil, err
	}
	plain, err := d.Box.OpenFor(sec, []byte(kind+"\x00"+id))
	return pub, plain, err
}
func (d *DB) ListRecords(ctx context.Context, kind string) (map[string][]byte, error) {
	rows, err := d.SQL.QueryContext(ctx, "SELECT id,public_json FROM records WHERE kind=? ORDER BY updated_at DESC", kind)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string][]byte)
	for rows.Next() {
		var id string
		var pub []byte
		if err := rows.Scan(&id, &pub); err != nil {
			return nil, err
		}
		out[id] = pub
	}
	return out, rows.Err()
}
func (d *DB) DeleteRecord(ctx context.Context, kind, id string) error {
	_, err := d.SQL.ExecContext(ctx, "DELETE FROM records WHERE kind=? AND id=?", kind, id)
	return err
}

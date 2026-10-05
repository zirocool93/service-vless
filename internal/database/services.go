package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sort"
	"time"

	"github.com/zirocool93/service-vless/internal/services"
)

func (d *DB) UpsertNode(ctx context.Context, n services.Node) error {
	pub, err := json.Marshal(n)
	if err != nil {
		return err
	}
	sec, err := json.Marshal(struct {
		RawConfig string `json:"raw_config"`
	}{n.RawConfig})
	if err != nil {
		return err
	}
	return d.PutRecord(ctx, "node", n.ID, pub, sec)
}
func (d *DB) GetNode(ctx context.Context, id string) (services.Node, error) {
	var n services.Node
	pub, sec, err := d.GetRecord(ctx, "node", id)
	if err != nil {
		return n, err
	}
	if err := json.Unmarshal(pub, &n); err != nil {
		return n, err
	}
	var secret struct {
		RawConfig string `json:"raw_config"`
	}
	if err := json.Unmarshal(sec, &secret); err != nil {
		return n, err
	}
	n.RawConfig = secret.RawConfig
	return n, nil
}
func (d *DB) ListNodes(ctx context.Context) ([]services.Node, error) {
	records, err := d.ListRecords(ctx, "node")
	if err != nil {
		return nil, err
	}
	out := make([]services.Node, 0, len(records))
	for _, pub := range records {
		var n services.Node
		if err := json.Unmarshal(pub, &n); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}
func (d *DB) MarkSubscriptionStale(ctx context.Context, subscriptionID string, seenIDs []string) error {
	seen := make(map[string]bool, len(seenIDs))
	for _, id := range seenIDs {
		seen[id] = true
	}
	nodes, err := d.ListNodes(ctx)
	if err != nil {
		return err
	}
	for _, n := range nodes {
		if n.SubscriptionID != subscriptionID || seen[n.ID] || n.Stale {
			continue
		}
		full, err := d.GetNode(ctx, n.ID)
		if err != nil {
			return err
		}
		full.Stale = true
		if err := d.UpsertNode(ctx, full); err != nil {
			return err
		}
	}
	return nil
}
func (d *DB) UpdateSubscription(ctx context.Context, s services.Subscription) error {
	pub, err := json.Marshal(s)
	if err != nil {
		return err
	}
	sec, err := json.Marshal(struct {
		URL string `json:"url"`
	}{s.URL})
	if err != nil {
		return err
	}
	return d.PutRecord(ctx, "subscription", s.ID, pub, sec)
}
func (d *DB) GetSubscription(ctx context.Context, id string) (services.Subscription, error) {
	var s services.Subscription
	pub, sec, err := d.GetRecord(ctx, "subscription", id)
	if err != nil {
		return s, err
	}
	if err := json.Unmarshal(pub, &s); err != nil {
		return s, err
	}
	var secret struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(sec, &secret); err != nil {
		return s, err
	}
	s.URL = secret.URL
	return s, nil
}
func (d *DB) ListSubscriptions(ctx context.Context) ([]services.Subscription, error) {
	records, err := d.ListRecords(ctx, "subscription")
	if err != nil {
		return nil, err
	}
	out := make([]services.Subscription, 0, len(records))
	for _, pub := range records {
		var s services.Subscription
		if err := json.Unmarshal(pub, &s); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// ApplySubscriptionNodes атомарно сохраняет результат обновления подписки и
// помечает отсутствующие в новом ответе узлы как устаревшие.
func (d *DB) ApplySubscriptionNodes(ctx context.Context, sub services.Subscription, nodes []services.Node) error {
	tx, err := d.SQL.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := time.Now().Unix()
	seen := make(map[string]bool, len(nodes))
	put := func(kind, id string, pub, secret []byte) error {
		sealed, err := d.Box.SealFor(secret, []byte(kind+"\x00"+id))
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO records(kind,id,public_json,secret_json,updated_at) VALUES(?,?,?,?,?) ON CONFLICT(kind,id) DO UPDATE SET public_json=excluded.public_json,secret_json=excluded.secret_json,updated_at=excluded.updated_at`, kind, id, pub, sealed, now)
		return err
	}
	for _, n := range nodes {
		seen[n.ID] = true
		pub, err := json.Marshal(n)
		if err != nil {
			return err
		}
		secret, err := json.Marshal(struct {
			RawConfig string `json:"raw_config"`
		}{n.RawConfig})
		if err != nil {
			return err
		}
		if err := put("node", n.ID, pub, secret); err != nil {
			return err
		}
	}
	rows, err := tx.QueryContext(ctx, "SELECT id,public_json,secret_json FROM records WHERE kind='node'")
	if err != nil {
		return err
	}
	type staleRecord struct {
		id          string
		pub, secret []byte
	}
	var stale []staleRecord
	for rows.Next() {
		var id string
		var pub, sealed []byte
		if err := rows.Scan(&id, &pub, &sealed); err != nil {
			rows.Close()
			return err
		}
		var n services.Node
		if err := json.Unmarshal(pub, &n); err != nil {
			rows.Close()
			return err
		}
		if n.SubscriptionID == sub.ID && !seen[id] && !n.Stale {
			n.Stale = true
			updated, err := json.Marshal(n)
			if err != nil {
				rows.Close()
				return err
			}
			plain, err := d.Box.OpenFor(sealed, []byte("node\x00"+id))
			if err != nil {
				rows.Close()
				return err
			}
			stale = append(stale, staleRecord{id, updated, plain})
		}
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, r := range stale {
		if err := put("node", r.id, r.pub, r.secret); err != nil {
			return err
		}
	}
	pub, err := json.Marshal(sub)
	if err != nil {
		return err
	}
	secret, err := json.Marshal(struct {
		URL string `json:"url"`
	}{sub.URL})
	if err != nil {
		return err
	}
	if err := put("subscription", sub.ID, pub, secret); err != nil {
		return err
	}
	return tx.Commit()
}

var _ services.Store = (*DB)(nil)
var _ = sql.ErrNoRows
var _ = errors.Is

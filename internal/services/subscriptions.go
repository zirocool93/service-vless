package services

import (
	"context"
	"errors"
	"github.com/ubuntu-vpn-gateway/ubuntu-vpn-gateway/internal/proxy/subscription"
	"github.com/ubuntu-vpn-gateway/ubuntu-vpn-gateway/internal/proxy/xray"
	"time"
)

// StartScheduler обновляет включённые подписки по сохранённому интервалу до
// отмены контекста приложения. Повторный вызов создаёт отдельную петлю и
// поэтому должен выполняться один раз из main.
func (s *Service) StartScheduler(ctx context.Context) {
	s.runScheduledRefresh(ctx, time.Now().UTC())
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case now := <-ticker.C:
			s.runScheduledRefresh(ctx, now.UTC())
		case <-ctx.Done():
			return
		}
	}
}

func (s *Service) runScheduledRefresh(ctx context.Context, now time.Time) {
	subs, err := s.store.ListSubscriptions(ctx)
	if err != nil {
		s.publish("subscription.scheduler_error", "Не удалось прочитать список подписок")
		return
	}
	for _, sub := range subs {
		interval, enabled := subscriptionInterval(sub)
		if !enabled || !subscriptionDue(sub.LastUpdateAt, now, interval) {
			continue
		}
		_, err := s.RefreshSubscription(ctx, sub.ID)
		if err != nil {
			s.publish("subscription.refresh_failed", "Не удалось обновить подписку "+sub.Name)
			continue
		}
		s.publish("subscription.refreshed", "Подписка обновлена: "+sub.Name)
	}
}

func subscriptionInterval(sub Subscription) (time.Duration, bool) {
	if !sub.Enabled {
		return 0, false
	}
	switch sub.UpdateInterval {
	case "6h":
		return 6 * time.Hour, true
	case "12h":
		return 12 * time.Hour, true
	case "24h":
		return 24 * time.Hour, true
	default:
		return 0, false
	}
}

func subscriptionDue(last string, now time.Time, interval time.Duration) bool {
	if last == "" {
		return true
	}
	t, err := time.Parse(time.RFC3339, last)
	return err != nil || !now.Before(t.Add(interval))
}

func (s *Service) RefreshSubscription(ctx context.Context, id string) (int, error) {
	s.refreshMu.Lock()
	defer s.refreshMu.Unlock()
	sub, e := s.store.GetSubscription(ctx, id)
	if e != nil {
		return 0, e
	}
	if !sub.Enabled {
		return 0, errors.New("Подписка отключена")
	}
	nodes, e := subscription.Fetch(ctx, sub.URL)
	if e != nil {
		sub.UpdateStatus = "error"
		sub.LastError = "Не удалось обновить подписку"
		sub.LastUpdateAt = time.Now().UTC().Format(time.RFC3339)
		_ = s.store.UpdateSubscription(ctx, sub)
		return 0, e
	}
	return s.syncNodes(ctx, sub, nodes)
}
func (s *Service) syncNodes(ctx context.Context, sub Subscription, nodes []xray.Node) (int, error) {
	batch := make([]Node, 0, len(nodes))
	for _, parsed := range nodes {
		nodeID := stableID(sub.ID, parsed.CanonicalIdentity())
		existing, err := s.store.GetNode(ctx, nodeID)
		n := Node{ID: nodeID, Name: parsed.Name, Kind: "vless", SubscriptionID: sub.ID, RawConfig: parsed.RawURI, Enabled: true}
		if err == nil {
			n.Favorite = existing.Favorite
			n.Enabled = existing.Enabled
			n.LatencyMS = existing.LatencyMS
			n.LastTestAt = existing.LastTestAt
			n.LastError = existing.LastError
		}
		batch = append(batch, n)
	}
	sub.UpdateStatus = "ok"
	sub.LastError = ""
	sub.LastUpdateAt = time.Now().UTC().Format(time.RFC3339)
	if e := s.store.ApplySubscriptionNodes(ctx, sub, batch); e != nil {
		return 0, e
	}
	return len(batch), nil
}

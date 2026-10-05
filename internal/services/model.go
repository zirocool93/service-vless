package services

import (
	"context"
	"errors"
	"github.com/ubuntu-vpn-gateway/ubuntu-vpn-gateway/internal/provider"
)

type Node struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Kind           string `json:"kind"`
	SubscriptionID string `json:"subscription_id,omitempty"`
	RawConfig      string `json:"-"`
	Enabled        bool   `json:"enabled"`
	Favorite       bool   `json:"favorite"`
	Stale          bool   `json:"stale"`
	LatencyMS      int    `json:"latency_ms,omitempty"`
	LastTestAt     string `json:"last_test_at,omitempty"`
	LastError      string `json:"last_error,omitempty"`
}
type Subscription struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	URL            string `json:"-"`
	Enabled        bool   `json:"enabled"`
	UpdateInterval string `json:"update_interval"`
	UpdateStatus   string `json:"update_status"`
	LastError      string `json:"last_error,omitempty"`
	LastUpdateAt   string `json:"last_update_at,omitempty"`
}
type Store interface {
	GetNode(context.Context, string) (Node, error)
	ListNodes(context.Context) ([]Node, error)
	UpsertNode(context.Context, Node) error
	MarkSubscriptionStale(context.Context, string, []string) error
	GetSubscription(context.Context, string) (Subscription, error)
	ListSubscriptions(context.Context) ([]Subscription, error)
	UpdateSubscription(context.Context, Subscription) error
	ApplySubscriptionNodes(context.Context, Subscription, []Node) error
}

var ErrAWGUnavailable = errors.New("Подключение AmneziaWG пока недоступно: требуется отдельный review сети, подтверждённый watchdog и проверка отката на Ubuntu VM")

type Status struct {
	provider.Status
	ActiveNodeID string `json:"active_node_id,omitempty"`
	ExitIP       string `json:"exit_ip,omitempty"`
}

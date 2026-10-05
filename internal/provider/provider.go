// Package provider задаёт жизненный цикл будущего адаптера VPN-провайдера.
// В Phase0 адаптер и системные операции не реализованы.
package provider

import "context"

type State string

const (
	StateDisconnected  State = "disconnected"
	StateConnecting    State = "connecting"
	StateConnected     State = "connected"
	StateDisconnecting State = "disconnecting"
	StateTesting       State = "testing"
	StateSwitching     State = "switching"
	StateFailed        State = "failed"
	StateRollingBack   State = "rolling_back"
)

func (s State) Valid() bool {
	switch s {
	case StateDisconnected, StateConnecting, StateConnected, StateDisconnecting, StateTesting, StateSwitching, StateFailed, StateRollingBack:
		return true
	default:
		return false
	}
}

type Status struct {
	State   State  `json:"state"`
	Message string `json:"message"`
}

// TestResult содержит результаты уровней проверки без синтетических метрик.
type TestResult struct {
	Service  bool   `json:"service"`
	Endpoint bool   `json:"endpoint"`
	Internet bool   `json:"internet"`
	ExitIP   string `json:"exit_ip,omitempty"`
}

// Provider задаёт контракт интеграции с будущим провайдером.
type Provider interface {
	Connect(context.Context, string) error
	Disconnect(context.Context) error
	Status(context.Context) (Status, error)
	Test(context.Context, string) (TestResult, error)
}

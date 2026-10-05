package provider

import "testing"

func TestStateContract(t *testing.T) {
	states := []State{StateDisconnected, StateConnecting, StateConnected, StateDisconnecting, StateTesting, StateSwitching, StateFailed, StateRollingBack}
	for _, state := range states {
		if !state.Valid() {
			t.Errorf("state %q must be valid", state)
		}
	}
	if State("unknown").Valid() {
		t.Fatal("unknown state must be rejected")
	}
}

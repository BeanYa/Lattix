package dispatch

import (
	"testing"

	"lattix/shared"
)

func TestLatencyProbeActiveRequiresPanelAndAgentAcceptance(t *testing.T) {
	boolPtr := func(value bool) *bool { return &value }
	tests := []struct {
		name       string
		panelState string
		reported   *bool
		want       bool
	}{
		{name: "legacy agent without lifecycle provider", want: true},
		{name: "legacy agent while active", panelState: shared.PanelStateActive, want: true},
		{name: "legacy agent while updating", panelState: shared.PanelStateUpdating, want: false},
		{name: "active timeout remains accepted", panelState: shared.PanelStateActive, reported: boolPtr(true), want: true},
		{name: "agent pause wins after panel resumes", panelState: shared.PanelStateActive, reported: boolPtr(false), want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dispatcher := &Dispatcher{}
			if test.panelState != "" {
				dispatcher.PanelLifecycle = func() shared.PanelLifecycleSnapshot {
					return shared.PanelLifecycleSnapshot{State: test.panelState}
				}
			}
			if got := dispatcher.latencyProbeActive(test.reported); got != test.want {
				t.Fatalf("latencyProbeActive() = %v, want %v", got, test.want)
			}
		})
	}
}

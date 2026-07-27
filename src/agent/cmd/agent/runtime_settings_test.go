package main

import (
	"fmt"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"lattix/shared"
)

func TestSelectInitialToken(t *testing.T) {
	panelID, _ := shared.NewPanelInstanceID()
	bootstrap, _ := shared.NewCredential(panelID, 3)
	longTerm, _ := shared.NewCredential(panelID, 3)
	if got := selectInitialToken(longTerm, bootstrap); got != longTerm {
		t.Fatal("same panel and epoch must retain exchanged long-term token")
	}
	rotated, _ := shared.NewCredential(panelID, 4)
	if got := selectInitialToken(longTerm, rotated); got != rotated {
		t.Fatal("new epoch must select install bootstrap token")
	}
	otherPanelID, _ := shared.NewPanelInstanceID()
	rebind, _ := shared.NewCredential(otherPanelID, 1)
	if got := selectInitialToken(longTerm, rebind); got != rebind {
		t.Fatal("different panel must select install bootstrap token")
	}
}

func TestReconnectDelaySpecialCases(t *testing.T) {
	settings := shared.DefaultAgentSettings()
	serviceRestart := reconnectDelay(settings, 1, websocket.CloseServiceRestart)
	if serviceRestart < 200*time.Millisecond || serviceRestart > 500*time.Millisecond {
		t.Fatalf("1012 delay = %s", serviceRestart)
	}
	settings.Reconnect.Mode = shared.ReconnectModeLimited
	settings.Reconnect.MaxRetries = 2
	if got := reconnectDelay(settings, 3, 0); got != 5*time.Minute {
		t.Fatalf("limited probe delay = %s", got)
	}
}

func TestAuthenticationRejectedStopsWrappedCloseError(t *testing.T) {
	err := fmt.Errorf("read hello response: %w", &websocket.CloseError{
		Code: 4001,
		Text: "authentication failed",
	})
	if !authenticationRejected(err) {
		t.Fatal("wrapped 4001 close must stop reconnecting")
	}
	if authenticationRejected(fmt.Errorf("network timeout")) {
		t.Fatal("network errors must remain retryable")
	}
}

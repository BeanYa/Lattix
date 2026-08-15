package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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

func TestAuthenticationRejectedRequiresSentinel(t *testing.T) {
	err := fmt.Errorf("dial panel: %w", errAuthenticationRejected)
	if !authenticationRejected(err) {
		t.Fatal("wrapped explicit rejection must stop reconnecting")
	}
	closeErr := fmt.Errorf("read session response: %w", &websocket.CloseError{
		Code: 4001,
		Text: "authentication failed",
	})
	if authenticationRejected(closeErr) {
		t.Fatal("an untrusted websocket close must remain retryable")
	}
	if authenticationRejected(fmt.Errorf("network timeout")) {
		t.Fatal("network errors must remain retryable")
	}
}

func TestExplicitAuthenticationRejectionRequiresTrustedRPCBody(t *testing.T) {
	id := shared.NewMessageID()
	body, err := json.Marshal(shared.Envelope{
		Kind: shared.KindResponse, Type: shared.TypeSessionOpen,
		RequestID: id, TraceID: id, Code: shared.CodeAuthInvalidCredentials,
		Data: json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	response := &http.Response{
		StatusCode: http.StatusForbidden,
		Header:     http.Header{"X-Lattix-Protocol": []string{"1"}},
		Body:       io.NopCloser(bytes.NewReader(body)),
	}
	if !explicitAuthenticationRejection(response) {
		t.Fatal("trusted structured 403 must be terminal")
	}

	untrusted := &http.Response{
		StatusCode: http.StatusForbidden,
		Header:     http.Header{},
		Body:       io.NopCloser(bytes.NewReader(body)),
	}
	if explicitAuthenticationRejection(untrusted) {
		t.Fatal("proxy-style 403 without protocol header must remain retryable")
	}
}

func TestLifecycleRetryDelayUsesBoundedPanelHint(t *testing.T) {
	observed := shared.PanelLifecycleSnapshot{
		State:       shared.PanelStateFaulted,
		RetryPolicy: shared.RetryPolicy{MinMS: 30000, MaxMS: 90000},
	}
	for i := 0; i < 20; i++ {
		got := lifecycleRetryDelay(observed, time.Second)
		if got < 30*time.Second || got > 90*time.Second {
			t.Fatalf("hinted delay = %s", got)
		}
	}
}

func TestPanelUnavailableUsesLowFrequencyRetry(t *testing.T) {
	response := &http.Response{
		StatusCode: http.StatusServiceUnavailable,
		Body:       io.NopCloser(bytes.NewReader([]byte("proxy unavailable"))),
	}
	if !panelTemporarilyUnavailable(response) {
		t.Fatal("503 must be treated as temporary panel unavailability")
	}
	for i := 0; i < 20; i++ {
		got := unavailableRetryDelay()
		if got < 30*time.Second || got > 90*time.Second {
			t.Fatalf("unavailable delay = %s", got)
		}
	}
}

func TestNormalizePanelWS(t *testing.T) {
	cases := map[string]string{
		"ws://127.0.0.1:8080/api/agent/ws":      "ws://127.0.0.1:8080/api/agent/ws",
		"ws://host//api/agent/ws":               "ws://host/api/agent/ws", // 安装命令尾斜杠拼接产物
		"wss://panel.example.com//api/agent/ws": "wss://panel.example.com/api/agent/ws",
		"ws://host/api/agent/ws/":               "ws://host/api/agent/ws",
		"ws://host/api/agent/ws?x=1#frag":       "ws://host/api/agent/ws",
		"not a url":                             "not a url", // 保留原值，拨号错误贴近输入
	}
	for raw, want := range cases {
		if got := normalizePanelWS(raw); got != want {
			t.Errorf("normalizePanelWS(%q) = %q, want %q", raw, got, want)
		}
	}
}

func TestHandshakeErrorDescription(t *testing.T) {
	if got := handshakeErrorDescription(nil); got != "" {
		t.Errorf("nil response should produce empty description, got %q", got)
	}
	response := &http.Response{
		StatusCode: http.StatusTemporaryRedirect,
		Status:     "307 Temporary Redirect",
		Header:     http.Header{"Location": []string{"/api/agent/ws"}},
	}
	want := "307 Temporary Redirect (Location: /api/agent/ws)"
	if got := handshakeErrorDescription(response); got != want {
		t.Errorf("handshakeErrorDescription = %q, want %q", got, want)
	}
}

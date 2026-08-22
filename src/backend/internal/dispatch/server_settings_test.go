package dispatch

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"lattix/backend/internal/store"
	"lattix/shared"
)

func ptr(v string) *string { return &v }

func TestServerSettingsSyncDeliversChangedDocument(t *testing.T) {
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	serverID, err := st.CreateServer(ctx, store.ServerDraft{Alias: "s1", BootstrapToken: "tok", MachineType: store.MachineTypeDirect, CountryCode: "US", Location: "Test"})
	if err != nil {
		t.Fatal(err)
	}
	requester := &uninstallRequester{wake: make(chan struct{}, 2)}
	dispatcher := New(st, requester)

	// 期望版本：默认 latest（revision 1）→ 无变化时不回文档。
	dispatcher.HandleMessage(serverID, shared.Envelope{
		Kind: shared.KindRequest, Type: shared.TypeServerSettingsSync,
		RequestID: shared.NewMessageID(), TraceID: shared.NewMessageID(),
		Data: json.RawMessage(`{"panel_instance_id":"","applied_revision":1}`),
	})
	// 先触发面板默认变更 → 通知全部。
	if _, err := st.UpdateDefaultServerSettings(ctx, shared.ServerSettings{XrayVersion: ptr("v1.8.24")}); err != nil {
		t.Fatal(err)
	}
	dispatcher.NotifyServerSettingsChanged(ctx, 0, 2)
	// 服务器覆盖 → 仅通知该服务器。
	if err := st.UpdateServerCustomSettings(ctx, serverID, &shared.ServerSettings{XrayVersion: ptr("v1.8.10")}); err != nil {
		t.Fatal(err)
	}
	dispatcher.NotifyServerSettingsChanged(ctx, serverID, 3)

	// 模拟 agent 拉取：applied_revision=1 < effective 3 → 回文档。
	select {
	case envelope := <-requester.wake:
		_ = envelope
	case <-time.After(time.Second):
		t.Fatal("notify not delivered")
	}
	dispatcher.HandleMessage(serverID, shared.Envelope{
		Kind: shared.KindRequest, Type: shared.TypeServerSettingsSync,
		RequestID: shared.NewMessageID(), TraceID: shared.NewMessageID(),
		Data: json.RawMessage(`{"panel_instance_id":"x","applied_revision":1}`),
	})
	requester.mu.Lock()
	sent := append([]shared.Envelope(nil), requester.sent...)
	requester.mu.Unlock()
	var last shared.Envelope
	for _, envelope := range sent {
		if envelope.Kind == shared.KindResponse && envelope.Type == shared.TypeServerSettingsSync {
			last = envelope
		}
	}
	if last.Type != shared.TypeServerSettingsSync || last.Code != shared.CodeOK {
		t.Fatalf("no sync response, sent=%+v", sent)
	}
	var result shared.ServerSettingsSyncResult
	if err := json.Unmarshal(last.Data, &result); err != nil {
		t.Fatal(err)
	}
	if !result.Changed || result.Settings == nil {
		t.Fatalf("result = %+v, want changed document", result)
	}
	if result.Settings.Revision != 3 {
		t.Fatalf("revision = %d, want 3", result.Settings.Revision)
	}
	if result.Settings.Server.XrayVersion == nil || *result.Settings.Server.XrayVersion != "v1.8.10" {
		t.Fatalf("effective version = %v, want v1.8.10", result.Settings.Server.XrayVersion)
	}
	srv, err := st.ServerByID(ctx, serverID)
	if err != nil {
		t.Fatal(err)
	}
	if srv.ServerSettingsRevision != 1 {
		t.Fatalf("reported revision = %d, want 1", srv.ServerSettingsRevision)
	}
}

func TestServerSettingsSyncNoChangeWhenUpToDate(t *testing.T) {
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	serverID, err := st.CreateServer(context.Background(), store.ServerDraft{Alias: "s1", BootstrapToken: "tok", MachineType: store.MachineTypeDirect, CountryCode: "US", Location: "Test"})
	if err != nil {
		t.Fatal(err)
	}
	requester := &uninstallRequester{wake: make(chan struct{}, 2)}
	dispatcher := New(st, requester)
	dispatcher.HandleMessage(serverID, shared.Envelope{
		Kind: shared.KindRequest, Type: shared.TypeServerSettingsSync,
		RequestID: shared.NewMessageID(), TraceID: shared.NewMessageID(),
		Data: json.RawMessage(`{"panel_instance_id":"","applied_revision":1}`),
	})
	requester.mu.Lock()
	var result shared.ServerSettingsSyncResult
	for _, envelope := range requester.sent {
		if envelope.Kind == shared.KindResponse && envelope.Type == shared.TypeServerSettingsSync {
			_ = json.Unmarshal(envelope.Data, &result)
		}
	}
	requester.mu.Unlock()
	if result.Changed {
		t.Fatalf("expected no change, got %+v", result)
	}
}

func TestServerSettingsSyncInvalidPayload(t *testing.T) {
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	requester := &uninstallRequester{wake: make(chan struct{}, 2)}
	dispatcher := New(st, requester)
	dispatcher.HandleMessage(1, shared.Envelope{
		Kind: shared.KindRequest, Type: shared.TypeServerSettingsSync,
		RequestID: shared.NewMessageID(), TraceID: shared.NewMessageID(),
		Data: json.RawMessage(`not-json`),
	})
	requester.mu.Lock()
	defer requester.mu.Unlock()
	found := false
	for _, envelope := range requester.sent {
		if envelope.Kind == shared.KindResponse && envelope.Type == shared.TypeServerSettingsSync && envelope.Code == shared.CodeInvalidArgument {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected INVALID_ARGUMENT response, sent=%+v", requester.sent)
	}
}

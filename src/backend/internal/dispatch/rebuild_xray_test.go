package dispatch

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"lattix/backend/internal/store"
	"lattix/shared"
)

// TestRebuildXraySyncDeliversResult 验证 xray.rebuild 同步回执流转：
// agent 回执重建结果 → waiter 投递 → 调用方拿到结果，命令照常 acked 落库。
func TestRebuildXraySyncDeliversResult(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	serverID, err := st.CreateServer(ctx, store.ServerDraft{Alias: "agent", Address: "agent.test", BootstrapToken: "token", MachineType: store.MachineTypeDirect, CountryCode: "US", Location: "Test"})
	if err != nil {
		t.Fatal(err)
	}
	requester := &uninstallRequester{wake: make(chan struct{}, 2)}
	dispatcher := New(st, requester)
	go func() {
		select {
		case <-requester.wake:
		case <-ctx.Done():
			return
		}
		requester.mu.Lock()
		request := requester.sent[len(requester.sent)-1]
		requester.mu.Unlock()
		dispatcher.HandleMessage(serverID, shared.Envelope{
			Kind: shared.KindResponse, Type: request.Type,
			RequestID: request.RequestID, TraceID: request.TraceID,
			Code: shared.CodeOK,
			Data: json.RawMessage(`{"rebuild":{"rebuilt_inbounds":[{"tag":"node_1","port":12345,"kind":"vless"}],"rebuilt_pieces":["forward/3"],"rolled_back":false}}`),
		})
	}()

	result, err := dispatcher.RebuildXraySync(ctx, serverID, shared.RebuildXrayPayload{
		ExpectedInboundTags: []string{"node_1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.RebuiltInbounds) != 1 || result.RebuiltInbounds[0].Tag != "node_1" ||
		result.RebuiltInbounds[0].Port != 12345 || len(result.RebuiltPieces) != 1 ||
		result.RebuiltPieces[0] != "forward/3" || result.RolledBack {
		t.Fatalf("result = %+v", result)
	}
	requester.mu.Lock()
	requestID := requester.sent[0].RequestID
	requester.mu.Unlock()
	command, err := st.CommandByRequestID(ctx, requestID)
	if err != nil {
		t.Fatal(err)
	}
	if command.Status != store.CommandStatusAcked || command.Type != shared.TypeRebuildXray {
		t.Fatalf("命令 = %s/%s", command.Status, command.Type)
	}
}

// TestRebuildXraySyncFailureReturnsError 验证 agent 回执失败时同步调用返回错误且命令落 failed。
func TestRebuildXraySyncFailureReturnsError(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	serverID, err := st.CreateServer(ctx, store.ServerDraft{Alias: "agent", Address: "agent.test", BootstrapToken: "token", MachineType: store.MachineTypeDirect, CountryCode: "US", Location: "Test"})
	if err != nil {
		t.Fatal(err)
	}
	requester := &uninstallRequester{wake: make(chan struct{}, 2)}
	dispatcher := New(st, requester)
	go func() {
		select {
		case <-requester.wake:
		case <-ctx.Done():
			return
		}
		requester.mu.Lock()
		request := requester.sent[len(requester.sent)-1]
		requester.mu.Unlock()
		dispatcher.HandleMessage(serverID, shared.Envelope{
			Kind: shared.KindResponse, Type: request.Type,
			RequestID: request.RequestID, TraceID: request.TraceID,
			Code: shared.CodeInternalError, Message: "重建失败：自检缺失 node_2（已恢复备份 xray.json 并重启）",
			Data: json.RawMessage(`{"rebuild":{"rebuilt_inbounds":[],"rebuilt_pieces":[],"rolled_back":true}}`),
		})
	}()

	result, err := dispatcher.RebuildXraySync(ctx, serverID, shared.RebuildXrayPayload{})
	if err == nil {
		t.Fatal("失败回执应返回错误")
	}
	if result == nil || !result.RolledBack {
		t.Fatalf("失败回执应携带 RolledBack=true 的结果，实际 %+v", result)
	}
	requester.mu.Lock()
	requestID := requester.sent[0].RequestID
	requester.mu.Unlock()
	command, err := st.CommandByRequestID(ctx, requestID)
	if err != nil {
		t.Fatal(err)
	}
	if command.Status != store.CommandStatusFailed {
		t.Fatalf("命令状态 = %s，期望 failed", command.Status)
	}
}

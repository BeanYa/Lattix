package dispatch

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"lattix/backend/internal/store"
	"lattix/shared"
)

// TestCleanupXraySyncDeliversResult 验证 xray.cleanup 同步回执流转：
// agent 回执差异 → waiter 投递 → 调用方拿到结果，命令照常 acked 落库。
func TestCleanupXraySyncDeliversResult(t *testing.T) {
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
	dispatcher := New(st, requester, Options{}, Events{})
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
			Data: json.RawMessage(`{"cleanup":{"removed_inbounds":[{"tag":"chainfwd_99","port":20099}],"removed_pieces":["bridge/9"]}}`),
		})
	}()

	result, err := dispatcher.CleanupXraySync(ctx, serverID, shared.CleanupXrayPayload{
		DryRun: true, ExpectedInboundTags: []string{"node_1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.RemovedInbounds) != 1 || result.RemovedInbounds[0].Tag != "chainfwd_99" ||
		result.RemovedInbounds[0].Port != 20099 || len(result.RemovedPieces) != 1 || result.RemovedPieces[0] != "bridge/9" {
		t.Fatalf("result = %+v", result)
	}
	requester.mu.Lock()
	requestID := requester.sent[0].RequestID
	requester.mu.Unlock()
	command, err := st.CommandByRequestID(ctx, requestID)
	if err != nil {
		t.Fatal(err)
	}
	if command.Status != store.CommandStatusAcked {
		t.Fatalf("命令状态 = %s，期望 acked", command.Status)
	}
	if command.Type != shared.TypeCleanupXray {
		t.Fatalf("命令类型 = %s", command.Type)
	}
}

// TestCleanupXraySyncFailureReturnsError 验证 agent 回执失败时同步调用返回错误且命令落 failed。
func TestCleanupXraySyncFailureReturnsError(t *testing.T) {
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
	dispatcher := New(st, requester, Options{}, Events{})
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
			Code: shared.CodeInternalError, Message: "xray 配置校验失败",
			Data: json.RawMessage(`{}`),
		})
	}()

	_, err = dispatcher.CleanupXraySync(ctx, serverID, shared.CleanupXrayPayload{DryRun: true})
	if err == nil {
		t.Fatal("失败回执应返回错误")
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

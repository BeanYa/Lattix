package dispatch

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"lattix/backend/internal/logging"
	"lattix/backend/internal/store"
	"lattix/shared"
)

// TestCommandFailedDetailCarriesEndpointAndChainContext 验证 shared-endpoint 命令失败
// 的操作日志 Detail 携带 endpoint_id/chain_ids/attempts/error，便于按链路定位。
func TestCommandFailedDetailCarriesEndpointAndChainContext(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	serverID, _ := st.CreateServer(ctx, store.ServerDraft{Alias: "entry", Address: "entry.test", BootstrapToken: "token", MachineType: store.MachineTypeDirect, CountryCode: "US"})
	config := json.RawMessage(`{"protocol":"vless","port":443,"template":{}}`)
	endpoint, _, err := st.EnsureSharedEndpoint(ctx, serverID, shared.ProtocolVLESS, 443, "profile", config)
	if err != nil {
		t.Fatal(err)
	}
	createDirectSharedChain(t, st, serverID, endpoint.ID, "detail-chain")

	opLog, err := logging.OpenOperationStore(filepath.Join(t.TempDir(), "op.db"), 100)
	if err != nil {
		t.Fatal(err)
	}
	defer opLog.Close()
	d := New(st, &fakeRequester{online: map[int64]bool{serverID: true}})
	d.OperationLog = opLog

	if _, err := d.Enqueue(ctx, serverID, shared.TypeApplySharedEndpoint,
		shared.ApplySharedEndpointPayload{EndpointID: endpoint.ID}); err != nil {
		t.Fatal(err)
	}
	cmds, err := st.CommandsByType(ctx, shared.TypeApplySharedEndpoint)
	if err != nil || len(cmds) != 1 {
		t.Fatalf("apply 命令 = %d 条, err=%v", len(cmds), err)
	}
	cmd := cmds[0]
	d.handleCommandResponse(serverID, shared.Envelope{
		Kind: shared.KindResponse, Type: shared.TypeApplySharedEndpoint,
		RequestID: cmd.RequestID, TraceID: cmd.TraceID,
		Code:    shared.CodeInternalError,
		Message: "重启失败(exit status 1)，已回滚配置",
		Data:    mustMarshalEndpointResult(t, endpoint.ID),
	})

	items, _, err := opLog.List(ctx, logging.OperationFilter{Category: logging.CategoryCommand}, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Action != "command.failed" {
		t.Fatalf("operation entries = %+v", items)
	}
	detail := items[0].Detail
	for _, want := range []string{
		`"command_id"`, `"type":"shared-endpoint.apply"`, `"hop_id":0`,
		`"endpoint_id"`, `"chain_ids"`, `"attempts":1`, `"error"`, "重启失败",
	} {
		if !strings.Contains(detail, want) {
			t.Fatalf("Detail 缺少 %s: %s", want, detail)
		}
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(detail), &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed["endpoint_id"].(float64) != float64(endpoint.ID) {
		t.Fatalf("endpoint_id = %v", parsed["endpoint_id"])
	}
	chainIDs, _ := st.ChainIDsByEndpoint(ctx, endpoint.ID)
	if len(chainIDs) != 1 || parsed["chain_ids"].([]any)[0].(float64) != float64(chainIDs[0]) {
		t.Fatalf("chain_ids = %v", parsed["chain_ids"])
	}
}

func mustMarshalEndpointResult(t *testing.T, endpointID int64) []byte {
	t.Helper()
	b, err := json.Marshal(shared.ApplyResultPayload{EndpointID: endpointID})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

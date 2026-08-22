package panel

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"lattix/backend/internal/dispatch"
	"lattix/backend/internal/store"
	"lattix/shared"
)

// rebuildRecordingRequester 记录是否在线，并捕获下发的 xray.rebuild 载荷；
// 同时模拟 agent：同步回执重建结果（RebuildXraySync 等待回执，无回执会重试后报错）。
type rebuildRecordingRequester struct {
	settingsRequester
	disp           *dispatch.Dispatcher
	rebuildPayload shared.RebuildXrayPayload
}

func (f *rebuildRecordingRequester) Send(ctx context.Context, serverID int64, env shared.Envelope) error {
	if env.Type == shared.TypeRebuildXray {
		var p shared.RebuildXrayPayload
		if err := json.Unmarshal(env.Data, &p); err == nil {
			f.rebuildPayload = p
		}
		if f.disp != nil {
			f.disp.HandleMessage(serverID, shared.Envelope{
				Kind: shared.KindResponse, Type: env.Type,
				RequestID: env.RequestID, TraceID: env.TraceID,
				Code: shared.CodeOK,
				Data: json.RawMessage(`{"rebuild":{"rebuilt_inbounds":[{"tag":"node_1","port":10001,"kind":"dokodemo-door"}],"rebuilt_pieces":[],"rolled_back":false}}`),
			})
		}
	}
	return nil
}

// seedRebuildServer 构造带 1 个活跃节点 + 1 个非活跃节点的服务器。
func seedRebuildServer(t *testing.T) (*Server, *rebuildRecordingRequester, *store.Store, int64) {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	requester := &rebuildRecordingRequester{settingsRequester: settingsRequester{online: map[int64]bool{}}}
	dispatcher := dispatch.New(st, requester)
	requester.disp = dispatcher
	serverAPI := &Server{st: st, disp: dispatcher, req: requester}
	serverID, err := st.CreateServer(ctx, store.ServerDraft{Alias: "s1", Address: "s1.test", BootstrapToken: "tok", MachineType: store.MachineTypeDirect, CountryCode: "US", Location: "Test"})
	if err != nil {
		t.Fatal(err)
	}
	requester.online[serverID] = true
	tmpl := json.RawMessage(`{
		"protocol": "dokodemo-door", "listen": "0.0.0.0", "port": 10001,
		"tag": "{{TAG}}", "settings": {"address": "1.1.1.1", "port": 53, "network": "tcp,udp"}
	}`)
	port := 10001
	nodeID, err := st.InsertNode(ctx, "", serverID, shared.ProtocolDokodemo, &port, tmpl)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetNodeActive(ctx, nodeID, json.RawMessage("{}")); err != nil {
		t.Fatal(err)
	}
	return serverAPI, requester, st, serverID
}

func TestHandleRebuildXrayOffline(t *testing.T) {
	serverAPI, requester, _, serverID := seedRebuildServer(t)
	requester.online[serverID] = false
	rec := httptest.NewRecorder()
	serverAPI.handleRebuildXray(rec, httptest.NewRequest(http.MethodPost, "/api/server/rebuild-xray",
		strings.NewReader(`{"server_id":1}`)))
	env := decodeRPC(t, rec)
	if env.Code != shared.CodeConflict {
		t.Fatalf("离线 code = %q，期望 CONFLICT，body = %s", env.Code, rec.Body.String())
	}
}

func TestHandleRebuildXrayCollectsActiveNodes(t *testing.T) {
	serverAPI, requester, st, _ := seedRebuildServer(t)
	ctx := context.Background()
	// 第二个非活跃节点（不进入重建清单）。
	tmpl := json.RawMessage(`{"protocol":"dokodemo-door","listen":"0.0.0.0","port":10002,
		"tag":"{{TAG}}","settings":{"address":"1.1.1.1","port":53,"network":"tcp,udp"}}`)
	port := 10002
	nodeID, err := st.InsertNode(ctx, "", 1, shared.ProtocolDokodemo, &port, tmpl)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetNodeFailed(ctx, nodeID, "boom"); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	serverAPI.handleRebuildXray(rec, httptest.NewRequest(http.MethodPost, "/api/server/rebuild-xray",
		strings.NewReader(`{"server_id":1}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("状态码 = %d，body = %s", rec.Code, rec.Body.String())
	}
	if len(requester.rebuildPayload.Nodes) != 1 || requester.rebuildPayload.Nodes[0].NodeID != 1 {
		t.Fatalf("重建节点 = %+v，期望仅活跃节点 1", requester.rebuildPayload.Nodes)
	}
	// 期望 tag 只含活跃节点 tag（非活跃节点 tag 被剔除），期望 pieces 为空数组非 null。
	tags := map[string]bool{}
	for _, tag := range requester.rebuildPayload.ExpectedInboundTags {
		tags[tag] = true
	}
	if !tags["node_1"] || tags["node_2"] {
		t.Fatalf("期望 tag = %v", requester.rebuildPayload.ExpectedInboundTags)
	}
	if requester.rebuildPayload.ExpectedPieces == nil {
		t.Fatalf("期望 pieces 应为 [] 而非 null")
	}
}

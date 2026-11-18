package panel

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"lattix/backend/internal/store"
)

func setupOrchestrationHandler(t *testing.T) (*Server, *store.Store, int64, map[string]int64) {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "handler-graph.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	req := &fakeAutoRequester{online: map[int64]bool{}}
	srv, err := New(st, req, Config{AdminUser: "admin", AdminPass: "test-password-123"})
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]int64{}
	for _, alias := range []string{"A1", "A2", "B1", "B2"} {
		id, err := st.CreateServer(ctx, store.ServerDraft{
			Alias: alias, Address: "203.0.113." + alias[1:2],
			BootstrapToken: "tok-" + alias, MachineType: store.MachineTypeDirect, CountryCode: "JP",
		})
		if err != nil {
			t.Fatal(err)
		}
		ids[alias] = id
		req.online[id] = true
	}
	chainID, err := st.InsertChain(ctx, "HandlerChain")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.InsertChainHop(ctx, chainID, 0, ids["A1"], store.HopRoleEntry, 0, 443, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := st.InsertChainHop(ctx, chainID, 1, ids["B1"], store.HopRoleExit, 0, 8443, ""); err != nil {
		t.Fatal(err)
	}
	return srv, st, chainID, ids
}

func hopGraphBody(chainID int64, seq int, root, backup int64, subject, action, rootAddr string, rootPort int) string {
	return fmt.Sprintf(`{"chain_id":%d,"seq":%d,"nodes":[`+
		`{"kind":"server","server_id":%d,"is_root":true,"address":%q,"listen_port":%d},`+
		`{"kind":"trigger","subject":%q,"action":%q,"priority":0},`+
		`{"kind":"server","server_id":%d,"listen_port":8443}],`+
		`"edges":[{"from":0,"to":1},{"from":1,"to":2}]}`,
		chainID, seq, root, rootAddr, rootPort, subject, action, backup)
}

type handlerEnvelope struct {
	Code    string          `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

func decodeHandlerEnvelope(t *testing.T, rec *httptest.ResponseRecorder) handlerEnvelope {
	t.Helper()
	var env handlerEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode envelope: %v body=%s", err, rec.Body.String())
	}
	return env
}

func saveHopGraph(t *testing.T, srv *Server, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/chain/hop-orchestration/save", strings.NewReader(body))
	rec := httptest.NewRecorder()
	srv.handleSaveHopOrchestration(rec, req)
	return rec
}

func TestSaveHopOrchestrationPersistsAndSyncs(t *testing.T) {
	srv, st, chainID, ids := setupOrchestrationHandler(t)
	body := hopGraphBody(chainID, 0, ids["A1"], ids["A2"],
		store.TriggerSubjectSelf, store.TriggerActionBanBanned, "203.0.113.1", 443)
	rec := saveHopGraph(t, srv, body)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	env := decodeHandlerEnvelope(t, rec)
	if env.Code != "OK" {
		t.Fatalf("unexpected code %s message %s", env.Code, env.Message)
	}
	var dto chainDTO
	if err := json.Unmarshal(env.Data, &dto); err != nil {
		t.Fatal(err)
	}
	if len(dto.Hops) != 2 || dto.Hops[0].Orchestration == nil {
		t.Fatalf("missing orchestration in dto: %+v", dto.Hops)
	}
	orch := dto.Hops[0].Orchestration
	if orch.RootServerID != ids["A1"] || orch.ActiveServerID != ids["A1"] || orch.Triggered {
		t.Fatalf("unexpected orchestration: %+v", orch)
	}
	if len(orch.Nodes) != 3 || len(orch.Edges) != 2 {
		t.Fatalf("unexpected graph: %+v", orch)
	}
	if !orch.Nodes[0].IsRoot || !orch.Nodes[0].Active || orch.Nodes[0].Alias != "A1" {
		t.Fatalf("unexpected root node: %+v", orch.Nodes[0])
	}
	if orch.Nodes[1].OwnerServerID != ids["A1"] {
		t.Fatalf("owner not resolved: %+v", orch.Nodes[1])
	}
	if dto.Hops[1].Orchestration != nil {
		t.Fatal("unexpected orchestration on second hop")
	}

	updated, err := st.ChainHops(context.Background(), chainID)
	if err != nil {
		t.Fatal(err)
	}
	if len(updated) != 2 || updated[0].Address != "203.0.113.1" || updated[0].ForwardPort != 443 {
		t.Fatalf("current node not synced: %+v", updated)
	}

	body = hopGraphBody(chainID, 0, ids["A1"], ids["A2"],
		store.TriggerSubjectSelf, store.TriggerActionBanBanned, "203.0.113.1", 9443)
	rec = saveHopGraph(t, srv, body)
	if env := decodeHandlerEnvelope(t, rec); env.Code != "OK" {
		t.Fatalf("second save status %d body %s", rec.Code, rec.Body.String())
	}
	updated, _ = st.ChainHops(context.Background(), chainID)
	if updated[0].Address != "203.0.113.1" || updated[0].ForwardPort != 9443 {
		t.Fatalf("second sync failed: %+v", updated[0])
	}
}

func TestSaveHopOrchestrationRejects(t *testing.T) {
	srv, st, chainID, ids := setupOrchestrationHandler(t)
	badRoot := hopGraphBody(chainID, 0, ids["A2"], ids["A1"],
		store.TriggerSubjectSelf, store.TriggerActionBanBanned, "", 0)
	rec := saveHopGraph(t, srv, badRoot)
	if env := decodeHandlerEnvelope(t, rec); env.Code != "INVALID_ARGUMENT" ||
		!strings.Contains(env.Message, "第 1 跳") {
		t.Fatalf("expected validation error, got %d %s", rec.Code, rec.Body.String())
	}

	if _, err := st.CreateChainRevision(context.Background(), chainID, store.ChainRevisionSnapshot{Name: "HandlerChain"}); err != nil {
		t.Fatal(err)
	}
	valid := hopGraphBody(chainID, 0, ids["A1"], ids["A2"],
		store.TriggerSubjectSelf, store.TriggerActionBanBanned, "", 0)
	rec = saveHopGraph(t, srv, valid)
	if env := decodeHandlerEnvelope(t, rec); env.Code != "INVALID_ARGUMENT" ||
		!strings.Contains(env.Message, "在途修订") {
		t.Fatalf("expected deploy guard, got %d %s", rec.Code, rec.Body.String())
	}
}

func TestSwitchHopRejectsServerOutsideGraph(t *testing.T) {
	srv, _, chainID, ids := setupOrchestrationHandler(t)
	body := hopGraphBody(chainID, 0, ids["A1"], ids["A2"],
		store.TriggerSubjectSelf, store.TriggerActionBanBanned, "", 0)
	if rec := saveHopGraph(t, srv, body); rec.Code != http.StatusOK {
		t.Fatalf("save graph: %d %s", rec.Code, rec.Body.String())
	}
	req := httptest.NewRequest(http.MethodPost, "/api/chain/switch-hop",
		strings.NewReader(fmt.Sprintf(`{"chain_id":%d,"seq":0,"server_id":%d}`, chainID, ids["B2"])))
	rec := httptest.NewRecorder()
	srv.handleSwitchChainHop(rec, req)
	if env := decodeHandlerEnvelope(t, rec); env.Code != "INVALID_ARGUMENT" ||
		!strings.Contains(env.Message, "不在该跳的编排图中") {
		t.Fatalf("expected graph membership rejection, got %d %s", rec.Code, rec.Body.String())
	}
}

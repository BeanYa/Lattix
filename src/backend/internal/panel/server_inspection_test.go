package panel

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"lattix/backend/internal/store"
)

func TestServerInspectionDueTracksAttempts(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	serverID, err := st.CreateServer(ctx, store.ServerDraft{
		Alias: "direct", BootstrapToken: "token", MachineType: store.MachineTypeDirect,
	})
	if err != nil {
		t.Fatal(err)
	}
	server := &Server{st: st}
	due, err := server.serverInspectionDue(ctx, serverID)
	if err != nil || !due {
		t.Fatalf("empty due = %v, %v", due, err)
	}
	if _, err := st.CreateServerInspectionRun(ctx, serverID, "task-1"); err != nil {
		t.Fatal(err)
	}
	due, err = server.serverInspectionDue(ctx, serverID)
	if err != nil || due {
		t.Fatalf("queued due = %v, %v", due, err)
	}
	if err := st.CompleteServerInspectionRun(ctx, serverID, "task-1", "succeeded", `{}`, "", ""); err != nil {
		t.Fatal(err)
	}
	due, err = server.serverInspectionDue(ctx, serverID)
	if err != nil || due {
		t.Fatalf("recent completed due = %v, %v", due, err)
	}
}

func TestMaybeStartServerInspectionSkipsNAT(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	serverID, err := st.CreateServer(ctx, store.ServerDraft{
		Alias: "nat", BootstrapToken: "token", MachineType: store.MachineTypeNAT,
	})
	if err != nil {
		t.Fatal(err)
	}
	server := &Server{st: st}
	if err := server.maybeStartServerInspection(ctx, serverID, true); err != nil {
		t.Fatalf("NAT inspection error = %v", err)
	}
	if _, err := st.LatestServerInspectionRun(ctx, serverID); err == nil {
		t.Fatal("NAT server should not create an inspection run")
	}
}

func TestHandleListServerInspectionsOnlyDirectWithSummary(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	directID, err := st.CreateServer(ctx, store.ServerDraft{
		Alias: "direct", BootstrapToken: "token", MachineType: store.MachineTypeDirect,
	})
	if err != nil {
		t.Fatal(err)
	}
	natID, err := st.CreateServer(ctx, store.ServerDraft{
		Alias: "nat", BootstrapToken: "token-2", MachineType: store.MachineTypeNAT,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateServerInspectionRun(ctx, directID, "task-direct"); err != nil {
		t.Fatal(err)
	}
	if err := st.CompleteServerInspectionRun(ctx, directID, "task-direct", "succeeded",
		`{"status":"succeeded","tcp_ipv4":{"status":"available","targets":3,"failed":0}}`, "", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateServerInspectionRun(ctx, natID, "task-nat"); err != nil {
		t.Fatal(err)
	}

	server := &Server{st: st}
	recorder := httptest.NewRecorder()
	server.handleListServerInspections(recorder, httptest.NewRequest(http.MethodGet, "/api/server/inspections", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Code string               `json:"code"`
		Data serverInspectionsDTO `json:"data"`
	}
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if response.Code != "OK" || len(response.Data.Items) != 1 {
		t.Fatalf("response = %+v", response)
	}
	item := response.Data.Items[0]
	if item.ServerID != directID || item.MachineType != store.MachineTypeDirect {
		t.Fatalf("item = %+v", item)
	}
	if item.Latest == nil || item.Latest.Status != "succeeded" || len(item.Latest.Summary) == 0 {
		t.Fatalf("latest = %+v", item.Latest)
	}
	if len(item.History) != 1 {
		t.Fatalf("history = %+v", item.History)
	}
}

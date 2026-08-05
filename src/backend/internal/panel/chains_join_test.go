package panel

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"lattix/backend/internal/dispatch"
	"lattix/backend/internal/store"
	"lattix/shared"
)

// TestCreateChainsShareOccupiedEntryPort 验证跨 profile 端口共享：链 B 使用链 A
// 已占用的入口端口时自动加入同一共享端点（不再 409），两链 entry_shared 均为
// true，且只产生一条 shared_endpoints 行。
func TestCreateChainsShareOccupiedEntryPort(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	serverID, _ := st.CreateServer(ctx, "entry", "entry.test", "token", store.MachineTypeDirect, "", "", "US", "")
	requester := &chainEditRequester{online: map[int64]bool{serverID: true}}
	serverAPI := &Server{st: st, disp: dispatch.New(st, requester), req: requester}

	create := func(name, shortID string) chainDTO {
		t.Helper()
		node := createNodeRequest{Name: name, ServerID: serverID, Protocol: shared.ProtocolVLESS,
			ShortID: shortID, Dest: "dl.google.com:443", ServerNames: []string{"dl.google.com"},
			Fingerprint: shared.FingerprintChrome, Network: shared.NetworkTCP, Flow: shared.FlowVision}
		if err := node.normalize(); err != nil {
			t.Fatal(err)
		}
		entryPort := 443
		body, _ := json.Marshal(createChainRequest{Name: name,
			Hops: []chainHopRef{{ServerID: serverID}}, Entry: chainHopRef{ServerID: serverID},
			Exit: chainHopRef{ServerID: serverID}, Node: node, EntryPort: &entryPort,
			TrafficMultiplier: "1.000"})
		req := httptest.NewRequest("POST", "/api/chain/create", bytes.NewReader(body))
		recorder := httptest.NewRecorder()
		serverAPI.handleCreateChain(recorder, req)
		env := decodeRPC(t, recorder)
		if recorder.Code != http.StatusOK || env.Code != shared.CodeOK {
			t.Fatalf("create %s = %d %s %s", name, recorder.Code, env.Code, recorder.Body.String())
		}
		var dto chainDTO
		if err := json.Unmarshal(env.Data, &dto); err != nil {
			t.Fatal(err)
		}
		return dto
	}

	chainA := create("a", "short-a")
	chainB := create("b", "short-b") // 不同 short_id → 不同 profile

	// entry_shared 是列表刷新时的聚合快照：重新拉取列表，取两链最新 DTO。
	listRecorder := httptest.NewRecorder()
	serverAPI.handleListChains(listRecorder, httptest.NewRequest("GET", "/api/chains", nil))
	env := decodeRPC(t, listRecorder)
	if listRecorder.Code != http.StatusOK || env.Code != shared.CodeOK {
		t.Fatalf("list = %d %s %s", listRecorder.Code, env.Code, listRecorder.Body.String())
	}
	var listed []chainDTO
	if err := json.Unmarshal(env.Data, &listed); err != nil {
		t.Fatal(err)
	}
	for i := range listed {
		switch listed[i].ID {
		case chainA.ID:
			chainA = listed[i]
		case chainB.ID:
			chainB = listed[i]
		}
	}

	if chainA.EndpointID == 0 || chainB.EndpointID == 0 {
		t.Fatalf("chains lack endpoint: a=%d b=%d", chainA.EndpointID, chainB.EndpointID)
	}
	if chainA.EndpointID != chainB.EndpointID {
		t.Fatalf("chains did not share endpoint: %d vs %d", chainA.EndpointID, chainB.EndpointID)
	}
	if !chainA.EntryShared || !chainB.EntryShared {
		t.Fatalf("entry_shared = a:%v b:%v, want true/true", chainA.EntryShared, chainB.EntryShared)
	}
	endpoints, err := st.SharedEndpointsByServer(ctx, serverID)
	if err != nil {
		t.Fatal(err)
	}
	if len(endpoints) != 1 {
		t.Fatalf("shared_endpoints rows = %d, want 1", len(endpoints))
	}
}

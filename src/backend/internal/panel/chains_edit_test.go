package panel

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"lattix/backend/internal/dispatch"
	"lattix/backend/internal/store"
	"lattix/shared"
)

type chainEditRequester struct {
	online map[int64]bool
}

func (f *chainEditRequester) Send(context.Context, int64, shared.Envelope) error { return nil }
func (f *chainEditRequester) IsOnline(serverID int64) bool                       { return f.online[serverID] }

func TestEditChainRemovesMiddleAndPlansOnlyAffectedPieces(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	server := func(alias, token string) int64 {
		id, err := st.CreateServer(ctx, alias, alias+".example.com", token, store.MachineTypeDirect, "", "", "US", "")
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	aID, bID, cID := server("a", "token-a"), server("b", "token-b"), server("c", "token-c")
	port := 3000
	nodeRequest := createNodeRequest{Name: "chain", ServerID: cID, Protocol: shared.ProtocolVLESS,
		Port: &port, ShortID: "0123456789abcdef", Dest: "dl.google.com:443",
		ServerNames: []string{"dl.google.com"}, Fingerprint: shared.FingerprintChrome,
		Network: shared.NetworkTCP, Flow: shared.FlowVision}
	if err := nodeRequest.normalize(); err != nil {
		t.Fatal(err)
	}
	config, _ := json.Marshal(buildVirtualConfig(nodeRequest))
	nodeID, _ := st.InsertNode(ctx, "chain", cID, shared.ProtocolVLESS, &port, config)
	realized, _ := json.Marshal(shared.RealizedConfig{Port: port, PublicKey: "public-key",
		ShortID: nodeRequest.ShortID, ServerName: "dl.google.com", Network: shared.NetworkTCP,
		Flow: shared.FlowVision, Fingerprint: shared.FingerprintChrome})
	_ = st.SetNodeActive(ctx, nodeID, realized)
	chainID, _ := st.InsertChain(ctx, "chain")
	aHop, _ := st.InsertChainHop(ctx, chainID, 0, aID, store.HopRoleEntry, 0, 1000, "")
	bHop, _ := st.InsertChainHop(ctx, chainID, 1, bID, store.HopRoleMiddle, 0, 2000, "")
	cHop, _ := st.InsertChainHop(ctx, chainID, 2, cID, store.HopRoleExit, nodeID, port, "")
	_ = st.SetChainServiceNode(ctx, chainID, nodeID)
	for _, hopID := range []int64{aHop, bHop, cHop} {
		_ = st.SetChainHopStatus(ctx, hopID, store.HopStatusActive, "")
	}
	revision, err := st.CreateChainRevision(ctx, chainID, store.ChainRevisionSnapshot{
		Name: "chain", ServiceNodeID: nodeID, ServiceServerID: cID,
		ServiceConfig: config, ServiceRealized: realized, TrafficMultiplierMilli: 1000,
		Hops: []store.ChainRevisionHop{
			{HopID: aHop, ServerID: aID, Role: store.HopRoleEntry, Transport: "direct", ForwardPort: 1000},
			{HopID: bHop, ServerID: bID, Role: store.HopRoleMiddle, Transport: "direct", ForwardPort: 2000},
			{HopID: cHop, ServerID: cID, Role: store.HopRoleExit, ForwardPort: port},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.PublishChainRevision(ctx, revision.ID, false); err != nil {
		t.Fatal(err)
	}

	requester := &chainEditRequester{online: map[int64]bool{aID: true, bID: true, cID: true}}
	dispatcher := dispatch.New(st, requester)
	serverAPI := &Server{st: st, disp: dispatcher, req: requester}
	body, _ := json.Marshal(editChainRequest{ChainID: chainID, Name: "chain",
		Hops: []chainHopRef{{ServerID: aID}, {ServerID: cID}}, EntryPort: func() *int { value := 1000; return &value }(),
		Node: nodeRequest, TrafficMultiplier: "1.000"})
	req := httptest.NewRequest("POST", "/api/chain/edit", bytes.NewReader(body))
	recorder := httptest.NewRecorder()
	serverAPI.handleEditChain(recorder, req)
	if recorder.Code != 200 {
		t.Fatalf("edit response = %d %s", recorder.Code, recorder.Body.String())
	}

	desired, err := st.DesiredChainRevision(ctx, chainID)
	if err != nil {
		t.Fatal(err)
	}
	tasks, _ := st.RevisionTasks(ctx, desired.ID)
	byKey := map[string]store.ChainRevisionTask{}
	for _, task := range tasks {
		byKey[task.TaskKey] = task
	}
	if len(tasks) != 2 {
		t.Fatalf("revision tasks = %+v", tasks)
	}
	if task, ok := byKey["forward/"+formatID(aHop)]; !ok || task.Phase != "apply" || task.ServerID != aID {
		t.Fatalf("entry apply task = %+v present=%v", task, ok)
	}
	if task, ok := byKey["cleanup/forward/"+formatID(bHop)]; !ok || task.Phase != "cleanup" || task.ServerID != bID {
		t.Fatalf("middle cleanup task = %+v present=%v", task, ok)
	}
	hops, _ := st.ChainHops(ctx, chainID)
	if len(hops) != 2 || hops[0].ID != aHop || hops[1].ID != cHop {
		t.Fatalf("desired hops = %+v", hops)
	}
}

func formatID(value int64) string {
	raw, _ := json.Marshal(value)
	return string(raw)
}

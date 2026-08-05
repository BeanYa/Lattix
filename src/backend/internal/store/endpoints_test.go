package store

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"lattix/shared"
)

func TestSharedEndpointReuseAndAssignmentIdentity(t *testing.T) {
	ctx := context.Background()
	st, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	serverID, _ := st.CreateServer(ctx, "entry", "entry.test", "token", MachineTypeDirect, "", "", "US", "")
	config := json.RawMessage(`{"protocol":"vless","port":443,"template":{}}`)
	endpoint, created, err := st.EnsureSharedEndpoint(ctx, serverID, shared.ProtocolVLESS, 443, "profile-a", config)
	if err != nil || !created {
		t.Fatalf("create endpoint: created=%v err=%v", created, err)
	}
	reused, created, err := st.EnsureSharedEndpoint(ctx, serverID, shared.ProtocolVLESS, 443, "profile-a", config)
	if err != nil || created || reused.ID != endpoint.ID {
		t.Fatalf("reuse endpoint: got=%+v created=%v err=%v", reused, created, err)
	}
	if _, _, err := st.EnsureSharedEndpoint(ctx, serverID, shared.ProtocolVLESS, 443, "profile-b", config); !errors.Is(err, ErrEndpointConflict) {
		t.Fatalf("incompatible endpoint error = %v", err)
	}
	second, created, err := st.EnsureSharedEndpoint(ctx, serverID, shared.ProtocolVLESS, 8443, "profile-a", config)
	if err != nil || !created || second.ID == endpoint.ID || second.Port != 8443 {
		t.Fatalf("explicit second port: endpoint=%+v created=%v err=%v", second, created, err)
	}

	deployment, err := st.CreateInitialChainDeployment(ctx, InitialChainDeployment{
		Name: "direct", ServiceServerID: serverID, ServiceProtocol: shared.ProtocolVLESS,
		ServiceConfig: config, EndpointID: endpoint.ID, ServiceUUID: "service-uuid",
		TrafficMultiplierMilli: 1000,
		Hops:                   []InitialChainHop{{ServerID: serverID, Role: HopRoleExit}},
	})
	if err != nil {
		t.Fatal(err)
	}
	userID, _ := st.InsertUser(ctx, "user", "user-uuid", "sub-token", nil)
	added, removed, err := st.SetUserChains(ctx, userID, []int64{deployment.ChainID})
	if err != nil || len(added) != 1 || len(removed) != 0 {
		t.Fatalf("first assignment: added=%+v removed=%+v err=%v", added, removed, err)
	}
	firstUUID := added[0].AccessUUID
	added, removed, err = st.SetUserChains(ctx, userID, []int64{deployment.ChainID})
	if err != nil || len(added) != 0 || len(removed) != 0 {
		t.Fatalf("idempotent assignment: added=%+v removed=%+v err=%v", added, removed, err)
	}
	assignments, _ := st.UserChainAssignments(ctx, userID)
	if len(assignments) != 1 || assignments[0].AccessUUID != firstUUID {
		t.Fatalf("assignment credential changed: %+v", assignments)
	}
}

func TestAccessTrafficIsAttributedOnceToUserAndChain(t *testing.T) {
	ctx := context.Background()
	st, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	serverID, _ := st.CreateServer(ctx, "entry", "entry.test", "token", MachineTypeDirect, "", "", "US", "")
	config := json.RawMessage(`{"protocol":"vless","template":{}}`)
	endpoint, _, _ := st.EnsureSharedEndpoint(ctx, serverID, shared.ProtocolVLESS, 443, "profile", config)
	deployment, err := st.CreateInitialChainDeployment(ctx, InitialChainDeployment{
		Name: "direct", ServiceServerID: serverID, ServiceProtocol: shared.ProtocolVLESS,
		ServiceConfig: config, EndpointID: endpoint.ID, ServiceUUID: "service-uuid",
		TrafficMultiplierMilli: 1500,
		Hops:                   []InitialChainHop{{ServerID: serverID, Role: HopRoleExit}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.PublishChainRevision(ctx, deployment.RevisionID, false); err != nil {
		t.Fatal(err)
	}
	userID, _ := st.InsertUser(ctx, "user", "user-uuid", "sub-token", nil)
	added, _, err := st.SetUserChains(ctx, userID, []int64{deployment.ChainID})
	if err != nil {
		t.Fatal(err)
	}
	identity := "access:" + jsonNumber(added[0].ID)
	now := time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC)
	counters := []TrafficCounterSnapshot{
		{User: identity, Up: 100, Down: 200},
		{User: "tunnel:service-uuid", Up: 100, Down: 200},
		{EndpointID: endpoint.ID, Up: 100, Down: 200},
	}
	if err := st.ApplyTrafficSnapshot(ctx, serverID, "instance-1", counters, now); err != nil {
		t.Fatal(err)
	}
	traffic, _ := st.UserTraffic(ctx, "user-uuid")
	if traffic.Up != 100 || traffic.Down != 200 {
		t.Fatalf("user traffic = %+v", traffic)
	}
	totals, _ := st.ChainTrafficTotals(ctx, deployment.ChainID)
	if len(totals) != 1 || totals[0].HopID != 0 || totals[0].RawUp != 100 || totals[0].EffectiveUp != 150 {
		t.Fatalf("chain traffic = %+v", totals)
	}
	var endpointUp, endpointDown int64
	if err := st.db.QueryRow(`SELECT up, down FROM endpoint_traffic_totals WHERE endpoint_id=?`, endpoint.ID).
		Scan(&endpointUp, &endpointDown); err != nil || endpointUp != 100 || endpointDown != 200 {
		t.Fatalf("endpoint traffic = %d/%d err=%v", endpointUp, endpointDown, err)
	}
}

func TestUserUUIDByAssignment(t *testing.T) {
	ctx := context.Background()
	st, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	serverID, _ := st.CreateServer(ctx, "entry", "entry.test", "token", MachineTypeDirect, "", "", "US", "")
	config := json.RawMessage(`{"protocol":"vless","template":{}}`)
	endpoint, _, err := st.EnsureSharedEndpoint(ctx, serverID, shared.ProtocolVLESS, 443, "profile", config)
	if err != nil {
		t.Fatal(err)
	}
	deployment, err := st.CreateInitialChainDeployment(ctx, InitialChainDeployment{
		Name: "direct", ServiceServerID: serverID, ServiceProtocol: shared.ProtocolVLESS,
		ServiceConfig: config, EndpointID: endpoint.ID, ServiceUUID: "service-uuid",
		TrafficMultiplierMilli: 1000,
		Hops:                   []InitialChainHop{{ServerID: serverID, Role: HopRoleExit}},
	})
	if err != nil {
		t.Fatal(err)
	}
	userID, _ := st.InsertUser(ctx, "user", "user-uuid", "sub-token", nil)
	added, _, err := st.SetUserChains(ctx, userID, []int64{deployment.ChainID})
	if err != nil || len(added) != 1 {
		t.Fatalf("assignment: added=%+v err=%v", added, err)
	}
	uuid, err := st.UserUUIDByAssignment(ctx, added[0].ID)
	if err != nil || uuid != "user-uuid" {
		t.Fatalf("UserUUIDByAssignment(%d) = %q, %v", added[0].ID, uuid, err)
	}
	if _, err := st.UserUUIDByAssignment(ctx, 999999); err == nil {
		t.Fatal("unknown assignment id unexpectedly resolved")
	}
}

func TestValidateAssignableChainsRejectsLegacyChain(t *testing.T) {
	ctx := context.Background()
	st, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	chainID, err := st.InsertChain(ctx, "legacy")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.ValidateAssignableChains(ctx, []int64{chainID}); err == nil {
		t.Fatal("legacy chain unexpectedly assignable")
	}
}

func TestActiveEndpointAssignmentsIncludesGroupUsers(t *testing.T) {
	ctx := context.Background()
	st, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	chainA, endpointA := newTestEndpointChain(t, st, "ep-a")
	chainB, endpointB := newTestEndpointChain(t, st, "ep-b")
	// 分组用户（经分组引用 endpointA 的链）
	member, _ := st.InsertUser(ctx, "member", "00000000-0000-0000-0000-0000000000aa", "tok-m", nil)
	// 直接分配用户
	direct, _ := st.InsertUser(ctx, "direct", "00000000-0000-0000-0000-0000000000bb", "tok-d", nil)
	lgID, err := st.CreateLinkGroup(ctx, "普通组", []int64{chainA}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateUserGroup(ctx, "青铜会员", []int64{member}, []int64{lgID}); err != nil {
		t.Fatal(err)
	}
	// 回归：同一用户属于两个用户分组且引用同一链路组时，组派生查询不得产生重复行
	if _, err := st.CreateUserGroup(ctx, "白银会员", []int64{member}, []int64{lgID}); err != nil {
		t.Fatal(err)
	}
	directAdded, _, err := st.SetUserChains(ctx, direct, []int64{chainB})
	if err != nil {
		t.Fatal(err)
	}
	assignments, err := st.ActiveEndpointAssignments(ctx, endpointA)
	if err != nil || len(assignments) != 1 {
		t.Fatalf("endpoint A assignments = %+v err %v", assignments, err)
	}
	want := GroupAccessUUID("00000000-0000-0000-0000-0000000000aa", chainA)
	if assignments[0].UserID != member || assignments[0].ChainID != chainA ||
		assignments[0].EndpointID != endpointA || assignments[0].AccessUUID != want {
		t.Fatalf("endpoint A assignment = %+v want uuid %s", assignments[0], want)
	}
	// 分组派生身份必须是 group:<user_uuid>:<chain_id>，且用户 UUID 内嵌（≠ access:0）
	if assignments[0].UserUUID != "00000000-0000-0000-0000-0000000000aa" {
		t.Fatalf("endpoint A assignment UserUUID = %q", assignments[0].UserUUID)
	}
	wantIdentity := "group:00000000-0000-0000-0000-0000000000aa:" + jsonNumber(chainA)
	if got := assignments[0].Identity(); got != wantIdentity {
		t.Fatalf("group identity = %q want %q", got, wantIdentity)
	}
	// endpointB：直接用户可见；分组用户不在其链路上
	assignments, err = st.ActiveEndpointAssignments(ctx, endpointB)
	if err != nil || len(assignments) != 1 || assignments[0].UserID != direct {
		t.Fatalf("endpoint B assignments = %+v err %v", assignments, err)
	}
	// 直接身份保持 access:<assignment_id> 格式
	wantDirect := "access:" + jsonNumber(directAdded[0].ID)
	if got := assignments[0].Identity(); got != wantDirect {
		t.Fatalf("direct identity = %q want %q", got, wantDirect)
	}
	// 分组用户即使有直接分配行也被遮蔽（同链重复不出现）
	if _, _, err := st.SetUserChains(ctx, member, []int64{chainA}); err != nil {
		t.Fatal(err)
	}
	assignments, err = st.ActiveEndpointAssignments(ctx, endpointA)
	if err != nil || len(assignments) != 1 {
		t.Fatalf("after direct assign, endpoint A = %+v err %v", assignments, err)
	}
}

func jsonNumber(value int64) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

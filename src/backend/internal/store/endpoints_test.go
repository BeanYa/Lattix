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
	serverID, _ := st.CreateServer(ctx, ServerDraft{Alias: "entry", Address: "entry.test", BootstrapToken: "token", MachineType: MachineTypeDirect, CountryCode: "US"})
	config := json.RawMessage(`{"protocol":"vless","port":443,"template":{}}`)
	endpoint, created, err := st.EnsureSharedEndpoint(ctx, serverID, shared.ProtocolVLESS, 443, "profile-a", config)
	if err != nil || !created {
		t.Fatalf("create endpoint: created=%v err=%v", created, err)
	}
	reused, created, err := st.EnsureSharedEndpoint(ctx, serverID, shared.ProtocolVLESS, 443, "profile-a", config)
	if err != nil || created || reused.ID != endpoint.ID {
		t.Fatalf("reuse endpoint: got=%+v created=%v err=%v", reused, created, err)
	}
	// 不同 profile 同端口：加入既有监听（不再冲突）；protocol 不同才冲突。
	joined, created, err := st.EnsureSharedEndpoint(ctx, serverID, shared.ProtocolVLESS, 443, "profile-b", config)
	if err != nil || created || joined.ID != endpoint.ID {
		t.Fatalf("join incompatible profile: id=%d created=%v err=%v", joined.ID, created, err)
	}
	if _, _, err := st.EnsureSharedEndpoint(ctx, serverID, "socks", 443, "profile-b", config); !errors.Is(err, ErrEndpointConflict) {
		t.Fatalf("different protocol must conflict, got %v", err)
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
	serverID, _ := st.CreateServer(ctx, ServerDraft{Alias: "entry", Address: "entry.test", BootstrapToken: "token", MachineType: MachineTypeDirect, CountryCode: "US"})
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
	serverID, _ := st.CreateServer(ctx, ServerDraft{Alias: "entry", Address: "entry.test", BootstrapToken: "token", MachineType: MachineTypeDirect, CountryCode: "US"})
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
	// 一用户一组：加入第二用户分组自动移出旧组；组派生查询仍只返回该链路一行
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

func TestEnsureSharedEndpointJoinsWithoutDuplicateRows(t *testing.T) {
	ctx := context.Background()
	st, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	serverID, _ := st.CreateServer(ctx, ServerDraft{Alias: "entry", Address: "entry.test", BootstrapToken: "token", MachineType: MachineTypeDirect, CountryCode: "US"})
	config := json.RawMessage(`{"protocol":"vless","port":443,"template":{}}`)
	// 遗留重复行（并发竞态产物）：同一 server/port 已存在两行，Ensure 必须按 id 取首行加入，
	// 不新增行，也不取到后写入的重复行。
	insert := func(profile string) int64 {
		t.Helper()
		res, err := st.db.Exec(`INSERT INTO shared_endpoints
			(server_id, protocol, port, profile_hash, config_template) VALUES (?, ?, ?, ?, ?)`,
			serverID, shared.ProtocolVLESS, 443, profile, string(config))
		if err != nil {
			t.Fatal(err)
		}
		id, err := res.LastInsertId()
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	firstID := insert("profile-legacy-1")
	insert("profile-legacy-2")
	endpoint, _, err := st.EnsureSharedEndpoint(ctx, serverID, shared.ProtocolVLESS, 443, "profile-a", config)
	if err != nil {
		t.Fatal(err)
	}
	if endpoint.ID != firstID {
		t.Fatalf("join = %+v, want first row by id %d", endpoint, firstID)
	}
	var count int
	if err := st.db.QueryRow(`SELECT COUNT(*) FROM shared_endpoints WHERE server_id=? AND port=443`, serverID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("shared_endpoints rows for port 443 = %d, want 2 (no new rows)", count)
	}
}

func TestEndpointChainCount(t *testing.T) {
	ctx := context.Background()
	st, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	serverID, _ := st.CreateServer(ctx, ServerDraft{Alias: "entry", Address: "entry.test", BootstrapToken: "token", MachineType: MachineTypeDirect, CountryCode: "US"})
	config := json.RawMessage(`{"protocol":"vless","port":443,"template":{}}`)
	endpoint, _, err := st.EnsureSharedEndpoint(ctx, serverID, shared.ProtocolVLESS, 443, "profile", config)
	if err != nil {
		t.Fatal(err)
	}
	if count, err := st.EndpointChainCount(ctx, endpoint.ID); err != nil || count != 0 {
		t.Fatalf("count before chains = %d err=%v", count, err)
	}
	deployment, err := st.CreateInitialChainDeployment(ctx, InitialChainDeployment{
		Name: "a", ServiceServerID: serverID, ServiceProtocol: shared.ProtocolVLESS,
		ServiceConfig: config, EndpointID: endpoint.ID, ServiceUUID: "svc-a",
		TrafficMultiplierMilli: 1000,
		Hops:                   []InitialChainHop{{ServerID: serverID, Role: HopRoleExit}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if count, err := st.EndpointChainCount(ctx, endpoint.ID); err != nil || count != 1 {
		t.Fatalf("count after one chain = %d err=%v", count, err)
	}
	if err := st.DeleteChain(ctx, deployment.ChainID); err != nil {
		t.Fatal(err)
	}
	if count, err := st.EndpointChainCount(ctx, endpoint.ID); err != nil || count != 0 {
		t.Fatalf("count after delete = %d err=%v", count, err)
	}
}

func jsonNumber(value int64) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

// TestSetSharedEndpointActivePreservesEncryption 验证清理 #7：新一轮 realized 未携带
// encryption 时沿用库中已生效值，不清空（订阅 encryption 字段依赖）。
func TestSetSharedEndpointActivePreservesEncryption(t *testing.T) {
	ctx := context.Background()
	st, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	serverID, _ := st.CreateServer(ctx, ServerDraft{Alias: "entry", Address: "10.0.0.9",
		BootstrapToken: "token", MachineType: MachineTypeDirect, CountryCode: "US"})
	endpoint, _, err := st.EnsureSharedEndpoint(ctx, serverID, shared.ProtocolVLESS, 14433,
		"hash-enc", json.RawMessage(`{"protocol":"vless","template":{}}`))
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetSharedEndpointApplying(ctx, endpoint.ID); err != nil {
		t.Fatal(err)
	}
	if err := st.SetSharedEndpointActive(ctx, endpoint.ID,
		json.RawMessage(`{"port":14433,"encryption":"mlkem768x25519plus.0rtt.XXX"}`)); err != nil {
		t.Fatal(err)
	}
	// 重部署回写不含 encryption → 库中值必须保留。
	if err := st.SetSharedEndpointApplying(ctx, endpoint.ID); err != nil {
		t.Fatal(err)
	}
	if err := st.SetSharedEndpointActive(ctx, endpoint.ID, json.RawMessage(`{"port":14433}`)); err != nil {
		t.Fatal(err)
	}
	ep, err := st.SharedEndpointByID(ctx, endpoint.ID)
	if err != nil {
		t.Fatal(err)
	}
	var rc struct {
		Encryption string `json:"encryption"`
	}
	if err := json.Unmarshal(ep.RealizedConfig, &rc); err != nil {
		t.Fatal(err)
	}
	if rc.Encryption != "mlkem768x25519plus.0rtt.XXX" {
		t.Errorf("encryption 应保留，实际 %q（realized=%s）", rc.Encryption, ep.RealizedConfig)
	}
}

// TestEnsureProtocolSharedEndpoint 验证 hy2 出口共享：同机同协议仅一个活跃监听，
// 第二链并入（首链 profile/端口为准）；不同服务器互不影响；删除态不复用。
func TestEnsureProtocolSharedEndpoint(t *testing.T) {
	ctx := context.Background()
	st, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	serverID, _ := st.CreateServer(ctx, ServerDraft{Alias: "exit", Address: "exit.test", BootstrapToken: "token", MachineType: MachineTypeDirect, CountryCode: "US"})
	otherID, _ := st.CreateServer(ctx, ServerDraft{Alias: "other", Address: "other.test", BootstrapToken: "token2", MachineType: MachineTypeDirect, CountryCode: "JP"})

	cfg1 := json.RawMessage(`{"protocol":"hysteria","port":0,"port_hop":"30000-30031","template":{}}`)
	ep1, created, err := st.EnsureProtocolSharedEndpoint(ctx, serverID, shared.ProtocolHysteria2, 0, cfg1)
	if err != nil || !created {
		t.Fatalf("首链应创建: created=%v err=%v", created, err)
	}
	// 第二链不同 profile/段：并入首链监听（首链为准）。
	cfg2 := json.RawMessage(`{"protocol":"hysteria","port":0,"port_hop":"40000-40031","template":{}}`)
	ep2, created, err := st.EnsureProtocolSharedEndpoint(ctx, serverID, shared.ProtocolHysteria2, 0, cfg2)
	if err != nil || created || ep2.ID != ep1.ID {
		t.Fatalf("第二链应并入首链监听: id=%d created=%v err=%v", ep2.ID, created, err)
	}
	// 显式端口创建；后续 port=0 的链也并入（端口以既有为准）。
	ep3, created, err := st.EnsureProtocolSharedEndpoint(ctx, otherID, shared.ProtocolHysteria2, 14433, cfg1)
	if err != nil || !created || ep3.ID == ep1.ID || ep3.Port != 14433 {
		t.Fatalf("其他服务器应独立创建: ep=%+v created=%v err=%v", ep3, created, err)
	}
	ep4, created, err := st.EnsureProtocolSharedEndpoint(ctx, otherID, shared.ProtocolHysteria2, 0, cfg2)
	if err != nil || created || ep4.ID != ep3.ID {
		t.Fatalf("port=0 应并入同机既有监听: id=%d created=%v err=%v", ep4.ID, created, err)
	}
	// 不同协议互不影响。
	if _, created, err := st.EnsureProtocolSharedEndpoint(ctx, serverID, shared.ProtocolVLESS, 0, cfg1); err != nil || !created {
		t.Fatalf("不同协议应独立创建: created=%v err=%v", created, err)
	}
	// 失败态端点不复用：failed 后再次 Ensure 应新建。
	if err := st.SetSharedEndpointFailed(ctx, ep1.ID, "boom"); err != nil {
		t.Fatal(err)
	}
	ep5, created, err := st.EnsureProtocolSharedEndpoint(ctx, serverID, shared.ProtocolHysteria2, 0, cfg2)
	if err != nil || !created || ep5.ID == ep1.ID {
		t.Fatalf("failed 端点不应复用: id=%d created=%v err=%v", ep5.ID, created, err)
	}
	// 非法参数报错。
	if _, _, err := st.EnsureProtocolSharedEndpoint(ctx, 0, "hysteria", 0, cfg1); err == nil {
		t.Fatal("serverID=0 应报错")
	}
	if _, _, err := st.EnsureProtocolSharedEndpoint(ctx, serverID, "", 0, cfg1); err == nil {
		t.Fatal("空 protocol 应报错")
	}
	if _, _, err := st.EnsureProtocolSharedEndpoint(ctx, serverID, "hysteria", 0, json.RawMessage(`not json`)); err == nil {
		t.Fatal("非法 JSON 应报错")
	}
	if _, _, err := st.EnsureProtocolSharedEndpoint(ctx, serverID, "hysteria", 0, nil); err == nil {
		t.Fatal("空 config 应报错")
	}
}

// createHy2ServiceChain 建一条引用出口共享监听 endpointID 的 hy2 链（P4 测试夹具）。
func createHy2ServiceChain(t *testing.T, st *Store, serverID, endpointID, serviceEndpointID int64, name string) InitialChainDeploymentResult {
	t.Helper()
	deployment, err := st.CreateInitialChainDeployment(context.Background(), InitialChainDeployment{
		Name: name, ServiceServerID: serverID, ServiceProtocol: shared.ProtocolHysteria2,
		ServiceConfig: json.RawMessage(`{"protocol":"hysteria","template":{}}`),
		EndpointID: endpointID, ServiceEndpointID: serviceEndpointID, ServiceUUID: "svc-" + name,
		TrafficMultiplierMilli: 1000,
		Hops:                   []InitialChainHop{{ServerID: serverID, Role: HopRoleExit}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return deployment
}

// TestChainsByServiceEndpoint 验证出口共享监听的引用链枚举（P4）：返回全部未删
// 引用链（端到端 + 入口终结），已删除链排除。
func TestChainsByServiceEndpoint(t *testing.T) {
	ctx := context.Background()
	st, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	serverID, _ := st.CreateServer(ctx, ServerDraft{Alias: "exit", Address: "exit.test", BootstrapToken: "token", MachineType: MachineTypeDirect, CountryCode: "US"})
	hy2cfg := json.RawMessage(`{"protocol":"hysteria","template":{}}`)
	epE, _, err := st.EnsureProtocolSharedEndpoint(ctx, serverID, shared.ProtocolHysteria2, 0, hy2cfg)
	if err != nil {
		t.Fatal(err)
	}
	epE2, _, err := st.EnsureSharedEndpoint(ctx, serverID, shared.ProtocolVLESS, 443, "profile", json.RawMessage(`{"protocol":"vless","template":{}}`))
	if err != nil {
		t.Fatal(err)
	}
	dep1 := createHy2ServiceChain(t, st, serverID, 0, epE.ID, "e2e")
	dep2 := createHy2ServiceChain(t, st, serverID, epE2.ID, epE.ID, "entry")
	dep3 := createHy2ServiceChain(t, st, serverID, 0, epE.ID, "deleted")
	if err := st.DeleteChain(ctx, dep3.ChainID); err != nil {
		t.Fatal(err)
	}

	chains, err := st.ChainsByServiceEndpoint(ctx, epE.ID)
	if err != nil || len(chains) != 2 {
		t.Fatalf("应返回两条未删引用链: %v %d", err, len(chains))
	}
	if chains[0].ID != dep1.ChainID || chains[1].ID != dep2.ChainID {
		t.Fatalf("引用链 = %+v，期望 [%d %d]", chains, dep1.ChainID, dep2.ChainID)
	}
	// 不引用该端点的链不计入。
	other, _, err := st.EnsureProtocolSharedEndpoint(ctx, serverID, shared.ProtocolVLESS, 0, hy2cfg)
	if err != nil {
		t.Fatal(err)
	}
	if chains, err := st.ChainsByServiceEndpoint(ctx, other.ID); err != nil || len(chains) != 0 {
		t.Fatalf("无引用端点应返回空: %v %d", err, len(chains))
	}
}

// TestActiveServiceEndpointUsers 验证端到端 hy2 链（引用出口共享监听且无入口端点）的
// 业务用户 UUID 全集：直接分配 + 分组派生并入，排除 expired/disabled；入口终结链的
// 用户归属入口端点，不计入出口监听。
func TestActiveServiceEndpointUsers(t *testing.T) {
	ctx := context.Background()
	st, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	serverID, _ := st.CreateServer(ctx, ServerDraft{Alias: "exit", Address: "exit.test", BootstrapToken: "token", MachineType: MachineTypeDirect, CountryCode: "US"})
	hy2cfg := json.RawMessage(`{"protocol":"hysteria","template":{}}`)
	epE, _, err := st.EnsureProtocolSharedEndpoint(ctx, serverID, shared.ProtocolHysteria2, 0, hy2cfg)
	if err != nil {
		t.Fatal(err)
	}
	epE2, _, err := st.EnsureSharedEndpoint(ctx, serverID, shared.ProtocolVLESS, 443, "profile", json.RawMessage(`{"protocol":"vless","template":{}}`))
	if err != nil {
		t.Fatal(err)
	}
	e2e := createHy2ServiceChain(t, st, serverID, 0, epE.ID, "e2e")
	entry := createHy2ServiceChain(t, st, serverID, epE2.ID, epE.ID, "entry")

	u1, _ := st.InsertUser(ctx, "u1", "11111111-1111-1111-1111-111111111111", "sub1", nil)
	u2, _ := st.InsertUser(ctx, "u2", "22222222-2222-2222-2222-222222222222", "sub2", nil)
	u3, _ := st.InsertUser(ctx, "u3", "33333333-3333-3333-3333-333333333333", "sub3", nil)
	u4, _ := st.InsertUser(ctx, "u4", "44444444-4444-4444-4444-444444444444", "sub4", nil)
	if _, _, err := st.SetUserChains(ctx, u1, []int64{e2e.ChainID}); err != nil {
		t.Fatalf("端到端 hy2 链应可分配: %v", err)
	}
	if _, _, err := st.SetUserChains(ctx, u2, []int64{e2e.ChainID}); err != nil {
		t.Fatal(err)
	}
	if err := st.SetUserDisabled(ctx, u2, true); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.SetUserChains(ctx, u4, []int64{entry.ChainID}); err != nil {
		t.Fatal(err)
	}
	lgID, err := st.CreateLinkGroup(ctx, "组", []int64{e2e.ChainID}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateUserGroup(ctx, "青铜", []int64{u3}, []int64{lgID}); err != nil {
		t.Fatal(err)
	}

	uuids, err := st.ActiveServiceEndpointUsers(ctx, epE.ID)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"11111111-1111-1111-1111-111111111111", "33333333-3333-3333-3333-333333333333"}
	if len(uuids) != len(want) {
		t.Fatalf("用户全集 = %v，期望 %v（u2 disabled、u4 入口终结链均排除）", uuids, want)
	}
	for i := range want {
		if uuids[i] != want[i] {
			t.Fatalf("用户全集 = %v，期望 %v", uuids, want)
		}
	}
}

// TestChainByServiceNode 验证按出口业务节点反查未删链（P4 用户扇出改道用）。
func TestChainByServiceNode(t *testing.T) {
	ctx := context.Background()
	st, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	serverID, _ := st.CreateServer(ctx, ServerDraft{Alias: "exit", Address: "exit.test", BootstrapToken: "token", MachineType: MachineTypeDirect, CountryCode: "US"})
	dep := createHy2ServiceChain(t, st, serverID, 0, 0, "lookup")
	chain, err := st.ChainByServiceNode(ctx, dep.NodeID)
	if err != nil || chain == nil || chain.ID != dep.ChainID {
		t.Fatalf("chain = %+v err = %v", chain, err)
	}
	if chain, err := st.ChainByServiceNode(ctx, 9999); err != nil || chain != nil {
		t.Fatalf("未命中应返回 nil, nil: %+v %v", chain, err)
	}
	if err := st.DeleteChain(ctx, dep.ChainID); err != nil {
		t.Fatal(err)
	}
	if chain, err := st.ChainByServiceNode(ctx, dep.NodeID); err != nil || chain != nil {
		t.Fatalf("已删链应返回 nil, nil: %+v %v", chain, err)
	}
}

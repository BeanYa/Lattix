package store

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"lattix/shared"
)

// newTestEndpointChain 创建一条带共享入口的已发布链路（vless），返回 (chainID, endpointID)。
// 复制 chain_revision_test.go 的建链模式并在快照中携带 EndpointID。
func newTestEndpointChain(t *testing.T, st *Store, name string) (int64, int64) {
	t.Helper()
	ctx := context.Background()
	entryID, _ := st.CreateServer(ctx, name+"-entry", "entry.example.com", "tok-"+name+"-e", MachineTypeDirect, "", "", "US", "")
	exitID, _ := st.CreateServer(ctx, name+"-exit", "exit.example.com", "tok-"+name+"-x", MachineTypeDirect, "", "", "JP", "")
	config, _ := json.Marshal(shared.VirtualConfig{Protocol: shared.ProtocolVLESS, Network: shared.NetworkTCP, Template: json.RawMessage(`{}`)})
	endpoint, _, err := st.EnsureSharedEndpoint(ctx, entryID, shared.ProtocolVLESS, 30000, "profile", config)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetSharedEndpointActive(ctx, endpoint.ID, []byte(`{"port":20001}`)); err != nil {
		t.Fatal(err)
	}
	nodeID, _ := st.InsertNode(ctx, name+"-node", exitID, shared.ProtocolVLESS, nil, config)
	chainID, _ := st.InsertChain(ctx, name)
	entryHopID, _ := st.InsertChainHop(ctx, chainID, 0, entryID, HopRoleEntry, 0, 10001, "")
	exitHopID, _ := st.InsertChainHop(ctx, chainID, 1, exitID, HopRoleExit, nodeID, 0, "")
	_ = st.SetChainServiceNode(ctx, chainID, nodeID)
	realized, _ := json.Marshal(shared.RealizedConfig{Port: 20001, Network: shared.NetworkTCP})
	revision, err := st.CreateChainRevision(ctx, chainID, ChainRevisionSnapshot{
		Name: name, ServiceNodeID: nodeID, ServiceServerID: exitID,
		ServiceConfig: config, ServiceRealized: realized, TrafficMultiplierMilli: 1000,
		EndpointID: endpoint.ID, ServiceUUID: "svc-" + name,
		Hops: []ChainRevisionHop{
			{HopID: entryHopID, ServerID: entryID, Role: HopRoleEntry, Transport: "direct", ForwardPort: 10001},
			{HopID: exitHopID, ServerID: exitID, Role: HopRoleExit, ForwardPort: 20001},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.PublishChainRevision(ctx, revision.ID, false); err != nil {
		t.Fatal(err)
	}
	return chainID, endpoint.ID
}

func TestLinkGroupCRUD(t *testing.T) {
	ctx := context.Background()
	st, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	chainA, endpointA := newTestEndpointChain(t, st, "group-chain-a")
	chainB, _ := newTestEndpointChain(t, st, "group-chain-b")
	subID, err := st.CreateExternalSubscription(ctx, ExternalSubscription{
		Name: "机场X", URL: "https://sub.example.com/x", UpdateIntervalHours: 24,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateLinkGroup(ctx, "普通组", []int64{chainA, chainB},
		[]LinkGroupExternalSubscription{{SubscriptionID: subID, Mode: ExtSubModeStack}}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateLinkGroup(ctx, "普通组", nil, nil); err == nil {
		t.Fatal("重名分组应失败")
	}
	groups, err := st.ListLinkGroups(ctx)
	if err != nil || len(groups) != 1 {
		t.Fatalf("groups = %+v err %v", groups, err)
	}
	g := groups[0]
	if g.Name != "普通组" || g.ChainCount != 2 || g.ExtSubCount != 1 ||
		len(g.ChainIDs) != 2 || len(g.ExternalSubscriptions) != 1 ||
		g.ExternalSubscriptions[0].Mode != ExtSubModeStack || g.ExternalSubscriptions[0].SubscriptionID != subID {
		t.Fatalf("group = %+v", g)
	}
	if endpointA == 0 {
		t.Fatal("endpoint 未创建")
	}
	got, err := st.LinkGroupByID(ctx, g.ID)
	if err != nil || got.ID != g.ID {
		t.Fatalf("by id = %+v err %v", got, err)
	}
	taken, err := st.LinkGroupNameTaken(ctx, "普通组", 0)
	if err != nil || !taken {
		t.Fatalf("taken = %v err %v", taken, err)
	}
	if err := st.UpdateLinkGroup(ctx, g.ID, "改名组", []int64{chainA}, nil); err != nil {
		t.Fatal(err)
	}
	updated, _ := st.LinkGroupByID(ctx, g.ID)
	if updated.Name != "改名组" || updated.ChainCount != 1 || updated.ExtSubCount != 0 {
		t.Fatalf("updated = %+v", updated)
	}
	if err := st.DeleteLinkGroup(ctx, g.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.LinkGroupByID(ctx, g.ID); err == nil {
		t.Fatal("删除后应查不到")
	}
	leftover, _ := st.ListLinkGroups(ctx)
	if len(leftover) != 0 {
		t.Fatalf("删除后应无残留, got %+v", leftover)
	}
	var chains, subs int
	if err := st.db.QueryRow(`SELECT COUNT(*) FROM link_group_chains`).Scan(&chains); err != nil || chains != 0 {
		t.Fatalf("删除后 link_group_chains 应清空, count=%d err=%v", chains, err)
	}
	if err := st.db.QueryRow(`SELECT COUNT(*) FROM link_group_external_subscriptions`).Scan(&subs); err != nil || subs != 0 {
		t.Fatalf("删除后 link_group_external_subscriptions 应清空, count=%d err=%v", subs, err)
	}
	if err := st.UpdateLinkGroup(ctx, g.ID, "幽灵组", []int64{chainA}, nil); !errors.Is(err, ErrNotFound) {
		t.Fatalf("更新已删除分组应返回 ErrNotFound, got %v", err)
	}
	if err := st.db.QueryRow(`SELECT COUNT(*) FROM link_group_chains`).Scan(&chains); err != nil || chains != 0 {
		t.Fatalf("幽灵更新不应写入链路行, count=%d err=%v", chains, err)
	}
	if err := st.db.QueryRow(`SELECT COUNT(*) FROM link_group_external_subscriptions`).Scan(&subs); err != nil || subs != 0 {
		t.Fatalf("幽灵更新不应写入订阅行, count=%d err=%v", subs, err)
	}
}

func TestUserGroupCRUD(t *testing.T) {
	ctx := context.Background()
	st, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	chainA, _ := newTestEndpointChain(t, st, "ug-chain")
	lgID, err := st.CreateLinkGroup(ctx, "普通组", []int64{chainA}, nil)
	if err != nil {
		t.Fatal(err)
	}
	u1, _ := st.InsertUser(ctx, "alice", "00000000-0000-0000-0000-0000000000aa", "tok-a", nil)
	u2, _ := st.InsertUser(ctx, "bob", "00000000-0000-0000-0000-0000000000bb", "tok-b", nil)
	ugID, err := st.CreateUserGroup(ctx, "青铜会员", []int64{u1, u2}, []int64{lgID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateUserGroup(ctx, "青铜会员", nil, nil); err == nil {
		t.Fatal("重名用户分组应失败")
	}
	groups, err := st.ListUserGroups(ctx)
	if err != nil || len(groups) != 1 {
		t.Fatalf("groups = %+v err %v", groups, err)
	}
	ug := groups[0]
	if ug.Name != "青铜会员" || ug.MemberCount != 2 || ug.LinkGroupCount != 1 ||
		len(ug.UserIDs) != 2 || len(ug.LinkGroupIDs) != 1 || ug.LinkGroupIDs[0] != lgID {
		t.Fatalf("user group = %+v", ug)
	}
	if err := st.DeleteLinkGroup(ctx, lgID); err != nil {
		t.Fatal(err)
	}
	var ugLinks int
	if err := st.db.QueryRow(`SELECT COUNT(*) FROM user_group_links`).Scan(&ugLinks); err != nil || ugLinks != 0 {
		t.Fatalf("删除链路分组后 user_group_links 应清空, count=%d err=%v", ugLinks, err)
	}
	if _, err := st.UserGroupByID(ctx, ugID); err != nil {
		t.Fatalf("删除链路分组不应影响用户分组本身: %v", err)
	}
	groups, err = st.ListUserGroups(ctx)
	if err != nil || len(groups) != 1 || groups[0].LinkGroupCount != 0 {
		t.Fatalf("删除链路分组后用户分组 LinkGroupCount 应为 0, groups=%+v err=%v", groups, err)
	}
	if err := st.UpdateUserGroup(ctx, ugID, "黄金会员", []int64{u1}, []int64{}); err != nil {
		t.Fatal(err)
	}
	updated, _ := st.UserGroupByID(ctx, ugID)
	if updated.Name != "黄金会员" || updated.MemberCount != 1 || updated.LinkGroupCount != 0 {
		t.Fatalf("updated = %+v", updated)
	}
	if err := st.DeleteUserGroup(ctx, ugID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.UserGroupByID(ctx, ugID); err == nil {
		t.Fatal("删除后应查不到")
	}
	if err := st.UpdateUserGroup(ctx, ugID, "幽灵组", []int64{u1}, []int64{lgID}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("更新已删除用户分组应返回 ErrNotFound, got %v", err)
	}
	var ugMembers int
	if err := st.db.QueryRow(`SELECT COUNT(*) FROM user_group_members`).Scan(&ugMembers); err != nil || ugMembers != 0 {
		t.Fatalf("幽灵更新不应写入成员行, count=%d err=%v", ugMembers, err)
	}
	if err := st.db.QueryRow(`SELECT COUNT(*) FROM user_group_links`).Scan(&ugLinks); err != nil || ugLinks != 0 {
		t.Fatalf("幽灵更新不应写入关联行, count=%d err=%v", ugLinks, err)
	}
}

func TestGroupAccessUUID(t *testing.T) {
	uuid1 := GroupAccessUUID("user-a", 7)
	uuid2 := GroupAccessUUID("user-a", 8)
	uuid3 := GroupAccessUUID("user-b", 7)
	if uuid1 == uuid2 || uuid1 == uuid3 || uuid2 == uuid3 {
		t.Fatalf("UUID 应互不相同: %s %s %s", uuid1, uuid2, uuid3)
	}
	if GroupAccessUUID("user-a", 7) != uuid1 {
		t.Fatal("同输入应稳定")
	}
	if len(uuid1) != 36 || strings.Count(uuid1, "-") != 4 {
		t.Fatalf("应为标准 UUID 格式: %s", uuid1)
	}
}

func TestEffectiveAssignmentsShadowing(t *testing.T) {
	ctx := context.Background()
	st, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	chainA, endpointA := newTestEndpointChain(t, st, "eff-a")
	chainB, _ := newTestEndpointChain(t, st, "eff-b")
	subID, _ := st.CreateExternalSubscription(ctx, ExternalSubscription{
		Name: "机场X", URL: "https://sub.example.com/x", UpdateIntervalHours: 24,
	})
	lgID, err := st.CreateLinkGroup(ctx, "普通组", []int64{chainA, chainB},
		[]LinkGroupExternalSubscription{{SubscriptionID: subID, Mode: ExtSubModeMerge}})
	if err != nil {
		t.Fatal(err)
	}
	userID, _ := st.InsertUser(ctx, "alice", "00000000-0000-0000-0000-0000000000aa", "tok-a", nil)
	// 直接分配（应被遮蔽）：链路 A + 外部订阅 + 单机节点
	_, _, err = st.SetUserChains(ctx, userID, []int64{chainA})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetUserExternalSubscriptions(ctx, userID,
		[]UserExternalSubscription{{UserID: userID, SubscriptionID: subID, Mode: ExtSubModeStack}}); err != nil {
		t.Fatal(err)
	}
	_, err = st.InsertNode(ctx, "direct-node", 1, "vless", nil, []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.SetUserNodes(ctx, userID, []int64{1}); err != nil {
		t.Fatal(err)
	}

	// 未入组：有效分配 = 直接分配
	if ids, _ := st.UserGroupIDsForUser(ctx, userID); len(ids) != 0 {
		t.Fatalf("未入组应无分组, got %v", ids)
	}
	directChains, err := st.EffectiveUserChainAssignments(ctx, userID)
	if err != nil || len(directChains) != 1 || directChains[0].ChainID != chainA {
		t.Fatalf("direct chains = %+v err %v", directChains, err)
	}
	if directChains[0].AccessUUID == GroupAccessUUID("00000000-0000-0000-0000-0000000000aa", chainA) {
		t.Fatal("直接分配应保留存储的随机 access_uuid")
	}

	// 入组后：有效分配 = 分组派生（遮蔽直接分配），access_uuid 为确定性 UUIDv5
	ugID, err := st.CreateUserGroup(ctx, "青铜会员", []int64{userID}, []int64{lgID})
	if err != nil {
		t.Fatal(err)
	}
	ids, err := st.UserGroupIDsForUser(ctx, userID)
	if err != nil || len(ids) != 1 || ids[0] != ugID {
		t.Fatalf("group ids = %v err %v", ids, err)
	}
	groupChains, err := st.EffectiveUserChainAssignments(ctx, userID)
	if err != nil || len(groupChains) != 2 {
		t.Fatalf("group chains = %+v err %v", groupChains, err)
	}
	wantA := GroupAccessUUID("00000000-0000-0000-0000-0000000000aa", chainA)
	wantB := GroupAccessUUID("00000000-0000-0000-0000-0000000000aa", chainB)
	for _, a := range groupChains {
		if a.EndpointID == 0 {
			t.Fatalf("分组派生链路缺少 endpoint: %+v", a)
		}
		if a.ChainID == chainA && a.AccessUUID != wantA {
			t.Fatalf("chain A uuid = %s want %s", a.AccessUUID, wantA)
		}
		if a.ChainID == chainB && a.AccessUUID != wantB {
			t.Fatalf("chain B uuid = %s want %s", a.AccessUUID, wantB)
		}
	}
	// 多分组并集去重：再加一个引用同一链路分组的分组
	if _, err := st.CreateUserGroup(ctx, "白银会员", []int64{userID}, []int64{lgID}); err != nil {
		t.Fatal(err)
	}
	groupChains, _ = st.EffectiveUserChainAssignments(ctx, userID)
	if len(groupChains) != 2 {
		t.Fatalf("并集去重失败: %+v", groupChains)
	}
	// 外部订阅：分组模式生效（merge），且只出现一次
	extSubs, err := st.EffectiveUserExternalSubscriptions(ctx, userID)
	if err != nil || len(extSubs) != 1 || extSubs[0].Mode != ExtSubModeMerge {
		t.Fatalf("effective ext subs = %+v err %v", extSubs, err)
	}
	// 移出全部分组后恢复直接分配
	allGroups, err := st.ListUserGroups(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, g := range allGroups {
		if err := st.DeleteUserGroup(ctx, g.ID); err != nil {
			t.Fatal(err)
		}
	}
	restored, err := st.EffectiveUserChainAssignments(ctx, userID)
	if err != nil || len(restored) != 1 || restored[0].ChainID != chainA {
		t.Fatalf("restored = %+v err %v", restored, err)
	}
	_ = endpointA
}

func TestAffectedUsersQueries(t *testing.T) {
	ctx := context.Background()
	st, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	chainA, endpointA := newTestEndpointChain(t, st, "aff-a")
	chainB, _ := newTestEndpointChain(t, st, "aff-b")
	subID, _ := st.CreateExternalSubscription(ctx, ExternalSubscription{
		Name: "机场X", URL: "https://sub.example.com/x", UpdateIntervalHours: 24,
	})
	lgID, err := st.CreateLinkGroup(ctx, "普通组", []int64{chainA, chainB},
		[]LinkGroupExternalSubscription{{SubscriptionID: subID, Mode: ExtSubModeStack}})
	if err != nil {
		t.Fatal(err)
	}
	inGroup, _ := st.InsertUser(ctx, "member", "00000000-0000-0000-0000-0000000000aa", "tok-m", nil)
	outside, _ := st.InsertUser(ctx, "outside", "00000000-0000-0000-0000-0000000000bb", "tok-o", nil)
	ugID, err := st.CreateUserGroup(ctx, "青铜会员", []int64{inGroup}, []int64{lgID})
	if err != nil {
		t.Fatal(err)
	}
	// 直接分配 outside 到 chainA（验证两个分支并集）
	if _, _, err := st.SetUserChains(ctx, outside, []int64{chainA}); err != nil {
		t.Fatal(err)
	}

	users, err := st.SubscriptionUserIDsForChain(ctx, chainA)
	if err != nil || len(users) != 2 {
		t.Fatalf("chain users = %v err %v", users, err)
	}
	users, err = st.SubscriptionUserIDsForEndpoint(ctx, endpointA)
	if err != nil || len(users) != 2 {
		t.Fatalf("endpoint users = %v err %v", users, err)
	}
	users, err = st.SubscriptionUserIDsForLinkGroup(ctx, lgID)
	if err != nil || len(users) != 1 || users[0] != inGroup {
		t.Fatalf("link group users = %v err %v", users, err)
	}
	users, err = st.UsersForUserGroup(ctx, ugID)
	if err != nil || len(users) != 1 || users[0] != inGroup {
		t.Fatalf("user group users = %v err %v", users, err)
	}
	users, err = st.UsersByExternalSubscriptionThroughGroups(ctx, subID)
	if err != nil || len(users) != 1 || users[0] != inGroup {
		t.Fatalf("ext sub group users = %v err %v", users, err)
	}
	// 服务器维度：aff-a 入口(1)/出口(2) → 直接 + 分组 2 人；aff-b 入口(3) → 仅分组 1 人
	users, err = st.SubscriptionUserIDsForServer(ctx, 1)
	if err != nil || len(users) != 2 {
		t.Fatalf("server 1 (aff-a entry) users = %v err %v", users, err)
	}
	users, err = st.SubscriptionUserIDsForServer(ctx, 3)
	if err != nil || len(users) != 1 || users[0] != inGroup {
		t.Fatalf("server 3 (aff-b entry) users = %v err %v", users, err)
	}
}

package panel

import (
	"context"
	"testing"
	"time"

	"lattix/backend/internal/store"
	"lattix/shared"
)

// testOnlineNow 是测试基准时刻（UTC），各用例基于它加减窗口推算。
func testOnlineNow() time.Time {
	return time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
}

func TestOnlineUsersTrackerNoDataReturnsZero(t *testing.T) {
	var tracker OnlineUsersTracker
	if got := tracker.ConnectionsByUser("u1", testOnlineNow()); got != 0 {
		t.Fatalf("ConnectionsByUser on empty tracker = %d, want 0", got)
	}
}

func TestOnlineUsersTrackerZeroValueUsable(t *testing.T) {
	var tracker OnlineUsersTracker
	now := testOnlineNow()
	tracker.ApplySnapshot(1, []shared.OnlineUserStat{{User: "u1", IPs: []string{"1.1.1.1"}}}, now)
	if got := tracker.ConnectionsByUser("u1", now); got != 1 {
		t.Fatalf("ConnectionsByUser after ApplySnapshot on zero-value tracker = %d, want 1", got)
	}
}

func TestOnlineUsersTrackerMultiServerUnion(t *testing.T) {
	var tracker OnlineUsersTracker
	now := testOnlineNow()
	tracker.ApplySnapshot(1, []shared.OnlineUserStat{
		{User: "u1", IPs: []string{"1.1.1.1", "1.1.1.2"}},
	}, now)
	tracker.ApplySnapshot(2, []shared.OnlineUserStat{
		{User: "u1", IPs: []string{"2.2.2.2"}},
		{User: "u2", IPs: []string{"9.9.9.9"}},
	}, now)
	if got := tracker.ConnectionsByUser("u1", now); got != 3 {
		t.Fatalf("ConnectionsByUser(u1) = %d, want 3", got)
	}
	if got := tracker.ConnectionsByUser("u2", now); got != 1 {
		t.Fatalf("ConnectionsByUser(u2) = %d, want 1", got)
	}
}

func TestOnlineUsersTrackerCrossServerSameIPDedup(t *testing.T) {
	var tracker OnlineUsersTracker
	now := testOnlineNow()
	tracker.ApplySnapshot(1, []shared.OnlineUserStat{
		{User: "u1", IPs: []string{"1.1.1.1", "2.2.2.2"}},
	}, now)
	tracker.ApplySnapshot(2, []shared.OnlineUserStat{
		{User: "u1", IPs: []string{"2.2.2.2", "3.3.3.3"}},
	}, now)
	if got := tracker.ConnectionsByUser("u1", now); got != 3 {
		t.Fatalf("ConnectionsByUser(u1) = %d, want 3 (dedup of 2.2.2.2)", got)
	}
}

func TestOnlineUsersTrackerStaleSnapshotExcluded(t *testing.T) {
	var tracker OnlineUsersTracker
	now := testOnlineNow()
	tracker.ApplySnapshot(1, []shared.OnlineUserStat{
		{User: "u1", IPs: []string{"1.1.1.1"}},
	}, now.Add(-2*FreshnessWindow))
	tracker.ApplySnapshot(2, []shared.OnlineUserStat{
		{User: "u1", IPs: []string{"2.2.2.2"}},
	}, now)
	if got := tracker.ConnectionsByUser("u1", now); got != 1 {
		t.Fatalf("ConnectionsByUser(u1) = %d, want 1 (stale server 1 excluded)", got)
	}
}

func TestOnlineUsersTrackerExactlyAtFreshnessBoundaryIncluded(t *testing.T) {
	var tracker OnlineUsersTracker
	now := testOnlineNow()
	tracker.ApplySnapshot(1, []shared.OnlineUserStat{
		{User: "u1", IPs: []string{"1.1.1.1"}},
	}, now.Add(-FreshnessWindow))
	if got := tracker.ConnectionsByUser("u1", now); got != 1 {
		t.Fatalf("ConnectionsByUser(u1) = %d, want 1 (snapshot exactly at window boundary)", got)
	}
}

func TestOnlineUsersTrackerEmptySnapshotClearsServer(t *testing.T) {
	var tracker OnlineUsersTracker
	now := testOnlineNow()
	tracker.ApplySnapshot(1, []shared.OnlineUserStat{
		{User: "u1", IPs: []string{"1.1.1.1"}},
	}, now)
	tracker.ApplySnapshot(1, nil, now)
	if got := tracker.ConnectionsByUser("u1", now); got != 0 {
		t.Fatalf("ConnectionsByUser(u1) after empty snapshot = %d, want 0", got)
	}
}

func TestOnlineUsersTrackerSnapshotReplace(t *testing.T) {
	var tracker OnlineUsersTracker
	now := testOnlineNow()
	tracker.ApplySnapshot(1, []shared.OnlineUserStat{
		{User: "u1", IPs: []string{"1.1.1.1"}},
	}, now)
	tracker.ApplySnapshot(1, []shared.OnlineUserStat{
		{User: "u1", IPs: []string{"1.1.1.1", "1.1.1.2"}},
		{User: "u2", IPs: []string{"5.5.5.5"}},
	}, now)
	if got := tracker.ConnectionsByUser("u1", now); got != 2 {
		t.Fatalf("ConnectionsByUser(u1) = %d, want 2", got)
	}
	if got := tracker.ConnectionsByUser("u2", now); got != 1 {
		t.Fatalf("ConnectionsByUser(u2) = %d, want 1", got)
	}
}

func TestOnlineUsersTrackerSweepsStaleServerOnApply(t *testing.T) {
	var tracker OnlineUsersTracker
	now := testOnlineNow()
	tracker.ApplySnapshot(1, []shared.OnlineUserStat{
		{User: "u1", IPs: []string{"1.1.1.1"}},
	}, now.Add(-3*FreshnessWindow))
	// 另一台服务器上报帧时，超出窗口的旧服务器记录必须被物理清除（servers 与 updatedAt 两表）。
	tracker.ApplySnapshot(2, []shared.OnlineUserStat{
		{User: "u1", IPs: []string{"2.2.2.2"}},
	}, now)
	if _, ok := tracker.servers[1]; ok {
		t.Fatal("stale server 1 not swept from servers map")
	}
	if _, ok := tracker.updatedAt[1]; ok {
		t.Fatal("stale server 1 not swept from updatedAt map")
	}
	if got := tracker.ConnectionsByUser("u1", now); got != 1 {
		t.Fatalf("ConnectionsByUser(u1) = %d, want 1 (stale server 1 swept on apply)", got)
	}
}

func TestOnlineUsersTrackerSweepKeepsBoundarySnapshot(t *testing.T) {
	var tracker OnlineUsersTracker
	now := testOnlineNow()
	// 恰好 FreshnessWindow 前的记录不满足 now.Sub(updatedAt) > FreshnessWindow，不得被清扫。
	tracker.ApplySnapshot(1, []shared.OnlineUserStat{
		{User: "u1", IPs: []string{"1.1.1.1"}},
	}, now.Add(-FreshnessWindow))
	tracker.ApplySnapshot(2, []shared.OnlineUserStat{
		{User: "u1", IPs: []string{"2.2.2.2"}},
	}, now)
	if got := tracker.ConnectionsByUser("u1", now); got != 2 {
		t.Fatalf("ConnectionsByUser(u1) = %d, want 2 (boundary snapshot kept)", got)
	}
}

func TestOnlineUsersTrackerResolverMapsAccessIdentity(t *testing.T) {
	var tracker OnlineUsersTracker
	tracker.resolve = func(identity string) string {
		if identity == "access:7" {
			return "uuid-alice"
		}
		return ""
	}
	now := testOnlineNow()
	tracker.ApplySnapshot(1, []shared.OnlineUserStat{
		{User: "access:7", IPs: []string{"1.1.1.1", "1.1.1.2"}},
		{User: "uuid-bob", IPs: []string{"2.2.2.2"}},
	}, now)
	if got := tracker.ConnectionsByUser("uuid-alice", now); got != 2 {
		t.Fatalf("ConnectionsByUser(uuid-alice) after access identity resolution = %d, want 2", got)
	}
	if got := tracker.ConnectionsByUser("uuid-bob", now); got != 1 {
		t.Fatalf("ConnectionsByUser(uuid-bob) = %d, want 1", got)
	}
	if got := tracker.ConnectionsByUser("access:7", now); got != 0 {
		t.Fatalf("raw access identity leaked into tracker: %d", got)
	}
}

func TestOnlineUsersTrackerSkipsTunnelIdentity(t *testing.T) {
	var tracker OnlineUsersTracker
	now := testOnlineNow()
	tracker.ApplySnapshot(1, []shared.OnlineUserStat{
		{User: "tunnel:route-9", IPs: []string{"9.9.9.9"}},
		{User: "uuid-alice", IPs: []string{"1.1.1.1"}},
	}, now)
	if got := tracker.ConnectionsByUser("uuid-alice", now); got != 1 {
		t.Fatalf("ConnectionsByUser(uuid-alice) = %d, want 1", got)
	}
	if got := tracker.ConnectionsByUser("tunnel:route-9", now); got != 0 {
		t.Fatalf("internal tunnel identity counted as online: %d", got)
	}
}

func TestOnlineUsersTrackerDropsUnresolvableAccessIdentity(t *testing.T) {
	var tracker OnlineUsersTracker
	tracker.resolve = func(identity string) string {
		if identity == "access:7" {
			return "uuid-alice"
		}
		return ""
	}
	now := testOnlineNow()
	tracker.ApplySnapshot(1, []shared.OnlineUserStat{
		{User: "access:999", IPs: []string{"3.3.3.3"}}, // 分配已删除等无法归属
		{User: "access:7", IPs: []string{"1.1.1.1"}},
	}, now)
	if got := tracker.ConnectionsByUser("uuid-alice", now); got != 1 {
		t.Fatalf("ConnectionsByUser(uuid-alice) = %d, want 1", got)
	}
	if got := tracker.ConnectionsByUser("access:999", now); got != 0 {
		t.Fatalf("unresolvable access identity leaked into tracker: %d", got)
	}
}

func TestOnlineUsersTrackerZeroValueDropsAccessIdentity(t *testing.T) {
	var tracker OnlineUsersTracker
	now := testOnlineNow()
	tracker.ApplySnapshot(1, []shared.OnlineUserStat{
		{User: "access:7", IPs: []string{"1.1.1.1"}},
	}, now)
	if got := tracker.ConnectionsByUser("uuid-alice", now); got != 0 {
		t.Fatalf("zero-value tracker without resolver counted access identity = %d, want 0", got)
	}
}

func TestOnlineUsersTrackerResolvesGroupIdentity(t *testing.T) {
	var tracker OnlineUsersTracker
	tracker.resolve = func(identity string) string {
		if identity == "group:00000000-0000-0000-0000-0000000000aa:5" {
			return "00000000-0000-0000-0000-0000000000aa"
		}
		return ""
	}
	now := testOnlineNow()
	tracker.ApplySnapshot(1, []shared.OnlineUserStat{
		{User: "group:00000000-0000-0000-0000-0000000000aa:5", IPs: []string{"1.1.1.1", "1.1.1.2"}},
		{User: "group:00000000-0000-0000-0000-0000000000bb:9", IPs: []string{"2.2.2.2"}}, // 无法归属
	}, now)
	if got := tracker.ConnectionsByUser("00000000-0000-0000-0000-0000000000aa", now); got != 2 {
		t.Fatalf("ConnectionsByUser(member) after group identity resolution = %d, want 2", got)
	}
	if got := tracker.ConnectionsByUser("group:00000000-0000-0000-0000-0000000000aa:5", now); got != 0 {
		t.Fatalf("raw group identity leaked into tracker: %d", got)
	}
	if got := tracker.ConnectionsByUser("00000000-0000-0000-0000-0000000000bb", now); got != 0 {
		t.Fatalf("unresolvable group identity leaked into tracker: %d", got)
	}
}

func TestOnlineUserResolverGroupIdentity(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	chainID, endpointID := newTestEndpointChainStore(t, st, "g-online")
	member, _ := st.InsertUser(ctx, "member", "00000000-0000-0000-0000-0000000000aa", "tok-g", nil)
	lgID, err := st.CreateLinkGroup(ctx, "lg", []int64{chainID}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateUserGroup(ctx, "ug", []int64{member}, []int64{lgID}); err != nil {
		t.Fatal(err)
	}
	assignments, err := st.ActiveEndpointAssignments(ctx, endpointID)
	if err != nil || len(assignments) != 1 {
		t.Fatalf("assignments = %+v err %v", assignments, err)
	}
	resolver := onlineUserResolver(st)
	if got := resolver(assignments[0].Identity()); got != "00000000-0000-0000-0000-0000000000aa" {
		t.Fatalf("resolver(%q) = %q", assignments[0].Identity(), got)
	}
	if got := resolver("group:bogus"); got != "" {
		t.Fatalf("malformed group identity resolved to %q", got)
	}
}

func TestOnlineUsersTrackerConcurrentApplyAndQuery(t *testing.T) {
	var tracker OnlineUsersTracker
	now := testOnlineNow()
	done := make(chan struct{})
	for i := 0; i < 4; i++ {
		go func(id int64) {
			defer func() { done <- struct{}{} }()
			for j := 0; j < 200; j++ {
				ts := now.Add(time.Duration(j) * time.Second)
				tracker.ApplySnapshot(id, []shared.OnlineUserStat{
					{User: "u1", IPs: []string{"1.1.1.1", "1.1.1.2"}},
				}, ts)
				tracker.ConnectionsByUser("u1", ts)
				tracker.ConnectionsByUser("nobody", ts)
			}
		}(int64(i + 1))
	}
	for i := 0; i < 4; i++ {
		<-done
	}
}

func TestOnlineUsersTrackerDropsRelayIPs(t *testing.T) {
	var tracker OnlineUsersTracker
	tracker.isRelay = func(ip string) bool { return ip == "1.2.3.4" }
	now := testOnlineNow()
	tracker.ApplySnapshot(1, []shared.OnlineUserStat{
		{User: "u1", IPs: []string{"1.2.3.4", "5.6.7.8"}}, // 1.2.3.4 是面板服务器自身地址（中继）
	}, now)
	if got := tracker.ConnectionsByUser("u1", now); got != 1 {
		t.Fatalf("ConnectionsByUser(u1) = %d, want 1 (relay IP excluded)", got)
	}
}

func TestOnlineUsersTrackerNilRelayFilterKeepsAll(t *testing.T) {
	var tracker OnlineUsersTracker
	now := testOnlineNow()
	tracker.ApplySnapshot(1, []shared.OnlineUserStat{
		{User: "u1", IPs: []string{"1.2.3.4", "5.6.7.8"}},
	}, now)
	if got := tracker.ConnectionsByUser("u1", now); got != 2 {
		t.Fatalf("ConnectionsByUser(u1) without relay filter = %d, want 2", got)
	}
}

func TestOnlineUsersTrackerNormalizesIPs(t *testing.T) {
	var tracker OnlineUsersTracker
	tracker.isRelay = func(ip string) bool { return ip == "2001:db8::1" }
	now := testOnlineNow()
	// 带方括号的 IPv6（xray net.Address.String 形式）与无括号形式等价；
	// IPv4-in-IPv6 映射与点分 IPv4 等价；中继地址经归一化后命中过滤器。
	tracker.ApplySnapshot(1, []shared.OnlineUserStat{
		{User: "u1", IPs: []string{"[2001:db8::1]", "5.6.7.8", "::ffff:9.9.9.9"}},
	}, now)
	tracker.ApplySnapshot(2, []shared.OnlineUserStat{
		{User: "u1", IPs: []string{"9.9.9.9"}},
	}, now)
	if got := tracker.ConnectionsByUser("u1", now); got != 2 {
		t.Fatalf("ConnectionsByUser(u1) = %d, want 2 (relay IPv6 dropped, mapped IPv4 deduped)", got)
	}
}

func TestServerRelayFilterMatchesServerAddresses(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	serverID, err := st.CreateServer(ctx, "relay-srv", "1.2.3.4", "token-1", store.MachineTypeDirect, "", "", "US", "")
	if err != nil {
		t.Fatal(err)
	}
	srv, err := st.ServerByID(ctx, serverID)
	if err != nil {
		t.Fatal(err)
	}
	// 学习地址 + NIC 地址（含 IPv6）；默认地址 1.2.3.4 已入列表。
	if err := st.RefreshServerAddresses(ctx, srv, "5.6.7.8", []string{"2001:db8::10", "1.2.3.4"}); err != nil {
		t.Fatal(err)
	}
	filter := serverRelayFilter(st)
	for _, relay := range []string{"1.2.3.4", "5.6.7.8", "2001:db8::10", "[2001:db8::10]", "127.0.0.1", "::1"} {
		if !filter(relay) {
			t.Errorf("serverRelayFilter(%q) = false, want true (server-owned/loopback)", relay)
		}
	}
	if filter("9.9.9.9") {
		t.Errorf("serverRelayFilter(9.9.9.9) = true, want false (real client IP)")
	}
}

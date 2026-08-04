package panel

import (
	"testing"
	"time"

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

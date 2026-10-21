package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

// TestInsertUserFullScheduling 验证创建时的调度语义（§9）：
// 立即/过去生效 → started_at 直接置位；未来生效 → started_at 留空待 sweeper 激活。
func TestInsertUserFullScheduling(t *testing.T) {
	ctx := context.Background()
	st, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	now := time.Now()
	immediateID, err := st.InsertUserFull(ctx, User{Name: "now", UUID: "uuid-now", SubToken: "tok-now"})
	if err != nil {
		t.Fatal(err)
	}
	future := now.Add(time.Hour).Truncate(time.Second)
	scheduledID, err := st.InsertUserFull(ctx, User{
		Name: "trial", UUID: "uuid-trial", SubToken: "tok-trial",
		EffectiveAt: &future, IsTrial: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	immediate, err := st.UserByID(ctx, immediateID)
	if err != nil {
		t.Fatal(err)
	}
	if immediate.StartedAt == nil {
		t.Fatal("立即生效用户 started_at 应直接置位")
	}
	if immediate.IsTrial {
		t.Fatal("非试用用户 is_trial 应为 false")
	}

	scheduled, err := st.UserByID(ctx, scheduledID)
	if err != nil {
		t.Fatal(err)
	}
	if scheduled.StartedAt != nil {
		t.Fatalf("未来生效用户 started_at 应为空，got %v", scheduled.StartedAt)
	}
	if !scheduled.IsTrial {
		t.Fatal("试用用户 is_trial 应为 true")
	}
	if scheduled.EffectiveAt == nil || !scheduled.EffectiveAt.Equal(future) {
		t.Fatalf("effective_at = %v, want %v", scheduled.EffectiveAt, future)
	}
	if !scheduled.NotYetEffective(now) {
		t.Fatal("未来生效用户应判定为未生效")
	}
	if immediate.NotYetEffective(now) {
		t.Fatal("立即生效用户不应判定为未生效")
	}
}

// TestListActivationDueAndMarkStarted 验证激活扫描的到期选取与标记幂等（§9）。
func TestListActivationDueAndMarkStarted(t *testing.T) {
	ctx := context.Background()
	st, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	now := time.Now()
	effective := now.Add(time.Hour)
	if _, err := st.InsertUserFull(ctx, User{
		Name: "trial", UUID: "uuid-a", SubToken: "tok-a", EffectiveAt: &effective,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.InsertUser(ctx, "plain", "uuid-b", "tok-b", nil); err != nil {
		t.Fatal(err)
	}

	due, err := st.ListActivationDue(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(due) != 0 {
		t.Fatalf("生效时刻未到不应入选，got %d", len(due))
	}
	later := now.Add(2 * time.Hour)
	due, err = st.ListActivationDue(ctx, later)
	if err != nil {
		t.Fatal(err)
	}
	if len(due) != 1 || due[0].UUID != "uuid-a" {
		t.Fatalf("生效时刻已过应入选 uuid-a，got %+v", due)
	}
	if err := st.MarkUserStarted(ctx, due[0].ID, later); err != nil {
		t.Fatal(err)
	}
	if err := st.MarkUserStarted(ctx, due[0].ID, later); err != nil {
		t.Fatalf("重复标记应幂等: %v", err)
	}
	due, err = st.ListActivationDue(ctx, later)
	if err != nil {
		t.Fatal(err)
	}
	if len(due) != 0 {
		t.Fatalf("标记后不应再入选，got %d", len(due))
	}
	if err := st.MarkUserStarted(ctx, 9999, later); !errors.Is(err, ErrNotFound) {
		t.Fatalf("标记不存在用户 err = %v, want ErrNotFound", err)
	}
}

// TestNodeUserUUIDsEffectiveFilter 验证未生效用户不随 apply_node 下发（§9），
// 生效时刻越过后（以更晚的 now 查询）恢复纳入。
func TestNodeUserUUIDsEffectiveFilter(t *testing.T) {
	ctx := context.Background()
	st, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	serverID, err := st.CreateServer(ctx, ServerDraft{Alias: "s1", Address: "s1.test", BootstrapToken: "tok", MachineType: MachineTypeDirect, CountryCode: "US"})
	if err != nil {
		t.Fatal(err)
	}
	nodeID, err := st.InsertNode(ctx, "n1", serverID, "vless", nil, []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	effective := now.Add(time.Hour)
	userID, err := st.InsertUserFull(ctx, User{Name: "trial", UUID: "uuid-t", SubToken: "tok-t", EffectiveAt: &effective})
	if err != nil {
		t.Fatal(err)
	}
	plainID, err := st.InsertUser(ctx, "plain", "uuid-p", "tok-p", nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []int64{userID, plainID} {
		if _, _, err := st.SetUserNodes(ctx, id, []int64{nodeID}); err != nil {
			t.Fatal(err)
		}
	}

	uuids, err := st.NodeUserUUIDs(ctx, nodeID, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(uuids) != 1 || uuids[0] != "uuid-p" {
		t.Fatalf("未生效用户不应下发，got %v", uuids)
	}
	uuids, err = st.NodeUserUUIDs(ctx, nodeID, now.Add(2*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(uuids) != 2 {
		t.Fatalf("生效后应下发两个用户，got %v", uuids)
	}
}

// TestSetUserEffectiveTransitions 验证生效时刻编辑对激活标记的影响（§9）：
// 推迟到未来清空 started_at（回到待激活），设为当下/清除视作已激活。
func TestSetUserEffectiveTransitions(t *testing.T) {
	ctx := context.Background()
	st, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	now := time.Now()
	id, err := st.InsertUser(ctx, "u", "uuid-e", "tok-e", nil)
	if err != nil {
		t.Fatal(err)
	}
	future := now.Add(time.Hour)
	if err := st.SetUserEffective(ctx, id, &future, now); err != nil {
		t.Fatal(err)
	}
	u, err := st.UserByID(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if u.StartedAt != nil {
		t.Fatalf("推迟生效应清空 started_at，got %v", u.StartedAt)
	}
	if !u.NotYetEffective(now) {
		t.Fatal("推迟后应判定为未生效")
	}
	if err := st.SetUserEffective(ctx, id, nil, now); err != nil {
		t.Fatal(err)
	}
	u, err = st.UserByID(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if u.EffectiveAt != nil || u.StartedAt == nil {
		t.Fatalf("清除生效时刻应回到立即生效且置位 started_at，got eff=%v started=%v", u.EffectiveAt, u.StartedAt)
	}
	if err := st.SetUserEffective(ctx, 9999, nil, now); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing user err = %v, want ErrNotFound", err)
	}
}

// TestQuotaExhaustedFilter 配额停权标记（§9）：NodeUserUUIDs 不下发 quota_exhausted 用户，
// SetUserQuotaExhausted 置位/清位幂等且与 expired 正交。
func TestQuotaExhaustedFilter(t *testing.T) {
	ctx := context.Background()
	st, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	serverID, err := st.CreateServer(ctx, ServerDraft{Alias: "s1", Address: "s1.test", BootstrapToken: "tok", MachineType: MachineTypeDirect, CountryCode: "US"})
	if err != nil {
		t.Fatal(err)
	}
	nodeID, err := st.InsertNode(ctx, "n1", serverID, "vless", nil, []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	id, err := st.InsertUser(ctx, "u", "uuid-q", "tok-q", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.SetUserNodes(ctx, id, []int64{nodeID}); err != nil {
		t.Fatal(err)
	}
	if err := st.SetUserQuotaExhausted(ctx, id, true); err != nil {
		t.Fatal(err)
	}
	uuids, err := st.NodeUserUUIDs(ctx, nodeID, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(uuids) != 0 {
		t.Fatalf("配额停权用户不应下发，got %v", uuids)
	}
	u, err := st.UserByID(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if u.Expired {
		t.Fatal("配额停权不应置 expired（正交标记）")
	}
	if err := st.SetUserQuotaExhausted(ctx, id, false); err != nil {
		t.Fatal(err)
	}
	uuids, err = st.NodeUserUUIDs(ctx, nodeID, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(uuids) != 1 {
		t.Fatalf("解除配额停权后应恢复下发，got %v", uuids)
	}
	if err := st.SetUserQuotaExhausted(ctx, 9999, true); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing user err = %v, want ErrNotFound", err)
	}
}

// TestSetUserExpiryRestoresTrialTraffic 验证重新生效语义（§9）：
// 试用用户从已到期恢复时归档并清零当期流量；普通用户不清零。
func TestSetUserExpiryRestoresTrialTraffic(t *testing.T) {
	ctx := context.Background()
	st, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	now := time.Now()
	trialID, err := st.InsertUserFull(ctx, User{Name: "trial", UUID: "uuid-t", SubToken: "tok-t", IsTrial: true})
	if err != nil {
		t.Fatal(err)
	}
	plainID, err := st.InsertUser(ctx, "plain", "uuid-p", "tok-p", nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, uuid := range []string{"uuid-t", "uuid-p"} {
		if _, err := st.db.ExecContext(ctx,
			`INSERT INTO traffic (node_id, user_uuid, up, down) VALUES (0, ?, 60, 40)`, uuid); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.SetUserExpired(ctx, trialID, true); err != nil {
		t.Fatal(err)
	}
	if err := st.SetUserExpired(ctx, plainID, true); err != nil {
		t.Fatal(err)
	}

	expiry := now.Add(24 * time.Hour)
	if err := st.SetUserExpiry(ctx, trialID, &expiry, now); err != nil {
		t.Fatal(err)
	}
	if err := st.SetUserExpiry(ctx, plainID, &expiry, now); err != nil {
		t.Fatal(err)
	}

	trial, err := st.UserByID(ctx, trialID)
	if err != nil {
		t.Fatal(err)
	}
	if trial.Expired {
		t.Fatal("试用用户延长有效期后应复位 expired（重新生效）")
	}
	totals, err := st.UserTraffic(ctx, "uuid-t")
	if err != nil {
		t.Fatal(err)
	}
	if totals.Up+totals.Down != 0 {
		t.Fatalf("试用用户重新生效后流量应清零，got up=%d down=%d", totals.Up, totals.Down)
	}
	history, err := st.ListUserTrafficHistory(ctx, "uuid-t", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 1 || history[0].Up+history[0].Down != 100 {
		t.Fatalf("试用用户旧流量应归档到 history，got %+v", history)
	}

	plain, err := st.UserByID(ctx, plainID)
	if err != nil {
		t.Fatal(err)
	}
	if plain.Expired {
		t.Fatal("普通用户延长有效期后应复位 expired")
	}
	plainTotals, err := st.UserTraffic(ctx, "uuid-p")
	if err != nil {
		t.Fatal(err)
	}
	if plainTotals.Up+plainTotals.Down != 100 {
		t.Fatalf("普通用户重新生效不应清零流量，got up=%d down=%d", plainTotals.Up, plainTotals.Down)
	}
}

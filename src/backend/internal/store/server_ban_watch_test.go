package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

// TestCatalogCarrierOutcomes 校验三网窗口聚合：按运营商尾段归组、窗口边界
// （from 开区间 / to 闭区间）、只统计当前目标 key（换区域旧 key 忽略）、空目标集短路。
func TestCatalogCarrierOutcomes(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "banwatch.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()

	now := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	latency := 42.5
	success := &latency
	failure := (*float64)(nil)
	province := func(carrier string) string {
		return catalogProbeTargetKey(CatalogProbeRegion{Province: "广东"}, carrier)
	}
	city := func(carrier string) string {
		return catalogProbeTargetKey(CatalogProbeRegion{Province: "广东", City: "深圳"}, carrier)
	}
	samples := func(keys map[string]*float64) []CatalogProbeSample {
		out := make([]CatalogProbeSample, 0, len(keys))
		for key, latencyMS := range keys {
			out = append(out, CatalogProbeSample{Key: key, Kind: "tcp", LatencyMS: latencyMS})
		}
		return out
	}
	// 上一轮（窗口外）：三网全成功。
	if err := st.SaveServerCatalogProbes(ctx, 1, samples(map[string]*float64{
		province("telecom"): success, province("unicom"): success, province("mobile"): success,
	}), now.Add(-10*time.Minute)); err != nil {
		t.Fatal(err)
	}
	// 本轮（窗口内）：电信失败一次、联通失败两次、移动成功一次；另有旧区域 key 失败。
	if err := st.SaveServerCatalogProbes(ctx, 1, samples(map[string]*float64{
		province("telecom"): failure, province("unicom"): failure, province("mobile"): success,
		city("telecom"): failure, // 换省前的旧 key，不在目标集内应被忽略
	}), now.Add(-5*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveServerCatalogProbes(ctx, 1, samples(map[string]*float64{
		province("unicom"): failure,
	}), now.Add(-1*time.Minute)); err != nil {
		t.Fatal(err)
	}

	targetKeys := []string{province("telecom"), province("unicom"), province("mobile")}
	outcomes, err := st.CatalogCarrierOutcomes(ctx, 1, targetKeys, now.Add(-6*time.Minute), now)
	if err != nil {
		t.Fatal(err)
	}
	if len(outcomes) != 3 {
		t.Fatalf("carriers = %d, want 3: %+v", len(outcomes), outcomes)
	}
	if got := outcomes["telecom"]; got.Samples != 1 || got.Failed != 1 {
		t.Fatalf("telecom = %+v, want 1/1（窗口外成功样本与旧 key 均不计）", got)
	}
	if got := outcomes["unicom"]; got.Samples != 2 || got.Failed != 2 {
		t.Fatalf("unicom = %+v, want 2/2", got)
	}
	if got := outcomes["mobile"]; got.Samples != 1 || got.Failed != 0 {
		t.Fatalf("mobile = %+v, want 1/0", got)
	}
	// from 开区间：仅取 (from, to]，-6m 之前与 -10m 的样本不计（上面已验证 telecom）。

	if got, err := st.CatalogCarrierOutcomes(ctx, 1, nil, now.Add(-time.Hour), now); err != nil || len(got) != 0 {
		t.Fatalf("empty target keys: outcomes=%v err=%v", got, err)
	}
}

// TestServerBanWatchRoundtrip 校验 Ban 观察快照的保存与读取（含时间戳字段）。
func TestServerBanWatchRoundtrip(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "banwatch.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()

	watches, err := st.ServerBanWatchMap(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(watches) != 0 {
		t.Fatalf("initial watches = %d, want 0", len(watches))
	}

	now := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	banned := now
	confirmed := now.Add(30 * time.Minute)
	row := ServerBanWatch{ServerID: 7, LossStreak: 12, State: BanStateBanned,
		StreakStartedAt: &now, SuspectedAt: &now, BannedAt: &banned, LastConfirmAt: &confirmed, UpdatedAt: now}
	if err := st.UpsertServerBanWatch(ctx, row); err != nil {
		t.Fatal(err)
	}
	// 覆盖写：推进到 recovering。
	row.State = BanStateRecovering
	row.LossStreak = 12
	if err := st.UpsertServerBanWatch(ctx, row); err != nil {
		t.Fatal(err)
	}
	watches, err = st.ServerBanWatchMap(ctx)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := watches[7]
	if !ok {
		t.Fatal("watch row missing after upsert")
	}
	if got.State != BanStateRecovering || got.LossStreak != 12 {
		t.Fatalf("watch = %+v", got)
	}
	if got.BannedAt == nil || !got.BannedAt.Equal(banned) || got.LastConfirmAt == nil || !got.LastConfirmAt.Equal(confirmed) {
		t.Fatalf("timestamps = %+v", got)
	}
}

package panel

import (
	"testing"
	"time"

	"lattix/backend/internal/alert"
	"lattix/backend/internal/store"
)

// advance 是测试辅助：以固定确认周期（半小时）推进一步。
func advance(prev store.ServerBanWatch, online bool, verdict string, now time.Time) (store.ServerBanWatch, string) {
	return advanceBanWatch(prev, online, verdict, now, banConfirmInterval)
}

func assertEvent(t *testing.T, got, want string) {
	t.Helper()
	if got != want {
		t.Fatalf("event = %q, want %q", got, want)
	}
}

func assertState(t *testing.T, row store.ServerBanWatch, want string) {
	t.Helper()
	if banState(row) != want {
		t.Fatalf("state = %q, want %q", banState(row), want)
	}
}

// TestBanWatchStreakToSuspectedAndBanned 校验主路径：连续 5 轮全丢包进入 suspected
// （第 5 轮发疑似告警），第 10 轮进入 banned（发判定告警），此后不再重复告警。
func TestBanWatchStreakToSuspectedAndBanned(t *testing.T) {
	base := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	row := store.ServerBanWatch{}
	for i := int64(1); i <= 9; i++ {
		now := base.Add(time.Duration(i) * 5 * time.Minute)
		var event string
		row, event = advance(row, true, banVerdictAllLoss, now)
		if row.LossStreak != i {
			t.Fatalf("round %d: streak = %d", i, row.LossStreak)
		}
		if i < banSuspectStreak {
			assertState(t, row, store.BanStateNormal)
			assertEvent(t, event, "")
			continue
		}
		assertState(t, row, store.BanStateSuspected)
		if i == banSuspectStreak {
			assertEvent(t, event, alert.EventServerBanSuspected)
			if row.SuspectedAt == nil {
				t.Fatal("suspected_at not recorded")
			}
		} else {
			assertEvent(t, event, "")
		}
	}

	// 第 10 轮判定被 Ban。
	row, event := advance(row, true, banVerdictAllLoss, base.Add(50*time.Minute))
	assertState(t, row, store.BanStateBanned)
	assertEvent(t, event, alert.EventServerBanned)
	if row.BannedAt == nil || row.LastConfirmAt == nil {
		t.Fatal("banned_at / last_confirm_at not recorded")
	}

	// 继续全丢包：保持 banned，不重复告警。
	row, event = advance(row, true, banVerdictAllLoss, base.Add(55*time.Minute))
	assertState(t, row, store.BanStateBanned)
	assertEvent(t, event, "")
}

// TestBanWatchConfirmGateAndRecovery 校验 banned 后的半小时确认门与取消流程：
// 确认门内在线不动作；到点确认在线进入 recovering；下一轮仍在线才发取消告警并归零。
func TestBanWatchConfirmGateAndRecovery(t *testing.T) {
	base := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	row := store.ServerBanWatch{LossStreak: 12, State: store.BanStateBanned,
		BannedAt: &base, LastConfirmAt: &base}

	// banned 后 25 分钟确认在线：半小时未到，保持 banned。
	row, event := advance(row, true, banVerdictOnline, base.Add(25*time.Minute))
	assertState(t, row, store.BanStateBanned)
	assertEvent(t, event, "")

	// 第 30 分钟确认在线：进入 recovering（待下一轮取消），本轮不发告警。
	row, event = advance(row, true, banVerdictOnline, base.Add(30*time.Minute))
	assertState(t, row, store.BanStateRecovering)
	assertEvent(t, event, "")

	// 下一轮仍在线：正式取消被 Ban 告警并归零。
	row, event = advance(row, true, banVerdictOnline, base.Add(35*time.Minute))
	assertState(t, row, store.BanStateNormal)
	assertEvent(t, event, alert.EventServerBanRecovered)
	if row.LossStreak != 0 || row.BannedAt != nil || row.SuspectedAt != nil {
		t.Fatalf("recovery should reset watch, got %+v", row)
	}
}

// TestBanWatchRelapseDuringRecovering 校验 recovering 期间再次全丢包：回到 banned
// 并重新发出判定告警。
func TestBanWatchRelapseDuringRecovering(t *testing.T) {
	base := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	confirmed := base
	row := store.ServerBanWatch{LossStreak: 12, State: store.BanStateBanned,
		BannedAt: &base, LastConfirmAt: &confirmed}
	row, _ = advance(row, true, banVerdictOnline, base.Add(30*time.Minute))
	row, event := advance(row, true, banVerdictAllLoss, base.Add(35*time.Minute))
	assertState(t, row, store.BanStateBanned)
	assertEvent(t, event, alert.EventServerBanned)
	if row.LossStreak < banBannedStreak {
		t.Fatalf("relapse streak = %d, want >= %d", row.LossStreak, banBannedStreak)
	}
}

// TestBanWatchAgentOffline 前提 agent 在线：离线轮观察中归零、banned 保持
// （无数据不等于恢复），agent 回来后继续判定。
func TestBanWatchAgentOffline(t *testing.T) {
	base := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	// suspected 中 agent 掉线：归零重来。
	row := store.ServerBanWatch{LossStreak: 6, State: store.BanStateSuspected}
	row, event := advance(row, false, banVerdictInconclusive, base)
	assertState(t, row, store.BanStateNormal)
	assertEvent(t, event, "")
	if row.LossStreak != 0 {
		t.Fatalf("offline should reset streak, got %d", row.LossStreak)
	}

	// banned 中 agent 掉线：保持 banned（不能因失联悄悄解除告警）。
	confirmed := base
	row = store.ServerBanWatch{LossStreak: 12, State: store.BanStateBanned,
		BannedAt: &base, LastConfirmAt: &confirmed}
	row, event = advance(row, false, banVerdictInconclusive, base.Add(5*time.Minute))
	assertState(t, row, store.BanStateBanned)
	assertEvent(t, event, "")

	// normal 且 streak=0 的离线轮：无行也不落库。
	row = store.ServerBanWatch{}
	after, event := advance(row, false, banVerdictInconclusive, base)
	assertState(t, after, store.BanStateNormal)
	assertEvent(t, event, "")
	if after != row {
		t.Fatalf("idle offline round should not mutate watch, got %+v", after)
	}
}

// TestBanWatchInconclusiveHolds 校验样本不全的轮次：不计数也不重置（目录缺失、
// 上报迟滞不推进也不打断连续性）。
func TestBanWatchInconclusiveHolds(t *testing.T) {
	base := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	row := store.ServerBanWatch{LossStreak: 3, State: store.BanStateNormal}
	row, event := advance(row, true, banVerdictInconclusive, base.Add(5*time.Minute))
	assertEvent(t, event, "")
	if row.LossStreak != 3 || banState(row) != store.BanStateNormal {
		t.Fatalf("inconclusive should hold, got %+v", row)
	}

	// partial loss（三网齐全但有一路成功）= online：观察中直接归零。
	row, event = advance(row, true, banVerdictOnline, base.Add(10*time.Minute))
	assertEvent(t, event, "")
	if row.LossStreak != 0 {
		t.Fatalf("online verdict should reset streak, got %d", row.LossStreak)
	}
}

// TestBanVerdictFromOutcomes 校验单轮结论判定：三网齐全才可判定，全败即全丢包、
// 任一成功即在线、缺任一运营商即无结论。
func TestBanVerdictFromOutcomes(t *testing.T) {
	allLost := func(carriers ...string) map[string]store.CatalogCarrierOutcome {
		out := make(map[string]store.CatalogCarrierOutcome)
		for _, carrier := range carriers {
			out[carrier] = store.CatalogCarrierOutcome{Samples: 2, Failed: 2}
		}
		return out
	}
	if got := banVerdictFromOutcomes(allLost("telecom", "unicom", "mobile")); got != banVerdictAllLoss {
		t.Fatalf("tri-carrier all lost = %q, want %q", got, banVerdictAllLoss)
	}
	online := allLost("telecom", "unicom", "mobile")
	online["unicom"] = store.CatalogCarrierOutcome{Samples: 2, Failed: 1}
	if got := banVerdictFromOutcomes(online); got != banVerdictOnline {
		t.Fatalf("partial loss = %q, want %q", got, banVerdictOnline)
	}
	if got := banVerdictFromOutcomes(allLost("telecom", "unicom")); got != banVerdictInconclusive {
		t.Fatalf("missing carrier = %q, want %q", got, banVerdictInconclusive)
	}
	if got := banVerdictFromOutcomes(map[string]store.CatalogCarrierOutcome{}); got != banVerdictInconclusive {
		t.Fatalf("no samples = %q, want %q", got, banVerdictInconclusive)
	}
}

// TestBanEventDetail 校验推送文案非空（分类与轮次可读）。
func TestBanEventDetail(t *testing.T) {
	for _, event := range []string{
		alert.EventServerBanSuspected, alert.EventServerBanned, alert.EventServerBanRecovered,
	} {
		if banEventDetail(event, 5) == "" {
			t.Fatalf("detail for %q is empty", event)
		}
	}
}

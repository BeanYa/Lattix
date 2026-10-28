package panel

import (
	"testing"
	"time"

	"lattix/backend/internal/alert"
	"lattix/backend/internal/store"
)

func TestBillingStatusTransitions(t *testing.T) {
	tests := []struct {
		name    string
		enabled bool
		renewal string
		today   string
		online  bool
		want    string
	}{
		{"disabled", false, "2026-04-10", "2026-04-01", false, store.BillingDisabled},
		{"active", true, "2026-04-10", "2026-04-01", false, store.BillingActive},
		{"due today online", true, "2026-04-10", "2026-04-10", true, store.BillingDueToday},
		{"due today offline", true, "2026-04-10", "2026-04-10", false, store.BillingDueToday},
		{"overdue online", true, "2026-04-10", "2026-04-11", true, store.BillingAssumedValid},
		{"overdue offline", true, "2026-04-10", "2026-04-11", false, store.BillingExpired},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := billingStatus(tt.enabled, tt.renewal, tt.today, tt.online); got != tt.want {
				t.Fatalf("status = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestTrafficPeriodPreservesMonthEndAnchor(t *testing.T) {
	loc := time.FixedZone("test", 8*60*60)
	anchor := time.Date(2026, time.January, 31, 0, 0, 0, 0, loc)
	today := time.Date(2026, time.March, 15, 0, 0, 0, 0, loc)
	start, next := trafficPeriod(anchor, today, 1, "month")
	if got, want := start.Format("2006-01-02"), "2026-02-28"; got != want {
		t.Fatalf("start = %s, want %s", got, want)
	}
	if got, want := next.Format("2006-01-02"), "2026-03-31"; got != want {
		t.Fatalf("next = %s, want %s", got, want)
	}
}

func TestTrafficAccountingModes(t *testing.T) {
	if got := trafficUsed("outbound", 10, 20); got != 10 {
		t.Fatalf("outbound = %d", got)
	}
	if got := trafficUsed("bidirectional", 10, 20); got != 30 {
		t.Fatalf("bidirectional = %d", got)
	}
	if got := trafficUsed("max", 10, 20); got != 20 {
		t.Fatalf("max = %d", got)
	}
}

func TestBillingManualTransitions(t *testing.T) {
	legal := []struct{ from, to string }{
		{store.BillingActive, store.BillingActive},
		{store.BillingDueToday, store.BillingActive},
		{store.BillingAssumedValid, store.BillingActive},
		{store.BillingExpired, store.BillingActive},
	}
	for _, c := range legal {
		if !validBillingTransition(c.from, c.to) {
			t.Errorf("缺少手动转换边 %s → %s", c.from, c.to)
		}
	}
	illegal := []struct{ from, to string }{
		{store.BillingDisabled, store.BillingActive}, // 禁用后须重新开启统计计费
		{store.BillingActive, store.BillingExpired},  // 手动路径不得反向过期
		{store.BillingDueToday, store.BillingExpired},
	}
	for _, c := range illegal {
		if validBillingTransition(c.from, c.to) {
			t.Errorf("非法手动转换未被拒绝 %s → %s", c.from, c.to)
		}
	}
}

// TestBillingExpiryEvent 校验到期预警事件判定（§告警分类）：7 天内每日提醒、
// 跃迁今日到期/已过期各一次、assumed_valid 与远期 active 不告警。
func TestBillingExpiryEvent(t *testing.T) {
	cases := []struct {
		name     string
		previous string
		current  string
		renewal  string
		today    string
		want     string
	}{
		{"7 天内每日提醒", store.BillingActive, store.BillingActive, "2026-04-07", "2026-04-01", alert.EventBillingDueSoon},
		{"提醒窗口边界 7 天", store.BillingActive, store.BillingActive, "2026-04-08", "2026-04-01", alert.EventBillingDueSoon},
		{"8 天外不提醒", store.BillingActive, store.BillingActive, "2026-04-09", "2026-04-01", ""},
		{"进入今日到期", store.BillingActive, store.BillingDueToday, "2026-04-10", "2026-04-10", alert.EventBillingDueToday},
		{"今日到期不重复", store.BillingDueToday, store.BillingDueToday, "2026-04-10", "2026-04-10", ""},
		{"跃迁已过期", store.BillingDueToday, store.BillingExpired, "2026-04-10", "2026-04-11", alert.EventBillingExpired},
		{"已过期不重复", store.BillingExpired, store.BillingExpired, "2026-04-10", "2026-04-12", ""},
		{"assumed_valid 不告警", store.BillingDueToday, store.BillingAssumedValid, "2026-04-10", "2026-04-11", ""},
		{"续费后远期不告警", store.BillingExpired, store.BillingActive, "2026-11-10", "2026-04-11", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			event, detail := billingExpiryEvent(c.previous, c.current, c.renewal, c.today)
			if event != c.want {
				t.Fatalf("event = %q, want %q", event, c.want)
			}
			if event != "" && detail == "" {
				t.Fatalf("event %q has empty detail", event)
			}
		})
	}
}

// TestDaysUntilRenewal 校验日历天数差。
func TestDaysUntilRenewal(t *testing.T) {
	if days, ok := daysUntilRenewal("2026-04-08", "2026-04-01"); !ok || days != 7 {
		t.Fatalf("days = %d ok=%v, want 7", days, ok)
	}
	if days, ok := daysUntilRenewal("2026-04-01", "2026-04-11"); !ok || days != -10 {
		t.Fatalf("days = %d ok=%v, want -10", days, ok)
	}
	if _, ok := daysUntilRenewal("not-a-date", "2026-04-01"); ok {
		t.Fatal("invalid renewal date should not parse")
	}
}

package panel

import (
	"testing"
	"time"

	"lattix/backend/internal/store"
)

func TestBillingStatusTransitions(t *testing.T) {
	tests := []struct {
		name string
		enabled bool
		renewal string
		today string
		online bool
		want string
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
	if got, want := start.Format("2006-01-02"), "2026-02-28"; got != want { t.Fatalf("start = %s, want %s", got, want) }
	if got, want := next.Format("2006-01-02"), "2026-03-31"; got != want { t.Fatalf("next = %s, want %s", got, want) }
}

func TestTrafficAccountingModes(t *testing.T) {
	if got := trafficUsed("outbound", 10, 20); got != 10 { t.Fatalf("outbound = %d", got) }
	if got := trafficUsed("bidirectional", 10, 20); got != 30 { t.Fatalf("bidirectional = %d", got) }
	if got := trafficUsed("max", 10, 20); got != 20 { t.Fatalf("max = %d", got) }
}

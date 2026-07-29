package sub

import (
	"testing"
	"time"

	"lattix/backend/internal/store"
)

func TestDaysUntilReset(t *testing.T) {
	loc := time.UTC
	cases := []struct {
		name      string
		resetDay  int
		createdAt time.Time
		now       time.Time
		want      int
	}{
		{"本月重置日未到", 14, time.Date(2026, 1, 1, 0, 0, 0, 0, loc), time.Date(2026, 7, 30, 10, 0, 0, 0, loc), 15},
		{"当天即重置日→计入下月", 30, time.Date(2026, 1, 30, 0, 0, 0, 0, loc), time.Date(2026, 7, 30, 10, 0, 0, 0, loc), 29},
		{"reset_day=0 取创建日", 0, time.Date(2026, 3, 5, 0, 0, 0, 0, loc), time.Date(2026, 7, 30, 10, 0, 0, 0, loc), 6},
		{"跨年", 10, time.Date(2025, 1, 10, 0, 0, 0, 0, loc), time.Date(2026, 12, 20, 0, 0, 0, 0, loc), 21},
	}
	for _, c := range cases {
		u := &store.User{TrafficResetDay: c.resetDay, CreatedAt: c.createdAt}
		if got := daysUntilReset(u, c.now); got != c.want {
			t.Errorf("%s: daysUntilReset = %d, want %d", c.name, got, c.want)
		}
	}
}

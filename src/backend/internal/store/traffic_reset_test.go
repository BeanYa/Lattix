package store

import (
	"context"
	"testing"
	"time"
)

func TestUserTrafficResetAtUsesLastDayForShortMonths(t *testing.T) {
	loc := time.UTC
	cases := []struct {
		name      string
		resetDay  int
		createdAt time.Time
		year      int
		month     time.Month
		wantDay   int
	}{
		{"configured 31 in April", 31, time.Date(2026, 1, 1, 0, 0, 0, 0, loc), 2026, time.April, 30},
		{"configured 31 in common February", 31, time.Date(2026, 1, 1, 0, 0, 0, 0, loc), 2026, time.February, 28},
		{"configured 31 in leap February", 31, time.Date(2028, 1, 1, 0, 0, 0, 0, loc), 2028, time.February, 29},
		{"creation day in short month", 0, time.Date(2026, 1, 31, 0, 0, 0, 0, loc), 2026, time.April, 30},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			user := User{TrafficResetDay: tc.resetDay, CreatedAt: tc.createdAt}
			if got := user.TrafficResetAt(tc.year, tc.month, loc).Day(); got != tc.wantDay {
				t.Fatalf("reset day = %d, want %d", got, tc.wantDay)
			}
		})
	}
}

func TestUsersDueForTrafficResetCatchesMissedMonthEnd(t *testing.T) {
	ctx := context.Background()
	st, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	userID, err := st.InsertUser(ctx, "month-end", "user-uuid", "sub-token", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetUserSubSettings(ctx, userID, 0, 31, "", "", "", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.ExecContext(ctx,
		`INSERT INTO traffic (node_id, user_uuid, up, down, period_start) VALUES (0, ?, 1, 0, ?)`,
		"user-uuid", "2026-02-01T00:00:00Z"); err != nil {
		t.Fatal(err)
	}

	due, err := st.UsersDueForTrafficReset(ctx, time.Date(2026, time.March, 1, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if len(due) != 1 || due[0].ID != userID {
		t.Fatalf("due users = %+v", due)
	}
}

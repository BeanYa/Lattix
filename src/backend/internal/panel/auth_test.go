package panel

import (
	"context"
	"fmt"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"lattix/backend/internal/store"
)

func TestCheckPasswordFailsClosedWhenSettingsUnavailable(t *testing.T) {
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	s := &Server{st: st, cfg: Config{AdminPass: "fallback-password"}}
	ok, err := s.checkPassword(context.Background(), "fallback-password")
	if err == nil {
		t.Fatal("checkPassword silently fell back after a settings read failure")
	}
	if ok {
		t.Fatal("checkPassword accepted a password after a settings read failure")
	}
}

func TestConcurrentPasswordChangeCannotUpgradeOldLogin(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	oldHash, err := bcrypt.GenerateFromPassword([]byte("old-password"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetSetting(ctx, store.SettingAdminPassBcrypt, string(oldHash)); err != nil {
		t.Fatal(err)
	}
	s := &Server{st: st, cfg: Config{AdminPass: "fallback-password"}}
	ok, credential, err := s.authenticatePassword(ctx, "old-password")
	if err != nil || !ok {
		t.Fatalf("authenticate old password = %t, %v", ok, err)
	}
	newHash, err := bcrypt.GenerateFromPassword([]byte("new-password"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetSetting(ctx, store.SettingAdminPassBcrypt, string(newHash)); err != nil {
		t.Fatal(err)
	}
	session := signSession("admin", time.Now().Add(time.Hour), sessionSecretForCredential(credential))
	if _, valid, err := s.verifySession(ctx, session); err != nil {
		t.Fatal(err)
	} else if valid {
		t.Fatal("session authenticated with the old password remained valid after password change")
	}
}

func TestLoginLimiterBlocksAndResets(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	limiter := newLoginLimiter(func() time.Time { return now })
	for i := 1; i < loginFailureLimit; i++ {
		if retry, blocked := limiter.recordFailure("192.0.2.1"); blocked {
			t.Fatalf("failure %d blocked early for %s", i, retry)
		}
	}
	if retry, blocked := limiter.recordFailure("192.0.2.1"); !blocked || retry != loginBlockDuration {
		t.Fatalf("limit failure = (%s, %t), want (%s, true)", retry, blocked, loginBlockDuration)
	}
	if retry, blocked := limiter.retryAfter("192.0.2.1"); !blocked || retry != loginBlockDuration {
		t.Fatalf("blocked retry = (%s, %t)", retry, blocked)
	}
	now = now.Add(loginBlockDuration)
	if retry, blocked := limiter.retryAfter("192.0.2.1"); blocked {
		t.Fatalf("block did not expire: %s", retry)
	}
}

func TestLoginLimiterBoundsTrackedIPs(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	limiter := newLoginLimiter(func() time.Time { return now })
	for i := 0; i < maxTrackedLoginIPs+100; i++ {
		limiter.recordFailure(fmt.Sprintf("192.0.2.%d", i))
	}
	if got := len(limiter.attempts); got > maxTrackedLoginIPs {
		t.Fatalf("tracked IPs = %d, want at most %d", got, maxTrackedLoginIPs)
	}
}

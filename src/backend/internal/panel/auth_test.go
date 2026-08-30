package panel

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
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

func TestSessionSecretGeneratedOnFirstUse(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	s := &Server{st: st, cfg: Config{AdminUser: "admin", AdminPass: "fallback-password"}}
	secret, err := s.sessionSecret(ctx)
	if err != nil {
		t.Fatal(err)
	}
	stored, err := st.GetSetting(ctx, store.SettingSessionSecret)
	if err != nil {
		t.Fatal(err)
	}
	if stored == "" || string(secret) != stored {
		t.Fatal("session secret was not persisted on first use")
	}
	if len(stored) != 64 {
		t.Fatalf("session secret length = %d, want 64 (32 字节 hex 编码)", len(stored))
	}
	// 再次读取复用已持久化密钥，不重复生成。
	again, err := s.sessionSecret(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if string(again) != stored {
		t.Fatal("session secret changed between reads")
	}
	// 新密钥签发的会话可验证。
	session := signSession("admin", time.Now().Add(time.Hour), secret)
	user, valid, err := s.verifySession(ctx, session)
	if err != nil {
		t.Fatal(err)
	}
	if !valid || user != "admin" {
		t.Fatalf("session signed with generated secret = (%q, %t), want (admin, true)", user, valid)
	}
}

func TestSessionInvalidAfterSecretRotation(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	s := &Server{st: st, cfg: Config{AdminUser: "admin"}}
	secret, err := s.sessionSecret(ctx)
	if err != nil {
		t.Fatal(err)
	}
	session := signSession("admin", time.Now().Add(time.Hour), secret)
	if err := s.rotateSessionSecret(ctx); err != nil {
		t.Fatal(err)
	}
	if _, valid, err := s.verifySession(ctx, session); err != nil {
		t.Fatal(err)
	} else if valid {
		t.Fatal("session remained valid after session secret rotation")
	}
}

// TestSessionInvalidAfterSecretKeyDeleted 覆盖 -reset-admin 场景：密钥设置键被删除后
// （下次使用重新生成），重启后的新进程（新 Server，进程内缓存为空）重新生成密钥，
// 旧密钥签发的会话验证失败。
func TestSessionInvalidAfterSecretKeyDeleted(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	s := &Server{st: st, cfg: Config{AdminUser: "admin"}}
	secret, err := s.sessionSecret(ctx)
	if err != nil {
		t.Fatal(err)
	}
	session := signSession("admin", time.Now().Add(time.Hour), secret)
	if err := st.DeleteSetting(ctx, store.SettingSessionSecret); err != nil {
		t.Fatal(err)
	}
	restarted := &Server{st: st, cfg: Config{AdminUser: "admin"}}
	if _, valid, err := restarted.verifySession(ctx, session); err != nil {
		t.Fatal(err)
	} else if valid {
		t.Fatal("session remained valid after session secret reset")
	}
}

func TestChangePasswordInvalidatesSessions(t *testing.T) {
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
	s := &Server{st: st, cfg: Config{AdminUser: "admin"}}
	secret, err := s.sessionSecret(ctx)
	if err != nil {
		t.Fatal(err)
	}
	session := signSession("admin", time.Now().Add(time.Hour), secret)
	body := strings.NewReader(`{"current_password":"old-password","new_password":"new-password"}`)
	r := httptest.NewRequest(http.MethodPut, "/api/settings/password", body)
	w := httptest.NewRecorder()
	s.handleChangePassword(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("change password status = %d, body = %s", w.Code, w.Body.String())
	}
	if _, valid, err := s.verifySession(ctx, session); err != nil {
		t.Fatal(err)
	} else if valid {
		t.Fatal("session remained valid after password change")
	}
}

func TestIsSecureTrustsForwardedProtoFromTrustedPeers(t *testing.T) {
	s := &Server{cfg: Config{}}
	// 反代场景：面板收到的是纯 HTTP 请求（r.TLS == nil），协议由 X-Forwarded-Proto 声明。
	newRequest := func(remote string) *http.Request {
		r := httptest.NewRequest(http.MethodGet, "http://panel.example/", nil)
		r.RemoteAddr = remote
		r.Header.Set("X-Forwarded-Proto", "https")
		return r
	}
	if !s.isSecure(newRequest("127.0.0.1:8443")) {
		t.Fatal("loopback reverse proxy X-Forwarded-Proto must be trusted")
	}
	// 1panel/openresty 容器反代：对端为 docker 桥接网段，内建默认可信。
	if !s.isSecure(newRequest("172.18.0.3:9000")) {
		t.Fatal("docker bridge reverse proxy X-Forwarded-Proto must be trusted")
	}
	if s.isSecure(newRequest("198.51.100.7:443")) {
		t.Fatal("public peer X-Forwarded-Proto must not be trusted")
	}
}
func TestLoginLimiterBlocksAndResets(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	limiter := newLoginLimiter(func() time.Time { return now }, loginFailureLimit, loginFailureWindow, loginBlockDuration, maxTrackedLoginIPs)
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
	limiter := newLoginLimiter(func() time.Time { return now }, loginFailureLimit, loginFailureWindow, loginBlockDuration, maxTrackedLoginIPs)
	for i := 0; i < maxTrackedLoginIPs+100; i++ {
		limiter.recordFailure(fmt.Sprintf("192.0.2.%d", i))
	}
	if got := len(limiter.attempts); got > maxTrackedLoginIPs {
		t.Fatalf("tracked IPs = %d, want at most %d", got, maxTrackedLoginIPs)
	}
}

func TestLoginLimiterUsernameBucketBlocksAndExpires(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	limiter := newLoginLimiter(func() time.Time { return now },
		loginUsernameFailureLimit, loginUsernameFailureWindow, loginUsernameBlockDuration, maxTrackedLoginUsernames)
	// 桶键归一化：大小写不敏感，空用户名归入同一 "u:" 桶。
	if key := loginUsernameKey("Admin"); key != "u:admin" {
		t.Fatalf("username key = %q, want %q", key, "u:admin")
	}
	if key := loginUsernameKey(""); key != "u:" {
		t.Fatalf("empty username key = %q, want %q", key, "u:")
	}
	key := loginUsernameKey("admin")
	for i := 1; i < loginUsernameFailureLimit; i++ {
		if retry, blocked := limiter.recordFailure(key); blocked {
			t.Fatalf("failure %d blocked early for %s", i, retry)
		}
	}
	if retry, blocked := limiter.recordFailure(key); !blocked || retry != loginUsernameBlockDuration {
		t.Fatalf("limit failure = (%s, %t), want (%s, true)", retry, blocked, loginUsernameBlockDuration)
	}
	// 越过 IP 桶窗口（1min）仍封着：username 桶窗口/封禁时长独立。
	now = now.Add(loginFailureWindow)
	if retry, blocked := limiter.retryAfter(key); !blocked {
		t.Fatalf("username bucket not blocked after IP window: %s", retry)
	}
	// 封禁 15min 后解封。
	now = now.Add(loginUsernameBlockDuration)
	if retry, blocked := limiter.retryAfter(key); blocked {
		t.Fatalf("username block did not expire: %s", retry)
	}
}

// XFF 伪造绕过 per-IP 桶时，per-username 兜底桶仍累计并触发限流（M1）。
func TestLoginUsernameBucketStopsXFFSpoofing(t *testing.T) {
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	s := &Server{st: st, cfg: Config{AdminUser: "admin", AdminPass: "secret"}}
	login := func(username, xff string) *httptest.ResponseRecorder {
		body := strings.NewReader(fmt.Sprintf(`{"username":%q,"password":"wrong"}`, username))
		r := httptest.NewRequest(http.MethodPost, "/api/login", body)
		// 回环对端默认可信（同网攻击者场景）：XFF 被采纳为客户端 IP。
		r.RemoteAddr = "127.0.0.1:12345"
		r.Header.Set("X-Forwarded-For", xff)
		w := httptest.NewRecorder()
		s.handleLogin(w, r)
		return w
	}
	// 每次随机化 XFF：IP 桶永远是新桶不触发，username 桶持续累计。
	for i := 1; i < loginUsernameFailureLimit; i++ {
		w := login("admin", fmt.Sprintf("198.51.100.%d", i))
		if w.Code == http.StatusTooManyRequests {
			t.Fatalf("attempt %d rate limited early", i)
		}
	}
	w := login("admin", "198.51.100.254")
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("attempt %d status = %d, want 429（username 桶应拦截 XFF 伪造绕过）", loginUsernameFailureLimit, w.Code)
	}
	if w.Header().Get("Retry-After") == "" {
		t.Fatal("rate limited response missing Retry-After header")
	}
	// 其他用户名不受该桶影响（两桶按 key 独立）。
	if w := login("other", "198.51.100.200"); w.Code == http.StatusTooManyRequests {
		t.Fatal("different username wrongly rate limited by admin's bucket")
	}
}

// 认证成功后 per-IP 与 per-username 两桶都清零。
func TestLoginSuccessClearsBothBuckets(t *testing.T) {
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	s := &Server{st: st, cfg: Config{AdminUser: "admin", AdminPass: "secret"}}
	login := func(password string) *httptest.ResponseRecorder {
		body := strings.NewReader(fmt.Sprintf(`{"username":"admin","password":%q}`, password))
		r := httptest.NewRequest(http.MethodPost, "/api/login", body)
		r.RemoteAddr = "192.0.2.1:12345"
		w := httptest.NewRecorder()
		s.handleLogin(w, r)
		return w
	}
	for i := 0; i < loginFailureLimit-1; i++ {
		if w := login("wrong"); w.Code == http.StatusTooManyRequests {
			t.Fatalf("attempt %d rate limited early", i+1)
		}
	}
	if w := login("secret"); w.Code != http.StatusOK {
		t.Fatalf("login status = %d, body = %s", w.Code, w.Body.String())
	}
	if got := len(s.loginLimiter().attempts); got != 0 {
		t.Fatalf("IP bucket entries after success = %d, want 0", got)
	}
	if got := len(s.usernameLoginLimiter().attempts); got != 0 {
		t.Fatalf("username bucket entries after success = %d, want 0", got)
	}
}

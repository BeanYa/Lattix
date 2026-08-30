package panel

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
)

const (
	loginFailureLimit  = 5
	loginFailureWindow = time.Minute
	loginBlockDuration = 5 * time.Minute
	maxTrackedLoginIPs = 4096
	// username 兜底桶：XFF 可被直连同网攻击者伪造，per-IP 桶会被随机化 XFF 绕过，
	// 故按用户名独立计一层（10 次失败/5min 窗口→封 15min，与 IP 桶并存互不影响）。
	// 残余风险：知晓用户名者可持续失败登录以锁定该账号（fail2ban 同类权衡），防暴力破解优先。
	loginUsernameFailureLimit  = 10
	loginUsernameFailureWindow = 5 * time.Minute
	loginUsernameBlockDuration = 15 * time.Minute
	maxTrackedLoginUsernames   = 4096
	bcryptCheckConcurrency     = 4
)

var errBcryptBusy = errors.New("bcrypt concurrency limit reached")

type loginAttempt struct {
	failures     int
	windowStart  time.Time
	blockedUntil time.Time
	lastSeen     time.Time
}

type loginLimiter struct {
	mu            sync.Mutex
	attempts      map[string]loginAttempt
	now           func() time.Time
	failureLimit  int
	failureWindow time.Duration
	blockDuration time.Duration
	maxTracked    int
}

func newLoginLimiter(now func() time.Time, failureLimit int, failureWindow, blockDuration time.Duration, maxTracked int) *loginLimiter {
	return &loginLimiter{
		attempts:      make(map[string]loginAttempt),
		now:           now,
		failureLimit:  failureLimit,
		failureWindow: failureWindow,
		blockDuration: blockDuration,
		maxTracked:    maxTracked,
	}
}

func (s *Server) initAuthProtection() {
	s.authOnce.Do(func() {
		s.loginAttempts = newLoginLimiter(time.Now, loginFailureLimit, loginFailureWindow, loginBlockDuration, maxTrackedLoginIPs)
		s.loginUsernameAttempts = newLoginLimiter(time.Now, loginUsernameFailureLimit, loginUsernameFailureWindow, loginUsernameBlockDuration, maxTrackedLoginUsernames)
		s.bcryptSlots = make(chan struct{}, bcryptCheckConcurrency)
	})
}

func (s *Server) loginLimiter() *loginLimiter {
	s.initAuthProtection()
	return s.loginAttempts
}

// usernameLoginLimiter 返回 per-username 兜底限流器：按 RemoteAddr 计数会在反代部署下
// 把全员锁进同一桶，故保留 per-IP（采纳 XFF）之外再以用户名兜底，防 XFF 伪造绕过。
func (s *Server) usernameLoginLimiter() *loginLimiter {
	s.initAuthProtection()
	return s.loginUsernameAttempts
}

// loginUsernameKey 归一化 username 桶键：大小写不敏感；空用户名归入同一 "u:" 桶。
func loginUsernameKey(username string) string {
	return "u:" + strings.ToLower(username)
}

func (s *Server) acquireBcryptSlot() bool {
	s.initAuthProtection()
	select {
	case s.bcryptSlots <- struct{}{}:
		return true
	default:
		return false
	}
}

func (s *Server) releaseBcryptSlot() {
	<-s.bcryptSlots
}

func (s *Server) generatePasswordHash(password string) ([]byte, error) {
	if !s.acquireBcryptSlot() {
		return nil, errBcryptBusy
	}
	defer s.releaseBcryptSlot()
	return bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
}

func (l *loginLimiter) retryAfter(key string) (time.Duration, bool) {
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()
	attempt, ok := l.attempts[key]
	if !ok {
		return 0, false
	}
	attempt.lastSeen = now
	if attempt.blockedUntil.After(now) {
		l.attempts[key] = attempt
		return attempt.blockedUntil.Sub(now), true
	}
	if now.Sub(attempt.windowStart) >= l.failureWindow {
		delete(l.attempts, key)
		return 0, false
	}
	l.attempts[key] = attempt
	return 0, false
}

func (l *loginLimiter) recordFailure(key string) (time.Duration, bool) {
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()
	attempt := l.attempts[key]
	if attempt.windowStart.IsZero() || now.Sub(attempt.windowStart) >= l.failureWindow {
		attempt = loginAttempt{windowStart: now}
	}
	attempt.failures++
	attempt.lastSeen = now
	if attempt.failures >= l.failureLimit {
		attempt.blockedUntil = now.Add(l.blockDuration)
		l.attempts[key] = attempt
		return l.blockDuration, true
	}
	if _, exists := l.attempts[key]; !exists && len(l.attempts) >= l.maxTracked {
		l.evictOldest()
	}
	l.attempts[key] = attempt
	return 0, false
}

func (l *loginLimiter) recordSuccess(key string) {
	l.mu.Lock()
	delete(l.attempts, key)
	l.mu.Unlock()
}

func (l *loginLimiter) evictOldest() {
	oldestKey := ""
	var oldest time.Time
	for key, attempt := range l.attempts {
		if oldestKey == "" || attempt.lastSeen.Before(oldest) {
			oldestKey, oldest = key, attempt.lastSeen
		}
	}
	delete(l.attempts, oldestKey)
}

func writeLoginRateLimit(w http.ResponseWriter, retry time.Duration) {
	seconds := int64((retry + time.Second - 1) / time.Second)
	if seconds < 1 {
		seconds = 1
	}
	w.Header().Set("Retry-After", strconv.FormatInt(seconds, 10))
	writeProtocolError(w, http.StatusTooManyRequests, "登录尝试过于频繁，请稍后重试")
}

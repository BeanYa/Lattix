package panel

import (
	"errors"
	"net/http"
	"strconv"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
)

const (
	loginFailureLimit      = 5
	loginFailureWindow     = time.Minute
	loginBlockDuration     = 5 * time.Minute
	maxTrackedLoginIPs     = 4096
	bcryptCheckConcurrency = 4
)

var errBcryptBusy = errors.New("bcrypt concurrency limit reached")

type loginAttempt struct {
	failures     int
	windowStart  time.Time
	blockedUntil time.Time
	lastSeen     time.Time
}

type loginLimiter struct {
	mu       sync.Mutex
	attempts map[string]loginAttempt
	now      func() time.Time
}

func newLoginLimiter(now func() time.Time) *loginLimiter {
	return &loginLimiter{attempts: make(map[string]loginAttempt), now: now}
}

func (s *Server) initAuthProtection() {
	s.authOnce.Do(func() {
		s.loginAttempts = newLoginLimiter(time.Now)
		s.bcryptSlots = make(chan struct{}, bcryptCheckConcurrency)
	})
}

func (s *Server) loginLimiter() *loginLimiter {
	s.initAuthProtection()
	return s.loginAttempts
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

func (l *loginLimiter) retryAfter(ip string) (time.Duration, bool) {
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()
	attempt, ok := l.attempts[ip]
	if !ok {
		return 0, false
	}
	attempt.lastSeen = now
	if attempt.blockedUntil.After(now) {
		l.attempts[ip] = attempt
		return attempt.blockedUntil.Sub(now), true
	}
	if now.Sub(attempt.windowStart) >= loginFailureWindow {
		delete(l.attempts, ip)
		return 0, false
	}
	l.attempts[ip] = attempt
	return 0, false
}

func (l *loginLimiter) recordFailure(ip string) (time.Duration, bool) {
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()
	attempt := l.attempts[ip]
	if attempt.windowStart.IsZero() || now.Sub(attempt.windowStart) >= loginFailureWindow {
		attempt = loginAttempt{windowStart: now}
	}
	attempt.failures++
	attempt.lastSeen = now
	if attempt.failures >= loginFailureLimit {
		attempt.blockedUntil = now.Add(loginBlockDuration)
		l.attempts[ip] = attempt
		return loginBlockDuration, true
	}
	if _, exists := l.attempts[ip]; !exists && len(l.attempts) >= maxTrackedLoginIPs {
		l.evictOldest()
	}
	l.attempts[ip] = attempt
	return 0, false
}

func (l *loginLimiter) recordSuccess(ip string) {
	l.mu.Lock()
	delete(l.attempts, ip)
	l.mu.Unlock()
}

func (l *loginLimiter) evictOldest() {
	oldestIP := ""
	var oldest time.Time
	for ip, attempt := range l.attempts {
		if oldestIP == "" || attempt.lastSeen.Before(oldest) {
			oldestIP, oldest = ip, attempt.lastSeen
		}
	}
	delete(l.attempts, oldestIP)
}

func writeLoginRateLimit(w http.ResponseWriter, retry time.Duration) {
	seconds := int64((retry + time.Second - 1) / time.Second)
	if seconds < 1 {
		seconds = 1
	}
	w.Header().Set("Retry-After", strconv.FormatInt(seconds, 10))
	writeProtocolError(w, http.StatusTooManyRequests, "登录尝试过于频繁，请稍后重试")
}

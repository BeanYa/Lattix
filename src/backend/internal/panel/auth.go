package panel

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"lattix/backend/internal/logging"
	"lattix/shared"
)

// sessionTTL 是登录会话有效期。
const sessionTTL = 7 * 24 * time.Hour

// sessionCookie 是会话 cookie 名。
const sessionCookie = "lattix_session"

// sessionSecret 由管理员凭证派生（不加存储；改密码即全部会话失效）。
func (s *Server) sessionSecret(ctx context.Context) ([]byte, error) {
	credential, err := s.credentialKey(ctx)
	if err != nil {
		return nil, err
	}
	return sessionSecretForCredential(credential), nil
}

func sessionSecretForCredential(credential string) []byte {
	sum := sha256.Sum256([]byte(credential + "|lattix-session"))
	return sum[:]
}

// signSession 生成签名会话值：base64url(user|exp).base64url(hmac)。
func signSession(user string, exp time.Time, secret []byte) string {
	payload := fmt.Sprintf("%s|%d", user, exp.Unix())
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString([]byte(payload)) + "." +
		base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// verifySession 校验签名会话值，返回用户名。
func (s *Server) verifySession(ctx context.Context, value string) (string, bool, error) {
	parts := strings.Split(value, ".")
	if len(parts) != 2 {
		return "", false, nil
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return "", false, nil
	}
	secret, err := s.sessionSecret(ctx)
	if err != nil {
		return "", false, err
	}
	mac := hmac.New(sha256.New, secret)
	mac.Write(payload)
	expected := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(expected), []byte(parts[1])) {
		return "", false, nil
	}
	user, expStr, ok := strings.Cut(string(payload), "|")
	if !ok {
		return "", false, nil
	}
	exp, err := strconv.ParseInt(expStr, 10, 64)
	if err != nil || time.Now().Unix() > exp {
		return "", false, nil
	}
	return user, true, nil
}

// handleLogin 处理 POST /api/login：账号密码登录（§10），签发会话 cookie。
func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := readJSON(r, &req); err != nil {
		writeProtocolError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	ip := logging.ClientIP(r)
	if retry, blocked := s.loginLimiter().retryAfter(ip); blocked {
		writeLoginRateLimit(w, retry)
		return
	}
	passwordOK, credential, authErr := s.authenticatePassword(r.Context(), req.Password)
	if authErr != nil {
		if errors.Is(authErr, errBcryptBusy) {
			writeLoginRateLimit(w, time.Second)
			return
		}
		log.Printf("panel: login credential lookup request_id=%s: %v", logging.RequestID(r.Context()), authErr)
		writeRPC(w, shared.CodeServiceUnavailable, "登录服务暂时不可用", nil)
		return
	}
	if !hmac.Equal([]byte(req.Username), []byte(s.cfg.AdminUser)) || !passwordOK {
		if err := s.recordOperation(r.Context(), logging.OperationEvent{
			Severity: logging.SeverityWarning, Category: logging.CategoryAuth,
			Action: "auth.login_failed", Detail: map[string]string{"username": req.Username},
			Operator: req.Username, IP: logging.ClientIP(r), RequestID: logging.RequestID(r.Context()),
			TraceID: logging.TraceID(r.Context()),
		}); err != nil {
			log.Printf("panel: record login_failed event: %v", err)
		}
		if retry, blocked := s.loginLimiter().recordFailure(ip); blocked {
			writeLoginRateLimit(w, retry)
			return
		}
		writeRPC(w, shared.CodeAuthInvalidCredentials, "用户名或密码错误", nil)
		return
	}
	s.loginLimiter().recordSuccess(ip)
	secret := sessionSecretForCredential(credential)
	sessionValue := signSession(req.Username, time.Now().Add(sessionTTL), secret)
	csrfToken := csrfForSessionSecret(sessionValue, secret)
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    sessionValue,
		Path:     "/",
		HttpOnly: true,
		Secure:   s.isSecure(r), // HTTPS（含反代）下仅限加密通道回传
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(sessionTTL.Seconds()),
	})
	if err := s.recordOperation(r.Context(), logging.OperationEvent{
		Severity: logging.SeverityInfo, Category: logging.CategoryAuth, Action: "auth.login",
		Operator: req.Username, IP: logging.ClientIP(r), RequestID: logging.RequestID(r.Context()),
		TraceID: logging.TraceID(r.Context()),
	}); err != nil {
		log.Printf("panel: record login event: %v", err)
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"username": req.Username, "csrf_token": csrfToken,
	})
}

// handleLogout 处理 POST /api/logout：清除会话 cookie。
func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if err := readJSON(r, &struct{}{}); err != nil {
		writeProtocolError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	s.audit(r, "auth.logout", nil, nil, nil)
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		MaxAge:   -1,
	})
	writeJSON(w, http.StatusOK, nil)
}

// handleMe 处理 GET /api/me：返回当前登录用户（前端判定登录态）。
func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	user, _ := s.currentUser(r)
	cookie, err := r.Cookie(sessionCookie)
	if err != nil {
		writeRPC(w, shared.CodeAuthRequired, "未登录或会话已过期", nil)
		return
	}
	csrfToken, err := s.csrfForSession(r.Context(), cookie.Value)
	if err != nil {
		log.Printf("panel: create csrf request_id=%s: %v", logging.RequestID(r.Context()), err)
		writeRPC(w, shared.CodeServiceUnavailable, "认证服务暂时不可用", nil)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"username": user, "csrf_token": csrfToken,
	})
}

// currentUser 从请求 cookie 解析当前用户。
func (s *Server) currentUser(r *http.Request) (string, bool) {
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return "", false
	}
	user, ok, err := s.verifySession(r.Context(), c.Value)
	if err != nil {
		log.Printf("panel: verify session request_id=%s: %v", logging.RequestID(r.Context()), err)
		return "", false
	}
	return user, ok
}

// requireAuth 是会话校验中间件。
// 面板自更新进行中拒绝其余 API 操作（423 Locked），防止用户在二进制/前端
// 切换窗口内继续改动；仅放行更新进度轮询与登录态探活（前端等待重启用）。
func (s *Server) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := s.currentUser(r); !ok {
			writeRPC(w, shared.CodeAuthRequired, "未登录或会话已过期", nil)
			return
		}
		if s.upd != nil && s.upd.running() &&
			r.URL.Path != "/api/panel/get-update-status" && r.URL.Path != "/api/auth/me" {
			writeRPC(w, shared.CodeUpdateInProgress, "面板更新进行中，请稍候", nil)
			return
		}
		next(w, r)
	}
}

func (s *Server) csrfForSession(ctx context.Context, sessionValue string) (string, error) {
	secret, err := s.sessionSecret(ctx)
	if err != nil {
		return "", err
	}
	return csrfForSessionSecret(sessionValue, secret), nil
}

func csrfForSessionSecret(sessionValue string, secret []byte) string {
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte("csrf|" + sessionValue))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (s *Server) requireCSRF(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(sessionCookie)
		if err != nil {
			writeRPC(w, shared.CodeAuthRequired, "未登录或会话已过期", nil)
			return
		}
		expected, err := s.csrfForSession(r.Context(), cookie.Value)
		if err != nil {
			log.Printf("panel: verify csrf request_id=%s: %v", logging.RequestID(r.Context()), err)
			writeRPC(w, shared.CodeServiceUnavailable, "认证服务暂时不可用", nil)
			return
		}
		actual := r.Header.Get("X-CSRF-Token")
		if actual == "" || !hmac.Equal([]byte(expected), []byte(actual)) {
			writeRPC(w, shared.CodeAuthRequired, "CSRF token 无效", nil)
			return
		}
		next(w, r)
	}
}

func (s *Server) requireSameOrigin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		raw := r.Header.Get("Origin")
		if raw == "" {
			raw = r.Referer()
		}
		parsed, err := url.Parse(raw)
		if err != nil || parsed.Host == "" || !strings.EqualFold(parsed.Host, r.Host) {
			writeRPC(w, shared.CodeAuthRequired, "请求来源校验失败", nil)
			return
		}
		if s.isSecure(r) && parsed.Scheme != "https" {
			writeRPC(w, shared.CodeAuthRequired, "请求来源校验失败", nil)
			return
		}
		next(w, r)
	}
}

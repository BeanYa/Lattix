package panel

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// sessionTTL 是登录会话有效期。
const sessionTTL = 7 * 24 * time.Hour

// sessionCookie 是会话 cookie 名。
const sessionCookie = "lattix_session"

// sessionSecret 由管理员密码派生（不加存储；改密码即全部会话失效）。
func (s *Server) sessionSecret() []byte {
	sum := sha256.Sum256([]byte(s.cfg.AdminPass + "|lattix-session"))
	return sum[:]
}

// signSession 生成签名会话值：base64url(user|exp).base64url(hmac)。
func (s *Server) signSession(user string, exp time.Time) string {
	payload := fmt.Sprintf("%s|%d", user, exp.Unix())
	mac := hmac.New(sha256.New, s.sessionSecret())
	mac.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString([]byte(payload)) + "." +
		base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// verifySession 校验签名会话值，返回用户名。
func (s *Server) verifySession(value string) (string, bool) {
	parts := strings.Split(value, ".")
	if len(parts) != 2 {
		return "", false
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return "", false
	}
	mac := hmac.New(sha256.New, s.sessionSecret())
	mac.Write(payload)
	expected := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(expected), []byte(parts[1])) {
		return "", false
	}
	user, expStr, ok := strings.Cut(string(payload), "|")
	if !ok {
		return "", false
	}
	exp, err := strconv.ParseInt(expStr, 10, 64)
	if err != nil || time.Now().Unix() > exp {
		return "", false
	}
	return user, true
}

// handleLogin 处理 POST /api/login：账号密码登录（§10），签发会话 cookie。
func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Username != s.cfg.AdminUser || req.Password != s.cfg.AdminPass {
		writeError(w, http.StatusUnauthorized, "用户名或密码错误")
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    s.signSession(req.Username, time.Now().Add(sessionTTL)),
		Path:     "/",
		HttpOnly: true,
		Secure:   s.isSecure(r), // HTTPS（含反代）下仅限加密通道回传
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(sessionTTL.Seconds()),
	})
	writeJSON(w, http.StatusOK, map[string]string{"username": req.Username})
}

// handleLogout 处理 POST /api/logout：清除会话 cookie。
func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		MaxAge:   -1,
	})
	w.WriteHeader(http.StatusNoContent)
}

// handleMe 处理 GET /api/me：返回当前登录用户（前端判定登录态）。
func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	user, _ := s.currentUser(r)
	writeJSON(w, http.StatusOK, map[string]string{"username": user})
}

// currentUser 从请求 cookie 解析当前用户。
func (s *Server) currentUser(r *http.Request) (string, bool) {
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return "", false
	}
	return s.verifySession(c.Value)
}

// requireAuth 是会话校验中间件。
func (s *Server) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := s.currentUser(r); !ok {
			writeError(w, http.StatusUnauthorized, "未登录或会话已过期")
			return
		}
		next(w, r)
	}
}

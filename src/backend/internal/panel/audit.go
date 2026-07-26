package panel

import (
	"log"
	"net/http"
	"strings"

	"lattix/backend/internal/store"
)

// audit 记录一条 admin 类事件日志（§log）。
// 在写操作成功路径上调用（失败路径已 writeError 提前返回）；审计写入失败仅记日志，
// 不阻断业务——审计是旁路，不应让记日志失败回滚已成功的业务操作。
//
// action 语义形如 "server.create" / "node.delete" / "settings.update" / "admin.login"；
// serverID/nodeID 为 nil 表示无关联对象；detail 任意值（map/struct/string）会 json.Marshal。
func (s *Server) audit(r *http.Request, action string, serverID, nodeID *int64, detail any) {
	operator, _ := s.currentUser(r)
	if err := s.st.RecordEvent(r.Context(), store.EventCategoryAdmin, action,
		serverID, nodeID, detail, operator, clientIP(r)); err != nil {
		log.Printf("panel: audit %s: %v", action, err)
	}
}

// clientIP 取请求来源 IP：优先 X-Forwarded-For 首 IP（受信回环反代场景，§9），
// 否则回退 RemoteAddr（去除端口）。
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if idx := strings.IndexByte(xff, ','); idx >= 0 {
			return strings.TrimSpace(xff[:idx])
		}
		return strings.TrimSpace(xff)
	}
	host := r.RemoteAddr
	if i := strings.LastIndexByte(host, ':'); i >= 0 {
		// 去除端口（IPv6 形如 [::1]:port 也兼容）。
		if strings.HasPrefix(host, "[") {
			if j := strings.LastIndexByte(host, ']'); j >= 0 {
				return host[1:j]
			}
		} else {
			return host[:i]
		}
	}
	return host
}

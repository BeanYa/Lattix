package panel

import (
	"fmt"
	"net/http"
	"time"

	"lattix/backend/internal/store"
)

// serverDTO 是服务器对象的 API 表示。
type serverDTO struct {
	ID          int64      `json:"id"`
	Alias       string     `json:"alias"`
	Online      bool       `json:"online"` // 由 WS 连接存在性推导（§5）
	LastSeenAt  *time.Time `json:"last_seen_at"`
	XrayVersion string     `json:"xray_version"`
	Address     string     `json:"address"` // 公网地址（hello 记录，订阅用，§9）
	CreatedAt   time.Time  `json:"created_at"`
}

func (s *Server) toServerDTO(srv store.Server) serverDTO {
	return serverDTO{
		ID:          srv.ID,
		Alias:       srv.Alias,
		Online:      s.req.IsOnline(srv.ID),
		LastSeenAt:  srv.LastSeenAt,
		XrayVersion: srv.XrayVersion,
		Address:     srv.Address,
		CreatedAt:   srv.CreatedAt,
	}
}

// handleListServers 处理 GET /api/servers。
func (s *Server) handleListServers(w http.ResponseWriter, r *http.Request) {
	servers, err := s.st.ListServers(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]serverDTO, 0, len(servers))
	for _, srv := range servers {
		out = append(out, s.toServerDTO(srv))
	}
	writeJSON(w, http.StatusOK, out)
}

// handleCreateServer 处理 POST /api/servers：生成一次性 bootstrap token 与一行安装命令（§11）。
func (s *Server) handleCreateServer(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Alias string `json:"alias"`
	}
	if err := readJSON(r, &req); err != nil || req.Alias == "" {
		writeError(w, http.StatusBadRequest, "alias 不能为空")
		return
	}
	bootstrap := randomHex(16)
	id, err := s.st.CreateServer(r.Context(), req.Alias, bootstrap)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	srv, err := s.st.ServerByID(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	base := s.panelBase(r)
	writeJSON(w, http.StatusCreated, map[string]any{
		"server":          s.toServerDTO(*srv),
		"bootstrap_token": bootstrap,
		"install_command": fmt.Sprintf(
			"curl -fsSL %s/install.sh | bash -s -- --panel %s --token %s", base, base, bootstrap),
	})
}

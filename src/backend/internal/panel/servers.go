package panel

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"lattix/backend/internal/store"
	"lattix/shared"
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
		Alias       string `json:"alias"`
		Address     string `json:"address"`      // 公网地址，留空自动学习（§4）
		XrayVersion string `json:"xray_version"` // 默认 latest（§11）
	}
	if err := readJSON(r, &req); err != nil || req.Alias == "" {
		writeError(w, http.StatusBadRequest, "alias 不能为空")
		return
	}
	if req.XrayVersion == "" {
		req.XrayVersion = "latest"
	}
	bootstrap := randomHex(16)
	id, err := s.st.CreateServer(r.Context(), req.Alias, req.Address, bootstrap)
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
		"install_command": installCommand(base, bootstrap, req.XrayVersion),
	})
}

// handleRotateToken 处理 POST /api/servers/{id}/rotate-token：
// 换发新 bootstrap token 并重置回 bootstrap 状态（旧凭证含长期 token 立即失效），
// 返回一行安装命令（§10/§11）。
func (s *Server) handleRotateToken(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid server id")
		return
	}
	srv, err := s.st.ServerByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "服务器不存在")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	bootstrap := randomHex(16)
	if err := s.st.ResetServerBootstrap(r.Context(), srv.ID, bootstrap); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	base := s.panelBase(r)
	srv.Token = bootstrap
	srv.LastSeenAt = nil
	writeJSON(w, http.StatusOK, map[string]any{
		"server":          s.toServerDTO(*srv),
		"bootstrap_token": bootstrap,
		"install_command": installCommand(base, bootstrap, "latest"),
	})
}

// installCommand 生成一行安装命令（§11）：xray 版本随命令携带（latest 由 install.sh 解析）。
func installCommand(base, token, xrayVersion string) string {
	return fmt.Sprintf(
		"curl -fsSL %s/install.sh | bash -s -- --panel %s --token %s --xray-version %s",
		base, base, token, xrayVersion)
}

// handleDeleteServer 处理 DELETE /api/servers/{id}：
// 在线则先下发 uninstall 命令（agent 自卸载），随后级联删除记录（§10）。
// 离线服务器仅删除记录（agent 需手动清理）。
func (s *Server) handleDeleteServer(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid server id")
		return
	}
	if _, err := s.st.ServerByID(r.Context(), id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "服务器不存在")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if s.req.IsOnline(id) {
		// purge 参数：xray = 连同 xray 卸载（默认），agent = 仅卸载 agent（§5/§10）。
		payload := shared.UninstallPayload{PurgeXray: r.URL.Query().Get("purge") != "agent"}
		if _, err := s.disp.Enqueue(r.Context(), id, shared.TypeUninstall, payload); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	if err := s.st.DeleteServerCascade(r.Context(), id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

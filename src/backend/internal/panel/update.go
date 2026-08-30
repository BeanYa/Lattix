package panel

// 面板自更新的 HTTP 适配：更新状态机与下载/校验/替换流程在 selfupdate 包，
// 这里只做请求解析、生命周期栅栏、审计与响应写回。

import (
	"context"
	"net/http"
	"strings"
	"time"

	"lattix/shared"
)

// handlePanelVersion 处理 GET /api/panel/version：以 GitHub release 最新版本检测更新。
func (s *Server) handlePanelVersion(w http.ResponseWriter, r *http.Request) {
	info, err := s.upd.CheckVersion(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, info)
}

// handlePanelUpdateStart 处理 POST /api/panel/update：启动自更新（异步）。
// body 可指定 {"version":"vX.Y.Z","force":true}，缺省更新到 latest。
// force=true 时即使版本号相同也执行覆盖安装。
func (s *Server) handlePanelUpdateStart(w http.ResponseWriter, r *http.Request) {
	if !s.upd.CanUpdate() {
		writeError(w, http.StatusBadRequest, "dev 构建无对应 release，无法自更新")
		return
	}
	var req struct {
		Version string `json:"version"`
		Force   bool   `json:"force"`
	}
	if err := readJSON(r, &req); err != nil {
		writeProtocolError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if _, ok := s.upd.Begin(req.Version, req.Force); !ok {
		writeError(w, http.StatusConflict, "面板更新已在进行中")
		return
	}
	barrierCtx, cancelBarrier := context.WithTimeout(r.Context(), 5*time.Second)
	_, lifecycleErr := s.transitionLifecycle(barrierCtx, shared.PanelStateUpdating, "", true)
	cancelBarrier()
	if lifecycleErr != nil {
		s.upd.AbortStart(lifecycleErr)
		writeError(w, http.StatusInternalServerError, lifecycleErr.Error())
		return
	}

	s.audit(r, "panel.update_started", nil, nil, map[string]any{
		"current_version": s.cfg.Version,
		"target_version":  strings.TrimSpace(req.Version),
		"force":           req.Force,
	})
	s.upd.RunAsync(s.cfg.LifecycleContext)
	writeJSON(w, http.StatusAccepted, s.upd.Snapshot())
}

// handlePanelUpdateStatus 处理 GET /api/panel/update/status（更新进行中豁免 423 守卫）。
func (s *Server) handlePanelUpdateStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.upd.Snapshot())
}

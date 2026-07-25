package panel

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"lattix/backend/internal/store"
	"lattix/shared"
)

// metricsDTO 是主机指标的 API 表示（§13）。
type metricsDTO struct {
	Load1      float64 `json:"load1"`
	CPUPercent float64 `json:"cpu_percent"`
	MemTotal   uint64  `json:"mem_total"`
	MemUsed    uint64  `json:"mem_used"`
}

// serverDTO 是服务器对象的 API 表示。
type serverDTO struct {
	ID            int64       `json:"id"`
	Alias         string      `json:"alias"`
	Online        bool        `json:"online"` // 由 WS 连接存在性推导（§5）
	LastSeenAt    *time.Time  `json:"last_seen_at"`
	XrayVersion   string      `json:"xray_version"`
	AgentVersion  string      `json:"agent_version"`  // hello 上报的 agent 版本
	UpgradeNeeded bool        `json:"upgrade_needed"` // agent 落后出兼容窗口，需升级（§18）
	Address       string      `json:"address"`        // 公网地址（hello 记录，订阅用，§9）
	ConfigDrift   bool        `json:"config_drift"`   // 配置漂移标志（§17）
	Metrics       *metricsDTO `json:"metrics"`        // 主机遥测最新值（§13），无数据为 null
	CreatedAt     time.Time   `json:"created_at"`
}

func (s *Server) toServerDTO(srv store.Server) serverDTO {
	return serverDTO{
		ID:            srv.ID,
		Alias:         srv.Alias,
		Online:        s.req.IsOnline(srv.ID),
		LastSeenAt:    srv.LastSeenAt,
		XrayVersion:   srv.XrayVersion,
		AgentVersion:  srv.AgentVersion,
		UpgradeNeeded: srv.UpgradeNeeded,
		Address:       srv.Address,
		ConfigDrift:   srv.ConfigDrift,
		CreatedAt:     srv.CreatedAt,
	}
}

// handleListServers 处理 GET /api/servers。
func (s *Server) handleListServers(w http.ResponseWriter, r *http.Request) {
	servers, err := s.st.ListServers(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	metrics, err := s.st.ServerMetricsMap(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]serverDTO, 0, len(servers))
	for _, srv := range servers {
		dto := s.toServerDTO(srv)
		if m, ok := metrics[srv.ID]; ok {
			dto.Metrics = &metricsDTO{Load1: m.Load1, CPUPercent: m.CPUPercent, MemTotal: m.MemTotal, MemUsed: m.MemUsed}
		}
		out = append(out, dto)
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
		"install_command": s.installCommand(base, bootstrap, req.XrayVersion),
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
		"install_command": s.installCommand(base, bootstrap, "latest"),
	})
}

// handleUpdateServer 处理 PATCH /api/servers/{id}：管理员修改公网地址（§4/§9）。
// 地址一经写入不再被 hello 覆盖；置空则下次 hello 按 RemoteAddr 重新自动学习。
func (s *Server) handleUpdateServer(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid server id")
		return
	}
	var req struct {
		Address string `json:"address"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
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
	if err := s.st.UpdateServerAddress(r.Context(), id, req.Address); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	srv, err := s.st.ServerByID(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, s.toServerDTO(*srv))
}

// handleUpgradeXray 处理 POST /api/servers/{id}/upgrade（§18 版本升级管理）：
// 下发 upgrade_xray 命令（离线服务器留队列补发），agent 完成后经 telemetry 刷新版本号。
func (s *Server) handleUpgradeXray(w http.ResponseWriter, r *http.Request) {
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
	var req struct {
		Version string `json:"version"` // vX.Y.Z 或 latest（默认）
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Version == "" {
		req.Version = "latest"
	}
	if req.Version != "latest" && !strings.HasPrefix(req.Version, "v") {
		writeError(w, http.StatusBadRequest, "版本号须形如 vX.Y.Z 或 latest")
		return
	}
	cmdID, err := s.disp.Enqueue(r.Context(), id, shared.TypeUpgradeXray, shared.UpgradeXrayPayload{Version: req.Version})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"command_id": cmdID, "version": req.Version})
}

// handleUpgradeAgent 处理 POST /api/servers/{id}/upgrade-agent（§18 版本升级管理）：
// 下发 upgrade_agent 命令，agent 从 GitHub release 下载对应版本二进制，
// 校验 checksums.txt 后原子自替换并退出（systemd 拉起即完成升级）。
// 兼容窗口外（upgrade_needed）的服务器经此命令收敛回窗口内。
func (s *Server) handleUpgradeAgent(w http.ResponseWriter, r *http.Request) {
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
	var req struct {
		Version string `json:"version"` // vX.Y.Z 或 latest（默认）
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Version == "" {
		req.Version = "latest"
	}
	if req.Version != "latest" && !strings.HasPrefix(req.Version, "v") {
		writeError(w, http.StatusBadRequest, "版本号须形如 vX.Y.Z 或 latest")
		return
	}
	payload := shared.UpgradeAgentPayload{Version: req.Version}
	if s.cfg.GitHubRepo != "" {
		payload.ReleaseBase = "https://github.com/" + s.cfg.GitHubRepo + "/releases/download"
	}
	cmdID, err := s.disp.Enqueue(r.Context(), id, shared.TypeUpgradeAgent, payload)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"command_id": cmdID, "version": req.Version})
}

// handleRepairServer 处理 POST /api/servers/{id}/repair（§17 配置漂移修复）：
// 重放该服务器全部 active 节点的 apply_node，agent 据此重建配置并清除漂移标志。
func (s *Server) handleRepairServer(w http.ResponseWriter, r *http.Request) {
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
	nodes, err := s.st.ListNodes(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	reapplied := 0
	for _, n := range nodes {
		if n.ServerID != id || n.Status != store.NodeStatusActive {
			continue
		}
		var vc shared.VirtualConfig
		if err := json.Unmarshal(n.ConfigTemplate, &vc); err != nil {
			continue // 模板损坏的节点跳过（异常留在 nodes 表）
		}
		if err := s.enqueueApply(r, id, n.ID, vc); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		reapplied++
	}
	writeJSON(w, http.StatusOK, map[string]int{"reapplied": reapplied})
}

// installCommand 生成一行安装命令（§11）：xray 版本随命令携带（latest 由 install.sh 解析）。
// 正式版本（version 非 dev）时 install.sh 钉到面板同版本的 GitHub release 资产——
// 脚本与其安装的 agent 二进制天然同版，老面板生成的命令不受后续发版影响（不可变性）；
// dev 构建回退面板托管模式。
func (s *Server) installCommand(base, token, xrayVersion string) string {
	scriptURL := base + "/install.sh"
	if s.cfg.Version != "" && s.cfg.Version != "dev" && s.cfg.GitHubRepo != "" {
		scriptURL = fmt.Sprintf("https://github.com/%s/releases/download/%s/install.sh",
			s.cfg.GitHubRepo, s.cfg.Version)
	}
	return fmt.Sprintf(
		"curl -fsSL %s | bash -s -- --panel %s --token %s --xray-version %s",
		scriptURL, base, token, xrayVersion)
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

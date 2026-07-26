package panel

import (
	"context"
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
	ID            int64              `json:"id"`
	Alias         string             `json:"alias"`
	Online        bool               `json:"online"` // 由 WS 连接存在性推导（§5）
	LastSeenAt    *time.Time         `json:"last_seen_at"`
	XrayVersion   string             `json:"xray_version"`
	AgentVersion  string             `json:"agent_version"`  // hello 上报的 agent 版本
	UpgradeNeeded bool               `json:"upgrade_needed"` // agent 落后出兼容窗口，需升级（§18）
	Address       string             `json:"address"`        // 公网地址（hello 记录，订阅用，§9）
	LearnedAddr   string             `json:"learned_addr"`   // 拨入学习地址（受信回环代理时取 XFF 首 IP，§9），编辑地址时的内置候选
	NICAddresses  []string           `json:"nic_addresses"`  // agent 上报的网卡非回环地址（§9），编辑地址时的内置候选
	ConfigDrift   bool               `json:"config_drift"`   // 配置漂移标志（§17）
	MachineType   string             `json:"machine_type"`   // direct|nat（§21）
	AllowedPorts  []shared.PortRange `json:"allowed_ports"`  // NAT 可用端口段（§21），空 = 无段（仅出口档/direct）
	Metrics       *metricsDTO        `json:"metrics"`        // 主机遥测最新值（§13），无数据为 null
	CreatedAt     time.Time          `json:"created_at"`
}

func (s *Server) toServerDTO(srv store.Server) serverDTO {
	ranges, err := shared.ParsePortRanges(srv.AllowedPorts)
	if err != nil {
		ranges = nil // 存储值损坏不阻断列表（异常留在 servers 表）
	}
	if ranges == nil {
		ranges = []shared.PortRange{}
	}
	var nicAddrs []string
	if srv.NICAddresses != "" {
		if err := json.Unmarshal([]byte(srv.NICAddresses), &nicAddrs); err != nil {
			nicAddrs = nil // 存储值损坏不阻断列表（异常留在 servers 表）
		}
	}
	if nicAddrs == nil {
		nicAddrs = []string{}
	}
	return serverDTO{
		ID:            srv.ID,
		Alias:         srv.Alias,
		Online:        s.req.IsOnline(srv.ID),
		LastSeenAt:    srv.LastSeenAt,
		XrayVersion:   srv.XrayVersion,
		AgentVersion:  srv.AgentVersion,
		UpgradeNeeded: srv.UpgradeNeeded,
		Address:       srv.Address,
		LearnedAddr:   srv.LearnedAddr,
		NICAddresses:  nicAddrs,
		ConfigDrift:   srv.ConfigDrift,
		MachineType:   srv.MachineType,
		AllowedPorts:  ranges,
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
// 机器类型与 NAT 可用端口段为面板侧元数据（§21，不下发到 agent，引导流程不变）：
// NAT 类型 address 强制必填（共享 IP 由 IDC 提供，禁用自动学习）。
func (s *Server) handleCreateServer(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Alias        string             `json:"alias"`
		Address      string             `json:"address"`       // 公网地址，留空自动学习（§4；NAT 必填）
		XrayVersion  string             `json:"xray_version"`  // 默认 latest（§11）
		MachineType  string             `json:"machine_type"`  // direct（默认）| nat（§21）
		AllowedPorts []shared.PortRange `json:"allowed_ports"` // NAT 可用端口段（§21），留空 = 仅出口档
	}
	if err := readJSON(r, &req); err != nil || req.Alias == "" {
		writeError(w, http.StatusBadRequest, "alias 不能为空")
		return
	}
	if req.XrayVersion == "" {
		req.XrayVersion = "latest"
	}
	if req.MachineType == "" {
		req.MachineType = store.MachineTypeDirect
	}
	if req.MachineType != store.MachineTypeDirect && req.MachineType != store.MachineTypeNAT {
		writeError(w, http.StatusBadRequest, "machine_type 须为 direct 或 nat")
		return
	}
	if err := shared.ValidatePortRanges(req.AllowedPorts); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.MachineType == store.MachineTypeNAT && req.Address == "" {
		writeError(w, http.StatusBadRequest, "NAT 服务器必须填写公网地址（共享 IP 由 IDC 提供）")
		return
	}
	allowedJSON, err := marshalPortRanges(req.AllowedPorts)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	bootstrap := randomHex(16)
	id, err := s.st.CreateServer(r.Context(), req.Alias, req.Address, bootstrap, req.MachineType, allowedJSON)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	srv, err := s.st.ServerByID(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	sid := srv.ID
	s.audit(r, "server.create", &sid, nil, map[string]any{
		"alias": req.Alias, "machine_type": req.MachineType, "address": req.Address,
	})
	base := s.panelBase(r)
	writeJSON(w, http.StatusCreated, map[string]any{
		"server":          s.toServerDTO(*srv),
		"bootstrap_token": bootstrap,
		"install_command": s.installCommand(r.Context(), base, bootstrap, req.XrayVersion),
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
	sid := srv.ID
	s.audit(r, "server.rotate_token", &sid, nil, map[string]string{"alias": srv.Alias})
	base := s.panelBase(r)
	srv.Token = bootstrap
	srv.LastSeenAt = nil
	writeJSON(w, http.StatusOK, map[string]any{
		"server":          s.toServerDTO(*srv),
		"bootstrap_token": bootstrap,
		"install_command": s.installCommand(r.Context(), base, bootstrap, "latest"),
	})
}

// handleUpdateServer 处理 PATCH /api/servers/{id}：管理员修改公网地址（§4/§9）与
// NAT 可用端口段（§21）。地址一经写入不再被 hello 覆盖；置空则下次 hello 按 RemoteAddr
// 重新自动学习（NAT 类型禁止置空）。机器类型建后不允许互转；端口段收窄时校验
// 该 server 存量节点 realized 端口与链跳端口不越界，越界 400。
func (s *Server) handleUpdateServer(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid server id")
		return
	}
	var req struct {
		Address      string              `json:"address"`
		MachineType  string              `json:"machine_type"`  // 不允许互转：带不同值 → 400
		AllowedPorts *[]shared.PortRange `json:"allowed_ports"` // 省略 = 不变；显式 null/数组 = 整体替换
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
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
	if req.MachineType != "" && req.MachineType != srv.MachineType {
		writeError(w, http.StatusBadRequest, "机器类型建后不允许互转")
		return
	}
	if srv.MachineType == store.MachineTypeNAT && req.Address == "" {
		writeError(w, http.StatusBadRequest, "NAT 服务器必须填写公网地址（共享 IP 由 IDC 提供）")
		return
	}
	if req.AllowedPorts != nil {
		if err := shared.ValidatePortRanges(*req.AllowedPorts); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if err := s.checkPortsShrink(r, id, *req.AllowedPorts); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		allowedJSON, err := marshalPortRanges(*req.AllowedPorts)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if err := s.st.UpdateServerAllowedPorts(r.Context(), id, allowedJSON); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	if err := s.st.UpdateServerAddress(r.Context(), id, req.Address); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	srv, err = s.st.ServerByID(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	sid := id
	s.audit(r, "server.update", &sid, nil, map[string]any{
		"address": req.Address, "allowed_ports_changed": req.AllowedPorts != nil,
	})
	writeJSON(w, http.StatusOK, s.toServerDTO(*srv))
}

// checkPortsShrink 校验端口段收窄后存量使用不越界（§21）：
// 该 server 节点的指定/realized 端口与链跳 forward/portal 端口（监听侧）须仍在新段内。
func (s *Server) checkPortsShrink(r *http.Request, serverID int64, ranges []shared.PortRange) error {
	nodes, err := s.st.ListNodes(r.Context())
	if err != nil {
		return err
	}
	for _, n := range nodes {
		if n.ServerID != serverID {
			continue
		}
		if n.Port != nil && !shared.InListenRanges(ranges, *n.Port) {
			return fmt.Errorf("端口段收窄后节点 %d 指定端口 %d 越界", n.ID, *n.Port)
		}
		if len(n.RealizedConfig) > 0 {
			var rc shared.RealizedConfig
			if err := json.Unmarshal(n.RealizedConfig, &rc); err == nil && rc.Port != 0 &&
				!shared.InListenRanges(ranges, rc.Port) {
				return fmt.Errorf("端口段收窄后节点 %d realized 端口 %d 越界", n.ID, rc.Port)
			}
		}
	}
	hops, err := s.st.ChainHopsByServerID(r.Context(), serverID)
	if err != nil {
		return err
	}
	for _, h := range hops {
		if h.ForwardPort != 0 && !shared.InListenRanges(ranges, h.ForwardPort) {
			return fmt.Errorf("端口段收窄后链跳 %d forward 端口 %d 越界", h.ID, h.ForwardPort)
		}
		if h.PortalPort != 0 && !shared.InListenRanges(ranges, h.PortalPort) {
			return fmt.Errorf("端口段收窄后链跳 %d portal 端口 %d 越界", h.ID, h.PortalPort)
		}
	}
	return nil
}

// marshalPortRanges 序列化端口段为存储值（空切片 → 空串）。
func marshalPortRanges(ranges []shared.PortRange) (string, error) {
	if len(ranges) == 0 {
		return "", nil
	}
	b, err := json.Marshal(ranges)
	if err != nil {
		return "", err
	}
	return string(b), nil
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
	sid := id
	s.audit(r, "server.upgrade_xray", &sid, nil, map[string]any{"version": req.Version, "command": cmdID})
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
	sid := id
	s.audit(r, "server.upgrade_agent", &sid, nil, map[string]any{"version": req.Version, "command": cmdID})
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
	sid := id
	s.audit(r, "server.repair", &sid, nil, map[string]int{"reapplied": reapplied})
	writeJSON(w, http.StatusOK, map[string]int{"reapplied": reapplied})
}

// installCommand 生成一行安装命令（§11）：xray 版本随命令携带（latest 由 install.sh 解析）。
// 默认 GitHub release 钉版：正式版本面板生成指向面板同版本 release 资产的命令——脚本与其
// 安装的 agent 二进制天然同版，老面板生成的命令不受后续发版影响（不可变性）。
// 设置页开启"面板托管资源"（resource_source=panel），或 dev 构建无 release 可钉时，
// 改用面板 /resource 镜像（与 release 同布局，面板安装/更新时落地，与面板同版本），
// 脚本以 --source panel 从面板下载 agent 包。
func (s *Server) installCommand(ctx context.Context, base, token, xrayVersion string) string {
	hosted := s.getSetting(ctx, store.SettingResourceSource) == ResourceSourcePanel ||
		s.cfg.Version == "" || s.cfg.Version == "dev" || s.cfg.GitHubRepo == ""
	if hosted {
		return fmt.Sprintf(
			"curl -fsSL %s/resource/install.sh | bash -s -- --source panel --panel %s --token %s --xray-version %s",
			base, base, token, xrayVersion)
	}
	return fmt.Sprintf(
		"curl -fsSL https://github.com/%s/releases/download/%s/install.sh | bash -s -- --panel %s --token %s --xray-version %s",
		s.cfg.GitHubRepo, s.cfg.Version, base, token, xrayVersion)
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
	srv, err := s.st.ServerByID(r.Context(), id)
	if err != nil {
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
	// 级联删除后对象已不存在，审计行存 alias 快照留痕（§log）。
	sid := id
	s.audit(r, "server.delete", &sid, nil, map[string]any{
		"alias": srv.Alias, "purge": r.URL.Query().Get("purge"),
	})
	w.WriteHeader(http.StatusNoContent)
}

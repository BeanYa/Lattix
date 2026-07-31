package panel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"lattix/backend/internal/dispatch"
	"lattix/backend/internal/store"
	"lattix/backend/internal/ws"
	"lattix/shared"
)

// metricsDTO 是主机指标的 API 表示（§13）。
type metricsDTO struct {
	Load1            float64  `json:"load1"`
	Load5            float64  `json:"load5"`
	Load15           float64  `json:"load15"`
	CPUPercent       *float64 `json:"cpu_percent"`
	MemTotal         uint64   `json:"mem_total"`
	MemUsed          uint64   `json:"mem_used"`
	DiskTotal        uint64   `json:"disk_total"`
	DiskUsed         uint64   `json:"disk_used"`
	NetworkInterface string   `json:"network_interface"`
	NetworkTXBytes   uint64   `json:"network_tx_bytes"`
	NetworkRXBytes   uint64   `json:"network_rx_bytes"`
	NetworkTXBPS     *float64 `json:"network_tx_bps"`
	NetworkRXBPS     *float64 `json:"network_rx_bps"`
	UptimeSeconds    uint64   `json:"uptime_seconds"`
	LatencyMS        *float64 `json:"latency_ms"`
	UpdatedAt        string   `json:"updated_at"`
}

// serverDTO 是服务器对象的 API 表示。
type serverDTO struct {
	ID                           int64              `json:"id"`
	Alias                        string             `json:"alias"`
	ConnectionState              string             `json:"connection_state"`
	SessionID                    string             `json:"session_id,omitempty"`
	SessionKind                  string             `json:"session_kind,omitempty"`
	LastConnectedAt              *time.Time         `json:"last_connected_at"`
	LastDisconnectedAt           *time.Time         `json:"last_disconnected_at"`
	LastReconnectedAt            *time.Time         `json:"last_reconnected_at"`
	ReconnectCount               int64              `json:"reconnect_count"`
	LastDisconnectReason         string             `json:"last_disconnect_reason"`
	LastSeenAt                   *time.Time         `json:"last_seen_at"`
	XrayVersion                  string             `json:"xray_version"`
	AgentVersion                 string             `json:"agent_version"` // session.open 上报的 agent 版本
	Address                      string             `json:"address"`       // 公网地址（session.open 记录，订阅用，§9）
	LearnedAddr                  string             `json:"learned_addr"`  // 拨入学习公网地址（容器网关回退到 agent 公网网卡，§9）
	NICAddresses                 []string           `json:"nic_addresses"` // agent 上报的网卡非回环地址（§9），编辑地址时的内置候选
	ConfigDrift                  bool               `json:"config_drift"`  // 配置漂移标志（§17）
	MachineType                  string             `json:"machine_type"`  // direct|nat（§21）
	AllowedPorts                 []shared.PortRange `json:"allowed_ports"` // NAT 可用端口段（§21），空 = 无段（仅出口档/direct）
	Tags                         []string           `json:"tags"`          // 管理标签；按顺序供名称模板 {{TAG_n}} 使用
	CountryCode                  string             `json:"country_code"`  // ISO 3166-1 alpha-2
	Location                     string             `json:"location"`      // 城市或机房位置
	AgentSettingsStatus          string             `json:"agent_settings_status"`
	AgentSettingsRevision        int64              `json:"agent_settings_revision"`
	AgentSettingsDesiredRevision int64              `json:"agent_settings_desired_revision"`
	AgentSettingsError           string             `json:"agent_settings_error"`
	AgentSettingsReportedAt      *time.Time         `json:"agent_settings_reported_at"`
	Metrics                      *metricsDTO        `json:"metrics"` // 主机遥测最新值（§13），无数据为 null
	Billing                      *billingDTO        `json:"billing"`
	TrafficPlan                  trafficPlanDTO     `json:"traffic_plan"`
	CreatedAt                    time.Time          `json:"created_at"`
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
	desiredRevision := int64(0)
	if desired, err := s.st.AgentSettings(context.Background()); err == nil {
		desiredRevision = desired.Revision
	}
	settingsStatus := "pending"
	if srv.AgentSettingsError != "" {
		settingsStatus = "failed"
	} else if srv.AgentSettingsReportedAt != nil && srv.AgentSettingsRevision == desiredRevision {
		settingsStatus = "synced"
	}
	connection := ws.ConnectionSnapshot{State: shared.ConnectionStateNeverConnected}
	if srv.LastConnectedAt != nil {
		connection.State = shared.ConnectionStateOffline
	}
	if reader, ok := s.req.(interface {
		ConnectionState(int64, bool) ws.ConnectionSnapshot
	}); ok {
		connection = reader.ConnectionState(srv.ID, srv.LastConnectedAt != nil)
	} else if s.req.IsOnline(srv.ID) {
		connection.State = shared.ConnectionStateOnline
	}
	return serverDTO{
		ID:                           srv.ID,
		Alias:                        srv.Alias,
		ConnectionState:              connection.State,
		SessionID:                    connection.SessionID,
		SessionKind:                  connection.SessionKind,
		LastConnectedAt:              srv.LastConnectedAt,
		LastDisconnectedAt:           srv.LastDisconnectedAt,
		LastReconnectedAt:            srv.LastReconnectedAt,
		ReconnectCount:               srv.ReconnectCount,
		LastDisconnectReason:         srv.LastDisconnectReason,
		LastSeenAt:                   srv.LastSeenAt,
		XrayVersion:                  srv.XrayVersion,
		AgentVersion:                 srv.AgentVersion,
		Address:                      srv.Address,
		LearnedAddr:                  srv.LearnedAddr,
		NICAddresses:                 nicAddrs,
		ConfigDrift:                  srv.ConfigDrift,
		MachineType:                  srv.MachineType,
		AllowedPorts:                 ranges,
		Tags:                         decodeServerTags(srv.Tags),
		CountryCode:                  srv.CountryCode,
		Location:                     srv.Location,
		AgentSettingsStatus:          settingsStatus,
		AgentSettingsRevision:        srv.AgentSettingsRevision,
		AgentSettingsDesiredRevision: desiredRevision,
		AgentSettingsError:           srv.AgentSettingsError,
		AgentSettingsReportedAt:      srv.AgentSettingsReportedAt,
		CreatedAt:                    srv.CreatedAt,
	}
}

func (s *Server) toServerDTOWithPlans(ctx context.Context, srv store.Server) (serverDTO, error) {
	dto := s.toServerDTO(srv)
	billing, err := s.st.ServerBillingMap(ctx)
	if err != nil {
		return dto, err
	}
	plans, err := s.st.ServerTrafficPlanMap(ctx)
	if err != nil {
		return dto, err
	}
	providers, err := s.st.ListProviders(ctx)
	if err != nil {
		return dto, err
	}
	if err := s.enrichServerBilling(ctx, &dto, billing, plans, providerMap(providers)); err != nil {
		return dto, err
	}
	return dto, nil
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
	billing, err := s.st.ServerBillingMap(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	plans, err := s.st.ServerTrafficPlanMap(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	providerItems, err := s.st.ListProviders(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	providers := providerMap(providerItems)
	out := make([]serverDTO, 0, len(servers))
	for _, srv := range servers {
		dto := s.toServerDTO(srv)
		if m, ok := metrics[srv.ID]; ok {
			value := toMetricsDTO(m)
			dto.Metrics = &value
		}
		if err := s.enrichServerBilling(r.Context(), &dto, billing, plans, providers); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
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
		XrayVersion  string             `json:"xray_version"`  // 兼容旧客户端；安装脚本默认 latest
		MachineType  string             `json:"machine_type"`  // direct（默认）| nat（§21）
		AllowedPorts []shared.PortRange `json:"allowed_ports"` // NAT 可用端口段（§21），留空 = 仅出口档
		Tags         []string           `json:"tags"`
		CountryCode  string             `json:"country_code"`
		Location     string             `json:"location"`
		Billing      *billingInput      `json:"billing"`
		TrafficPlan  *trafficPlanInput  `json:"traffic_plan"`
	}
	if err := readJSON(r, &req); err != nil {
		writeProtocolError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Alias == "" {
		writeError(w, http.StatusBadRequest, "alias 不能为空")
		return
	}
	countryCode, location, err := normalizeServerGeography(req.CountryCode, req.Location)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
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
	today := s.billingDefaultDate(r.Context())
	var billing *store.ServerBilling
	if req.Billing != nil {
		validated, err := validateBillingInput(r.Context(), s.st, *req.Billing, today)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		billing = &validated
	}
	trafficInput := req.TrafficPlan
	if trafficInput == nil {
		trafficInput = &trafficPlanInput{AccountingMode: "outbound", ResetAnchorOn: today, ResetCount: 1, ResetUnit: "month"}
	}
	if err := validateTrafficInput(*trafficInput); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	traffic := store.ServerTrafficPlan{
		QuotaBytes: trafficInput.QuotaBytes, AccountingMode: trafficInput.AccountingMode,
		ResetAnchorOn: trafficInput.ResetAnchorOn, ResetCount: trafficInput.ResetCount,
		ResetUnit: trafficInput.ResetUnit, TrackingStartedOn: today,
	}
	allowedJSON, err := marshalPortRanges(req.AllowedPorts)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	tagsJSON, err := encodeServerTags(req.Tags)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	panelID, err := s.st.PanelInstanceID(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	bootstrap, err := shared.NewCredential(panelID, 1)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	id, err := s.st.CreateServerWithPlans(r.Context(), req.Alias, req.Address, bootstrap, req.MachineType,
		allowedJSON, tagsJSON, countryCode, location, billing, traffic)
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
		"alias": req.Alias, "machine_type": req.MachineType, "address": req.Address, "tags": req.Tags,
		"country_code": countryCode, "location": location,
	})
	base := s.panelBase(r)
	createdDTO, err := s.toServerDTOWithPlans(r.Context(), *srv)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"server":          createdDTO,
		"bootstrap_token": bootstrap,
		"install_command": s.installCommand(base, bootstrap),
	})
}

// handleRotateToken 处理 POST /api/servers/{id}/rotate-token：
// 换发新 bootstrap token 并重置回 bootstrap 状态（旧凭证含长期 token 立即失效），
// 返回一行安装命令（§10/§11）。
func (s *Server) handleRotateToken(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ServerID int64 `json:"server_id"`
	}
	if err := readJSON(r, &req); err != nil {
		writeProtocolError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	id := req.ServerID
	if id <= 0 {
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
	panelID, err := s.st.PanelInstanceID(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	bootstrap, err := shared.NewCredential(panelID, srv.CredentialEpoch+1)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := s.st.ResetServerBootstrap(r.Context(), srv.ID, bootstrap); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if closer, ok := s.req.(interface {
		CloseAgent(serverID int64, code int, reason string)
	}); ok {
		closer.CloseAgent(srv.ID, 4001, "credential revoked")
	}
	sid := srv.ID
	s.audit(r, "server.rotate_token", &sid, nil, map[string]string{"alias": srv.Alias})
	base := s.panelBase(r)
	srv.Token = bootstrap
	srv.LastSeenAt = nil
	rotatedDTO, err := s.toServerDTOWithPlans(r.Context(), *srv)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"server":          rotatedDTO,
		"bootstrap_token": bootstrap,
		"install_command": s.installCommand(base, bootstrap),
	})
}

// handleUpdateServer 处理 PATCH /api/servers/{id}：管理员修改公网地址（§4/§9）与
// NAT 可用端口段（§21）。地址一经写入不再被 session.open 覆盖；置空则下次 session.open 按 RemoteAddr
// 重新自动学习（NAT 类型禁止置空）。机器类型建后不允许互转；端口段收窄时校验
// 该 server 存量节点 realized 端口与链跳端口不越界，越界 400。
func (s *Server) handleUpdateServer(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ServerID     int64               `json:"server_id"`
		Alias        *string             `json:"alias"` // 省略 = 不变
		Address      string              `json:"address"`
		MachineType  string              `json:"machine_type"`  // 不允许互转：带不同值 → 400
		AllowedPorts *[]shared.PortRange `json:"allowed_ports"` // 省略 = 不变；显式 null/数组 = 整体替换
		Tags         *[]string           `json:"tags"`          // 省略 = 不变；数组 = 整体替换
		CountryCode  *string             `json:"country_code"`  // 省略 = 不变
		Location     *string             `json:"location"`      // 省略 = 不变
		Billing      *billingInput       `json:"billing"`
		TrafficPlan  *trafficPlanInput   `json:"traffic_plan"`
	}
	if err := readJSON(r, &req); err != nil {
		writeProtocolError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	id := req.ServerID
	if id <= 0 {
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
	if req.MachineType != "" && req.MachineType != srv.MachineType {
		writeError(w, http.StatusBadRequest, "机器类型建后不允许互转")
		return
	}
	if req.Billing != nil {
		if _, err := validateBillingInput(r.Context(), s.st, *req.Billing, s.billingDefaultDate(r.Context())); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	if req.TrafficPlan != nil {
		if err := validateTrafficInput(*req.TrafficPlan); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	beforeDTO := s.toServerDTO(*srv)
	alias := srv.Alias
	if req.Alias != nil {
		alias = strings.TrimSpace(*req.Alias)
		if alias == "" {
			writeError(w, http.StatusBadRequest, "alias 不能为空")
			return
		}
		if len([]rune(alias)) > 100 {
			writeError(w, http.StatusBadRequest, "alias 最多 100 个字符")
			return
		}
	}
	if srv.MachineType == store.MachineTypeNAT && req.Address == "" {
		writeError(w, http.StatusBadRequest, "NAT 服务器必须填写公网地址（共享 IP 由 IDC 提供）")
		return
	}
	countryCode, location := srv.CountryCode, srv.Location
	if req.CountryCode != nil || req.Location != nil {
		if req.CountryCode == nil || req.Location == nil {
			writeError(w, http.StatusBadRequest, "country_code 与 location 须同时提供")
			return
		}
		countryCode, location, err = normalizeServerGeography(*req.CountryCode, *req.Location)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
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
	if req.Tags != nil {
		tagsJSON, err := encodeServerTags(*req.Tags)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if err := s.st.UpdateServerTags(r.Context(), id, tagsJSON); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	if err := s.st.UpdateServerAlias(r.Context(), id, alias); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := s.st.UpdateServerAddress(r.Context(), id, req.Address); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := s.st.UpdateServerGeography(r.Context(), id, countryCode, location); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := s.saveServerPlans(r.Context(), id, req.Billing, req.TrafficPlan); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	srv, err = s.st.ServerByID(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	sid := id
	afterDTO, err := s.toServerDTOWithPlans(r.Context(), *srv)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	changes := changedValues(
		map[string]any{
			"alias": beforeDTO.Alias, "address": beforeDTO.Address, "allowed_ports": beforeDTO.AllowedPorts,
			"tags": beforeDTO.Tags, "country_code": beforeDTO.CountryCode, "location": beforeDTO.Location,
		},
		map[string]any{
			"alias": afterDTO.Alias, "address": afterDTO.Address, "allowed_ports": afterDTO.AllowedPorts,
			"tags": afterDTO.Tags, "country_code": afterDTO.CountryCode, "location": afterDTO.Location,
		},
	)
	if len(changes) > 0 {
		s.audit(r, "server.updated", &sid, nil, changes)
	}
	writeJSON(w, http.StatusOK, afterDTO)
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
	var req struct {
		ServerID int64  `json:"server_id"`
		Version  string `json:"version"` // vX.Y.Z 或 latest（默认）
	}
	if err := readJSON(r, &req); err != nil {
		writeProtocolError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	id := req.ServerID
	if id <= 0 {
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
func (s *Server) handleUpgradeAgent(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ServerID int64  `json:"server_id"`
		Version  string `json:"version"` // vX.Y.Z 或 latest（默认）
		Force    bool   `json:"force"`
	}
	if err := readJSON(r, &req); err != nil {
		writeProtocolError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	id := req.ServerID
	if id <= 0 {
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
	if req.Version == "" {
		req.Version = "latest"
	}
	if req.Version != "latest" && !strings.HasPrefix(req.Version, "v") {
		writeError(w, http.StatusBadRequest, "版本号须形如 vX.Y.Z 或 latest")
		return
	}
	payload := shared.UpgradeAgentPayload{Version: req.Version, Force: req.Force}
	if s.cfg.GitHubRepo != "" {
		payload.ReleaseBase = "https://github.com/" + s.cfg.GitHubRepo + "/releases/download"
	}
	cmdID, err := s.disp.Enqueue(r.Context(), id, shared.TypeUpgradeAgent, payload)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	sid := id
	s.audit(r, "server.upgrade_agent", &sid, nil, map[string]any{"version": req.Version, "force": req.Force, "command": cmdID})
	writeJSON(w, http.StatusOK, map[string]any{"command_id": cmdID, "version": req.Version, "force": req.Force})
}

// handleRepairServer 处理 POST /api/servers/{id}/repair（§17 配置漂移修复）：
// 重放该服务器全部 active 节点的 apply_node，agent 据此重建配置并清除漂移标志。
func (s *Server) handleRepairServer(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ServerID int64 `json:"server_id"`
	}
	if err := readJSON(r, &req); err != nil {
		writeProtocolError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	id := req.ServerID
	if id <= 0 {
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

// installCommand 通过仓库根安装入口安装 Agent。Release 面板显式钉住自身版本；
// dev 构建省略版本，由入口解析 latest。xray 版本由 Agent 安装脚本默认解析 latest。
func (s *Server) installCommand(base, token string) string {
	versionArg := ""
	if s.cfg.Version != "" && s.cfg.Version != "dev" {
		versionArg = " --version " + s.cfg.Version
	}
	return fmt.Sprintf(
		"curl -fsSL https://raw.githubusercontent.com/%s/main/install.sh | bash -s -- agent%s --panel %s --token %s",
		s.cfg.GitHubRepo, versionArg, base, token)
}

// handleDeleteServer 处理 DELETE /api/servers/{id}：
// 在线则先下发 uninstall 命令（agent 自卸载），随后级联删除记录（§10）。
// 离线服务器仅删除记录（agent 需手动清理）。
func (s *Server) handleDeleteServer(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ServerID int64  `json:"server_id"`
		Purge    string `json:"purge"`
	}
	if err := readJSON(r, &req); err != nil {
		writeProtocolError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	id := req.ServerID
	if id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid server id")
		return
	}
	if req.Purge == "" {
		req.Purge = "xray"
	}
	if req.Purge != "xray" && req.Purge != "agent" {
		writeError(w, http.StatusBadRequest, "purge 须为 xray 或 agent")
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
	affected, err := s.st.ChainsReferencingServer(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	for _, chain := range affected {
		revisions, err := s.st.ChainDeploymentRevisions(r.Context(), chain.ID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		reason := fmt.Sprintf("服务器 %d（%s）已删除，链路失效", id, srv.Alias)
		if err := s.st.InvalidateChainForServerDeletion(r.Context(), chain.ID, id, reason); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		seen := map[string]bool{}
		for _, revision := range revisions {
			hops := revisionSnapshotHops(revision)
			for i, hop := range hops {
				if hop.ServerID == id {
					continue
				}
				for _, kind := range dispatch.ChainHopPieces(hops, i) {
					key := fmt.Sprintf("%d/%d/%s", hop.ServerID, hop.ID, kind)
					if seen[key] {
						continue
					}
					seen[key] = true
					if _, err := s.disp.Enqueue(r.Context(), hop.ServerID, shared.TypeRemoveChainHop,
						shared.RemoveChainHopPayload{HopID: hop.ID, Kind: kind}); err != nil {
						writeError(w, http.StatusInternalServerError, err.Error())
						return
					}
				}
			}
			if revision.Snapshot.ServiceServerID != id {
				key := fmt.Sprintf("node/%d/%d", revision.Snapshot.ServiceServerID, revision.Snapshot.ServiceNodeID)
				if !seen[key] {
					seen[key] = true
					if _, err := s.disp.Enqueue(r.Context(), revision.Snapshot.ServiceServerID, shared.TypeRemoveNode,
						shared.RemoveNodePayload{NodeID: revision.Snapshot.ServiceNodeID}); err != nil {
						writeError(w, http.StatusInternalServerError, err.Error())
						return
					}
				}
			}
		}
	}
	uninstallAcked := false
	uninstallAttempts := 0
	if s.req.IsOnline(id) {
		// purge 参数：xray = 连同 xray 卸载（默认），agent = 仅卸载 agent（§5/§10）。
		payload := shared.UninstallPayload{PurgeXray: req.Purge != "agent"}
		uninstallCtx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		uninstallAcked, uninstallAttempts, err = s.disp.UninstallWithRetry(uninstallCtx, id, payload)
		cancel()
		if err != nil {
			// Deletion remains authoritative when the delivery session disappears.
			log.Printf("panel: server %d uninstall delivery: %v", id, err)
		}
	}
	if err := s.st.DeleteServerCascade(context.Background(), id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if forgetter, ok := s.req.(interface {
		ForgetAgent(int64, int, string)
	}); ok {
		forgetter.ForgetAgent(id, 1008, "credentials revoked")
	}
	// 级联删除后对象已不存在，审计行存 alias 快照留痕（§log）。
	sid := id
	s.audit(r, "server.delete", &sid, nil, map[string]any{
		"alias": srv.Alias, "purge": req.Purge,
		"uninstall_acked": uninstallAcked, "uninstall_attempts": uninstallAttempts,
	})
	writeJSON(w, http.StatusOK, nil)
}

func revisionSnapshotHops(revision store.ChainRevision) []store.ChainHop {
	hops := make([]store.ChainHop, 0, len(revision.Snapshot.Hops))
	for i, hop := range revision.Snapshot.Hops {
		nodeID := int64(0)
		if i == len(revision.Snapshot.Hops)-1 {
			nodeID = revision.Snapshot.ServiceNodeID
		}
		hops = append(hops, store.ChainHop{ID: hop.HopID, ChainID: revision.ChainID, Seq: i,
			ServerID: hop.ServerID, Role: hop.Role, NodeID: nodeID, TunnelUUID: hop.TunnelUUID})
	}
	return hops
}

package panel

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"lattix/backend/internal/dispatch"
	"lattix/backend/internal/progress"
	"lattix/backend/internal/store"
	"lattix/shared"
)

// chainNodeObserveStages 是链路/节点操作的统一观察阶段（与分组操作一致的三段式）。
var chainNodeObserveStages = []progress.Stage{
	{Key: "db", Label: "校验并写入数据库"},
	{Key: "publish", Label: "下发节点配置"},
	{Key: "regenerate", Label: "重新生成订阅文件"},
}

// chainHopDTO 是链跳对象的 API 表示（§21）。
type chainHopDTO struct {
	ID              int64            `json:"id"`
	Seq             int              `json:"seq"`
	ServerID        int64            `json:"server_id"`
	ServerAlias     string           `json:"server_alias"`
	Role            string           `json:"role"` // entry/middle/exit
	NodeID          int64            `json:"node_id"`
	Status          string           `json:"status"` // pending/applying/active/failed
	Error           string           `json:"error"`
	ForwardPort     int              `json:"forward_port"` // entry 跳 = 订阅端口（监听侧）
	Address         string           `json:"address"`      // 本跳所选公网地址（空 = 跟随服务器默认地址，§9）
	PortalPort      int              `json:"portal_port"`
	PortalPublicKey string           `json:"portal_public_key,omitempty"`
	TunnelUUID      string           `json:"tunnel_uuid,omitempty"`
	Traffic         *chainTrafficDTO `json:"traffic,omitempty"`
}

type chainTrafficDTO struct {
	RawUp         int64 `json:"raw_up"`
	RawDown       int64 `json:"raw_down"`
	EffectiveUp   int64 `json:"effective_up"`
	EffectiveDown int64 `json:"effective_down"`
}

type chainRevisionTaskDTO struct {
	Key      string `json:"key"`
	Phase    string `json:"phase"`
	Action   string `json:"action"`
	Kind     string `json:"kind"`
	HopID    int64  `json:"hop_id"`
	ServerID int64  `json:"server_id"`
	Status   string `json:"status"`
	Error    string `json:"error"`
}

// chainDTO 是链对象的 API 表示（含逐跳状态，§21）。
type chainDTO struct {
	ID                  int64                  `json:"id"`
	Name                string                 `json:"name"`
	Status              string                 `json:"status"` // pending/applying/active/degraded/failed
	Error               string                 `json:"error"`  // 失败时定位到跳
	CreatedAt           time.Time              `json:"created_at"`
	Hops                []chainHopDTO          `json:"hops"`
	ServiceNodeID       int64                  `json:"service_node_id"`
	EndpointID          int64                  `json:"endpoint_id"`
	EntryPort           int                    `json:"entry_port"`
	EntryShared         bool                   `json:"entry_shared,omitempty"`
	EndpointStatus      string                 `json:"endpoint_status,omitempty"`
	EndpointError       string                 `json:"endpoint_error,omitempty"`
	TrafficMultiplier   string                 `json:"traffic_multiplier"`
	Traffic             *chainTrafficDTO       `json:"traffic,omitempty"`
	PublishedRevisionID int64                  `json:"published_revision_id"`
	DesiredRevisionID   int64                  `json:"desired_revision_id"`
	RevisionStatus      string                 `json:"revision_status,omitempty"`
	RevisionForced      bool                   `json:"revision_forced"`
	RevisionTasks       []chainRevisionTaskDTO `json:"revision_tasks"`
	ServiceConfig       json.RawMessage        `json:"service_config,omitempty"`
}

// toChainDTO 组装链 DTO（跳按 seq 升序：首位入口，末位出口）。
func (s *Server) toChainDTO(r *http.Request, c store.Chain) (chainDTO, error) {
	hops, err := s.st.ChainHops(r.Context(), c.ID)
	if err != nil {
		return chainDTO{}, err
	}
	out := chainDTO{ID: c.ID, Name: c.Name, Status: c.Status, Error: c.Error, CreatedAt: c.CreatedAt,
		ServiceNodeID: c.ServiceNodeID, TrafficMultiplier: formatTrafficMultiplier(c.TrafficMultiplierMilli),
		EndpointID:          c.EndpointID,
		PublishedRevisionID: c.PublishedRevisionID, DesiredRevisionID: c.DesiredRevisionID,
		Hops: []chainHopDTO{}, RevisionTasks: []chainRevisionTaskDTO{}}
	revisionID := c.PublishedRevisionID
	if c.DesiredRevisionID != 0 {
		revisionID = c.DesiredRevisionID
	}
	if revisionID != 0 {
		if revision, err := s.st.ChainRevisionByID(r.Context(), revisionID); err == nil {
			if revision.Snapshot.EndpointID != 0 {
				out.EndpointID = revision.Snapshot.EndpointID
			}
			out.RevisionStatus = revision.Status
			out.RevisionForced = revision.Forced
			out.ServiceConfig = append(json.RawMessage(nil), revision.Snapshot.ServiceConfig...)
			tasks, err := s.st.RevisionTasks(r.Context(), revisionID)
			if err != nil {
				return chainDTO{}, err
			}
			for _, task := range tasks {
				out.RevisionTasks = append(out.RevisionTasks, chainRevisionTaskDTO{Key: task.TaskKey,
					Phase: task.Phase, Action: task.Action, Kind: task.Kind, HopID: task.HopID,
					ServerID: task.ServerID, Status: task.Status, Error: task.Error})
			}
		}
	}
	if out.EndpointID != 0 {
		if endpoint, err := s.st.SharedEndpointByID(r.Context(), out.EndpointID); err == nil {
			out.EntryPort = endpoint.Port
			out.EndpointStatus = endpoint.Status
			out.EndpointError = endpoint.Error
		}
		if count, err := s.st.EndpointChainCount(r.Context(), out.EndpointID); err == nil {
			out.EntryShared = count >= 2
		}
	}
	if len(hops) == 0 && c.PublishedRevisionID != 0 {
		if revision, err := s.st.PublishedChainRevision(r.Context(), c.ID); err == nil {
			for seq, hop := range revision.Snapshot.Hops {
				nodeID := int64(0)
				if seq == len(revision.Snapshot.Hops)-1 {
					nodeID = revision.Snapshot.ServiceNodeID
				}
				hops = append(hops, store.ChainHop{ID: hop.HopID, ChainID: c.ID, Seq: seq,
					ServerID: hop.ServerID, Role: hop.Role, NodeID: nodeID, ForwardPort: hop.ForwardPort,
					Address: hop.Address,
					PortalPort: hop.PortalPort, PortalPublicKey: hop.PortalPublicKey,
					PortalServerName: hop.PortalServerName, TunnelUUID: hop.TunnelUUID})
			}
		}
	}
	totals, err := s.st.ChainTrafficTotals(r.Context(), c.ID)
	if err != nil {
		return chainDTO{}, err
	}
	trafficByHop := map[int64]chainTrafficDTO{}
	for _, total := range totals {
		trafficByHop[total.HopID] = chainTrafficDTO{RawUp: total.RawUp, RawDown: total.RawDown,
			EffectiveUp: total.EffectiveUp, EffectiveDown: total.EffectiveDown}
	}
	if total, ok := trafficByHop[0]; ok {
		out.Traffic = &total
	}
	for _, h := range hops {
		dto := chainHopDTO{
			ID:              h.ID,
			Seq:             h.Seq,
			ServerID:        h.ServerID,
			Role:            h.Role,
			NodeID:          h.NodeID,
			Status:          h.Status,
			Error:           h.Error,
			ForwardPort:     h.ForwardPort,
			Address:         h.Address,
			PortalPort:      h.PortalPort,
			PortalPublicKey: h.PortalPublicKey,
			TunnelUUID:      h.TunnelUUID,
		}
		if traffic, ok := trafficByHop[h.ID]; ok {
			dto.Traffic = &traffic
		}
		if srv, err := s.st.ServerByID(r.Context(), h.ServerID); err == nil {
			dto.ServerAlias = srv.Alias
		}
		out.Hops = append(out.Hops, dto)
	}
	if out.EntryPort == 0 && len(out.Hops) > 0 {
		out.EntryPort = out.Hops[0].ForwardPort
	}
	return out, nil
}

// handleListChains 处理 GET /api/chains（列表，含跳状态）。
func (s *Server) handleListChains(w http.ResponseWriter, r *http.Request) {
	chains, err := s.st.ListChains(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]chainDTO, 0, len(chains))
	for _, c := range chains {
		dto, err := s.toChainDTO(r, c)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		out = append(out, dto)
	}
	writeJSON(w, http.StatusOK, out)
}

// createChainRequest 是链路构图的提交（§10/§21）：依次入口 / 中间跳（0-2）/ 出口，
// 出口携带业务节点的协议表单（复用建节点请求），入口端口可空 = 自动。
type createChainRequest struct {
	Name              string            `json:"name"`
	Hops              []chainHopRef     `json:"hops,omitempty"`
	Entry             chainHopRef       `json:"entry"`
	Middle            []chainHopRef     `json:"middle"` // 0-2 个
	Exit              chainHopRef       `json:"exit"`
	EntryPort         *int              `json:"entry_port"` // 留空 = 自动；须在入口机可用段内
	Node              createNodeRequest `json:"node"`       // 出口业务节点表单（server_id 忽略，取 exit.server_id）
	TrafficMultiplier string            `json:"traffic_multiplier"`
}

type chainHopRef struct {
	ServerID int64  `json:"server_id"`
	Address  string `json:"address,omitempty"` // 所选公网地址（空 = 跟随服务器默认地址，§9）
}

type editChainRequest struct {
	ChainID           int64             `json:"chain_id"`
	Name              string            `json:"name"`
	Hops              []chainHopRef     `json:"hops"`
	EntryPort         *int              `json:"entry_port"`
	Node              createNodeRequest `json:"node"`
	TrafficMultiplier string            `json:"traffic_multiplier"`
}

// handleCreateChain 处理 POST /api/chains：构图校验 → 落库出口业务节点（pending）+
// chains/chain_hops 行 → 触发编排（§21.1：出口先就绪，逐跳向外，入口最后生效）。
func (s *Server) handleCreateChain(w http.ResponseWriter, r *http.Request) {
	var req createChainRequest
	if err := readJSON(r, &req); err != nil {
		writeProtocolError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	trafficMultiplierMilli, err := parseTrafficMultiplier(req.TrafficMultiplier)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	refs := req.Hops
	if len(refs) == 0 {
		refs = append(append([]chainHopRef{req.Entry}, req.Middle...), req.Exit)
	}
	if len(refs) < 1 || len(refs) > 4 {
		writeError(w, http.StatusBadRequest, "链路须包含 1-4 台服务器")
		return
	}
	// 出口节点协议表单校验（dokodemo 为端口转发，无用户概念，不能作链出口）。
	req.Node.Name = req.Name
	req.Node.ServerID = refs[len(refs)-1].ServerID
	if err := req.Node.normalize(); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if len(refs) > 1 && req.Node.Protocol == shared.ProtocolDokodemo {
		writeError(w, http.StatusBadRequest, "dokodemo-door 不能作为链出口节点")
		return
	}

	// 逐跳加载服务器并校验：同 server 不重复（O(n) 查重即环检测）；
	// 入口与中间跳必须有入站能力（direct 或 allowed_ports 非空），出口任意（§21）。
	servers := make([]*store.Server, 0, len(refs))
	seen := map[int64]bool{}
	for i, ref := range refs {
		if seen[ref.ServerID] {
			writeError(w, http.StatusBadRequest, "同一服务器在一条链中不重复")
			return
		}
		seen[ref.ServerID] = true
		srv, err := s.st.ServerByID(r.Context(), ref.ServerID)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				writeError(w, http.StatusBadRequest, fmt.Sprintf("服务器 %d 不存在", ref.ServerID))
				return
			}
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if i < len(refs)-1 && !inboundCapable(srv) {
			writeError(w, http.StatusBadRequest,
				fmt.Sprintf("服务器 %s 无入站能力（仅出口档 NAT），不能作入口/中间跳", srv.Alias))
			return
		}
		// 跳地址引用（§9）：非空时须属于该服务器当前地址集合（空 = 跟随服务器默认地址）。
		if ref.Address != "" && !store.ServerAddressSet(srv)[ref.Address] {
			writeError(w, http.StatusBadRequest,
				fmt.Sprintf("服务器 %s 不存在公网地址 %s", srv.Alias, ref.Address))
			return
		}
		servers = append(servers, srv)
	}
	entrySrv, exitSrv := servers[0], servers[len(servers)-1]
	nameServers := make([]nameTemplateServer, 0, len(servers))
	for _, srv := range servers {
		nameServers = append(nameServers, nameServer(srv))
	}
	name, err := resolveNameTemplate(req.Name, nameTemplateValues{
		Protocol:   req.Node.Protocol,
		Port:       req.EntryPort,
		Servers:    nameServers,
		PanelShort: store.EffectivePanelShort(s.getSetting(r.Context(), store.SettingPanelShort)),
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	req.Name = name
	req.Node.Name = name

	// 入口端口：可空 = 自动；指定时须在入口机可用段内（NAT 受限直连），且 1-65535。
	entryPort := 0
	if req.EntryPort != nil {
		if *req.EntryPort < 1 || *req.EntryPort > 65535 {
			writeError(w, http.StatusBadRequest, "入口端口须为 1-65535")
			return
		}
		if err := checkPortInRanges(entrySrv, *req.EntryPort); err != nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("入口端口 %d 不在入口机可用段内", *req.EntryPort))
			return
		}
		entryPort = *req.EntryPort
	}
	if len(refs) == 1 {
		if req.EntryPort != nil {
			req.Node.Port = req.EntryPort
		} else if req.Node.Port != nil {
			entryPort = *req.Node.Port
		}
	}
	// 出口节点端口：受限直连 NAT 机上用户指定端口必须在段内（自动端口由编排器携带候选展开）。
	if req.Node.Port != nil {
		if err := checkPortInRanges(exitSrv, *req.Node.Port); err != nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("出口节点端口 %d 不在出口机可用段内", *req.Node.Port))
			return
		}
	}

	o := s.observeStart(r, "chain.create", "创建链路", chainNodeObserveStages)
	defer o.Close()

	// Build the complete initial deployment before exposing it to the scheduler.
	vc := buildVirtualConfig(req.Node)
	endpointID := int64(0)
	serviceUUID := ""
	if vc.Protocol == shared.ProtocolVLESS {
		endpointConfig := vc
		endpointConfig.Port = entryPort
		endpointConfig.StaticClients = nil
		endpointJSON, err := json.Marshal(endpointConfig)
		if err != nil {
			o.Fail(err)
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		profile := endpointConfig
		profile.Port = 0
		profileJSON, _ := json.Marshal(profile)
		profileHash := fmt.Sprintf("%x", sha256.Sum256(profileJSON))
		endpoint, _, err := s.st.EnsureSharedEndpoint(r.Context(), entrySrv.ID, vc.Protocol,
			entryPort, profileHash, endpointJSON)
		if err != nil {
			o.Fail(err)
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		endpointID = endpoint.ID
		serviceUUID = uuid.NewString()
		vc.StaticClients = []shared.ClientCredential{{ID: serviceUUID, Email: "tunnel:" + serviceUUID}}
		// A one-hop shared chain exits directly from the endpoint and does not
		// need a second public listener on the same server.
		if len(servers) == 1 {
			vc.Port = 0
			req.Node.Port = nil
		}
	}
	vcJSON, err := json.Marshal(vc)
	if err != nil {
		o.Fail(err)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	initialHops := make([]store.InitialChainHop, 0, len(servers))
	plaintext := req.Node.Protocol == shared.ProtocolSocks || req.Node.Protocol == shared.ProtocolHTTP
	for i, srv := range servers {
		role := store.HopRoleMiddle
		if len(servers) == 1 {
			role = store.HopRoleExit
		} else if i == 0 {
			role = store.HopRoleEntry
		} else if i == len(servers)-1 {
			role = store.HopRoleExit
		}
		hopForwardPort := 0
		if (role == store.HopRoleEntry || len(servers) == 1) && endpointID == 0 {
			hopForwardPort = entryPort
		}
		// 反向链标记（§21.1）：下游无入站能力（nat 且 allowed_ports 空）→ 本跳为 portal 所在上游机，
		// 预生成 tunnel_uuid（面板下发；short_id 由编排器确定性派生）。
		transport := ""
		if i < len(servers)-1 {
			transport = transportForServers(servers[i+1], plaintext)
		}
		tunnelUUID := ""
		if transport == "reverse" || transport == "encrypted" {
			tunnelUUID = uuid.NewString()
		}
		initialHops = append(initialHops, store.InitialChainHop{
			ServerID: srv.ID, Role: role, Transport: transport,
			ForwardPort: hopForwardPort, Address: refs[i].Address, TunnelUUID: tunnelUUID,
		})
	}
	deployment, err := s.st.CreateInitialChainDeployment(r.Context(), store.InitialChainDeployment{
		Name: req.Name, ServiceServerID: exitSrv.ID, ServiceProtocol: vc.Protocol,
		ServicePort: req.Node.Port, ServiceConfig: vcJSON, EndpointID: endpointID, ServiceUUID: serviceUUID,
		TrafficMultiplierMilli: trafficMultiplierMilli, Hops: initialHops,
	})
	if err != nil {
		o.Fail(err)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	chainID := deployment.ChainID
	o.Report("db", 100, "链路已落库")
	for _, task := range deployment.ApplyKeys {
		_, id := splitRevisionPieceKey(task)
		serverID := exitSrv.ID
		if !strings.HasPrefix(task, dispatch.RevisionPieceService+"/") {
			for _, hop := range deployment.Hops {
				if hop.HopID == id {
					serverID = hop.ServerID
					break
				}
			}
		}
		if !s.req.IsOnline(serverID) {
			_ = s.st.SetChainRevisionStatus(r.Context(), deployment.RevisionID, store.RevisionStatusWaitingForAgent, "")
			break
		}
	}
	if err := s.disp.StartChain(r.Context(), chainID); err != nil {
		o.Fail(err)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	o.Report("publish", 100, "编排已启动")
	chain, err := s.st.ChainByID(r.Context(), chainID)
	if err != nil {
		o.Fail(err)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	dto, err := s.toChainDTO(r, *chain)
	if err != nil {
		o.Fail(err)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	o.Report("regenerate", 0, "等待订阅重生成")
	s.audit(r, "chain.create", nil, nil, map[string]any{"chain_id": chainID, "name": req.Name, "hops": len(dto.Hops)})
	writeJSON(w, http.StatusCreated, dto)
}

func splitRevisionPieceKey(key string) (string, int64) {
	kind, value, _ := strings.Cut(key, "/")
	id, _ := strconv.ParseInt(value, 10, 64)
	return kind, id
}

func (s *Server) handleEditChain(w http.ResponseWriter, r *http.Request) {
	var req editChainRequest
	if err := readJSON(r, &req); err != nil {
		writeProtocolError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	chain, err := s.st.ChainByID(r.Context(), req.ChainID)
	if err != nil {
		writeError(w, http.StatusNotFound, "链路不存在")
		return
	}
	// 部署失败（failed/active_failed）的链允许编辑后重新编排（§21）；
	// 其他在途状态（applying/waiting_for_agent/active_unconfirmed）仍拒绝并发编辑。
	failed := chain.Status == store.ChainStatusFailed || chain.Status == store.ChainStatusActiveFailed
	if chain.DesiredRevisionID != 0 && !failed {
		writeError(w, http.StatusConflict, "链路已有部署中的编辑")
		return
	}
	current, err := s.st.PublishedChainRevision(r.Context(), chain.ID)
	if err != nil && !failed {
		writeError(w, http.StatusConflict, "链路尚无已发布 revision")
		return
	}
	// 失败链以失败 desired revision 为基线（Agent 实际状态 = 部分落地的失败配置，
	// 而非已发布快照），并把 chain_hops 的落地参数（realized 端口等）并入基线，
	// 使未变化的 piece 按运行中参数复用，避免发布后订阅端口漂移。
	var failedRevision *store.ChainRevision
	if failed {
		if revision, desiredErr := s.st.DesiredChainRevision(r.Context(), chain.ID); desiredErr == nil {
			failedRevision = revision
			current = revision
			if hops, hopErr := s.st.ChainHops(r.Context(), chain.ID); hopErr == nil {
				byID := make(map[int64]store.ChainHop, len(hops))
				for _, hop := range hops {
					byID[hop.ID] = hop
				}
				for i := range current.Snapshot.Hops {
					if hop, ok := byID[current.Snapshot.Hops[i].HopID]; ok {
						current.Snapshot.Hops[i].ForwardPort = hop.ForwardPort
						current.Snapshot.Hops[i].PortalPort = hop.PortalPort
						current.Snapshot.Hops[i].PortalPublicKey = hop.PortalPublicKey
						current.Snapshot.Hops[i].PortalServerName = hop.PortalServerName
					}
				}
			}
		}
	}
	if current == nil {
		writeError(w, http.StatusConflict, "链路尚无已发布 revision")
		return
	}
	if len(req.Hops) < 1 || len(req.Hops) > 4 {
		writeError(w, http.StatusBadRequest, "链路须包含 1-4 台服务器")
		return
	}
	multiplier, err := parseTrafficMultiplier(req.TrafficMultiplier)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	servers := make([]*store.Server, 0, len(req.Hops))
	seen := map[int64]bool{}
	for i, ref := range req.Hops {
		if seen[ref.ServerID] {
			writeError(w, http.StatusBadRequest, "同一服务器在一条链中不重复")
			return
		}
		seen[ref.ServerID] = true
		server, err := s.st.ServerByID(r.Context(), ref.ServerID)
		if err != nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("服务器 %d 不存在", ref.ServerID))
			return
		}
		if i < len(req.Hops)-1 && !inboundCapable(server) {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("服务器 %s 无入站能力", server.Alias))
			return
		}
		// 跳地址引用（§9）：非空时须属于该服务器当前地址集合（空 = 跟随服务器默认地址）。
		if ref.Address != "" && !store.ServerAddressSet(server)[ref.Address] {
			writeError(w, http.StatusBadRequest,
				fmt.Sprintf("服务器 %s 不存在公网地址 %s", server.Alias, ref.Address))
			return
		}
		servers = append(servers, server)
	}
	req.Name = strings.TrimSpace(req.Name)
	req.Node.Name = req.Name
	req.Node.ServerID = servers[len(servers)-1].ID
	if err := req.Node.normalize(); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if len(servers) > 1 && req.Node.Protocol == shared.ProtocolDokodemo {
		writeError(w, http.StatusBadRequest, "dokodemo-door 不能作为中转链出口")
		return
	}
	nameServers := make([]nameTemplateServer, 0, len(servers))
	for _, server := range servers {
		nameServers = append(nameServers, nameServer(server))
	}
	name, err := resolveNameTemplate(req.Name, nameTemplateValues{
		Protocol:   req.Node.Protocol,
		Port:       req.EntryPort,
		Servers:    nameServers,
		PanelShort: store.EffectivePanelShort(s.getSetting(r.Context(), store.SettingPanelShort)),
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	req.Name = name
	req.Node.Name = name
	if req.EntryPort != nil {
		if *req.EntryPort < 1 || *req.EntryPort > 65535 || checkPortInRanges(servers[0], *req.EntryPort) != nil {
			writeError(w, http.StatusBadRequest, "入口端口不在可用范围内")
			return
		}
	}
	if len(servers) == 1 && req.EntryPort != nil {
		req.Node.Port = req.EntryPort
	}
	vc := buildVirtualConfig(req.Node)
	endpointID := int64(0)
	serviceUUID := current.Snapshot.ServiceUUID
	if vc.Protocol == shared.ProtocolVLESS && current.Snapshot.EndpointID != 0 {
		if serviceUUID == "" {
			serviceUUID = uuid.NewString()
		}
		endpointPort := 0
		if req.EntryPort != nil {
			endpointPort = *req.EntryPort
		}
		endpointConfig := vc
		endpointConfig.Port = endpointPort
		endpointJSON, _ := json.Marshal(endpointConfig)
		profile := endpointConfig
		profile.Port = 0
		profileJSON, _ := json.Marshal(profile)
		profileHash := fmt.Sprintf("%x", sha256.Sum256(profileJSON))
		if req.EntryPort == nil && len(current.Snapshot.Hops) > 0 &&
			current.Snapshot.Hops[0].ServerID == servers[0].ID {
			if currentEndpoint, err := s.st.SharedEndpointByID(r.Context(), current.Snapshot.EndpointID); err == nil &&
				currentEndpoint.ProfileHash == profileHash {
				endpointID = currentEndpoint.ID
			}
		}
		if endpointID == 0 {
			endpoint, _, err := s.st.EnsureSharedEndpoint(r.Context(), servers[0].ID,
				vc.Protocol, endpointPort, profileHash, endpointJSON)
			if err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			endpointID = endpoint.ID
		}
		vc.StaticClients = []shared.ClientCredential{{ID: serviceUUID, Email: "tunnel:" + serviceUUID}}
		if len(servers) == 1 {
			vc.Port = 0
			req.Node.Port = nil
		}
	}
	serviceConfig, err := json.Marshal(vc)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	oldByServer := map[int64]store.ChainRevisionHop{}
	for _, hop := range current.Snapshot.Hops {
		oldByServer[hop.ServerID] = hop
	}
	desiredHops := make([]store.ChainRevisionHop, 0, len(servers))
	for i, server := range servers {
		hop, ok := oldByServer[server.ID]
		if !ok {
			hopID, err := s.st.NextChainHopID(r.Context(), chain.ID, server.ID)
			if err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			hop = store.ChainRevisionHop{HopID: hopID, ServerID: server.ID}
		}
		hop.Role = store.HopRoleMiddle
		if len(servers) == 1 || i == len(servers)-1 {
			hop.Role = store.HopRoleExit
		} else if i == 0 {
			hop.Role = store.HopRoleEntry
		}
		if i == 0 && req.EntryPort != nil && endpointID == 0 {
			hop.ForwardPort = *req.EntryPort
		}
		if i == 0 && endpointID != 0 {
			hop.ForwardPort = 0
		}
		hop.Address = req.Hops[i].Address // 地址引用以本次编辑提交为准（§9）
		desiredHops = append(desiredHops, hop)
	}
	plaintext := req.Node.Protocol == shared.ProtocolSocks || req.Node.Protocol == shared.ProtocolHTTP
	for i := 0; i+1 < len(desiredHops); i++ {
		transport := "direct"
		if !inboundCapable(servers[i+1]) {
			transport = "reverse"
		} else if plaintext {
			transport = "encrypted"
		}
		old := desiredHops[i]
		if old.Transport != transport || old.TunnelUUID == "" && transport != "direct" {
			desiredHops[i].PortalPort = 0
			desiredHops[i].PortalPublicKey = ""
			desiredHops[i].PortalServerName = ""
		}
		desiredHops[i].Transport = transport
		if transport == "direct" {
			desiredHops[i].TunnelUUID = ""
		} else if desiredHops[i].TunnelUUID == "" {
			desiredHops[i].TunnelUUID = uuid.NewString()
		}
	}
	desired := store.ChainRevisionSnapshot{Name: req.Name, ServiceNodeID: current.Snapshot.ServiceNodeID,
		ServiceServerID: servers[len(servers)-1].ID, ServiceConfig: serviceConfig,
		EndpointID: endpointID, ServiceUUID: serviceUUID,
		TrafficMultiplierMilli: multiplier, Hops: desiredHops}
	currentPlanTopology := revisionTopology(1, current.Snapshot)
	desiredPlanTopology := revisionTopology(2, desired)
	plan, err := dispatch.PlanRevision(currentPlanTopology, desiredPlanTopology)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	o := s.observeStart(r, "chain.edit", "编辑链路", chainNodeObserveStages)
	defer o.Close()
	// 失败链基线为失败 revision：仅已落地（acked）的 piece 可复用；
	// 未落地的相同 piece 必须重发，否则会以未应用的配置被标记 active。
	if failedRevision != nil {
		tasks, err := s.st.RevisionTasks(r.Context(), failedRevision.ID)
		if err != nil {
			o.Fail(err)
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		acked := make(map[string]bool, len(tasks))
		for _, task := range tasks {
			if task.Phase == "apply" && task.Status == store.RevisionTaskAcked {
				acked[task.TaskKey] = true
			}
		}
		for _, piece := range plan.Reuse {
			if !acked[piece.Key] {
				plan.Apply = append(plan.Apply, piece)
			}
		}
	}
	for _, piece := range plan.Apply {
		desired.ApplyKeys = append(desired.ApplyKeys, piece.Key)
	}
	serviceChanged := false
	for _, key := range desired.ApplyKeys {
		if key == fmt.Sprintf("service/%d", desired.ServiceNodeID) {
			serviceChanged = true
		}
	}
	if !serviceChanged {
		desired.ServiceRealized = append(json.RawMessage(nil), current.Snapshot.ServiceRealized...)
	}
	revision, err := s.st.CreateChainRevision(r.Context(), chain.ID, desired)
	if err != nil {
		o.Fail(err)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	for _, piece := range plan.Apply {
		_, _ = s.st.AddRevisionTask(r.Context(), store.ChainRevisionTask{RevisionID: revision.ID,
			TaskKey: piece.Key, Phase: "apply", Action: "apply", Kind: piece.Kind,
			HopID: piece.HopID, ServerID: piece.ServerID})
	}
	for _, piece := range plan.Cleanup {
		_, _ = s.st.AddRevisionTask(r.Context(), store.ChainRevisionTask{RevisionID: revision.ID,
			TaskKey: "cleanup/" + piece.Key, Phase: "cleanup", Action: "remove", Kind: piece.Kind,
			HopID: piece.HopID, ServerID: piece.ServerID})
	}
	if current.Snapshot.ServiceServerID != desired.ServiceServerID {
		_, _ = s.st.AddRevisionTask(r.Context(), store.ChainRevisionTask{RevisionID: revision.ID,
			TaskKey: fmt.Sprintf("cleanup/service/%d", desired.ServiceNodeID), Phase: "cleanup", Action: "remove",
			Kind: dispatch.RevisionPieceService, HopID: desired.ServiceNodeID, ServerID: current.Snapshot.ServiceServerID})
	}
	var nodePort *int
	if vc.Port != 0 {
		value := vc.Port
		nodePort = &value
	}
	if err := s.disp.EditChainTopology(r.Context(), revision, vc.Protocol, nodePort, serviceChanged); err != nil {
		o.Fail(err)
		if errors.Is(err, store.ErrChainStatusChanged) {
			writeError(w, http.StatusConflict, "链路状态已变化，请刷新后重试")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// 废弃被取代的失败 revision：未投递/在途命令不再送达 Agent（§21 失败后编辑）。
	if failedRevision != nil {
		if err := s.st.AbandonChainRevision(r.Context(), failedRevision.ID, "被新编辑取代"); err != nil {
			o.Fail(err)
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	o.Report("db", 100, "新 revision 已保存")
	offline := false
	for _, piece := range plan.Apply {
		if !s.req.IsOnline(piece.ServerID) {
			offline = true
			break
		}
	}
	if offline {
		_ = s.st.SetChainRevisionStatus(r.Context(), revision.ID, store.RevisionStatusWaitingForAgent, "")
	}
	// 链状态机入口：所需 Agent 全部在线 → 推进编排；存在离线必需服务器 →
	// applying → waiting_for_agent，Agent 上线后由 ResumeChainsByServer 恢复（§21.1）。
	if err := s.disp.StartChainOrWait(r.Context(), chain.ID, offline, "编辑所需 Agent 离线，等待上线后恢复编排"); err != nil {
		o.Fail(err)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	o.Report("publish", 100, "编排已启动")
	chain, _ = s.st.ChainByID(r.Context(), chain.ID)
	dto, err := s.toChainDTO(r, *chain)
	if err != nil {
		o.Fail(err)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	o.Report("regenerate", 0, "等待订阅重生成")
	writeJSON(w, http.StatusAccepted, dto)
}

func revisionTopology(revisionID int64, snapshot store.ChainRevisionSnapshot) dispatch.RevisionTopology {
	hops := make([]dispatch.RevisionHopSpec, 0, len(snapshot.Hops))
	for index, hop := range snapshot.Hops {
		settings, _ := json.Marshal(map[string]any{
			"tunnel_uuid": hop.TunnelUUID,
			"local_only":  index == 0 && snapshot.EndpointID != 0,
			"address":     hop.Address, // 地址引用（§9）：选择变更须触发本跳 piece 重发并沿下游哈希传播
		})
		hops = append(hops, dispatch.RevisionHopSpec{HopID: hop.HopID, ServerID: hop.ServerID,
			Transport: hop.Transport, ListenPort: hop.ForwardPort, Settings: settings})
	}
	return dispatch.RevisionTopology{RevisionID: revisionID, ServiceID: snapshot.ServiceNodeID,
		Service: snapshot.ServiceConfig, Hops: hops,
		DirectShared: snapshot.EndpointID != 0 && len(snapshot.Hops) == 1}
}

func (s *Server) handleForcePublishChain(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ChainID int64 `json:"chain_id"`
	}
	if err := readJSON(r, &req); err != nil || req.ChainID <= 0 {
		writeProtocolError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	o := s.observeStart(r, "chain.force_publish", "强制发布链路", chainNodeObserveStages)
	defer o.Close()
	if err := s.disp.ForcePublishRevision(r.Context(), req.ChainID); err != nil {
		o.Fail(err)
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	o.Report("db", 100, "revision 已更新")
	o.Report("publish", 100, "已强制发布")
	chain, _ := s.st.ChainByID(r.Context(), req.ChainID)
	dto, err := s.toChainDTO(r, *chain)
	if err != nil {
		o.Fail(err)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	o.Report("regenerate", 0, "等待订阅重生成")
	writeJSON(w, http.StatusOK, dto)
}

func transportForServers(next *store.Server, plaintext bool) string {
	if !inboundCapable(next) {
		return "reverse"
	}
	if plaintext {
		return "encrypted"
	}
	return "direct"
}

func parseTrafficMultiplier(value string) (int, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 1000, nil
	}
	parts := strings.Split(value, ".")
	if len(parts) > 2 || parts[0] == "" {
		return 0, fmt.Errorf("流量倍率格式无效")
	}
	whole, err := strconv.Atoi(parts[0])
	if err != nil || whole < 0 {
		return 0, fmt.Errorf("流量倍率格式无效")
	}
	fraction := ""
	if len(parts) == 2 {
		fraction = parts[1]
	}
	if len(fraction) > 3 {
		return 0, fmt.Errorf("流量倍率最多三位小数")
	}
	for len(fraction) < 3 {
		fraction += "0"
	}
	fractionValue := 0
	if fraction != "" {
		fractionValue, err = strconv.Atoi(fraction)
		if err != nil {
			return 0, fmt.Errorf("流量倍率格式无效")
		}
	}
	milli := whole*1000 + fractionValue
	if milli < 1 || milli > 1_000_000 {
		return 0, fmt.Errorf("流量倍率须为 0.001-1000.000")
	}
	return milli, nil
}

func formatTrafficMultiplier(milli int) string {
	if milli == 0 {
		milli = 1000
	}
	return fmt.Sprintf("%d.%03d", milli/1000, milli%1000)
}

func (s *Server) handleSetChainTrafficMultiplier(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ChainID    int64  `json:"chain_id"`
		Multiplier string `json:"traffic_multiplier"`
	}
	if err := readJSON(r, &req); err != nil {
		writeProtocolError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	milli, err := parseTrafficMultiplier(req.Multiplier)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if _, err := s.st.ChainByID(r.Context(), req.ChainID); err != nil {
		writeError(w, http.StatusNotFound, "链路不存在")
		return
	}
	if err := s.st.SetChainTrafficMultiplier(r.Context(), req.ChainID, milli); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"traffic_multiplier": formatTrafficMultiplier(milli)})
}

func (s *Server) handleResetChainTraffic(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ChainID int64 `json:"chain_id"`
	}
	if err := readJSON(r, &req); err != nil || req.ChainID <= 0 {
		writeProtocolError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := s.st.ResetChainTraffic(r.Context(), req.ChainID); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, nil)
}

func (s *Server) handleGetChainTrafficHistory(w http.ResponseWriter, r *http.Request) {
	chainID, err := strconv.ParseInt(r.URL.Query().Get("chain_id"), 10, 64)
	if err != nil || chainID <= 0 {
		writeError(w, http.StatusBadRequest, "invalid chain id")
		return
	}
	hopID, _ := strconv.ParseInt(r.URL.Query().Get("hop_id"), 10, 64)
	days := 30
	if value := r.URL.Query().Get("days"); value != "" {
		days, err = strconv.Atoi(value)
		if err != nil || days < 1 || days > 730 {
			writeError(w, http.StatusBadRequest, "days must be between 1 and 730")
			return
		}
	}
	_, location := s.st.TrafficLocation(r.Context())
	since := time.Now().In(location).AddDate(0, 0, -days+1).Format("2006-01-02")
	buckets, err := s.st.ChainTrafficDaily(r.Context(), chainID, hopID, since)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, buckets)
}

// handleRetryChain 处理 POST /api/chains/{id}/retry：只重放失败 piece（§21）。
func (s *Server) handleRetryChain(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ChainID int64 `json:"chain_id"`
	}
	if err := readJSON(r, &req); err != nil {
		writeProtocolError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	id := req.ChainID
	if id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid chain id")
		return
	}
	o := s.observeStart(r, "chain.retry", "重试链路", chainNodeObserveStages)
	defer o.Close()
	if err := s.disp.RetryChain(r.Context(), id); err != nil {
		o.Fail(err)
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "链不存在")
			return
		}
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	o.Report("db", 100, "失败任务已复位")
	o.Report("publish", 100, "重发命令已下发")
	chain, err := s.st.ChainByID(r.Context(), id)
	if err != nil {
		o.Fail(err)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	dto, err := s.toChainDTO(r, *chain)
	if err != nil {
		o.Fail(err)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	o.Report("regenerate", 0, "等待订阅重生成")
	s.audit(r, "chain.retry", nil, nil, map[string]any{"chain_id": id})
	writeJSON(w, http.StatusOK, dto)
}

// handleDeleteChain 处理 DELETE /api/chains/{id}：反向拆链（§21）——
// 入口→出口逐跳下发 remove_chain_hop（离线留队列补发），出口业务节点走现有删除流程，最后删行。
func (s *Server) handleDeleteChain(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ChainID int64 `json:"chain_id"`
	}
	if err := readJSON(r, &req); err != nil {
		writeProtocolError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	id := req.ChainID
	if id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid chain id")
		return
	}
	chain, err := s.st.ChainByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "链不存在")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	affectedUsers, err := s.st.SubscriptionUserIDsForChain(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	hops, err := s.st.ChainHops(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if len(hops) == 0 {
		if revision, err := s.st.PublishedChainRevision(r.Context(), id); err == nil {
			hops = revisionSnapshotHops(*revision)
		}
	}
	o := s.observeStart(r, "chain.delete", "删除链路", chainNodeObserveStages)
	defer o.CloseIfPending()
	for i, h := range hops {
		for _, kind := range dispatch.ChainHopPieces(hops, i) {
			if _, err := s.disp.Enqueue(r.Context(), h.ServerID, shared.TypeRemoveChainHop,
				shared.RemoveChainHopPayload{HopID: h.ID, Kind: kind}); err != nil {
				o.Fail(err)
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
		}
		// 出口业务节点走现有删除流程（remove_node + 删行，§6）。
		if h.Role == store.HopRoleExit && h.NodeID != 0 {
			if _, err := s.disp.Enqueue(r.Context(), h.ServerID, shared.TypeRemoveNode,
				shared.RemoveNodePayload{NodeID: h.NodeID}); err != nil {
				o.Fail(err)
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			if err := s.st.DeleteNode(r.Context(), h.NodeID); err != nil {
				o.Fail(err)
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
		}
	}
	o.Report("publish", 100, "拆链命令已下发")
	if err := s.disp.DeleteChain(r.Context(), id); err != nil {
		o.Fail(err)
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "链路不存在")
			return
		}
		if errors.Is(err, store.ErrChainStatusChanged) {
			writeError(w, http.StatusConflict, "链路状态已变化，请刷新后重试")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	o.Report("db", 100, "链路已删除")
	if chain.EndpointID != 0 {
		if err := s.disp.ReconcileSharedEndpoint(r.Context(), chain.EndpointID); err != nil {
			log.Printf("panel: reconcile shared endpoint %d after chain delete: %v", chain.EndpointID, err)
		}
	}
	if s.subscriptions != nil {
		s.subscriptions.EnqueueUsers(affectedUsers, s.panelBase(r))
		o.WatchUsers(affectedUsers)
	}
	o.Report("regenerate", 0, "等待订阅重生成")
	s.audit(r, "chain.delete", nil, nil, map[string]any{"chain_id": id, "hops": len(hops)})
	writeJSON(w, http.StatusOK, nil)
}

// inboundCapable 报告服务器是否有入站能力（§21：direct 或 NAT 受限直连）。
func inboundCapable(srv *store.Server) bool {
	return srv.MachineType == store.MachineTypeDirect || srv.AllowedPorts != ""
}

// checkPortInRanges 校验指定端口在 NAT 机可用段内（监听侧）；无段/direct 机不限制（Agent 检查占用，§7）。
func checkPortInRanges(srv *store.Server, port int) error {
	ranges, err := shared.ParsePortRanges(srv.AllowedPorts)
	if err != nil {
		return err
	}
	if len(ranges) > 0 && !shared.InListenRanges(ranges, port) {
		return fmt.Errorf("端口 %d 不在可用段内", port)
	}
	return nil
}

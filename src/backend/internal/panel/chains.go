package panel

import (
	"context"
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

type chainHopCandidateDTO struct {
	Priority    int    `json:"priority"`
	ServerID    int64  `json:"server_id"`
	ServerAlias string `json:"server_alias,omitempty"`
	Address     string `json:"address,omitempty"`
	ListenPort  int    `json:"listen_port,omitempty"`
	Active      bool   `json:"active"`
}

type chainFailoverEventDTO struct {
	ID              int64     `json:"id"`
	Seq             int       `json:"seq"`
	FromServerID    int64     `json:"from_server_id"`
	FromServerAlias string    `json:"from_server_alias,omitempty"`
	ToServerID      int64     `json:"to_server_id"`
	ToServerAlias   string    `json:"to_server_alias,omitempty"`
	ReasonCode      string    `json:"reason_code"`
	ReasonDetail    string    `json:"reason_detail"`
	RevisionID      int64     `json:"revision_id"`
	CreatedAt       time.Time `json:"created_at"`
}

// chainHopDTO 是链跳对象的 API 表示（§21）。
type chainHopDTO struct {
	ID                 int64                     `json:"id"`
	Seq                int                       `json:"seq"`
	ServerID           int64                     `json:"server_id"`
	ServerAlias        string                    `json:"server_alias"`
	Role               string                    `json:"role"` // entry/middle/exit
	NodeID             int64                     `json:"node_id"`
	Status             string                    `json:"status"` // pending/applying/active/failed
	Error              string                    `json:"error"`
	ForwardPort        int                       `json:"forward_port"` // entry 跳 = 订阅端口（监听侧）
	Address            string                    `json:"address"`      // 本跳所选公网地址（空 = 跟随服务器默认地址，§9）
	PortalPort         int                       `json:"portal_port"`
	PortalPublicKey    string                    `json:"portal_public_key,omitempty"`
	TunnelUUID         string                    `json:"tunnel_uuid,omitempty"`
	Traffic            *chainTrafficDTO          `json:"traffic,omitempty"`
	Candidates         []chainHopCandidateDTO    `json:"candidates,omitempty"`
	ActivePriority     int                       `json:"active_priority"`
	PrimaryServerID    int64                     `json:"primary_server_id,omitempty"`
	PrimaryServerAlias string                    `json:"primary_server_alias,omitempty"`
	Orchestration      *chainHopOrchestrationDTO `json:"orchestration,omitempty"`
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
	ID                  int64                   `json:"id"`
	Name                string                  `json:"name"`
	Status              string                  `json:"status"` // pending/applying/active/degraded/failed
	Error               string                  `json:"error"`  // 失败时定位到跳
	CreatedAt           time.Time               `json:"created_at"`
	Hops                []chainHopDTO           `json:"hops"`
	ServiceNodeID       int64                   `json:"service_node_id"`
	EndpointID          int64                   `json:"endpoint_id"`
	EntryPort           int                     `json:"entry_port"`
	EntryShared         bool                    `json:"entry_shared,omitempty"`
	EndpointStatus      string                  `json:"endpoint_status,omitempty"`
	EndpointError       string                  `json:"endpoint_error,omitempty"`
	TrafficMultiplier   string                  `json:"traffic_multiplier"`
	Traffic             *chainTrafficDTO        `json:"traffic,omitempty"`
	PublishedRevisionID int64                   `json:"published_revision_id"`
	DesiredRevisionID   int64                   `json:"desired_revision_id"`
	RevisionStatus      string                  `json:"revision_status,omitempty"`
	RevisionForced      bool                    `json:"revision_forced"`
	RevisionTasks       []chainRevisionTaskDTO  `json:"revision_tasks"`
	ServiceConfig       json.RawMessage         `json:"service_config,omitempty"`
	ServiceExternal     *shared.ExternalExit    `json:"service_external,omitempty"` // 外部直链出口规格（未托管出口链路）
	EntryConfig         *shared.VirtualConfig   `json:"entry_config,omitempty"`     // 入口协议区块配置回填（P4，前端编辑回填用）
	FailoverEvents      []chainFailoverEventDTO `json:"failover_events,omitempty"`
	OrchestrationError  string                  `json:"orchestration_error,omitempty"`
}

// chainDTOContext 是一次列表渲染共享的只读上下文：服务器别名与封禁状态。
// 逐链重复查询会让列表页的查询数随链路数线性膨胀（实测 72 条链 ≈ 550 次查询），
// 而这两张表在整页渲染期间是稳定的。
type chainDTOContext struct {
	servers map[int64]store.Server
	bans    map[int64]store.ServerBanWatch
}

// chainDTOContext 加载列表渲染共用上下文（单链路调用点也可直接用）。
func (s *Server) chainDTOContext(ctx context.Context) *chainDTOContext {
	cc := &chainDTOContext{bans: map[int64]store.ServerBanWatch{}}
	if servers, err := s.st.ListServers(ctx); err == nil {
		cc.servers = make(map[int64]store.Server, len(servers))
		for _, srv := range servers {
			cc.servers[srv.ID] = srv
		}
	}
	if bans, err := s.st.ServerBanWatchMap(ctx); err == nil {
		cc.bans = bans
	}
	return cc
}

// toChainDTO 组装链 DTO（跳按 seq 升序：首位入口，末位出口）。
func (s *Server) toChainDTO(r *http.Request, c store.Chain) (chainDTO, error) {
	return s.toChainDTOWith(r, c, s.chainDTOContext(r.Context()))
}

func (s *Server) toChainDTOWith(r *http.Request, c store.Chain, cc *chainDTOContext) (chainDTO, error) {
	hops, err := s.st.ChainHops(r.Context(), c.ID)
	if err != nil {
		return chainDTO{}, err
	}
	out := chainDTO{ID: c.ID, Name: c.Name, Status: c.Status, Error: c.Error, CreatedAt: c.CreatedAt,
		ServiceNodeID: c.ServiceNodeID, TrafficMultiplier: formatTrafficMultiplier(c.TrafficMultiplierMilli),
		EndpointID:          c.EndpointID,
		PublishedRevisionID: c.PublishedRevisionID, DesiredRevisionID: c.DesiredRevisionID,
		Hops: []chainHopDTO{}, RevisionTasks: []chainRevisionTaskDTO{}}
	if message, err := s.st.ChainOrchestrationError(r.Context(), c.ID); err == nil {
		out.OrchestrationError = message
	}
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
			// 外部出口规格含凭据：DTO 只回显打码后的预览（数据面用快照原文）。
			out.ServiceExternal = redactExternalExit(revision.Snapshot.ServiceExternal)
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
			// 入口端点配置回填（P4 入口协议区块，前端编辑回填用；取不到则留 nil，不阻断列表）。
			var entryConfig shared.VirtualConfig
			if err := json.Unmarshal(endpoint.ConfigTemplate, &entryConfig); err == nil {
				out.EntryConfig = &entryConfig
			}
		}
		if count, err := s.st.EndpointChainCount(r.Context(), out.EndpointID); err == nil {
			out.EntryShared = count >= 2
		}
	}
	if (len(hops) == 0 || c.Status == store.ChainStatusInvalid) && c.PublishedRevisionID != 0 {
		if revision, err := s.st.PublishedChainRevision(r.Context(), c.ID); err == nil && len(revision.Snapshot.Hops) > len(hops) {
			bySeq := make(map[int]store.ChainHop, len(hops))
			for _, h := range hops {
				bySeq[h.Seq] = h
			}
			merged := make([]store.ChainHop, 0, len(revision.Snapshot.Hops))
			for seq, snapHop := range revision.Snapshot.Hops {
				if existing, ok := bySeq[seq]; ok {
					merged = append(merged, existing)
					continue
				}
				nodeID := int64(0)
				if seq == len(revision.Snapshot.Hops)-1 {
					nodeID = revision.Snapshot.ServiceNodeID
				}
				merged = append(merged, store.ChainHop{
					ID: snapHop.HopID, ChainID: c.ID, Seq: seq,
					ServerID: snapHop.ServerID, Role: snapHop.Role, NodeID: nodeID,
					Status: store.HopStatusFailed, Error: "服务器已删除",
					ForwardPort: snapHop.ForwardPort, Address: snapHop.Address,
					PortalPort: snapHop.PortalPort, PortalPublicKey: snapHop.PortalPublicKey,
					PortalServerName: snapHop.PortalServerName, TunnelUUID: snapHop.TunnelUUID,
				})
			}
			hops = merged
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
	graphs, _ := s.st.ChainHopGraphs(r.Context(), c.ID)
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
		if srv, ok := cc.servers[h.ServerID]; ok {
			dto.ServerAlias = srv.Alias
		}
		if graph, ok := graphs[h.Seq]; ok {
			graphCopy := graph
			dto.Orchestration = s.buildHopOrchestrationDTO(h, &graphCopy, cc)
		}
		out.Hops = append(out.Hops, dto)
	}
	if out.EntryPort == 0 && len(out.Hops) > 0 {
		out.EntryPort = out.Hops[0].ForwardPort
	}
	if events, err := s.st.ChainFailoverEvents(r.Context(), c.ID, 10); err == nil && len(events) > 0 {
		out.FailoverEvents = make([]chainFailoverEventDTO, 0, len(events))
		for _, ev := range events {
			fromAlias, toAlias := "", ""
			if srv, ok := cc.servers[ev.FromServerID]; ok {
				fromAlias = srv.Alias
			}
			if srv, ok := cc.servers[ev.ToServerID]; ok {
				toAlias = srv.Alias
			}
			out.FailoverEvents = append(out.FailoverEvents, chainFailoverEventDTO{
				ID:              ev.ID,
				Seq:             ev.Seq,
				FromServerID:    ev.FromServerID,
				FromServerAlias: fromAlias,
				ToServerID:      ev.ToServerID,
				ToServerAlias:   toAlias,
				ReasonCode:      ev.ReasonCode,
				ReasonDetail:    ev.ReasonDetail,
				RevisionID:      ev.RevisionID,
				CreatedAt:       ev.CreatedAt,
			})
		}
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
	cc := s.chainDTOContext(r.Context())
	for _, c := range chains {
		dto, err := s.toChainDTOWith(r, c, cc)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		out = append(out, dto)
	}
	writeJSON(w, http.StatusOK, out)
}

type chainHopCandidateInput struct {
	ServerID   int64  `json:"server_id"`
	Address    string `json:"address,omitempty"`
	ListenPort *int   `json:"listen_port,omitempty"`
}

// createChainRequest 是链路构图的提交（§10/§21）：依次入口 / 中间跳（0-2）/ 出口，
// 出口携带业务节点的协议表单（复用建节点请求），入口端口可空 = 自动。
type createChainRequest struct {
	Orchestrations    []orchestrationDraft `json:"orchestrations,omitempty"`
	Name              string               `json:"name"`
	Hops              []chainHopRef        `json:"hops,omitempty"`
	Entry             chainHopRef          `json:"entry"`
	Middle            []chainHopRef        `json:"middle"` // 0-2 个
	Exit              chainHopRef          `json:"exit"`
	EntryPort         *int                 `json:"entry_port"`           // 留空 = 自动；须在入口机可用段内
	Node              createNodeRequest    `json:"node"`                 // 出口业务节点表单（server_id 忽略，取 exit.server_id）
	EntryNode         *createNodeRequest   `json:"entry_node,omitempty"` // 入口协议区块（P4；nil=端到端现状，v1 仅 vless+reality）
	TrafficMultiplier string               `json:"traffic_multiplier"`
}

type chainHopRef struct {
	ServerID int64  `json:"server_id"`
	Address  string `json:"address,omitempty"` // 所选公网地址（空 = 跟随服务器默认地址，§9）
}

type editChainRequest struct {
	Orchestrations    []orchestrationDraft `json:"orchestrations,omitempty"`
	ChainID           int64                `json:"chain_id"`
	Name              string               `json:"name"`
	Hops              []chainHopRef        `json:"hops"`
	EntryPort         *int                 `json:"entry_port"`
	Node              createNodeRequest    `json:"node"`
	EntryNode         *createNodeRequest   `json:"entry_node,omitempty"` // 入口协议区块（P4；nil=端到端现状）
	TrafficMultiplier string               `json:"traffic_multiplier"`
}

// handleCreateChain 处理 POST /api/chains：构图校验 → 落库出口业务节点（pending）+
// chains/chain_hops 行 → 触发编排（§21.1：出口先就绪，逐跳向外，入口最后生效）。
func (s *Server) handleCreateChain(w http.ResponseWriter, r *http.Request) {
	s.orchestrationMu.Lock()
	defer s.orchestrationMu.Unlock()
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
	graphWrites, activeGraph, err := s.prepareChainGraphs(r.Context(), 0, refs, req.Orchestrations)
	if err != nil {
		templateError(w, err)
		return
	}
	applyGraphPorts(activeGraph, len(refs), &req.EntryPort, &req.Node)
	// 逐跳加载服务器并校验（先于出口节点表单：外部直链出口没有节点表单）：
	// 同 server 不重复（O(n) 查重即环检测）；入口与中间跳必须有入站能力
	// （direct 或 allowed_ports 非空，§21）；未托管外部直链节点只能作最后一跳。
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
		// 未托管外部直链节点只能作链路最后一跳（出口）：它没有 Agent、
		// 没有入站能力，也无法承载入口/中间跳的 forward 监听。
		if srv.IsExternal() && (i < len(refs)-1 || len(refs) == 1) {
			writeError(w, http.StatusBadRequest,
				fmt.Sprintf("未托管服务器 %s 只能作为链路的最后一跳（出口）", srv.Alias))
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
	externalExit := exitSrv.ParseExternalExit()
	if exitSrv.IsExternal() && externalExit == nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("未托管服务器 %s 的外部直链配置损坏，请重新保存该服务器", exitSrv.Alias))
		return
	}
	if externalExit != nil {
		// 服务器表单没有必填项：不完整的出口规格在这里拒绝（补全后即可建链）。
		if err := externalExit.Validate(); err != nil {
			writeError(w, http.StatusBadRequest,
				fmt.Sprintf("外部直链出口配置不可用：%v（请编辑服务器 %s 补全协议参数）", err, exitSrv.Alias))
			return
		}
	}
	// 出口节点协议表单校验（dokodemo 为端口转发，无用户概念，不能作链出口）。
	// 外部直链出口没有托管业务节点：忽略节点表单，协议取外部规格。
	exitProtocol := ""
	if externalExit != nil {
		exitProtocol = externalExit.Protocol
		req.Node = createNodeRequest{}
	} else {
		req.Node.Name = req.Name
		req.Node.ServerID = exitSrv.ID
		if err := req.Node.normalize(); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if len(refs) > 1 && req.Node.Protocol == shared.ProtocolDokodemo {
			writeError(w, http.StatusBadRequest, "dokodemo-door 不能作为链出口节点")
			return
		}
		exitProtocol = req.Node.Protocol
	}
	// 入口协议（客户端接入端点）：外部直链出口链恒由入口共享端点承载，入口服务器 A 与
	// 普通链路一样可配置完整的 VLESS 协议细节（传输/安全层/flow/encryption/证书/reality
	// 子参数），缺省 reality 自动生成；托管出口链沿用入口协议区块（v1 固定 VLESS+Reality）。
	if req.EntryNode == nil && externalExit != nil {
		req.EntryNode = &createNodeRequest{Protocol: shared.ProtocolVLESS}
	}
	if req.EntryNode != nil {
		if len(refs) < 2 {
			writeError(w, http.StatusBadRequest, "入口协议区块仅用于多跳链路（单跳客户端直连出口即可）")
			return
		}
		if req.EntryNode.Protocol != "" && req.EntryNode.Protocol != shared.ProtocolVLESS {
			writeError(w, http.StatusBadRequest, "入口协议仅支持 VLESS（入口共享端点）")
			return
		}
		if externalExit == nil && exitProtocol != shared.ProtocolHysteria2 && exitProtocol != shared.ProtocolVLESS {
			writeError(w, http.StatusBadRequest, "入口协议区块 v1 仅支持 hysteria2/vless 出口")
			return
		}
		req.EntryNode.Protocol = shared.ProtocolVLESS
		if externalExit == nil {
			// 托管出口的入口协议区块 v1 固定 Reality（子参数可空自动生成）。
			req.EntryNode.Security = shared.SecurityReality
		}
		req.EntryNode.Name = req.Name
		req.EntryNode.ServerID = entrySrv.ID
		if err := req.EntryNode.normalize(); err != nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("入口协议参数: %v", err))
			return
		}
	}
	// ACME 证书模式：落地（出口）服务器须有域名型公网地址（§3.3 模式 B，无则 400）；
	// 外部直链出口链的落地是入口服务器 A（客户端接入端点证书由 A 签发）。
	if externalExit == nil {
		if err := applyACMEDomain(&req.Node, exitSrv); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	} else if req.EntryNode != nil {
		if err := applyACMEDomain(req.EntryNode, entrySrv); err != nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("入口协议参数: %v", err))
			return
		}
	}
	var graphHy2Endpoint *store.SharedEndpoint
	if req.Node.Protocol == shared.ProtocolHysteria2 {
		if len(activeGraph) > 0 {
			graphHy2Endpoint, err = s.prepareGraphHy2Endpoint(r.Context(), &req.Node, exitSrv.ID, activeGraph, len(servers)-1)
			if err != nil {
				writeError(w, http.StatusBadRequest, err.Error())
				return
			}
		}
		ownEndpointID := int64(0)
		if graphHy2Endpoint != nil {
			ownEndpointID = graphHy2Endpoint.ID
		}
		if err := s.resolveHy2PortHop(r.Context(), &req.Node, exitSrv, 0, ownEndpointID); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
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
	// 入口监听是 dokodemo 管道（层随出口协议：ss 为 tcp,udp，hy2 为 udp，其余 tcp）；
	// vless 出口或勾选入口协议区块时入口走共享端点合并，跳过前置校验。
	entryShared := req.Node.Protocol == shared.ProtocolVLESS || req.EntryNode != nil
	sharedHy2Exit := graphHy2Endpoint != nil && activeGraph[len(servers)-1].ListenPort > 0
	if entryPort > 0 && !entryShared && !(len(servers) == 1 && sharedHy2Exit) {
		if err := s.checkPortConflict(r.Context(), entrySrv.ID, req.Node.Protocol, entryPort, 0, 0); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	// 出口节点端口：受限直连 NAT 机上用户指定端口必须在段内（自动端口由编排器携带候选展开）。
	if req.Node.Port != nil {
		if err := checkPortInRanges(exitSrv, *req.Node.Port); err != nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("出口节点端口 %d 不在出口机可用段内", *req.Node.Port))
			return
		}
		// 单跳 vless 的节点端口即入口端口（上方已别名），由共享端点合并接管、编排后不实际
		// 监听；冲突判定已由入口校验按 protocol != vless 门控，此处跳过避免重复误判。
		singleHopShared := len(servers) == 1 && req.Node.Protocol == shared.ProtocolVLESS
		if !singleHopShared && !sharedHy2Exit {
			if err := s.checkPortConflict(r.Context(), exitSrv.ID, req.Node.Protocol, *req.Node.Port, 0, 0); err != nil {
				writeError(w, http.StatusBadRequest, err.Error())
				return
			}
		}
	}
	ports := chainForwardPorts(servers, req.Node, req.EntryPort, entryShared, req.EntryNode != nil, activeGraph, nil)
	if err := s.checkChainForwardListeners(r.Context(), servers, req.Node, ports, entryShared, req.EntryNode != nil, activeGraph, graphWrites, 0); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	o := s.observeStart(r, "chain.create", "创建链路", chainNodeObserveStages)
	defer o.Close()

	// Build the complete initial deployment before exposing it to the scheduler.
	vc := buildVirtualConfig(req.Node)
	endpointID := int64(0)
	serviceUUID := ""
	// 入口端点：vless 出口（现状）或勾选入口协议区块（P4，入口协议配置独立于出口）。
	if externalExit != nil || req.Node.Protocol == shared.ProtocolVLESS || req.EntryNode != nil {
		endpointSource := vc
		if externalExit != nil || req.EntryNode != nil {
			// 外部直链出口：入口端点承载客户端接入（VLESS+Reality），出站走外部协议。
			endpointSource = buildVirtualConfig(*req.EntryNode)
		}
		endpointConfig := endpointSource
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
		endpoint, _, err := s.st.EnsureSharedEndpoint(r.Context(), entrySrv.ID, shared.ProtocolVLESS,
			entryPort, profileHash, endpointJSON)
		if err != nil {
			o.Fail(err)
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		endpointID = endpoint.ID
		if externalExit == nil {
			// 托管出口：入口端点以 VLESS 隧道身份建立到出口业务 inbound 的内部连接。
			serviceUUID = uuid.NewString()
			vc.StaticClients = []shared.ClientCredential{{ID: serviceUUID, Email: "tunnel:" + serviceUUID}}
		}
		// A one-hop shared chain exits directly from the endpoint and does not
		// need a second public listener on the same server.
		if len(servers) == 1 {
			vc.Port = 0
			req.Node.Port = nil
		}
	}
	// hy2 出口共享监听（P4 §3.2）：同机多链并入同一 hy2 监听与端口段（首链 profile 为准）。
	serviceEndpointID := int64(0)
	if externalExit == nil && vc.Protocol == shared.ProtocolHysteria2 {
		endpointJSON, err := json.Marshal(vc)
		if err != nil {
			o.Fail(err)
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		port := 0
		if req.Node.Port != nil {
			port = *req.Node.Port
		}
		svcEndpoint, created, err := s.st.EnsureProtocolSharedEndpoint(r.Context(), exitSrv.ID,
			shared.ProtocolHysteria2, port, endpointJSON)
		if err != nil {
			o.Fail(err)
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		serviceEndpointID = svcEndpoint.ID
		// 并入既有共享监听（首链 profile 为准）：实现参数以端点 config_template 覆盖回本链，
		// 否则自动分配的 port_hop 段与端点段分叉——dispatch 按本链段绑定逐跳 forward，
		// 而出口 DNAT/订阅 mport 仅覆盖端点段，链必断（终审修复 I-1）。
		if !created {
			adoptHy2EndpointParams(&req.Node, &vc, svcEndpoint)
		}
		// 出口节点自身不再监听（共享监听承载）；端口/实现参数以共享端点 realized 为准（Task 6 镜像）。
		vc.Port = 0
		req.Node.Port = nil
	}
	vcJSON, err := json.Marshal(vc)
	if err != nil {
		o.Fail(err)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	initialHops := make([]store.InitialChainHop, 0, len(servers))
	// 明文出口协议（socks/http）经反向隧道时以加密段替代（§21.1 加密段）；
	// 外部直链出口没有反向隧道，plaintext 不参与其 transport 推导。
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
		if n, ok := activeGraph[i]; ok && role == store.HopRoleMiddle {
			hopForwardPort = n.ListenPort
		}
		if (role == store.HopRoleEntry || len(servers) == 1) && endpointID == 0 {
			hopForwardPort = entryPort
		}
		// hy2 端到端跳跃段"全跳同段"（评审 #3）：非用户指定的入口/中间跳 forward 端口
		// 绑定段起点——否则自动端口下 dispatch 下发 HopPortEnd=0+段长-1，agent 自选
		// 端口后段 inbound 循环为空，端口跳跃在自动分配路径全断。入口区块链末段由
		// hy2 outbound 拨号（客户端不跨链跳跃），不参与段绑定。
		if hopForwardPort == 0 && req.EntryNode == nil && req.Node.Protocol == shared.ProtocolHysteria2 &&
			req.Node.PortHop != "" && role != store.HopRoleExit {
			if start, _, err := shared.ParsePortHop(req.Node.PortHop); err == nil {
				hopForwardPort = start
			}
		}
		// 反向链标记（§21.1）：下游无入站能力（nat 且 allowed_ports 空）→ 本跳为 portal 所在上游机，
		// 预生成 tunnel_uuid（面板下发；short_id 由编排器确定性派生）。
		transport := ""
		if i < len(servers)-1 {
			if servers[i+1].IsExternal() {
				// 下游是外部直链节点：本跳直接以 dokodemo 拨外部节点公网 host:port，
				// 无需反向隧道（外部节点不承载我方 inbound，也不在我方控制面）。
				transport = "direct"
			} else {
				transport = transportForServers(servers[i+1], plaintext)
				// 入口终结模式末段：出口为 hy2 且可直连时由本跳 xray 以 hy2 outbound 直拨（§3.2）；
				// 出口无入站能力（零公共端口 NAT）保持 reverse（功能可用，失去 UDP 收益）。
				if i == len(servers)-2 && req.EntryNode != nil &&
					req.Node.Protocol == shared.ProtocolHysteria2 && inboundCapable(servers[i+1]) {
					transport = "hy2"
				}
			}
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
	serviceProtocol := vc.Protocol
	serviceConfig := vcJSON
	servicePort := req.Node.Port
	var serviceExternal *shared.ExternalExit
	if externalExit != nil {
		// 外部直链出口：无出口业务节点；外部规格随修订冻结（ChainRevisionSnapshot）。
		serviceProtocol = ""
		serviceConfig = json.RawMessage(`{}`)
		servicePort = nil
		serviceExternal = externalExit
	}
	deployment, err := s.st.CreateInitialChainDeployment(r.Context(), store.InitialChainDeployment{
		GraphWrites: graphWrites, Name: req.Name, ServiceServerID: exitSrv.ID, ServiceProtocol: serviceProtocol,
		ServicePort: servicePort, ServiceConfig: serviceConfig, EndpointID: endpointID,
		ServiceEndpointID: serviceEndpointID, ServiceUUID: serviceUUID,
		TrafficMultiplierMilli: trafficMultiplierMilli, Hops: initialHops,
		ServiceExternal: serviceExternal,
	})
	if err != nil {
		o.Fail(err)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	chainID := deployment.ChainID
	if s.orchestrator != nil {
		s.orchestrator.NotifyChainUpdated(chainID)
	}
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

// adoptHy2EndpointParams 并入既有 hy2 出口共享监听（EnsureProtocolSharedEndpoint
// created=false，首链 profile 为准，spec §3.2）时，把端点 config_template 的实现参数
// 覆盖回本链表单与虚拟配置：port_hop 驱动 dispatch 逐跳段绑定与 DNAT 覆盖段，
// obfs/带宽/指纹经强制发布进入 realized（订阅源）——保留本链自身值会与端点实际监听
// 分叉断链（终审修复 I-1）。证书/SNI 类参数不下发进这些路径，不纳入覆盖。
func adoptHy2EndpointParams(req *createNodeRequest, vc *shared.VirtualConfig, endpoint *store.SharedEndpoint) {
	var first shared.VirtualConfig
	if err := json.Unmarshal(endpoint.ConfigTemplate, &first); err != nil {
		return
	}
	req.PortHop, vc.PortHop = first.PortHop, first.PortHop
	vc.ObfsPassword = first.ObfsPassword
	vc.UpMbps, vc.DownMbps = first.UpMbps, first.DownMbps
	vc.Fingerprint = first.Fingerprint
}

// adoptEntryProtocol 把入口共享端点已保存的 VLESS 配置回填到入口协议请求（外部直链
// 出口编辑未提交入口参数时沿用原配置，避免 short_id/dest 重新随机生成导致订阅参数漂移）。
func adoptEntryProtocol(req *createNodeRequest, config shared.VirtualConfig) {
	req.Protocol = shared.ProtocolVLESS
	req.Network = config.Network
	req.Security = config.Security
	req.CertMode = config.CertMode
	req.TLSDomain = config.TLSDomain
	req.ServiceName = config.ServiceName
	req.Path = config.Path
	req.Mode = config.Mode
	req.Host = config.Host
	req.Flow = config.Flow
	req.Encryption = config.Encryption
	req.Fingerprint = config.Fingerprint
	var template struct {
		StreamSettings struct {
			RealitySettings struct {
				ShortIDs    []string `json:"shortIds"`
				Dest        string   `json:"dest"`
				ServerNames []string `json:"serverNames"`
			} `json:"realitySettings"`
		} `json:"streamSettings"`
	}
	if json.Unmarshal(config.Template, &template) != nil {
		return
	}
	reality := template.StreamSettings.RealitySettings
	if len(reality.ShortIDs) > 0 {
		req.ShortID = reality.ShortIDs[0]
	}
	req.Dest = reality.Dest
	req.ServerNames = reality.ServerNames
}

func (s *Server) handleEditChain(w http.ResponseWriter, r *http.Request) {
	s.orchestrationMu.Lock()
	defer s.orchestrationMu.Unlock()
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
	// 部署失败（failed/active_failed/exchange_failed）的链允许编辑后重新编排（§21）；
	// 等待 Agent（waiting_for_agent）的链允许再编辑取代等待中的 revision——该状态尚未下发任何
	// 命令（编排暂停），废弃安全（§21.1）；applying/active_unconfirmed 仍拒绝并发编辑。
	failed := chain.Status == store.ChainStatusFailed || chain.Status == store.ChainStatusActiveFailed ||
		chain.Status == store.ChainStatusExchangeFailed
	waitingForAgent := chain.Status == store.ChainStatusWaitingForAgent
	if chain.DesiredRevisionID != 0 && !failed && !waitingForAgent {
		writeError(w, http.StatusConflict, "链路已有部署中的编辑")
		return
	}
	var waitingRevisionID int64
	if waitingForAgent {
		waitingRevisionID = chain.DesiredRevisionID
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
		} else if current == nil && chain.Status == store.ChainStatusExchangeFailed {
			if revision, latestErr := s.st.LatestChainRevision(r.Context(), chain.ID); latestErr == nil {
				failedRevision = revision
				current = revision
			}
		}
		if current != nil {
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
	graphWrites, activeGraph, err := s.prepareChainGraphs(r.Context(), chain.ID, req.Hops, req.Orchestrations)
	if err != nil {
		templateError(w, err)
		return
	}
	applyGraphPorts(activeGraph, len(req.Hops), &req.EntryPort, &req.Node)
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
		if server.IsExternal() && (i < len(req.Hops)-1 || len(req.Hops) == 1) {
			writeError(w, http.StatusBadRequest,
				fmt.Sprintf("未托管服务器 %s 只能作为链路的最后一跳（出口）", server.Alias))
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
	exitSrv := servers[len(servers)-1]
	externalExit := exitSrv.ParseExternalExit()
	if exitSrv.IsExternal() && externalExit == nil {
		writeError(w, http.StatusBadRequest,
			fmt.Sprintf("未托管服务器 %s 的外部直链配置损坏，请重新保存该服务器", exitSrv.Alias))
		return
	}
	if externalExit != nil {
		if err := externalExit.Validate(); err != nil {
			writeError(w, http.StatusBadRequest,
				fmt.Sprintf("外部直链出口配置不可用：%v（请编辑服务器 %s 补全协议参数）", err, exitSrv.Alias))
			return
		}
	}
	// 导出/出口形态切换不在 v1 编辑范围：已发布修订已冻结出口形态（业务节点或外部规格），
	// 切换需要重建入口端点与全部下游 piece，请新建链路。
	if (externalExit != nil) != (current.Snapshot.ServiceExternal != nil) {
		writeError(w, http.StatusBadRequest, "暂不支持在托管出口与外部直链出口之间切换，请新建链路")
		return
	}
	exitProtocol := ""
	if externalExit != nil {
		exitProtocol = externalExit.Protocol
		req.Node = createNodeRequest{}
	} else {
		req.Node.Name = req.Name
		req.Node.ServerID = exitSrv.ID
		if err := req.Node.normalize(); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if len(servers) > 1 && req.Node.Protocol == shared.ProtocolDokodemo {
			writeError(w, http.StatusBadRequest, "dokodemo-door 不能作为中转链出口")
			return
		}
		exitProtocol = req.Node.Protocol
	}
	// 入口协议（客户端接入端点）：外部直链出口链恒挂在入口共享端点上，入口服务器 A 可
	// 与普通链路一样配置完整 VLESS 协议细节（未提交时沿用当前端点配置）；托管出口链沿用
	// 入口协议区块 v1（固定 VLESS+Reality）。
	if req.EntryNode == nil && externalExit != nil {
		if current.Snapshot.EndpointID == 0 {
			writeError(w, http.StatusBadRequest, "该链路缺少入口共享端点，请新建链路后再使用外部直链出口")
			return
		}
		req.EntryNode = &createNodeRequest{Protocol: shared.ProtocolVLESS}
		if endpoint, err := s.st.SharedEndpointByID(r.Context(), current.Snapshot.EndpointID); err == nil {
			// 入口协议细节沿用当前端点配置（服务端记住的完整 VLESS 参数）。
			var currentEntry shared.VirtualConfig
			if json.Unmarshal(endpoint.ConfigTemplate, &currentEntry) == nil {
				adoptEntryProtocol(req.EntryNode, currentEntry)
			}
		}
	}
	if req.EntryNode != nil {
		if len(req.Hops) < 2 {
			writeError(w, http.StatusBadRequest, "入口协议区块仅用于多跳链路（单跳客户端直连出口即可）")
			return
		}
		// 存量端到端链（快照无入口端点）不能补勾入口区块（终审修复 I-2）：端点创建门控
		// 要求快照已有端点，放行会把末段 transport 半应用为 hy2 而端点缺失 → 断链。
		// v1 不支持新增入口区块；取消勾选（entry_node 省略，端点回收）不受影响。
		if current.Snapshot.EndpointID == 0 {
			writeError(w, http.StatusBadRequest, "存量链路不能新增入口协议区块，请新建链路")
			return
		}
		if req.EntryNode.Protocol != "" && req.EntryNode.Protocol != shared.ProtocolVLESS {
			writeError(w, http.StatusBadRequest, "入口协议仅支持 VLESS（入口共享端点）")
			return
		}
		if externalExit == nil && exitProtocol != shared.ProtocolHysteria2 && exitProtocol != shared.ProtocolVLESS {
			writeError(w, http.StatusBadRequest, "入口协议区块 v1 仅支持 hysteria2/vless 出口")
			return
		}
		req.EntryNode.Protocol = shared.ProtocolVLESS
		if externalExit == nil {
			req.EntryNode.Security = shared.SecurityReality // 托管出口入口块固定 reality（子参数可空自动生成）
		}
		req.EntryNode.Name = req.Name
		req.EntryNode.ServerID = servers[0].ID
		if err := req.EntryNode.normalize(); err != nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("入口协议参数: %v", err))
			return
		}
		if externalExit != nil {
			// 落地是入口服务器 A：ACME 证书域名按入口机地址检测填充。
			if err := applyACMEDomain(req.EntryNode, servers[0]); err != nil {
				writeError(w, http.StatusBadRequest, fmt.Sprintf("入口协议参数: %v", err))
				return
			}
		}
	}
	// ACME 证书模式：落地（出口）服务器须有域名型公网地址（镜像创建路径）。
	if externalExit == nil {
		if err := applyACMEDomain(&req.Node, exitSrv); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	oldByServer := make(map[int64]store.ChainRevisionHop, len(current.Snapshot.Hops))
	for _, hop := range current.Snapshot.Hops {
		oldByServer[hop.ServerID] = hop
	}
	hasFixedForward := len(activeGraph) > 0
	for _, srv := range servers[:len(servers)-1] {
		hasFixedForward = hasFixedForward || oldByServer[srv.ID].ForwardPort > 0
	}
	var graphHy2Endpoint *store.SharedEndpoint
	if req.Node.Protocol == shared.ProtocolHysteria2 {
		if hasFixedForward {
			graphHy2Endpoint, err = s.prepareGraphHy2Endpoint(r.Context(), &req.Node, exitSrv.ID, activeGraph, len(servers)-1)
			if err != nil {
				writeError(w, http.StatusBadRequest, err.Error())
				return
			}
		}
		ownEndpointID := current.Snapshot.ServiceEndpointID
		if graphHy2Endpoint != nil {
			ownEndpointID = graphHy2Endpoint.ID
		}
		if err := s.resolveHy2PortHop(r.Context(), &req.Node, exitSrv, req.ChainID, ownEndpointID); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
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
	// 入口共享门控（评审移交②）：对齐"实际会挂端点"的条件——编辑路径仅在快照已有
	// 入口端点（Snapshot.EndpointID != 0）时挂载/保留端点（见下方 endpointID 解析）；
	// 无端点链改 vless 出口或勾选入口区块并不会挂端点，入口端口仍是 dokodemo 实监听，
	// 必须照常做冲突校验。外部直链出口链恒有入口端点。
	entryShared := (externalExit != nil || req.Node.Protocol == shared.ProtocolVLESS || req.EntryNode != nil) &&
		current.Snapshot.EndpointID != 0
	sharedHy2Exit := graphHy2Endpoint != nil && activeGraph[len(servers)-1].ListenPort > 0
	if req.EntryPort != nil {
		if *req.EntryPort < 1 || *req.EntryPort > 65535 || checkPortInRanges(servers[0], *req.EntryPort) != nil {
			writeError(w, http.StatusBadRequest, "入口端口不在可用范围内")
			return
		}
		// 入口走共享端点合并时跳过前置校验；excludeChainID 排除本链既有占用。
		if !entryShared && !(len(servers) == 1 && sharedHy2Exit) {
			if err := s.checkPortConflict(r.Context(), servers[0].ID, req.Node.Protocol, *req.EntryPort, 0, req.ChainID); err != nil {
				writeError(w, http.StatusBadRequest, err.Error())
				return
			}
		}
	}
	if len(servers) == 1 && req.EntryPort != nil {
		req.Node.Port = req.EntryPort
	}
	// 出口节点端口冲突前置校验（镜像创建路径），excludeChainID 排除本链既有占用。
	// 单跳 vless 且实际挂共享端点时节点端口即入口端口（上方已别名），由共享端点合并
	// 接管、编排后不实际监听；冲突判定已由入口校验门控，此处跳过避免重复误判。
	singleHopShared := len(servers) == 1 && entryShared
	if req.Node.Port != nil && !singleHopShared && !sharedHy2Exit {
		if err := s.checkPortConflict(r.Context(), servers[len(servers)-1].ID, req.Node.Protocol, *req.Node.Port, 0, req.ChainID); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	ports := chainForwardPorts(servers, req.Node, req.EntryPort, entryShared, req.EntryNode != nil, activeGraph, oldByServer)
	if err := s.checkChainForwardListeners(r.Context(), servers, req.Node, ports, entryShared, req.EntryNode != nil, activeGraph, graphWrites, chain.ID); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	vc := buildVirtualConfig(req.Node)
	endpointID := int64(0)
	serviceUUID := current.Snapshot.ServiceUUID
	if externalExit != nil {
		// 外部直链出口：入口端点承载客户端接入（VLESS+Reality），不使用隧道身份。
		serviceUUID = ""
	}
	if (externalExit != nil || vc.Protocol == shared.ProtocolVLESS || req.EntryNode != nil) &&
		current.Snapshot.EndpointID != 0 {
		if serviceUUID == "" && externalExit == nil {
			serviceUUID = uuid.NewString()
		}
		endpointPort := 0
		if req.EntryPort != nil {
			endpointPort = *req.EntryPort
		}
		// 入口协议区块（P4）：入口端点配置独立于出口协议。
		endpointSource := vc
		if externalExit != nil {
			endpointSource = buildVirtualConfig(*req.EntryNode)
		} else if req.EntryNode != nil {
			endpointSource = buildVirtualConfig(*req.EntryNode)
		}
		endpointConfig := endpointSource
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
				shared.ProtocolVLESS, endpointPort, profileHash, endpointJSON)
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
	// hy2 出口共享监听（P4 §3.2，镜像创建路径）：同机多链并入同一 hy2 监听与端口段。
	serviceEndpointID := int64(0)
	if externalExit == nil && vc.Protocol == shared.ProtocolHysteria2 {
		endpointJSON, err := json.Marshal(vc)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		port := 0
		if req.Node.Port != nil {
			port = *req.Node.Port
		}
		svcEndpoint, created, err := s.st.EnsureProtocolSharedEndpoint(r.Context(), servers[len(servers)-1].ID,
			shared.ProtocolHysteria2, port, endpointJSON)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		serviceEndpointID = svcEndpoint.ID
		// 并入既有共享监听（首链 profile 为准，镜像创建路径）：实现参数以端点
		// config_template 覆盖回本链，避免 port_hop 段分叉断链（终审修复 I-1）。
		if !created {
			adoptHy2EndpointParams(&req.Node, &vc, svcEndpoint)
		}
		// 出口节点自身不再监听（共享监听承载）；端口/实现参数以共享端点 realized 为准（Task 6 镜像）。
		vc.Port = 0
		req.Node.Port = nil
	}
	serviceConfig, err := json.Marshal(vc)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !hasFixedForward {
		// Legacy automatic forwarding keeps the outlet profile selected at provisioning.
		ports = chainForwardPorts(servers, req.Node, req.EntryPort, endpointID != 0, req.EntryNode != nil, activeGraph, oldByServer)
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
		if i < len(ports) {
			hop.ForwardPort = ports[i]
		} else if i == 0 {
			if endpointID != 0 {
				hop.ForwardPort = 0
			} else if req.EntryPort != nil {
				hop.ForwardPort = *req.EntryPort
			} else if n, ok := activeGraph[i]; ok {
				hop.ForwardPort = n.ListenPort
			}
		}
		hop.Address = req.Hops[i].Address // 地址引用以本次编辑提交为准（§9）
		desiredHops = append(desiredHops, hop)
	}
	plaintext := req.Node.Protocol == shared.ProtocolSocks || req.Node.Protocol == shared.ProtocolHTTP
	for i := 0; i+1 < len(desiredHops); i++ {
		transport := "direct"
		if servers[i+1].IsExternal() {
			// 下游是外部直链节点：本跳直接 dokodemo 拨外部 host:port，无反向隧道。
			transport = "direct"
		} else if !inboundCapable(servers[i+1]) {
			transport = "reverse"
		} else if plaintext {
			transport = "encrypted"
		}
		// 入口终结模式末段（P4 §3.2）：出口 hy2 且可直连 → 本跳 hy2 outbound 直拨。
		if externalExit == nil && i == len(desiredHops)-2 && req.EntryNode != nil &&
			req.Node.Protocol == shared.ProtocolHysteria2 && inboundCapable(servers[i+1]) {
			transport = "hy2"
		}
		old := desiredHops[i]
		if old.Transport != transport || old.TunnelUUID == "" && transport != "direct" {
			desiredHops[i].PortalPort = 0
			desiredHops[i].PortalPublicKey = ""
			desiredHops[i].PortalServerName = ""
		}
		desiredHops[i].Transport = transport
		if transport == "direct" || transport == "hy2" {
			desiredHops[i].TunnelUUID = ""
		} else if desiredHops[i].TunnelUUID == "" {
			desiredHops[i].TunnelUUID = uuid.NewString()
		}
	}
	desired := store.ChainRevisionSnapshot{Name: req.Name, ServiceNodeID: current.Snapshot.ServiceNodeID,
		ServiceServerID: servers[len(servers)-1].ID, ServiceConfig: serviceConfig,
		EndpointID: endpointID, ServiceEndpointID: serviceEndpointID, ServiceUUID: serviceUUID,
		TrafficMultiplierMilli: multiplier, Hops: desiredHops, ServiceExternal: externalExit}
	if externalExit != nil {
		// 外部直链出口：无出口业务节点与隧道身份；外部规格随修订冻结。
		desired.ServiceNodeID = 0
		desired.ServiceConfig = json.RawMessage(`{}`)
		desired.ServiceUUID = ""
	}
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
	revision := plannedChainRevision(chain, current, desired, plan)
	revision.Edit.GraphWrites = graphWrites
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
	// 废弃被取代的等待中 revision（Agent 离线期间的再编辑，§21.1）：等待中无在途
	// 命令，仅落 revision 终态；Agent 上线后按新 revision 恢复编排。
	if waitingRevisionID != 0 {
		if err := s.st.AbandonChainRevision(r.Context(), waitingRevisionID, "被新编辑取代"); err != nil {
			o.Fail(err)
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	if s.orchestrator != nil {
		s.orchestrator.NotifyChainUpdated(chain.ID)
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
	// UDP 中转管道分层（§3.2）：出口协议纳入 hop settings 哈希——协议编辑变更
	// （如 trojan→ss）时触发 forward piece 重发以切换 tcp/tcp,udp；协议不变时
	// current/desired 同函数计算，哈希相等不误重发。
	var svc struct {
		Protocol string `json:"protocol"`
	}
	_ = json.Unmarshal(snapshot.ServiceConfig, &svc)
	// 外部直链出口协议均为 TCP 承载：管道层恒 tcp（不看外部协议名，socks/ss 也不开 UDP）。
	forwardLayers := "tcp"
	if snapshot.ServiceExternal == nil {
		forwardLayers = shared.PortLayers(svc.Protocol)
	}
	for index, hop := range snapshot.Hops {
		settings, _ := json.Marshal(map[string]any{
			"tunnel_uuid":    hop.TunnelUUID,
			"local_only":     index == 0 && snapshot.EndpointID != 0,
			"address":        hop.Address, // 地址引用（§9）：选择变更须触发本跳 piece 重发并沿下游哈希传播
			"forward_layers": forwardLayers,
		})
		hops = append(hops, dispatch.RevisionHopSpec{HopID: hop.HopID, ServerID: hop.ServerID,
			Transport: hop.Transport, ListenPort: hop.ForwardPort, Settings: settings})
	}
	return dispatch.RevisionTopology{RevisionID: revisionID, ServiceID: snapshot.ServiceNodeID,
		Service: snapshot.ServiceConfig, Hops: hops,
		ServiceEndpointID: snapshot.ServiceEndpointID,
		DirectShared:      snapshot.EndpointID != 0 && len(snapshot.Hops) == 1,
		External:          snapshot.ServiceExternal}
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
	if s.orchestrator != nil {
		s.orchestrator.ResumeChain(id)
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
	affectedUsers, err := s.st.CatalogUserIDs(r.Context())
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
	if s.orchestrator != nil {
		s.orchestrator.StopChain(id)
	}
	pieces, err := s.chainDeploymentPieces(r.Context(), id)
	if err != nil {
		o.Fail(err)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if len(pieces) == 0 {
		for i, h := range hops {
			for _, kind := range store.ChainHopPieces(hops, i) {
				pieces = append(pieces, chainPieceRef{ServerID: h.ServerID, HopID: h.ID, Kind: kind})
			}
		}
	}
	managedServer := func(serverID int64) bool {
		srv, err := s.st.ServerByID(r.Context(), serverID)
		return err == nil && !srv.IsExternal()
	}
	for _, piece := range pieces {
		if !managedServer(piece.ServerID) {
			continue
		}
		if _, err := s.disp.Enqueue(r.Context(), piece.ServerID, shared.TypeRemoveChainHop,
			shared.RemoveChainHopPayload{HopID: piece.HopID, Kind: piece.Kind}); err != nil {
			o.Fail(err)
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	for _, h := range hops {
		// 出口业务节点走现有删除流程（remove_node + 删行，§6）；已转为外部节点的服务器跳过下发命令。
		if h.Role == store.HopRoleExit && h.NodeID != 0 {
			if managedServer(h.ServerID) {
				if _, err := s.disp.Enqueue(r.Context(), h.ServerID, shared.TypeRemoveNode,
					shared.RemoveNodePayload{NodeID: h.NodeID}); err != nil {
					o.Fail(err)
					writeError(w, http.StatusInternalServerError, err.Error())
					return
				}
			}
			if err := s.st.DeleteNode(r.Context(), h.NodeID); err != nil {
				o.Fail(err)
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
		}
	}
	log.Printf("panel: chain %d delete: cleanup pieces=%d", id, len(pieces))
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
	// hy2 出口共享监听（P4，评审移交①）：删链释放引用后 reconcile 出口端点，
	// 重算 clients（tunnel 身份/业务用户），监听本身保留给其余引用链。
	if chain.ServiceEndpointID != 0 {
		if err := s.disp.ReconcileSharedEndpoint(r.Context(), chain.ServiceEndpointID); err != nil {
			log.Printf("panel: reconcile service endpoint %d after chain delete: %v", chain.ServiceEndpointID, err)
		}
	}
	if s.subscriptions != nil {
		o.WatchUsers(affectedUsers)
		s.subscriptions.EnqueueUsers(affectedUsers, s.panelBase(r))
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

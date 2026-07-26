package panel

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"lattix/backend/internal/dispatch"
	"lattix/backend/internal/store"
	"lattix/shared"
)

// chainHopDTO 是链跳对象的 API 表示（§21）。
type chainHopDTO struct {
	ID              int64  `json:"id"`
	Seq             int    `json:"seq"`
	ServerID        int64  `json:"server_id"`
	ServerAlias     string `json:"server_alias"`
	Role            string `json:"role"` // entry/middle/exit
	NodeID          int64  `json:"node_id"`
	Status          string `json:"status"` // pending/applying/active/failed
	Error           string `json:"error"`
	ForwardPort     int    `json:"forward_port"` // entry 跳 = 订阅端口（监听侧）
	PortalPort      int    `json:"portal_port"`
	PortalPublicKey string `json:"portal_public_key,omitempty"`
	TunnelUUID      string `json:"tunnel_uuid,omitempty"`
}

// chainDTO 是链对象的 API 表示（含逐跳状态，§21）。
type chainDTO struct {
	ID        int64         `json:"id"`
	Name      string        `json:"name"`
	Status    string        `json:"status"` // pending/applying/active/degraded/failed
	Error     string        `json:"error"`  // 失败时定位到跳
	CreatedAt time.Time     `json:"created_at"`
	Hops      []chainHopDTO `json:"hops"`
}

// toChainDTO 组装链 DTO（跳按 seq 升序：首位入口，末位出口）。
func (s *Server) toChainDTO(r *http.Request, c store.Chain) (chainDTO, error) {
	hops, err := s.st.ChainHops(r.Context(), c.ID)
	if err != nil {
		return chainDTO{}, err
	}
	out := chainDTO{ID: c.ID, Name: c.Name, Status: c.Status, Error: c.Error, CreatedAt: c.CreatedAt, Hops: []chainHopDTO{}}
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
			PortalPort:      h.PortalPort,
			PortalPublicKey: h.PortalPublicKey,
			TunnelUUID:      h.TunnelUUID,
		}
		if srv, err := s.st.ServerByID(r.Context(), h.ServerID); err == nil {
			dto.ServerAlias = srv.Alias
		}
		out.Hops = append(out.Hops, dto)
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
	Name      string            `json:"name"`
	Entry     chainHopRef       `json:"entry"`
	Middle    []chainHopRef     `json:"middle"` // 0-2 个
	Exit      chainHopRef       `json:"exit"`
	EntryPort *int              `json:"entry_port"` // 留空 = 自动；须在入口机可用段内
	Node      createNodeRequest `json:"node"`       // 出口业务节点表单（server_id 忽略，取 exit.server_id）
}

type chainHopRef struct {
	ServerID int64 `json:"server_id"`
}

// handleCreateChain 处理 POST /api/chains：构图校验 → 落库出口业务节点（pending）+
// chains/chain_hops 行 → 触发编排（§21.1：出口先就绪，逐跳向外，入口最后生效）。
func (s *Server) handleCreateChain(w http.ResponseWriter, r *http.Request) {
	var req createChainRequest
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if len(req.Middle) > 2 {
		writeError(w, http.StatusBadRequest, "链长上限 4 跳（入口 + 中间跳 ≤2 + 出口）")
		return
	}
	// 出口节点协议表单校验（dokodemo 为端口转发，无用户概念，不能作链出口）。
	req.Node.Name = req.Name
	req.Node.ServerID = req.Exit.ServerID
	if err := req.Node.normalize(); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.Node.Protocol == shared.ProtocolDokodemo {
		writeError(w, http.StatusBadRequest, "dokodemo-door 不能作为链出口节点")
		return
	}

	// 逐跳加载服务器并校验：同 server 不重复（O(n) 查重即环检测）；
	// 入口与中间跳必须有入站能力（direct 或 allowed_ports 非空），出口任意（§21）。
	refs := append(append([]chainHopRef{req.Entry}, req.Middle...), req.Exit)
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
		servers = append(servers, srv)
	}
	entrySrv, exitSrv := servers[0], servers[len(servers)-1]
	name, err := resolveNameTemplate(req.Name, nameTemplateValues{
		Location: entrySrv.Alias,
		ServerID: entrySrv.ID,
		Protocol: req.Node.Protocol,
		Port:     req.EntryPort,
		Entry:    entrySrv.Alias,
		EntryID:  entrySrv.ID,
		Exit:     exitSrv.Alias,
		ExitID:   exitSrv.ID,
		Hops:     len(servers),
		Tags:     decodeServerTags(entrySrv.Tags),
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
	// 出口节点端口：受限直连 NAT 机上用户指定端口必须在段内（自动端口由编排器携带候选展开）。
	if req.Node.Port != nil {
		if err := checkPortInRanges(exitSrv, *req.Node.Port); err != nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("出口节点端口 %d 不在出口机可用段内", *req.Node.Port))
			return
		}
	}

	// 落库：出口业务节点（pending，由编排器阶段 1 下发）+ chains/chain_hops 行。
	vc := buildVirtualConfig(req.Node)
	vcJSON, err := json.Marshal(vc)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	nodeID, err := s.st.InsertNode(r.Context(), req.Name, exitSrv.ID, vc.Protocol, req.Node.Port, vcJSON)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	chainID, err := s.st.InsertChain(r.Context(), req.Name)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	for i, srv := range servers {
		role := store.HopRoleMiddle
		if i == 0 {
			role = store.HopRoleEntry
		} else if i == len(servers)-1 {
			role = store.HopRoleExit
		}
		hopNodeID := int64(0)
		if role == store.HopRoleExit {
			hopNodeID = nodeID
		}
		hopForwardPort := 0
		if role == store.HopRoleEntry {
			hopForwardPort = entryPort
		}
		// 反向链标记（§21.1）：下游无入站能力（nat 且 allowed_ports 空）→ 本跳为 portal 所在上游机，
		// 预生成 tunnel_uuid（面板下发；short_id 由编排器确定性派生）。
		tunnelUUID := ""
		if i < len(servers)-1 && !inboundCapable(servers[i+1]) {
			tunnelUUID = uuid.NewString()
		}
		if _, err := s.st.InsertChainHop(r.Context(), chainID, i, srv.ID, role, hopNodeID, hopForwardPort, tunnelUUID); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	if err := s.disp.StartChain(r.Context(), chainID); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	chain, err := s.st.ChainByID(r.Context(), chainID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	dto, err := s.toChainDTO(r, *chain)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.audit(r, "chain.create", nil, nil, map[string]any{"chain_id": chainID, "name": req.Name, "hops": len(dto.Hops)})
	writeJSON(w, http.StatusCreated, dto)
}

// handleRetryChain 处理 POST /api/chains/{id}/retry：只重放失败 piece（§21）。
func (s *Server) handleRetryChain(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid chain id")
		return
	}
	if err := s.disp.RetryChain(r.Context(), id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "链不存在")
			return
		}
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	chain, err := s.st.ChainByID(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	dto, err := s.toChainDTO(r, *chain)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.audit(r, "chain.retry", nil, nil, map[string]any{"chain_id": id})
	writeJSON(w, http.StatusOK, dto)
}

// handleDeleteChain 处理 DELETE /api/chains/{id}：反向拆链（§21）——
// 入口→出口逐跳下发 remove_chain_hop（离线留队列补发），出口业务节点走现有删除流程，最后删行。
func (s *Server) handleDeleteChain(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid chain id")
		return
	}
	if _, err := s.st.ChainByID(r.Context(), id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "链不存在")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	hops, err := s.st.ChainHops(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	for i, h := range hops {
		for _, kind := range dispatch.ChainHopPieces(hops, i) {
			if _, err := s.disp.Enqueue(r.Context(), h.ServerID, shared.TypeRemoveChainHop,
				shared.RemoveChainHopPayload{HopID: h.ID, Kind: kind}); err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
		}
		// 出口业务节点走现有删除流程（remove_node + 删行，§6）。
		if h.Role == store.HopRoleExit && h.NodeID != 0 {
			if _, err := s.disp.Enqueue(r.Context(), h.ServerID, shared.TypeRemoveNode,
				shared.RemoveNodePayload{NodeID: h.NodeID}); err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			if err := s.st.DeleteNode(r.Context(), h.NodeID); err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
		}
	}
	if err := s.st.DeleteChain(r.Context(), id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.audit(r, "chain.delete", nil, nil, map[string]any{"chain_id": id, "hops": len(hops)})
	w.WriteHeader(http.StatusNoContent)
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

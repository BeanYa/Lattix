package panel

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"lattix/backend/internal/store"
)

const (
	maxHopGraphServers  = 8
	maxHopGraphTriggers = 16
	maxHopGraphPriority = 9999
)

// hopOrchestrationNodeInput / edgeInput 是保存接口的请求形状。
type hopOrchestrationNodeInput struct {
	Kind       string `json:"kind"`
	ServerID   int64  `json:"server_id,omitempty"`
	IsRoot     bool   `json:"is_root,omitempty"`
	Address    string `json:"address,omitempty"`
	ListenPort int    `json:"listen_port,omitempty"`
	Subject    string `json:"subject,omitempty"`
	Action     string `json:"action,omitempty"`
	Priority   int    `json:"priority,omitempty"`
}

type hopOrchestrationEdgeInput struct {
	From int `json:"from"`
	To   int `json:"to"`
}

type hopOrchestrationSaveRequest struct {
	ChainID int64                       `json:"chain_id"`
	Seq     int                         `json:"seq"`
	Nodes   []hopOrchestrationNodeInput `json:"nodes"`
	Edges   []hopOrchestrationEdgeInput `json:"edges"`
}

// buildGraphFromRequest 把请求转成待校验/落库的图；边的 from/to 此时是 nodes 下标。
func buildGraphFromRequest(req hopOrchestrationSaveRequest) (store.ChainHopGraph, error) {
	graph := store.ChainHopGraph{ChainID: req.ChainID, Seq: req.Seq}
	for i, node := range req.Nodes {
		kind := strings.TrimSpace(node.Kind)
		if kind != store.HopGraphKindServer && kind != store.HopGraphKindTrigger {
			return graph, fmt.Errorf("第 %d 跳：节点 %d 的类型 %q 无效", req.Seq+1, i+1, node.Kind)
		}
		graph.Nodes = append(graph.Nodes, store.ChainHopGraphNode{
			Kind:       kind,
			ServerID:   node.ServerID,
			Address:    strings.TrimSpace(node.Address),
			ListenPort: node.ListenPort,
			Subject:    strings.TrimSpace(node.Subject),
			Action:     strings.TrimSpace(node.Action),
			Priority:   node.Priority,
			IsRoot:     node.IsRoot,
			Ordinal:    i,
		})
	}
	if len(graph.Nodes) == 0 {
		if len(req.Edges) > 0 {
			return graph, fmt.Errorf("第 %d 跳：空编排不能带连线", req.Seq+1)
		}
		return graph, nil
	}
	for _, edge := range req.Edges {
		if edge.From < 0 || edge.From >= len(req.Nodes) || edge.To < 0 || edge.To >= len(req.Nodes) {
			return graph, fmt.Errorf("第 %d 跳：连线下标越界", req.Seq+1)
		}
		graph.Edges = append(graph.Edges, store.ChainHopGraphEdge{
			FromNode: int64(edge.From), ToNode: int64(edge.To),
		})
	}
	return graph, nil
}

func validTriggerCombo(subject, action string) bool {
	switch {
	case subject == store.TriggerSubjectSelf && (action == store.TriggerActionBanSuspected || action == store.TriggerActionBanBanned):
		return true
	case subject == store.TriggerSubjectPrev && action == store.TriggerActionSegmentTimeout:
		return true
	case subject == store.TriggerSubjectNext && action == store.TriggerActionSegmentTimeout:
		return true
	case subject == store.TriggerSubjectRoot && action == store.TriggerActionRecovered:
		return true
	}
	return false
}

// validateHopOrchestration 校验一张待保存的编排图（spec §4）。边的 FromNode/ToNode 是 nodes 下标。
func validateHopOrchestration(ctx context.Context, st *store.Store, chain *store.Chain, hops []store.ChainHop, seq int, graph store.ChainHopGraph) error {
	prefix := func(format string, args ...any) error {
		return fmt.Errorf("第 %d 跳："+format, append([]any{seq + 1}, args...)...)
	}
	if seq < 0 || seq >= len(hops) {
		return prefix("跳位不存在")
	}
	if len(graph.Nodes) == 0 {
		if len(graph.Edges) != 0 {
			return prefix("空编排不能带连线")
		}
		return nil
	}
	if len(graph.Edges) == 0 {
		return prefix("编排图至少需要一条连线")
	}

	// 节点分类与数量上限。
	var servers, triggers []int
	for i, node := range graph.Nodes {
		switch node.Kind {
		case store.HopGraphKindServer:
			if node.ServerID <= 0 {
				return prefix("服务器节点缺少 server_id")
			}
			servers = append(servers, i)
		case store.HopGraphKindTrigger:
			if !validTriggerCombo(node.Subject, node.Action) {
				return prefix("触发器类型无效（%s/%s）", node.Subject, node.Action)
			}
			if node.Priority < 0 || node.Priority > maxHopGraphPriority {
				return prefix("触发器优先级须在 0-%d", maxHopGraphPriority)
			}
			triggers = append(triggers, i)
		default:
			return prefix("节点类型无效")
		}
	}
	if len(servers) > maxHopGraphServers {
		return prefix("服务器节点不能超过 %d 台", maxHopGraphServers)
	}
	if len(triggers) > maxHopGraphTriggers {
		return prefix("触发器不能超过 %d 个", maxHopGraphTriggers)
	}

	// 边：交替、自环、重复。
	type pair struct{ from, to int }
	seenEdges := make(map[pair]bool, len(graph.Edges))
	out := make([][]int, len(graph.Nodes))
	in := make([][]int, len(graph.Nodes))
	for _, edge := range graph.Edges {
		from, to := int(edge.FromNode), int(edge.ToNode)
		if from < 0 || from >= len(graph.Nodes) || to < 0 || to >= len(graph.Nodes) {
			return prefix("连线下标越界")
		}
		if from == to {
			return prefix("不允许自环")
		}
		if seenEdges[pair{from, to}] {
			return prefix("存在重复连线")
		}
		seenEdges[pair{from, to}] = true
		fromKind, toKind := graph.Nodes[from].Kind, graph.Nodes[to].Kind
		if !((fromKind == store.HopGraphKindServer && toKind == store.HopGraphKindTrigger) ||
			(fromKind == store.HopGraphKindTrigger && toKind == store.HopGraphKindServer)) {
			return prefix("连线必须是 服务器→触发器 或 触发器→服务器")
		}
		out[from] = append(out[from], to)
		in[to] = append(in[to], from)
	}

	// 根：唯一、与存量根/当前服务器一致。
	root := -1
	for _, i := range servers {
		if graph.Nodes[i].IsRoot {
			if root >= 0 {
				return prefix("只能有一个根服务器")
			}
			root = i
		}
	}
	if root < 0 {
		return prefix("缺少根服务器")
	}
	existing, err := st.ChainHopGraph(ctx, chain.ID, seq)
	if err != nil {
		return err
	}
	expectedRoot := hops[seq].ServerID
	if existing != nil {
		for _, node := range existing.Nodes {
			if node.IsRoot {
				expectedRoot = node.ServerID
				break
			}
		}
	}
	if graph.Nodes[root].ServerID != expectedRoot {
		return prefix("根服务器必须是该跳原服务器（#%d）", expectedRoot)
	}

	// 当前运行服务器必须在图中。
	currentFound := false
	serverSeen := make(map[int64]bool, len(servers))
	for _, i := range servers {
		if serverSeen[graph.Nodes[i].ServerID] {
			return prefix("同一服务器在编排图中重复")
		}
		serverSeen[graph.Nodes[i].ServerID] = true
		if graph.Nodes[i].ServerID == hops[seq].ServerID {
			currentFound = true
		}
	}
	if !currentFound {
		return prefix("当前运行的服务器必须保留在编排图中")
	}

	// 触发器度数、归属与唯一性。
	recoveryCount := 0
	triggerKeys := make(map[string]bool, len(triggers))
	for _, i := range triggers {
		node := graph.Nodes[i]
		if len(out[i]) != 1 {
			return prefix("触发器只能连接一个目标服务器")
		}
		if node.Subject == store.TriggerSubjectRoot {
			recoveryCount++
			if recoveryCount > 1 {
				return prefix("原节点恢复触发器最多一个")
			}
			if len(in[i]) != 0 {
				return prefix("原节点恢复触发器不能有来源服务器")
			}
			if out[i][0] != root {
				return prefix("原节点恢复触发器只能连接根服务器")
			}
			continue
		}
		if len(in[i]) != 1 {
			return prefix("触发器必须且只能连接一个所属服务器")
		}
		owner := in[i][0]
		target := out[i][0]
		if target == owner {
			return prefix("触发器不能指向自己的所属服务器")
		}
		key := fmt.Sprintf("%d|%s|%s", graph.Nodes[owner].ServerID, node.Subject, node.Action)
		if triggerKeys[key] {
			return prefix("同一服务器同一触发条件只能配置一个触发器")
		}
		triggerKeys[key] = true
		if node.Subject == store.TriggerSubjectPrev && seq == 0 {
			return prefix("入口跳没有上一跳超时触发器")
		}
		if node.Subject == store.TriggerSubjectNext && seq == len(hops)-1 {
			return prefix("出口跳没有下一跳超时触发器")
		}
		if node.Subject == store.TriggerSubjectSelf {
			ownerSrv, err := st.ServerByID(ctx, graph.Nodes[owner].ServerID)
			if err != nil {
				return prefix("触发器所属服务器不存在")
			}
			if ownerSrv.IsExternal() || ownerSrv.NonDirect {
				return prefix("外部直链/非直连服务器没有三网封禁观察")
			}
		}
	}

	// 服务器：非根必须有来源；角色/地址/端口/跨跳复用。
	otherPrimaries := make(map[int64]bool, len(hops))
	for i, hop := range hops {
		if i != seq {
			otherPrimaries[hop.ServerID] = true
		}
	}
	otherGraphServers := make(map[int64]bool)
	if allGraphs, err := st.ChainHopGraphs(ctx, chain.ID); err == nil {
		for graphSeq, other := range allGraphs {
			if graphSeq == seq {
				continue
			}
			for _, node := range other.Nodes {
				if node.Kind == store.HopGraphKindServer {
					otherGraphServers[node.ServerID] = true
				}
			}
		}
	} else {
		return err
	}
	currentHopServer, err := st.ServerByID(ctx, hops[seq].ServerID)
	if err != nil {
		return prefix("当前跳服务器不存在")
	}
	for _, i := range servers {
		node := graph.Nodes[i]
		if i != root && len(in[i]) == 0 {
			return prefix("服务器 #%d 没有任何来源触发器", node.ServerID)
		}
		if otherPrimaries[node.ServerID] || otherGraphServers[node.ServerID] {
			return prefix("服务器 #%d 已被同链其他跳使用", node.ServerID)
		}
		srv, err := st.ServerByID(ctx, node.ServerID)
		if err != nil {
			return prefix("服务器 #%d 不存在", node.ServerID)
		}
		isExit := hops[seq].Role == store.HopRoleExit
		if isExit {
			if srv.IsExternal() != currentHopServer.IsExternal() {
				return prefix("出口备选须与主出口同为托管或同为外部直链")
			}
			if srv.IsExternal() {
				exit := srv.ParseExternalExit()
				if exit == nil || exit.Validate() != nil {
					return prefix("外部直链服务器 %s 的出口配置不可用", srv.Alias)
				}
			}
		} else {
			if srv.IsExternal() {
				return prefix("外部直链服务器不能作为入口/中转备选")
			}
			if !inboundCapable(srv) {
				return prefix("服务器 %s 无入站能力", srv.Alias)
			}
		}
		if node.Address != "" && !store.ServerAddressSet(srv)[node.Address] {
			return prefix("服务器 %s 不存在公网地址 %s", srv.Alias, node.Address)
		}
		if node.ListenPort < 0 || node.ListenPort > 65535 {
			return prefix("端口须为 1-65535")
		}
		if node.ListenPort > 0 {
			if err := checkPortInRanges(srv, node.ListenPort); err != nil {
				return prefix("服务器 %s：%v", srv.Alias, err)
			}
		}
	}

	// 全图从根/恢复触发器可达（恢复触发器是合法的无来源节点）。
	visited := make([]bool, len(graph.Nodes))
	queue := []int{root}
	visited[root] = true
	for _, i := range triggers {
		if graph.Nodes[i].Subject == store.TriggerSubjectRoot {
			visited[i] = true
			queue = append(queue, i)
		}
	}
	for len(queue) > 0 {
		i := queue[0]
		queue = queue[1:]
		for _, to := range out[i] {
			if !visited[to] {
				visited[to] = true
				queue = append(queue, to)
			}
		}
	}
	for i := range graph.Nodes {
		if !visited[i] {
			return prefix("存在从根不可达的节点")
		}
	}

	// 去掉所有 →根 的边后必须无环（只允许经触发器回到根）。
	color := make([]int, len(graph.Nodes))
	var walk func(int) bool
	walk = func(i int) bool {
		switch color[i] {
		case 1:
			return false
		case 2:
			return true
		}
		color[i] = 1
		for _, to := range out[i] {
			if to == root {
				continue
			}
			if !walk(to) {
				return false
			}
		}
		color[i] = 2
		return true
	}
	for i := range graph.Nodes {
		if !walk(i) {
			return prefix("不允许出现非根环（只允许经触发器回到根）")
		}
	}
	return nil
}

// chainHopOrchestrationDTO 是跳位编排图的 API 表示。
type chainHopOrchestrationNodeDTO struct {
	ID            int64  `json:"id"`
	Kind          string `json:"kind"`
	ServerID      int64  `json:"server_id,omitempty"`
	Address       string `json:"address,omitempty"`
	ListenPort    int    `json:"listen_port,omitempty"`
	Subject       string `json:"subject,omitempty"`
	Action        string `json:"action,omitempty"`
	Priority      int    `json:"priority"`
	IsRoot        bool   `json:"is_root,omitempty"`
	Active        bool   `json:"active,omitempty"`
	OwnerServerID int64  `json:"owner_server_id,omitempty"`
	Alias         string `json:"alias,omitempty"`
	CountryCode   string `json:"country_code,omitempty"`
	Location      string `json:"location,omitempty"`
	KindOfServer  string `json:"kind_of_server,omitempty"`
	Online        bool   `json:"online"`
	Banned        bool   `json:"banned"`
}

type chainHopOrchestrationEdgeDTO struct {
	From int64 `json:"from"`
	To   int64 `json:"to"`
}

type chainHopOrchestrationDTO struct {
	RootServerID   int64                          `json:"root_server_id"`
	ActiveServerID int64                          `json:"active_server_id"`
	Triggered      bool                           `json:"triggered"`
	Nodes          []chainHopOrchestrationNodeDTO `json:"nodes"`
	Edges          []chainHopOrchestrationEdgeDTO `json:"edges"`
}

func (s *Server) buildHopOrchestrationDTO(ctx context.Context, hop store.ChainHop, graph *store.ChainHopGraph, bans map[int64]store.ServerBanWatch) *chainHopOrchestrationDTO {
	if graph == nil || len(graph.Nodes) == 0 {
		return nil
	}
	byID := make(map[int64]int, len(graph.Nodes))
	for i, node := range graph.Nodes {
		byID[node.ID] = i
	}
	owners := make(map[int64]int64)
	for _, edge := range graph.Edges {
		from, okFrom := byID[edge.FromNode]
		to, okTo := byID[edge.ToNode]
		if okFrom && okTo && graph.Nodes[from].Kind == store.HopGraphKindServer && graph.Nodes[to].Kind == store.HopGraphKindTrigger {
			owners[edge.ToNode] = graph.Nodes[from].ServerID
		}
	}
	var rootID int64
	for _, node := range graph.Nodes {
		if node.IsRoot {
			rootID = node.ServerID
		}
	}
	out := &chainHopOrchestrationDTO{
		RootServerID: rootID, ActiveServerID: hop.ServerID, Triggered: hop.ServerID != rootID,
		Nodes: make([]chainHopOrchestrationNodeDTO, 0, len(graph.Nodes)),
		Edges: make([]chainHopOrchestrationEdgeDTO, 0, len(graph.Edges)),
	}
	for _, node := range graph.Nodes {
		dto := chainHopOrchestrationNodeDTO{
			ID: node.ID, Kind: node.Kind, ServerID: node.ServerID,
			Address: node.Address, ListenPort: node.ListenPort,
			Subject: node.Subject, Action: node.Action, Priority: node.Priority,
			IsRoot: node.IsRoot, OwnerServerID: owners[node.ID],
		}
		if node.Kind == store.HopGraphKindServer {
			dto.Active = node.ServerID == hop.ServerID
			if srv, err := s.st.ServerByID(ctx, node.ServerID); err == nil {
				dto.Alias = srv.Alias
				dto.CountryCode = srv.CountryCode
				dto.Location = srv.Location
				dto.KindOfServer = srv.Kind
				dto.Online = s.req.IsOnline(node.ServerID)
			}
			if bw, ok := bans[node.ServerID]; ok && bw.State == store.BanStateBanned {
				dto.Banned = true
			}
		}
		out.Nodes = append(out.Nodes, dto)
	}
	for _, edge := range graph.Edges {
		out.Edges = append(out.Edges, chainHopOrchestrationEdgeDTO{From: edge.FromNode, To: edge.ToNode})
	}
	return out
}

// handleSaveHopOrchestration 保存一个跳位的编排图（整图替换；空图 = 删除编排）。
func (s *Server) handleSaveHopOrchestration(w http.ResponseWriter, r *http.Request) {
	var req hopOrchestrationSaveRequest
	if err := readJSON(r, &req); err != nil || req.ChainID <= 0 || req.Seq < 0 {
		writeProtocolError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	chain, err := s.st.ChainByID(r.Context(), req.ChainID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "链不存在")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if chain.DesiredRevisionID != 0 || chain.Status == store.ChainStatusApplying {
		writeError(w, http.StatusBadRequest, "链路有在途修订，暂不能修改自动编排")
		return
	}
	hops, err := s.st.ChainHops(r.Context(), req.ChainID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if req.Seq >= len(hops) {
		writeError(w, http.StatusBadRequest, "跳位不存在")
		return
	}
	graph, err := buildGraphFromRequest(req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := validateHopOrchestration(r.Context(), s.st, chain, hops, req.Seq, graph); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if _, err := s.st.ReplaceChainHopGraph(r.Context(), req.ChainID, req.Seq, graph.Nodes, graph.Edges); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// 当前运行节点的地址/端口写回工作跳（详情/向导/后续切换共用同一份值）。
	for _, node := range graph.Nodes {
		if node.Kind != store.HopGraphKindServer || node.ServerID != hops[req.Seq].ServerID {
			continue
		}
		if err := s.st.SetChainHopAddress(r.Context(), hops[req.Seq].ID, node.Address); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if err := s.st.SetChainHopForwardPort(r.Context(), hops[req.Seq].ID, node.ListenPort); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		break
	}
	_ = s.st.SetChainOrchestrationError(r.Context(), req.ChainID, "")
	if s.orchestrator != nil {
		if len(graph.Nodes) == 0 {
			s.orchestrator.StopChain(req.ChainID)
		} else {
			s.orchestrator.EnsureChainWorker(req.ChainID)
		}
		s.orchestrator.ResumeChain(req.ChainID)
		s.orchestrator.NotifyChainUpdated(req.ChainID)
	}
	s.audit(r, "chain.hop_orchestration_save", nil, nil, map[string]any{
		"chain_id": req.ChainID, "seq": req.Seq, "nodes": len(graph.Nodes), "edges": len(graph.Edges),
	})
	updated, err := s.st.ChainByID(r.Context(), req.ChainID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	dto, err := s.toChainDTO(r, *updated)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, dto)
}

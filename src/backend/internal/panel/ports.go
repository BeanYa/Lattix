package panel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"lattix/backend/internal/store"
	"lattix/shared"
)

// 端口冲突来源的中文描述（报错文案用）。
var portOccupantSourceNames = map[string]string{
	"node":          "节点",
	"endpoint":      "共享监听",
	"chain_forward": "链路中转管道",
	"chain_portal":  "链路隧道",
}

// findPortConflict 端口冲突前置判定（纯函数）：传输层重叠且区间相交即冲突。
// portEnd=0 表示单端口 [port,port]；>0 表示连续保留段 [port,portEnd]（hy2 跳跃段）。
// excludeChainID 用于编辑链路时排除自身既有占用。vless 链入口的共享端点合并语义
// 由调用点门控（chains.go 入口校验 protocol != vless 才进本函数），此处不做协议豁免。
func findPortConflict(occupants []store.PortOccupant, protocol string, port, portEnd int, excludeChainID int64) error {
	layers := shared.PortLayers(protocol)
	reqEnd := portEnd
	if reqEnd == 0 {
		reqEnd = port
	}
	for _, o := range occupants {
		occEnd := o.PortEnd
		if occEnd == 0 {
			occEnd = o.Port
		}
		if !shared.LayersOverlap(layers, o.Layers) || port > occEnd || o.Port > reqEnd {
			continue
		}
		if excludeChainID != 0 && o.ChainID == excludeChainID {
			continue
		}
		source := portOccupantSourceNames[o.Source]
		if source == "" {
			source = "监听"
		}
		if o.PortEnd > 0 {
			return fmt.Errorf("端口段 %d-%d 与%s「%s」的保留段 %d-%d 重叠（%s 层），请更换或留空自动分配",
				port, reqEnd, source, o.RefName, o.Port, o.PortEnd, o.Layers)
		}
		return fmt.Errorf("端口 %d 已被%s「%s」占用（%s 层），请更换端口或留空自动分配",
			port, source, o.RefName, o.Layers)
	}
	return nil
}

// checkPortConflict 查询服务器端口占用并判定冲突（端口 0 = 自动分配，无需校验）。
// portEnd=0 单端口；>0 校验段 [port,portEnd]（hy2 跳跃段）。
func (s *Server) checkPortConflict(ctx context.Context, serverID int64, protocol string, port, portEnd int, excludeChainID int64) error {
	if port <= 0 {
		return nil
	}
	occupants, err := s.st.PortOccupants(ctx, serverID)
	if err != nil {
		return err
	}
	return findPortConflict(occupants, protocol, port, portEnd, excludeChainID)
}

func orchestrationListener(srv *store.Server, protocol, portHop string, port int, sharedEntry bool) (store.PortOccupant, error) {
	claim := store.PortOccupant{Port: port, Protocol: protocol, Source: "chain_forward"}
	if sharedEntry {
		claim.Protocol, claim.Source, portHop = shared.ProtocolVLESS, "endpoint", ""
	}
	claim.Layers = shared.PortLayers(claim.Protocol)
	if port <= 0 {
		return claim, nil
	}
	if srv.IsExternal() {
		return claim, fmt.Errorf("外部直链出口的监听端口由外部协议配置决定")
	}
	claim.PortEnd = shared.ForwardPortHopEnd(port, portHop)
	if claim.PortEnd == 0 {
		return claim, checkPortInRanges(srv, port)
	}
	ranges, err := shared.ParsePortRanges(srv.AllowedPorts)
	if err != nil {
		return claim, err
	}
	if claim.PortEnd > 65535 || (len(ranges) > 0 && !shared.SpanInListenRanges(ranges, port, claim.PortEnd)) {
		return claim, fmt.Errorf("端口跳跃段 %d-%d 不在服务器 %s 可用段内", port, claim.PortEnd, srv.Alias)
	}
	return claim, nil
}

func (s *Server) checkOrchestrationListener(ctx context.Context, serverID, chainID int64, claim store.PortOccupant, planned []store.PortOccupant) error {
	if claim.Port <= 0 {
		return nil
	}
	occupants, err := s.st.PortOccupants(ctx, serverID)
	if err != nil {
		return err
	}
	occupied := append(occupants, planned...)
	if claim.Source == "endpoint" {
		checked := occupied[:0]
		for _, p := range occupied {
			if p.Source == "endpoint" && p.Port == claim.Port && p.PortEnd == 0 {
				if p.Protocol != claim.Protocol {
					return fmt.Errorf("端口 %d 已有不兼容的共享监听（%s）", p.Port, p.Protocol)
				}
				continue
			}
			checked = append(checked, p)
		}
		occupied = checked
	}
	return findPortConflict(occupied, claim.Protocol, claim.Port, claim.PortEnd, chainID)
}

func (s *Server) checkChainForwardListeners(ctx context.Context, servers []*store.Server, node createNodeRequest, entryPort *int, entryShared, entryProtocol bool, active map[int]store.ChainHopGraphNode, writes []store.HopGraphWrite, chainID int64) error {
	graphs := make(map[int]store.ChainHopGraph, len(writes))
	for _, w := range writes {
		graphs[w.Graph.Seq] = w.Graph
	}
	for seq, srv := range servers[:len(servers)-1] {
		n, hasGraph := active[seq]
		sharedListener := seq == 0 && entryShared
		portHop := node.PortHop
		if sharedListener || node.Protocol != shared.ProtocolHysteria2 || (entryProtocol && seq == len(servers)-2 && inboundCapable(servers[len(servers)-1])) {
			portHop = ""
		}
		if err := s.checkCandidateForwardSpans(ctx, graphs[seq], srv.ID, portHop); err != nil {
			return fmt.Errorf("第 %d 跳：%w", seq+1, err)
		}
		if !hasGraph && (sharedListener || entryProtocol || portHop == "") {
			continue // Legacy fixed entry and exit ports are checked separately.
		}
		port := n.ListenPort
		if !hasGraph && seq == 0 && entryPort != nil {
			port = *entryPort
		}
		if port == 0 && !entryProtocol && !sharedListener && portHop != "" {
			port, _, _ = shared.ParsePortHop(portHop)
		}
		claim, err := orchestrationListener(srv, node.Protocol, portHop, port, sharedListener)
		if err == nil {
			err = s.checkOrchestrationListener(ctx, srv.ID, chainID, claim, nil)
		}
		if err != nil {
			return fmt.Errorf("第 %d 跳：%w", seq+1, err)
		}
	}
	if len(servers) == 1 && entryShared && active[0].ListenPort > 0 {
		claim, err := orchestrationListener(servers[0], node.Protocol, "", active[0].ListenPort, true)
		if err != nil {
			return err
		}
		return s.checkOrchestrationListener(ctx, servers[0].ID, chainID, claim, nil)
	}
	return nil
}

func (s *Server) checkCandidateForwardSpans(ctx context.Context, graph store.ChainHopGraph, activeServerID int64, portHop string) error {
	if portHop == "" {
		return nil
	}
	for _, n := range graph.Nodes {
		if n.Kind != store.HopGraphKindServer || n.ServerID == activeServerID || n.ListenPort <= 0 {
			continue
		}
		srv, err := s.st.ServerByID(ctx, n.ServerID)
		if err != nil {
			return err
		}
		if _, err := orchestrationListener(srv, shared.ProtocolHysteria2, portHop, n.ListenPort, false); err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) prepareGraphHy2Endpoint(ctx context.Context, node *createNodeRequest, serverID int64, active map[int]store.ChainHopGraphNode, exitSeq int) (*store.SharedEndpoint, error) {
	if len(active) == 0 {
		return nil, nil
	}
	ep, err := s.st.ProtocolSharedEndpoint(ctx, serverID, shared.ProtocolHysteria2)
	if errors.Is(err, store.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if err := matchGraphSharedExitPort(ep, active[exitSeq].ListenPort); err != nil {
		return nil, err
	}
	var config shared.VirtualConfig
	if err := json.Unmarshal(ep.ConfigTemplate, &config); err != nil {
		return nil, err
	}
	node.PortHop = config.PortHop
	if node.PortHop == "" {
		node.PortHop = "off"
	}
	return ep, nil
}

func matchGraphSharedExitPort(endpoint *store.SharedEndpoint, port int) error {
	if port > 0 && endpoint.Port != port {
		return fmt.Errorf("共享出口监听使用端口 %d，无法单独改为 %d", endpoint.Port, port)
	}
	return nil
}

// allocUDPPortHop 在候选空间内分配连续 length 个空闲 udp 端口（hy2 跳跃段自动分配）：
// NAT 机候选 = 各段监听侧并集（段整体须落在同一 NAT 段内，span 语义）；
// direct 机候选 = [40000,61000]。避开全部 udp 层占用（单端口与保留段）。
func allocUDPPortHop(occupants []store.PortOccupant, rs []shared.PortRange, length int) (start, end int, ok bool) {
	type window struct{ lo, hi int }
	var windows []window
	if len(rs) > 0 {
		for _, r := range rs {
			s, e := r.PubStart, r.PubEnd
			if r.ListenStart != 0 {
				s, e = r.ListenStart, r.ListenEnd
			}
			windows = append(windows, window{s, e})
		}
	} else {
		windows = []window{{40000, 61000}}
	}
	conflicts := func(p int) bool {
		for _, o := range occupants {
			if !shared.LayersOverlap("udp", o.Layers) {
				continue
			}
			oe := o.PortEnd
			if oe == 0 {
				oe = o.Port
			}
			if p >= o.Port && p <= oe {
				return true
			}
		}
		return false
	}
	for _, w := range windows {
		for s := w.lo; s+length-1 <= w.hi; s++ {
			free := true
			for p := s; p < s+length; p++ {
				if conflicts(p) {
					s = p // 跳到占用点之后继续
					free = false
					break
				}
			}
			if free && s+length-1 <= w.hi {
				return s, s + length - 1, true
			}
		}
	}
	return 0, 0, false
}

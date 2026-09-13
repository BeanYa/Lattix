package panel

import (
	"context"
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

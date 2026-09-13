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

// findPortConflict 端口冲突前置判定（纯函数）：同端口 + 传输层重叠即冲突。
// excludeChainID 用于编辑链路时排除自身既有占用。vless 链入口的共享端点合并语义
// 由调用点门控（chains.go 入口校验 protocol != vless 才进本函数），此处不做协议豁免。
func findPortConflict(occupants []store.PortOccupant, protocol string, port int, excludeChainID int64) error {
	layers := shared.PortLayers(protocol)
	for _, o := range occupants {
		if o.Port != port || !shared.LayersOverlap(layers, o.Layers) {
			continue
		}
		if excludeChainID != 0 && o.ChainID == excludeChainID {
			continue
		}
		source := portOccupantSourceNames[o.Source]
		if source == "" {
			source = "监听"
		}
		return fmt.Errorf("端口 %d 已被%s「%s」占用（%s 层），请更换端口或留空自动分配",
			port, source, o.RefName, o.Layers)
	}
	return nil
}

// checkPortConflict 查询服务器端口占用并判定冲突（端口 0 = 自动分配，无需校验）。
func (s *Server) checkPortConflict(ctx context.Context, serverID int64, protocol string, port int, excludeChainID int64) error {
	if port <= 0 {
		return nil
	}
	occupants, err := s.st.PortOccupants(ctx, serverID)
	if err != nil {
		return err
	}
	return findPortConflict(occupants, protocol, port, excludeChainID)
}

package store

import (
	"context"
	"fmt"

	"lattix/shared"
)

// PortOccupant 是一台服务器上一个已被占用端口的画像（端口冲突前置治理的数据源）。
type PortOccupant struct {
	Port     int
	Layers   string // "tcp" / "udp" / "tcp,udp"
	Source   string // "node" | "endpoint" | "chain_forward" | "chain_portal"
	Protocol string
	ChainID  int64 // 0 = 无链归属（独立节点）
	RefName  string
}

// PortOccupants 汇总服务器上全部受管端口占用：业务节点、共享端点、链路逐跳
// forward/portal 监听。仅返回 port>0 的确定占用（port=0 待 agent 分配的不构成冲突）。
func (s *Store) PortOccupants(ctx context.Context, serverID int64) ([]PortOccupant, error) {
	var out []PortOccupant

	appendRows := func(query, source string, fixedLayers string, args ...any) error {
		rows, err := s.db.QueryContext(ctx, query, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var o PortOccupant
			o.Source = source
			if err := rows.Scan(&o.Port, &o.Protocol, &o.ChainID, &o.RefName); err != nil {
				return err
			}
			if fixedLayers != "" {
				o.Layers = fixedLayers
			} else {
				o.Layers = shared.PortLayers(o.Protocol)
			}
			out = append(out, o)
		}
		return rows.Err()
	}

	// 业务节点（协议决定层）；归属链 = 以该节点为出口业务节点的未删链。
	if err := appendRows(`SELECT n.port, n.protocol, COALESCE(c.id,0), n.name
		FROM nodes n LEFT JOIN chains c ON c.service_node_id=n.id AND c.deleted_at IS NULL
		WHERE n.server_id=? AND n.port IS NOT NULL AND n.port>0`, "node", "", serverID); err != nil {
		return nil, fmt.Errorf("query node occupants: %w", err)
	}
	// 共享端点（pending/applying/active 均视为将占用；port=0 未分配的不算）。
	if err := appendRows(`SELECT e.port, e.protocol,
		COALESCE((SELECT c2.id FROM chains c2 WHERE c2.endpoint_id=e.id AND c2.deleted_at IS NULL
			ORDER BY c2.id LIMIT 1),0), 'shared-endpoint #' || e.id
		FROM shared_endpoints e
		WHERE e.server_id=? AND e.port>0 AND e.status IN ('pending','applying','active')`,
		"endpoint", "", serverID); err != nil {
		return nil, fmt.Errorf("query endpoint occupants: %w", err)
	}
	// 链路逐跳 forward：dokodemo 管道按出口业务协议分层（ss 出口 tcp,udp，其余 tcp，§3.2）；
	// 服务节点缺失（异常数据）回退 vless=tcp 保持保守。portal 为 vless+reality，恒 tcp。
	if err := appendRows(`SELECT h.forward_port, COALESCE(n.protocol, 'vless'), c.id, c.name
		FROM chain_hops h JOIN chains c ON c.id=h.chain_id AND c.deleted_at IS NULL
		LEFT JOIN nodes n ON n.id=c.service_node_id
		WHERE h.server_id=? AND h.forward_port>0`, "chain_forward", "", serverID); err != nil {
		return nil, fmt.Errorf("query forward occupants: %w", err)
	}
	if err := appendRows(`SELECT h.portal_port, 'vless', c.id, c.name
		FROM chain_hops h JOIN chains c ON c.id=h.chain_id AND c.deleted_at IS NULL
		WHERE h.server_id=? AND h.portal_port>0`, "chain_portal", "tcp", serverID); err != nil {
		return nil, fmt.Errorf("query portal occupants: %w", err)
	}
	return out, nil
}

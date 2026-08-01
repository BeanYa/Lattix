package store

import (
	"context"
	"fmt"

	"lattix/shared"
)

// ExpectedXrayState 返回一台服务器当前应存在的 xray 受管配置件（xray.cleanup 期望集合，
// §docs/xray-cleanup-design.md §3）：期望 inbound tag 集合与期望 piece key 集合。
// 数据源：nodes（直连/链出口服务节点）、chain_hops JOIN 未删除链（全部入口/内部/出口跳）、
// shared_endpoints（共享端点）。
func (s *Store) ExpectedXrayState(ctx context.Context, serverID int64) ([]string, []string, error) {
	tags := []string{}
	pieces := []string{}

	// 3.1 直连节点：全部状态（pending/applying/active/failed）均计入——面板仍管理。
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM nodes WHERE server_id = ?`, serverID)
	if err != nil {
		return nil, nil, fmt.Errorf("expected xray nodes: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, nil, fmt.Errorf("scan expected xray node: %w", err)
		}
		tags = append(tags, shared.NodeTag(id))
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("expected xray nodes: %w", err)
	}

	// 3.2 中转链跳：该服务器关联的全部未删除链的全部跳（入口/内部/出口）。
	// bridge 归属取决于同链上一跳的 tunnel_uuid（可能在其他服务器），故取整链再按服务器过滤。
	hopRows, err := s.db.QueryContext(ctx, `SELECT h.id, h.chain_id, h.seq, h.server_id, h.role,
		h.node_id, h.status, h.error, h.forward_port, h.portal_port, h.portal_public_key,
		h.portal_server_name, h.tunnel_uuid, h.created_at
		FROM chain_hops h JOIN chains c ON c.id = h.chain_id
		WHERE c.deleted_at IS NULL
		AND h.chain_id IN (SELECT chain_id FROM chain_hops WHERE server_id = ?)
		ORDER BY h.chain_id, h.seq`, serverID)
	if err != nil {
		return nil, nil, fmt.Errorf("expected xray chain hops: %w", err)
	}
	defer hopRows.Close()
	hops, err := scanChainHops(hopRows)
	if err != nil {
		return nil, nil, fmt.Errorf("expected xray chain hops: %w", err)
	}
	for i := 0; i < len(hops); {
		j := i + 1
		for j < len(hops) && hops[j].ChainID == hops[i].ChainID {
			j++
		}
		chain := hops[i:j]
		for k, h := range chain {
			if h.ServerID != serverID {
				continue
			}
			for _, kind := range ChainHopPieces(chain, k) {
				pieces = append(pieces, fmt.Sprintf("%s/%d", kind, h.ID))
				switch kind {
				case shared.HopKindForward:
					tags = append(tags, shared.ChainForwardTag(h.ID))
				case shared.HopKindPortal:
					tags = append(tags, shared.ChainPortalTag(h.ID))
				}
			}
		}
		i = j
	}

	// 3.3 共享端点。
	epRows, err := s.db.QueryContext(ctx, `SELECT id FROM shared_endpoints WHERE server_id = ?`, serverID)
	if err != nil {
		return nil, nil, fmt.Errorf("expected xray shared endpoints: %w", err)
	}
	defer epRows.Close()
	for epRows.Next() {
		var id int64
		if err := epRows.Scan(&id); err != nil {
			return nil, nil, fmt.Errorf("scan expected xray shared endpoint: %w", err)
		}
		tags = append(tags, shared.SharedEndpointTag(id))
		pieces = append(pieces, fmt.Sprintf("shared-endpoint/%d", id))
	}
	if err := epRows.Err(); err != nil {
		return nil, nil, fmt.Errorf("expected xray shared endpoints: %w", err)
	}
	return tags, pieces, nil
}

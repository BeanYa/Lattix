package store

import (
	"context"
	"encoding/json"
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
		h.node_id, h.status, h.error, h.forward_port, h.address, h.portal_port, h.portal_public_key,
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
		// hy2 段画像（P4，镜像 dispatch 阶段 4 / materializeRevision 的同源判定）：
		// 快照 ServiceConfig 的 port_hop 决定逐跳保留段长；跳 transport 取自 revision 快照。
		span := 0
		transports := map[int64]string{}
		skipEntryForward := false
		snapshot := s.chainSnapshotForExpected(ctx, chain[0].ChainID)
		if snapshot != nil {
			var svc struct {
				PortHop string `json:"port_hop"`
			}
			if err := json.Unmarshal(snapshot.ServiceConfig, &svc); err == nil && svc.PortHop != "" {
				if start, end, err := shared.ParsePortHop(svc.PortHop); err == nil {
					span = end - start + 1
				}
			}
			for _, h := range snapshot.Hops {
				transports[h.HopID] = h.Transport
			}
			skipEntryForward = len(snapshot.Hops) == 2 && snapshot.Hops[0].Transport == "hy2"
		}
		for k, h := range chain {
			if h.ServerID != serverID {
				continue
			}
			for _, kind := range ChainHopPieces(chain, k) {
				if kind == shared.HopKindForward && k == 0 && skipEntryForward {
					continue // 入口终结 2 跳 hy2：hop0 免管道（无 piece、无 inbound）
				}
				pieces = append(pieces, fmt.Sprintf("%s/%d", kind, h.ID))
				switch kind {
				case shared.HopKindForward:
					tags = append(tags, shared.ChainForwardTag(h.ID))
					// hy2 端到端跳跃段：段内其余端口的附加 inbound（<tag>_hop_<port>，
					// 段长随出口 port_hop，hy2 末段跳自身不保留段）。
					if span > 0 && transports[h.ID] != "hy2" && h.ForwardPort > 0 {
						for p := h.ForwardPort + 1; p <= h.ForwardPort+span-1; p++ {
							tags = append(tags, fmt.Sprintf("%s_hop_%d", shared.ChainForwardTag(h.ID), p))
						}
					}
				case shared.HopKindPortal:
					tags = append(tags, shared.ChainPortalTag(h.ID))
				}
			}
		}
		i = j
	}

	// 3.3 共享端点：仅计链引用的端点（chains.endpoint_id / service_endpoint_id 关联、
	// 链未删除——后者为 hy2 出口共享监听，P4）。
	// 链删除时端点记录不删（流量/审计引用），孤儿端点（无存活链引用）不在面板有效
	// 管理范围内，xray.cleanup 应将其从 config.json 中清理。
	epRows, err := s.db.QueryContext(ctx, `SELECT id FROM shared_endpoints
		WHERE server_id = ? AND (id IN (SELECT endpoint_id FROM chains
			WHERE deleted_at IS NULL AND endpoint_id <> 0)
		OR id IN (SELECT service_endpoint_id FROM chains
			WHERE deleted_at IS NULL AND service_endpoint_id <> 0))`, serverID)
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

// chainSnapshotForExpected 取链当前生效的 revision 快照（期望状态归因用，镜像 dispatch
// 的取值顺序：desired 优先，无 desired 回退 published）；取不到返回 nil（按无 hy2 段处理）。
func (s *Store) chainSnapshotForExpected(ctx context.Context, chainID int64) *ChainRevisionSnapshot {
	if revision, err := s.DesiredChainRevision(ctx, chainID); err == nil {
		return &revision.Snapshot
	}
	if revision, err := s.PublishedChainRevision(ctx, chainID); err == nil {
		return &revision.Snapshot
	}
	return nil
}

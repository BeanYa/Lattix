package store

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"lattix/shared"
)

// ServerLatencySample 是一轮回报里的单条服务器间延迟结果（agent 并行测速上报）。
type ServerLatencySample struct {
	ToServer  int64
	Kind      string   // tcp|icmp
	Port      int      // tcp 为复用的链路监听端口；icmp 恒 0
	LatencyMS *float64 // nil = 失败/超时样本（连通性参考）
}

// ServerLatencyRow 是 server_latency_probes 最新值行。
type ServerLatencyRow struct {
	FromServer int64
	ToServer   int64
	Kind       string
	Port       int
	LatencyMS  *float64
	MeasuredAt time.Time
}

// SaveServerLatencyProbes 覆盖写有向对最新值并插入历史（latency.report 驱动）。
// 仅保存已知服务器对（外键校验失败的样本跳过，不阻断整轮）。
func (s *Store) SaveServerLatencyProbes(ctx context.Context, fromServer int64, samples []ServerLatencySample, measuredAt time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin server latency: %w", err)
	}
	defer tx.Rollback()
	for _, sample := range samples {
		if sample.ToServer <= 0 || (sample.Kind != "tcp" && sample.Kind != "icmp") {
			continue
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO server_latency_probes
			(from_server, to_server, kind, port, latency_ms, measured_at)
			VALUES (?, ?, ?, ?, ?, ?)
			ON CONFLICT(from_server, to_server, kind, port) DO UPDATE SET
			latency_ms=excluded.latency_ms, measured_at=excluded.measured_at`,
			fromServer, sample.ToServer, sample.Kind, sample.Port, sample.LatencyMS, measuredAt.UTC()); err != nil {
			return fmt.Errorf("upsert server latency: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO server_latency_history
			(from_server, to_server, kind, port, latency_ms, measured_at) VALUES (?, ?, ?, ?, ?, ?)`,
			fromServer, sample.ToServer, sample.Kind, sample.Port, sample.LatencyMS, measuredAt.UTC()); err != nil {
			return fmt.Errorf("insert server latency history: %w", err)
		}
	}
	return tx.Commit()
}

// FreshServerLatencyProbes 返回新鲜度窗口内的最新值行（窗口外视为失效：
// Agent 离线或拓扑变更后未再刷新的服务器对不再计入链路延迟）。
func (s *Store) FreshServerLatencyProbes(ctx context.Context, maxAge time.Duration) ([]ServerLatencyRow, error) {
	cutoff := time.Now().UTC().Add(-maxAge)
	rows, err := s.db.QueryContext(ctx, `SELECT from_server, to_server, kind, port, latency_ms, measured_at
		FROM server_latency_probes WHERE measured_at >= ? ORDER BY from_server, to_server, kind, port`, cutoff)
	if err != nil {
		return nil, fmt.Errorf("fresh server latency: %w", err)
	}
	defer rows.Close()
	out := make([]ServerLatencyRow, 0)
	for rows.Next() {
		var row ServerLatencyRow
		if err := rows.Scan(&row.FromServer, &row.ToServer, &row.Kind, &row.Port, &row.LatencyMS, &row.MeasuredAt); err != nil {
			return nil, fmt.Errorf("scan server latency: %w", err)
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// DeleteExpiredServerLatencyHistory 清理保留窗口外的历史（后台小时任务）。
func (s *Store) DeleteExpiredServerLatencyHistory(ctx context.Context, retention time.Duration) (int64, error) {
	cutoff := time.Now().UTC().Add(-retention)
	result, err := s.db.ExecContext(ctx,
		`DELETE FROM server_latency_history WHERE measured_at < ?`, cutoff)
	if err != nil {
		return 0, fmt.Errorf("delete expired server latency history: %w", err)
	}
	return result.RowsAffected()
}

// ServerLatencyTarget 是面板下发给某 Agent 的一个探测目标（本服务器 → 目标服务器）。
type ServerLatencyTarget struct {
	ToServer int64
	Address  string // 目标服务器的设置公网地址（域名或 IP，§9 地址引用回退）
	Kind     string // tcp|icmp
	Port     int    // tcp 为复用的链路监听端口；icmp 恒 0
}

// ServerLatencyEdge 是已发布拓扑里的一段物理测速边（§链路统计）：
// UpstreamHopID 为链路卡中该段所属的上游跳（连线归属），From→To 为物理发起方向
// （direct 段上游→下游；reverse/encrypted 段下游 bridge→上游 portal，方向反转）。
type ServerLatencyEdge struct {
	ChainID       int64
	UpstreamHopID int64
	FromServer    int64
	ToServer      int64
	TargetAddress string
	Kind          string // tcp|icmp
	Port          int
}

// PublishedLatencyEdges 展开全部已发布链路的测速边（§链路统计）：
// direct 段由上游拨下跳监听端口（tcp），reverse/encrypted 段由下游 bridge 拨上游
// portal 监听端口（tcp，方向反转），hy2 段（UDP/QUIC）仅 icmp；每段同时携带
// icmp 边作为 TCP 不可用时的回退来源。目标地址为目标跳的设置公网地址。
func (s *Store) PublishedLatencyEdges(ctx context.Context) ([]ServerLatencyEdge, error) {
	servers, err := s.ListServers(ctx)
	if err != nil {
		return nil, fmt.Errorf("list servers for latency edges: %w", err)
	}
	byID := make(map[int64]*Server, len(servers))
	for i := range servers {
		byID[servers[i].ID] = &servers[i]
	}
	rows, err := s.db.QueryContext(ctx, `SELECT c.id, r.snapshot FROM chains c
		JOIN chain_revisions r ON r.id=c.published_revision_id
		WHERE c.deleted_at IS NULL AND c.published_revision_id != 0 ORDER BY c.id`)
	if err != nil {
		return nil, fmt.Errorf("query published chains: %w", err)
	}
	defer rows.Close()
	edges := make([]ServerLatencyEdge, 0)
	for rows.Next() {
		var chainID int64
		var raw string
		if err := rows.Scan(&chainID, &raw); err != nil {
			return nil, fmt.Errorf("scan chain snapshot: %w", err)
		}
		var snapshot ChainRevisionSnapshot
		if err := json.Unmarshal([]byte(raw), &snapshot); err != nil || len(snapshot.Hops) < 2 {
			continue
		}
		// 出口业务 realized 端口（末段 tcp 探测复用出口监听端口，NAT 受限机换算公网侧）。
		var realized shared.RealizedConfig
		_ = json.Unmarshal(snapshot.ServiceRealized, &realized)
		for i := 0; i+1 < len(snapshot.Hops); i++ {
			up, down := snapshot.Hops[i], snapshot.Hops[i+1]
			if up.ServerID == 0 || down.ServerID == 0 || up.ServerID == down.ServerID {
				continue
			}
			transport := up.Transport
			if transport == "" {
				transport = "direct"
			}
			last := i == len(snapshot.Hops)-2
			add := func(from, to int64, kind string, port int, addrRef string) {
				if port < 0 {
					port = 0
				}
				toSrv := byID[to]
				if toSrv == nil {
					return
				}
				// hop 地址引用（§9）：目标跳所选公网地址可用则用之，否则跟随服务器默认地址。
				edges = append(edges, ServerLatencyEdge{ChainID: chainID, UpstreamHopID: up.HopID,
					FromServer: from, ToServer: to,
					TargetAddress: ResolveServerAddress(toSrv, addrRef), Kind: kind, Port: port})
			}
			switch transport {
			case "reverse", "encrypted":
				// 反向段：物理路径为下游 bridge 拨上游 portal（方向反转到上游机），
				// tcp 复用上游 portal 监听端口（公网侧按上游机 allowed_ports 换算）。
				if up.PortalPort > 0 {
					add(down.ServerID, up.ServerID, "tcp", natPublicPort(byID[up.ServerID], up.PortalPort), up.Address)
				}
				add(down.ServerID, up.ServerID, "icmp", 0, up.Address)
			case "hy2":
				// hy2 段 UDP/QUIC 无 TCP 端口可复用，仅 icmp。
				add(up.ServerID, down.ServerID, "icmp", 0, down.Address)
			default:
				// 直连段：上游拨下跳监听端口。下跳为出口时复用出口业务 realized 端口
				// （NAT 受限机换算公网侧），中间跳复用其 forward 监听端口。
				listen := down.ForwardPort
				if last {
					listen = realized.Port
				}
				if listen > 0 {
					add(up.ServerID, down.ServerID, "tcp", natPublicPort(byID[down.ServerID], listen), down.Address)
				}
				add(up.ServerID, down.ServerID, "icmp", 0, down.Address)
			}
		}
	}
	return edges, rows.Err()
}

// LatencyProbeTargets 按测速边聚合每台服务器的探测目标集（同 (from,to,kind,port)
// 去重——同一对服务器的多条链路只测一次）。
func (s *Store) LatencyProbeTargets(ctx context.Context) (map[int64][]ServerLatencyTarget, error) {
	edges, err := s.PublishedLatencyEdges(ctx)
	if err != nil {
		return nil, err
	}
	type edgeKey struct {
		from, to int64
		kind     string
		port     int
	}
	seen := make(map[edgeKey]ServerLatencyTarget)
	for _, edge := range edges {
		key := edgeKey{from: edge.FromServer, to: edge.ToServer, kind: edge.Kind, port: edge.Port}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = ServerLatencyTarget{ToServer: edge.ToServer, Address: edge.TargetAddress, Kind: edge.Kind, Port: edge.Port}
	}
	out := make(map[int64][]ServerLatencyTarget)
	for key, target := range seen {
		out[key.from] = append(out[key.from], ServerLatencyTarget{
			ToServer: key.to, Address: target.Address, Kind: key.kind, Port: key.port,
		})
	}
	return out, nil
}

// natPublicPort 将监听端口按服务器 allowed_ports 段换算为公网侧端口（无段原样返回）。
func natPublicPort(srv *Server, listen int) int {
	ranges, err := shared.ParsePortRanges(srv.AllowedPorts)
	if err != nil || len(ranges) == 0 {
		return listen
	}
	if pub, ok := shared.PublicPort(ranges, listen); ok {
		return pub
	}
	return listen
}

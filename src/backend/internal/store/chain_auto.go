package store

import (
	"context"
	"fmt"
	"time"
)

// PairWatchState 定义有向服务器对连通性状态。
const (
	PairStateNormal      = "normal"
	PairStateUnreachable = "unreachable"
)

// FailoverReasonCode 定义触发自动编排切换的原因码。
const (
	FailoverReasonBanned         = "server_banned"
	FailoverReasonUnreachable    = "pair_unreachable"
	FailoverReasonQuotaExhausted = "quota_exhausted"
	FailoverReasonAgentOffline   = "agent_offline"
	FailoverReasonFailback       = "failback"
	FailoverReasonManualSwitch   = "manual_switch"
)

// HopSwitchTarget 是一次跳位切换的目标（服务器 + 可选地址/端口覆盖）。
type HopSwitchTarget struct {
	ChainID    int64  `json:"chain_id"`
	Seq        int    `json:"seq"`
	ServerID   int64  `json:"server_id"`
	Address    string `json:"address"`
	ListenPort int    `json:"listen_port"`
}

// ServerPairWatch 表示有向服务器对（from_server -> to_server）的连通性观察快照。
type ServerPairWatch struct {
	FromServer int64
	ToServer   int64
	LossStreak int64
	State      string
	UpdatedAt  time.Time
}

// ChainFailoverEvent 表示一次自动（或手动快捷）跳位切换的审计事件。
type ChainFailoverEvent struct {
	ID           int64     `json:"id"`
	ChainID      int64     `json:"chain_id"`
	Seq          int       `json:"seq"`
	FromServerID int64     `json:"from_server_id"`
	ToServerID   int64     `json:"to_server_id"`
	ReasonCode   string    `json:"reason_code"`
	ReasonDetail string    `json:"reason_detail"`
	RevisionID   int64     `json:"revision_id"`
	CreatedAt    time.Time `json:"created_at"`
}

// ServerPairWatchMap 返回所有有向服务器对的连通性观察快照，键为 [2]int64{from, to}。
func (s *Store) ServerPairWatchMap(ctx context.Context) (map[[2]int64]ServerPairWatch, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT from_server, to_server, loss_streak, state, updated_at
		FROM server_pair_watch`)
	if err != nil {
		return nil, fmt.Errorf("list server pair watch: %w", err)
	}
	defer rows.Close()
	out := make(map[[2]int64]ServerPairWatch)
	for rows.Next() {
		var row ServerPairWatch
		if err := rows.Scan(&row.FromServer, &row.ToServer, &row.LossStreak, &row.State, &row.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan server pair watch: %w", err)
		}
		out[[2]int64{row.FromServer, row.ToServer}] = row
	}
	return out, rows.Err()
}

// UpsertServerPairWatch 插入或更新一条有向服务器对连通性观察状态。
func (s *Store) UpsertServerPairWatch(ctx context.Context, row ServerPairWatch) error {
	if row.State == "" {
		row.State = PairStateNormal
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO server_pair_watch
		(from_server, to_server, loss_streak, state, updated_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(from_server, to_server) DO UPDATE SET
		loss_streak=excluded.loss_streak, state=excluded.state, updated_at=excluded.updated_at`,
		row.FromServer, row.ToServer, row.LossStreak, row.State, row.UpdatedAt.UTC())
	if err != nil {
		return fmt.Errorf("upsert server pair watch: %w", err)
	}
	return nil
}

// RecordChainFailoverEvent 记录一条自动编排或快捷切换历史事件。
func (s *Store) RecordChainFailoverEvent(ctx context.Context, ev ChainFailoverEvent) (int64, error) {
	res, err := s.db.ExecContext(ctx, `INSERT INTO chain_failover_events
		(chain_id, seq, from_server_id, to_server_id, reason_code, reason_detail, revision_id)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		ev.ChainID, ev.Seq, ev.FromServerID, ev.ToServerID, ev.ReasonCode, ev.ReasonDetail, ev.RevisionID)
	if err != nil {
		return 0, fmt.Errorf("insert chain failover event: %w", err)
	}
	return res.LastInsertId()
}

// ChainFailoverEvents 查询指定链路最近的切换历史事件（按时间倒序）。
func (s *Store) ChainFailoverEvents(ctx context.Context, chainID int64, limit int) ([]ChainFailoverEvent, error) {
	if limit <= 0 || limit > 200 {
		limit = 20
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, chain_id, seq, from_server_id, to_server_id,
		reason_code, reason_detail, revision_id, created_at
		FROM chain_failover_events WHERE chain_id = ?
		ORDER BY created_at DESC, id DESC LIMIT ?`, chainID, limit)
	if err != nil {
		return nil, fmt.Errorf("list chain failover events: %w", err)
	}
	defer rows.Close()
	out := make([]ChainFailoverEvent, 0)
	for rows.Next() {
		var ev ChainFailoverEvent
		if err := rows.Scan(&ev.ID, &ev.ChainID, &ev.Seq, &ev.FromServerID, &ev.ToServerID,
			&ev.ReasonCode, &ev.ReasonDetail, &ev.RevisionID, &ev.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan chain failover event: %w", err)
		}
		out = append(out, ev)
	}
	return out, rows.Err()
}

// HopSwitchPolicy 是切换目标健康判定所需的阈值（来自编排图所在跳的配置）。
type HopSwitchPolicy struct {
	UnreachableStreak int
}

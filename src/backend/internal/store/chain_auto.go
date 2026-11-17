package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// BanTriggerLevel 定义触发自动切换的三网封禁观察级别。
const (
	BanTriggerSuspected = "suspected"
	BanTriggerBanned    = "banned"
)

// FailbackMode 定义主节点恢复后的回切模式。
const (
	FailbackSticky     = "sticky"     // 粘性：切换后保持在替补节点，不自动回切
	FailbackPreemptive = "preemptive" // 抢占回切：更高优先级候选恢复健康且过冷却期后自动切回
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

// ChainHopCandidate 表示链路上某个跳位（seq）的一个候选服务器条目。
// Priority = 0 表示管理员设定的首选主节点，Priority >= 1 表示按序替补的备选节点。
type ChainHopCandidate struct {
	ID         int64     `json:"id"`
	ChainID    int64     `json:"chain_id"`
	Seq        int       `json:"seq"`
	Priority   int       `json:"priority"`
	ServerID   int64     `json:"server_id"`
	Address    string    `json:"address"`
	ListenPort int       `json:"listen_port"`
	CreatedAt  time.Time `json:"created_at"`
}

// ChainAutoPolicy 表示单条链路的自动编排与故障切换策略。
type ChainAutoPolicy struct {
	ChainID           int64      `json:"chain_id"`
	Enabled           bool       `json:"enabled"`
	OnBan             bool       `json:"on_ban"`
	BanTriggerLevel   string     `json:"ban_trigger_level"`
	OnUnreachable     bool       `json:"on_unreachable"`
	UnreachableStreak int        `json:"unreachable_streak"`
	OnQuotaExhausted  bool       `json:"on_quota_exhausted"`
	OnAgentOffline    bool       `json:"on_agent_offline"`
	OfflineTimeoutSec int        `json:"offline_timeout_sec"`
	FailbackMode      string     `json:"failback_mode"`
	CooldownSec       int        `json:"cooldown_sec"`
	LastFailoverAt    *time.Time `json:"last_failover_at,omitempty"`
	UpdatedAt         time.Time  `json:"updated_at"`
}

// DefaultChainAutoPolicy 返回链路的默认自动编排策略。
func DefaultChainAutoPolicy(chainID int64) ChainAutoPolicy {
	return ChainAutoPolicy{
		ChainID:           chainID,
		Enabled:           false,
		OnBan:             true,
		BanTriggerLevel:   BanTriggerBanned,
		OnUnreachable:     true,
		UnreachableStreak: 3,
		OnQuotaExhausted:  true,
		OnAgentOffline:    false,
		OfflineTimeoutSec: 900,
		FailbackMode:      FailbackSticky,
		CooldownSec:       900,
	}
}

// Normalize 修正缺省枚举与越界阈值。
func (p *ChainAutoPolicy) Normalize() {
	if p.BanTriggerLevel != BanTriggerSuspected && p.BanTriggerLevel != BanTriggerBanned {
		p.BanTriggerLevel = BanTriggerBanned
	}
	if p.UnreachableStreak < 1 {
		p.UnreachableStreak = 3
	} else if p.UnreachableStreak > 100 {
		p.UnreachableStreak = 100
	}
	if p.OfflineTimeoutSec < 30 {
		p.OfflineTimeoutSec = 900
	} else if p.OfflineTimeoutSec > 86400 {
		p.OfflineTimeoutSec = 86400
	}
	if p.FailbackMode != FailbackSticky && p.FailbackMode != FailbackPreemptive {
		p.FailbackMode = FailbackSticky
	}
	if p.CooldownSec < 0 {
		p.CooldownSec = 900
	} else if p.CooldownSec > 86400 {
		p.CooldownSec = 86400
	}
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

// ReplaceChainHopCandidates 原子替换一条链路的全部跳位候选池记录。
func (s *Store) ReplaceChainHopCandidates(ctx context.Context, chainID int64, candidates []ChainHopCandidate) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin replace chain hop candidates: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `DELETE FROM chain_hop_candidates WHERE chain_id = ?`, chainID); err != nil {
		return fmt.Errorf("delete old chain hop candidates: %w", err)
	}
	for _, c := range candidates {
		if _, err := tx.ExecContext(ctx, `INSERT INTO chain_hop_candidates
			(chain_id, seq, priority, server_id, address, listen_port)
			VALUES (?, ?, ?, ?, ?, ?)`,
			chainID, c.Seq, c.Priority, c.ServerID, c.Address, c.ListenPort); err != nil {
			return fmt.Errorf("insert chain hop candidate (seq=%d, priority=%d): %w", c.Seq, c.Priority, err)
		}
	}
	return tx.Commit()
}

// ChainHopCandidates 返回指定链路的全部候选节点（按 seq 升序、priority 升序）。
func (s *Store) ChainHopCandidates(ctx context.Context, chainID int64) ([]ChainHopCandidate, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, chain_id, seq, priority, server_id, address, listen_port, created_at
		FROM chain_hop_candidates WHERE chain_id = ? ORDER BY seq ASC, priority ASC`, chainID)
	if err != nil {
		return nil, fmt.Errorf("query chain hop candidates: %w", err)
	}
	defer rows.Close()
	out := make([]ChainHopCandidate, 0)
	for rows.Next() {
		var c ChainHopCandidate
		if err := rows.Scan(&c.ID, &c.ChainID, &c.Seq, &c.Priority, &c.ServerID, &c.Address, &c.ListenPort, &c.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan chain hop candidate: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// AllChainHopCandidatesMap 返回所有未删除链路的候选池（按 chain_id 分组，组内按 seq、priority 升序）。
func (s *Store) AllChainHopCandidatesMap(ctx context.Context) (map[int64][]ChainHopCandidate, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT hc.id, hc.chain_id, hc.seq, hc.priority, hc.server_id, hc.address, hc.listen_port, hc.created_at
		FROM chain_hop_candidates hc
		JOIN chains c ON c.id = hc.chain_id
		WHERE c.deleted_at IS NULL
		ORDER BY hc.chain_id ASC, hc.seq ASC, hc.priority ASC`)
	if err != nil {
		return nil, fmt.Errorf("list all chain hop candidates: %w", err)
	}
	defer rows.Close()
	out := make(map[int64][]ChainHopCandidate)
	for rows.Next() {
		var c ChainHopCandidate
		if err := rows.Scan(&c.ID, &c.ChainID, &c.Seq, &c.Priority, &c.ServerID, &c.Address, &c.ListenPort, &c.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan chain hop candidate: %w", err)
		}
		out[c.ChainID] = append(out[c.ChainID], c)
	}
	return out, rows.Err()
}

// UpsertChainAutoPolicy 创建或更新一条链路的自动编排策略（保留已有的 last_failover_at）。
func (s *Store) UpsertChainAutoPolicy(ctx context.Context, p ChainAutoPolicy) error {
	p.Normalize()
	_, err := s.db.ExecContext(ctx, `INSERT INTO chain_auto_policies
		(chain_id, enabled, on_ban, ban_trigger_level, on_unreachable, unreachable_streak,
		 on_quota_exhausted, on_agent_offline, offline_timeout_sec, failback_mode, cooldown_sec, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(chain_id) DO UPDATE SET
		enabled=excluded.enabled,
		on_ban=excluded.on_ban,
		ban_trigger_level=excluded.ban_trigger_level,
		on_unreachable=excluded.on_unreachable,
		unreachable_streak=excluded.unreachable_streak,
		on_quota_exhausted=excluded.on_quota_exhausted,
		on_agent_offline=excluded.on_agent_offline,
		offline_timeout_sec=excluded.offline_timeout_sec,
		failback_mode=excluded.failback_mode,
		cooldown_sec=excluded.cooldown_sec,
		updated_at=CURRENT_TIMESTAMP`,
		p.ChainID, p.Enabled, p.OnBan, p.BanTriggerLevel, p.OnUnreachable, p.UnreachableStreak,
		p.OnQuotaExhausted, p.OnAgentOffline, p.OfflineTimeoutSec, p.FailbackMode, p.CooldownSec)
	if err != nil {
		return fmt.Errorf("upsert chain auto policy: %w", err)
	}
	return nil
}

func scanChainAutoPolicy(scanner interface{ Scan(...any) error }) (ChainAutoPolicy, error) {
	var p ChainAutoPolicy
	var lastFailover sql.NullTime
	err := scanner.Scan(&p.ChainID, &p.Enabled, &p.OnBan, &p.BanTriggerLevel, &p.OnUnreachable,
		&p.UnreachableStreak, &p.OnQuotaExhausted, &p.OnAgentOffline, &p.OfflineTimeoutSec,
		&p.FailbackMode, &p.CooldownSec, &lastFailover, &p.UpdatedAt)
	if err != nil {
		return ChainAutoPolicy{}, err
	}
	if lastFailover.Valid {
		t := lastFailover.Time
		p.LastFailoverAt = &t
	}
	return p, nil
}

// ChainAutoPolicyByID 查询指定链路的自动编排策略；若未持久化则返回 DefaultChainAutoPolicy。
func (s *Store) ChainAutoPolicyByID(ctx context.Context, chainID int64) (ChainAutoPolicy, error) {
	p, err := scanChainAutoPolicy(s.db.QueryRowContext(ctx, `SELECT chain_id, enabled, on_ban, ban_trigger_level,
		on_unreachable, unreachable_streak, on_quota_exhausted, on_agent_offline, offline_timeout_sec,
		failback_mode, cooldown_sec, last_failover_at, updated_at
		FROM chain_auto_policies WHERE chain_id = ?`, chainID))
	if errors.Is(err, sql.ErrNoRows) {
		return DefaultChainAutoPolicy(chainID), nil
	}
	if err != nil {
		return ChainAutoPolicy{}, fmt.Errorf("query chain auto policy: %w", err)
	}
	return p, nil
}

// ChainAutoPolicyMap 返回全部链路的自动编排策略字典（按 chain_id 索引）。
func (s *Store) ChainAutoPolicyMap(ctx context.Context) (map[int64]ChainAutoPolicy, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT chain_id, enabled, on_ban, ban_trigger_level,
		on_unreachable, unreachable_streak, on_quota_exhausted, on_agent_offline, offline_timeout_sec,
		failback_mode, cooldown_sec, last_failover_at, updated_at
		FROM chain_auto_policies`)
	if err != nil {
		return nil, fmt.Errorf("list chain auto policies: %w", err)
	}
	defer rows.Close()
	out := make(map[int64]ChainAutoPolicy)
	for rows.Next() {
		p, err := scanChainAutoPolicy(rows)
		if err != nil {
			return nil, fmt.Errorf("scan chain auto policy: %w", err)
		}
		out[p.ChainID] = p
	}
	return out, rows.Err()
}

// TouchChainFailoverAt 更新链路最近一次自动切换时间戳（用于冷却期判定）。
func (s *Store) TouchChainFailoverAt(ctx context.Context, chainID int64, at time.Time) error {
	_, err := s.db.ExecContext(ctx, `UPDATE chain_auto_policies
		SET last_failover_at = ?, updated_at = CURRENT_TIMESTAMP WHERE chain_id = ?`, at.UTC(), chainID)
	return err
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

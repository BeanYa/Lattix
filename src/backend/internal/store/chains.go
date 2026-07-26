package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// 链状态机（§21.1）：pending → applying → active | failed；
// 任一跳 server 离线推导 degraded（在线且跳均 active 后恢复 active）。
const (
	ChainStatusPending  = "pending"
	ChainStatusApplying = "applying"
	ChainStatusActive   = "active"
	ChainStatusDegraded = "degraded"
	ChainStatusFailed   = "failed"
)

// 跳角色（§21.1）：entry / middle / exit。
const (
	HopRoleEntry  = "entry"
	HopRoleMiddle = "middle"
	HopRoleExit   = "exit"
)

// 跳状态（§21.1）：pending → applying → active | failed（与节点状态机同语义）。
const (
	HopStatusPending  = "pending"
	HopStatusApplying = "applying"
	HopStatusActive   = "active"
	HopStatusFailed   = "failed"
)

// Chain 是 chains 表的一行（§21 链级状态机）。
type Chain struct {
	ID        int64
	Name      string
	Status    string
	Error     string
	CreatedAt time.Time
}

// ChainHop 是 chain_hops 表的一行（§21 逐跳状态与独立重试）。
// TunnelUUID 仅反向链 portal 所在跳（上游机）非空，同时充当该跳→下一跳为反向链的标记。
type ChainHop struct {
	ID               int64
	ChainID          int64
	Seq              int
	ServerID         int64
	Role             string
	NodeID           int64 // 仅出口跳：业务 nodes.id
	Status           string
	Error            string
	ForwardPort      int // entry 跳 = 订阅端口（监听侧）
	PortalPort       int
	PortalPublicKey  string
	PortalServerName string // portal 回执的 Reality SNI（bridge spec 用，空回退白名单首位）
	TunnelUUID       string
	CreatedAt        time.Time
}

const chainCols = `id, name, status, error, created_at`

func scanChain(row interface{ Scan(...any) error }) (*Chain, error) {
	var c Chain
	if err := row.Scan(&c.ID, &c.Name, &c.Status, &c.Error, &c.CreatedAt); err != nil {
		return nil, err
	}
	return &c, nil
}

const chainHopCols = `id, chain_id, seq, server_id, role, node_id, status, error, forward_port, portal_port, portal_public_key, portal_server_name, tunnel_uuid, created_at`

func scanChainHop(row interface{ Scan(...any) error }) (*ChainHop, error) {
	var h ChainHop
	err := row.Scan(&h.ID, &h.ChainID, &h.Seq, &h.ServerID, &h.Role, &h.NodeID, &h.Status, &h.Error,
		&h.ForwardPort, &h.PortalPort, &h.PortalPublicKey, &h.PortalServerName, &h.TunnelUUID, &h.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &h, nil
}

func scanChainHops(rows *sql.Rows) ([]ChainHop, error) {
	defer rows.Close()
	out := []ChainHop{}
	for rows.Next() {
		h, err := scanChainHop(rows)
		if err != nil {
			return nil, fmt.Errorf("scan chain hop: %w", err)
		}
		out = append(out, *h)
	}
	return out, rows.Err()
}

// InsertChain 插入一条链（pending），返回链 id。
func (s *Store) InsertChain(ctx context.Context, name string) (int64, error) {
	res, err := s.db.ExecContext(ctx, `INSERT INTO chains (name) VALUES (?)`, name)
	if err != nil {
		return 0, fmt.Errorf("insert chain: %w", err)
	}
	return res.LastInsertId()
}

// InsertChainHop 插入一个跳（pending）。forwardPort 仅入口跳用户指定时非 0；
// tunnelUUID 仅反向链 portal 所在跳（上游机）非空。
func (s *Store) InsertChainHop(ctx context.Context, chainID int64, seq int, serverID int64, role string, nodeID int64, forwardPort int, tunnelUUID string) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO chain_hops (chain_id, seq, server_id, role, node_id, forward_port, tunnel_uuid) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		chainID, seq, serverID, role, nodeID, forwardPort, tunnelUUID)
	if err != nil {
		return 0, fmt.Errorf("insert chain hop: %w", err)
	}
	return res.LastInsertId()
}

// ChainByID 按 id 查找链。
func (s *Store) ChainByID(ctx context.Context, id int64) (*Chain, error) {
	c, err := scanChain(s.db.QueryRowContext(ctx,
		`SELECT `+chainCols+` FROM chains WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("query chain: %w", err)
	}
	return c, nil
}

// ListChains 列出全部链（按 id 升序）。
func (s *Store) ListChains(ctx context.Context) ([]Chain, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+chainCols+` FROM chains ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("list chains: %w", err)
	}
	defer rows.Close()
	out := []Chain{}
	for rows.Next() {
		c, err := scanChain(rows)
		if err != nil {
			return nil, fmt.Errorf("scan chain: %w", err)
		}
		out = append(out, *c)
	}
	return out, rows.Err()
}

// ChainHops 列出一条链的全部跳（按 seq 升序：seq 0 = 入口，末位 = 出口）。
func (s *Store) ChainHops(ctx context.Context, chainID int64) ([]ChainHop, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+chainHopCols+` FROM chain_hops WHERE chain_id = ? ORDER BY seq`, chainID)
	if err != nil {
		return nil, fmt.Errorf("list chain hops: %w", err)
	}
	return scanChainHops(rows)
}

// ChainHopByID 按 id 查找跳。
func (s *Store) ChainHopByID(ctx context.Context, id int64) (*ChainHop, error) {
	h, err := scanChainHop(s.db.QueryRowContext(ctx,
		`SELECT `+chainHopCols+` FROM chain_hops WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("query chain hop: %w", err)
	}
	return h, nil
}

// ChainHopsByServerID 按 server 查跳（degraded 推导用，§21.1）。
func (s *Store) ChainHopsByServerID(ctx context.Context, serverID int64) ([]ChainHop, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+chainHopCols+` FROM chain_hops WHERE server_id = ? ORDER BY chain_id, seq`, serverID)
	if err != nil {
		return nil, fmt.Errorf("list chain hops by server: %w", err)
	}
	return scanChainHops(rows)
}

// ChainHopByNodeID 按业务节点查出口跳（apply_node 回执路由链编排用；无关联返回 ErrNotFound）。
func (s *Store) ChainHopByNodeID(ctx context.Context, nodeID int64) (*ChainHop, error) {
	h, err := scanChainHop(s.db.QueryRowContext(ctx,
		`SELECT `+chainHopCols+` FROM chain_hops WHERE node_id = ?`, nodeID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("query chain hop by node: %w", err)
	}
	return h, nil
}

// SetChainStatus 更新链状态与错误定位（error 定位到跳，§21）。
func (s *Store) SetChainStatus(ctx context.Context, id int64, status, errMsg string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE chains SET status = ?, error = ? WHERE id = ?`, status, errMsg, id)
	return err
}

// SetChainHopStatus 更新跳状态与错误详情。
func (s *Store) SetChainHopStatus(ctx context.Context, id int64, status, errMsg string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE chain_hops SET status = ?, error = ? WHERE id = ?`, status, errMsg, id)
	return err
}

// SetChainHopPortalRealized 写回 portal 回执的生效端口、公钥与 Reality SNI（§21.1）。
func (s *Store) SetChainHopPortalRealized(ctx context.Context, id int64, port int, publicKey, serverName string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE chain_hops SET portal_port = ?, portal_public_key = ?, portal_server_name = ? WHERE id = ?`,
		port, publicKey, serverName, id)
	return err
}

// SetChainHopForwardPort 写回 forward 回执的生效端口（entry 跳 = 订阅端口，监听侧）。
func (s *Store) SetChainHopForwardPort(ctx context.Context, id int64, port int) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE chain_hops SET forward_port = ? WHERE id = ?`, port, id)
	return err
}

// DeleteChain 删除链及其全部跳（remove_chain_hop / remove_node 已由 panel 先行下发，§21）。
func (s *Store) DeleteChain(ctx context.Context, id int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, q := range []string{
		`DELETE FROM chain_hops WHERE chain_id = ?`,
		`DELETE FROM chains WHERE id = ?`,
	} {
		if _, err := tx.ExecContext(ctx, q, id); err != nil {
			return fmt.Errorf("delete chain: %w", err)
		}
	}
	return tx.Commit()
}

// ChainExitNodeIDs 返回全部链出口业务节点 id 集合（订阅排除单机条目用，§21）。
func (s *Store) ChainExitNodeIDs(ctx context.Context) (map[int64]bool, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT DISTINCT node_id FROM chain_hops WHERE node_id != 0`)
	if err != nil {
		return nil, fmt.Errorf("list chain exit nodes: %w", err)
	}
	defer rows.Close()
	out := map[int64]bool{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan chain exit node: %w", err)
		}
		out[id] = true
	}
	return out, rows.Err()
}

// CommandsByType 列出指定类型的全部命令（链编排器从 commands 表推导 piece 进度用，§21.1）。
func (s *Store) CommandsByType(ctx context.Context, typ string) ([]Command, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, server_id, type, payload, status, attempts FROM commands WHERE type = ? ORDER BY id`, typ)
	if err != nil {
		return nil, fmt.Errorf("list commands by type: %w", err)
	}
	defer rows.Close()
	out := []Command{}
	for rows.Next() {
		var c Command
		var payload string
		if err := rows.Scan(&c.ID, &c.ServerID, &c.Type, &payload, &c.Status, &c.Attempts); err != nil {
			return nil, fmt.Errorf("scan command: %w", err)
		}
		c.Payload = json.RawMessage(payload)
		out = append(out, c)
	}
	return out, rows.Err()
}

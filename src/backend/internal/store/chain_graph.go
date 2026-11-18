package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// 编排图节点/触发器枚举。
const (
	HopGraphKindServer  = "server"
	HopGraphKindTrigger = "trigger"

	TriggerSubjectSelf = "self"
	TriggerSubjectPrev = "prev"
	TriggerSubjectNext = "next"
	TriggerSubjectRoot = "root"

	TriggerActionBanSuspected   = "ban_suspected"
	TriggerActionBanBanned      = "ban_banned"
	TriggerActionSegmentTimeout = "segment_timeout"
	TriggerActionRecovered      = "recovered"

	DefaultHopGraphCooldownSec = 300
)

// ChainHopGraph 是一条链路某个跳位的编排图（根=该跳原服务器）。
type ChainHopGraph struct {
	ChainID      int64
	Seq          int
	CooldownSec  int
	LastSwitchAt *time.Time
	Nodes        []ChainHopGraphNode
	Edges        []ChainHopGraphEdge
}

// ChainHopGraphNode 是图里的服务器节点或触发器节点。
type ChainHopGraphNode struct {
	ID         int64
	Kind       string
	ServerID   int64
	Address    string
	ListenPort int
	Subject    string
	Action     string
	Priority   int
	IsRoot     bool
	Ordinal    int
}

// ChainHopGraphEdge 是一条有向边（server→trigger 或 trigger→server）。
type ChainHopGraphEdge struct {
	FromNode int64
	ToNode   int64
}

// ChainHopGraphRef 指向某个被清空的编排图。
type ChainHopGraphRef struct {
	ChainID      int64
	Seq          int
	RootServerID int64
}

func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func nullableString(v string) any {
	if v == "" {
		return nil
	}
	return v
}

// ReplaceChainHopGraph 原子替换一条链路某个跳位的整张编排图；空节点表示删除该图。
func (s *Store) ReplaceChainHopGraph(ctx context.Context, chainID int64, seq int, nodes []ChainHopGraphNode, edges []ChainHopGraphEdge) (*ChainHopGraph, error) {
	if chainID <= 0 || seq < 0 {
		return nil, fmt.Errorf("invalid graph target: chain %d seq %d", chainID, seq)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if len(nodes) == 0 {
		if err := deleteChainHopGraphTx(tx, chainID, seq); err != nil {
			return nil, err
		}
		return nil, tx.Commit()
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO chain_hop_graphs (chain_id, seq) VALUES (?, ?)
		ON CONFLICT(chain_id, seq) DO UPDATE SET updated_at=CURRENT_TIMESTAMP`, chainID, seq); err != nil {
		return nil, fmt.Errorf("upsert hop graph: %w", err)
	}
	if err := deleteChainHopGraphRowsTx(tx, chainID, seq); err != nil {
		return nil, err
	}
	ids := make([]int64, len(nodes))
	for i, n := range nodes {
		var serverID any
		if n.ServerID > 0 {
			serverID = n.ServerID
		}
		res, err := tx.ExecContext(ctx, `INSERT INTO chain_hop_graph_nodes
			(chain_id, seq, kind, server_id, address, listen_port, trigger_subject, trigger_action, priority, is_root, ordinal)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			chainID, seq, n.Kind, serverID, n.Address, n.ListenPort,
			nullableString(n.Subject), nullableString(n.Action), n.Priority, boolInt(n.IsRoot), i)
		if err != nil {
			return nil, fmt.Errorf("insert hop graph node: %w", err)
		}
		id, err := res.LastInsertId()
		if err != nil {
			return nil, err
		}
		ids[i] = id
	}
	for _, e := range edges {
		if e.FromNode < 0 || e.FromNode >= int64(len(nodes)) || e.ToNode < 0 || e.ToNode >= int64(len(nodes)) {
			return nil, fmt.Errorf("hop graph edge out of range: %d -> %d", e.FromNode, e.ToNode)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO chain_hop_graph_edges
			(chain_id, seq, from_node, to_node) VALUES (?, ?, ?, ?)`,
			chainID, seq, ids[int(e.FromNode)], ids[int(e.ToNode)]); err != nil {
			return nil, fmt.Errorf("insert hop graph edge: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.ChainHopGraph(ctx, chainID, seq)
}

// ChainHopGraph 读取单个跳位的编排图；不存在返回 (nil, nil)。
func (s *Store) ChainHopGraph(ctx context.Context, chainID int64, seq int) (*ChainHopGraph, error) {
	graphs, err := s.ChainHopGraphs(ctx, chainID)
	if err != nil {
		return nil, err
	}
	if g, ok := graphs[seq]; ok {
		return &g, nil
	}
	return nil, nil
}

// ChainHopGraphs 读取一条链路的全部编排图（按 seq）。
func (s *Store) ChainHopGraphs(ctx context.Context, chainID int64) (map[int]ChainHopGraph, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT seq, cooldown_sec, last_switch_at FROM chain_hop_graphs
		WHERE chain_id=? ORDER BY seq`, chainID)
	if err != nil {
		return nil, err
	}
	out := make(map[int]ChainHopGraph)
	for rows.Next() {
		var g ChainHopGraph
		var last sql.NullTime
		if err := rows.Scan(&g.Seq, &g.CooldownSec, &last); err != nil {
			rows.Close()
			return nil, err
		}
		g.ChainID = chainID
		if last.Valid {
			t := last.Time
			g.LastSwitchAt = &t
		}
		out[g.Seq] = g
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	if len(out) == 0 {
		return out, nil
	}
	nodeRows, err := s.db.QueryContext(ctx, `SELECT id, seq, kind, server_id, address, listen_port,
		trigger_subject, trigger_action, priority, is_root, ordinal
		FROM chain_hop_graph_nodes WHERE chain_id=? ORDER BY seq, ordinal, id`, chainID)
	if err != nil {
		return nil, err
	}
	for nodeRows.Next() {
		var n ChainHopGraphNode
		var seq int
		var serverID sql.NullInt64
		var subject, action sql.NullString
		var isRoot int
		if err := nodeRows.Scan(&n.ID, &seq, &n.Kind, &serverID, &n.Address, &n.ListenPort,
			&subject, &action, &n.Priority, &isRoot, &n.Ordinal); err != nil {
			nodeRows.Close()
			return nil, err
		}
		n.ServerID = serverID.Int64
		n.Subject = subject.String
		n.Action = action.String
		n.IsRoot = isRoot == 1
		g := out[seq]
		g.Nodes = append(g.Nodes, n)
		out[seq] = g
	}
	if err := nodeRows.Err(); err != nil {
		nodeRows.Close()
		return nil, err
	}
	nodeRows.Close()
	edgeRows, err := s.db.QueryContext(ctx, `SELECT seq, from_node, to_node FROM chain_hop_graph_edges
		WHERE chain_id=? ORDER BY seq, id`, chainID)
	if err != nil {
		return nil, err
	}
	defer edgeRows.Close()
	for edgeRows.Next() {
		var seq int
		var e ChainHopGraphEdge
		if err := edgeRows.Scan(&seq, &e.FromNode, &e.ToNode); err != nil {
			return nil, err
		}
		g := out[seq]
		g.Edges = append(g.Edges, e)
		out[seq] = g
	}
	return out, edgeRows.Err()
}

// DeleteChainHopGraph 删除一个跳位的编排图。
func (s *Store) DeleteChainHopGraph(ctx context.Context, chainID int64, seq int) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := deleteChainHopGraphTx(tx, chainID, seq); err != nil {
		return err
	}
	return tx.Commit()
}

func deleteChainHopGraphRowsTx(tx *sql.Tx, chainID int64, seq int) error {
	if _, err := tx.Exec(`DELETE FROM chain_hop_graph_edges WHERE chain_id=? AND seq=?`, chainID, seq); err != nil {
		return fmt.Errorf("delete hop graph edges: %w", err)
	}
	if _, err := tx.Exec(`DELETE FROM chain_hop_graph_nodes WHERE chain_id=? AND seq=?`, chainID, seq); err != nil {
		return fmt.Errorf("delete hop graph nodes: %w", err)
	}
	return nil
}

func deleteChainHopGraphTx(tx *sql.Tx, chainID int64, seq int) error {
	if err := deleteChainHopGraphRowsTx(tx, chainID, seq); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM chain_hop_graphs WHERE chain_id=? AND seq=?`, chainID, seq); err != nil {
		return fmt.Errorf("delete hop graph: %w", err)
	}
	return nil
}

func deleteChainHopGraphsForChainTx(tx *sql.Tx, chainID int64) error {
	if _, err := tx.Exec(`DELETE FROM chain_hop_graph_edges WHERE chain_id=?`, chainID); err != nil {
		return fmt.Errorf("delete chain hop graph edges: %w", err)
	}
	if _, err := tx.Exec(`DELETE FROM chain_hop_graph_nodes WHERE chain_id=?`, chainID); err != nil {
		return fmt.Errorf("delete chain hop graph nodes: %w", err)
	}
	if _, err := tx.Exec(`DELETE FROM chain_hop_graphs WHERE chain_id=?`, chainID); err != nil {
		return fmt.Errorf("delete chain hop graphs: %w", err)
	}
	return nil
}

// ClearChainHopGraphsForServer 清空引用指定服务器的整张跳位图（服务器删除）。
func (s *Store) ClearChainHopGraphsForServer(ctx context.Context, serverID int64) ([]ChainHopGraphRef, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	refs, err := clearChainHopGraphsForServerTx(tx, serverID)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return refs, nil
}

func clearChainHopGraphsForServerTx(tx *sql.Tx, serverID int64) ([]ChainHopGraphRef, error) {
	rows, err := tx.Query(`SELECT DISTINCT chain_id, seq FROM chain_hop_graph_nodes
		WHERE kind='server' AND server_id=?`, serverID)
	if err != nil {
		return nil, err
	}
	type target struct{ chainID int64; seq int }
	var targets []target
	for rows.Next() {
		var t target
		if err := rows.Scan(&t.chainID, &t.seq); err != nil {
			rows.Close()
			return nil, err
		}
		targets = append(targets, t)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	refs := make([]ChainHopGraphRef, 0, len(targets))
	for _, t := range targets {
		var root sql.NullInt64
		if err := tx.QueryRow(`SELECT server_id FROM chain_hop_graph_nodes
			WHERE chain_id=? AND seq=? AND is_root=1`, t.chainID, t.seq).Scan(&root); err != nil && err != sql.ErrNoRows {
			return nil, err
		}
		refs = append(refs, ChainHopGraphRef{ChainID: t.chainID, Seq: t.seq, RootServerID: root.Int64})
		if err := deleteChainHopGraphTx(tx, t.chainID, t.seq); err != nil {
			return nil, err
		}
	}
	return refs, nil
}

// reconcileChainHopGraphsTx 在工作拓扑替换时协调编排图：
// 根与新跳服务器不一致/跳被删除/跨跳复用 → 清图；根不变则保留并同步当前节点的地址/端口。
// 仅对建链/编辑修订调用（切换修订必须保留图）。
func reconcileChainHopGraphsTx(tx *sql.Tx, chainID int64, hops []ChainRevisionHop) error {
	primaries := make(map[int64]int, len(hops))
	for seq, hop := range hops {
		primaries[hop.ServerID] = seq
	}
	type graphInfo struct {
		seq     int
		root    int64
		servers []int64
	}
	rows, err := tx.Query(`SELECT seq FROM chain_hop_graphs WHERE chain_id=? ORDER BY seq`, chainID)
	if err != nil {
		return err
	}
	var graphs []graphInfo
	for rows.Next() {
		var g graphInfo
		if err := rows.Scan(&g.seq); err != nil {
			rows.Close()
			return err
		}
		var root sql.NullInt64
		if err := tx.QueryRow(`SELECT server_id FROM chain_hop_graph_nodes
			WHERE chain_id=? AND seq=? AND is_root=1`, chainID, g.seq).Scan(&root); err != nil && err != sql.ErrNoRows {
			rows.Close()
			return err
		}
		g.root = root.Int64
		graphs = append(graphs, g)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for i := range graphs {
		serverRows, err := tx.Query(`SELECT server_id FROM chain_hop_graph_nodes
			WHERE chain_id=? AND seq=? AND kind='server'`, chainID, graphs[i].seq)
		if err != nil {
			return err
		}
		for serverRows.Next() {
			var id sql.NullInt64
			if err := serverRows.Scan(&id); err != nil {
				serverRows.Close()
				return err
			}
			if id.Valid {
				graphs[i].servers = append(graphs[i].servers, id.Int64)
			}
		}
		if err := serverRows.Err(); err != nil {
			serverRows.Close()
			return err
		}
		serverRows.Close()
	}
	for _, g := range graphs {
		clear := g.seq < 0 || g.seq >= len(hops) || g.root == 0 || hops[g.seq].ServerID != g.root
		if !clear {
			for _, srv := range g.servers {
				if seq, ok := primaries[srv]; ok && seq != g.seq {
					clear = true
					break
				}
			}
		}
		if clear {
			if err := deleteChainHopGraphTx(tx, chainID, g.seq); err != nil {
				return err
			}
			continue
		}
		hop := hops[g.seq]
		if _, err := tx.Exec(`UPDATE chain_hop_graph_nodes SET address=?, listen_port=?
			WHERE chain_id=? AND seq=? AND kind='server' AND server_id=?`,
			hop.Address, hop.ForwardPort, chainID, g.seq, hop.ServerID); err != nil {
			return err
		}
	}
	return nil
}
// TouchChainHopGraphSwitch 记录某跳最近一次自动切换时刻（冷却窗口）。
func (s *Store) TouchChainHopGraphSwitch(ctx context.Context, chainID int64, seq int, at time.Time) error {
	_, err := s.db.ExecContext(ctx, `UPDATE chain_hop_graphs SET last_switch_at=?, updated_at=CURRENT_TIMESTAMP
		WHERE chain_id=? AND seq=?`, at.UTC(), chainID, seq)
	return err
}

// ResetChainHopGraphLastSwitch 清空某跳的切换冷却（测试/管理员重试用）。
func (s *Store) ResetChainHopGraphLastSwitch(ctx context.Context, chainID int64, seq int) error {
	_, err := s.db.ExecContext(ctx, `UPDATE chain_hop_graphs SET last_switch_at=NULL WHERE chain_id=? AND seq=?`, chainID, seq)
	return err
}

// SetChainOrchestrationError 记录/清空某链的编排失败现场（worker failed）。
func (s *Store) SetChainOrchestrationError(ctx context.Context, chainID int64, message string) error {
	if message == "" {
		_, err := s.db.ExecContext(ctx, `DELETE FROM chain_orchestration_states WHERE chain_id=?`, chainID)
		return err
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO chain_orchestration_states (chain_id, error, updated_at)
		VALUES (?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(chain_id) DO UPDATE SET error=excluded.error, updated_at=CURRENT_TIMESTAMP`, chainID, message)
	return err
}

// ChainOrchestrationError 读取某链的编排失败现场；空串表示正常。
func (s *Store) ChainOrchestrationError(ctx context.Context, chainID int64) (string, error) {
	var message string
	err := s.db.QueryRowContext(ctx, `SELECT error FROM chain_orchestration_states WHERE chain_id=?`, chainID).Scan(&message)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return message, err
}

// ChainIDsWithHopGraphs 返回所有含编排图的链（worker 只为这些链创建）。
func (s *Store) ChainIDsWithHopGraphs(ctx context.Context) ([]int64, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT chain_id FROM chain_hop_graphs ORDER BY chain_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// ChainsWithWorkingHopOnServer 返回当前工作拓扑里用该服务器作跳的链（决定是否失效）。
func (s *Store) ChainsWithWorkingHopOnServer(ctx context.Context, serverID int64) ([]Chain, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT chain_id FROM chain_hops WHERE server_id=?`, serverID)
	if err != nil {
		return nil, err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	var out []Chain
	for _, id := range ids {
		chain, err := s.ChainByID(ctx, id)
		if err != nil || chain.Status == ChainStatusDeleted {
			continue
		}
		out = append(out, *chain)
	}
	return out, nil
}

// ChainsWithGraphNodeOnServer 返回编排图里引用该服务器的跳位（图节点，非工作跳）。
func (s *Store) ChainsWithGraphNodeOnServer(ctx context.Context, serverID int64) ([]ChainHopGraphRef, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT chain_id, seq FROM chain_hop_graph_nodes
		WHERE kind='server' AND server_id=?`, serverID)
	if err != nil {
		return nil, err
	}
	type target struct{ chainID int64; seq int }
	var targets []target
	for rows.Next() {
		var t target
		if err := rows.Scan(&t.chainID, &t.seq); err != nil {
			rows.Close()
			return nil, err
		}
		targets = append(targets, t)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	out := make([]ChainHopGraphRef, 0, len(targets))
	for _, t := range targets {
		var root sql.NullInt64
		if err := s.db.QueryRowContext(ctx, `SELECT server_id FROM chain_hop_graph_nodes
			WHERE chain_id=? AND seq=? AND is_root=1`, t.chainID, t.seq).Scan(&root); err != nil && err != sql.ErrNoRows {
			return nil, err
		}
		out = append(out, ChainHopGraphRef{ChainID: t.chainID, Seq: t.seq, RootServerID: root.Int64})
	}
	return out, nil
}


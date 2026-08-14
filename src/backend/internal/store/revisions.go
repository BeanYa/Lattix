package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

const (
	RevisionStatusApplying          = "applying"
	RevisionStatusWaitingForAgent   = "waiting_for_agent"
	RevisionStatusActive            = "active"
	RevisionStatusActiveUnconfirmed = "active_unconfirmed"
	RevisionStatusActiveFailed      = "active_failed"
	RevisionStatusFailed            = "failed"
	RevisionStatusCancelled         = "cancelled"
)

const (
	RevisionTaskPending   = "pending"
	RevisionTaskQueued    = "queued"
	RevisionTaskAcked     = "acked"
	RevisionTaskFailed    = "failed"
	RevisionTaskAbandoned = "abandoned"
)

const (
	RevisionPieceService = "service"
	RevisionPieceForward = "forward"
	RevisionPiecePortal  = "portal"
	RevisionPieceBridge  = "bridge"
)

type ChainRevisionHop struct {
	HopID            int64  `json:"hop_id"`
	ServerID         int64  `json:"server_id"`
	Role             string `json:"role"`
	Transport        string `json:"transport"`
	ForwardPort      int    `json:"forward_port"`
	PortalPort       int    `json:"portal_port"`
	PortalPublicKey  string `json:"portal_public_key,omitempty"`
	PortalServerName string `json:"portal_server_name,omitempty"`
	TunnelUUID       string `json:"tunnel_uuid,omitempty"`
}

type ChainRevisionSnapshot struct {
	Name                   string             `json:"name"`
	ServiceNodeID          int64              `json:"service_node_id"`
	ServiceServerID        int64              `json:"service_server_id"`
	EndpointID             int64              `json:"endpoint_id,omitempty"`
	ServiceUUID            string             `json:"service_uuid,omitempty"`
	ServiceConfig          json.RawMessage    `json:"service_config"`
	ServiceRealized        json.RawMessage    `json:"service_realized,omitempty"`
	TrafficMultiplierMilli int                `json:"traffic_multiplier_milli"`
	Hops                   []ChainRevisionHop `json:"hops"`
	ApplyKeys              []string           `json:"apply_keys,omitempty"`
}

type ChainRevision struct {
	ID          int64
	ChainID     int64
	RevisionNo  int
	Status      string
	Forced      bool
	Snapshot    ChainRevisionSnapshot
	Error       string
	CreatedAt   time.Time
	PublishedAt *time.Time
}

type ChainRevisionTask struct {
	ID         int64
	RevisionID int64
	TaskKey    string
	Phase      string
	Action     string
	Kind       string
	HopID      int64
	ServerID   int64
	Status     string
	CommandID  int64
	Error      string
}

type InitialChainHop struct {
	ServerID    int64
	Role        string
	Transport   string
	ForwardPort int
	TunnelUUID  string
}

type InitialChainDeployment struct {
	Name                   string
	ServiceServerID        int64
	ServiceProtocol        string
	ServicePort            *int
	ServiceConfig          json.RawMessage
	EndpointID             int64
	ServiceUUID            string
	TrafficMultiplierMilli int
	Hops                   []InitialChainHop
}

type InitialChainDeploymentResult struct {
	ChainID    int64
	NodeID     int64
	RevisionID int64
	Hops       []ChainRevisionHop
	ApplyKeys  []string
}

// CreateInitialChainDeployment persists a new chain and its complete first
// deployment plan atomically. No scheduler-visible aggregate exists until all
// revision tasks have been stored successfully.
func (s *Store) CreateInitialChainDeployment(
	ctx context.Context,
	input InitialChainDeployment,
) (InitialChainDeploymentResult, error) {
	var out InitialChainDeploymentResult
	if len(input.Hops) == 0 {
		return out, errors.New("initial chain deployment requires at least one hop")
	}
	if input.Hops[len(input.Hops)-1].TunnelUUID != "" {
		return out, errors.New("initial chain exit hop cannot open a tunnel to a missing downstream hop")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return out, fmt.Errorf("begin initial chain deployment: %w", err)
	}
	defer tx.Rollback()

	nodeResult, err := tx.ExecContext(ctx,
		`INSERT INTO nodes (name, server_id, protocol, port, config_template) VALUES (?, ?, ?, ?, ?)`,
		input.Name, input.ServiceServerID, input.ServiceProtocol, input.ServicePort, string(input.ServiceConfig))
	if err != nil {
		return out, fmt.Errorf("insert initial chain service node: %w", err)
	}
	out.NodeID, err = nodeResult.LastInsertId()
	if err != nil {
		return out, fmt.Errorf("read initial chain service node id: %w", err)
	}

	chainResult, err := tx.ExecContext(ctx,
		`INSERT INTO chains (name, service_node_id, endpoint_id, service_uuid, traffic_multiplier_milli)
		 VALUES (?, ?, ?, ?, ?)`,
		input.Name, out.NodeID, input.EndpointID, input.ServiceUUID, input.TrafficMultiplierMilli)
	if err != nil {
		return out, fmt.Errorf("insert initial chain: %w", err)
	}
	out.ChainID, err = chainResult.LastInsertId()
	if err != nil {
		return out, fmt.Errorf("read initial chain id: %w", err)
	}

	out.Hops = make([]ChainRevisionHop, 0, len(input.Hops))
	for seq, hop := range input.Hops {
		nodeID := int64(0)
		if seq == len(input.Hops)-1 {
			nodeID = out.NodeID
		}
		hopResult, err := tx.ExecContext(ctx,
			`INSERT INTO chain_hops (chain_id, seq, server_id, role, node_id, forward_port, tunnel_uuid)
			 VALUES (?, ?, ?, ?, ?, ?, ?)`,
			out.ChainID, seq, hop.ServerID, hop.Role, nodeID, hop.ForwardPort, hop.TunnelUUID)
		if err != nil {
			return out, fmt.Errorf("insert initial chain hop %d: %w", seq, err)
		}
		hopID, err := hopResult.LastInsertId()
		if err != nil {
			return out, fmt.Errorf("read initial chain hop %d id: %w", seq, err)
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO chain_hop_identities (id, chain_id, server_id) VALUES (?, ?, ?)`,
			hopID, out.ChainID, hop.ServerID); err != nil {
			return out, fmt.Errorf("insert initial chain hop %d identity: %w", seq, err)
		}
		out.Hops = append(out.Hops, ChainRevisionHop{
			HopID: hopID, ServerID: hop.ServerID, Role: hop.Role, Transport: hop.Transport,
			ForwardPort: hop.ForwardPort, TunnelUUID: hop.TunnelUUID,
		})
	}

	out.ApplyKeys = []string{}
	if input.EndpointID == 0 || len(out.Hops) > 1 {
		out.ApplyKeys = append(out.ApplyKeys, fmt.Sprintf("%s/%d", RevisionPieceService, out.NodeID))
	}
	for i, hop := range out.Hops {
		if i < len(out.Hops)-1 {
			out.ApplyKeys = append(out.ApplyKeys, fmt.Sprintf("%s/%d", RevisionPieceForward, hop.HopID))
		}
		if hop.TunnelUUID != "" {
			out.ApplyKeys = append(out.ApplyKeys,
				fmt.Sprintf("%s/%d", RevisionPiecePortal, hop.HopID),
				fmt.Sprintf("%s/%d", RevisionPieceBridge, out.Hops[i+1].HopID))
		}
	}

	snapshot := ChainRevisionSnapshot{
		Name: input.Name, ServiceNodeID: out.NodeID, ServiceServerID: input.ServiceServerID,
		EndpointID: input.EndpointID, ServiceUUID: input.ServiceUUID,
		ServiceConfig: input.ServiceConfig, TrafficMultiplierMilli: input.TrafficMultiplierMilli,
		Hops: out.Hops, ApplyKeys: out.ApplyKeys,
	}
	rawSnapshot, err := json.Marshal(snapshot)
	if err != nil {
		return out, fmt.Errorf("encode initial chain revision: %w", err)
	}
	revisionResult, err := tx.ExecContext(ctx,
		`INSERT INTO chain_revisions (chain_id, revision_no, snapshot) VALUES (?, 1, ?)`,
		out.ChainID, string(rawSnapshot))
	if err != nil {
		return out, fmt.Errorf("insert initial chain revision: %w", err)
	}
	out.RevisionID, err = revisionResult.LastInsertId()
	if err != nil {
		return out, fmt.Errorf("read initial chain revision id: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE chains SET desired_revision_id=?, updated_at=CURRENT_TIMESTAMP WHERE id=?`,
		out.RevisionID, out.ChainID); err != nil {
		return out, fmt.Errorf("link initial chain revision: %w", err)
	}

	serverByHopID := make(map[int64]int64, len(out.Hops))
	for _, hop := range out.Hops {
		serverByHopID[hop.HopID] = hop.ServerID
	}
	for _, key := range out.ApplyKeys {
		kind, rawID, ok := strings.Cut(key, "/")
		if !ok {
			return out, fmt.Errorf("invalid initial chain task key %q", key)
		}
		pieceID, err := strconv.ParseInt(rawID, 10, 64)
		if err != nil {
			return out, fmt.Errorf("invalid initial chain task key %q: %w", key, err)
		}
		var serverID int64
		switch kind {
		case RevisionPieceService:
			if pieceID != out.NodeID {
				return out, fmt.Errorf("initial chain service task %q has an invalid node", key)
			}
			serverID = input.ServiceServerID
		case RevisionPieceForward, RevisionPiecePortal, RevisionPieceBridge:
			var found bool
			serverID, found = serverByHopID[pieceID]
			if !found {
				return out, fmt.Errorf("initial chain task %q has no hop server", key)
			}
		default:
			return out, fmt.Errorf("initial chain task %q has an invalid kind", key)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO chain_revision_tasks
			(revision_id, task_key, phase, action, kind, hop_id, server_id, status)
			VALUES (?, ?, 'apply', 'apply', ?, ?, ?, ?)`,
			out.RevisionID, key, kind, pieceID, serverID, RevisionTaskPending); err != nil {
			return out, fmt.Errorf("insert initial chain task %q: %w", key, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return out, fmt.Errorf("commit initial chain deployment: %w", err)
	}
	return out, nil
}

func (s *Store) CreateHopIdentity(ctx context.Context, chainID, serverID int64) (int64, error) {
	res, err := s.db.ExecContext(ctx, `INSERT INTO chain_hop_identities (chain_id, server_id) VALUES (?, ?)`, chainID, serverID)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) CreateChainRevision(ctx context.Context, chainID int64, snapshot ChainRevisionSnapshot) (*ChainRevision, error) {
	raw, err := json.Marshal(snapshot)
	if err != nil {
		return nil, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var next int
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(revision_no),0)+1 FROM chain_revisions WHERE chain_id=?`, chainID).Scan(&next); err != nil {
		return nil, err
	}
	res, err := tx.ExecContext(ctx, `INSERT INTO chain_revisions (chain_id, revision_no, snapshot) VALUES (?, ?, ?)`, chainID, next, string(raw))
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	if _, err := tx.ExecContext(ctx, `UPDATE chains SET desired_revision_id=?, updated_at=CURRENT_TIMESTAMP WHERE id=?`, id, chainID); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.ChainRevisionByID(ctx, id)
}

func scanRevision(row interface{ Scan(...any) error }) (*ChainRevision, error) {
	var revision ChainRevision
	var forced int
	var snapshot string
	var published sql.NullTime
	if err := row.Scan(&revision.ID, &revision.ChainID, &revision.RevisionNo, &revision.Status, &forced,
		&snapshot, &revision.Error, &revision.CreatedAt, &published); err != nil {
		return nil, err
	}
	revision.Forced = forced != 0
	if err := json.Unmarshal([]byte(snapshot), &revision.Snapshot); err != nil {
		return nil, fmt.Errorf("decode revision snapshot: %w", err)
	}
	if published.Valid {
		revision.PublishedAt = &published.Time
	}
	return &revision, nil
}

func (s *Store) ChainRevisionByID(ctx context.Context, id int64) (*ChainRevision, error) {
	revision, err := scanRevision(s.db.QueryRowContext(ctx, `SELECT id, chain_id, revision_no, status, forced,
		snapshot, error, created_at, published_at FROM chain_revisions WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return revision, err
}

func (s *Store) DesiredChainRevision(ctx context.Context, chainID int64) (*ChainRevision, error) {
	revision, err := scanRevision(s.db.QueryRowContext(ctx, `SELECT r.id, r.chain_id, r.revision_no, r.status, r.forced,
		r.snapshot, r.error, r.created_at, r.published_at FROM chain_revisions r
		JOIN chains c ON c.desired_revision_id=r.id WHERE c.id=?`, chainID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return revision, err
}

func (s *Store) PublishedChainRevision(ctx context.Context, chainID int64) (*ChainRevision, error) {
	revision, err := scanRevision(s.db.QueryRowContext(ctx, `SELECT r.id, r.chain_id, r.revision_no, r.status, r.forced,
		r.snapshot, r.error, r.created_at, r.published_at FROM chain_revisions r
		JOIN chains c ON c.published_revision_id=r.id WHERE c.id=?`, chainID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return revision, err
}

func (s *Store) ChainDeploymentRevisions(ctx context.Context, chainID int64) ([]ChainRevision, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT r.id, r.chain_id, r.revision_no, r.status, r.forced,
		r.snapshot, r.error, r.created_at, r.published_at FROM chain_revisions r
		JOIN chains c ON c.id=r.chain_id
		WHERE c.id=? AND (r.id=c.published_revision_id OR r.id=c.desired_revision_id)
		ORDER BY r.revision_no`, chainID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var revisions []ChainRevision
	for rows.Next() {
		revision, err := scanRevision(rows)
		if err != nil {
			return nil, err
		}
		revisions = append(revisions, *revision)
	}
	return revisions, rows.Err()
}

func (s *Store) ChainsReferencingServer(ctx context.Context, serverID int64) ([]Chain, error) {
	chains, err := s.ListChains(ctx)
	if err != nil {
		return nil, err
	}
	var out []Chain
	for _, chain := range chains {
		if chain.Status == ChainStatusDeleted {
			continue
		}
		revisions, err := s.ChainDeploymentRevisions(ctx, chain.ID)
		if err != nil {
			return nil, err
		}
		found := false
		for _, revision := range revisions {
			for _, hop := range revision.Snapshot.Hops {
				if hop.ServerID == serverID {
					found = true
					break
				}
			}
		}
		if !found {
			hops, err := s.ChainHops(ctx, chain.ID)
			if err != nil {
				return nil, err
			}
			for _, hop := range hops {
				if hop.ServerID == serverID {
					found = true
					break
				}
			}
		}
		if found {
			out = append(out, chain)
		}
	}
	return out, nil
}

// InvalidateChainForServerDeletion 服务器删除时级联失效链。可选 fromStatus 开启 CAS：
// 仅当链仍处于链状态机已校验的状态时写入 invalid，否则回滚并返回 ErrChainStatusChanged。
func (s *Store) InvalidateChainForServerDeletion(ctx context.Context, chainID, serverID int64, reason string, fromStatus ...string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `UPDATE commands SET status=?, error=?, updated_at=CURRENT_TIMESTAMP
		WHERE id IN (SELECT command_id FROM chain_revision_tasks WHERE revision_id IN
			(SELECT id FROM chain_revisions WHERE chain_id=?)) AND status IN (?, ?)`,
		CommandStatusAbandoned, reason, chainID, CommandStatusQueued, CommandStatusSent); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE chain_revision_tasks SET status=?, error=?, updated_at=CURRENT_TIMESTAMP
		WHERE revision_id IN (SELECT id FROM chain_revisions WHERE chain_id=?) AND status IN (?, ?)`,
		RevisionTaskAbandoned, reason, chainID, RevisionTaskPending, RevisionTaskQueued); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE chain_revisions SET status=?, error=?
		WHERE chain_id=? AND id=(SELECT desired_revision_id FROM chains WHERE id=?)`,
		RevisionStatusCancelled, reason, chainID, chainID); err != nil {
		return err
	}
	chainQuery := `UPDATE chains SET status=?, error=?, desired_revision_id=0,
		updated_at=CURRENT_TIMESTAMP WHERE id=?`
	chainArgs := []any{ChainStatusInvalid, reason, chainID}
	if len(fromStatus) > 0 {
		chainQuery += ` AND status=?`
		chainArgs = append(chainArgs, fromStatus[0])
	}
	result, err := tx.ExecContext(ctx, chainQuery, chainArgs...)
	if err != nil {
		return err
	}
	if len(fromStatus) > 0 {
		if n, nerr := result.RowsAffected(); nerr != nil {
			return nerr
		} else if n == 0 {
			return fmt.Errorf("%w: chain %d not in status %s", ErrChainStatusChanged, chainID, fromStatus[0])
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM chain_hops WHERE chain_id=?`, chainID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE chain_hop_identities
		SET archived_at=COALESCE(archived_at, CURRENT_TIMESTAMP) WHERE chain_id=?`, chainID); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) UpdateChainRevision(ctx context.Context, revisionID int64, status, errorMessage string, snapshot ChainRevisionSnapshot) error {
	raw, err := json.Marshal(snapshot)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `UPDATE chain_revisions SET status=?, error=?, snapshot=? WHERE id=?`,
		status, errorMessage, string(raw), revisionID)
	return err
}

func (s *Store) SetChainRevisionStatus(ctx context.Context, revisionID int64, status, errorMessage string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE chain_revisions SET status=?, error=? WHERE id=?`, status, errorMessage, revisionID)
	return err
}

// AbandonChainRevision 废弃被新编辑取代的失败 revision（§21 失败后编辑）：
// 未投递/在途的任务与命令置 abandoned（不再送达 Agent 产生过期配置），
// revision 置 cancelled；acked/failed 等终态不受影响。
func (s *Store) AbandonChainRevision(ctx context.Context, revisionID int64, reason string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `UPDATE commands SET status=?, error=?, updated_at=CURRENT_TIMESTAMP
		WHERE id IN (SELECT command_id FROM chain_revision_tasks WHERE revision_id=?) AND status IN (?, ?)`,
		CommandStatusAbandoned, reason, revisionID, CommandStatusQueued, CommandStatusSent); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE chain_revision_tasks SET status=?, error=?, updated_at=CURRENT_TIMESTAMP
		WHERE revision_id=? AND status IN (?, ?)`,
		RevisionTaskAbandoned, reason, revisionID, RevisionTaskPending, RevisionTaskQueued); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE chain_revisions SET status=?, error=? WHERE id=? AND status IN (?, ?)`,
		RevisionStatusCancelled, reason, revisionID, RevisionStatusFailed, RevisionStatusActiveFailed); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) AddRevisionTask(ctx context.Context, task ChainRevisionTask) (int64, error) {
	res, err := s.db.ExecContext(ctx, `INSERT INTO chain_revision_tasks
		(revision_id, task_key, phase, action, kind, hop_id, server_id, status)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(revision_id, task_key) DO NOTHING`, task.RevisionID, task.TaskKey, task.Phase,
		task.Action, task.Kind, task.HopID, task.ServerID, RevisionTaskPending)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) RevisionTasks(ctx context.Context, revisionID int64) ([]ChainRevisionTask, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, revision_id, task_key, phase, action, kind, hop_id,
		server_id, status, command_id, error FROM chain_revision_tasks WHERE revision_id=? ORDER BY id`, revisionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ChainRevisionTask
	for rows.Next() {
		var task ChainRevisionTask
		if err := rows.Scan(&task.ID, &task.RevisionID, &task.TaskKey, &task.Phase, &task.Action, &task.Kind,
			&task.HopID, &task.ServerID, &task.Status, &task.CommandID, &task.Error); err != nil {
			return nil, err
		}
		out = append(out, task)
	}
	return out, rows.Err()
}

func (s *Store) LinkRevisionTaskCommand(ctx context.Context, taskID, commandID int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE chain_revision_tasks SET command_id=?, status=?, updated_at=CURRENT_TIMESTAMP WHERE id=?`,
		commandID, RevisionTaskQueued, taskID)
	return err
}

func (s *Store) LinkRevisionTaskByKey(ctx context.Context, revisionID int64, taskKey string, commandID int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE chain_revision_tasks SET command_id=?, status=?, updated_at=CURRENT_TIMESTAMP
		WHERE revision_id=? AND task_key=?`, commandID, RevisionTaskQueued, revisionID, taskKey)
	return err
}

// EnqueueRevisionTaskCommand atomically persists a command and links it to its
// revision task before the dispatcher can deliver it to an Agent.
func (s *Store) EnqueueRevisionTaskCommand(ctx context.Context, requestID, traceID string, serverID int64,
	typ string, data json.RawMessage, revisionID int64, taskKey string) (int64, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx,
		`INSERT INTO commands (request_id, trace_id, server_id, type, data) VALUES (?, ?, ?, ?, ?)`,
		requestID, traceID, serverID, typ, string(data))
	if err != nil {
		return 0, fmt.Errorf("enqueue revision command: %w", err)
	}
	commandID, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	result, err := tx.ExecContext(ctx, `UPDATE chain_revision_tasks
		SET command_id=?, status=?, updated_at=CURRENT_TIMESTAMP
		WHERE revision_id=? AND task_key=? AND status=?`, commandID, RevisionTaskQueued,
		revisionID, taskKey, RevisionTaskPending)
	if err != nil {
		return 0, err
	}
	if rows, err := result.RowsAffected(); err != nil || rows != 1 {
		if err != nil {
			return 0, err
		}
		return 0, fmt.Errorf("revision task %s is missing or not pending", taskKey)
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return commandID, nil
}

func (s *Store) RevisionTaskByCommandID(ctx context.Context, commandID int64) (*ChainRevisionTask, error) {
	var task ChainRevisionTask
	err := s.db.QueryRowContext(ctx, `SELECT id, revision_id, task_key, phase, action, kind, hop_id,
		server_id, status, command_id, error FROM chain_revision_tasks WHERE command_id=?`, commandID).
		Scan(&task.ID, &task.RevisionID, &task.TaskKey, &task.Phase, &task.Action, &task.Kind,
			&task.HopID, &task.ServerID, &task.Status, &task.CommandID, &task.Error)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &task, err
}

// SetRevisionTaskResult 回写任务终态（acked/failed）。仅 pending/queued 可转入：
// abandoned 任务（编辑取代/删除级联）不得被迟到回执或死信回写翻回。
func (s *Store) SetRevisionTaskResult(ctx context.Context, taskID int64, success bool, errorMessage string) error {
	status := RevisionTaskAcked
	if !success {
		status = RevisionTaskFailed
	}
	result, err := s.db.ExecContext(ctx, `UPDATE chain_revision_tasks SET status=?, error=?, updated_at=CURRENT_TIMESTAMP
		WHERE id=? AND status IN (?, ?)`,
		status, errorMessage, taskID, RevisionTaskPending, RevisionTaskQueued)
	if err != nil {
		return err
	}
	if n, nerr := result.RowsAffected(); nerr != nil {
		return nerr
	} else if n == 0 {
		return fmt.Errorf("%w: revision task %d cannot transition to %s", ErrStateTransition, taskID, status)
	}
	return nil
}

func (s *Store) ResetFailedRevisionTasks(ctx context.Context, revisionID int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE chain_revision_tasks SET status=?, command_id=0, error='',
		updated_at=CURRENT_TIMESTAMP WHERE revision_id=? AND phase='apply' AND status=?`,
		RevisionTaskPending, revisionID, RevisionTaskFailed)
	return err
}

// PublishChainRevision 发布 revision 并在同一事务内更新链状态（普通发布 → active，
// 强制发布 → active_unconfirmed）。可选 fromStatus 参数开启 CAS 守卫：链必须仍处于
// 调用方（链状态机）已校验的状态，否则整体回滚并返回 ErrChainStatusChanged。
func (s *Store) PublishChainRevision(ctx context.Context, revisionID int64, forced bool, fromStatus ...string) error {
	revision, err := s.ChainRevisionByID(ctx, revisionID)
	if err != nil {
		return err
	}
	status := RevisionStatusActive
	chainStatus := ChainStatusActive
	if forced {
		status = RevisionStatusActiveUnconfirmed
		chainStatus = ChainStatusActiveUnconfirmed
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `UPDATE chain_revisions SET status=?, forced=?, published_at=CURRENT_TIMESTAMP WHERE id=?`,
		status, forced, revisionID); err != nil {
		return err
	}
	desiredRevisionID := int64(0)
	if forced {
		desiredRevisionID = revisionID
	}
	chainQuery := `UPDATE chains SET name=?, service_node_id=?, endpoint_id=?, service_uuid=?, traffic_multiplier_milli=?,
		published_revision_id=?, desired_revision_id=?, status=?, error='', updated_at=CURRENT_TIMESTAMP WHERE id=?`
	chainArgs := []any{
		revision.Snapshot.Name, revision.Snapshot.ServiceNodeID, revision.Snapshot.EndpointID,
		revision.Snapshot.ServiceUUID, revision.Snapshot.TrafficMultiplierMilli,
		revisionID, desiredRevisionID, chainStatus, revision.ChainID,
	}
	if len(fromStatus) > 0 {
		chainQuery += ` AND status=?`
		chainArgs = append(chainArgs, fromStatus[0])
	}
	result, err := tx.ExecContext(ctx, chainQuery, chainArgs...)
	if err != nil {
		return err
	}
	if len(fromStatus) > 0 {
		if n, nerr := result.RowsAffected(); nerr != nil {
			return nerr
		} else if n == 0 {
			return fmt.Errorf("%w: chain %d not in status %s", ErrChainStatusChanged, revision.ChainID, fromStatus[0])
		}
	}
	return tx.Commit()
}

func (s *Store) RevisionByCommandID(ctx context.Context, commandID int64) (*ChainRevision, *ChainRevisionTask, error) {
	task, err := s.RevisionTaskByCommandID(ctx, commandID)
	if err != nil {
		return nil, nil, err
	}
	revision, err := s.ChainRevisionByID(ctx, task.RevisionID)
	return revision, task, err
}

// ReplaceWorkingChainTopology 以编辑目标 revision 整体替换链工作拓扑（hops/出口节点），
// 并在同一事务内把链状态 CAS 写入 applying。可选 fromStatus 开启守卫：仅当链仍处于
// 调用方（链状态机）已校验的编辑前置状态时写入，否则回滚并返回 ErrChainStatusChanged。
func (s *Store) ReplaceWorkingChainTopology(ctx context.Context, revision *ChainRevision, protocol string, port *int, serviceChanged bool, fromStatus ...string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM chain_hops WHERE chain_id=?`, revision.ChainID); err != nil {
		return err
	}
	for seq, hop := range revision.Snapshot.Hops {
		nodeID := int64(0)
		if seq == len(revision.Snapshot.Hops)-1 {
			nodeID = revision.Snapshot.ServiceNodeID
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO chain_hops
			(id, chain_id, seq, server_id, role, node_id, status, error, forward_port,
			 portal_port, portal_public_key, portal_server_name, tunnel_uuid)
			VALUES (?, ?, ?, ?, ?, ?, ?, '', ?, ?, ?, ?, ?)`, hop.HopID, revision.ChainID, seq,
			hop.ServerID, hop.Role, nodeID, HopStatusPending, hop.ForwardPort, hop.PortalPort,
			hop.PortalPublicKey, hop.PortalServerName, hop.TunnelUUID); err != nil {
			return fmt.Errorf("insert desired chain hop: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE chain_hop_identities SET archived_at=NULL WHERE id=?`, hop.HopID); err != nil {
			return fmt.Errorf("restore chain hop identity: %w", err)
		}
	}
	nodeStatus := NodeStatusActive
	if serviceChanged {
		nodeStatus = NodeStatusPending
	}
	if _, err := tx.ExecContext(ctx, `UPDATE nodes SET name=?, server_id=?, protocol=?, port=?, config_template=?,
		status=?, error=NULL WHERE id=?`, revision.Snapshot.Name, revision.Snapshot.ServiceServerID, protocol,
		port, string(revision.Snapshot.ServiceConfig), nodeStatus, revision.Snapshot.ServiceNodeID); err != nil {
		return err
	}
	chainQuery := `UPDATE chains SET name=?, status=?, error='', updated_at=CURRENT_TIMESTAMP WHERE id=?`
	chainArgs := []any{revision.Snapshot.Name, ChainStatusApplying, revision.ChainID}
	if len(fromStatus) > 0 {
		chainQuery += ` AND status=?`
		chainArgs = append(chainArgs, fromStatus[0])
	}
	result, err := tx.ExecContext(ctx, chainQuery, chainArgs...)
	if err != nil {
		return err
	}
	if len(fromStatus) > 0 {
		if n, nerr := result.RowsAffected(); nerr != nil {
			return nerr
		} else if n == 0 {
			return fmt.Errorf("%w: chain %d not in status %s", ErrChainStatusChanged, revision.ChainID, fromStatus[0])
		}
	}
	return tx.Commit()
}

func (s *Store) NextChainHopID(ctx context.Context, chainID, serverID int64) (int64, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var next int64
	if err := tx.QueryRowContext(ctx, `SELECT MAX(value)+1 FROM (
		SELECT COALESCE(MAX(id),0) AS value FROM chain_hops
		UNION ALL SELECT COALESCE(MAX(id),0) FROM chain_hop_identities)`).Scan(&next); err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO chain_hop_identities (id, chain_id, server_id) VALUES (?, ?, ?)`, next, chainID, serverID); err != nil {
		return 0, err
	}
	return next, tx.Commit()
}

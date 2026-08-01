package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
)

const (
	EndpointStatusPending  = "pending"
	EndpointStatusApplying = "applying"
	EndpointStatusActive   = "active"
	EndpointStatusFailed   = "failed"
)

var ErrEndpointConflict = errors.New("shared endpoint port conflicts with an incompatible managed listener")

type SharedEndpoint struct {
	ID             int64
	ServerID       int64
	Protocol       string
	Port           int
	ProfileHash    string
	ConfigTemplate json.RawMessage
	RealizedConfig json.RawMessage
	Status         string
	Error          string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

const endpointCols = `id, server_id, protocol, port, profile_hash, config_template,
	realized_config, status, error, created_at, updated_at`

func scanEndpoint(row interface{ Scan(...any) error }) (*SharedEndpoint, error) {
	var endpoint SharedEndpoint
	var template string
	var realized sql.NullString
	if err := row.Scan(&endpoint.ID, &endpoint.ServerID, &endpoint.Protocol, &endpoint.Port,
		&endpoint.ProfileHash, &template, &realized, &endpoint.Status, &endpoint.Error,
		&endpoint.CreatedAt, &endpoint.UpdatedAt); err != nil {
		return nil, err
	}
	endpoint.ConfigTemplate = json.RawMessage(template)
	if realized.Valid {
		endpoint.RealizedConfig = json.RawMessage(realized.String)
	}
	return &endpoint, nil
}

func (s *Store) SharedEndpointByID(ctx context.Context, id int64) (*SharedEndpoint, error) {
	endpoint, err := scanEndpoint(s.db.QueryRowContext(ctx,
		`SELECT `+endpointCols+` FROM shared_endpoints WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return endpoint, err
}

// EnsureSharedEndpoint reuses a compatible managed listener. A selected port
// occupied by a different managed profile is a conflict; an unmanaged OS
// listener is detected later by the Agent's bind probe.
func (s *Store) EnsureSharedEndpoint(ctx context.Context, serverID int64, protocol string, port int,
	profileHash string, config json.RawMessage) (*SharedEndpoint, bool, error) {
	if serverID <= 0 || profileHash == "" || !json.Valid(config) {
		return nil, false, fmt.Errorf("invalid shared endpoint")
	}
	if port > 0 {
		rows, err := s.db.QueryContext(ctx, `SELECT `+endpointCols+`
			FROM shared_endpoints WHERE server_id=? AND port=? ORDER BY id`, serverID, port)
		if err != nil {
			return nil, false, err
		}
		defer rows.Close()
		for rows.Next() {
			endpoint, err := scanEndpoint(rows)
			if err != nil {
				return nil, false, err
			}
			if endpoint.ProfileHash != profileHash || endpoint.Protocol != protocol {
				return nil, false, ErrEndpointConflict
			}
			return endpoint, false, nil
		}
	}
	if port == 0 {
		endpoint, err := scanEndpoint(s.db.QueryRowContext(ctx, `SELECT `+endpointCols+`
			FROM shared_endpoints WHERE server_id=? AND profile_hash=? AND protocol=? ORDER BY
			CASE status WHEN 'active' THEN 0 WHEN 'applying' THEN 1 ELSE 2 END, id LIMIT 1`,
			serverID, profileHash, protocol))
		if err == nil {
			return endpoint, false, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return nil, false, err
		}
	}
	result, err := s.db.ExecContext(ctx, `INSERT INTO shared_endpoints
		(server_id, protocol, port, profile_hash, config_template) VALUES (?, ?, ?, ?, ?)`,
		serverID, protocol, port, profileHash, string(config))
	if err != nil {
		return nil, false, err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return nil, false, err
	}
	endpoint, err := s.SharedEndpointByID(ctx, id)
	return endpoint, true, err
}

func (s *Store) SetSharedEndpointApplying(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE shared_endpoints SET status=?, error='',
		updated_at=CURRENT_TIMESTAMP WHERE id=?`, EndpointStatusApplying, id)
	return err
}

func (s *Store) SetSharedEndpointActive(ctx context.Context, id int64, realized json.RawMessage) error {
	var value struct {
		Port int `json:"port"`
	}
	if err := json.Unmarshal(realized, &value); err != nil || value.Port <= 0 {
		return fmt.Errorf("invalid shared endpoint realized config")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var serverID int64
	if err := tx.QueryRowContext(ctx, `SELECT server_id FROM shared_endpoints WHERE id=?`, id).Scan(&serverID); err != nil {
		return err
	}
	var conflictID int64
	err = tx.QueryRowContext(ctx, `SELECT id FROM shared_endpoints
		WHERE server_id=? AND port=? AND id<>? LIMIT 1`, serverID, value.Port, id).Scan(&conflictID)
	if err == nil {
		return fmt.Errorf("%w: endpoint %d already owns port %d", ErrEndpointConflict, conflictID, value.Port)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE shared_endpoints SET port=?, realized_config=?, status=?,
		error='', updated_at=CURRENT_TIMESTAMP WHERE id=?`, value.Port, string(realized), EndpointStatusActive, id); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) SetSharedEndpointFailed(ctx context.Context, id int64, message string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE shared_endpoints SET status=?, error=?,
		updated_at=CURRENT_TIMESTAMP WHERE id=?`, EndpointStatusFailed, message, id)
	return err
}

// SetSharedEndpointPending 将端点重置为 pending 并清除 realized 配置
// （最后一条路由链删除后下发 remove 命令时使用；端点记录保留供后续复用）。
func (s *Store) SetSharedEndpointPending(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE shared_endpoints SET status=?, realized_config=NULL,
		error='', updated_at=CURRENT_TIMESTAMP WHERE id=?`, EndpointStatusPending, id)
	return err
}

type UserChainAssignment struct {
	ID         int64
	UserID     int64
	ChainID    int64
	EndpointID int64
	AccessUUID string
	CreatedAt  time.Time
}

func (s *Store) UserChainAssignments(ctx context.Context, userID int64) ([]UserChainAssignment, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT a.id, a.user_id, a.chain_id, c.endpoint_id,
		a.access_uuid, a.created_at FROM user_chain_assignments a
		JOIN chains c ON c.id=a.chain_id WHERE a.user_id=? AND c.deleted_at IS NULL ORDER BY a.chain_id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []UserChainAssignment
	for rows.Next() {
		var item UserChainAssignment
		if err := rows.Scan(&item.ID, &item.UserID, &item.ChainID, &item.EndpointID,
			&item.AccessUUID, &item.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

// ActiveEndpointAssignments returns every active client routed by one shared
// endpoint in a single query. This is the hot path for complete endpoint
// reconciliation, so it must not scale queries with the number of users.
func (s *Store) ActiveEndpointAssignments(ctx context.Context, endpointID int64) ([]UserChainAssignment, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT a.id, a.user_id, a.chain_id, c.endpoint_id,
		a.access_uuid, a.created_at FROM user_chain_assignments a
		JOIN chains c ON c.id=a.chain_id
		JOIN users u ON u.id=a.user_id
		WHERE c.endpoint_id=? AND c.deleted_at IS NULL AND u.expired=0 AND u.disabled=0
		ORDER BY a.chain_id, a.id`, endpointID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []UserChainAssignment{}
	for rows.Next() {
		var item UserChainAssignment
		if err := rows.Scan(&item.ID, &item.UserID, &item.ChainID, &item.EndpointID,
			&item.AccessUUID, &item.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *Store) UserChainIDs(ctx context.Context, userID int64) ([]int64, error) {
	assignments, err := s.UserChainAssignments(ctx, userID)
	if err != nil {
		return nil, err
	}
	ids := make([]int64, 0, len(assignments))
	for _, assignment := range assignments {
		ids = append(ids, assignment.ChainID)
	}
	return ids, nil
}

// ValidateAssignableChains verifies the complete requested set before the
// panel changes any legacy node assignments in the same request.
func (s *Store) ValidateAssignableChains(ctx context.Context, chainIDs []int64) error {
	seen := make(map[int64]bool, len(chainIDs))
	for _, chainID := range chainIDs {
		if seen[chainID] {
			continue
		}
		seen[chainID] = true
		var endpointID int64
		if err := s.db.QueryRowContext(ctx, `SELECT endpoint_id FROM chains
			WHERE id=? AND deleted_at IS NULL`, chainID).Scan(&endpointID); err != nil {
			return fmt.Errorf("chain %d is not assignable: %w", chainID, err)
		}
		if endpointID == 0 {
			return fmt.Errorf("chain %d has no shared endpoint", chainID)
		}
	}
	return nil
}

// SetUserChains replaces direct user-to-chain assignments while preserving
// access UUIDs for unchanged rows.
func (s *Store) SetUserChains(ctx context.Context, userID int64, chainIDs []int64) (added, removed []UserChainAssignment, err error) {
	if err := s.ValidateAssignableChains(ctx, chainIDs); err != nil {
		return nil, nil, err
	}
	current, err := s.UserChainAssignments(ctx, userID)
	if err != nil {
		return nil, nil, err
	}
	want := make(map[int64]bool, len(chainIDs))
	for _, id := range chainIDs {
		want[id] = true
	}
	have := make(map[int64]UserChainAssignment, len(current))
	for _, assignment := range current {
		have[assignment.ChainID] = assignment
		if !want[assignment.ChainID] {
			removed = append(removed, assignment)
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, nil, err
	}
	defer tx.Rollback()
	for _, assignment := range removed {
		if _, err := tx.ExecContext(ctx, `DELETE FROM user_chain_assignments WHERE id=?`, assignment.ID); err != nil {
			return nil, nil, err
		}
	}
	for chainID := range want {
		if _, ok := have[chainID]; ok {
			continue
		}
		var endpointID int64
		if err := tx.QueryRowContext(ctx, `SELECT endpoint_id FROM chains
			WHERE id=? AND deleted_at IS NULL`, chainID).Scan(&endpointID); err != nil {
			return nil, nil, fmt.Errorf("chain %d is not assignable: %w", chainID, err)
		}
		accessUUID := uuid.NewString()
		result, err := tx.ExecContext(ctx, `INSERT INTO user_chain_assignments
			(user_id, chain_id, access_uuid) VALUES (?, ?, ?)`, userID, chainID, accessUUID)
		if err != nil {
			return nil, nil, err
		}
		id, _ := result.LastInsertId()
		added = append(added, UserChainAssignment{ID: id, UserID: userID, ChainID: chainID,
			EndpointID: endpointID, AccessUUID: accessUUID})
	}
	if err := tx.Commit(); err != nil {
		return nil, nil, err
	}
	sort.Slice(added, func(i, j int) bool { return added[i].ChainID < added[j].ChainID })
	return added, removed, nil
}

// SharedEndpointsByServer 返回指定服务器上的全部共享端点（端口段收窄越界校验用）。
func (s *Store) SharedEndpointsByServer(ctx context.Context, serverID int64) ([]SharedEndpoint, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+endpointCols+` FROM shared_endpoints WHERE server_id=? ORDER BY id`, serverID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var endpoints []SharedEndpoint
	for rows.Next() {
		ep, err := scanEndpoint(rows)
		if err != nil {
			return nil, err
		}
		endpoints = append(endpoints, *ep)
	}
	return endpoints, rows.Err()
}

// NonActiveEndpointsByServer 返回指定服务器上状态非 active 的共享端点（自愈补发用）。
func (s *Store) NonActiveEndpointsByServer(ctx context.Context, serverID int64) ([]SharedEndpoint, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+endpointCols+` FROM shared_endpoints WHERE server_id=? AND status<>?`, serverID, EndpointStatusActive)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var endpoints []SharedEndpoint
	for rows.Next() {
		ep, err := scanEndpoint(rows)
		if err != nil {
			return nil, err
		}
		endpoints = append(endpoints, *ep)
	}
	return endpoints, rows.Err()
}

// ChainIDsByEndpoint 返回使用指定共享端点的链 ID 列表（端点→链状态联动用）。
func (s *Store) ChainIDsByEndpoint(ctx context.Context, endpointID int64) ([]int64, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id FROM chains WHERE endpoint_id=? AND status IN (?, ?)`,
		endpointID, ChainStatusActive, ChainStatusDegraded)
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

func (s *Store) SharedEndpointIDsForAssignments(assignments ...[]UserChainAssignment) []int64 {
	set := map[int64]bool{}
	for _, list := range assignments {
		for _, assignment := range list {
			if assignment.EndpointID > 0 {
				set[assignment.EndpointID] = true
			}
		}
	}
	ids := make([]int64, 0, len(set))
	for id := range set {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

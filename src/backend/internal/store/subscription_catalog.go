package store

import (
	"context"
	"database/sql"
	"time"
)

const catalogChain = `c.deleted_at IS NULL AND c.status NOT IN ('invalid', 'deleted')`
const activeCatalogUser = `u.expired=0 AND u.disabled=0 AND u.quota_exhausted=0
	AND (u.expires_at IS NULL OR u.expires_at>?)
	AND (u.effective_at IS NULL OR u.effective_at<=?)`

// Preserve both types of legacy credentials while routing the published
// revision, including while a replacement endpoint is being prepared.
const catalogChainAssignments = `
	SELECT u.id, u.uuid, c.id, COALESCE(json_extract(r.snapshot,'$.endpoint_id'),0),
		COALESCE(a.id, 0), COALESCE(a.access_uuid, '')
	FROM users u CROSS JOIN chains c
	JOIN chain_revisions r ON r.id=c.published_revision_id
	LEFT JOIN user_chain_assignments a ON a.user_id=u.id AND a.chain_id=c.id
		AND NOT EXISTS (
			SELECT 1 FROM subscription_group_credentials old WHERE old.user_id=u.id AND old.chain_id=c.id
		)
	WHERE ` + catalogChain

func (s *Store) catalogChainAssignments(ctx context.Context, predicate string, args ...any) ([]UserChainAssignment, error) {
	rows, err := s.db.QueryContext(ctx, catalogChainAssignments+predicate+` ORDER BY c.id, u.id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var assignments []UserChainAssignment
	for rows.Next() {
		var a UserChainAssignment
		if err := rows.Scan(&a.UserID, &a.UserUUID, &a.ChainID, &a.EndpointID, &a.ID, &a.AccessUUID); err != nil {
			return nil, err
		}
		if a.ID == 0 {
			a.AccessUUID = GroupAccessUUID(a.UserUUID, a.ChainID)
		}
		assignments = append(assignments, a)
	}
	return assignments, rows.Err()
}

func (s *Store) CatalogUserChainAssignments(ctx context.Context, userID int64) ([]UserChainAssignment, error) {
	return s.catalogChainAssignments(ctx, ` AND u.id=?`, userID)
}

func (s *Store) CatalogEndpointAssignments(ctx context.Context, endpointID int64, now time.Time) ([]UserChainAssignment, error) {
	return s.catalogChainAssignments(ctx, ` AND json_extract(r.snapshot,'$.endpoint_id')=? AND `+activeCatalogUser,
		endpointID, now.Unix(), now.Unix())
}

func (s *Store) CatalogServiceEndpointUsers(ctx context.Context, endpointID int64, now time.Time) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT u.uuid FROM users u
		WHERE `+activeCatalogUser+`
		AND EXISTS (SELECT 1 FROM chains c WHERE `+catalogChain+` AND (
			(c.service_endpoint_id=? AND c.endpoint_id=0)
			OR EXISTS (SELECT 1 FROM chain_revisions r WHERE r.id=c.published_revision_id
				AND json_extract(r.snapshot,'$.service_endpoint_id')=?
				AND COALESCE(json_extract(r.snapshot,'$.endpoint_id'),0)=0)
		)) ORDER BY u.uuid`, now.Unix(), now.Unix(), endpointID, endpointID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var users []string
	for rows.Next() {
		var uuid string
		if err := rows.Scan(&uuid); err != nil {
			return nil, err
		}
		users = append(users, uuid)
	}
	return users, rows.Err()
}

const catalogNodePredicate = catalogChain + `
	AND (
		(c.service_node_id=n.id AND c.endpoint_id=0 AND c.service_endpoint_id=0)
		OR EXISTS (
			SELECT 1 FROM chain_revisions r WHERE r.id=c.published_revision_id
			AND json_extract(r.snapshot,'$.service_node_id')=n.id
			AND COALESCE(json_extract(r.snapshot,'$.endpoint_id'),0)=0
			AND COALESCE(json_extract(r.snapshot,'$.service_endpoint_id'),0)=0
		)
	)`

// CatalogUserNodeIDs includes standalone assignments and ordinary chain exits.
// Shared ingress and service endpoints use their own credential reconciliation.
func (s *Store) CatalogUserNodeIDs(ctx context.Context, userID int64) ([]int64, error) {
	return querySubscriptionUserIDs(ctx, s.db, `SELECT n.id FROM nodes n
		WHERE EXISTS (SELECT 1 FROM user_nodes a WHERE a.node_id=n.id AND a.user_id=?)
		OR EXISTS (SELECT 1 FROM chains c WHERE `+catalogNodePredicate+`) ORDER BY n.id`, userID)
}

func (s *Store) PublishedCatalogNodeIDsForServer(ctx context.Context, serverID int64) ([]int64, error) {
	return querySubscriptionUserIDs(ctx, s.db, `SELECT n.id FROM nodes n WHERE n.server_id=? AND n.status=?
		AND EXISTS (SELECT 1 FROM chains c WHERE c.published_revision_id<>0 AND `+catalogNodePredicate+`)
		ORDER BY n.id`, serverID, NodeStatusActive)
}

func (s *Store) CatalogNodeUserUUIDs(ctx context.Context, nodeID int64, now time.Time) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT u.uuid FROM users u
		WHERE `+activeCatalogUser+`
		AND EXISTS (SELECT 1 FROM nodes n WHERE n.id=? AND (
			EXISTS (SELECT 1 FROM user_nodes a WHERE a.node_id=n.id AND a.user_id=u.id)
			OR EXISTS (SELECT 1 FROM chains c WHERE `+catalogNodePredicate+`)))
		ORDER BY u.id`, now.Unix(), now.Unix(), nodeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var users []string
	for rows.Next() {
		var uuid string
		if err := rows.Scan(&uuid); err != nil {
			return nil, err
		}
		users = append(users, uuid)
	}
	return users, rows.Err()
}

func (s *Store) CatalogUserIDs(ctx context.Context) ([]int64, error) {
	return querySubscriptionUserIDs(ctx, s.db, `SELECT id FROM users ORDER BY id`)
}

// External sources are available to every user. Legacy attachment modes retain
// their existing traffic accounting. New sources contribute nodes by default.
func (s *Store) CatalogExternalSubscriptions(ctx context.Context, userID int64) ([]UserExternalSubscriptionJoined, error) {
	sources, err := s.ListExternalSubscriptions(ctx)
	if err != nil {
		return nil, err
	}
	direct, err := s.ListUserExternalSubscriptions(ctx, userID)
	if err != nil {
		return nil, err
	}
	modes := make(map[int64]string, len(direct))
	for _, item := range direct {
		modes[item.SubscriptionID] = item.Mode
	}
	out := make([]UserExternalSubscriptionJoined, 0, len(sources))
	for _, source := range sources {
		mode := modes[source.ID]
		if mode == "" {
			mode = ExtSubModeNodes
		}
		out = append(out, UserExternalSubscriptionJoined{
			UserID: userID, SubscriptionID: source.ID, Mode: mode,
			Name: source.Name, Upload: source.Upload, Download: source.Download,
			Total: source.Total, Expire: source.Expire, NodeCount: source.NodeCount,
		})
	}
	return out, nil
}

// Freeze the credentials and traffic modes already assigned by legacy groups.
// Catalog availability thereafter depends only on the resource itself.
func migrateSubscriptionCatalogAccess(tx *sql.Tx) error {
	if _, err := tx.Exec(`CREATE TABLE IF NOT EXISTS subscription_group_credentials (
		user_id INTEGER NOT NULL REFERENCES users(id),
		chain_id INTEGER NOT NULL REFERENCES chains(id),
		PRIMARY KEY (user_id, chain_id)
	)`); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT OR IGNORE INTO subscription_group_credentials(user_id,chain_id)
		SELECT gm.user_id,gc.chain_id FROM user_group_members gm
		JOIN user_group_links gl ON gl.user_group_id=gm.user_group_id
		JOIN link_group_chains gc ON gc.group_id=gl.link_group_id
		JOIN user_chain_assignments a ON a.user_id=gm.user_id AND a.chain_id=gc.chain_id`); err != nil {
		return err
	}
	_, err := tx.Exec(`INSERT INTO user_external_subscriptions(user_id,subscription_id,mode)
		SELECT user_id,subscription_id,mode FROM (
			SELECT gm.user_id,ge.subscription_id,ge.mode,
				ROW_NUMBER() OVER (PARTITION BY gm.user_id,ge.subscription_id ORDER BY ge.group_id) AS priority
			FROM user_group_members gm
			JOIN user_group_links gl ON gl.user_group_id=gm.user_group_id
			JOIN link_group_external_subscriptions ge ON ge.group_id=gl.link_group_id
			JOIN external_subscriptions es ON es.id=ge.subscription_id
		) WHERE priority=1
		ON CONFLICT(user_id,subscription_id) DO UPDATE SET mode=excluded.mode`)
	return err
}

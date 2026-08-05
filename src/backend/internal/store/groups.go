package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// groupUUIDNamespace 是分组派生链路访问凭据的 UUIDv5 命名空间（固定值）。
var groupUUIDNamespace = uuid.MustParse("e1d8b3c4-9c6e-4a5b-8f2d-1a2b3c4d5e6f")

// GroupAccessUUID 返回分组派生链路（共享入口）的确定性访问凭据：同一用户+链路恒定，
// 分组内容调整不改变客户端凭据；与 user_chain_assignments 的随机 access_uuid 同构。
func GroupAccessUUID(userUUID string, chainID int64) string {
	return uuid.NewSHA1(groupUUIDNamespace, []byte(fmt.Sprintf("%s:%d", userUUID, chainID))).String()
}

// LinkGroupExternalSubscription 是链路分组内的一条外部订阅（含模式）。
type LinkGroupExternalSubscription struct {
	SubscriptionID int64  `json:"subscription_id"`
	Mode           string `json:"mode"`
}

// LinkGroup 是一个链路分组的完整视图（JSON 键为前端契约）。
type LinkGroup struct {
	ID                    int64                            `json:"id"`
	Name                  string                           `json:"name"`
	ChainIDs              []int64                          `json:"chain_ids"`
	ExternalSubscriptions []LinkGroupExternalSubscription  `json:"external_subscriptions"`
	ChainCount            int                              `json:"chain_count"`
	ExtSubCount           int                              `json:"external_subscription_count"`
	UserGroupNames        []string                         `json:"user_group_names"`
	CreatedAt             time.Time                        `json:"created_at"`
	UpdatedAt             time.Time                        `json:"updated_at"`
}

// UserGroup 是一个用户分组的完整视图（JSON 键为前端契约）。
type UserGroup struct {
	ID             int64     `json:"id"`
	Name           string    `json:"name"`
	UserIDs        []int64   `json:"user_ids"`
	LinkGroupIDs   []int64   `json:"link_group_ids"`
	MemberCount    int       `json:"member_count"`
	LinkGroupCount int       `json:"link_group_count"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// CreateLinkGroup 创建链路分组；name 重复返回错误。
func (s *Store) CreateLinkGroup(ctx context.Context, name string, chainIDs []int64, extSubs []LinkGroupExternalSubscription) (int64, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin create link group: %w", err)
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `INSERT INTO link_groups (name) VALUES (?)`, name)
	if err != nil {
		return 0, fmt.Errorf("insert link group: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	if err := insertLinkGroupMembers(ctx, tx, id, chainIDs, extSubs); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit create link group: %w", err)
	}
	return id, nil
}

// UpdateLinkGroup 整表替换链路分组（名称 + 链路 + 外部订阅）。
func (s *Store) UpdateLinkGroup(ctx context.Context, id int64, name string, chainIDs []int64, extSubs []LinkGroupExternalSubscription) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin update link group: %w", err)
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `UPDATE link_groups SET name=?, updated_at=CURRENT_TIMESTAMP WHERE id=?`, name, id)
	if err != nil {
		return fmt.Errorf("update link group name: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	if err := insertLinkGroupMembers(ctx, tx, id, chainIDs, extSubs); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit update link group: %w", err)
	}
	return nil
}

// insertLinkGroupMembers 先清空再写入链路分组成员（同一事务内）。
func insertLinkGroupMembers(ctx context.Context, tx *sql.Tx, groupID int64, chainIDs []int64, extSubs []LinkGroupExternalSubscription) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM link_group_chains WHERE group_id=?`, groupID); err != nil {
		return fmt.Errorf("clear link group chains: %w", err)
	}
	for _, chainID := range chainIDs {
		if _, err := tx.ExecContext(ctx, `INSERT INTO link_group_chains (group_id, chain_id) VALUES (?, ?)`,
			groupID, chainID); err != nil {
			return fmt.Errorf("insert link group chain: %w", err)
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM link_group_external_subscriptions WHERE group_id=?`, groupID); err != nil {
		return fmt.Errorf("clear link group external subscriptions: %w", err)
	}
	for _, sub := range extSubs {
		if _, err := tx.ExecContext(ctx, `INSERT INTO link_group_external_subscriptions
			(group_id, subscription_id, mode) VALUES (?, ?, ?)`,
			groupID, sub.SubscriptionID, sub.Mode); err != nil {
			return fmt.Errorf("insert link group external subscription: %w", err)
		}
	}
	return nil
}

// DeleteLinkGroup 删除链路分组并级联清理成员表（SQLite 未开启 PRAGMA foreign_keys，
// 与外键文档声明无关，必须手工级联：link_group_chains / link_group_external_subscriptions /
// user_group_links）。
func (s *Store) DeleteLinkGroup(ctx context.Context, id int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin delete link group: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM link_group_chains WHERE group_id=?`, id); err != nil {
		return fmt.Errorf("delete link group chains: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM link_group_external_subscriptions WHERE group_id=?`, id); err != nil {
		return fmt.Errorf("delete link group external subscriptions: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM user_group_links WHERE link_group_id=?`, id); err != nil {
		return fmt.Errorf("delete user group links: %w", err)
	}
	res, err := tx.ExecContext(ctx, `DELETE FROM link_groups WHERE id=?`, id)
	if err != nil {
		return fmt.Errorf("delete link group: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return tx.Commit()
}

// ListLinkGroups 返回全部链路分组（含成员与引用它的用户分组名）。
func (s *Store) ListLinkGroups(ctx context.Context) ([]LinkGroup, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, name, created_at, updated_at FROM link_groups ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("list link groups: %w", err)
	}
	defer rows.Close()
	var groups []LinkGroup
	for rows.Next() {
		var g LinkGroup
		var created, updated string
		if err := rows.Scan(&g.ID, &g.Name, &created, &updated); err != nil {
			return nil, err
		}
		g.CreatedAt, _ = time.Parse("2006-01-02 15:04:05", created)
		g.UpdatedAt, _ = time.Parse("2006-01-02 15:04:05", updated)
		groups = append(groups, g)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range groups {
		if err := s.fillLinkGroup(ctx, &groups[i]); err != nil {
			return nil, err
		}
	}
	return groups, nil
}

// fillLinkGroup 填充成员与统计（每个分组 3 条查询；管理页低频轮询，量级可接受）。
// 软删除的链不出现在视图中（与解析侧 EffectiveUserChainAssignments 的
// deleted_at IS NULL 过滤一致）。
func (s *Store) fillLinkGroup(ctx context.Context, g *LinkGroup) error {
	g.ChainIDs = make([]int64, 0)
	g.ExternalSubscriptions = make([]LinkGroupExternalSubscription, 0)
	g.UserGroupNames = make([]string, 0)
	chainRows, err := s.db.QueryContext(ctx, `SELECT lgc.chain_id FROM link_group_chains lgc
		JOIN chains c ON c.id = lgc.chain_id
		WHERE lgc.group_id=? AND c.deleted_at IS NULL ORDER BY lgc.chain_id`, g.ID)
	if err != nil {
		return err
	}
	for chainRows.Next() {
		var id int64
		if err := chainRows.Scan(&id); err != nil {
			chainRows.Close()
			return err
		}
		g.ChainIDs = append(g.ChainIDs, id)
		g.ChainCount++
	}
	chainRows.Close()
	if err := chainRows.Err(); err != nil {
		return err
	}
	subRows, err := s.db.QueryContext(ctx, `SELECT subscription_id, mode FROM link_group_external_subscriptions WHERE group_id=? ORDER BY subscription_id`, g.ID)
	if err != nil {
		return err
	}
	for subRows.Next() {
		var sub LinkGroupExternalSubscription
		if err := subRows.Scan(&sub.SubscriptionID, &sub.Mode); err != nil {
			subRows.Close()
			return err
		}
		g.ExternalSubscriptions = append(g.ExternalSubscriptions, sub)
		g.ExtSubCount++
	}
	subRows.Close()
	if err := subRows.Err(); err != nil {
		return err
	}
	refRows, err := s.db.QueryContext(ctx, `SELECT ug.name FROM user_group_links ugl
		JOIN user_groups ug ON ug.id = ugl.user_group_id WHERE ugl.link_group_id=? ORDER BY ug.name`, g.ID)
	if err != nil {
		return err
	}
	for refRows.Next() {
		var name string
		if err := refRows.Scan(&name); err != nil {
			refRows.Close()
			return err
		}
		g.UserGroupNames = append(g.UserGroupNames, name)
	}
	refRows.Close()
	return refRows.Err()
}

func (s *Store) LinkGroupByID(ctx context.Context, id int64) (*LinkGroup, error) {
	var g LinkGroup
	var created, updated string
	err := s.db.QueryRowContext(ctx, `SELECT id, name, created_at, updated_at FROM link_groups WHERE id=?`, id).
		Scan(&g.ID, &g.Name, &created, &updated)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrNotFound
		}
		return nil, err
	}
	g.CreatedAt, _ = time.Parse("2006-01-02 15:04:05", created)
	g.UpdatedAt, _ = time.Parse("2006-01-02 15:04:05", updated)
	if err := s.fillLinkGroup(ctx, &g); err != nil {
		return nil, err
	}
	return &g, nil
}

func (s *Store) LinkGroupNameTaken(ctx context.Context, name string, excludeID int64) (bool, error) {
	var exists int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM link_groups WHERE name=? AND id != ?`, name, excludeID).
		Scan(&exists); err != nil {
		return false, err
	}
	return exists > 0, nil
}

// CreateUserGroup 创建用户分组；name 重复返回错误。
func (s *Store) CreateUserGroup(ctx context.Context, name string, userIDs, linkGroupIDs []int64) (int64, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin create user group: %w", err)
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `INSERT INTO user_groups (name) VALUES (?)`, name)
	if err != nil {
		return 0, fmt.Errorf("insert user group: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	if err := insertUserGroupMembers(ctx, tx, id, userIDs, linkGroupIDs); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit create user group: %w", err)
	}
	return id, nil
}

// UpdateUserGroup 整表替换用户分组（名称 + 成员 + 关联链路分组）。
func (s *Store) UpdateUserGroup(ctx context.Context, id int64, name string, userIDs, linkGroupIDs []int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin update user group: %w", err)
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `UPDATE user_groups SET name=?, updated_at=CURRENT_TIMESTAMP WHERE id=?`, name, id)
	if err != nil {
		return fmt.Errorf("update user group name: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	if err := insertUserGroupMembers(ctx, tx, id, userIDs, linkGroupIDs); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit update user group: %w", err)
	}
	return nil
}

// insertUserGroupMembers 先清空再写入用户分组（成员 + 关联链路分组，同一事务内）。
func insertUserGroupMembers(ctx context.Context, tx *sql.Tx, groupID int64, userIDs, linkGroupIDs []int64) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM user_group_members WHERE user_group_id=?`, groupID); err != nil {
		return fmt.Errorf("clear user group members: %w", err)
	}
	for _, userID := range userIDs {
		if _, err := tx.ExecContext(ctx, `INSERT INTO user_group_members (user_group_id, user_id) VALUES (?, ?)`,
			groupID, userID); err != nil {
			return fmt.Errorf("insert user group member: %w", err)
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM user_group_links WHERE user_group_id=?`, groupID); err != nil {
		return fmt.Errorf("clear user group links: %w", err)
	}
	for _, linkGroupID := range linkGroupIDs {
		if _, err := tx.ExecContext(ctx, `INSERT INTO user_group_links (user_group_id, link_group_id) VALUES (?, ?)`,
			groupID, linkGroupID); err != nil {
			return fmt.Errorf("insert user group link: %w", err)
		}
	}
	return nil
}

// DeleteUserGroup 删除用户分组并手工级联清理（user_group_members / user_group_links；
// 同 DeleteLinkGroup，SQLite 未开启 foreign_keys）。
func (s *Store) DeleteUserGroup(ctx context.Context, id int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin delete user group: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM user_group_members WHERE user_group_id=?`, id); err != nil {
		return fmt.Errorf("delete user group members: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM user_group_links WHERE user_group_id=?`, id); err != nil {
		return fmt.Errorf("delete user group links: %w", err)
	}
	res, err := tx.ExecContext(ctx, `DELETE FROM user_groups WHERE id=?`, id)
	if err != nil {
		return fmt.Errorf("delete user group: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return tx.Commit()
}

// ListUserGroups 返回全部用户分组（含成员与关联链路分组）。
func (s *Store) ListUserGroups(ctx context.Context) ([]UserGroup, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, name, created_at, updated_at FROM user_groups ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("list user groups: %w", err)
	}
	defer rows.Close()
	var groups []UserGroup
	for rows.Next() {
		var g UserGroup
		var created, updated string
		if err := rows.Scan(&g.ID, &g.Name, &created, &updated); err != nil {
			return nil, err
		}
		g.CreatedAt, _ = time.Parse("2006-01-02 15:04:05", created)
		g.UpdatedAt, _ = time.Parse("2006-01-02 15:04:05", updated)
		groups = append(groups, g)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range groups {
		if err := s.fillUserGroup(ctx, &groups[i]); err != nil {
			return nil, err
		}
	}
	return groups, nil
}

func (s *Store) fillUserGroup(ctx context.Context, g *UserGroup) error {
	g.UserIDs = make([]int64, 0)
	g.LinkGroupIDs = make([]int64, 0)
	userRows, err := s.db.QueryContext(ctx, `SELECT user_id FROM user_group_members WHERE user_group_id=? ORDER BY user_id`, g.ID)
	if err != nil {
		return err
	}
	for userRows.Next() {
		var id int64
		if err := userRows.Scan(&id); err != nil {
			userRows.Close()
			return err
		}
		g.UserIDs = append(g.UserIDs, id)
		g.MemberCount++
	}
	userRows.Close()
	if err := userRows.Err(); err != nil {
		return err
	}
	linkRows, err := s.db.QueryContext(ctx, `SELECT link_group_id FROM user_group_links WHERE user_group_id=? ORDER BY link_group_id`, g.ID)
	if err != nil {
		return err
	}
	for linkRows.Next() {
		var id int64
		if err := linkRows.Scan(&id); err != nil {
			linkRows.Close()
			return err
		}
		g.LinkGroupIDs = append(g.LinkGroupIDs, id)
		g.LinkGroupCount++
	}
	linkRows.Close()
	return linkRows.Err()
}

func (s *Store) UserGroupByID(ctx context.Context, id int64) (*UserGroup, error) {
	var g UserGroup
	var created, updated string
	err := s.db.QueryRowContext(ctx, `SELECT id, name, created_at, updated_at FROM user_groups WHERE id=?`, id).
		Scan(&g.ID, &g.Name, &created, &updated)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrNotFound
		}
		return nil, err
	}
	g.CreatedAt, _ = time.Parse("2006-01-02 15:04:05", created)
	g.UpdatedAt, _ = time.Parse("2006-01-02 15:04:05", updated)
	if err := s.fillUserGroup(ctx, &g); err != nil {
		return nil, err
	}
	return &g, nil
}

func (s *Store) UserGroupNameTaken(ctx context.Context, name string, excludeID int64) (bool, error) {
	var exists int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM user_groups WHERE name=? AND id != ?`, name, excludeID).
		Scan(&exists); err != nil {
		return false, err
	}
	return exists > 0, nil
}

// UserGroupIDsForUser 返回用户所属的用户分组 ID 列表。
func (s *Store) UserGroupIDsForUser(ctx context.Context, userID int64) ([]int64, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT user_group_id FROM user_group_members WHERE user_id=? ORDER BY user_group_id`, userID)
	if err != nil {
		return nil, fmt.Errorf("user group ids for user: %w", err)
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// EffectiveUserChainAssignments 返回用户生效的链路分配：
//   - 用户在任意用户分组内 → 只返回分组派生链路（AccessUUID 为确定性 UUIDv5，
//     ID 为 0）；直接分配被遮蔽；
//   - 否则 → 直接分配（等价 UserChainAssignments）。
func (s *Store) EffectiveUserChainAssignments(ctx context.Context, userID int64) ([]UserChainAssignment, error) {
	groupIDs, err := s.UserGroupIDsForUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	if len(groupIDs) == 0 {
		return s.UserChainAssignments(ctx, userID)
	}
	rows, err := s.db.QueryContext(ctx, `SELECT u.uuid, lgc.chain_id, c.endpoint_id
		FROM users u
		JOIN user_group_members ugm ON ugm.user_id = u.id
		JOIN user_group_links ugl ON ugl.user_group_id = ugm.user_group_id
		JOIN link_group_chains lgc ON lgc.group_id = ugl.link_group_id
		JOIN chains c ON c.id = lgc.chain_id
		WHERE u.id = ? AND c.deleted_at IS NULL
		ORDER BY lgc.chain_id`, userID)
	if err != nil {
		return nil, fmt.Errorf("effective chain assignments: %w", err)
	}
	defer rows.Close()
	seen := map[int64]bool{}
	var out []UserChainAssignment
	for rows.Next() {
		var userUUID string
		var a UserChainAssignment
		if err := rows.Scan(&userUUID, &a.ChainID, &a.EndpointID); err != nil {
			return nil, err
		}
		if seen[a.ChainID] {
			continue
		}
		seen[a.ChainID] = true
		a.UserID = userID
		a.UserUUID = userUUID
		a.AccessUUID = GroupAccessUUID(userUUID, a.ChainID)
		out = append(out, a)
	}
	return out, rows.Err()
}

// EffectiveUserExternalSubscriptions 返回用户生效的外部订阅：
//   - 分组用户 → 分组派生（mode 取分组行，经多分组引用同一订阅时按订阅去重取首行）；
//   - 否则 → 直接分配（等价 ListUserExternalSubscriptions）。
func (s *Store) EffectiveUserExternalSubscriptions(ctx context.Context, userID int64) ([]UserExternalSubscriptionJoined, error) {
	groupIDs, err := s.UserGroupIDsForUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	if len(groupIDs) == 0 {
		return s.ListUserExternalSubscriptions(ctx, userID)
	}
	rows, err := s.db.QueryContext(ctx, `SELECT lges.subscription_id, lges.mode,
		es.name, es.upload, es.download, es.total, es.expire, es.node_count
		FROM user_group_members ugm
		JOIN user_group_links ugl ON ugl.user_group_id = ugm.user_group_id
		JOIN link_group_external_subscriptions lges ON lges.group_id = ugl.link_group_id
		JOIN external_subscriptions es ON es.id = lges.subscription_id
		WHERE ugm.user_id = ?
		ORDER BY lges.subscription_id, lges.group_id`, userID)
	if err != nil {
		return nil, fmt.Errorf("effective external subscriptions: %w", err)
	}
	defer rows.Close()
	seen := map[int64]bool{}
	var out []UserExternalSubscriptionJoined
	for rows.Next() {
		var joined UserExternalSubscriptionJoined
		var expire sql.NullInt64
		if err := rows.Scan(&joined.SubscriptionID, &joined.Mode, &joined.Name,
			&joined.Upload, &joined.Download, &joined.Total, &expire, &joined.NodeCount); err != nil {
			return nil, err
		}
		if seen[joined.SubscriptionID] {
			continue
		}
		seen[joined.SubscriptionID] = true
		joined.UserID = userID
		if expire.Valid {
			joined.Expire = &expire.Int64
		}
		out = append(out, joined)
	}
	return out, rows.Err()
}

// SubscriptionUserIDsForLinkGroup 返回经用户分组引用该链路分组的全部用户。
func (s *Store) SubscriptionUserIDsForLinkGroup(ctx context.Context, linkGroupID int64) ([]int64, error) {
	return querySubscriptionUserIDs(ctx, s.db, `SELECT DISTINCT ugm.user_id
		FROM user_group_members ugm
		JOIN user_group_links ugl ON ugl.user_group_id = ugm.user_group_id
		WHERE ugl.link_group_id=? ORDER BY ugm.user_id`, linkGroupID)
}

// UsersForUserGroup 返回用户分组全体成员。
func (s *Store) UsersForUserGroup(ctx context.Context, userGroupID int64) ([]int64, error) {
	return querySubscriptionUserIDs(ctx, s.db,
		`SELECT user_id FROM user_group_members WHERE user_group_id=? ORDER BY user_id`, userGroupID)
}

// UsersByExternalSubscriptionThroughGroups 返回经链路分组引用指定外部订阅的用户。
func (s *Store) UsersByExternalSubscriptionThroughGroups(ctx context.Context, subscriptionID int64) ([]int64, error) {
	return querySubscriptionUserIDs(ctx, s.db, `SELECT DISTINCT ugm.user_id
		FROM user_group_members ugm
		JOIN user_group_links ugl ON ugl.user_group_id = ugm.user_group_id
		JOIN link_group_external_subscriptions lges ON lges.group_id = ugl.link_group_id
		WHERE lges.subscription_id=? ORDER BY ugm.user_id`, subscriptionID)
}

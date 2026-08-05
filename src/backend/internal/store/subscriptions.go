package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"
)

const (
	SubscriptionModeSuggested = "suggested"
	SubscriptionModeTemplate  = "template"

	SubscriptionGenerationMissing = "missing"
	SubscriptionGenerationPending = "pending"
	SubscriptionGenerationReady   = "ready"
	SubscriptionGenerationError   = "error"
)

var DefaultBalancedCategories = []string{
	"ai", "youtube", "google", "private", "domestic", "telegram", "github", "overseas",
}

type SubscriptionProfile struct {
	UserID             int64
	Mode               string
	Preset             string
	CategoriesJSON     string
	PortableTemplateID string
	MihomoTemplateID   string
	SingboxTemplateID  string
	QuanXTemplateID    string
	GenerationStatus   string
	GenerationError    string
	UpdatedAt          time.Time
}

type SubscriptionTemplate struct {
	ID            string     `json:"id"`
	Name          string     `json:"name"`
	Kind          string     `json:"kind"`
	Origin        string     `json:"origin"`
	SourceURL     string     `json:"source_url"`
	Content       string     `json:"content,omitempty"`
	ContentSHA256 string     `json:"content_sha256"`
	License       string     `json:"license"`
	Readonly      bool       `json:"readonly"`
	FetchedAt     *time.Time `json:"fetched_at,omitempty"`
	LastAttemptAt *time.Time `json:"last_attempt_at,omitempty"`
	LastError     string     `json:"last_error,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
}

type SubscriptionFile struct {
	SnapshotID  int64
	Revision    int64
	Format      string
	ContentType string
	Content     []byte
	Warnings    []string
	GeneratedAt time.Time
}

type SubscriptionTemplateRule struct {
	TemplateID     string
	TemplateSHA256 string
	Name           string
	SourceURL      string
	Content        []byte
	ContentSHA256  string
}

type SubscriptionRuleFile struct {
	SnapshotID   int64
	Revision     int64
	Name         string
	Format       string
	SourceSHA256 string
	ContentType  string
	Content      []byte
	GeneratedAt  time.Time
}

type SubscriptionSnapshotStatus struct {
	Status      string     `json:"status"`
	Error       string     `json:"error,omitempty"`
	Revision    int64      `json:"revision"`
	SourceLabel string     `json:"source_label,omitempty"`
	GeneratedAt *time.Time `json:"generated_at,omitempty"`
}

func defaultSubscriptionProfile(userID int64) SubscriptionProfile {
	return SubscriptionProfile{
		UserID: userID, Mode: SubscriptionModeSuggested, Preset: "balanced",
		CategoriesJSON:   `["ai","youtube","google","private","domestic","telegram","github","overseas"]`,
		GenerationStatus: SubscriptionGenerationMissing,
	}
}

func (s *Store) UserSubscriptionProfile(ctx context.Context, userID int64) (SubscriptionProfile, error) {
	var profile SubscriptionProfile
	err := s.db.QueryRowContext(ctx, `SELECT user_id, mode, preset, categories, portable_template_id,
		mihomo_template_id, singbox_template_id, quanx_template_id, generation_status,
		generation_error, updated_at FROM user_subscription_profiles WHERE user_id = ?`, userID).Scan(
		&profile.UserID, &profile.Mode, &profile.Preset, &profile.CategoriesJSON,
		&profile.PortableTemplateID, &profile.MihomoTemplateID, &profile.SingboxTemplateID,
		&profile.QuanXTemplateID, &profile.GenerationStatus, &profile.GenerationError, &profile.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		if _, userErr := s.UserByID(ctx, userID); userErr != nil {
			return SubscriptionProfile{}, userErr
		}
		return defaultSubscriptionProfile(userID), nil
	}
	if err != nil {
		return SubscriptionProfile{}, fmt.Errorf("query user subscription profile: %w", err)
	}
	return profile, nil
}

func (s *Store) SaveUserSubscriptionProfile(ctx context.Context, profile SubscriptionProfile) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO user_subscription_profiles
		(user_id, mode, preset, categories, portable_template_id, mihomo_template_id,
		 singbox_template_id, quanx_template_id, generation_status, generation_error, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, '', CURRENT_TIMESTAMP)
		ON CONFLICT(user_id) DO UPDATE SET mode=excluded.mode, preset=excluded.preset,
		categories=excluded.categories, portable_template_id=excluded.portable_template_id,
		mihomo_template_id=excluded.mihomo_template_id, singbox_template_id=excluded.singbox_template_id,
		quanx_template_id=excluded.quanx_template_id, generation_status=excluded.generation_status,
		generation_error='', updated_at=CURRENT_TIMESTAMP`,
		profile.UserID, profile.Mode, profile.Preset, profile.CategoriesJSON,
		profile.PortableTemplateID, profile.MihomoTemplateID, profile.SingboxTemplateID,
		profile.QuanXTemplateID, profile.GenerationStatus)
	if err != nil {
		return fmt.Errorf("save user subscription profile: %w", err)
	}
	return nil
}

func (s *Store) SetSubscriptionGenerationError(ctx context.Context, userID int64, message string) error {
	profile, err := s.UserSubscriptionProfile(ctx, userID)
	if err != nil {
		return err
	}
	profile.GenerationStatus = SubscriptionGenerationError
	if err := s.SaveUserSubscriptionProfile(ctx, profile); err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `UPDATE user_subscription_profiles SET generation_error = ? WHERE user_id = ?`, message, userID)
	return err
}

func (s *Store) PublishSubscriptionSnapshot(
	ctx context.Context,
	userID int64,
	sourceLabel, sourceSHA string,
	files map[string]SubscriptionFile,
	rules []SubscriptionRuleFile,
	warnings []string,
) (SubscriptionSnapshotStatus, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return SubscriptionSnapshotStatus{}, err
	}
	defer tx.Rollback()
	warningsJSON := ""
	if len(warnings) > 0 {
		encoded, err := json.Marshal(warnings)
		if err != nil {
			return SubscriptionSnapshotStatus{}, err
		}
		warningsJSON = string(encoded)
	}
	var revision int64
	if err := tx.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(revision), 0) + 1 FROM subscription_snapshots WHERE user_id = ?`, userID).Scan(&revision); err != nil {
		return SubscriptionSnapshotStatus{}, err
	}
	res, err := tx.ExecContext(ctx, `INSERT INTO subscription_snapshots
		(user_id, revision, source_label, source_sha256, warnings) VALUES (?, ?, ?, ?, ?)`,
		userID, revision, sourceLabel, sourceSHA, warningsJSON)
	if err != nil {
		return SubscriptionSnapshotStatus{}, fmt.Errorf("insert subscription snapshot: %w", err)
	}
	snapshotID, err := res.LastInsertId()
	if err != nil {
		return SubscriptionSnapshotStatus{}, err
	}
	for format, file := range files {
		if _, err := tx.ExecContext(ctx, `INSERT INTO subscription_files
			(snapshot_id, format, content_type, content) VALUES (?, ?, ?, ?)`,
			snapshotID, format, file.ContentType, file.Content); err != nil {
			return SubscriptionSnapshotStatus{}, fmt.Errorf("insert %s subscription file: %w", format, err)
		}
	}
	for _, rule := range rules {
		if _, err := tx.ExecContext(ctx, `INSERT INTO subscription_rule_files
			(snapshot_id, name, format, source_sha256, content_type, content) VALUES (?, ?, ?, ?, ?, ?)`,
			snapshotID, rule.Name, rule.Format, rule.SourceSHA256, rule.ContentType, rule.Content); err != nil {
			return SubscriptionSnapshotStatus{}, fmt.Errorf("insert %s/%s subscription rule: %w", rule.Format, rule.Name, err)
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO published_subscription_snapshots
		(user_id, snapshot_id, published_at) VALUES (?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(user_id) DO UPDATE SET snapshot_id=excluded.snapshot_id, published_at=CURRENT_TIMESTAMP`,
		userID, snapshotID); err != nil {
		return SubscriptionSnapshotStatus{}, fmt.Errorf("publish subscription snapshot: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO user_subscription_profiles
		(user_id, mode, preset, categories, generation_status) VALUES (?, 'suggested', 'balanced', ?, 'ready')
		ON CONFLICT(user_id) DO UPDATE SET generation_status='ready', generation_error='', updated_at=CURRENT_TIMESTAMP`,
		userID, defaultSubscriptionProfile(userID).CategoriesJSON); err != nil {
		return SubscriptionSnapshotStatus{}, fmt.Errorf("mark subscription ready: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM subscription_rule_files WHERE snapshot_id IN
		(SELECT id FROM subscription_snapshots WHERE user_id = ? AND id != ? AND revision < ?)`,
		userID, snapshotID, revision-1); err != nil {
		return SubscriptionSnapshotStatus{}, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM subscription_files WHERE snapshot_id IN
		(SELECT id FROM subscription_snapshots WHERE user_id = ? AND id != ? AND revision < ?)`,
		userID, snapshotID, revision-1); err != nil {
		return SubscriptionSnapshotStatus{}, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM subscription_snapshots
		WHERE user_id = ? AND id != ? AND revision < ?`, userID, snapshotID, revision-1); err != nil {
		return SubscriptionSnapshotStatus{}, err
	}
	if err := tx.Commit(); err != nil {
		return SubscriptionSnapshotStatus{}, err
	}
	now := time.Now().UTC()
	return SubscriptionSnapshotStatus{
		Status: SubscriptionGenerationReady, Revision: revision, SourceLabel: sourceLabel, GeneratedAt: &now,
	}, nil
}

func (s *Store) SubscriptionRuleFile(
	ctx context.Context, userID int64, version, format, name string,
) (SubscriptionRuleFile, error) {
	var file SubscriptionRuleFile
	err := s.db.QueryRowContext(ctx, `SELECT rf.snapshot_id, sn.revision, rf.name, rf.format,
		rf.source_sha256, rf.content_type, rf.content, sn.generated_at FROM subscription_snapshots sn
		JOIN subscription_rule_files rf ON rf.snapshot_id=sn.id
		WHERE sn.user_id=? AND rf.source_sha256=? AND rf.format=? AND rf.name=?
		ORDER BY sn.revision DESC LIMIT 1`,
		userID, version, format, name).Scan(&file.SnapshotID, &file.Revision, &file.Name,
		&file.Format, &file.SourceSHA256, &file.ContentType, &file.Content, &file.GeneratedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return SubscriptionRuleFile{}, ErrNotFound
	}
	if err != nil {
		return SubscriptionRuleFile{}, fmt.Errorf("query subscription rule file: %w", err)
	}
	return file, nil
}

func (s *Store) PublishedSubscriptionFile(ctx context.Context, userID int64, format string) (SubscriptionFile, error) {
	var file SubscriptionFile
	var warningsJSON string
	err := s.db.QueryRowContext(ctx, `SELECT f.snapshot_id, sn.revision, f.format, f.content_type,
		f.content, COALESCE(sn.warnings, ''), sn.generated_at FROM published_subscription_snapshots p
		JOIN subscription_snapshots sn ON sn.id = p.snapshot_id
		JOIN subscription_files f ON f.snapshot_id = sn.id
		WHERE p.user_id = ? AND f.format = ?`, userID, format).Scan(
		&file.SnapshotID, &file.Revision, &file.Format, &file.ContentType, &file.Content, &warningsJSON, &file.GeneratedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return SubscriptionFile{}, ErrNotFound
	}
	if err != nil {
		return SubscriptionFile{}, fmt.Errorf("query published subscription file: %w", err)
	}
	if warningsJSON != "" {
		_ = json.Unmarshal([]byte(warningsJSON), &file.Warnings)
	}
	return file, nil
}

func (s *Store) SubscriptionSnapshotStatus(ctx context.Context, userID int64) (SubscriptionSnapshotStatus, error) {
	profile, err := s.UserSubscriptionProfile(ctx, userID)
	if err != nil {
		return SubscriptionSnapshotStatus{}, err
	}
	status := SubscriptionSnapshotStatus{Status: profile.GenerationStatus, Error: profile.GenerationError}
	err = s.db.QueryRowContext(ctx, `SELECT sn.revision, sn.source_label, sn.generated_at
		FROM published_subscription_snapshots p JOIN subscription_snapshots sn ON sn.id=p.snapshot_id
		WHERE p.user_id=?`, userID).Scan(&status.Revision, &status.SourceLabel, &status.GeneratedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return status, nil
	}
	return status, err
}

func (s *Store) SubscriptionUserIDsForNode(ctx context.Context, nodeID int64) ([]int64, error) {
	return querySubscriptionUserIDs(ctx, s.db, `SELECT user_id FROM user_nodes WHERE node_id=? ORDER BY user_id`, nodeID)
}

func (s *Store) SubscriptionUserIDsForChain(ctx context.Context, chainID int64) ([]int64, error) {
	return querySubscriptionUserIDs(ctx, s.db, `SELECT user_id FROM (
		SELECT user_id FROM user_chain_assignments WHERE chain_id=?
		UNION SELECT un.user_id FROM user_nodes un
			JOIN chains c ON c.service_node_id=un.node_id WHERE c.id=?
		UNION SELECT ugm.user_id FROM user_group_members ugm
			JOIN user_group_links ugl ON ugl.user_group_id = ugm.user_group_id
			JOIN link_group_chains lgc ON lgc.group_id = ugl.link_group_id
			WHERE lgc.chain_id=?) ORDER BY user_id`, chainID, chainID, chainID)
}

func (s *Store) SubscriptionUserIDsForEndpoint(ctx context.Context, endpointID int64) ([]int64, error) {
	return querySubscriptionUserIDs(ctx, s.db, `SELECT user_id FROM (
		SELECT DISTINCT a.user_id FROM user_chain_assignments a
			JOIN chains c ON c.id=a.chain_id
			WHERE c.endpoint_id=? AND c.deleted_at IS NULL
		UNION SELECT DISTINCT ugm.user_id FROM user_group_members ugm
			JOIN user_group_links ugl ON ugl.user_group_id = ugm.user_group_id
			JOIN link_group_chains lgc ON lgc.group_id = ugl.link_group_id
			JOIN chains c ON c.id = lgc.chain_id
			WHERE c.endpoint_id=? AND c.deleted_at IS NULL) ORDER BY user_id`, endpointID, endpointID)
}

func (s *Store) SubscriptionUserIDsForServer(ctx context.Context, serverID int64) ([]int64, error) {
	direct, err := querySubscriptionUserIDs(ctx, s.db, `SELECT DISTINCT un.user_id FROM user_nodes un
		JOIN nodes n ON n.id=un.node_id WHERE n.server_id=?`, serverID)
	if err != nil {
		return nil, err
	}
	seen := make(map[int64]bool, len(direct))
	for _, id := range direct {
		seen[id] = true
	}
	rows, err := s.db.QueryContext(ctx, `SELECT c.id, r.snapshot FROM chains c
		JOIN chain_revisions r ON r.id=c.published_revision_id WHERE c.published_revision_id != 0`)
	if err != nil {
		return nil, err
	}
	type affectedChain struct {
		chainID       int64
		serviceNodeID int64
	}
	var affectedChains []affectedChain
	for rows.Next() {
		var chainID int64
		var raw string
		if err := rows.Scan(&chainID, &raw); err != nil {
			return nil, err
		}
		var snapshot struct {
			ServiceNodeID   int64 `json:"service_node_id"`
			ServiceServerID int64 `json:"service_server_id"`
			Hops            []struct {
				ServerID int64 `json:"server_id"`
			} `json:"hops"`
		}
		if json.Unmarshal([]byte(raw), &snapshot) != nil {
			continue
		}
		affected := snapshot.ServiceServerID == serverID
		for _, hop := range snapshot.Hops {
			affected = affected || hop.ServerID == serverID
		}
		if !affected {
			continue
		}
		affectedChains = append(affectedChains, affectedChain{chainID: chainID, serviceNodeID: snapshot.ServiceNodeID})
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	for _, chain := range affectedChains {
		for _, query := range []struct {
			text string
			arg  int64
		}{
			{`SELECT user_id FROM user_chain_assignments WHERE chain_id=?`, chain.chainID},
			{`SELECT user_id FROM user_nodes WHERE node_id=?`, chain.serviceNodeID},
			{`SELECT DISTINCT ugm.user_id FROM user_group_members ugm
				JOIN user_group_links ugl ON ugl.user_group_id = ugm.user_group_id
				JOIN link_group_chains lgc ON lgc.group_id = ugl.link_group_id
				WHERE lgc.chain_id=?`, chain.chainID},
		} {
			ids, err := querySubscriptionUserIDs(ctx, s.db, query.text, query.arg)
			if err != nil {
				return nil, err
			}
			for _, id := range ids {
				seen[id] = true
			}
		}
	}
	out := make([]int64, 0, len(seen))
	for id := range seen {
		out = append(out, id)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out, nil
}

type queryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func querySubscriptionUserIDs(ctx context.Context, q queryer, query string, args ...any) ([]int64, error) {
	rows, err := q.QueryContext(ctx, query, args...)
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

func (s *Store) ListSubscriptionTemplates(ctx context.Context) ([]SubscriptionTemplate, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, name, kind, origin, source_url, content, content_sha256,
		license, readonly, fetched_at, last_attempt_at, last_error, created_at, updated_at
		FROM subscription_templates ORDER BY readonly DESC, name COLLATE NOCASE`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SubscriptionTemplate
	for rows.Next() {
		template, err := scanSubscriptionTemplate(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, template)
	}
	return out, rows.Err()
}

func (s *Store) SubscriptionTemplateByID(ctx context.Context, id string) (SubscriptionTemplate, error) {
	template, err := scanSubscriptionTemplate(s.db.QueryRowContext(ctx, `SELECT id, name, kind, origin,
		source_url, content, content_sha256, license, readonly, fetched_at, last_attempt_at, last_error,
		created_at, updated_at FROM subscription_templates WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return SubscriptionTemplate{}, ErrNotFound
	}
	return template, err
}

type scanner interface{ Scan(...any) error }

func scanSubscriptionTemplate(row scanner) (SubscriptionTemplate, error) {
	var template SubscriptionTemplate
	var readonly int
	var fetched, attempted sql.NullTime
	err := row.Scan(&template.ID, &template.Name, &template.Kind, &template.Origin, &template.SourceURL,
		&template.Content, &template.ContentSHA256, &template.License, &readonly, &fetched, &attempted,
		&template.LastError, &template.CreatedAt, &template.UpdatedAt)
	if err != nil {
		return SubscriptionTemplate{}, err
	}
	template.Readonly = readonly != 0
	if fetched.Valid {
		template.FetchedAt = &fetched.Time
	}
	if attempted.Valid {
		template.LastAttemptAt = &attempted.Time
	}
	return template, nil
}

func (s *Store) UpsertSubscriptionTemplate(ctx context.Context, template SubscriptionTemplate) error {
	return s.UpsertSubscriptionTemplateWithRules(ctx, template, nil, false)
}

// UpsertSubscriptionTemplateWithRules atomically updates a template and its
// complete referenced-rule cache when replaceRules is true.
func (s *Store) UpsertSubscriptionTemplateWithRules(
	ctx context.Context, template SubscriptionTemplate, rules []SubscriptionTemplateRule, replaceRules bool,
) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	readonly := 0
	if template.Readonly {
		readonly = 1
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO subscription_templates
		(id, name, kind, origin, source_url, content, content_sha256, license, readonly,
		 fetched_at, last_attempt_at, last_error, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(id) DO UPDATE SET name=excluded.name, kind=excluded.kind, origin=excluded.origin,
		source_url=excluded.source_url, content=excluded.content, content_sha256=excluded.content_sha256,
		license=excluded.license, readonly=excluded.readonly, fetched_at=excluded.fetched_at,
		last_attempt_at=excluded.last_attempt_at, last_error=excluded.last_error, updated_at=CURRENT_TIMESTAMP`,
		template.ID, template.Name, template.Kind, template.Origin, template.SourceURL, template.Content,
		template.ContentSHA256, template.License, readonly, template.FetchedAt, template.LastAttemptAt,
		template.LastError)
	if err != nil {
		return err
	}
	if replaceRules {
		if _, err := tx.ExecContext(ctx, `DELETE FROM subscription_template_rules WHERE template_id=?`, template.ID); err != nil {
			return err
		}
		for _, rule := range rules {
			if _, err := tx.ExecContext(ctx, `INSERT INTO subscription_template_rules
				(template_id, template_sha256, name, source_url, content, content_sha256)
				VALUES (?, ?, ?, ?, ?, ?)`, template.ID, rule.TemplateSHA256, rule.Name,
				rule.SourceURL, rule.Content, rule.ContentSHA256); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

func (s *Store) SubscriptionTemplateRules(ctx context.Context, templateID, templateSHA string) ([]SubscriptionTemplateRule, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT template_id, template_sha256, name, source_url,
		content, content_sha256 FROM subscription_template_rules
		WHERE template_id=? AND template_sha256=? ORDER BY name`, templateID, templateSHA)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var rules []SubscriptionTemplateRule
	for rows.Next() {
		var rule SubscriptionTemplateRule
		if err := rows.Scan(&rule.TemplateID, &rule.TemplateSHA256, &rule.Name,
			&rule.SourceURL, &rule.Content, &rule.ContentSHA256); err != nil {
			return nil, err
		}
		rules = append(rules, rule)
	}
	return rules, rows.Err()
}

func (s *Store) EnsureSubscriptionTemplate(ctx context.Context, template SubscriptionTemplate) error {
	readonly := 0
	if template.Readonly {
		readonly = 1
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO subscription_templates
		(id, name, kind, origin, source_url, content, content_sha256, license, readonly)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET name=excluded.name, kind=excluded.kind,
		origin=excluded.origin, source_url=excluded.source_url, license=excluded.license,
		readonly=excluded.readonly, updated_at=CURRENT_TIMESTAMP`, template.ID, template.Name, template.Kind,
		template.Origin, template.SourceURL, template.Content, template.ContentSHA256, template.License, readonly)
	return err
}

func (s *Store) DeleteSubscriptionTemplate(ctx context.Context, id string) error {
	var refs int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM user_subscription_profiles WHERE
		portable_template_id=? OR mihomo_template_id=? OR singbox_template_id=? OR quanx_template_id=?`,
		id, id, id, id).Scan(&refs); err != nil {
		return err
	}
	if refs > 0 {
		return fmt.Errorf("template is used by %d user profiles", refs)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM subscription_template_rules WHERE template_id=?`, id); err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx, `DELETE FROM subscription_templates WHERE id=? AND readonly=0`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return tx.Commit()
}

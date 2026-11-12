package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/google/uuid"
)

const (
	PublicationVisibility    = "visibility"
	PublicationTemplate      = "template"
	PublicationTemplateReset = "template_reset"

	PublicationQueued    = "queued"
	PublicationRunning   = "running"
	PublicationCompleted = "completed"
	PublicationFailed    = "failed"

	maxPendingPublicationJobs = 32
)

var (
	ErrSubscriptionPublicationQueueFull = errors.New("subscription publication queue is full")
	ErrSubscriptionItemUnavailable      = errors.New("subscription item is unavailable")
)

type SubscriptionPublicationJob struct {
	ID           string `json:"id"`
	Kind         string `json:"kind"`
	Status       string `json:"status"`
	QueuedBehind bool   `json:"queued_behind"`
	Error        string `json:"error,omitempty"`

	userID  int64
	BaseURL string `json:"-"`
	payload string
}

type subscriptionPublicationTemplate struct {
	Mode                string `json:"mode"`
	Preset              string `json:"preset"`
	CategoriesJSON      string `json:"categories"`
	PortableTemplateID  string `json:"portable_template_id"`
	MihomoTemplateID    string `json:"mihomo_template_id"`
	SingboxTemplateID   string `json:"singbox_template_id"`
	QuanXTemplateID     string `json:"quanx_template_id"`
	RegionGroupsEnabled *bool  `json:"region_groups_enabled,omitempty"`
}

func (s *Store) EnqueueSubscriptionTemplate(ctx context.Context, profile SubscriptionProfile, regionGroupsEnabled *bool, baseURL string) (SubscriptionPublicationJob, error) {
	payload, err := json.Marshal(subscriptionPublicationTemplate{
		Mode: profile.Mode, Preset: profile.Preset, CategoriesJSON: profile.CategoriesJSON,
		PortableTemplateID: profile.PortableTemplateID, MihomoTemplateID: profile.MihomoTemplateID,
		SingboxTemplateID: profile.SingboxTemplateID, QuanXTemplateID: profile.QuanXTemplateID,
		RegionGroupsEnabled: regionGroupsEnabled,
	})
	if err != nil {
		return SubscriptionPublicationJob{}, err
	}
	return enqueueSubscriptionPublication(ctx, s.db, profile.UserID, PublicationTemplate, baseURL, string(payload))
}

func (s *Store) EnqueueSubscriptionTemplateReset(ctx context.Context, userID int64, baseURL string) (SubscriptionPublicationJob, error) {
	return enqueueSubscriptionPublication(ctx, s.db, userID, PublicationTemplateReset, baseURL, "")
}

// EnqueueSubscriptionVisibility retains previously hidden items that the current
// catalog cannot display. Reading the previous request and inserting the new
// one in the same transaction also preserves those items under concurrent saves.
func (s *Store) EnqueueSubscriptionVisibility(ctx context.Context, userID int64, requested []string, available map[string]bool, baseURL string) (SubscriptionPublicationJob, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return SubscriptionPublicationJob{}, err
	}
	defer tx.Rollback()
	previous, err := requestedSubscriptionVisibility(ctx, tx, userID)
	if err != nil {
		return SubscriptionPublicationJob{}, err
	}
	previouslyHidden := subscriptionVisibilitySet(previous)
	hidden := make(map[string]bool, len(requested)+len(previous))
	for _, id := range requested {
		if !available[id] && !previouslyHidden[id] {
			return SubscriptionPublicationJob{}, fmt.Errorf("%w: %s", ErrSubscriptionItemUnavailable, id)
		}
		hidden[id] = true
	}
	for _, id := range previous {
		if !available[id] {
			hidden[id] = true
		}
	}
	ids := make([]string, 0, len(hidden))
	for id := range hidden {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	payload, err := json.Marshal(ids)
	if err != nil {
		return SubscriptionPublicationJob{}, err
	}
	job, err := enqueueSubscriptionPublication(ctx, tx, userID, PublicationVisibility, baseURL, string(payload))
	if err != nil {
		return SubscriptionPublicationJob{}, err
	}
	return job, tx.Commit()
}

type subscriptionPublicationQueryer interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func enqueueSubscriptionPublication(ctx context.Context, db subscriptionPublicationQueryer, userID int64, kind, baseURL, payload string) (SubscriptionPublicationJob, error) {
	job := SubscriptionPublicationJob{ID: uuid.NewString(), userID: userID, Kind: kind, Status: PublicationQueued}
	err := db.QueryRowContext(ctx, `INSERT INTO subscription_publication_jobs
		(id, user_id, kind, status, queued_behind, base_url, payload)
		SELECT ?, id, ?, 'queued', pending.count > 0, ?, ?
		FROM users, (
			SELECT COUNT(*) AS count FROM subscription_publication_jobs
			WHERE user_id = ? AND status IN ('queued', 'running')
		) pending
		WHERE id = ? AND pending.count < ?
		RETURNING queued_behind`,
		job.ID, kind, baseURL, payload, userID, userID, maxPendingPublicationJobs).Scan(&job.QueuedBehind)
	if errors.Is(err, sql.ErrNoRows) {
		var exists bool
		if err := db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE id = ?)`, userID).Scan(&exists); err != nil {
			return SubscriptionPublicationJob{}, err
		}
		if !exists {
			return SubscriptionPublicationJob{}, ErrNotFound
		}
		return SubscriptionPublicationJob{}, ErrSubscriptionPublicationQueueFull
	}
	return job, err
}

func (s *Store) SubscriptionPublicationJobs(ctx context.Context, userID int64) ([]SubscriptionPublicationJob, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, kind, status, queued_behind, error
		FROM subscription_publication_jobs WHERE user_id = ? ORDER BY sequence DESC LIMIT 64`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	jobs := make([]SubscriptionPublicationJob, 0)
	for rows.Next() {
		var job SubscriptionPublicationJob
		if err := rows.Scan(&job.ID, &job.Kind, &job.Status, &job.QueuedBehind, &job.Error); err != nil {
			return nil, err
		}
		jobs = append(jobs, job)
	}
	return jobs, rows.Err()
}

func (s *Store) SubscriptionPublicationJob(ctx context.Context, userID int64, id string) (SubscriptionPublicationJob, error) {
	job := SubscriptionPublicationJob{ID: id, userID: userID}
	err := s.db.QueryRowContext(ctx, `SELECT kind, status, queued_behind, error FROM subscription_publication_jobs
		WHERE user_id = ? AND id = ?`, userID, id).Scan(&job.Kind, &job.Status, &job.QueuedBehind, &job.Error)
	if errors.Is(err, sql.ErrNoRows) {
		return SubscriptionPublicationJob{}, ErrNotFound
	}
	return job, err
}

func (s *Store) RecoverSubscriptionPublicationJobs(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `UPDATE subscription_publication_jobs SET status = 'queued' WHERE status = 'running'`)
	return err
}

func (s *Store) QueuedSubscriptionPublicationUserIDs(ctx context.Context) ([]int64, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT user_id FROM subscription_publication_jobs
		WHERE status IN ('queued', 'running') GROUP BY user_id ORDER BY MIN(sequence)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var users []int64
	for rows.Next() {
		var userID int64
		if err := rows.Scan(&userID); err != nil {
			return nil, err
		}
		users = append(users, userID)
	}
	return users, rows.Err()
}

// ClaimSubscriptionPublicationJob requires the user publication lock. A
// running row under that lock survived an interruption and can be replayed.
func (s *Store) ClaimSubscriptionPublicationJob(ctx context.Context, userID int64) (SubscriptionPublicationJob, error) {
	job := SubscriptionPublicationJob{userID: userID, Status: PublicationRunning}
	err := s.db.QueryRowContext(ctx, `UPDATE subscription_publication_jobs SET status = 'running'
		WHERE sequence = (
			SELECT sequence FROM subscription_publication_jobs WHERE user_id = ?
			AND status IN ('queued', 'running') ORDER BY sequence LIMIT 1
		)
		RETURNING id, kind, queued_behind, base_url, payload`,
		userID).Scan(&job.ID, &job.Kind, &job.QueuedBehind, &job.BaseURL, &job.payload)
	if errors.Is(err, sql.ErrNoRows) {
		return SubscriptionPublicationJob{}, ErrNotFound
	}
	return job, err
}

func (s *Store) FailSubscriptionPublicationJob(ctx context.Context, job SubscriptionPublicationJob, message string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE subscription_publication_jobs SET status = 'failed', error = ?,
		payload = '', base_url = '' WHERE id = ? AND user_id = ? AND status = 'running'`, message, job.ID, job.userID)
	return err
}

func (s *Store) RequeueSubscriptionPublicationJob(ctx context.Context, job SubscriptionPublicationJob) error {
	_, err := s.db.ExecContext(ctx, `UPDATE subscription_publication_jobs SET status = 'queued'
		WHERE id = ? AND user_id = ? AND status = 'running'`, job.ID, job.userID)
	return err
}

// UserRequestedHiddenSubscriptionItems reports the most recent outstanding
// choice for the editor, falling back to the published choice.
func (s *Store) UserRequestedHiddenSubscriptionItems(ctx context.Context, userID int64) ([]string, error) {
	return requestedSubscriptionVisibility(ctx, s.db, userID)
}

func requestedSubscriptionVisibility(ctx context.Context, db subscriptionPublicationQueryer, userID int64) ([]string, error) {
	return readSubscriptionVisibility(db.QueryRowContext(ctx, `SELECT COALESCE(
		(SELECT payload FROM subscription_publication_jobs WHERE user_id = ? AND kind = 'visibility'
			AND status IN ('queued', 'running') ORDER BY sequence DESC LIMIT 1),
		(SELECT hidden_ids FROM user_subscription_visibility WHERE user_id = ?), '[]')`, userID, userID))
}

// UserHiddenSubscriptionItems reports the published preference. A publisher
// compiling a visibility job sees that job's candidate preference.
func (s *Store) UserHiddenSubscriptionItems(ctx context.Context, userID int64) (map[string]bool, error) {
	if change := subscriptionPublicationFromContext(ctx, userID); change != nil && change.job.Kind == PublicationVisibility {
		return change.hiddenIDs, nil
	}
	ids, err := readSubscriptionVisibility(s.db.QueryRowContext(ctx,
		`SELECT COALESCE((SELECT hidden_ids FROM user_subscription_visibility WHERE user_id = ?), '[]')`, userID))
	if err != nil {
		return nil, err
	}
	return subscriptionVisibilitySet(ids), nil
}

func readSubscriptionVisibility(row *sql.Row) ([]string, error) {
	var raw string
	if err := row.Scan(&raw); err != nil {
		return nil, err
	}
	ids := []string{}
	err := json.Unmarshal([]byte(raw), &ids)
	return ids, err
}

func subscriptionVisibilitySet(ids []string) map[string]bool {
	hidden := make(map[string]bool, len(ids))
	for _, id := range ids {
		hidden[id] = true
	}
	return hidden
}

type subscriptionPublicationContextKey struct{}

type subscriptionPublicationChange struct {
	job       SubscriptionPublicationJob
	hiddenIDs map[string]bool
	template  subscriptionPublicationTemplate
}

// WithSubscriptionPublicationJob applies an isolated proposal to compiler
// reads. The preference and terminal state are persisted only with the snapshot.
func WithSubscriptionPublicationJob(ctx context.Context, job SubscriptionPublicationJob) (context.Context, error) {
	change := &subscriptionPublicationChange{job: job}
	switch job.Kind {
	case PublicationVisibility:
		var ids []string
		if err := json.Unmarshal([]byte(job.payload), &ids); err != nil {
			return ctx, err
		}
		change.hiddenIDs = subscriptionVisibilitySet(ids)
	case PublicationTemplate:
		if err := json.Unmarshal([]byte(job.payload), &change.template); err != nil {
			return ctx, err
		}
	case PublicationTemplateReset:
	default:
		return ctx, fmt.Errorf("unknown subscription publication kind %q", job.Kind)
	}
	return context.WithValue(ctx, subscriptionPublicationContextKey{}, change), nil
}

func subscriptionPublicationFromContext(ctx context.Context, userID int64) *subscriptionPublicationChange {
	change, _ := ctx.Value(subscriptionPublicationContextKey{}).(*subscriptionPublicationChange)
	if change != nil && change.job.userID == userID {
		return change
	}
	return nil
}

func (change *subscriptionPublicationChange) profile(current SubscriptionProfile) SubscriptionProfile {
	if change == nil || change.job.Kind == PublicationVisibility {
		return current
	}
	if change.job.Kind == PublicationTemplate {
		template := change.template
		current.Mode = template.Mode
		current.Preset = template.Preset
		current.CategoriesJSON = template.CategoriesJSON
		current.PortableTemplateID = template.PortableTemplateID
		current.MihomoTemplateID = template.MihomoTemplateID
		current.SingboxTemplateID = template.SingboxTemplateID
		current.QuanXTemplateID = template.QuanXTemplateID
		current.UserConfigured = true
		if template.RegionGroupsEnabled != nil {
			current.DisableRegionGroups = !*template.RegionGroupsEnabled
		}
		return current
	}
	if !current.UserConfigured {
		return current
	}
	// Reset the user's choices. The region setting shares a column with
	// administrator assignments, so preserve its value when any are configured.
	defaults := defaultSubscriptionProfile(current.UserID)
	current.Mode = defaults.Mode
	current.Preset = defaults.Preset
	current.CategoriesJSON = defaults.CategoriesJSON
	current.PortableTemplateID = ""
	current.MihomoTemplateID = ""
	current.SingboxTemplateID = ""
	current.QuanXTemplateID = ""
	current.UserConfigured = false
	if !current.PanelRoutingConfigured() {
		current.DisableRegionGroups = defaults.DisableRegionGroups
	}
	return current
}

func (change *subscriptionPublicationChange) complete(ctx context.Context, tx *sql.Tx) error {
	if change == nil {
		return nil
	}
	job := change.job
	if job.Kind == PublicationVisibility {
		if _, err := tx.ExecContext(ctx, `INSERT INTO user_subscription_visibility (user_id, hidden_ids) VALUES (?, ?)
			ON CONFLICT(user_id) DO UPDATE SET hidden_ids = excluded.hidden_ids`, job.userID, job.payload); err != nil {
			return err
		}
	} else {
		current, err := readUserSubscriptionProfile(ctx, tx, job.userID)
		if err != nil {
			return err
		}
		profile := change.profile(current)
		if _, err := tx.ExecContext(ctx, `UPDATE user_subscription_profiles
			SET mode = ?, preset = ?, categories = ?, portable_template_id = ?, mihomo_template_id = ?,
				singbox_template_id = ?, quanx_template_id = ?, disable_region_groups = ?, user_configured = ?
			WHERE user_id = ?`, profile.Mode, profile.Preset, profile.CategoriesJSON, profile.PortableTemplateID,
			profile.MihomoTemplateID, profile.SingboxTemplateID, profile.QuanXTemplateID, profile.DisableRegionGroups,
			profile.UserConfigured, job.userID); err != nil {
			return err
		}
	}
	result, err := tx.ExecContext(ctx, `UPDATE subscription_publication_jobs
		SET status = 'completed', payload = '', base_url = '' WHERE id = ? AND user_id = ? AND status = 'running'`,
		job.ID, job.userID)
	if err != nil {
		return err
	}
	if updated, err := result.RowsAffected(); err != nil {
		return err
	} else if updated != 1 {
		return ErrNotFound
	}
	return nil
}

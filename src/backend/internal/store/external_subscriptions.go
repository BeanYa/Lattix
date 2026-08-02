package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

type ExternalSubscription struct {
	ID                  int64      `json:"id"`
	Name                string     `json:"name"`
	URL                 string     `json:"url"`
	UserAgent           string     `json:"user_agent"`
	SkipCertVerify      bool       `json:"skip_cert_verify"`
	AutoUpdate          bool       `json:"auto_update"`
	UpdateIntervalHours int        `json:"update_interval_hours"`
	Format              string     `json:"format"`
	NodeCount           int        `json:"node_count"`
	Upload              int64      `json:"upload"`
	Download            int64      `json:"download"`
	Total               int64      `json:"total"`
	Expire              *int64     `json:"expire,omitempty"`
	LastSyncAt          *time.Time `json:"last_sync_at,omitempty"`
	LastAttemptAt       *time.Time `json:"last_attempt_at,omitempty"`
	LastError           string     `json:"last_error,omitempty"`
	CreatedAt           time.Time  `json:"created_at"`
	UpdatedAt           time.Time  `json:"updated_at"`
}

type ExternalChain struct {
	ID             int64           `json:"id"`
	SubscriptionID int64           `json:"subscription_id"`
	Name           string          `json:"name"`
	Protocol       string          `json:"protocol"`
	Server         string          `json:"server"`
	Port           int             `json:"port"`
	Config         json.RawMessage `json:"config"`
	ConfigSHA256   string          `json:"config_sha256"`
	CreatedAt      time.Time       `json:"created_at"`
}

const externalSubscriptionColumns = `id, name, url, user_agent, skip_cert_verify,
	auto_update, update_interval_hours, format, node_count, upload, download, total,
	expire, last_sync_at, last_attempt_at, last_error, created_at, updated_at`

func scanExternalSubscription(row scanner) (ExternalSubscription, error) {
	var sub ExternalSubscription
	var skipCert, autoUpdate int
	var expire sql.NullInt64
	var lastSyncAt, lastAttemptAt, createdAt, updatedAt sql.NullTime
	err := row.Scan(&sub.ID, &sub.Name, &sub.URL, &sub.UserAgent, &skipCert,
		&autoUpdate, &sub.UpdateIntervalHours, &sub.Format, &sub.NodeCount,
		&sub.Upload, &sub.Download, &sub.Total, &expire,
		&lastSyncAt, &lastAttemptAt, &sub.LastError, &createdAt, &updatedAt)
	if err != nil {
		return ExternalSubscription{}, err
	}
	sub.SkipCertVerify = skipCert != 0
	sub.AutoUpdate = autoUpdate != 0
	if expire.Valid {
		sub.Expire = &expire.Int64
	}
	if lastSyncAt.Valid {
		sub.LastSyncAt = &lastSyncAt.Time
	}
	if lastAttemptAt.Valid {
		sub.LastAttemptAt = &lastAttemptAt.Time
	}
	sub.CreatedAt, sub.UpdatedAt = createdAt.Time, updatedAt.Time
	return sub, nil
}

func (s *Store) CreateExternalSubscription(ctx context.Context, sub ExternalSubscription) (int64, error) {
	skipCertVerify, autoUpdate := 0, 0
	if sub.SkipCertVerify {
		skipCertVerify = 1
	}
	if sub.AutoUpdate {
		autoUpdate = 1
	}
	res, err := s.db.ExecContext(ctx, `INSERT INTO external_subscriptions
		(name, url, user_agent, skip_cert_verify, auto_update, update_interval_hours, format)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		sub.Name, sub.URL, sub.UserAgent, skipCertVerify, autoUpdate,
		sub.UpdateIntervalHours, sub.Format)
	if err != nil {
		return 0, fmt.Errorf("insert external subscription: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("external subscription id: %w", err)
	}
	return id, nil
}

func (s *Store) UpdateExternalSubscription(ctx context.Context, sub ExternalSubscription) error {
	skipCertVerify, autoUpdate := 0, 0
	if sub.SkipCertVerify {
		skipCertVerify = 1
	}
	if sub.AutoUpdate {
		autoUpdate = 1
	}
	res, err := s.db.ExecContext(ctx, `UPDATE external_subscriptions SET
		name = ?, url = ?, user_agent = ?, skip_cert_verify = ?, auto_update = ?,
		update_interval_hours = ?, format = ?, node_count = ?, upload = ?, download = ?,
		total = ?, expire = ?, last_sync_at = ?, last_attempt_at = ?, last_error = ?,
		updated_at = CURRENT_TIMESTAMP
		WHERE id = ?`,
		sub.Name, sub.URL, sub.UserAgent, skipCertVerify, autoUpdate,
		sub.UpdateIntervalHours, sub.Format, sub.NodeCount, sub.Upload, sub.Download,
		sub.Total, sub.Expire, sub.LastSyncAt, sub.LastAttemptAt, sub.LastError, sub.ID)
	if err != nil {
		return fmt.Errorf("update external subscription: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("external subscription rows: %w", err)
	}
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) DeleteExternalSubscription(ctx context.Context, id int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin delete external subscription: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM external_chains WHERE subscription_id = ?`, id); err != nil {
		return fmt.Errorf("delete external subscription chains: %w", err)
	}
	res, err := tx.ExecContext(ctx, `DELETE FROM external_subscriptions WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete external subscription: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("external subscription rows: %w", err)
	}
	if affected == 0 {
		return ErrNotFound
	}
	return tx.Commit()
}

func (s *Store) ListExternalSubscriptions(ctx context.Context) ([]ExternalSubscription, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+externalSubscriptionColumns+`
		FROM external_subscriptions ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("list external subscriptions: %w", err)
	}
	defer rows.Close()
	var subs []ExternalSubscription
	for rows.Next() {
		sub, err := scanExternalSubscription(rows)
		if err != nil {
			return nil, fmt.Errorf("scan external subscription: %w", err)
		}
		subs = append(subs, sub)
	}
	return subs, rows.Err()
}

func (s *Store) ExternalSubscriptionByID(ctx context.Context, id int64) (ExternalSubscription, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+externalSubscriptionColumns+`
		FROM external_subscriptions WHERE id = ?`, id)
	sub, err := scanExternalSubscription(row)
	if errors.Is(err, sql.ErrNoRows) {
		return ExternalSubscription{}, ErrNotFound
	}
	if err != nil {
		return ExternalSubscription{}, fmt.Errorf("query external subscription %d: %w", id, err)
	}
	return sub, nil
}

func (s *Store) ExternalSubscriptionByURL(ctx context.Context, url string) (ExternalSubscription, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+externalSubscriptionColumns+`
		FROM external_subscriptions WHERE url = ?`, url)
	sub, err := scanExternalSubscription(row)
	if errors.Is(err, sql.ErrNoRows) {
		return ExternalSubscription{}, ErrNotFound
	}
	if err != nil {
		return ExternalSubscription{}, fmt.Errorf("query external subscription by url: %w", err)
	}
	return sub, nil
}

func (s *Store) ReplaceExternalChains(ctx context.Context, subID int64, chains []ExternalChain) (int, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin replace external chains: %w", err)
	}
	defer tx.Rollback()

	// 同步在事务外拉取（最长 30s），期间订阅可能已被删除；若已不存在
	// 则拒绝写入，避免为已删除订阅残留孤儿节点（PRAGMA foreign_keys 未开启）。
	var exists int
	if err := tx.QueryRowContext(ctx,
		`SELECT EXISTS(SELECT 1 FROM external_subscriptions WHERE id = ?)`, subID).Scan(&exists); err != nil {
		return 0, fmt.Errorf("check external subscription exists: %w", err)
	}
	if exists == 0 {
		return 0, ErrNotFound
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM external_chains WHERE subscription_id = ?`, subID); err != nil {
		return 0, fmt.Errorf("clear external chains: %w", err)
	}
	seen := make(map[string]bool)
	count := 0
	for _, chain := range chains {
		if seen[chain.ConfigSHA256] {
			continue
		}
		seen[chain.ConfigSHA256] = true
		if _, err := tx.ExecContext(ctx, `INSERT INTO external_chains
			(subscription_id, name, protocol, server, port, config, config_sha256)
			VALUES (?, ?, ?, ?, ?, ?, ?)`,
			subID, chain.Name, chain.Protocol, chain.Server, chain.Port,
			string(chain.Config), chain.ConfigSHA256); err != nil {
			return 0, fmt.Errorf("insert external chain: %w", err)
		}
		count++
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit replace external chains: %w", err)
	}
	return count, nil
}

func (s *Store) ListExternalChains(ctx context.Context, subID int64) ([]ExternalChain, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, subscription_id, name, protocol,
		server, port, config, config_sha256, created_at
		FROM external_chains WHERE subscription_id = ? ORDER BY id`, subID)
	if err != nil {
		return nil, fmt.Errorf("list external chains: %w", err)
	}
	defer rows.Close()
	var chains []ExternalChain
	for rows.Next() {
		var chain ExternalChain
		var createdAt sql.NullTime
		var config string
		if err := rows.Scan(&chain.ID, &chain.SubscriptionID, &chain.Name, &chain.Protocol,
			&chain.Server, &chain.Port, &config, &chain.ConfigSHA256, &createdAt); err != nil {
			return nil, fmt.Errorf("scan external chain: %w", err)
		}
		chain.Config = json.RawMessage(config)
		chain.CreatedAt = createdAt.Time
		chains = append(chains, chain)
	}
	return chains, rows.Err()
}

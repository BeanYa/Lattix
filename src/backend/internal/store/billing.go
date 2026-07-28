package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	BillingDisabled     = "disabled"
	BillingActive       = "active"
	BillingDueToday     = "due_today"
	BillingAssumedValid = "assumed_valid"
	BillingExpired      = "expired"
)

type Provider struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	WebsiteURL string `json:"website_url"`
}

type ServerBilling struct {
	ServerID            int64
	Enabled             bool
	ProviderID          int64
	AmountMinor         int64
	Currency            string
	ServiceStartedOn    string
	IntervalCount       int
	IntervalUnit        string
	NextRenewalOn       string
	Status              string
	AssumedValidThrough string
	LastInspectedOn     string
	StatusChangedAt     string
}

type ServerTrafficPlan struct {
	ServerID          int64
	QuotaBytes        *int64
	AccountingMode    string
	ResetAnchorOn     string
	ResetCount        int
	ResetUnit         string
	TrackingStartedOn string
}

func (s *Store) ListProviders(ctx context.Context) ([]Provider, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, name, website_url FROM providers ORDER BY name COLLATE NOCASE`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Provider
	for rows.Next() {
		var p Provider
		if err := rows.Scan(&p.ID, &p.Name, &p.WebsiteURL); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) CreateProvider(ctx context.Context, name, website string) (Provider, error) {
	res, err := s.db.ExecContext(ctx, `INSERT INTO providers (name, website_url) VALUES (?, ?)`, strings.TrimSpace(name), strings.TrimSpace(website))
	if err != nil {
		return Provider{}, err
	}
	id, _ := res.LastInsertId()
	return Provider{ID: id, Name: strings.TrimSpace(name), WebsiteURL: strings.TrimSpace(website)}, nil
}

func (s *Store) UpdateProvider(ctx context.Context, id int64, name, website string) error {
	res, err := s.db.ExecContext(ctx, `UPDATE providers SET name = ?, website_url = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`, strings.TrimSpace(name), strings.TrimSpace(website), id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) DeleteProvider(ctx context.Context, id int64) error {
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM server_billing WHERE provider_id = ?`, id).Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return errors.New("provider is in use")
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM providers WHERE id = ?`, id)
	return err
}

func scanBilling(scanner interface{ Scan(...any) error }) (ServerBilling, error) {
	var b ServerBilling
	err := scanner.Scan(&b.ServerID, &b.Enabled, &b.ProviderID, &b.AmountMinor, &b.Currency,
		&b.ServiceStartedOn, &b.IntervalCount, &b.IntervalUnit, &b.NextRenewalOn, &b.Status,
		&b.AssumedValidThrough, &b.LastInspectedOn, &b.StatusChangedAt)
	return b, err
}

func (s *Store) ServerBillingMap(ctx context.Context) (map[int64]ServerBilling, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT server_id, enabled, COALESCE(provider_id, 0), amount_minor, currency,
		service_started_on, interval_count, interval_unit, next_renewal_on, status,
		assumed_valid_through, last_inspected_on, status_changed_at FROM server_billing`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]ServerBilling{}
	for rows.Next() {
		b, err := scanBilling(rows)
		if err != nil {
			return nil, err
		}
		out[b.ServerID] = b
	}
	return out, rows.Err()
}

func (s *Store) UpsertServerBilling(ctx context.Context, b ServerBilling) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO server_billing
		(server_id, enabled, provider_id, amount_minor, currency, service_started_on, interval_count, interval_unit, next_renewal_on, status, assumed_valid_through, last_inspected_on)
		VALUES (?, ?, NULLIF(?, 0), ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(server_id) DO UPDATE SET enabled=excluded.enabled, provider_id=excluded.provider_id,
		amount_minor=excluded.amount_minor, currency=excluded.currency, service_started_on=excluded.service_started_on,
		interval_count=excluded.interval_count, interval_unit=excluded.interval_unit,
		next_renewal_on=excluded.next_renewal_on, status=excluded.status,
		assumed_valid_through=excluded.assumed_valid_through, last_inspected_on=excluded.last_inspected_on,
		status_changed_at=CASE WHEN server_billing.status <> excluded.status THEN CURRENT_TIMESTAMP ELSE server_billing.status_changed_at END,
		updated_at=CURRENT_TIMESTAMP`, b.ServerID, b.Enabled, b.ProviderID, b.AmountMinor, b.Currency,
		b.ServiceStartedOn, b.IntervalCount, b.IntervalUnit, b.NextRenewalOn, b.Status,
		b.AssumedValidThrough, b.LastInspectedOn)
	return err
}

func (s *Store) ServerTrafficPlanMap(ctx context.Context) (map[int64]ServerTrafficPlan, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT server_id, quota_bytes, accounting_mode, reset_anchor_on, reset_count, reset_unit, tracking_started_on FROM server_traffic_plans`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]ServerTrafficPlan{}
	for rows.Next() {
		var p ServerTrafficPlan
		var quota sql.NullInt64
		if err := rows.Scan(&p.ServerID, &quota, &p.AccountingMode, &p.ResetAnchorOn, &p.ResetCount, &p.ResetUnit, &p.TrackingStartedOn); err != nil {
			return nil, err
		}
		if quota.Valid {
			v := quota.Int64
			p.QuotaBytes = &v
		}
		out[p.ServerID] = p
	}
	return out, rows.Err()
}

func (s *Store) UpsertServerTrafficPlan(ctx context.Context, p ServerTrafficPlan) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO server_traffic_plans
		(server_id, quota_bytes, accounting_mode, reset_anchor_on, reset_count, reset_unit, tracking_started_on)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(server_id) DO UPDATE SET quota_bytes=excluded.quota_bytes, accounting_mode=excluded.accounting_mode,
		reset_anchor_on=excluded.reset_anchor_on, reset_count=excluded.reset_count, reset_unit=excluded.reset_unit,
		updated_at=CURRENT_TIMESTAMP`, p.ServerID, p.QuotaBytes, p.AccountingMode, p.ResetAnchorOn, p.ResetCount, p.ResetUnit, p.TrackingStartedOn)
	return err
}

func (s *Store) ServerNetworkUsage(ctx context.Context, serverID int64, from, through string) (txBytes, rxBytes int64, err error) {
	err = s.db.QueryRowContext(ctx, `SELECT COALESCE(SUM(tx_bytes),0), COALESCE(SUM(rx_bytes),0)
		FROM server_network_usage_daily WHERE server_id = ? AND usage_date >= ? AND usage_date <= ?`, serverID, from, through).Scan(&txBytes, &rxBytes)
	return
}

func (s *Store) InspectableBilling(ctx context.Context) ([]ServerBilling, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT server_id, enabled, COALESCE(provider_id, 0), amount_minor, currency,
		service_started_on, interval_count, interval_unit, next_renewal_on, status,
		assumed_valid_through, last_inspected_on, status_changed_at FROM server_billing WHERE enabled = 1`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ServerBilling
	for rows.Next() {
		b, err := scanBilling(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

func ParseDate(value string) (time.Time, error) { return time.Parse("2006-01-02", value) }

func DateString(value time.Time) string { return value.Format("2006-01-02") }

func (s *Store) ProviderByID(ctx context.Context, id int64) (Provider, error) {
	var p Provider
	err := s.db.QueryRowContext(ctx, `SELECT id, name, website_url FROM providers WHERE id = ?`, id).Scan(&p.ID, &p.Name, &p.WebsiteURL)
	if errors.Is(err, sql.ErrNoRows) {
		return Provider{}, ErrNotFound
	}
	return p, err
}

func ValidateInterval(count int, unit string) error {
	if count < 1 || count > 10000 {
		return fmt.Errorf("interval count must be between 1 and 10000")
	}
	switch unit {
	case "day", "month", "year":
		return nil
	}
	return fmt.Errorf("interval unit must be day, month, or year")
}

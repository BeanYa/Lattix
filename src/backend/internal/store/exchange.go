package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

type ExchangeRate struct {
	BaseCurrency  string `json:"base_currency"`
	QuoteCurrency string `json:"quote_currency"`
	Rate          string `json:"rate"`
	RateDate      string `json:"rate_date"`
	Source        string `json:"source"`
	FetchedAt     string `json:"fetched_at"`
}

type CustomExchangeRate struct {
	ID             int64  `json:"id"`
	SourceCurrency string `json:"source_currency"`
	SourceAmount   string `json:"source_amount"`
	TargetCurrency string `json:"target_currency"`
	TargetAmount   string `json:"target_amount"`
	Enabled        bool   `json:"enabled"`
	UpdatedAt      string `json:"updated_at"`
}

func (s *Store) ReplaceExchangeRates(ctx context.Context, rates []ExchangeRate) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, r := range rates {
		if _, err := tx.ExecContext(ctx, `INSERT INTO exchange_rates
			(base_currency, quote_currency, rate, rate_date, source, fetched_at) VALUES (?, ?, ?, ?, ?, ?)
			ON CONFLICT(base_currency, quote_currency) DO UPDATE SET rate=excluded.rate, rate_date=excluded.rate_date,
			source=excluded.source, fetched_at=excluded.fetched_at`, r.BaseCurrency, r.QuoteCurrency, r.Rate, r.RateDate, r.Source, r.FetchedAt); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) ExchangeRates(ctx context.Context) ([]ExchangeRate, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT base_currency, quote_currency, rate, rate_date, source, fetched_at FROM exchange_rates ORDER BY base_currency, quote_currency`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ExchangeRate
	for rows.Next() {
		var r ExchangeRate
		if err := rows.Scan(&r.BaseCurrency, &r.QuoteCurrency, &r.Rate, &r.RateDate, &r.Source, &r.FetchedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) LatestExchangeFetch(ctx context.Context) (time.Time, error) {
	var value sql.NullTime
	err := s.db.QueryRowContext(ctx, `SELECT MAX(fetched_at) FROM exchange_rates`).Scan(&value)
	if err != nil || !value.Valid {
		return time.Time{}, err
	}
	return value.Time, nil
}

func (s *Store) ListCustomExchangeRates(ctx context.Context) ([]CustomExchangeRate, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, source_currency, source_amount, target_currency, target_amount, enabled, updated_at FROM custom_exchange_rates ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CustomExchangeRate
	for rows.Next() {
		var r CustomExchangeRate
		if err := rows.Scan(&r.ID, &r.SourceCurrency, &r.SourceAmount, &r.TargetCurrency, &r.TargetAmount, &r.Enabled, &r.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) UpsertCustomExchangeRate(ctx context.Context, r CustomExchangeRate) (int64, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var existingID int64
	err = tx.QueryRowContext(ctx, `SELECT id FROM custom_exchange_rates WHERE source_currency = ?`, r.SourceCurrency).Scan(&existingID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}
	if err == nil && existingID != r.ID {
		return 0, errors.New("该源币种已存在自定义汇率")
	}
	if r.Enabled {
		if _, err := tx.ExecContext(ctx, `UPDATE custom_exchange_rates SET enabled = 0
			WHERE enabled = 1 AND target_currency = ? AND id <> ?`, r.TargetCurrency, r.ID); err != nil {
			return 0, err
		}
	}
	if r.ID == 0 {
		res, err := tx.ExecContext(ctx, `INSERT INTO custom_exchange_rates (source_currency, source_amount, target_currency, target_amount, enabled) VALUES (?, ?, ?, ?, ?)`, r.SourceCurrency, r.SourceAmount, r.TargetCurrency, r.TargetAmount, r.Enabled)
		if err != nil {
			return 0, err
		}
		r.ID, _ = res.LastInsertId()
	} else {
		res, err := tx.ExecContext(ctx, `UPDATE custom_exchange_rates SET source_currency=?, source_amount=?, target_currency=?, target_amount=?, enabled=?, updated_at=CURRENT_TIMESTAMP WHERE id=?`, r.SourceCurrency, r.SourceAmount, r.TargetCurrency, r.TargetAmount, r.Enabled, r.ID)
		if err != nil {
			return 0, err
		}
		if changed, _ := res.RowsAffected(); changed == 0 {
			return 0, ErrNotFound
		}
	}
	return r.ID, tx.Commit()
}

func (s *Store) DeleteCustomExchangeRate(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM custom_exchange_rates WHERE id = ?`, id)
	return err
}

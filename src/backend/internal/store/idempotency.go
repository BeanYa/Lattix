package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

type IdempotencyRecord struct {
	RequestHash  string
	ResponseJSON string
}

func (s *Store) IdempotencyRecord(
	ctx context.Context,
	operator, route, key string,
) (*IdempotencyRecord, error) {
	var record IdempotencyRecord
	err := s.db.QueryRowContext(ctx, `
		SELECT request_hash, response_json
		FROM rpc_idempotency
		WHERE operator = ? AND route = ? AND idempotency_key = ?`,
		operator, route, key,
	).Scan(&record.RequestHash, &record.ResponseJSON)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("query idempotency record: %w", err)
	}
	return &record, nil
}

func (s *Store) SaveIdempotencyRecord(
	ctx context.Context,
	operator, route, key, requestHash, responseJSON string,
) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO rpc_idempotency (
			operator, route, idempotency_key, request_hash, response_json
		) VALUES (?, ?, ?, ?, ?)`,
		operator, route, key, requestHash, responseJSON,
	)
	if err != nil {
		return fmt.Errorf("save idempotency record: %w", err)
	}
	return nil
}

func (s *Store) DeleteExpiredIdempotencyRecords(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `
		DELETE FROM rpc_idempotency
		WHERE created_at < datetime('now', '-24 hours')`)
	if err != nil {
		return fmt.Errorf("delete expired idempotency records: %w", err)
	}
	return nil
}

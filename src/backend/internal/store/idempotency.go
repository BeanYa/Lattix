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

var ErrIdempotencyReservationExists = errors.New("idempotency reservation already exists")

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

func (s *Store) ReserveIdempotencyRecord(
	ctx context.Context,
	operator, route, key, requestHash string,
) error {
	result, err := s.db.ExecContext(ctx, `
			INSERT INTO rpc_idempotency (
				operator, route, idempotency_key, request_hash, response_json
			) VALUES (?, ?, ?, ?, ?)
			ON CONFLICT(operator, route, idempotency_key) DO NOTHING`,
		operator, route, key, requestHash, "",
	)
	if err != nil {
		return fmt.Errorf("reserve idempotency record: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("reserve idempotency record: %w", err)
	}
	if rows != 1 {
		return ErrIdempotencyReservationExists
	}
	return nil
}

func (s *Store) CompleteIdempotencyRecord(
	ctx context.Context,
	operator, route, key, requestHash, responseJSON string,
) error {
	result, err := s.db.ExecContext(ctx, `
		UPDATE rpc_idempotency SET response_json = ?
		WHERE operator = ? AND route = ? AND idempotency_key = ?
			AND request_hash = ? AND response_json = ''`,
		responseJSON, operator, route, key, requestHash)
	if err != nil {
		return fmt.Errorf("complete idempotency record: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("complete idempotency record: %w", err)
	}
	if rows != 1 {
		return errors.New("complete idempotency record: reservation is missing or already complete")
	}
	return nil
}

func (s *Store) DeleteIdempotencyReservation(
	ctx context.Context,
	operator, route, key, requestHash string,
) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM rpc_idempotency
		WHERE operator = ? AND route = ? AND idempotency_key = ?
			AND request_hash = ? AND response_json = ''`, operator, route, key, requestHash)
	if err != nil {
		return fmt.Errorf("delete idempotency reservation: %w", err)
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

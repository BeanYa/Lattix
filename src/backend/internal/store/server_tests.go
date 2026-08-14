package store

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"lattix/shared"
)

var ErrServerTestInProgress = errors.New("store: server test already in progress")

type ServerTestTask struct {
	ServerID       int64
	TaskID         string
	Generation     int64
	RequestID      string
	Status         shared.ServerTestTaskStatus
	Categories     []shared.ServerTestCategory
	CatalogVersion string
	CatalogHashes  map[string]string
	Result         *shared.ServerTestReport
	ResultSHA256   string
	ErrorCode      string
	ErrorMessage   string
	AgentVersion   string
	CreatedAt      string
	AcceptedAt     *string
	StartedAt      *string
	CompletedAt    *string
	UpdatedAt      string
}

// EnqueueServerTest atomically replaces the previous terminal task and queues
// the Agent command. A non-terminal task prevents replacement.
func (s *Store) EnqueueServerTest(
	ctx context.Context,
	serverID int64,
	traceID string,
	categories []shared.ServerTestCategory,
	catalog shared.ServerTestCatalogSnapshot,
) (*ServerTestTask, int64, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, 0, err
	}
	defer tx.Rollback()

	var previousGeneration int64
	var previousStatus string
	err = tx.QueryRowContext(ctx,
		`SELECT generation, status FROM server_test_tasks WHERE server_id = ?`, serverID,
	).Scan(&previousGeneration, &previousStatus)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, 0, fmt.Errorf("read latest server test: %w", err)
	}
	if err == nil && !shared.ServerTestTaskStatus(previousStatus).Terminal() {
		return nil, 0, ErrServerTestInProgress
	}

	taskID := shared.NewMessageID()
	requestID := shared.NewMessageID()
	if traceID == "" {
		traceID = shared.NewMessageID()
	}
	generation := previousGeneration + 1
	payload := shared.ServerTestRunPayload{
		SchemaVersion: shared.ServerTestSchemaVersion,
		TaskID:        taskID, Generation: generation,
		Categories: append([]shared.ServerTestCategory(nil), categories...), Catalog: catalog,
	}
	if err := payload.Validate(); err != nil {
		return nil, 0, fmt.Errorf("validate server test command: %w", err)
	}
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return nil, 0, err
	}
	categoriesJSON, _ := json.Marshal(categories)
	hashesJSON, _ := json.Marshal(catalog.Hashes)

	if _, err := tx.ExecContext(ctx, `DELETE FROM server_test_result_chunks WHERE server_id = ?`, serverID); err != nil {
		return nil, 0, fmt.Errorf("clear server test chunks: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE commands SET status = ?, updated_at = CURRENT_TIMESTAMP
		WHERE server_id = ? AND type = ? AND status IN (?, ?)`,
		CommandStatusAbandoned, serverID, shared.TypeServerTestRun, CommandStatusQueued, CommandStatusSent); err != nil {
		return nil, 0, fmt.Errorf("supersede old server test command: %w", err)
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO server_test_tasks
		(server_id, task_id, generation, request_id, status, categories, catalog_version, catalog_hashes)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(server_id) DO UPDATE SET
			task_id=excluded.task_id, generation=excluded.generation, request_id=excluded.request_id,
			status=excluded.status, categories=excluded.categories,
			catalog_version=excluded.catalog_version, catalog_hashes=excluded.catalog_hashes,
			result_json=NULL, result_sha256='', error_code='', error_message='', agent_version='',
			created_at=CURRENT_TIMESTAMP, accepted_at=NULL, started_at=NULL, completed_at=NULL,
			updated_at=CURRENT_TIMESTAMP`,
		serverID, taskID, generation, requestID, shared.ServerTestQueued,
		string(categoriesJSON), catalog.Version, string(hashesJSON))
	if err != nil {
		return nil, 0, fmt.Errorf("create server test task: %w", err)
	}
	result, err := tx.ExecContext(ctx,
		`INSERT INTO commands (request_id, trace_id, server_id, type, data) VALUES (?, ?, ?, ?, ?)`,
		requestID, traceID, serverID, shared.TypeServerTestRun, string(payloadJSON))
	if err != nil {
		return nil, 0, fmt.Errorf("enqueue server test command: %w", err)
	}
	commandID, err := result.LastInsertId()
	if err != nil {
		return nil, 0, err
	}
	if err := tx.Commit(); err != nil {
		return nil, 0, err
	}
	task, err := s.ServerTestByServerID(ctx, serverID)
	return task, commandID, err
}

func (s *Store) ServerTestByServerID(ctx context.Context, serverID int64) (*ServerTestTask, error) {
	var task ServerTestTask
	var status, categoriesJSON, hashesJSON string
	var resultJSON sql.NullString
	var acceptedAt, startedAt, completedAt sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT server_id, task_id, generation, request_id, status,
		categories, catalog_version, catalog_hashes, result_json, result_sha256,
		error_code, error_message, agent_version, created_at, accepted_at, started_at,
		completed_at, updated_at FROM server_test_tasks WHERE server_id = ?`, serverID).Scan(
		&task.ServerID, &task.TaskID, &task.Generation, &task.RequestID, &status,
		&categoriesJSON, &task.CatalogVersion, &hashesJSON, &resultJSON, &task.ResultSHA256,
		&task.ErrorCode, &task.ErrorMessage, &task.AgentVersion, &task.CreatedAt,
		&acceptedAt, &startedAt, &completedAt, &task.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("read server test task: %w", err)
	}
	task.Status = shared.ServerTestTaskStatus(status)
	if err := json.Unmarshal([]byte(categoriesJSON), &task.Categories); err != nil {
		return nil, fmt.Errorf("decode server test categories: %w", err)
	}
	if err := json.Unmarshal([]byte(hashesJSON), &task.CatalogHashes); err != nil {
		return nil, fmt.Errorf("decode server test hashes: %w", err)
	}
	if resultJSON.Valid {
		var report shared.ServerTestReport
		if err := json.Unmarshal([]byte(resultJSON.String), &report); err != nil {
			return nil, fmt.Errorf("decode server test report: %w", err)
		}
		task.Result = &report
	}
	setNullableString := func(value sql.NullString) *string {
		if !value.Valid {
			return nil
		}
		copy := value.String
		return &copy
	}
	task.AcceptedAt = setNullableString(acceptedAt)
	task.StartedAt = setNullableString(startedAt)
	task.CompletedAt = setNullableString(completedAt)
	return &task, nil
}

// serverTestTransitions 声明式定义服务器测试任务状态机转换（§server-test）：
//
//	queued → accepted → running → succeeded | completed_with_errors
//	任一非终态 → failed（命令失败/死信）
//
// 进展帧允许同状态/回退幂等（Agent 重复帧、迟到 accepted 帧），因此 accepted/running
// 对全部非终态开放；终态由 generation/terminal 校验拒绝回退。
// 三条写入 SQL（UpdateServerTestState/FailServerTestCommand/finalizeServerTestResult）
// 的前置状态集合统一由本表推导（serverTestFromSet），不再散落硬编码。
var serverTestTransitions = map[shared.ServerTestTaskStatus]map[shared.ServerTestTaskStatus]bool{
	shared.ServerTestQueued: {
		shared.ServerTestAccepted:            true,
		shared.ServerTestRunning:             true,
		shared.ServerTestSucceeded:           true,
		shared.ServerTestCompletedWithErrors: true,
		shared.ServerTestFailed:              true,
	},
	shared.ServerTestAccepted: {
		shared.ServerTestAccepted:            true, // 重复帧幂等
		shared.ServerTestRunning:             true,
		shared.ServerTestSucceeded:           true,
		shared.ServerTestCompletedWithErrors: true,
		shared.ServerTestFailed:              true,
	},
	shared.ServerTestRunning: {
		shared.ServerTestAccepted:            true, // 迟到 accepted 帧容忍
		shared.ServerTestRunning:             true, // 重复帧幂等
		shared.ServerTestSucceeded:           true,
		shared.ServerTestCompletedWithErrors: true,
		shared.ServerTestFailed:              true,
	},
	shared.ServerTestSucceeded:           {},
	shared.ServerTestCompletedWithErrors: {},
	shared.ServerTestFailed:              {},
}

// serverTestStatusOrder 是转换表键的固定顺序（SQL IN 子句参数顺序确定）。
var serverTestStatusOrder = []shared.ServerTestTaskStatus{
	shared.ServerTestQueued, shared.ServerTestAccepted, shared.ServerTestRunning,
	shared.ServerTestSucceeded, shared.ServerTestCompletedWithErrors, shared.ServerTestFailed,
}

func validServerTestTransition(from, to shared.ServerTestTaskStatus) bool {
	if from == to {
		return true
	}
	targets, ok := serverTestTransitions[from]
	return ok && targets[to]
}

// serverTestFromSet 返回允许转入 target 状态的前置状态集合（由转换表推导，顺序确定）。
func serverTestFromSet(target shared.ServerTestTaskStatus) []shared.ServerTestTaskStatus {
	out := make([]shared.ServerTestTaskStatus, 0, len(serverTestStatusOrder))
	for _, from := range serverTestStatusOrder {
		if serverTestTransitions[from][target] {
			out = append(out, from)
		}
	}
	return out
}

// UpdateServerTestState persists lifecycle transitions, but not progress
// counters. A stale generation is reported as superseded.
func (s *Store) UpdateServerTestState(ctx context.Context, serverID int64, taskID string, generation int64, status shared.ServerTestTaskStatus) (bool, error) {
	if !status.Valid() || status.Terminal() {
		return false, fmt.Errorf("invalid non-terminal server test status %q", status)
	}
	timestampColumn := ""
	switch status {
	case shared.ServerTestAccepted:
		timestampColumn = ", accepted_at = COALESCE(accepted_at, CURRENT_TIMESTAMP)"
	case shared.ServerTestRunning:
		timestampColumn = ", accepted_at = COALESCE(accepted_at, CURRENT_TIMESTAMP), started_at = COALESCE(started_at, CURRENT_TIMESTAMP)"
	}
	fromSet := serverTestFromSet(status)
	placeholders := strings.Repeat("?,", len(fromSet))
	placeholders = placeholders[:len(placeholders)-1]
	args := []any{status, serverID, taskID, generation}
	for _, from := range fromSet {
		args = append(args, from)
	}
	result, err := s.db.ExecContext(ctx, `UPDATE server_test_tasks SET status = ?, updated_at = CURRENT_TIMESTAMP`+timestampColumn+
		` WHERE server_id = ? AND task_id = ? AND generation = ? AND status IN (`+placeholders+`)`,
		args...)
	if err != nil {
		return false, err
	}
	changed, err := result.RowsAffected()
	return changed > 0, err
}

func (s *Store) FailServerTestCommand(ctx context.Context, serverID int64, taskID string, generation int64, code, message string) error {
	fromSet := serverTestFromSet(shared.ServerTestFailed)
	placeholders := strings.Repeat("?,", len(fromSet))
	placeholders = placeholders[:len(placeholders)-1]
	args := []any{shared.ServerTestFailed, code, message, serverID, taskID, generation}
	for _, from := range fromSet {
		args = append(args, from)
	}
	_, err := s.db.ExecContext(ctx, `UPDATE server_test_tasks SET status = ?, error_code = ?,
		error_message = ?, completed_at = CURRENT_TIMESTAMP, updated_at = CURRENT_TIMESTAMP
		WHERE server_id = ? AND task_id = ? AND generation = ? AND status IN (`+placeholders+`)`,
		args...)
	return err
}

type ServerTestChunkOutcome string

const (
	ServerTestChunkAccepted   ServerTestChunkOutcome = "accepted"
	ServerTestChunkComplete   ServerTestChunkOutcome = "complete"
	ServerTestChunkSuperseded ServerTestChunkOutcome = "superseded"
)

func (s *Store) SaveServerTestResultChunk(ctx context.Context, serverID int64, payload shared.ServerTestResultChunkPayload) (ServerTestChunkOutcome, error) {
	if !shared.ValidMessageID(payload.TaskID) || payload.Generation < 1 || payload.Index < 0 || len(payload.Data) > 256<<10 {
		return "", errors.New("invalid server test result chunk")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	var currentTaskID string
	var currentGeneration int64
	var currentStatus shared.ServerTestTaskStatus
	var currentSHA256 string
	if err := tx.QueryRowContext(ctx, `SELECT task_id, generation, status, result_sha256 FROM server_test_tasks WHERE server_id = ?`, serverID).
		Scan(&currentTaskID, &currentGeneration, &currentStatus, &currentSHA256); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ServerTestChunkSuperseded, nil
		}
		return "", err
	}
	if currentTaskID != payload.TaskID || currentGeneration != payload.Generation {
		return ServerTestChunkSuperseded, nil
	}
	if currentStatus.Terminal() {
		if payload.Index == 0 && payload.Manifest != nil && payload.Manifest.SHA256 == currentSHA256 {
			return ServerTestChunkComplete, nil
		}
		return "", errors.New("server test already has an authoritative terminal result")
	}
	var manifestJSON any
	chunkCount := 0
	if payload.Manifest != nil {
		if payload.Index != 0 {
			return "", errors.New("result manifest must be attached to chunk zero")
		}
		if err := payload.Manifest.Validate(); err != nil {
			return "", err
		}
		if payload.Manifest.TaskID != payload.TaskID || payload.Manifest.Generation != payload.Generation {
			return "", errors.New("result manifest identity mismatch")
		}
		chunkCount = payload.Manifest.ChunkCount
		encoded, _ := json.Marshal(payload.Manifest)
		manifestJSON = string(encoded)
	} else {
		var storedManifest string
		if err := tx.QueryRowContext(ctx, `SELECT manifest FROM server_test_result_chunks
			WHERE server_id = ? AND task_id = ? AND generation = ? AND chunk_index = 0`,
			serverID, payload.TaskID, payload.Generation).Scan(&storedManifest); err != nil {
			return "", errors.New("result chunk arrived before manifest")
		}
		var manifest shared.ServerTestResultManifest
		if err := json.Unmarshal([]byte(storedManifest), &manifest); err != nil {
			return "", err
		}
		chunkCount = manifest.ChunkCount
		manifestJSON = nil
	}
	if payload.Index >= chunkCount {
		return "", errors.New("result chunk index out of range")
	}
	var existing []byte
	err = tx.QueryRowContext(ctx, `SELECT data FROM server_test_result_chunks
		WHERE server_id = ? AND task_id = ? AND generation = ? AND chunk_index = ?`,
		serverID, payload.TaskID, payload.Generation, payload.Index).Scan(&existing)
	if err == nil {
		if !bytes.Equal(existing, payload.Data) {
			return "", errors.New("conflicting duplicate result chunk")
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return "", err
	} else {
		if _, err := tx.ExecContext(ctx, `INSERT INTO server_test_result_chunks
			(server_id, task_id, generation, chunk_index, chunk_count, manifest, data)
			VALUES (?, ?, ?, ?, ?, ?, ?)`, serverID, payload.TaskID, payload.Generation,
			payload.Index, chunkCount, manifestJSON, payload.Data); err != nil {
			return "", err
		}
	}
	var received int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM server_test_result_chunks
		WHERE server_id = ? AND task_id = ? AND generation = ?`,
		serverID, payload.TaskID, payload.Generation).Scan(&received); err != nil {
		return "", err
	}
	if received < chunkCount {
		return ServerTestChunkAccepted, tx.Commit()
	}
	if err := finalizeServerTestResult(ctx, tx, serverID, payload.TaskID, payload.Generation); err != nil {
		return "", err
	}
	return ServerTestChunkComplete, tx.Commit()
}

func finalizeServerTestResult(ctx context.Context, tx *sql.Tx, serverID int64, taskID string, generation int64) error {
	rows, err := tx.QueryContext(ctx, `SELECT chunk_index, manifest, data FROM server_test_result_chunks
		WHERE server_id = ? AND task_id = ? AND generation = ? ORDER BY chunk_index`, serverID, taskID, generation)
	if err != nil {
		return err
	}
	defer rows.Close()
	var compressed bytes.Buffer
	var manifest shared.ServerTestResultManifest
	expectedIndex := 0
	for rows.Next() {
		var index int
		var manifestJSON sql.NullString
		var data []byte
		if err := rows.Scan(&index, &manifestJSON, &data); err != nil {
			return err
		}
		if index != expectedIndex {
			return errors.New("server test result has missing chunk")
		}
		if index == 0 {
			if !manifestJSON.Valid || json.Unmarshal([]byte(manifestJSON.String), &manifest) != nil {
				return errors.New("server test result manifest is missing")
			}
		}
		compressed.Write(data)
		expectedIndex++
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if compressed.Len() != manifest.CompressedSize || expectedIndex != manifest.ChunkCount {
		return errors.New("server test compressed size mismatch")
	}
	reader, err := gzip.NewReader(bytes.NewReader(compressed.Bytes()))
	if err != nil {
		return fmt.Errorf("open server test result gzip: %w", err)
	}
	resultJSON, err := io.ReadAll(io.LimitReader(reader, 8<<20+1))
	closeErr := reader.Close()
	if err != nil {
		return fmt.Errorf("read server test result gzip: %w", err)
	}
	if closeErr != nil {
		return fmt.Errorf("close server test result gzip: %w", closeErr)
	}
	if len(resultJSON) != manifest.UncompressedSize || len(resultJSON) > 8<<20 {
		return errors.New("server test uncompressed size mismatch")
	}
	sum := sha256.Sum256(resultJSON)
	if hex.EncodeToString(sum[:]) != manifest.SHA256 {
		return errors.New("server test result sha256 mismatch")
	}
	var report shared.ServerTestReport
	if err := json.Unmarshal(resultJSON, &report); err != nil {
		return fmt.Errorf("decode server test report: %w", err)
	}
	if report.SchemaVersion != shared.ServerTestSchemaVersion || report.TaskID != taskID ||
		report.Generation != generation || report.Status != manifest.Status {
		return errors.New("server test report identity mismatch")
	}
	fromSet := serverTestFromSet(manifest.Status)
	if len(fromSet) == 0 {
		return fmt.Errorf("server test result manifest has non-terminal status %q", manifest.Status)
	}
	placeholders := strings.Repeat("?,", len(fromSet))
	placeholders = placeholders[:len(placeholders)-1]
	args := []any{manifest.Status, string(resultJSON), manifest.SHA256, manifest.ErrorCode,
		manifest.ErrorMessage, manifest.AgentVersion, serverID, taskID, generation}
	for _, from := range fromSet {
		args = append(args, from)
	}
	result, err := tx.ExecContext(ctx, `UPDATE server_test_tasks SET status = ?, result_json = ?,
		result_sha256 = ?, error_code = ?, error_message = ?, agent_version = ?,
		completed_at = CURRENT_TIMESTAMP, updated_at = CURRENT_TIMESTAMP
		WHERE server_id = ? AND task_id = ? AND generation = ? AND status IN (`+placeholders+`)`,
		args...)
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil || changed != 1 {
		return errors.New("server test result was superseded")
	}
	_, err = tx.ExecContext(ctx, `DELETE FROM server_test_result_chunks
		WHERE server_id = ? AND task_id = ? AND generation = ?`, serverID, taskID, generation)
	return err
}

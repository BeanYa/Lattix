package servertest

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"lattix/agent/internal/fileutil"
	"lattix/shared"
)

type Sender func(shared.Envelope) error

// taskJournalState is the persisted journal state (taskJournal.State). The journal
// is stored as JSON, so the string values are part of the on-disk format and must
// not change. Transition table:
//
//	running        → result_pending (result persisted, awaiting delivery)
//	result_pending → completed      (panel published the full result)
//	completed      → (terminal marker, kept for idempotent re-accept)
type taskJournalState string

const (
	taskStateRunning       taskJournalState = "running"
	taskStateResultPending taskJournalState = "result_pending"
	taskStateCompleted     taskJournalState = "completed"
)

var taskJournalTransitions = map[taskJournalState]map[taskJournalState]bool{
	taskStateRunning: {
		taskStateResultPending: true,
	},
	taskStateResultPending: {
		taskStateCompleted: true,
	},
	taskStateCompleted: {},
}

// Valid reports whether s is a known journal state.
func (s taskJournalState) Valid() bool {
	_, ok := taskJournalTransitions[s]
	return ok
}

// validTaskJournalTransition reports whether the from → to journal transition is
// legal (same-state is idempotent).
func validTaskJournalTransition(from, to taskJournalState) bool {
	if from == to {
		return true
	}
	targets, ok := taskJournalTransitions[from]
	return ok && targets[to]
}

type taskJournal struct {
	Version   int                              `json:"version"`
	State     taskJournalState                 `json:"state"` // running|result_pending|completed
	BootID    string                           `json:"boot_id"`
	Payload   shared.ServerTestRunPayload      `json:"payload"`
	Manifest  *shared.ServerTestResultManifest `json:"manifest,omitempty"`
	UpdatedAt time.Time                        `json:"updated_at"`
}

type Manager struct {
	mu               sync.Mutex
	dataDir          string
	journalPath      string
	resultPath       string
	agentVersion     string
	journal          *taskJournal
	progress         *shared.ServerTestProgressPayload
	lastProgressSent time.Time
	sender           Sender
	pending          map[string]chan shared.Envelope
	wake             chan struct{}
	idle             chan struct{}
	executionDone    chan struct{}
}

func NewManager(dataDir, agentVersion string) (*Manager, error) {
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, err
	}
	manager := &Manager{
		dataDir: dataDir, journalPath: filepath.Join(dataDir, "server-test-task.json"),
		resultPath:   filepath.Join(dataDir, "server-test-result.json.gz"),
		agentVersion: agentVersion, pending: make(map[string]chan shared.Envelope),
		wake: make(chan struct{}, 1), idle: make(chan struct{}),
	}
	if err := manager.load(); err != nil {
		return nil, err
	}
	if manager.journal == nil || manager.journal.State == taskStateCompleted {
		if manager.journal != nil {
			_ = os.Remove(manager.resultPath)
		}
		close(manager.idle)
		manager.executionDone = make(chan struct{})
		close(manager.executionDone)
	} else if manager.journal.State == taskStateResultPending {
		manager.executionDone = make(chan struct{})
		close(manager.executionDone)
	} else if manager.journal.State == taskStateRunning {
		manager.executionDone = make(chan struct{})
		if err := manager.recordInterruptedResult(); err != nil {
			return nil, err
		}
	}
	go manager.deliveryLoop()
	manager.signal()
	return manager, nil
}

func (m *Manager) Accept(payload shared.ServerTestRunPayload) error {
	if err := payload.Validate(); err != nil {
		return err
	}
	m.mu.Lock()
	if m.journal != nil {
		if m.journal.Payload.TaskID == payload.TaskID && m.journal.Payload.Generation == payload.Generation {
			m.mu.Unlock()
			return nil
		}
		if m.journal.State == taskStateCompleted {
			m.journal = nil
		} else {
			m.mu.Unlock()
			return errors.New("another server test is already running or awaiting delivery")
		}
	}
	m.idle = make(chan struct{})
	m.executionDone = make(chan struct{})
	m.journal = &taskJournal{
		Version: 1, State: taskStateRunning, BootID: currentBootID(), Payload: payload, UpdatedAt: time.Now().UTC(),
	}
	if err := m.saveJournalLocked(); err != nil {
		m.journal = nil
		close(m.idle)
		m.mu.Unlock()
		return fmt.Errorf("persist accepted server test: %w", err)
	}
	m.mu.Unlock()
	go m.run(payload)
	return nil
}

func (m *Manager) Busy() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.journal != nil && m.journal.State != taskStateCompleted
}

func (m *Manager) WaitIdle(ctx context.Context) error {
	m.mu.Lock()
	idle := m.idle
	m.mu.Unlock()
	select {
	case <-idle:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (m *Manager) WaitExecution(ctx context.Context) error {
	m.mu.Lock()
	done := m.executionDone
	m.mu.Unlock()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (m *Manager) Attach(sender Sender) {
	m.mu.Lock()
	m.sender = sender
	progress := m.progress
	m.mu.Unlock()
	if progress != nil {
		_ = sender(progressEnvelope(*progress))
	}
	m.signal()
}

func (m *Manager) Detach() {
	m.mu.Lock()
	m.sender = nil
	m.mu.Unlock()
}

func (m *Manager) HandleResponse(envelope shared.Envelope) bool {
	if envelope.Kind != shared.KindResponse || envelope.Type != shared.TypeServerTestResult {
		return false
	}
	m.mu.Lock()
	channel := m.pending[envelope.RequestID]
	m.mu.Unlock()
	if channel == nil {
		return false
	}
	select {
	case channel <- envelope:
	default:
	}
	return true
}

func (m *Manager) run(payload shared.ServerTestRunPayload) {
	ctx, cancel := context.WithTimeout(context.Background(), taskTimeout+time.Minute)
	defer cancel()
	report, err := RunWorker(ctx, m.dataDir, m.agentVersion, payload, m.reportProgress)
	if err != nil {
		now := time.Now().UTC().Format(time.RFC3339Nano)
		report = shared.ServerTestReport{
			SchemaVersion: shared.ServerTestSchemaVersion,
			TaskID:        payload.TaskID, Generation: payload.Generation,
			Status: shared.ServerTestFailed, StartedAt: now, CompletedAt: now,
			AgentVersion: m.agentVersion, CatalogVersion: payload.Catalog.Version,
			Environment: inspectEnvironment("degraded", "worker process failed"),
			ErrorCode:   "worker_failed", ErrorMessage: err.Error(),
		}
	}
	if err := m.persistResult(report); err != nil {
		// The task remains running in the journal. A restart will convert it to a
		// precise interrupted result instead of silently losing the task.
		return
	}
	m.signal()
}

func (m *Manager) reportProgress(progress shared.ServerTestProgressPayload) {
	m.mu.Lock()
	if m.journal == nil || m.journal.Payload.TaskID != progress.TaskID || m.journal.State != taskStateRunning {
		m.mu.Unlock()
		return
	}
	copy := progress
	m.progress = &copy
	sender := m.sender
	if !m.lastProgressSent.IsZero() && time.Since(m.lastProgressSent) < time.Second {
		sender = nil
	} else if sender != nil {
		m.lastProgressSent = time.Now()
	}
	m.mu.Unlock()
	if sender != nil {
		_ = sender(progressEnvelope(progress))
	}
}

func progressEnvelope(progress shared.ServerTestProgressPayload) shared.Envelope {
	id := shared.NewMessageID()
	return shared.Envelope{
		Kind: shared.KindEvent, Type: shared.TypeServerTestProgress,
		RequestID: id, TraceID: id, Data: mustMarshal(progress),
	}
}

func (m *Manager) persistResult(report shared.ServerTestReport) error {
	raw, err := json.Marshal(report)
	if err != nil {
		return err
	}
	if len(raw) > 8<<20 {
		return errors.New("server test report exceeds 8 MiB")
	}
	var compressed bytes.Buffer
	writer := gzip.NewWriter(&compressed)
	if _, err := writer.Write(raw); err != nil {
		return err
	}
	if err := writer.Close(); err != nil {
		return err
	}
	sum := sha256.Sum256(raw)
	chunkCount := (compressed.Len() + (256 << 10) - 1) / (256 << 10)
	manifest := shared.ServerTestResultManifest{
		SchemaVersion: shared.ServerTestSchemaVersion,
		TaskID:        report.TaskID, Generation: report.Generation, Status: report.Status,
		AgentVersion: report.AgentVersion, CatalogVersion: report.CatalogVersion,
		UncompressedSize: len(raw), CompressedSize: compressed.Len(),
		SHA256: hex.EncodeToString(sum[:]), ChunkCount: chunkCount,
		ErrorCode: report.ErrorCode, ErrorMessage: report.ErrorMessage,
	}
	if err := manifest.Validate(); err != nil {
		return err
	}
	if err := fileutil.WriteFileAtomic(m.resultPath, compressed.Bytes(), 0o600); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.journal == nil || m.journal.Payload.TaskID != report.TaskID || m.journal.Payload.Generation != report.Generation {
		return errors.New("server test result was superseded locally")
	}
	if !validTaskJournalTransition(m.journal.State, taskStateResultPending) {
		return fmt.Errorf("invalid server test journal transition %q → %q", m.journal.State, taskStateResultPending)
	}
	m.journal.State = taskStateResultPending
	m.journal.Manifest = &manifest
	m.journal.UpdatedAt = time.Now().UTC()
	m.progress = nil
	if err := m.saveJournalLocked(); err != nil {
		return err
	}
	select {
	case <-m.executionDone:
	default:
		close(m.executionDone)
	}
	return nil
}

func (m *Manager) deliveryLoop() {
	for {
		<-m.wake
		for {
			m.mu.Lock()
			ready := m.journal != nil && m.journal.State == taskStateResultPending && m.journal.Manifest != nil && m.sender != nil
			m.mu.Unlock()
			if !ready {
				break
			}
			if err := m.deliverOnce(); err != nil {
				time.AfterFunc(10*time.Second, m.signal)
				break
			}
		}
	}
}

func (m *Manager) deliverOnce() error {
	m.mu.Lock()
	journal := *m.journal
	manifest := *m.journal.Manifest
	sender := m.sender
	m.mu.Unlock()
	compressed, err := os.ReadFile(m.resultPath)
	if err != nil {
		return err
	}
	if len(compressed) != manifest.CompressedSize {
		return errors.New("pending result size does not match journal")
	}
	for index := 0; index < manifest.ChunkCount; index++ {
		start := index * (256 << 10)
		end := start + (256 << 10)
		if end > len(compressed) {
			end = len(compressed)
		}
		payload := shared.ServerTestResultChunkPayload{
			TaskID: journal.Payload.TaskID, Generation: journal.Payload.Generation,
			Index: index, Data: compressed[start:end],
		}
		if index == 0 {
			payload.Manifest = &manifest
		}
		requestID := shared.NewMessageID()
		responseChannel := make(chan shared.Envelope, 1)
		m.mu.Lock()
		m.pending[requestID] = responseChannel
		m.mu.Unlock()
		envelope := shared.Envelope{
			Kind: shared.KindRequest, Type: shared.TypeServerTestResult,
			RequestID: requestID, TraceID: requestID, Data: mustMarshal(payload),
		}
		if err := sender(envelope); err != nil {
			m.removePending(requestID)
			return err
		}
		timer := time.NewTimer(20 * time.Second)
		var response shared.Envelope
		select {
		case response = <-responseChannel:
			timer.Stop()
		case <-timer.C:
			m.removePending(requestID)
			return errors.New("panel did not acknowledge server test result chunk")
		}
		m.removePending(requestID)
		if response.Code != shared.CodeOK {
			return fmt.Errorf("panel rejected server test result: %s: %s", response.Code, response.Message)
		}
		var ack shared.ServerTestResultACK
		if err := json.Unmarshal(response.Data, &ack); err != nil {
			return err
		}
		if ack.Status == "superseded" || ack.Status == "complete" {
			return m.completeLocal(journal.Payload.TaskID, journal.Payload.Generation)
		}
		if ack.Status != "accepted" {
			return fmt.Errorf("unknown server test result ACK %q", ack.Status)
		}
	}
	return errors.New("panel did not publish the complete server test result")
}

func (m *Manager) removePending(requestID string) {
	m.mu.Lock()
	delete(m.pending, requestID)
	m.mu.Unlock()
}

func (m *Manager) completeLocal(taskID string, generation int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.journal == nil || m.journal.Payload.TaskID != taskID || m.journal.Payload.Generation != generation {
		return nil
	}
	if m.journal.State == taskStateCompleted {
		return nil
	}
	if !validTaskJournalTransition(m.journal.State, taskStateCompleted) {
		return fmt.Errorf("invalid server test journal transition %q → %q", m.journal.State, taskStateCompleted)
	}
	m.journal.State = taskStateCompleted
	m.journal.Manifest = nil
	m.journal.UpdatedAt = time.Now().UTC()
	if err := m.saveJournalLocked(); err != nil {
		return err
	}
	m.progress = nil
	close(m.idle)
	if err := os.Remove(m.resultPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func (m *Manager) load() error {
	raw, err := os.ReadFile(m.journalPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var journal taskJournal
	if err := json.Unmarshal(raw, &journal); err != nil || journal.Version != 1 || journal.Payload.Validate() != nil ||
		!journal.State.Valid() {
		return fmt.Errorf("invalid server test journal: %w", err)
	}
	if journal.State == taskStateResultPending && journal.Manifest == nil {
		return errors.New("server test result journal has no manifest")
	}
	m.journal = &journal
	return nil
}

func (m *Manager) recordInterruptedResult() error {
	m.mu.Lock()
	journal := *m.journal
	m.mu.Unlock()
	code := "execution_interrupted"
	message := "the previous Agent process stopped before the test completed"
	current := currentBootID()
	if journal.BootID != "" && current != "" {
		if journal.BootID == current {
			code, message = "agent_restarted", "Agent restarted before the test completed"
		} else {
			code, message = "host_rebooted", "the host rebooted before the test completed"
		}
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	return m.persistResult(shared.ServerTestReport{
		SchemaVersion: shared.ServerTestSchemaVersion,
		TaskID:        journal.Payload.TaskID, Generation: journal.Payload.Generation,
		Status: shared.ServerTestFailed, StartedAt: journal.UpdatedAt.Format(time.RFC3339Nano), CompletedAt: now,
		AgentVersion: m.agentVersion, CatalogVersion: journal.Payload.Catalog.Version,
		Environment: inspectEnvironment("degraded", "previous worker was interrupted"),
		ErrorCode:   code, ErrorMessage: message,
	})
}

func (m *Manager) saveJournalLocked() error {
	encoded, err := json.MarshalIndent(m.journal, "", "  ")
	if err != nil {
		return err
	}
	return fileutil.WriteFileAtomic(m.journalPath, encoded, 0o600)
}

func (m *Manager) signal() {
	select {
	case m.wake <- struct{}{}:
	default:
	}
}

func currentBootID() string {
	raw, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil {
		return ""
	}
	return string(bytes.TrimSpace(raw))
}

func mustMarshal(value any) json.RawMessage {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return encoded
}

package panel

import (
	"context"
	"log"
	"sync"
	"time"

	"lattix/backend/internal/store"
)

type chainWorkerState string

const (
	workerIdle       chainWorkerState = "idle"
	workerEvaluating chainWorkerState = "evaluating"
	workerDeploying  chainWorkerState = "deploying"
	workerFailed     chainWorkerState = "failed"
	workerPaused     chainWorkerState = "paused"
	workerStopped    chainWorkerState = "stopped"
)

// chainWorkerSerializes 是每条含编排图的链一个 worker；无图的链不建 worker。
type chainWorkerManager struct {
	o       *AutoOrchestrator
	mu      sync.Mutex
	workers map[int64]*chainWorker
	ctx     context.Context
}

func newChainWorkerManager(o *AutoOrchestrator) *chainWorkerManager {
	return &chainWorkerManager{o: o, workers: make(map[int64]*chainWorker)}
}

func (m *chainWorkerManager) Start(ctx context.Context) {
	m.mu.Lock()
	m.ctx = ctx
	m.mu.Unlock()
}

// Ensure 只为有编排图的链创建 worker；图被删空时停止并回收。
func (m *chainWorkerManager) Ensure(chainID int64) bool {
	graphs, err := m.o.s.st.ChainHopGraphs(context.Background(), chainID)
	if err != nil || len(graphs) == 0 {
		m.Stop(chainID)
		return false
	}
	m.mu.Lock()
	if _, ok := m.workers[chainID]; ok {
		m.mu.Unlock()
		return true
	}
	ctx := m.ctx
	if ctx == nil {
		m.mu.Unlock()
		return false
	}
	worker := &chainWorker{chainID: chainID, o: m.o, wakeCh: make(chan struct{}, 1), state: workerIdle}
	m.workers[chainID] = worker
	m.mu.Unlock()
	go worker.run(ctx)
	return true
}

func (m *chainWorkerManager) worker(chainID int64) *chainWorker {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.workers[chainID]
}

func (m *chainWorkerManager) Has(chainID int64) bool {
	return m.worker(chainID) != nil
}

func (m *chainWorkerManager) Wake(chainID int64) {
	if w := m.worker(chainID); w != nil {
		w.wake()
	}
}

func (m *chainWorkerManager) Pause(chainID int64) {
	if w := m.worker(chainID); w != nil {
		w.setState(workerPaused)
	}
}

func (m *chainWorkerManager) Resume(chainID int64) {
	if w := m.worker(chainID); w != nil {
		w.resume(m.ctx)
	}
}

func (m *chainWorkerManager) Stop(chainID int64) {
	m.mu.Lock()
	w := m.workers[chainID]
	delete(m.workers, chainID)
	m.mu.Unlock()
	if w != nil {
		w.setState(workerStopped)
	}
}

func (m *chainWorkerManager) State(chainID int64) chainWorkerState {
	if w := m.worker(chainID); w != nil {
		return w.stateOf()
	}
	return workerStopped
}

func (m *chainWorkerManager) WaitSettled(ctx context.Context, chainID int64, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		state := m.State(chainID)
		if state != workerEvaluating && state != workerDeploying {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(25 * time.Millisecond):
		}
	}
	return context.DeadlineExceeded
}

type chainWorker struct {
	chainID int64
	o       *AutoOrchestrator
	wakeCh  chan struct{}

	mu    sync.Mutex
	state chainWorkerState
	err   string
}

func (w *chainWorker) stateOf() chainWorkerState {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.state
}

func (w *chainWorker) setState(state chainWorkerState) {
	w.mu.Lock()
	w.state = state
	w.mu.Unlock()
}

func (w *chainWorker) wake() {
	select {
	case w.wakeCh <- struct{}{}:
	default:
	}
}

func (w *chainWorker) resume(ctx context.Context) {
	w.mu.Lock()
	w.state = workerIdle
	w.err = ""
	w.mu.Unlock()
	if ctx == nil {
		ctx = context.Background()
	}
	_ = w.o.s.st.SetChainOrchestrationError(ctx, w.chainID, "")
	w.wake()
}

func (w *chainWorker) run(ctx context.Context) {
	ticker := time.NewTicker(w.o.workerTick)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-w.wakeCh:
		}
		w.step(ctx)
	}
}

func (w *chainWorker) step(ctx context.Context) {
	if state := w.stateOf(); state == workerPaused || state == workerFailed || state == workerStopped {
		return
	}
	chain, err := w.o.s.st.ChainByID(ctx, w.chainID)
	if err != nil {
		w.setState(workerStopped)
		return
	}
	graphs, err := w.o.s.st.ChainHopGraphs(ctx, w.chainID)
	if err != nil || len(graphs) == 0 {
		w.setState(workerStopped)
		return
	}
	if chain.DesiredRevisionID != 0 || chain.Status == store.ChainStatusApplying {
		w.setState(workerDeploying)
		return
	}
	w.setState(workerEvaluating)
	if err := w.o.EvaluateChain(ctx, w.chainID); err != nil {
		w.fail(ctx, err)
		return
	}
	w.setState(workerIdle)
}

func (w *chainWorker) fail(ctx context.Context, err error) {
	message := err.Error()
	w.mu.Lock()
	w.state = workerFailed
	w.err = message
	w.mu.Unlock()
	_ = w.o.s.st.SetChainOrchestrationError(ctx, w.chainID, message)
	log.Printf("auto_orchestrator: chain %d paused: %v", w.chainID, err)
}

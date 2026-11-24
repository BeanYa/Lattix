package panel

import (
	"context"
	"fmt"
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
	wg      sync.WaitGroup
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
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		worker.run(ctx)
	}()
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

// Wait 等待所有 worker goroutine 退出（调用方应先 cancel Start 传入的 ctx）。
// 退出前不要关闭库：WAL 下仍存活的连接会在库文件删除后重建 -shm。
func (m *chainWorkerManager) Wait(timeout time.Duration) bool {
	done := make(chan struct{})
	go func() {
		m.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return true
	case <-time.After(timeout):
		return false
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
	if chain.Status == store.ChainStatusFailed || chain.Status == store.ChainStatusActiveFailed {
		for _, g := range graphs {
			if g.SyncPending {
				w.fail(ctx, fmt.Errorf("模板同步部署失败：%s", chain.Error))
				return
			}
		}
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
	// 先落库错误、再发布状态：面板与测试都以 state=failed 为信号去读错误，
	// 反序会读到空错误（负载高的 CI 上偶发失败）。
	if perr := w.o.s.st.SetChainOrchestrationError(ctx, w.chainID, message); perr != nil {
		log.Printf("auto_orchestrator: chain %d persist orchestration error: %v", w.chainID, perr)
	}
	w.mu.Lock()
	w.state = workerFailed
	w.err = message
	w.mu.Unlock()
	log.Printf("auto_orchestrator: chain %d paused: %v", w.chainID, err)
}

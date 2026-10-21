package dispatch

// 清理任务自愈（reconcile）：旧 revision 的清理（remove_*）命令面向已下线/已删除的
// 服务器时不会 ack，若只按“清理任务完成”推导链状态，链会永久停在 cleanup_pending
//（用户上报：等待清理 + 旧 revision 配置等待清理 + 清理任务在队列中）。
// 本文件按 §21.1 清理阶段语义收敛：
//   - 目标服务器离线/已删除不阻塞链状态（remove 命令保留在 commands 队列，重连后照常下发）；
//   - failed/abandoned 为终态，不阻塞；
//   - 无阻塞任务（或已无任务）时 cleanup_pending → active（经 chainFSM，见 refreshCleanupStatus）；
//   - 自愈任务队列：废弃已删服务器任务、补入 pending 任务、复位悬空/终态命令后重入队。
// 所有链状态变更仍经 chain_fsm.go（Transition/Evaluate），本文件不直接写链状态。

import (
	"context"
	"errors"
	"log"

	"lattix/backend/internal/store"
)

// ReconcileChainCleanups 清理任务全量自愈入口：启动（ResumeChains）、Agent 重连
// （OnAgentConnect）与 60s 周期 ticker（main.go，与 ReconcileStaleEndpoints 并列）调用。
// 并发运行被抑制：ticker 与重连可能同时触发，单次运行已覆盖全部链，重叠运行无意义。
func (d *Dispatcher) ReconcileChainCleanups(ctx context.Context) {
	if !d.cleanupReconcileMu.TryLock() {
		log.Printf("dispatch: cleanup reconcile already running, skip overlapping run")
		return
	}
	defer d.cleanupReconcileMu.Unlock()

	chains, err := d.st.ListChains(ctx)
	if err != nil {
		log.Printf("dispatch: reconcile chain cleanups: list chains: %v", err)
		return
	}
	for _, chain := range chains {
		// 清理任务只属于“已发布且无待发布编辑”的 revision（与 refreshCleanupStatus 同语义）：
		// desired_revision_id 非零表示有编辑/强制发布待编排，交给编排路径处理。
		if chain.PublishedRevisionID == 0 || chain.DesiredRevisionID != 0 {
			continue
		}
		revision, err := d.st.PublishedChainRevision(ctx, chain.ID)
		if err != nil {
			if !errors.Is(err, store.ErrNotFound) {
				log.Printf("dispatch: reconcile chain %d published revision: %v", chain.ID, err)
			}
			continue
		}
		d.reconcileRevisionCleanup(ctx, revision)
	}
}

// reconcileRevisionCleanup 自愈单条已发布 revision 的清理任务，然后刷新链状态。
func (d *Dispatcher) reconcileRevisionCleanup(ctx context.Context, revision *store.ChainRevision) {
	tasks, err := d.st.RevisionTasks(ctx, revision.ID)
	if err != nil {
		log.Printf("dispatch: revision %d cleanup tasks: %v", revision.ID, err)
		return
	}
	for _, task := range tasks {
		if task.Phase != "cleanup" {
			continue
		}
		switch task.Status {
		case store.RevisionTaskPending:
			// 发布后入队前崩溃/服务器删除竞态：补入队（目标不存在则废弃）。
			d.enqueueCleanupTask(ctx, revision, task)
		case store.RevisionTaskQueued:
			if _, err := d.st.ServerByID(ctx, task.ServerID); err != nil {
				if errors.Is(err, store.ErrNotFound) {
					d.abandonCleanupTask(ctx, task, "服务器已删除")
				} else {
					log.Printf("dispatch: cleanup task %d server %d: %v", task.ID, task.ServerID, err)
				}
				continue
			}
			stale, err := d.cleanupTaskCommandStale(ctx, task)
			if err != nil {
				log.Printf("dispatch: cleanup task %d command %d: %v", task.ID, task.CommandID, err)
				continue
			}
			if !stale {
				continue // 命令在途/有效，等回执
			}
			reset, err := d.st.ResetQueuedRevisionTask(ctx, task.ID, task.CommandID)
			if err != nil {
				log.Printf("dispatch: reset cleanup task %d: %v", task.ID, err)
				continue
			}
			if !reset {
				continue // CAS 失败：并发已改写（回执/级联），下一轮再判
			}
			log.Printf("dispatch: cleanup task %d (%s) re-enqueued after stale command %d",
				task.ID, task.TaskKey, task.CommandID)
			task.Status = store.RevisionTaskPending
			task.CommandID = 0
			d.enqueueCleanupTask(ctx, revision, task)
		}
	}
	d.refreshCleanupStatus(ctx, revision.ID)
}

// cleanupTaskCommandStale 报告 queued 清理任务关联的命令是否已不可用：command_id 悬空
// （命令行被级联删除）或命令已终态（回执先于任务回写的崩溃窗口）。queued/sent 视为在途。
func (d *Dispatcher) cleanupTaskCommandStale(ctx context.Context, task store.ChainRevisionTask) (bool, error) {
	if task.CommandID == 0 {
		return true, nil
	}
	command, err := d.st.CommandByID(ctx, task.CommandID)
	if errors.Is(err, store.ErrNotFound) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	switch command.Status {
	case store.CommandStatusAcked, store.CommandStatusFailed, store.CommandStatusAbandoned:
		return true, nil
	}
	return false, nil
}

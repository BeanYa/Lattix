package dispatch

// 清理任务自愈（reconcile）：旧 revision 的清理（remove_*）命令面向已下线/已删除的
// 服务器时不会 ack，若只按“清理任务完成”推导链状态，链会永久停在 cleanup_pending
//（用户上报：等待清理 + 旧 revision 配置等待清理 + 清理任务在队列中）。
// 本文件按 §21.1 清理阶段语义收敛：
//   - 目标服务器离线/已删除不阻塞链状态（remove 命令保留在 commands 队列，重连后照常下发）；
//   - failed/abandoned 为终态，不阻塞；
//   - 无阻塞任务（或已无任务）时 cleanup_pending → active（经 chainFSM，见 refreshCleanupStatus）；
//   - 自愈任务队列：废弃已删服务器任务、补入 pending 任务、复位悬空命令后重入队、补写终态结果、重发超时命令。
// 所有链状态变更仍经 chain_fsm.go（Transition/Evaluate），本文件不直接写链状态。

import (
	"context"
	"errors"
	"log"
	"time"

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
			command, err := d.st.CommandByID(ctx, task.CommandID)
			if err == nil {
				d.reconcileCleanupCommand(ctx, command)
				continue
			}
			if !errors.Is(err, store.ErrNotFound) {
				log.Printf("dispatch: cleanup task %d command %d: %v", task.ID, task.CommandID, err)
				continue
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

// cleanupCommandSentTimeout 是清理命令「已投递但迟迟无回执」的判定阈值：超时后复位同一
// 命令并重发（清理 remove_* 命令幂等，重发安全；attempts 递增直至死信）。仅用于 cleanup
// 任务，阈值可放宽（覆盖连接保持健康但回执丢失：Flush 只取 queued，sent 仅靠重连复位）。
// var 而非 const：测试可临时改小/置零。
var cleanupCommandSentTimeout = 2 * time.Minute

func (d *Dispatcher) reconcileCleanupCommand(ctx context.Context, command *store.Command) {
	switch command.Status {
	case store.CommandStatusAcked, store.CommandStatusFailed, store.CommandStatusAbandoned:
		if _, err := d.st.SyncRevisionTaskResult(ctx, command.ID); err != nil {
			log.Printf("dispatch: sync cleanup task result for command %d: %v", command.ID, err)
		}
	case store.CommandStatusQueued:
		if d.req.IsOnline(command.ServerID) {
			d.Flush(ctx, command.ServerID)
		}
	case store.CommandStatusSent:
		if !d.req.IsOnline(command.ServerID) || d.commandGateBlocked(ctx, command.ServerID) ||
			!commandDeliveryTimedOut(command, cleanupCommandSentTimeout) {
			return
		}
		requeued, err := d.st.RequeueSentCommand(ctx, command.ID, command.Attempts)
		if err != nil {
			log.Printf("dispatch: requeue cleanup command %d: %v", command.ID, err)
		} else if requeued {
			log.Printf("dispatch: cleanup command %d requeued after receipt timeout (attempts=%d)", command.ID, command.Attempts)
			d.Flush(ctx, command.ServerID)
		}
	}
}

func commandDeliveryTimedOut(command *store.Command, timeout time.Duration) bool {
	sentAt, ok := parseCommandTimestamp(command.UpdatedAt)
	if !ok {
		log.Printf("dispatch: command %d: unparsable updated_at %q, treating as timed out", command.ID, command.UpdatedAt)
		return true
	}
	return time.Since(sentAt) > timeout
}

// parseCommandTimestamp 解析 commands.updated_at：SQLite DATETIME 经驱动扫描为 RFC3339
// （"2026-11-10T03:32:35Z"），兼容 CURRENT_TIMESTAMP 默认文本格式与显式时区偏移。
func parseCommandTimestamp(value string) (time.Time, bool) {
	if value == "" {
		return time.Time{}, false
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02 15:04:05"} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed, true
		}
	}
	return time.Time{}, false
}

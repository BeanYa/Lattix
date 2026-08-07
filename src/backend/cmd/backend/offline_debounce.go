package main

import (
	"log"
	"os"
	"sync"
	"time"
)

// offlineDebounceDefault 是断连消音的默认窗口：WS 断开后延迟该时长才上报 offline
// 事件/告警并触发链降级重估；窗口内重连成功则全部取消（§19 事件告警）。
const offlineDebounceDefault = 10 * time.Second

// offlineDebounceWindow 是生效窗口；LATTIX_OFFLINE_DEBOUNCE（Go duration）可覆盖（dev/e2e 用）。
var offlineDebounceWindow = offlineDebounceDefault

func init() {
	if v := os.Getenv("LATTIX_OFFLINE_DEBOUNCE"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d <= 0 {
			log.Printf("LATTIX_OFFLINE_DEBOUNCE invalid, using %s: %v", offlineDebounceWindow, err)
		} else {
			offlineDebounceWindow = d
		}
	}
}

// offlineDebouncer 按 server 维度延迟执行断连副作用：schedule 替换同 server 的待执行
// 任务，cancel（重连成功）或 close（关停/drain）取消。全部方法并发安全。
type offlineDebouncer struct {
	window time.Duration

	mu      sync.Mutex
	pending map[int64]*time.Timer
	closed  bool
}

func newOfflineDebouncer(window time.Duration) *offlineDebouncer {
	if window <= 0 {
		window = offlineDebounceDefault
	}
	return &offlineDebouncer{window: window, pending: make(map[int64]*time.Timer)}
}

// schedule 延迟 window 执行 fn；同 server 已有待执行任务时替换之（断连时间以最后一次为准）。
func (d *offlineDebouncer) schedule(serverID int64, fn func()) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return
	}
	if t, ok := d.pending[serverID]; ok {
		t.Stop()
	}
	d.pending[serverID] = time.AfterFunc(d.window, func() {
		d.mu.Lock()
		delete(d.pending, serverID)
		closed := d.closed
		d.mu.Unlock()
		if !closed {
			fn()
		}
	})
}

// cancel 取消 server 的待执行任务（重连成功时调用），无待执行任务时为空操作。
func (d *offlineDebouncer) cancel(serverID int64) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if t, ok := d.pending[serverID]; ok {
		t.Stop()
		delete(d.pending, serverID)
	}
}

// close 停止并丢弃所有待执行任务；之后的 schedule 为空操作。
func (d *offlineDebouncer) close() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.closed = true
	for id, t := range d.pending {
		t.Stop()
		delete(d.pending, id)
	}
}

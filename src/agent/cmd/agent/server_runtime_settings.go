package main

import (
	"strings"
	"sync"
	"time"

	"lattix/shared"
)

// reconcileCooldown 防止固定版本对齐失败后每个 sync 周期无限重试：
// 同版本失败后冷却期内不再尝试，面板持续显示 failed 状态。
const reconcileCooldown = 30 * time.Minute

// serverRuntimeSettings 持有面板下发的服务器生效设置（照抄 runtimeSettings 模式）。
type serverRuntimeSettings struct {
	mu                 sync.RWMutex
	value              shared.ServerSettings
	panelInstanceID    string
	appliedRevision    int64
	lastApplyError     string
	changed            chan struct{}
	reconciling        bool
	lastAttemptVersion string
	lastAttemptAt      time.Time
}

func newServerRuntimeSettings(document shared.ServerSettingsDocument) *serverRuntimeSettings {
	r := &serverRuntimeSettings{changed: make(chan struct{})}
	if document.Validate() == nil {
		r.value = document.Server
		r.panelInstanceID = ""
		r.appliedRevision = document.Revision
	}
	return r
}

func (r *serverRuntimeSettings) snapshot() (shared.ServerSettings, string, int64, string) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.value, r.panelInstanceID, r.appliedRevision, r.lastApplyError
}

// apply 应用新文档并返回旧值（供对齐逻辑判断版本变化）。
func (r *serverRuntimeSettings) apply(document shared.ServerSettingsDocument) shared.ServerSettings {
	r.mu.Lock()
	previous := r.value
	r.value = document.Server
	r.panelInstanceID = ""
	r.appliedRevision = document.Revision
	r.lastApplyError = ""
	changed := r.changed
	r.changed = make(chan struct{})
	r.mu.Unlock()
	close(changed)
	return previous
}

func (r *serverRuntimeSettings) fail(message string) {
	r.mu.Lock()
	r.lastApplyError = message
	r.mu.Unlock()
}

func (r *serverRuntimeSettings) resetForPanelRebind() {
	r.mu.Lock()
	r.value = shared.ServerSettings{}
	r.panelInstanceID = ""
	r.appliedRevision = 0
	r.lastApplyError = ""
	r.lastAttemptVersion = ""
	changed := r.changed
	r.changed = make(chan struct{})
	r.mu.Unlock()
	close(changed)
}

// shouldReconcile 决定是否触发版本对齐：期望为固定版本、与当前不一致、
// 且（版本与上次尝试不同 或 已过冷却期）。
func (r *serverRuntimeSettings) shouldReconcile(current string) (string, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	v := r.value.XrayVersion
	if v == nil || *v == "" || *v == "latest" {
		return "", false
	}
	if strings.TrimPrefix(*v, "v") == strings.TrimPrefix(current, "v") {
		return "", false
	}
	if *v == r.lastAttemptVersion && time.Since(r.lastAttemptAt) < reconcileCooldown {
		return "", false
	}
	return *v, true
}

// markAttempt 记录一次对齐尝试；失败时写入 lastApplyError 供回执展示。
func (r *serverRuntimeSettings) markAttempt(version string, err error) {
	r.mu.Lock()
	r.lastAttemptVersion = version
	r.lastAttemptAt = time.Now()
	if err != nil {
		r.lastApplyError = err.Error()
	}
	r.mu.Unlock()
}

func (r *serverRuntimeSettings) beginReconcile() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.reconciling {
		return false
	}
	r.reconciling = true
	return true
}

func (r *serverRuntimeSettings) endReconcile() {
	r.mu.Lock()
	r.reconciling = false
	r.mu.Unlock()
}

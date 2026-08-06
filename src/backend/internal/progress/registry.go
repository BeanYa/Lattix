// Package progress 提供旁路式操作进度观察：长操作在校验通过后注册一个观察
// （Observation），在流程自然节点处报告阶段/百分比/消息；观察失败（panic、
// 参数错误、容量满）绝不影响原操作——所有方法 nil 安全并 recover 一切 panic。
package progress

import (
	"context"
	"log"
	"sync"
	"time"

	"lattix/shared"
)

const MaxRunningObservations = 64

// 终态观察保留时长（测试可改写）。
var finishedTTL = 5 * time.Minute

// Status 观察状态。
type Status string

const (
	StatusRunning Status = "running"
	StatusDone    Status = "done"
	StatusFailed  Status = "failed"
)

// Stage 观察的阶段定义（前端弹窗展示用，label 为中文）。
type Stage struct {
	Key   string `json:"key"`
	Label string `json:"label"`
}

// Observation 一条旁路观察记录。方法全部 nil 安全（Start 失败返回 nil 句柄）。
type Observation struct {
	r *Registry

	ID         string     `json:"id"`
	Kind       string     `json:"kind"`
	Title      string     `json:"title"`
	Stages     []Stage    `json:"stages"`
	Stage      string     `json:"stage"`
	Percent    int        `json:"percent"`
	Message    string     `json:"message"`
	Status     Status     `json:"status"`
	Warnings   []string   `json:"warnings,omitempty"`
	Error      string     `json:"error,omitempty"`
	StartedAt  time.Time  `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`

	watched      map[int64]bool // WatchUsers 登记的用户
	watchedTotal int            // 登记用户总数
	published    int            // 已成功发布的用户数
	errored      int            // 已处理但发布失败的用户数
	finished     bool
}

// Registry 观察注册表（进程内、无持久化）。
type Registry struct {
	mu  sync.Mutex
	obs map[string]*Observation
}

func NewRegistry() *Registry {
	return &Registry{obs: make(map[string]*Observation)}
}

// Start 创建观察。容量满或参数非法返回 nil（调用方继续正常业务，报告为 no-op）。
func (r *Registry) Start(kind, title string, stages []Stage) *Observation {
	defer r.recover()
	if r == nil || kind == "" || title == "" {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now()
	for id, o := range r.obs {
		if o.finished && o.FinishedAt != nil && now.Sub(*o.FinishedAt) > finishedTTL {
			delete(r.obs, id)
		}
	}
	running := 0
	for _, o := range r.obs {
		if o.Status == StatusRunning {
			running++
		}
	}
	if running >= MaxRunningObservations {
		return nil
	}
	o := &Observation{
		r: r, ID: shared.NewMessageID(), Kind: kind, Title: title,
		Stages: append([]Stage(nil), stages...), Status: StatusRunning,
		StartedAt: time.Now(), watched: make(map[int64]bool),
	}
	r.obs[o.ID] = o
	return o
}

// Get 返回观察快照副本（r 已置空，快照上的方法调用均为 no-op，外部不可修改内部状态）。
func (r *Registry) Get(id string) (*Observation, bool) {
	defer r.recover()
	if r == nil {
		return nil, false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	o, ok := r.obs[id]
	if !ok {
		return nil, false
	}
	if o.finished && o.FinishedAt != nil && time.Since(*o.FinishedAt) > finishedTTL {
		delete(r.obs, id)
		return nil, false
	}
	cp := *o
	cp.r = nil // 快照不指向注册表，所有方法成为 no-op（nil 安全）
	cp.Stages = append([]Stage(nil), o.Stages...)
	cp.Warnings = append([]string(nil), o.Warnings...)
	return &cp, true
}

// Attach 把观察 ID 绑定到请求 context（writeRPC 自动注入 envelope）。
func (r *Registry) Attach(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, observeIDKey{}, id)
}

// ObserveIDFromContext 从 context 读取观察 ID（writeRPC/responseRecorder 用）。
func (r *Registry) ObserveIDFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	id, _ := ctx.Value(observeIDKey{}).(string)
	return id
}

type observeIDKey struct{}

// Report 推进阶段/百分比/消息。stageKey 不在 Stages 中时仅更新 percent/message。
func (o *Observation) Report(stageKey string, percent int, message string) {
	defer o.recover()
	if o == nil || o.r == nil {
		return
	}
	o.r.mu.Lock()
	defer o.r.mu.Unlock()
	if o.finished {
		return
	}
	for _, s := range o.Stages {
		if s.Key == stageKey {
			o.Stage = stageKey
			break
		}
	}
	if percent < 0 {
		percent = 0
	}
	if percent > 100 {
		percent = 100
	}
	o.Percent = percent
	o.Message = message
}

// Warn 追加警告（终态时随观察一起呈现）。
func (o *Observation) Warn(msg string) {
	defer o.recover()
	if o == nil || o.r == nil || msg == "" {
		return
	}
	o.r.mu.Lock()
	defer o.r.mu.Unlock()
	if o.finished {
		return
	}
	o.Warnings = append(o.Warnings, msg)
}

// WatchUsers 登记等待发布的用户集（regenerator 每完成一个回调推进百分比）。
func (o *Observation) WatchUsers(userIDs []int64) {
	defer o.recover()
	if o == nil || o.r == nil || len(userIDs) == 0 {
		return
	}
	o.r.mu.Lock()
	defer o.r.mu.Unlock()
	if o.finished {
		return
	}
	for _, id := range userIDs {
		if id > 0 && !o.watched[id] {
			o.watched[id] = true
			o.watchedTotal++
		}
	}
}

// Finish 标记完成（幂等）。
func (o *Observation) Finish() {
	defer o.recover()
	if o == nil || o.r == nil {
		return
	}
	o.r.mu.Lock()
	defer o.r.mu.Unlock()
	o.finishLocked(StatusDone, "")
}

// Fail 标记失败（幂等）。业务失败路径在写错误响应前调用。
func (o *Observation) Fail(err error) {
	defer o.recover()
	if o == nil || o.r == nil {
		return
	}
	o.r.mu.Lock()
	defer o.r.mu.Unlock()
	msg := ""
	if err != nil {
		msg = err.Error()
	}
	o.finishLocked(StatusFailed, msg)
}

// Close 是 defer 收尾专用：未落终态时按 done 收口（含已累积警告）。业务 panic
// 时也会执行，观察不悬挂；业务错误路径应显式调用 Fail。
func (o *Observation) Close() {
	defer o.recover()
	if o == nil || o.r == nil {
		return
	}
	o.r.mu.Lock()
	defer o.r.mu.Unlock()
	if !o.finished {
		o.finishLocked(StatusDone, "")
	}
}

func (o *Observation) finishLocked(status Status, errMsg string) {
	if o.finished {
		return
	}
	o.finished = true
	o.Status = status
	o.Error = errMsg
	if status == StatusDone && o.Percent < 100 {
		o.Percent = 100
	}
	now := time.Now()
	o.FinishedAt = &now
}

// NotifyUserPublished 由 regenerator 每发布完一个用户调用：推进所有 watch 该
// 用户的活跃观察的百分比；发布失败计入该观察警告。无活跃观察时零开销返回。
func (r *Registry) NotifyUserPublished(userID int64, publishErr error) {
	defer r.recover()
	if r == nil || userID <= 0 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, o := range r.obs {
		if o.Status != StatusRunning || o.finished || !o.watched[userID] {
			continue
		}
		o.watched[userID] = false // 每用户只计一次
		if publishErr != nil {
			o.errored++
			o.Warnings = append(o.Warnings, publishErr.Error())
		} else {
			o.published++
		}
		if o.published+o.errored >= o.watchedTotal {
			o.Percent = 100
		} else if o.watchedTotal > 0 {
			o.Percent = o.published * 100 / o.watchedTotal
		}
	}
}

func (r *Registry) recover() {
	if rec := recover(); rec != nil {
		log.Printf("progress: recover: %v", rec)
	}
}

func (o *Observation) recover() {
	if rec := recover(); rec != nil {
		log.Printf("progress: recover(%s): %v", safeID(o), rec)
	}
}

func safeID(o *Observation) string {
	if o == nil {
		return ""
	}
	return o.ID
}

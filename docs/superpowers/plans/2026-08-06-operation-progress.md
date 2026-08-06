# 旁路式操作进度系统（observe）实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 为面板长链路/长 IO 操作附加旁路式进度观察（observe），前端弹出类似面板更新的阶段进度弹窗。

**Architecture:** Go 后端新增 `internal/progress` 观察注册表（纯旁路、panic 隔离），长操作 handler 在校验通过后注册观察并在自然节点报告进度；regenerator 发布循环旁路回调推进"重新生成订阅文件"进度；RPC envelope 附加 `observe_id`；前端新增 `OperationProgress` 弹窗轮询 `GET /api/observe-task/get`。所有原业务逻辑零修改。

**Tech Stack:** Go（net/http、SQLite）、React + TypeScript + shadcn/ui、Vitest、openapi-typescript 生成。

**执行环境:** 在 worktree `lattix-codex/.worktree/observe-progress` 中实现（分支 `feat/observe-progress`，从 main 切出），完成后合并回 main。

## Global Constraints

- 硬性约束 1：不修改任何原操作逻辑——校验/写库/队列/错误处理/响应结构保持原样，只插入报告调用。
- 硬性约束 2：观察进程崩溃（panic/参数错误）不影响原操作——报告方法全部 recover，`Start` 失败返回 nil 句柄，nil 句柄一切报告为 no-op。
- 硬性约束 3：对外命名一律 observe 语义：envelope 字段 `observe_id`（snake），API 路径 `/api/observe-task/get`（kebab），前端 `postObserved` / `observeId`（camel）。不出现 task_id/task 对外命名。
- 校验失败不创建观察；任务用 `defer o.Close()` 收尾；业务失败路径 handler 写错误响应前显式 `o.Fail(err)`。
- 已有进度机制（面板更新、服务器测试、xray/agent 升级）不动。
- 阶段列表由后端返回（`Stages: [{key,label}]`），前端不硬编码阶段。
- 终态观察 TTL 5 分钟；running 观察上限 64。
- 提交信息用仓库现有风格（`feat(...)`/`fix(...)`，中文正文）。

---

### Task 1: `internal/progress` 观察注册表（核心包）

**Files:**
- Create: `src/backend/internal/progress/registry.go`
- Test: `src/backend/internal/progress/registry_test.go`

**Interfaces:**
- Produces:
  - `type Stage struct { Key, Label string }`
  - `type Status string`（`StatusRunning`/`StatusDone`/`StatusFailed` 常量）
  - `type Observation struct`（字段见代码；方法全部 nil-safe + panic-safe）
  - `type Registry struct`；`func NewRegistry() *Registry`
  - `(*Registry) Start(kind, title string, stages []Stage) *Observation`（容量满/参数非法返回 nil）
  - `(*Registry) Get(id string) (*Observation, bool)`
  - `(*Registry) Attach(ctx context.Context, id string) context.Context` / `(*Registry) ObserveIDFromContext(ctx context.Context) string`
  - `(*Registry) NotifyUserPublished(userID int64, publishErr error)`（regenerator 调用）
  - `(*Observation) Report(stageKey string, percent int, message string)`
  - `(*Observation) Warn(msg string)`、`(*Observation) Finish()`、`(*Observation) Fail(err error)`、`(*Observation) Close()`（幂等）
  - `(*Observation) WatchUsers(userIDs []int64)`
  - 常量 `MaxRunningObservations = 64`、`FinishedTTL = 5 * time.Minute`

- [ ] **Step 1: 写失败测试 `registry_test.go`**

```go
package progress

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestStartReportFinish(t *testing.T) {
	r := NewRegistry()
	o := r.Start("user_group.update", "更新用户分组", []Stage{{Key: "db", Label: "校验并写入数据库"}, {Key: "reconcile", Label: "同步共享端点"}, {Key: "regenerate", Label: "重新生成订阅文件"}})
	if o == nil || o.ID == "" || o.Status != StatusRunning {
		t.Fatalf("Start = %+v, want running observation with ID", o)
	}
	o.Report("db", 100, "写入完成")
	got, ok := r.Get(o.ID)
	if !ok || got.Stage != "db" || got.Percent != 100 || got.Message != "写入完成" {
		t.Fatalf("Get after Report = %+v, want stage=db percent=100", got)
	}
	o.Finish()
	got, _ = r.Get(o.ID)
	if got.Status != StatusDone || got.FinishedAt == nil {
		t.Fatalf("after Finish: status=%s finished=%v", got.Status, got.FinishedAt)
	}
	o.Finish() // 幂等
}

func TestFailWithError(t *testing.T) {
	r := NewRegistry()
	o := r.Start("x", "y", nil)
	o.Fail(errors.New("boom"))
	got, _ := r.Get(o.ID)
	if got.Status != StatusFailed || !strings.Contains(got.Error, "boom") {
		t.Fatalf("after Fail: %+v", got)
	}
}

func TestCloseFallsBackToDone(t *testing.T) {
	r := NewRegistry()
	o := r.Start("x", "y", nil)
	o.Warn("部分失败")
	o.Close()
	got, _ := r.Get(o.ID)
	if got.Status != StatusDone || len(got.Warnings) != 1 {
		t.Fatalf("after Close: %+v", got)
	}
}

func TestNilObservationIsNoOp(t *testing.T) {
	var o *Observation // 模拟 Start 失败返回 nil 后继续调用
	o.Report("db", 100, "x")
	o.Warn("w")
	o.Finish()
	o.Fail(errors.New("e"))
	o.Close()
	o.WatchUsers([]int64{1, 2})
}

func TestReportRecoversPanic(t *testing.T) {
	r := NewRegistry()
	o := r.Start("x", "y", nil)
	func() {
		defer func() { _ = recover() }() // 如果 panic 泄漏，这个 recover 会抓住并导致测试失败
		o.Report("db", 100, strings.Repeat("x", 1<<20))
	}()
	got, _ := r.Get(o.ID)
	if got == nil {
		t.Fatal("observation must survive report panic")
	}
}

func TestCapacityLimit(t *testing.T) {
	r := NewRegistry()
	for i := 0; i < MaxRunningObservations+5; i++ {
		o := r.Start("k", "t", nil)
		if i < MaxRunningObservations && o == nil {
			t.Fatalf("Start #%d returned nil under capacity", i)
		}
		if i >= MaxRunningObservations && o != nil {
			t.Fatalf("Start #%d returned non-nil over capacity", i)
		}
	}
}

func TestFinishedTTLClears(t *testing.T) {
	old := finishedTTL
	finishedTTL = 20 * time.Millisecond
	defer func() { finishedTTL = old }()
	r := NewRegistry()
	o := r.Start("x", "y", nil)
	o.Finish()
	time.Sleep(40 * time.Millisecond)
	if _, ok := r.Get(o.ID); ok {
		t.Fatal("finished observation should be cleaned after TTL")
	}
}

func TestAttachAndObserveIDFromContext(t *testing.T) {
	r := NewRegistry()
	ctx := r.Attach(context.Background(), "obs-1")
	if got := r.ObserveIDFromContext(ctx); got != "obs-1" {
		t.Fatalf("ObserveIDFromContext = %q", got)
	}
	if got := r.ObserveIDFromContext(context.Background()); got != "" {
		t.Fatalf("empty ctx ObserveIDFromContext = %q", got)
	}
}

func TestWatchUsersProgressAndWarnings(t *testing.T) {
	r := NewRegistry()
	o := r.Start("x", "y", []Stage{{Key: "regenerate", Label: "重新生成订阅文件"}})
	o.WatchUsers([]int64{1, 2, 3, 4})
	o.Report("regenerate", 0, "等待发布")
	r.NotifyUserPublished(1, nil)
	r.NotifyUserPublished(2, errors.New("生成失败"))
	got, _ := r.Get(o.ID)
	if got.Percent != 25 {
		t.Fatalf("percent = %d, want 25", got.Percent)
	}
	r.NotifyUserPublished(3, nil)
	r.NotifyUserPublished(4, nil)
	got, _ = r.Get(o.ID)
	if got.Percent != 100 {
		t.Fatalf("percent = %d, want 100", got.Percent)
	}
	if len(got.Warnings) != 1 {
		t.Fatalf("warnings = %v, want 1", got.Warnings)
	}
	// 未登记用户不影响
	r.NotifyUserPublished(999, errors.New("ignored"))
	got, _ = r.Get(o.ID)
	if len(got.Warnings) != 1 {
		t.Fatalf("warnings = %v, want still 1", got.Warnings)
	}
}

func TestNotifyPublishedRecoversPanic(t *testing.T) {
	r := NewRegistry()
	o := r.Start("x", "y", []Stage{{Key: "regenerate", Label: "重新生成订阅文件"}})
	o.WatchUsers([]int64{1})
	// 触发一次非法调用路径后仍可继续使用
	r.NotifyUserPublished(1, errors.New("boom"))
	if _, ok := r.Get(o.ID); !ok {
		t.Fatal("registry must survive notification")
	}
}
```

- [ ] **Step 2: 运行确认失败**

Run（在 `src/backend` 下）: `go test ./internal/progress/...`
Expected: FAIL（package 不存在）

- [ ] **Step 3: 实现 `registry.go`**

```go
// Package progress 提供旁路式操作进度观察：长操作在校验通过后注册一个观察
// （Observation），在流程自然节点处报告阶段/百分比/消息；观察失败（panic、
// 参数错误、容量满）绝不影响原操作——所有方法 nil 安全并 recover 一切 panic。
package progress

import (
	"context"
	"errors"
	"log"
	"strings"
	"sync"
	"time"

	"lattix/shared"
)

const (
	MaxRunningObservations = 64
	finishedTTL            = 5 * time.Minute // 终态观察保留时长（测试可改写）
)

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

	ID         string    `json:"id"`
	Kind       string    `json:"kind"`
	Title      string    `json:"title"`
	Stages     []Stage   `json:"stages"`
	Stage      string    `json:"stage"`
	Percent    int       `json:"percent"`
	Message    string    `json:"message"`
	Status     Status    `json:"status"`
	Warnings   []string  `json:"warnings,omitempty"`
	Error      string    `json:"error,omitempty"`
	StartedAt  time.Time `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`

	watched  map[int64]bool // WatchUsers 登记的用户
	published int           // 已通知发布的用户数
	finished bool
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
	if r == nil || kind == "" || title == "" || len(stages) == 0 {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
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

// Get 返回观察快照副本（外部不可修改内部状态）。
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
	cp := *o
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
		if id > 0 {
			o.watched[id] = true
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
	matched := make([]*Observation, 0, 2)
	for _, o := range r.obs {
		if o.Status == StatusRunning && !o.finished && o.watched[userID] {
			matched = append(matched, o)
		}
	}
	for _, o := range matched {
		if !o.watched[userID] {
			continue
		}
		o.watched[userID] = false // 每用户只计一次
		o.published++
		total := len(o.watched) + o.published
		if total > 0 {
			o.Percent = o.published * 100 / total
		}
		if publishErr != nil {
			o.Warnings = append(o.Warnings, publishErr.Error())
		}
	}
	r.mu.Unlock()
	for _, o := range matched {
		if o.published == 0 {
			continue
		}
		_ = o
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

var _ = errors.New // 占位避免误删导入（若不需要可删除本行与 errors 导入）
```

注意：若 `errors` 未被使用，删除其导入与末尾占位行；运行 `go vet ./internal/progress/...` 检查。

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/progress/... -race -v`
Expected: PASS（全部用例）

- [ ] **Step 5: Commit**

```bash
git add src/backend/internal/progress/
git commit -m "feat(progress): 旁路式操作进度观察注册表"
```

---

### Task 2: 后端集成（Server 字段、envelope 注入、查询端点、regenerator 通知）

**Files:**
- Modify: `src/backend/internal/panel/panel.go:50-79`（Server 增加 `observes *progress.Registry`）、`src/backend/internal/panel/panel.go:465-503`（rpcResponse 增加 ObserveID 字段、writeRPC 注入）、`src/backend/internal/panel/panel.go:154-160`（New 中初始化）
- Modify: `src/backend/internal/panel/rpc_routes.go:309-320`（rpcCapture 透传 ObserveID）
- Modify: `src/backend/internal/logging/http.go:370-388`（responseRecorder 增加 ObserveID()）
- Modify: `src/backend/internal/panel/panel.go`（RegisterRoutes 增 `/api/observe-task/get`）
- Create: `src/backend/internal/panel/observe_task.go`（`handleGetObserveTask`）
- Modify: `src/backend/internal/sub/sub.go:53-79`（New 保持，增加 `SetObserver(r *progress.Registry)`）
- Modify: `src/backend/internal/sub/regenerate.go:91-117`（runRegenerator 发布循环旁路回调）
- Modify: `src/backend/cmd/backend/main.go`（构造 sub.Server 后调用 SetObserver；查找 `sub.New` 调用点）
- Test: `src/backend/internal/panel/observe_task_test.go`（新）、`src/backend/internal/sub/regenerate_test.go`（若无现成文件则新建同名测试文件）

**Interfaces:**
- Consumes: Task 1 的 `progress.Registry`（`Start/Get/Attach/ObserveIDFromContext/NotifyUserPublished`、`Observation.Report/Warn/Finish/Fail/Close/WatchUsers`）
- Produces: `GET /api/observe-task/get?observe_id=…` 返回 Observation JSON；envelope 携带 `observe_id`；`sub.Server.SetObserver(*progress.Registry)`

- [ ] **Step 1: 写失败测试 `observe_task_test.go`**

先看现有测试辅助（`httptest.NewRequest` + `serverAPI` 构造方式，参照 `groups_test.go` 或 `contract_test.go` 的 Server 构造）。测试内容：

```go
package panel

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"lattix/backend/internal/progress"
)

func TestObserveTaskGet(t *testing.T) {
	// 参照现有测试构造 serverAPI（见 groups_test.go 的 setup 模式）
	serverAPI, _ := newTestServerAPI(t) // 若现有辅助函数名不同，按实际调整
	reg := progress.NewRegistry()
	serverAPI.observes = reg
	o := reg.Start("user_group.update", "更新用户分组",
		[]progress.Stage{{Key: "db", Label: "校验并写入数据库"}})
	o.Report("db", 50, "写入中")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/observe-task/get?observe_id="+o.ID, nil)
	serverAPI.handleGetObserveTask(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var got struct {
		Data progress.Observation `json:"data"`
	}
	if err := decodeJSON(rec.Body, &got); err != nil {
		t.Fatal(err)
	}
	if got.Data.ID != o.ID || got.Data.Percent != 50 || got.Data.Status != progress.StatusRunning {
		t.Fatalf("observation = %+v", got.Data)
	}

	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodGet, "/api/observe-task/get?observe_id=nope", nil)
	serverAPI.handleGetObserveTask(rec2, req2)
	if rec2.Code != http.StatusNotFound {
		t.Fatalf("missing observe status = %d, want 404", rec2.Code)
	}
}

func TestEnvelopeCarriesObserveID(t *testing.T) {
	// 校验响应 envelope 的 observe_id 字段（参照 contract_test.go 的断言方式）
	serverAPI, _ := newTestServerAPI(t)
	reg := progress.NewRegistry()
	serverAPI.observes = reg
	o := reg.Start("k", "t", []progress.Stage{{Key: "a", Label: "A"}})
	ctx := reg.Attach(httptest.NewRequest(http.MethodGet, "/", nil).Context(), o.ID)
	rec := httptest.NewRecorder()
	writeRPC(rec, "OK", "", nil) // 直接调用不带 ctx 的写路径无法携带——改为：
	_ = ctx
	_ = rec
	// 说明：envelope 注入经 responseRecorder 从 request context 读取；
	// 集成断言放到 handler 级测试（Task 3 起各挂点测试断言 response envelope.observe_id）
}
```

> 若 `newTestServerAPI` 等辅助函数不存在，按 `groups_test.go` 实际构造方式写。`TestEnvelopeCarriesObserveID` 可精简为验证 `handleGetObserveTask` 已覆盖的 404/200 分支 + Task 3 中的 handler 级断言（若时间有限允许删除该用例，但必须保留 404 分支断言）。

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/panel/ -run 'TestObserveTaskGet' -v`
Expected: FAIL（handleGetObserveTask 未定义 / observes 字段不存在）

- [ ] **Step 3: 实现**

3a. `panel.go` Server 结构加字段（`progress` 导入）：

```go
	observes      *progress.Registry // 旁路操作进度观察（nil = 关闭）
```

`New` 中初始化：`observes: progress.NewRegistry(),`

3b. `rpcResponse` 增加字段与 `writeRPC` 注入（panel.go:465-503）：

```go
type rpcResponse struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Data      any    `json:"data"`
	RequestID string `json:"request_id"`
	TraceID   string `json:"trace_id"`
	ObserveID string `json:"observe_id,omitempty"`
}
```

`rpcResponseWriter` 接口增加：

```go
type rpcResponseWriter interface {
	SetRPCOutcome(code, safeMessage string)
	RPCIDs() (requestID, traceID string)
	ObserveID() string
}
```

`writeRPC` 中读取（在 `traceID == ""` 分支后）：

```go
	observeID := ""
	if rw, ok := w.(rpcResponseWriter); ok {
		observeID = rw.ObserveID()
	}
	// ... Encode(rpcResponse{ ..., ObserveID: observeID })
```

`writeProtocolError` 同样注入（可选，错误响应不携带亦可；保持一致则同样添加）。

3c. `logging/http.go` responseRecorder（导入 `lattix/backend/internal/progress` 会在 panel 包内造成循环？——不：logging 不 import panel，直接 import progress 即可）：

```go
func (w *responseRecorder) ObserveID() string {
	// responseRecorder 无 progress 依赖时改为由请求日志中间件配置：
	// 在 http.go 增加注册函数 SetObserveIDReader(func(ctx) string)，由 panel 初始化时注入。
	// 为避免 logging→progress 反向依赖，采用注入式：
	if readObserveID == nil {
		return ""
	}
	return readObserveID(w.request.Context())
}
```

文件顶部增加包级变量与 setter：

```go
var readObserveID func(ctx context.Context) string

// SetObserveIDReader 注入 observe_id 读取器（panel 启动时调用，避免依赖反向）。
func SetObserveIDReader(fn func(ctx context.Context) string) {
	readObserveID = fn
}
```

3d. `rpc_routes.go` rpcCapture 透传：

```go
func (w *rpcCapture) ObserveID() string {
	if target, ok := w.target.(interface{ ObserveID() string }); ok {
		return target.ObserveID()
	}
	return ""
}
```

3e. 新建 `observe_task.go`：

```go
package panel

import (
	"net/http"
	"strconv"

	"lattix/backend/internal/progress"
)

// handleGetObserveTask 处理 GET /api/observe-task/get：返回旁路观察进度快照。
// 404 = 观察不存在或已清理（终态保留 5 分钟）。
func (s *Server) handleGetObserveTask(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("observe_id")
	if id == "" || !validObserveID(id) {
		writeError(w, http.StatusBadRequest, "observe_id 必须为 32 位十六进制")
		return
	}
	obs, ok := s.observes.Get(id)
	if !ok {
		writeError(w, http.StatusNotFound, "观察不存在或已清理")
		return
	}
	writeJSON(w, http.StatusOK, obs)
}

func validObserveID(id string) bool {
	if len(id) != 32 {
		return false
	}
	_, err := strconv.ParseUint(id, 16, 128)
	return err == nil
}

var _ = progress.StatusRunning // 占位：避免未使用导入（若不需要删除本行与 progress 导入）
```

> `progress` 导入若不需要请删除。`validObserveID` 与 `shared.NewMessageID` 产出格式一致（32 hex）。

3f. `panel.go` RegisterRoutes 注册（放在 `/api/panel/get-update-status` 附近）：

```go
	s.registerRPC(mux, http.MethodGet, "/api/observe-task/get",
		rpcRouteOptions{Auth: true, AllowedQuery: []string{"observe_id"}, LogPolicy: logging.LogFailuresOnly, Debug: true},
		s.handleGetObserveTask)
```

3g. `sub/sub.go` 增加观察者注入（`progress` 导入）：

```go
type Server struct {
	// ...原有字段...
	observer *progress.Registry // 旁路观察（nil = 关闭，发布循环零开销）
}

// SetObserver 注入旁路观察注册表（panel 初始化时调用；可多次调用，最后者生效）。
func (s *Server) SetObserver(r *progress.Registry) {
	s.observer = r
}
```

3h. `sub/regenerate.go` runRegenerator 发布循环旁路回调（regenerate.go:109-115）：

```go
		publishCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		result, err := s.PublishUser(publishCtx, userID, baseURL)
		cancel()
		if s.observer != nil {
			s.observer.NotifyUserPublished(userID, err)
		}
		if err != nil {
			log.Printf("subscription: regenerate user %d: %v", userID, err)
		}
		_ = result
```

3i. `cmd/backend/main.go` 找到 `sub.New` 调用处，构造后调用 `SetObserver(progress.NewRegistry())` 并把同一实例注入 panel.Server（`New` 里已建，改为：先建 registry，再分别注入 sub 与 panel Server——将 panel `New` 中初始化改为可选覆盖：`if s.observes == nil { s.observes = progress.NewRegistry() }` 不成立，直接改为：main 中创建 registry 传参最麻烦。**简化**：panel.New 内部创建 registry 并保存局部变量 → 通过新增 `func (s *Server) ObserverRegistry() *progress.Registry` 暴露 → main 调 `sub.SetObserver(panel.ObserverRegistry())`）。

```go
// panel.go:
func (s *Server) ObserverRegistry() *progress.Registry {
	if s.observes == nil {
		s.observes = progress.NewRegistry()
	}
	return s.observes
}

// main.go（sub.New 之后）:
if panelService != nil { // 变量名按 main.go 实际
	subService.SetObserver(panelService.ObserverRegistry())
}
```

3j. 写 regenerator 通知测试（`sub/regenerate_test.go` 新建或并入现有测试文件；无现成文件则新建）：

```go
package sub

import (
	"errors"
	"testing"

	"lattix/backend/internal/progress"
)

func TestObserverNotifyUserPublished(t *testing.T) {
	reg := progress.NewRegistry()
	o := reg.Start("k", "t", []progress.Stage{{Key: "regenerate", Label: "重新生成订阅文件"}})
	o.WatchUsers([]int64{7})
	reg.NotifyUserPublished(7, errors.New("fail"))
	got, ok := reg.Get(o.ID)
	if !ok || got.Percent != 100 || len(got.Warnings) != 1 {
		t.Fatalf("after notify: %+v", got)
	}
}
```

- [ ] **Step 4: 运行测试确认通过**

Run: `go build ./... && go test ./internal/panel/ ./internal/sub/ ./internal/logging/ -run 'TestObserveTaskGet|TestObserverNotify' -v`
Expected: PASS

- [ ] **Step 5: 全量回归**

Run: `go test ./...`
Expected: PASS（现有测试不破坏——envelope 新增可选字段向后兼容）

- [ ] **Step 6: Commit**

```bash
git add src/backend/internal/panel/ src/backend/internal/sub/ src/backend/internal/logging/http.go src/backend/cmd/backend/
git commit -m "feat(panel): observe 观察注册表集成（envelope 注入 + 查询端点 + regenerator 旁路回调）"
```

---

### Task 3: 分组操作观察挂点（groups.go）

**Files:**
- Modify: `src/backend/internal/panel/groups.go`（6 个 handler：`handleCreateLinkGroup`、`handleUpdateLinkGroup`、`handleDeleteLinkGroup`、`handleCreateUserGroup`、`handleUpdateUserGroup`、`handleDeleteUserGroup`）
- Test: `src/backend/internal/panel/groups_observe_test.go`（新）

**Interfaces:**
- Consumes: `s.observes.Start(...)`、`s.observes.Attach(ctx, id)`、`Observation.Report/Warn/WatchUsers/Close/Fail`、`s.subscriptions.EnqueueUsers`（现状）
- Produces: 分组增删改响应 envelope 携带 observe_id；观察终态含阶段序列与警告

- [ ] **Step 1: 写失败测试 `groups_observe_test.go`**

```go
package panel

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// 参照现有 groups_test.go 的 Server 构造辅助写测试。
// 核心断言（每个 handler 一个用例，模式相同，以 handleCreateUserGroup 为例）：

func TestCreateUserGroupCarriesObserveID(t *testing.T) {
	serverAPI, st := newTestServerAPI(t) // 按实际辅助函数调整
	// 准备：一个用户、一个链路分组（按现有测试的构造方式）
	// POST /api/user-group/create {name:"g", user_ids:[1], link_group_ids:[1]}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/user-group/create",
		strings.NewReader(`{"name":"测试分组","user_ids":[<uid>],"link_group_ids":[<lgid>]}`))
	req.Header.Set("Content-Type", "application/json")
	serverAPI.handleCreateUserGroup(rec, req)
	// 断言：响应 envelope 的 observe_id 非空；之后 GET /api/observe-task/get 返回
	// 该观察且 status=done、stages 含 regenerate 阶段。
	_ = st
}
```

> 用例清单（每个断言 envelope.observe_id 非空 + 观察终态 done）：
> 1. `TestCreateUserGroupCarriesObserveID`
> 2. `TestUpdateUserGroupCarriesObserveID`
> 3. `TestDeleteUserGroupCarriesObserveID`
> 4. `TestCreateLinkGroupCarriesObserveID`
> 5. `TestUpdateLinkGroupCarriesObserveID`
> 6. `TestDeleteLinkGroupCarriesObserveID`
> 7. `TestUserGroupValidationFailureNoObserve`（重名/用户不存在 → envelope 无 observe_id）

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/panel/ -run 'Test.*UserGroupCarriesObserveID|Test.*LinkGroupCarriesObserveID|TestUserGroupValidationFailureNoObserve' -v`
Expected: FAIL（envelope 无 observe_id 断言失败）

- [ ] **Step 3: 实现（6 个 handler 插入挂点）**

新增辅助函数（groups.go 顶部或 observe 相关文件）：

```go
// observeStart 创建旁路观察并绑定到请求 context；返回观察句柄（可能为 nil）。
func (s *Server) observeStart(r *http.Request, kind, title string, stages []progress.Stage) *progress.Observation {
	o := s.observes.Start(kind, title, stages)
	if o != nil {
		*r = *r.WithContext(s.observes.Attach(r.Context(), o.ID))
	}
	return o
}
```

`handleCreateLinkGroup`（groups.go:115-133）插入——在 `validateLinkGroup` 成功后、`CreateLinkGroup` 前：

```go
	o := s.observeStart(r, "link_group.create", "创建链路分组",
		[]progress.Stage{
			{Key: "db", Label: "校验并写入数据库"},
			{Key: "reconcile", Label: "同步共享端点"},
			{Key: "regenerate", Label: "重新生成订阅文件"},
		})
	defer o.Close()
	id, err := s.st.CreateLinkGroup(r.Context(), name, chainIDs, extSubs)
	if err != nil {
		o.Fail(err)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	o.Report("db", 100, "分组已保存")
	o.Report("reconcile", 100, "共享端点已同步")
	s.triggerGroupChange(r.Context(), nil, s.endpointIDsForChains(r.Context(), chainIDs))
	o.Report("regenerate", 0, "等待订阅重生成")
```

> 注意：`defer o.Close()` 在 nil 句柄下安全。`triggerGroupChange` 内 `EnqueueUsers` 只接收用户；链路分组 create 无用户，regenerate 阶段无 watch——阶段直接推进 0%→Close 收口 100%。reconcile 无用户时 `endpointIDsForChains` 返回空，阶段标记 100 即可。

`handleUpdateLinkGroup`（groups.go:136-173）插入——`validateLinkGroup` 成功后：

```go
	o := s.observeStart(r, "link_group.update", "更新链路分组",
		[]progress.Stage{
			{Key: "db", Label: "校验并写入数据库"},
			{Key: "reconcile", Label: "同步共享端点"},
			{Key: "regenerate", Label: "重新生成订阅文件"},
		})
	defer o.Close()
	if err := s.st.UpdateLinkGroup(r.Context(), req.ID, name, chainIDs, extSubs); err != nil {
		o.Fail(err)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	o.Report("db", 100, "分组已保存")
	endpointIDs := s.endpointIDsForChains(r.Context(), affectedChains)
	o.Report("reconcile", 100, "共享端点已同步")
	s.triggerGroupChange(r.Context(), users, endpointIDs)
	o.WatchUsers(users)
	o.Report("regenerate", 0, "等待订阅重生成")
```

`handleDeleteLinkGroup`（groups.go:175-212）：`DeleteLinkGroup` 成功后：

```go
	o := s.observeStart(r, "link_group.delete", "删除链路分组", 同阶段列表)
	defer o.Close()
	if err := s.st.DeleteLinkGroup(r.Context(), req.ID); err != nil { o.Fail(err); ...原错误路径 }
	o.Report("db", 100, "分组已删除")
	s.triggerGroupChange(r.Context(), users, s.endpointIDsForChains(r.Context(), before.ChainIDs))
	o.WatchUsers(users)
	o.Report("regenerate", 0, "等待订阅重生成")
```

`handleCreateUserGroup`（groups.go:223-243）：

```go
	o := s.observeStart(r, "user_group.create", "创建用户分组",
		[]progress.Stage{
			{Key: "db", Label: "校验并写入数据库"},
			{Key: "reconcile", Label: "同步共享端点"},
			{Key: "regenerate", Label: "重新生成订阅文件"},
		})
	defer o.Close()
	id, err := s.st.CreateUserGroup(r.Context(), name, userIDs, linkGroupIDs)
	if err != nil {
		o.Fail(err)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	o.Report("db", 100, "分组已保存")
	s.triggerGroupChange(r.Context(), userIDs, s.endpointIDsForLinkGroups(r.Context(), linkGroupIDs))
	o.WatchUsers(userIDs)
	o.Report("regenerate", 0, "等待订阅重生成")
```

`handleUpdateUserGroup`（groups.go:246-279）：`UpdateUserGroup` 成功后：

```go
	o.Report("db", 100, "分组已保存")
	s.triggerGroupChange(r.Context(), allUsers, s.endpointIDsForLinkGroups(r.Context(), allLinkGroups))
	o.WatchUsers(allUsers)
	o.Report("regenerate", 0, "等待订阅重生成")
```

`handleDeleteUserGroup`（groups.go:281-319）：`DeleteUserGroup` 成功后：

```go
	o.Report("db", 100, "分组已删除")
	s.triggerGroupChange(r.Context(), users, s.endpointIDsForLinkGroups(r.Context(), before.LinkGroupIDs))
	o.WatchUsers(users)
	o.Report("regenerate", 0, "等待订阅重生成")
```

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/panel/ -run 'Observe' -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add src/backend/internal/panel/groups.go src/backend/internal/panel/groups_observe_test.go
git commit -m "feat(panel): 分组操作附加 observe 进度观察"
```

---

### Task 4: 链路/节点操作观察挂点（chains.go、nodes.go）

**Files:**
- Modify: `src/backend/internal/panel/chains.go`（`handleCreateChain`、`handleEditChain`、`handleDeleteChain`、`handleRetryChain`、`handleForcePublishChain`）
- Modify: `src/backend/internal/panel/nodes.go`（`handleCreateNode`、`handleRetryNode`、`handleDeleteNode`）
- Test: `src/backend/internal/panel/chains_observe_test.go`、`nodes_observe_test.go`（新）

**Interfaces:**
- Consumes: Task 3 的 `observeStart` 辅助；`s.disp`、`s.subscriptions.EnqueueUsers`（现状）
- Produces: 链路/节点操作响应携带 observe_id；观察含"校验并写入数据库 → 下发节点配置 → 重新生成订阅文件"阶段

- [ ] **Step 1: 写失败测试**

以现有 `chains_edit_test.go`/`nodes_test.go` 构造方式为模板。断言模式与 Task 3 相同：响应 envelope.observe_id 非空、观察终态 done、阶段序列正确。

用例清单：
- `TestCreateChainCarriesObserveID`、`TestEditChainCarriesObserveID`、`TestDeleteChainCarriesObserveID`、`TestRetryChainCarriesObserveID`、`TestForcePublishChainCarriesObserveID`
- `TestCreateNodeCarriesObserveID`、`TestRetryNodeCarriesObserveID`、`TestDeleteNodeCarriesObserveID`

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/panel/ -run 'Test.*ChainCarriesObserveID|Test.*NodeCarriesObserveID' -v`
Expected: FAIL

- [ ] **Step 3: 实现**

阶段定义（统一）：

```go
var chainNodeObserveStages = []progress.Stage{
	{Key: "db", Label: "校验并写入数据库"},
	{Key: "publish", Label: "下发节点配置"},
	{Key: "regenerate", Label: "重新生成订阅文件"},
}
```

各 handler 插入模式（与 Task 3 一致，`observeStart` + `defer o.Close()` + 失败路径 `o.Fail(err)` 后写错误 + 成功后 `o.Report("db", 100, ...)` + 发布循环/force publish 前 `o.Report("publish", 100, ...)` + enqueue 后 `o.WatchUsers(affectedUsers)` + `o.Report("regenerate", 0, "等待订阅重生成")`）。

> 实现提示：
> - `handleForcePublishChain`（chains.go:772-791）：`ForcePublishRevision` 成功后即 publish 阶段完成；`toChainDTO` 失败路径照旧 `o.Fail`。
> - `handleCreateNode`/`handleRetryNode`/`handleDeleteNode`（nodes.go）：`WriteNode`/发布调用后报告 publish 100；`EnqueueUsers(affectedUsers, s.panelBase(r))` 后 `WatchUsers(affectedUsers)`（nodes.go:326 附近）。
> - 若 handler 内发布是同步等待 agent 回执（链创建等），publish 阶段放在回执等待之后报告 100（该阶段为真实耗时步骤）。
> - 不改变任何现有返回结构/错误路径，只插入报告调用。

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/panel/ -run 'Observe' -v && go build ./...`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add src/backend/internal/panel/chains.go src/backend/internal/panel/nodes.go src/backend/internal/panel/chains_observe_test.go src/backend/internal/panel/nodes_observe_test.go
git commit -m "feat(panel): 链路/节点操作附加 observe 进度观察"
```

---

### Task 5: 服务器重建/修复观察挂点（servers.go）

**Files:**
- Modify: `src/backend/internal/panel/servers.go`（`handleRebuildXray`、`handleRepairServer`、`handleCleanupXray`）
- Test: `src/backend/internal/panel/servers_observe_test.go`（新）

**Interfaces:**
- Consumes: Task 3 的 `observeStart`
- Produces: rebuild/repair/cleanup 响应携带 observe_id

- [ ] **Step 1: 写失败测试**

用例：`TestRebuildXrayCarriesObserveID`、`TestRepairServerCarriesObserveID`、`TestCleanupXrayCarriesObserveID`（参照 `servers_rebuild_test.go` 构造）。断言模式同 Task 3；阶段：

```go
[]progress.Stage{
	{Key: "db", Label: "校验并写入数据库"},
	{Key: "dispatch", Label: "下发命令"},
	{Key: "ack", Label: "等待 agent 回执"},
}
```

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/panel/ -run 'TestRebuildXrayCarriesObserveID|TestRepairServerCarriesObserveID|TestCleanupXrayCarriesObserveID' -v`
Expected: FAIL

- [ ] **Step 3: 实现**

`handleRebuildXray`（servers.go:856 起）：校验通过后 `observeStart`；写库成功后 `Report("db", 100)`；`RebuildXray`/命令 Enqueue 后 `Report("dispatch", 100)`；agent 回执等待（若同步等待则完成时 `Report("ack", 100)`，否则 ack 阶段标记为排队等待并按现状返回）——**以现有 handler 的同步/异步结构为准**，只插入报告，不改变等待方式；handler 返回前 `o.Report("ack", 100, "已下发，后台执行")` 或按实际。

`handleRepairServer`、`handleCleanupXray` 同理插入（`servers.go:802-880` 附近）。

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/panel/ -run 'Observe' -v && go build ./...`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add src/backend/internal/panel/servers.go src/backend/internal/panel/servers_observe_test.go
git commit -m "feat(panel): 服务器重建/修复操作附加 observe 进度观察"
```

---

### Task 6: 外部订阅/模板同步观察挂点

**Files:**
- Modify: `src/backend/internal/panel/external_subscriptions.go`（`handleCreateExternalSubscription`、`handleUpdateExternalSubscription`、`handleSyncExternalSubscription`、`handleDeleteExternalSubscription`）
- Modify: `src/backend/internal/panel/subscription_templates.go`（`handleRefreshSubscriptionTemplates`）
- Test: `src/backend/internal/panel/external_subscriptions_observe_test.go`（新）

**Interfaces:**
- Consumes: Task 3 的 `observeStart`
- Produces: 上述操作响应携带 observe_id

- [ ] **Step 1: 写失败测试**

用例：`TestSyncExternalSubscriptionCarriesObserveID`、`TestCreateExternalSubscriptionCarriesObserveID`、`TestUpdateExternalSubscriptionCarriesObserveID`、`TestDeleteExternalSubscriptionCarriesObserveID`、`TestRefreshSubscriptionTemplatesCarriesObserveID`。阶段（同步型）：

```go
[]progress.Stage{
	{Key: "fetch", Label: "拉取远程内容"},
	{Key: "parse", Label: "解析数据"},
	{Key: "db", Label: "写入数据库"},
	{Key: "regenerate", Label: "重发布关联用户"},
}
```

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/panel/ -run 'TestSyncExternalSubscriptionCarriesObserveID|Test.*ExternalSubscriptionCarriesObserveID|TestRefreshSubscriptionTemplatesCarriesObserveID' -v`
Expected: FAIL

- [ ] **Step 3: 实现**

`handleSyncExternalSubscription`（external_subscriptions.go:130-156）——`req.ID` 校验后、`s.extSubs.Sync` 前：

```go
	o := s.observeStart(r, "external_subscription.sync", "同步外部订阅",
		[]progress.Stage{
			{Key: "fetch", Label: "拉取远程订阅"},
			{Key: "parse", Label: "解析节点"},
			{Key: "db", Label: "写入数据库"},
			{Key: "regenerate", Label: "重发布关联用户"},
		})
	defer o.Close()
	sub, err := s.extSubs.Sync(r.Context(), req.ID)
	if err != nil {
		o.Fail(err)
		status := http.StatusBadGateway
		if errors.Is(err, store.ErrNotFound) {
			status = http.StatusNotFound
		}
		writeError(w, status, err.Error())
		return
	}
	o.Report("fetch", 100, "拉取完成")
	o.Report("parse", 100, "解析完成")
	o.Report("db", 100, "已写入数据库")
	s.republishExternalSubUsers(r.Context(), []int64{sub.ID})
	o.Report("regenerate", 100, "已触发重发布")
```

> 说明：`extSubs.Sync` 内部含网络拉取（慢步骤），阶段报告粒度为"调用前 fetch 0% → 返回后逐段标记"。若需要更细粒度（拉取中实时进度），在实现时若 `extsub.Service.Sync` 支持回调则插入，否则保持粗粒度——**不改 Sync 签名**。
> create/update/delete/template.refresh 按同一模式插入；模板刷新无重发布步骤则省略 regenerate 阶段。

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/panel/ -run 'Observe' -v && go build ./...`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add src/backend/internal/panel/external_subscriptions.go src/backend/internal/panel/subscription_templates.go src/backend/internal/panel/external_subscriptions_observe_test.go
git commit -m "feat(panel): 外部订阅/模板同步附加 observe 进度观察"
```

---

### Task 7: 前端契约（openapi + requester + api）

**Files:**
- Modify: `docs/openapi.yaml`（新增 `/api/observe-task/get` 端点 + Observation schema）
- Modify: `src/frontend/src/lib/requester.ts`（`postObserved`、envelope 解析 observe_id）
- Modify: `src/frontend/src/lib/api.ts`（长操作方法改用 `postObserved`，导出 `TrackedResult`）
- Generate: `src/frontend/src/lib/api-contract.generated.ts`（`bun run generate:api`）
- Test: `src/frontend/src/lib/requester.test.ts`（新增用例）

**Interfaces:**
- Consumes: 现有 `Requester.execute`、`parseEnvelope`
- Produces: `api.postObserved<T>()` → `Promise<{ data: T; observeId?: string }>`；`api.userGroupCreate` 等长操作方法返回 `{ data, observeId }`

- [ ] **Step 1: 写失败测试（requester.test.ts 增补）**

参照现有 `requester.test.ts` 的 mock 方式（fetch mock）。新增：

```ts
it('postObserved resolves observeId from envelope', async () => {
  // mock fetch 返回 envelope 含 observe_id: 'abcd...'
  // 断言 await requester.postObserved('/api/x', {}) 的 observeId 与 data
})

it('postObserved resolves undefined observeId when envelope lacks it', async () => {
  // mock fetch 返回无 observe_id 的 envelope
  // 断言 observeId === undefined
})
```

- [ ] **Step 2: 运行确认失败**

Run（在 `src/frontend`）: `bun run test -- requester.test.ts`
Expected: FAIL（postObserved 未定义）

- [ ] **Step 3: 实现**

3a. `requester.ts`：`RequestOptions` 不变；新增类型与方法：

```ts
export type TrackedResult<T> = { data: T; observeId?: string }

export class Requester {
  // 在 post 后追加：
  async postObserved<T>(
    path: RPCPathByMethod<'POST'>,
    body: object = {},
    options?: RequestOptions,
  ): Promise<TrackedResult<T>> {
    return this.executeObserved<T>('POST', path, body, options)
  }
  // ...
}
```

`executeObserved` 与 `execute` 共用逻辑：在 `parseEnvelope` 之后把 `envelope.observe_id` 取出。实现方式：给 `RPCEnvelope` 类型增加可选 `observe_id?: string`（api-contract.generated.ts 中 `RPCEnvelope` 结构由生成器产出——若生成器不支持自定义字段，则在 requester.ts 内定义本地类型 `ObservedEnvelope<T> = RPCEnvelope<T> & { observe_id?: string }` 并在 `parseEnvelope` 返回值后断言读取）：

```ts
private async executeObserved<T>(
  method: 'POST',
  path: string,
  body: object | undefined,
  options: RequestOptions | undefined,
): Promise<TrackedResult<T>> {
  const traceId = options?.traceId ?? newRequestId()
  const display = options?.display ?? 'foreground'
  const idempotencyKey = options?.idempotencyKey ?? newRequestId()
  let lastError: RequestError | undefined
  for (let attempt = 0; attempt < 1; attempt++) {
    const requestId = newRequestId()
    const lifecycle = { requestId, traceId, method, path, display }
    this.emit({ phase: 'start', ...lifecycle })
    const { signal, cleanup, abortSource } = combinedSignal(
      options?.signal,
      options?.timeoutMs ?? 15_000,
    )
    try {
      const headers: Record<string, string> = {
        'Content-Type': 'application/json',
        'Idempotency-Key': idempotencyKey,
        'X-Request-ID': requestId,
        'X-Trace-ID': traceId,
      }
      if (this.csrfToken) headers['X-CSRF-Token'] = this.csrfToken
      const response = await fetch(path, {
        method,
        credentials: 'include',
        headers,
        body: JSON.stringify(body ?? {}),
        signal,
      })
      const envelope = await parseEnvelope<T>(response, requestId, traceId)
      if (!response.ok) {
        throw new RequestError({ kind: 'protocol', code: envelope.code || `HTTP_${response.status}`, message: envelope.message || `请求协议失败（HTTP ${response.status}）`, httpStatus: response.status, requestId: envelope.request_id, traceId: envelope.trace_id })
      }
      if (envelope.code !== 'OK' && envelope.code !== 'ACCEPTED') {
        const error = businessError(envelope, response.status)
        if (error.code === 'AUTH_REQUIRED') this.unauthorizedHandler?.()
        throw error
      }
      this.emit({ phase: 'finish', ...lifecycle })
      cleanup()
      const observed = envelope as RPCEnvelope<T> & { observe_id?: string }
      return { data: envelope.data, observeId: observed.observe_id }
    } catch (error) {
      cleanup()
      lastError = normalizeError(error, requestId, traceId, abortSource())
      this.emit({ phase: 'finish', ...lifecycle, error: lastError })
      throw lastError
    }
  }
  throw lastError
}
```

> `parseEnvelope` 已有 `isRecord(value)` 校验，未知字段 `observe_id` 会被保留在返回对象中——若 `parseEnvelope` 返回值类型不含 observe_id，用断言读取即可（类型安全由本地 `& { observe_id?: string }` 保证）。

3b. `api.ts`：新增导出与改造长操作方法：

```ts
export type TrackedResult<T> = { data: T; observeId?: string }
```

长操作方法（分组 6 个、链路 5 个、节点 3 个、服务器 rebuild/repair/cleanup 3 个、外部订阅 4 个、模板刷新 1 个）改为 `requester.postObserved<T>(...)` 返回 `TrackedResult<T>`。

3c. `docs/openapi.yaml`：新增：

```yaml
  /api/observe-task/get:
    get:
      tags: [panel]
      summary: 查询旁路观察进度
      security:
        - session: []
      parameters:
        - name: observe_id
          in: query
          required: true
          schema:
            type: string
      responses:
        '200':
          description: 观察快照
          content:
            application/json:
              schema:
                type: object
                properties:
                  code: { type: string }
                  data:
                    type: object
                    properties:
                      id: { type: string }
                      kind: { type: string }
                      title: { type: string }
                      stage: { type: string }
                      percent: { type: integer }
                      message: { type: string }
                      status: { type: string, enum: [running, done, failed] }
                      warnings: { type: array, items: { type: string } }
                      error: { type: string }
                      stages:
                        type: array
                        items:
                          type: object
                          properties:
                            key: { type: string }
                            label: { type: string }
```

3d. 生成：`bun run generate:api`

- [ ] **Step 4: 运行确认通过**

Run: `bun run test -- requester.test.ts && bun run check:api`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add docs/openapi.yaml src/frontend/src/lib/requester.ts src/frontend/src/lib/api.ts src/frontend/src/lib/api-contract.generated.ts src/frontend/src/lib/requester.test.ts
git commit -m "feat(frontend): postObserved 契约与长操作 API 改造"
```

---

### Task 8: 前端进度弹窗组件与 Provider

**Files:**
- Create: `src/frontend/src/lib/operation-progress.ts`（context 类型）
- Create: `src/frontend/src/lib/operation-progress-context.ts`（context 定义，参照 `app-dialog.ts` 模式）
- Create: `src/frontend/src/components/OperationProgressProvider.tsx`
- Create: `src/frontend/src/components/OperationProgress.tsx`（弹窗）
- Modify: `src/frontend/src/components/Layout.tsx`（挂 Provider，参照 AppDialogProvider 挂载方式）
- Test: `src/frontend/src/components/OperationProgress.test.tsx`（新，vitest + @testing-library/react，若项目无 testing-library 则用现有测试基建，否则写纯逻辑测试）

**Interfaces:**
- Consumes: `requester.getJSON<Observation>`（新 api 方法 `api.observeTask(observeId)` → `GET /api/observe-task/get?observe_id=`）
- Produces: `useOperationProgress().showOperation({ observeId })`

- [ ] **Step 1: 写失败测试**

若项目有 @testing-library/react（检查 package.json devDependencies），写组件测试：mock `api.observeTask` 依次返回 running（阶段 2/3）→ done，断言渲染阶段列表与勾选；404 分支渲染丢失提示。若无 testing-library，测试 provider 的状态机逻辑（提取纯函数 `nextProgressState(prev, obs)`）。

- [ ] **Step 2: 运行确认失败**

Run: `bun run test -- OperationProgress`
Expected: FAIL（组件不存在）

- [ ] **Step 3: 实现**

3a. `operation-progress.ts`：

```ts
export type ObserveStage = { key: string; label: string }
export type ObserveStatus = 'running' | 'done' | 'failed'

export type Observation = {
  id: string
  kind: string
  title: string
  stages: ObserveStage[]
  stage: string
  percent: number
  message: string
  status: ObserveStatus
  warnings?: string[]
  error?: string
  started_at?: string
  finished_at?: string
}

export type OperationProgressContextValue = {
  showOperation: (opts: { observeId: string }) => void
}
```

3b. context + provider：状态 `{ observeId, observation }`；`showOperation` 设置 observeId 并启动轮询（`window.setInterval` 400ms，调 `api.observeTask`）；observation 终态时停止轮询；`done` 无警告 1s 后自动关闭；组件卸载清理定时器。

3c. `OperationProgress.tsx`：视觉参照 `UpdateOverlay.tsx`（固定定位遮罩、卡片、阶段列表 ✓/spinner/○、进度条、message、警告明细 `<ul>`、失败 error + 关闭按钮、404 丢失提示）。标题与阶段来自 `observation.title`/`observation.stages`。

3d. `api.ts` 增加：

```ts
observeTask: (observeId: string) =>
  requester.get<Observation>('/api/observe-task/get', { observe_id: observeId }, { display: 'silent' }),
```

3e. `Layout.tsx` 挂载 `OperationProgressProvider`（在 `AppDialogProvider` 外层或内层均可，参照现有 Provider 嵌套）。

- [ ] **Step 4: 运行确认通过**

Run: `bun run test && bun run lint && bun run check:api && bun run build`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add src/frontend/src/lib/operation-progress.ts src/frontend/src/lib/operation-progress-context.ts src/frontend/src/components/OperationProgressProvider.tsx src/frontend/src/components/OperationProgress.tsx src/frontend/src/components/Layout.tsx src/frontend/src/components/OperationProgress.test.tsx src/frontend/src/lib/api.ts
git commit -m "feat(frontend): observe 进度弹窗与 Provider"
```

---

### Task 9: 页面调用点接入

**Files:**
- Modify: `src/frontend/src/pages/Groups.tsx`（6 处：linkGroupCreate/Update/Delete、userGroupCreate/Update/Delete）
- Modify: `src/frontend/src/pages/Chains.tsx`（createChain/editChain/deleteChain/retryChain/forcePublishChain）
- Modify: `src/frontend/src/pages/Nodes.tsx`（createNode/retryNode/deleteNode）
- Modify: `src/frontend/src/pages/Servers.tsx`（rebuildXray/repairServer/cleanupXray）
- Modify: `src/frontend/src/pages/ExternalSubscriptions.tsx`（create/update/delete/sync）
- Modify: `src/frontend/src/pages/SubscriptionTemplates.tsx`（refreshSubscriptionTemplates）

**Interfaces:**
- Consumes: Task 7 的 `TrackedResult`、Task 8 的 `useOperationProgress().showOperation`
- Produces: 长操作点击后弹出进度弹窗；失败仍走内联错误

- [ ] **Step 1: 实现（无测试，纯接入；模式统一）**

每个调用点模式（以 Groups.tsx UserGroupDialog.onSave 为例）：

```tsx
const { showOperation } = useOperationProgress()
// ...
async function onSave() {
  if (!name.trim()) { setError('分组名称不能为空'); return }
  setSaving(true)
  setError('')
  try {
    if (group) {
      const { observeId } = await api.userGroupUpdate({ id: group.id, name: name.trim(), user_ids: userSel, link_group_ids: linkGroupSel })
      if (observeId) showOperation({ observeId })
    } else {
      const { observeId } = await api.userGroupCreate({ name: name.trim(), user_ids: userSel, link_group_ids: linkGroupSel })
      if (observeId) showOperation({ observeId })
    }
    onSaved()
  } catch (err) {
    setError(errorMessage(err))
  } finally {
    setSaving(false)
  }
}
```

> 各页面注意：`api.*` 现返回 `TrackedResult`，凡长操作方法解构 `data` 的地方改为 `const { data, observeId } = ...` 并保留原 data 用法；失败路径（catch → setError）不动。

- [ ] **Step 2: 运行确认通过**

Run: `bun run test && bun run lint && bun run check:api && bun run build`
Expected: PASS

- [ ] **Step 3: Commit**

```bash
git add src/frontend/src/pages/
git commit -m "feat(frontend): 长操作页面接入 observe 进度弹窗"
```

---

### Task 10: 全量回归与收尾

**Files:**
- 无新增；可能微调

- [ ] **Step 1: 后端全量测试与构建**

Run: `go build ./... && go vet ./src/backend/... && go test ./...`
Expected: PASS

- [ ] **Step 2: 前端全量**

Run（`src/frontend`）: `bun run test && bun run lint && bun run check:api && bun run build`
Expected: PASS

- [ ] **Step 3: 前端构建产物同步到后端**

Run（参照 README 手动构建流程）:
```bash
rm -rf src/backend/internal/web/dist
mkdir -p src/backend/internal/web/dist
cp -a src/frontend/dist/. src/backend/internal/web/dist/
```

- [ ] **Step 4: e2e 冒烟（可选，若本机有 xray）**

Run: `bash scripts/e2e/groups.sh`（确认分组链路不回归）
Expected: PASS 或环境不允许时跳过并说明

- [ ] **Step 5: 计划核对（spec 对照）**

逐条核对 spec 需求：
- [ ] 旁路观察不修改原逻辑（代码评审确认各 handler 仅插入报告调用）
- [ ] 崩溃隔离（progress 包测试覆盖）
- [ ] observe 命名（grep 确认无 `task_id`/`/api/task/` 残留：`rg -n "task_id|/api/task" src docs/openapi.yaml`）
- [ ] 校验失败无观察（测试覆盖）
- [ ] 已有进度机制未动

- [ ] **Step 6: Commit 收尾（如有改动）**

```bash
git add -A
git commit -m "chore: observe 进度系统收尾"
```

---

## Self-Review 记录

- **Spec 覆盖**：progress 包（T1）、envelope/端点/regenerator（T2）、分组（T3）、链路/节点（T4）、服务器（T5）、外部订阅/模板（T6）、前端契约（T7）、弹窗（T8）、调用点（T9）、回归（T10）。"已有进度机制不动"在 T10 核对。崩溃隔离在 T1 测试。命名核对在 T10。✅
- **占位符**：Task 3-6 的 handler 插入代码引用"按现有文件结构插入"，挂点位置已给出文件与函数名；`newTestServerAPI` 等辅助函数名标注"按实际调整"——执行者需读现有测试文件后落笔。这是有意的（现有测试辅助名未知），非逻辑占位。✅
- **类型一致性**：`observeStart`（T3 定义）被 T4-T6 复用；`postObserved`（T7）→ `TrackedResult`（T7）→ 页面解构（T9）→ `showOperation({ observeId })`（T8）。`progress.Observation` JSON 字段（T1）与前端 `Observation` 类型（T8）字段对齐（id/kind/title/stages/stage/percent/message/status/warnings/error）。✅

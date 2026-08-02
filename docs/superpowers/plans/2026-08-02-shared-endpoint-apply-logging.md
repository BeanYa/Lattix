# shared-endpoint.apply 日志细节完善与自动重试退避 实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让 shared-endpoint.apply 失败日志携带链路上下文（endpoint_id/chain_ids/attempts）与真实错误原因（systemd stderr + journal 尾部），并给端点自动重试加 60s 内存退避抑制刷屏。

**Architecture:** 三段式：① agent 侧 runner/restartApply 捕获并透传失败详情；② backend 侧 dispatcher 统一构造命令操作日志 Detail（新增 commandDetail 辅助函数）；③ chainFSM Evaluate 自动补发路径加内存退避（Dispatcher 持有 map，ack 时清除）。

**Tech Stack:** Go（src/agent、src/backend 两个独立 module），标准库 log/exec/sqlite（modernc）。

## Global Constraints

- 设计文档：`docs/superpowers/specs/2026-08-02-shared-endpoint-apply-logging-design.md`（已批准，commit c2782d1f9）。
- 测试命令：agent 在 `src/agent` 下运行 `go test ./internal/xray/...`；backend 在 `src/backend` 下运行 `go test ./internal/dispatch/... ./internal/logging/...`。
- 注释与错误消息使用中文，与仓库现有风格一致；不引入新依赖。
- 退避只节流 chainFSM Evaluate 的自动补发；管理员手动重试（panel `/api/chain/retry` → `ReconcileSharedEndpoint`）不受限。
- 错误消息/详情截断上限：stderr 8 行、journal 尾部 2000 字符（防刷爆 operation_log detail 列）。
- `endpointAutoRetryMinInterval` 必须是 `var`（测试可临时调小），默认 `60 * time.Second`。

---

### Task 1: Agent — SystemdRunner.Restart 捕获 stderr 与 journal 尾部

**Files:**
- Modify: `src/agent/internal/xray/runner.go:41-43`
- Test: `src/agent/internal/xray/runner_test.go`（新建）

**Interfaces:**
- Consumes: 现有 `SystemdRunner` 结构（`unit string`）、`exec`/`strings`/`strconv`/`time`（runner.go 已导入）。
- Produces: 包级函数 `firstLines(s string, n int) string`、`trimDetail(s string) string`；包级变量 `journalTail func(context.Context, string, int) string`（默认 `journalTailImpl`，测试可替换）；方法 `(*SystemdRunner).restartErr(err error, output string) error`。Task 2 依赖新的 Restart 错误格式。

- [ ] **Step 1: 写失败测试**

创建 `src/agent/internal/xray/runner_test.go`：

```go
package xray

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestSystemdRunnerRestartErrorIncludesStderrAndJournal(t *testing.T) {
	orig := journalTail
	journalTail = func(context.Context, string, int) string { return "journal-boom" }
	defer func() { journalTail = orig }()

	r := &SystemdRunner{unit: "xray"}
	err := r.restartErr(errors.New("exit status 1"), "Job for xray.service failed\nSee systemctl status xray")
	for _, want := range []string{"exit status 1", "Job for xray.service failed", "journal-boom"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("错误应包含 %q: %v", want, err)
		}
	}
}

func TestSystemdRunnerRestartErrorTrimsLongOutput(t *testing.T) {
	orig := journalTail
	journalTail = func(context.Context, string, int) string { return "" }
	defer func() { journalTail = orig }()

	r := &SystemdRunner{unit: "xray"}
	err := r.restartErr(errors.New("exit status 1"), strings.Repeat("x", 5000))
	if len(err.Error()) > 2100 {
		t.Fatalf("错误消息过长: %d", len(err.Error()))
	}
}

func TestFirstLines(t *testing.T) {
	if got := firstLines("a\nb\nc\nd\ne", 3); got != "a | b | c" {
		t.Fatalf("firstLines = %q", got)
	}
	if got := firstLines("  \n single \n", 3); got != "single" {
		t.Fatalf("firstLines 去空白 = %q", got)
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run（workdir `src/agent`）：`go test ./internal/xray/ -run 'TestSystemdRunnerRestartError|TestFirstLines' -v`
Expected: FAIL — `undefined: journalTail` / `undefined: firstLines`。

- [ ] **Step 3: 实现**

修改 `src/agent/internal/xray/runner.go`：

- 替换 `Restart`（41-43 行）：

```go
// Restart 实现 Runner。
func (r *SystemdRunner) Restart(ctx context.Context) error {
	out, err := exec.CommandContext(ctx, "systemctl", "restart", r.unit).CombinedOutput()
	if err != nil {
		return r.restartErr(err, string(out))
	}
	return nil
}

// restartErr 汇总重启失败详情：systemd stderr（截断 8 行）+ xray journal 尾部（best-effort）。
func (r *SystemdRunner) restartErr(err error, output string) error {
	detail := firstLines(output, 8)
	if tail := journalTail(context.Background(), r.unit, 20); tail != "" {
		detail += "; journal: " + tail
	}
	detail = trimDetail(detail)
	if detail != "" {
		return fmt.Errorf("systemctl restart %s: %v: %s", r.unit, err, detail)
	}
	return fmt.Errorf("systemctl restart %s: %v", r.unit, err)
}

// journalTail 抓取 systemd 单元最近 n 行 journal（失败/超时返回空串）。
var journalTail = journalTailImpl

func journalTailImpl(ctx context.Context, unit string, n int) string {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "journalctl", "-u", unit, "-n", strconv.Itoa(n), "--no-pager", "-o", "cat").Output()
	if err != nil {
		return ""
	}
	return trimDetail(string(out))
}

// firstLines 返回前 n 行（去空白、以 " | " 连接）。
func firstLines(s string, n int) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) > n {
		lines = lines[:n]
	}
	return strings.Join(lines, " | ")
}

// trimDetail 截断诊断文本到 2000 字符（防刷爆错误消息/日志列）。
func trimDetail(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 2000 {
		s = s[:2000]
	}
	return s
}
```

（`strconv`、`strings`、`time` 均已导入，无需改 import。）

- [ ] **Step 4: 运行测试确认通过**

Run（workdir `src/agent`）：`go test ./internal/xray/ -run 'TestSystemdRunnerRestartError|TestFirstLines' -v`
Expected: PASS（3 个测试）。

- [ ] **Step 5: 提交**

```bash
git add src/agent/internal/xray/runner.go src/agent/internal/xray/runner_test.go
git commit -m "feat(agent): capture systemd stderr and journal tail on xray restart failure"
```

---

### Task 2: Agent — restartApply 失败详情日志与共享端点失败透传

**Files:**
- Modify: `src/agent/internal/xray/chain.go:148-155`（restartApply）
- Modify: `src/agent/cmd/agent/main.go:646-714`（5 个 apply/remove 处理器失败日志）
- Test: `src/agent/internal/xray/endpoint_test.go`（追加用例）

**Interfaces:**
- Consumes: Task 1 的 Runner 错误格式（Restart 失败时错误已含 stderr/journal）；现有 `failingRestartRunner`（cleanup_test.go:282，`Restart` 返回 `errors.New("restart boom")`）；`newTestEndpointManager`（endpoint_test.go:46）。
- Produces: `ApplySharedEndpoint`/`ApplyChainHop`/`ApplyNode` 失败时返回的错误已含 runner 详情；main.go 处理器失败路径日志。

- [ ] **Step 1: 写失败测试**

在 `src/agent/internal/xray/endpoint_test.go` 追加（文件已导入 `os`，需在 import 中加 `strings`）：

```go
// TestApplySharedEndpointRestartFailureReportsDetail 验证重启失败时错误消息携带
// runner 的真实原因（stderr/journal 详情），且配置回滚到上一份。
func TestApplySharedEndpointRestartFailureReportsDetail(t *testing.T) {
	mgr := newTestEndpointManager(t)
	payload := endpointPortPayload(11, nil)
	if _, err := mgr.ApplySharedEndpoint(payload); err != nil {
		t.Fatal(err)
	}
	first, err := os.ReadFile(mgr.configPath)
	if err != nil {
		t.Fatal(err)
	}
	mgr.runner = &failingRestartRunner{}
	_, err = mgr.ApplySharedEndpoint(payload)
	if err == nil {
		t.Fatal("重启失败应返回错误")
	}
	if !strings.Contains(err.Error(), "restart boom") {
		t.Fatalf("错误应包含 runner 详情: %v", err)
	}
	restored, err := os.ReadFile(mgr.configPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(restored) != string(first) {
		t.Fatal("失败后配置应回滚到上一份")
	}
}
```

- [ ] **Step 2: 运行测试确认基线**

Run（workdir `src/agent`）：`go test ./internal/xray/ -run TestApplySharedEndpointRestartFailureReportsDetail -v`
Expected: PASS（回归锁定：`restartApply` 已把 runner 错误经 `%v` 透传，此测试锁定"详情透传 + 失败回滚"行为，防止后续改动丢失详情；Step 3 的实现变更不改变该行为）。

- [ ] **Step 3: 实现**

修改 `src/agent/internal/xray/chain.go`（148-155 行）：

```go
// restartApply 重启 xray 使落盘配置生效，失败恢复上一份并再次重启（§6 步骤 7-8）。
func (m *Manager) restartApply() error {
	if err := m.runner.Restart(context.Background()); err != nil {
		log.Printf("xray: restart apply failed: %v（回滚上一份配置并再次重启）", err)
		m.restorePrev()
		_ = m.runner.Restart(context.Background())
		return fmt.Errorf("重启失败(%v)，已回滚配置", err)
	}
	return nil
}
```

（chain.go 已导入 `log`；错误消息现在自动携带 Task 1 的 stderr/journal 详情。）

修改 `src/agent/cmd/agent/main.go` 的 5 个处理器，在失败分支补日志（`replyResult` 前）：

`TypeApplyNode`（646-653 行）：

```go
	case shared.TypeApplyNode:
		var p shared.ApplyNodePayload
		if !parseData(sc, env, &p) {
			return
		}
		log.Printf("node.apply request_id=%s node=%d users=%d", env.RequestID, p.NodeID, len(p.UserUUIDs))
		realized, err := mgr.ApplyNode(p.NodeID, p.Config, p.UserUUIDs, p.DestCandidates, p.PortCandidates)
		if err != nil {
			log.Printf("node.apply failed request_id=%s node=%d: %v", env.RequestID, p.NodeID, err)
		}
		replyResult(sc, env, resultOf(p.NodeID, realized), err)
```

`TypeApplyChainHop`（655-665 行）：

```go
	case shared.TypeApplyChainHop:
		var p shared.ApplyChainHopPayload
		if !parseData(sc, env, &p) {
			return
		}
		log.Printf("chain-hop.apply request_id=%s chain=%d hop=%d kind=%s", env.RequestID, p.ChainID, p.HopID, p.Kind)
		realized, err := mgr.ApplyChainHop(p)
		if err != nil {
			log.Printf("chain-hop.apply failed request_id=%s chain=%d hop=%d kind=%s: %v",
				env.RequestID, p.ChainID, p.HopID, p.Kind, err)
		}
		if err == nil {
			persistChainPieces(statePath, st, mgr)
		}
		replyResult(sc, env, resultOfHop(p.HopID, p.Kind, realized), err)
```

`TypeApplySharedEndpoint`（667-678 行）：

```go
	case shared.TypeApplySharedEndpoint:
		var p shared.ApplySharedEndpointPayload
		if !parseData(sc, env, &p) {
			return
		}
		log.Printf("shared-endpoint.apply request_id=%s endpoint=%d clients=%d routes=%d",
			env.RequestID, p.EndpointID, len(p.Clients), len(p.Routes))
		realized, err := mgr.ApplySharedEndpoint(p)
		if err != nil {
			log.Printf("shared-endpoint.apply failed request_id=%s endpoint=%d clients=%d routes=%d: %v",
				env.RequestID, p.EndpointID, len(p.Clients), len(p.Routes), err)
		}
		if err == nil {
			persistChainPieces(statePath, st, mgr)
		}
		replyResult(sc, env, resultOfEndpoint(p.EndpointID, realized), err)
```

`TypeRemoveSharedEndpoint`（680-689 行）：

```go
	case shared.TypeRemoveSharedEndpoint:
		var p shared.RemoveSharedEndpointPayload
		if !parseData(sc, env, &p) {
			return
		}
		log.Printf("shared-endpoint.remove request_id=%s endpoint=%d", env.RequestID, p.EndpointID)
		err := mgr.RemoveSharedEndpoint(p.EndpointID)
		if err != nil {
			log.Printf("shared-endpoint.remove failed request_id=%s endpoint=%d: %v", env.RequestID, p.EndpointID, err)
		}
		if err == nil {
			persistChainPieces(statePath, st, mgr)
		}
		replyResult(sc, env, resultOfEndpoint(p.EndpointID, nil), err)
```

`TypeRemoveChainHop`（704-714 行）：

```go
	case shared.TypeRemoveChainHop:
		var p shared.RemoveChainHopPayload
		if !parseData(sc, env, &p) {
			return
		}
		log.Printf("chain-hop.remove request_id=%s hop=%d kind=%s", env.RequestID, p.HopID, p.Kind)
		err := mgr.RemoveChainHop(p.HopID, p.Kind)
		if err != nil {
			log.Printf("chain-hop.remove failed request_id=%s hop=%d kind=%s: %v", env.RequestID, p.HopID, p.Kind, err)
		}
		if err == nil {
			persistChainPieces(statePath, st, mgr)
		}
		replyResult(sc, env, resultOfHop(p.HopID, p.Kind, nil), err)
```

- [ ] **Step 4: 运行测试确认通过**

Run（workdir `src/agent`）：`go test ./internal/xray/ -run TestApplySharedEndpointRestartFailureReportsDetail -v`
Expected: PASS。

Run（workdir `src/agent`）：`go build ./cmd/agent`
Expected: 无输出（编译通过）。

- [ ] **Step 5: 提交**

```bash
git add src/agent/internal/xray/chain.go src/agent/internal/xray/endpoint_test.go src/agent/cmd/agent/main.go
git commit -m "feat(agent): log restart failure detail before rollback and per-command failure logs"
```

---

### Task 3: Backend — 命令操作日志 Detail 携带链路上下文

**Files:**
- Modify: `src/backend/internal/dispatch/dispatcher.go:342-346, 931-936, 1015-1020`（三处 recordOperation）
- Test: `src/backend/internal/dispatch/command_log_test.go`（新建）

**Interfaces:**
- Consumes: `store.Command`（`ID`/`Type`/`Data`/`Attempts`）、`store.ChainIDsByEndpoint(ctx, endpointID)`（endpoints.go:354）、`logging.OpenOperationStore`、`shared.ApplySharedEndpointPayload`/`RemoveSharedEndpointPayload`/`ApplyResultPayload`、测试辅助 `createDirectSharedChain`（endpoint_test.go:12）、`fakeRequester`（chain_test.go:16）。
- Produces: 方法 `(*Dispatcher).commandDetail(ctx context.Context, cmd store.Command, hopID int64, errMsg string) map[string]any`；函数 `commandEndpointID(cmd store.Command) (int64, bool)`。Task 4 不依赖本任务。

- [ ] **Step 1: 写失败测试**

创建 `src/backend/internal/dispatch/command_log_test.go`：

```go
package dispatch

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"lattix/backend/internal/logging"
	"lattix/backend/internal/store"
	"lattix/shared"
)

// TestCommandFailedDetailCarriesEndpointAndChainContext 验证 shared-endpoint 命令失败
// 的操作日志 Detail 携带 endpoint_id/chain_ids/attempts/error，便于按链路定位。
func TestCommandFailedDetailCarriesEndpointAndChainContext(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	serverID, _ := st.CreateServer(ctx, "entry", "entry.test", "token", store.MachineTypeDirect, "", "", "US", "")
	config := json.RawMessage(`{"protocol":"vless","port":443,"template":{}}`)
	endpoint, _, err := st.EnsureSharedEndpoint(ctx, serverID, shared.ProtocolVLESS, 443, "profile", config)
	if err != nil {
		t.Fatal(err)
	}
	createDirectSharedChain(t, st, serverID, endpoint.ID, "detail-chain")

	opLog, err := logging.OpenOperationStore(filepath.Join(t.TempDir(), "op.db"), 100)
	if err != nil {
		t.Fatal(err)
	}
	defer opLog.Close()
	d := New(st, &fakeRequester{online: map[int64]bool{serverID: true}})
	d.OperationLog = opLog

	if _, err := d.Enqueue(ctx, serverID, shared.TypeApplySharedEndpoint,
		shared.ApplySharedEndpointPayload{EndpointID: endpoint.ID}); err != nil {
		t.Fatal(err)
	}
	cmds, err := st.CommandsByType(ctx, shared.TypeApplySharedEndpoint)
	if err != nil || len(cmds) != 1 {
		t.Fatalf("apply 命令 = %d 条, err=%v", len(cmds), err)
	}
	cmd := cmds[0]
	d.handleCommandResponse(serverID, shared.Envelope{
		Kind: shared.KindResponse, Type: shared.TypeApplySharedEndpoint,
		RequestID: cmd.RequestID, TraceID: cmd.TraceID,
		Code:    shared.CodeInternalError,
		Message: "重启失败(exit status 1)，已回滚配置",
		Data:    mustMarshalEndpointResult(t, endpoint.ID),
	})

	items, _, err := opLog.List(ctx, logging.OperationFilter{Category: logging.CategoryCommand}, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Action != "command.failed" {
		t.Fatalf("operation entries = %+v", items)
	}
	detail := items[0].Detail
	for _, want := range []string{
		`"command_id"`, `"type":"shared-endpoint.apply"`, `"hop_id":0`,
		`"endpoint_id"`, `"chain_ids"`, `"attempts":1`, `"error"`, "重启失败",
	} {
		if !strings.Contains(detail, want) {
			t.Fatalf("Detail 缺少 %s: %s", want, detail)
		}
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(detail), &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed["endpoint_id"].(float64) != float64(endpoint.ID) {
		t.Fatalf("endpoint_id = %v", parsed["endpoint_id"])
	}
	chainIDs, _ := st.ChainIDsByEndpoint(ctx, endpoint.ID)
	if len(chainIDs) != 1 || parsed["chain_ids"].([]any)[0].(float64) != float64(chainIDs[0]) {
		t.Fatalf("chain_ids = %v", parsed["chain_ids"])
	}
}

func mustMarshalEndpointResult(t *testing.T, endpointID int64) []byte {
	t.Helper()
	b, err := json.Marshal(shared.ApplyResultPayload{EndpointID: endpointID})
	if err != nil {
		t.Fatal(err)
	}
	return b
}
```

- [ ] **Step 2: 运行测试确认失败**

Run（workdir `src/backend`）：`go test ./internal/dispatch/ -run TestCommandFailedDetailCarriesEndpointAndChainContext -v`
Expected: FAIL — Detail 缺少 `"endpoint_id"`（当前只有 command_id/type/hop_id/error）。

- [ ] **Step 3: 实现**

修改 `src/backend/internal/dispatch/dispatcher.go`：

- 在 `handleCommandResponse` 附近的合适位置（如 `logWebSocketRPC` 前）新增两个函数：

```go
// commandDetail 构造命令操作日志 Detail：统一携带 command_id/type/attempts/hop_id，
// shared-endpoint 命令附加 endpoint_id 与使用它的 chain_ids，失败/死信附加 error。
func (d *Dispatcher) commandDetail(ctx context.Context, cmd store.Command, hopID int64, errMsg string) map[string]any {
	detail := map[string]any{
		"command_id": cmd.ID,
		"type":       cmd.Type,
		"hop_id":     hopID,
		"attempts":   cmd.Attempts,
	}
	if endpointID, ok := commandEndpointID(cmd); ok {
		detail["endpoint_id"] = endpointID
		if chainIDs, err := d.st.ChainIDsByEndpoint(ctx, endpointID); err != nil {
			log.Printf("dispatch: chain ids for endpoint %d: %v", endpointID, err)
		} else if len(chainIDs) > 0 {
			detail["chain_ids"] = chainIDs
		}
	}
	if errMsg != "" {
		detail["error"] = errMsg
	}
	return detail
}

// commandEndpointID 从命令数据解析 shared-endpoint 相关命令的端点 id（非端点命令返回 false）。
func commandEndpointID(cmd store.Command) (int64, bool) {
	switch cmd.Type {
	case shared.TypeApplySharedEndpoint:
		var p shared.ApplySharedEndpointPayload
		if json.Unmarshal(cmd.Data, &p) == nil && p.EndpointID != 0 {
			return p.EndpointID, true
		}
	case shared.TypeRemoveSharedEndpoint:
		var p shared.RemoveSharedEndpointPayload
		if json.Unmarshal(cmd.Data, &p) == nil && p.EndpointID != 0 {
			return p.EndpointID, true
		}
	}
	return 0, false
}
```

- 替换 `command.succeeded` 的 Detail（934 行）：

```go
		d.recordOperation(logging.OperationEvent{
			Severity: logging.SeverityInfo, Category: logging.CategoryCommand,
			Action: "command.succeeded", ServerID: &serverID, NodeID: optionalID(p.NodeID),
			Detail:    d.commandDetail(ctx, *cmd, p.HopID, ""),
			RequestID: env.RequestID, TraceID: env.TraceID,
		})
```

- 替换 `command.failed` 的 Detail（1018 行）：

```go
		d.recordOperation(logging.OperationEvent{
			Severity: logging.SeverityError, Category: logging.CategoryCommand,
			Action: "command.failed", ServerID: &serverID, NodeID: optionalID(p.NodeID),
			Detail:    d.commandDetail(ctx, *cmd, p.HopID, errorMessage),
			RequestID: env.RequestID, TraceID: env.TraceID,
		})
```

- 替换 `command.dead_lettered` 的 Detail（342-346 行；该路径的上下文变量名是 `c`）：

```go
			d.recordOperation(logging.OperationEvent{
				Severity: logging.SeverityError, Category: logging.CategoryCommand,
				Action: "command.dead_lettered", ServerID: &serverID,
				Detail: d.commandDetail(ctx, c, 0,
					fmt.Sprintf("command %d dead-lettered after %d attempts", c.ID, c.Attempts)),
			})
```

（`ctx` 在 `Flush`/`handleCommandResponse` 均已定义；`store`、`shared`、`json`、`log`、`fmt` 均已导入。）

- [ ] **Step 4: 运行测试确认通过**

Run（workdir `src/backend`）：`go test ./internal/dispatch/ -run TestCommandFailedDetailCarriesEndpointAndChainContext -v`
Expected: PASS。

Run（workdir `src/backend`）：`go test ./internal/dispatch/`
Expected: 全量 PASS（回归既有回执/重试测试）。

- [ ] **Step 5: 提交**

```bash
git add src/backend/internal/dispatch/dispatcher.go src/backend/internal/dispatch/command_log_test.go
git commit -m "feat(dispatch): operation log detail carries endpoint and chain context"
```

---

### Task 4: Backend — 端点自动重试内存退避

**Files:**
- Modify: `src/backend/internal/dispatch/dispatcher.go`（Dispatcher 结构体 + New + 成功回执分支）
- Modify: `src/backend/internal/dispatch/chain_fsm.go:175-184`（Evaluate 自动补发路径）
- Test: `src/backend/internal/dispatch/endpoint_test.go`（追加用例）

**Interfaces:**
- Consumes: Task 3 无依赖；`store.SetSharedEndpointFailed/SetSharedEndpointActive`、`d.recomputeChain`、`createDirectSharedChain`。
- Produces: `Dispatcher` 字段 `endpointRetryMu sync.Mutex`、`endpointRetriedAt map[int64]time.Time`（New 中初始化）；方法 `(*Dispatcher).allowEndpointAutoRetry(endpointID int64) bool`、`(*Dispatcher).clearEndpointRetry(endpointID int64)`；包级 `var endpointAutoRetryMinInterval = 60 * time.Second`。

- [ ] **Step 1: 写失败测试**

在 `src/backend/internal/dispatch/endpoint_test.go` 追加（文件已导入 `context`/`encoding/json`/`testing`/`store`/`shared`，需在 import 中加 `time`）：

```go
// TestEvaluateEndpointAutoRetryBackoff 验证端点自动重试退避：间隔内不重复补发、
// 超间隔后补发、端点生效（ack 清除记录）后再次失败可立即补发。
func TestEvaluateEndpointAutoRetryBackoff(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	serverID, _ := st.CreateServer(ctx, "entry", "entry.test", "token", store.MachineTypeDirect, "", "", "US", "")
	config := json.RawMessage(`{"protocol":"vless","port":443,"template":{}}`)
	endpoint, _, err := st.EnsureSharedEndpoint(ctx, serverID, shared.ProtocolVLESS, 443, "profile", config)
	if err != nil {
		t.Fatal(err)
	}
	deployment := createDirectSharedChain(t, st, serverID, endpoint.ID, "backoff")
	chainID := deployment.ChainID
	hops, _ := st.ChainHops(ctx, chainID)
	for _, h := range hops {
		if err := st.SetChainHopStatus(ctx, h.ID, store.HopStatusActive, ""); err != nil {
			t.Fatal(err)
		}
	}

	d := New(st, &fakeRequester{online: map[int64]bool{serverID: true}})
	if err := d.ReconcileSharedEndpoint(ctx, endpoint.ID); err != nil {
		t.Fatal(err)
	}
	count := func() int {
		cmds, _ := st.CommandsByType(ctx, shared.TypeApplySharedEndpoint)
		return len(cmds)
	}
	if got := count(); got != 1 {
		t.Fatalf("基线 apply 命令数 = %d，期望 1", got)
	}

	origInterval := endpointAutoRetryMinInterval
	defer func() { endpointAutoRetryMinInterval = origInterval }()

	// 端点 failed 后首次重算：无退避记录 → 允许自动补发（记录本次时间）。
	if err := st.SetSharedEndpointFailed(ctx, endpoint.ID, "boom"); err != nil {
		t.Fatal(err)
	}
	d.recomputeChain(ctx, chainID)
	if got := count(); got != 2 {
		t.Fatalf("首次失败后应补发，实际 %d 条", got)
	}

	// 再次置 failed 并重算：间隔内（1h）自动补发被抑制。
	endpointAutoRetryMinInterval = time.Hour
	if err := st.SetSharedEndpointFailed(ctx, endpoint.ID, "boom"); err != nil {
		t.Fatal(err)
	}
	d.recomputeChain(ctx, chainID)
	if got := count(); got != 2 {
		t.Fatalf("退避期间不应重复补发，实际 %d 条", got)
	}

	// 间隔缩短后重算：自动补发恢复。
	endpointAutoRetryMinInterval = time.Millisecond
	d.recomputeChain(ctx, chainID)
	if got := count(); got != 3 {
		t.Fatalf("超间隔后应补发，实际 %d 条", got)
	}

	// 端点生效（ack 路径会调用 clearEndpointRetry）清除退避记录：再次失败可立即补发。
	realized := json.RawMessage(`{"port":443}`)
	if err := st.SetSharedEndpointActive(ctx, endpoint.ID, realized); err != nil {
		t.Fatal(err)
	}
	d.clearEndpointRetry(endpoint.ID)
	if err := st.SetSharedEndpointFailed(ctx, endpoint.ID, "boom"); err != nil {
		t.Fatal(err)
	}
	d.recomputeChain(ctx, chainID)
	if got := count(); got != 4 {
		t.Fatalf("ack 清除退避后应立即补发，实际 %d 条", got)
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run（workdir `src/backend`）：`go test ./internal/dispatch/ -run TestEvaluateEndpointAutoRetryBackoff -v`
Expected: FAIL — `undefined: endpointAutoRetryMinInterval` / `undefined: allowEndpointAutoRetry`。

- [ ] **Step 3: 实现**

修改 `src/backend/internal/dispatch/dispatcher.go`：

- Dispatcher 结构体（`testProgress` 字段附近，49-53 行区域）追加：

```go
	endpointRetryMu   sync.Mutex
	endpointRetriedAt map[int64]time.Time // 端点自动重试退避：key endpointID → 上次自动补发时间
```

- `New`（62-71 行）初始化：

```go
	d := &Dispatcher{
		st: st, req: req, flushMu: make(map[int64]*sync.Mutex),
		testProgress:   make(map[int64]shared.ServerTestProgressPayload),
		cleanupWaiters: make(map[string]chan cleanupWaiterOut),
		endpointRetriedAt: make(map[int64]time.Time),
	}
```

- `maxCommandAttempts` 常量附近（310-311 行）追加：

```go
// endpointAutoRetryMinInterval 端点自动重试最小间隔（抑制 failed 端点重复补发刷屏）。
// var 而非 const：测试可临时调小。
var endpointAutoRetryMinInterval = 60 * time.Second
```

- 在 `handleCommandResponse` 附近（如 `logWebSocketRPC` 之后）追加两个方法：

```go
// allowEndpointAutoRetry 端点自动重试退避：距上次自动补发不足间隔时拒绝；允许时记录本次时间。
func (d *Dispatcher) allowEndpointAutoRetry(endpointID int64) bool {
	d.endpointRetryMu.Lock()
	defer d.endpointRetryMu.Unlock()
	if last, ok := d.endpointRetriedAt[endpointID]; ok && time.Since(last) < endpointAutoRetryMinInterval {
		return false
	}
	d.endpointRetriedAt[endpointID] = time.Now()
	return true
}

// clearEndpointRetry 清除端点的自动重试退避记录（apply 成功回执时调用）。
func (d *Dispatcher) clearEndpointRetry(endpointID int64) {
	d.endpointRetryMu.Lock()
	defer d.endpointRetryMu.Unlock()
	delete(d.endpointRetriedAt, endpointID)
}
```

- 成功回执的共享端点分支（954-955 行 `if p.EndpointID != 0 {` 后）加清除：

```go
		if p.EndpointID != 0 {
			d.clearEndpointRetry(p.EndpointID)
			realized, _ := json.Marshal(p.RealizedConfig)
```

修改 `src/backend/internal/dispatch/chain_fsm.go`（175-184 行，自动补发块）：

```go
			if endpoint != nil && endpoint.Status != store.EndpointStatusApplying && f.d.req.IsOnline(endpoint.ServerID) {
				if !f.d.allowEndpointAutoRetry(endpoint.ID) {
					log.Printf("chain_fsm: chain %d endpoint %d auto-retry suppressed (interval %s)",
						chainID, endpoint.ID, endpointAutoRetryMinInterval)
					return
				}
				if err := f.d.ReconcileSharedEndpoint(ctx, chain.EndpointID); err != nil {
					log.Printf("chain_fsm: chain %d auto-reconcile endpoint %d: %v", chainID, chain.EndpointID, err)
				}
			}
```

（`time`、`sync`、`log` 均已导入。）

- [ ] **Step 4: 运行测试确认通过**

Run（workdir `src/backend`）：`go test ./internal/dispatch/ -run TestEvaluateEndpointAutoRetryBackoff -v`
Expected: PASS。

Run（workdir `src/backend`）：`go test ./internal/dispatch/`
Expected: 全量 PASS（含既有 `TestEvaluateDoesNotDoubleApplyInFlightEndpoint` 幂等回归）。

- [ ] **Step 5: 提交**

```bash
git add src/backend/internal/dispatch/dispatcher.go src/backend/internal/dispatch/chain_fsm.go src/backend/internal/dispatch/endpoint_test.go
git commit -m "feat(dispatch): throttle endpoint auto-retry with 60s in-memory backoff"
```

---

### Task 5: 全量验证与收尾

**Files:** 无代码改动。

- [ ] **Step 1: agent 模块验证**

Run（workdir `src/agent`）：`go build ./... && go vet ./internal/xray/... && go test ./internal/xray/...`
Expected: 全部通过（build 无输出；vet 无告警；测试 PASS）。

- [ ] **Step 2: backend 模块验证**

Run（workdir `src/backend`）：`go build ./... && go vet ./internal/dispatch/... && go test ./internal/dispatch/... ./internal/logging/...`
Expected: 全部通过。

- [ ] **Step 3: 状态确认**

Run（workdir 仓库根）：`git status --short`
Expected: 工作区干净（4 个任务提交均已完成）。

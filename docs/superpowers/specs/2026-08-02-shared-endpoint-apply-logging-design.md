# shared-endpoint.apply 日志细节完善与自动重试退避的设计

日期：2026-08-02

## 背景与问题

面板操作日志中出现 100+ 条 `shared-endpoint.apply` 失败记录，但每条只有：

```json
{ "command_id": 87772, "error": "重启失败(exit status 1)，已回滚配置", "hop_id": 0, "type": "shared-endpoint.apply" }
```

两个关键信息缺失：

1. **链路（上下文）不可见**：无法知道该命令属于哪个共享端点、被哪些链使用、是第几次尝试。
2. **错误原因不可见**：`exit status 1` 只说明 `systemctl restart xray` 失败，看不到 xray 启动失败的真实原因（端口占用、配置错误等）。

根因定位：

- **链路断点**：`dispatcher.go` 的 `command.failed` 操作日志 Detail 只有 `{command_id, type, hop_id, error}`，未从命令数据/回执中提取 `endpoint_id`、`chain_ids`、`attempts`。
- **错误原因断点**：`SystemdRunner.Restart`（runner.go）用 `exec.Command(...).Run()`，只返回 `exit status 1`，不捕获 stderr，失败后也不取 journalctl 尾部。
- **重试风暴**：`chain_fsm.go` `Evaluate` 对 failed 端点在服务器在线时无条件自动补发（每次回执/订阅/评估触发都会再发一次），无退避 → 日志刷屏且掩盖真实根因。

## 目标

- 失败日志中可见：端点在途命令所属的 endpoint_id、chain_ids、attempts，以及 xray 重启失败的真实原因。
- 端点自动重试加最小间隔退避（内存态，60s），抑制刷屏；管理员手动重试不受限。

## 改动清单

### 1. Agent：让"为什么"可见

#### 1.1 `src/agent/internal/xray/runner.go` — `SystemdRunner.Restart`

- `exec.CommandContext(...).Run()` → 失败时用 `CombinedOutput()` 捕获 stderr。
- 失败时追加 `journalctl -u xray -n 20 --no-pager -o cat` 输出尾部（best-effort，截断到 ~4KB，多行合并进错误消息）。
- 返回错误形如：`systemctl restart xray: <stderr>; journal: <tail>`。

#### 1.2 `src/agent/internal/xray/chain.go` — `restartApply`

- 失败分支：先 `log.Printf` 完整错误（含 journal 尾部），再 `restorePrev()` 回滚并再次重启——保证 agent 本地日志记录的是**失败配置**的启动日志。
- 返回错误保持现有文案前缀（`重启失败(%v)，已回滚配置`），`%v` 现在携带 stderr/journal 详情。

#### 1.3 `src/agent/cmd/agent/main.go` — apply/remove 处理器失败路径

- `node.apply` / `chain-hop.apply` / `shared-endpoint.apply` / `shared-endpoint.remove` / `chain-hop.remove` 失败时补一行 `log.Printf("... failed request_id=%s node=%d/hop=%d/endpoint=%d: %v")`（入口已有成功侧上下文日志，补齐失败侧）。

### 2. Backend：让"链路"可见

#### 2.1 `src/backend/internal/dispatch/dispatcher.go` — 操作日志 Detail 扩充

统一扩充三处（`command.failed`、`command.succeeded`、`command.dead_lettered`）：

```go
Detail: map[string]any{
    "command_id":  cmdID,
    "type":        cmd.Type,
    "hop_id":      p.HopID,
    "endpoint_id": p.EndpointID, // 非 0 时才包含
    "chain_ids":   chainIDs,     // 仅 shared-endpoint 时查询
    "attempts":    cmd.Attempts,
    "error":       errorMessage, // 仅失败/死信
}
```

- `endpoint_id` 取自回执 payload `p.EndpointID`（死信路径从 `cmd.Data` 解析 `ApplySharedEndpointPayload`）。
- `chain_ids` 用现有 `d.st.ChainIDsByEndpoint(ctx, endpointID)` 查询（失败路径 `command.failed` 已查询该数据，复用；成功/死信路径按需新增）。
- `attempts` 取自 `store.Command.Attempts`。

### 3. Backend：自动重试退避（抑制刷屏）

`src/backend/internal/dispatch/dispatcher.go`（Dispatcher 结构）新增：

```go
retryMu    sync.Mutex
retriedAt  map[int64]time.Time // key: endpointID
```

`src/backend/internal/dispatch/chain_fsm.go` — `Evaluate` 条件 2 自动补发路径：

- 距 `retriedAt[endpointID]` 不足 60s → 跳过并 `log.Printf("chain_fsm: chain %d endpoint %d auto-retry suppressed (%s ago)")`，不再触发 `ReconcileSharedEndpoint`。
- 否则记录当前时间并补发。
- 端点 apply 成功（`handleCommandResponse` 成功路径 `p.EndpointID != 0`）时清除 `retriedAt`，使后续失败可立即重试。
- 管理员手动重试（panel `handleRetryChain` → `ReconcileSharedEndpoint`）不经过 Evaluate，不受节流影响。
- map 只在有失败记录时增长，随 ack 清除；面板重启后内存态自然失效（与现有 `testProgress` 模式一致）。

## 错误处理与边界

- journalctl/systemctl 不可用（用户态/非 systemd 环境）：`CombinedOutput` 失败仅返回 stderr；journal 抓取失败静默跳过，不影响主错误。
- 错误消息长度受控（stderr + journal 尾部截断），避免刷爆 operation_log detail 列。
- `chain_ids` 查询失败：记 `log.Printf` 并省略该字段，不阻塞失败回执处理。
- 退避只节流 Evaluate 自动补发；若端点重试仍失败，60s 后继续自动补发（幂等重发复用端口/密钥，语义不变）。

## 测试

- `src/agent/internal/xray`：runner 失败含 stderr 的用例；`restartApply` 回滚路径错误消息包含详情。
- `src/backend/internal/dispatch`：
  - `command.failed` Detail 含 endpoint_id/chain_ids/attempts 的断言；
  - Evaluate 退避：60s 内不重复下发、超时后下发、ack 后清除（用可注入时钟或调整常量）。
- 回归：`go test ./internal/xray/... ./internal/dispatch/...`。

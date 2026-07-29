---
kind: logging_system
name: Lattix 日志系统：操作日志与请求日志双通道设计
category: logging_system
scope:
    - '**'
source_files:
    - src/backend/internal/logging/operation.go
    - src/backend/internal/logging/request.go
    - src/backend/internal/logging/http.go
    - src/backend/internal/logging/context.go
    - docs/logging-design.md
---

## 1. 系统概览

Lattix 采用**双通道结构化日志**架构，将日志严格分为两类独立存储：
- **操作日志（Operation Log）**：记录业务状态变更、管理操作和系统事件，持久化到独立 SQLite 文件 `operation.db`
- **请求日志（Request Log）**：记录 HTTP/WebSocket RPC 的元数据，以 JSONL 格式追加写入文件

两类日志物理隔离、独立限额、独立清空，不进入业务数据库备份，也不提供导入导出功能。

## 2. 核心组件与文件

### 操作日志子系统 (`src/backend/internal/logging/operation.go`)
- 使用独立 SQLite 数据库，单连接模式避免竞争
- 固定类别枚举：`server`、`chain`、`user`、`settings`、`panel`、`agent`、`command`、`auth`、`log`
- 三级严重性：`info`、`warning`、`error`，由服务端根据动作自动判定
- 支持按严重级别、类别、服务器ID、操作员、关键字和时间范围查询
- 默认保留最新 1000 条，范围 100–100000 条
- 面板启停生命周期通过 `runtime_state` 表标记异常退出

### 请求日志子系统 (`src/backend/internal/logging/request.go`)
- 异步写入：内存队列（容量 1024）+ 单写协程串行追加
- JSONL 格式，每行独立 JSON 对象，支持分段归档
- 默认总容量 10 MiB，单段目标为总容量的 1/10（最小 64 KiB）
- 支持 Tail 读取最近 N 行（10/30/50/100），默认 30 行
- 写入失败降级：丢弃日志不影响业务响应，累计丢弃数可监控

### HTTP 中间件 (`src/backend/internal/logging/http.go`)
- 请求 ID 和追踪 ID 注入：`X-Request-ID`、`X-Trace-ID` 头
- 敏感信息脱敏：参数名匹配 token/password/secret/key 等关键字时替换为 `[REDACTED]`
- 路由级日志策略：`full`（全部记录）、`failures_only`（仅失败）、`none`（不记录）
- WebSocket 升级握手立即记录，命令型 WS RPC 有响应才记录
- 自动判断严重级别：HTTP 5xx/panic → error，4xx/慢请求 → warning

### 上下文传递 (`src/backend/internal/logging/context.go`)
- `RequestMeta` 结构体在 context 中传递请求元数据
- 白名单属性机制：只能通过 `AddSafeAttribute` 显式添加安全字段
- 幂等重放标记：`IdempotencyReplayed` 标识是否返回已持久化的首次执行结果

## 3. 存储架构

```
<业务数据库目录>/
├── lattix.db          # 业务数据库
└── logs/              # 日志根目录（权限 0700）
    ├── operation.db   # 操作日志 SQLite（权限 0600）
    └── requests/      # 请求日志目录
        ├── requests-current.jsonl     # 当前写入段
        └── requests-<UTC时间>-<纳秒>.jsonl  # 归档段
```

可通过启动参数 `-log-dir` 或环境变量 `LATTIX_LOG_DIR` 覆盖日志目录。

## 4. 关键设计决策

### 安全与脱敏
- 路径中的 `/sub/{token}` 使用 SHA-256 摘要前 4 字节替换
- 单个参数、路径、User-Agent、错误摘要限制 512 字节
- attributes JSON 总长度限制 4096 字节，超出时添加 `_truncated=true`
- 仅在回环地址信任 `X-Forwarded-For` 防止 IP 伪造

### 性能与可靠性
- 日志写入失败不阻断业务：队列满、文件写入失败直接丢弃
- 优雅关停预算 10 秒：HTTP 服务 8 秒 + 日志排空 2 秒
- 启动时修复不完整 JSONL 行，保证后续写入正确
- 丢弃数量聚合上报，不为每个丢弃请求单独写错误日志

### Agent 连接状态
- 真实状态跃迁：`agent.online`（离线→在线）、`agent.offline`（在线→离线）、`agent.reconnected`（连接替换）
- 旧连接被新连接替换后，其退出不会触发 `agent.offline`

## 5. 开发者规范

### 记录操作日志
```go
// 使用 OperationStore.Record 记录业务操作
store.Record(ctx, logging.OperationEvent{
    Severity:  logging.SeverityInfo,
    Category:  logging.CategoryServer,
    Action:    "server.updated",
    Detail:    map[string]interface{}{"before": before, "after": after},
    Operator:  operator,
    RequestID: requestID,
})
```

### 记录请求日志
```go
// 通过中间件自动记录，Handler 中设置 RPC 结果
rec.SetRPCOutcome(code, safeMessage)  // 设置业务码和安全消息
logging.AddSafeAttribute(ctx, "server_id", serverID)  // 仅允许白名单字段
```

### 路由日志策略
- 高频状态查询使用 `failures_only`
- 健康检查、日志读取、遥测使用 `none`
- 普通 API 默认 `full`，需评估频率调整策略

## 6. 前端集成

前端提供两个日志页面：
- `/logs/operations`：操作日志，服务端过滤分页
- `/logs/requests`：请求日志，客户端窗口过滤

刷新偏好保存在浏览器 `localStorage`，默认不自动刷新。设置页显示日志容量、丢弃数和实际目录路径。

## 7. 非目标声明

明确不包含：日志导入导出、请求体/完整响应体记录、全文索引、远程聚合、多管理员审计、旧 `/event-log` 路由兼容。
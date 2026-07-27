# 日志系统设计

本文描述 Lattix 当前日志系统的实现契约。日志分为**操作日志**和**请求日志**：

- 操作日志回答“谁在何时修改了什么、系统发生了什么状态变化”；
- 请求日志回答“哪些 API 请求进入了面板、结果和耗时如何”。

两类日志独立存储、独立限额、独立清空，不进入业务数据库备份，也不提供导入或导出功能。

## 1. 设计目标

1. 为服务器、Agent、链路、用户、设置、面板生命周期和命令执行留下可检索记录；
2. 准确区分 Agent 的在线、离线和连接替换，避免用虚假的离线/在线事件描述重连；
3. 记录 HTTP 与 WebSocket RPC 元数据，同时避免记录请求体、凭证和敏感参数；
4. 日志写入失败不阻断业务请求、状态更新或命令处理；
5. 通过固定容量限制日志占用，默认不自动刷新页面，降低面板压力；
6. 日志与业务数据物理隔离，避免操作日志 SQLite 竞争影响业务数据库。

## 2. 目录与存储

默认日志根目录是业务数据库同级的 `logs/`：

```text
<业务数据库目录>/
├── lattix.db
└── logs/
    ├── operation.db
    └── requests/
        ├── requests-current.jsonl
        └── requests-<UTC时间>-<纳秒时间戳>.jsonl
```

日志目录可通过以下任一方式覆盖：

- 启动参数：`-log-dir /path/to/logs`
- 环境变量：`LATTIX_LOG_DIR=/path/to/logs`

启动参数优先于环境变量。目录在运行时以 `0700` 权限自动创建；请求日志文件以 `0600`
权限创建。已存在目录的权限不会被面板主动修改。

### 2.1 与业务数据库的边界

- `operation.db` 是独立 SQLite 文件和独立连接，不使用业务数据库连接池；
- 请求日志直接追加 JSONL 文件，不经过 SQLite；
- 业务数据库只保存日志容量设置：
  - `operation_log_limit`
  - `request_log_max_mb`
- “下载备份”只备份业务 SQLite，不包含 `operation.db` 或请求日志文件；
- 日志没有导入、导出接口。

## 3. 操作日志

### 3.1 数据模型

操作日志保存在 `operation.db` 的 `operation_log` 表：

| 字段 | 说明 |
|---|---|
| `id` | SQLite 自增主键 |
| `event_id` | 随机事件 ID，唯一 |
| `ts` | UTC RFC3339Nano 时间 |
| `severity` | `info` / `warning` / `error` |
| `category` | 固定类别 |
| `action` | 稳定的机器可读动作名，如 `server.updated` |
| `server_id` | 可选的关联服务器 ID |
| `node_id` | 可选的关联节点 ID |
| `detail` | JSON 或文本详情 |
| `operator` | 操作员；系统事件可为空 |
| `ip` | 操作来源 IP |
| `request_id` | 直接触发事件的 HTTP 请求或 WS 消息 ID；后台事件可为空 |
| `trace_id` | 对应完整业务链；无上游请求的后台任务自行生成 |

`runtime_state` 表保存当前进程运行标记，用于识别未正常结束的上一次运行。

### 3.2 类别

操作日志类别固定为：

| 类别 | 内容 |
|---|---|
| `server` | 服务器创建、更新、删除、凭证轮换、配置漂移 |
| `chain` | 链路和节点创建、删除、退化、失败、恢复 |
| `user` | 用户创建、更新、停用、节点分配、删除 |
| `settings` | 面板设置和告警测试 |
| `panel` | 启停、异常退出、重启、更新、备份 |
| `agent` | Agent 在线、离线、连接替换 |
| `command` | 命令成功、失败、死信 |
| `auth` | 登录、登录失败、登出、密码修改 |
| `log` | 日志清空、请求日志丢弃等日志系统自身事件 |

### 3.3 重要程度

- `info`：成功操作、正常状态变化、登录成功、Agent 上线和正常停止；
- `warning`：Agent 离线、连接或链路退化、配置漂移、登录失败、重启请求、
  更新开始、上次异常退出；
- `error`：命令失败或死信、链路失败、更新失败、备份失败、日志写入丢弃。

重要程度由服务端根据动作固定判定，不接受前端自定义。

### 3.4 Agent 连接状态

Agent 连接事件按真实状态跃迁记录：

| 场景 | 动作 | 说明 |
|---|---|---|
| 无连接变为有连接 | `agent.online` | 服务器从离线变为在线 |
| 当前连接实际注销 | `agent.offline` | 服务器从在线变为离线，级别为 `warning` |
| 新连接替换同服务器旧连接 | `agent.reconnected` | 服务器始终在线，不额外伪造离线和上线 |

旧连接被新连接替换后，其退出不会删除新连接，也不会触发 `agent.offline`。

### 3.5 变更详情

- 更新操作只记录实际发生变化的字段；
- 无变化的更新不产生操作日志；
- 一般设置和服务器更新使用 `{before, after}` 结构；
- 删除操作保留必要的对象快照，保证对象删除后仍可理解历史记录；
- 密码、token、私钥、Webhook 签名等凭证不写入日志，只记录“已变更”或是否设置；
- 日志写入发生在业务操作成功之后，写入失败只写进程标准日志，不回滚或误报业务失败。

### 3.6 生命周期

面板启动时写入 `panel.started` 并设置运行标记；如果启动时发现旧标记，先写入
`panel.unclean_shutdown`。正常停止时写入 `panel.stopped` 并清除运行标记。

优雅关停总预算为 5 秒。HTTP 服务最多使用前 4 秒，剩余时间用于写入停止事件并排空请求
日志队列。关停阶段停止请求日志的丢弃回调，优先可靠写入 `panel.stopped`，避免请求日志
排空超时导致下次启动误判为异常退出。

### 3.7 保留、查询与清空

- 默认保留最新 1000 条；
- 设置范围为 100–100000 条；
- 每次写入后删除超出限制的最旧记录；
- 支持按重要程度、类别、服务器、操作员、关键字和时间范围查询；
- 服务端分页，默认每页 10 条，单次最多 200 条；
- 清空操作日志后，保留一条新的 `operation_log.cleared`，其中记录清除条数和操作者。

## 4. 请求日志

### 4.1 JSONL 格式

请求日志一行对应一个被记录的 HTTP 请求或 WS RPC，每行是独立 JSON 对象：

| 字段 | 说明 |
|---|---|
| `timestamp` | UTC 时间 |
| `request_id` | 单次 HTTP 尝试或 WS 消息 ID；HTTP 同时返回 `X-Request-ID` |
| `trace_id` | 完整业务链 ID；HTTP 同时返回 `X-Trace-ID` |
| `severity` | 请求重要程度 |
| `transport` | `http` / `websocket` |
| `method` | HTTP 方法；WS 可为空 |
| `path` | 实际请求路径；订阅 token 会哈希；WS 可为空 |
| `route` | Go `ServeMux` 匹配的路由模板；WS 可为空 |
| `rpc_type` | WS 的 `domain.action`；普通 HTTP 可为空 |
| `attributes` | 路由显式允许的安全属性及脱敏后的 GET query |
| `http_status` | HTTP 状态码；WS 业务消息可为空 |
| `rpc_code` | 管理 HTTP RPC 或 WS response 的稳定业务码 |
| `duration_ms` | 请求处理耗时 |
| `response_bytes` | 响应字节数 |
| `operator` | 已登录操作员 |
| `ip` | 客户端 IP |
| `user_agent` | 截断后的 User-Agent |
| `error_summary` | 协议错误、非成功业务码或 panic 的安全简短错误 |
| `idempotency_replayed` | 是否返回已持久化的首次执行结果 |

请求日志不保存原始请求体、响应体全文、请求头、Cookie、Authorization 或会话内容。
RPC handler 成功解析 body 后，只能写入路由注册时显式声明的安全标量属性，例如
`server_id`、`node_id`、`chain_id`、`user_id`、`version`。不得通过字段名猜测或通用
反射记录 body。JSON 解析失败时不尝试从原始 body 提取属性。

### 4.2 记录范围

记录：

- `/api/*`
- `/sub/*`
- `/api/agent/ws` WebSocket 握手
- 有响应的命令型 WS RPC

默认不记录：

- SPA 静态文件；
- `GET /healthz` 和 `GET /readyz` 的轮询；
- `GET /api/log/list-requests` 和 `GET /api/log/list-operations`；
- `telemetry.report`；
- WS ping/pong 控制帧。

`POST /api/log/clear-requests` 会在清空完成后由请求中间件写入，因此它会成为新的
第一条请求日志。成功的 WebSocket 升级在握手完成时立即记录为 HTTP 101，不等待连接
关闭。Agent 主动事件只在协议错误时写请求日志；真实状态变化写操作日志。

### 4.3 路由日志策略

每个 HTTP RPC 路由或 WS type 在服务端注册时声明固定 `LogPolicy`：

- `full`：记录所有调用，默认策略；
- `failures_only`：只记录协议错误、非成功业务码或慢请求；
- `none`：不写请求日志。

策略由服务端决定，调用方不能通过 header 关闭日志。高频状态查询使用
`failures_only`，健康检查、日志读取、遥测和 ping/pong 使用 `none`。如果普通 GET
改为自动高频轮询，必须同步调整它的 LogPolicy。健康状态不逐次记录；ready 状态变化
分别写 `panel.not_ready` 和 `panel.ready` 操作日志。

### 4.4 请求重要程度

- `info`：协议正常且 RPC code 为 `OK` / `ACCEPTED`；
- `warning`：HTTP 4xx、慢请求，或
  `AUTH_REQUIRED` / `AUTH_INVALID_CREDENTIALS` / `INVALID_ARGUMENT` /
  `NOT_FOUND` / `CONFLICT` / `OPERATION_LOCKED` / `UNSUPPORTED_ACTION`；
- `error`：HTTP 5xx、panic，或
  `INTERNAL_ERROR` / `UPSTREAM_ERROR` / `SERVICE_UNAVAILABLE`。

优先级为 `error > warning > info`。HTTP 200 但 RPC code 为 `INTERNAL_ERROR`
时必须记为 error；成功但超过慢请求阈值时记为 warning。WebSocket 101 始终是 info，
成功的幂等重放不提升级别。

### 4.5 脱敏规则

参数名匹配以下关键字时，值替换为 `[REDACTED]`：

```text
token password secret key cookie authorization cert private
```

其他限制：

- `/sub/{token}` 路径中的 token 替换为 SHA-256 摘要前 4 字节；
- 单个参数、路径、User-Agent 和错误摘要限制为 512 字节；
- attributes JSON 总长度限制为 4096 字节，超出时添加 `_truncated=true`；
- 只在直接连接来自回环地址时信任 `X-Forwarded-For` 的首个地址，防止外部请求伪造来源 IP；
- 日志从结构化 RPC outcome 读取 code 和安全 message，不捕获并重新解析响应 body；
- panic 或非 RPC 内容端点只记录经过截断和脱敏的安全摘要。

### 4.6 异步写入和故障降级

- 请求完成后把日志投递到容量为 1024 的内存队列；
- 单个后台写协程串行追加 JSONL，业务请求不等待磁盘写入；
- 队列已满、日志已关闭或文件写入失败时丢弃该条日志，不影响 HTTP 响应；
- 丢弃数量累计显示在日志页和设置页；
- 写入恢复后，将本轮累计丢弃数量聚合记录为 `request_log.dropped` 操作日志，
  不为每个丢弃请求单独写一条错误；
- 启动时若当前 JSONL 最后一行不完整，截断该行，保证后续每行仍是有效 JSON。

### 4.7 分段与容量

- 默认总容量 10 MiB；
- 设置范围 1–1024 MiB；
- 当前段为 `requests-current.jsonl`；
- 单段目标大小为总容量的 1/10，最小 64 KiB；
- 达到单段目标后，将当前段重命名为带 UTC 时间的归档段并创建新当前段；
- 超过总容量时按时间删除最旧归档段，不删除正在写入的当前段。

日志页面只读取各分段尾部，不扫描完整文件。可选窗口为 10、30、50、100 行，默认 30 行。
筛选只作用于已经读取的当前窗口。

## 5. HTTP API

所有日志 API 都需要管理员会话：

| 方法 | 路由 | 说明 |
|---|---|---|
| `GET` | `/api/log/list-operations` | 查询操作日志，支持过滤和分页 |
| `POST` | `/api/log/clear-operations` | 清空操作日志并保留清空审计记录 |
| `GET` | `/api/log/list-requests?limit=30` | 读取最新请求日志窗口和容量状态 |
| `POST` | `/api/log/clear-requests` | 清空请求日志文件 |

旧 `/event-log` 路由和业务数据库中的 `event_log` 表不再使用，也不提供兼容访问或迁移逻辑。

## 6. 前端行为

日志页包含两个子页面：

- `/logs/operations`：操作日志；
- `/logs/requests`：请求日志。

两个页面分别保存刷新偏好，可选：

```text
不刷新 / 5 / 10 / 15 / 30 / 60 秒
```

默认均为“不刷新”。偏好写入当前浏览器的 `localStorage`，不写入服务器设置。请求日志窗口
大小也按浏览器保存，默认 30 行。

操作日志使用服务端过滤和分页；请求日志使用客户端当前窗口过滤。两个清空按钮分别确认，
互不影响。

设置页显示：

- 操作日志保留条数；
- 请求日志容量上限；
- 请求日志当前占用和累计丢弃数；
- 实际日志目录；
- 业务备份不包含日志的明确提示。

## 7. 非目标

当前设计明确不包含：

- 日志导入或导出；
- 请求体、完整响应体或全部 HTTP 头记录；
- 请求日志全文索引或历史分页；
- 日志上传、远程聚合或多管理员审计；
- `/event-log` 旧路由兼容；
- 旧 `event_log` 数据迁移。

HTTP RPC 的路由、响应信封、Requester、request/trace ID、幂等和 WS 消息契约见
[RPC API、Requester 与 Agent 通道设计](rpc-api-design.md)。

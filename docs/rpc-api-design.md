# RPC API、Requester 与 Agent 通道设计

本文定义 Lattix 自有管理 HTTP API、Frontend Requester、Backend RPC
中间件以及 Panel ↔ Agent WebSocket 协议的最终契约。

当前项目处于全新开发阶段。本次协议为破坏性替换：不保留旧 RESTful
路由、旧响应结构或旧 WebSocket 信封。Panel、Frontend 和 Agent 应同步发布；
业务数据库由 Panel 启动时依据 `PRAGMA user_version` 自动执行事务化结构迁移，
无需随版本重新安装。

## 1. 协议边界

本设计只约束 Lattix 自有协议：

- Frontend → Panel 的管理 HTTP RPC；
- Panel ↔ Agent 的 WebSocket RPC；
- Panel 内部对 Agent 发起请求的 `AgentRequester`。

以下端点或调用遵循各自协议，不套用管理 RPC 的成功响应信封：

- `GET /sub/{token}`；
- `GET /sub/{token}/rules/{version}/{format}/{name}`；
- `GET /api/backup/download` 的成功文件响应；
- `GET /api/agent/ws` 的 HTTP Upgrade；
- SPA 静态资源；
- `GET /healthz` 和 `GET /readyz`；
- GitHub Release、Webhook、Telegram、文件下载等第三方 HTTP。

第三方 HTTP 能力通过窄接口实现：`JSONRequester`、`FileRequester` 和
`WebhookRequester`，具体实现分别为 `ExternalJSONRequester`、
`ExternalFileRequester`、`ExternalWebhookRequester`。这些实现共同依赖：

```go
type HTTPDoer interface {
    Do(*http.Request) (*http.Response, error)
}
```

第三方 client 必须遵循对方的 HTTP 状态码和响应格式，不能套用 Lattix RPC
业务码。

## 2. HTTP 语义

### 2.1 方法与参数

- 明确的只读查询使用 GET；
- 除明确 GET 外，所有动作使用 POST；
- 不使用 PUT、PATCH、DELETE；
- 路由使用 `/api/{单数领域}/{kebab-case 动作}`；
- GET 参数只放 query string；
- POST 参数只放 JSON body；
- 目标 ID 使用 `server_id`、`node_id`、`chain_id`、`user_id` 等明确名称；
- 不使用 `{id}` 资源路径表达业务目标。

HTTP POST body 直接承载业务参数，不再包装请求信封。请求跟踪和幂等元数据
使用 HTTP header：

```http
X-Request-ID: 32 位小写十六进制随机值
X-Trace-ID: 32 位小写十六进制随机值
Idempotency-Key: 调用方生成的幂等键
X-CSRF-Token: 与管理员 session 绑定的 CSRF token
```

### 2.2 HTTP 状态与业务结果

HTTP 状态码只表达 HTTP、路由、协议解析和进程是否成功完成请求：

- 命中 RPC 并完成协议解析后，无论业务成功或失败均返回 HTTP 200；
- 路由不存在和方法不支持使用 404/405；
- JSON 语法损坏或信封结构错误使用 400；
- body 超限使用 413；
- Content-Type 不支持使用 415；
- 登录限流或登录、改密的认证工作负载达到上限时使用 429，并返回 `Retry-After`；
  body 为 `application/problem+json`，协议码为 `HTTP_429`；
- panic 表示 RPC 未能完成，使用 500；
- `/readyz` 未就绪使用 503。

面板进入优雅关停 drain 后，已经命中管理 RPC 的请求仍遵循上述规则：
`HTTP 200 + SERVICE_UNAVAILABLE`；WebSocket Upgrade 和 `/readyz` 使用 HTTP 503。

JSON 合法但字段缺失、值越界或业务约束不满足，返回
`HTTP 200 + INVALID_ARGUMENT`。

### 2.3 统一响应信封

所有管理 JSON RPC 使用：

```json
{
  "code": "OK",
  "message": "",
  "data": {},
  "request_id": "0123456789abcdef0123456789abcdef",
  "trace_id": "0123456789abcdef0123456789abcdef"
}
```

无返回数据时 `data` 为 `null`，不返回 204。异步操作快速返回
`ACCEPTED`，随后通过 GET 查询状态。
`POST /api/setting/update` 在期望设置及新 revision 已提交后返回 `OK`；Agent 的后续
pull/apply 不改变该调用的业务结果。`POST /api/panel/restart` 在同步登记重启意图后返回
`ACCEPTED`，并发请求返回 `OPERATION_LOCKED`。

稳定基础业务码：

```text
OK
ACCEPTED
AUTH_REQUIRED
AUTH_INVALID_CREDENTIALS
INVALID_ARGUMENT
NOT_FOUND
CONFLICT
OPERATION_LOCKED
UNSUPPORTED_ACTION
INTERNAL_ERROR
UPSTREAM_ERROR
SERVICE_UNAVAILABLE
```

只有前端确实需要不同处理分支时才增加领域码，例如
`SERVER_OFFLINE`、`PORT_OUT_OF_RANGE`、`UPDATE_IN_PROGRESS`。
程序只能判断 `code`，不能匹配 `message`。

字段错误使用：

```json
{
  "code": "INVALID_ARGUMENT",
  "message": "请求参数无效",
  "data": {
    "fields": {
      "expires_at": "不能是过去的时间"
    }
  },
  "request_id": "...",
  "trace_id": "..."
}
```

内部 cause 不返回客户端。响应只包含安全消息，完整 cause 写带 request/trace
ID 的进程诊断日志。

## 3. 路由

```text
POST /api/auth/login
POST /api/auth/logout
GET  /api/auth/me

GET  /api/dashboard/get

GET  /api/server/list
GET  /api/server/list-metric-samples?limit=
GET  /api/server/get-metric-history?server_id=&hours=
GET  /api/server/list-commands?server_id=&limit=
GET  /api/server/get-test?server_id=
POST /api/server/run-test
GET  /api/server-test/catalog-status
POST /api/server-test/refresh-catalog
POST /api/server/create
POST /api/server/update
POST /api/server/delete
POST /api/server/rotate-token
POST /api/server/repair
POST /api/server/cleanup-xray
POST /api/server/upgrade-xray
POST /api/server/upgrade-agent
POST /api/server/confirm-renewal
GET  /api/server/list-release-versions?kind=

GET  /api/billing/stats?from=&to=&granularity=&rate_mode=
GET  /api/billing/stats/estimated?from=&to=&granularity=&rate_mode=

GET  /api/provider/list
POST /api/provider/create
POST /api/provider/update
POST /api/provider/delete

GET  /api/exchange-rate/list
POST /api/exchange-rate/refresh
POST /api/exchange-rate/save-custom
POST /api/exchange-rate/delete-custom

GET  /api/node/list
POST /api/node/create
POST /api/node/retry
POST /api/node/delete

GET  /api/chain/list
POST /api/chain/create
POST /api/chain/edit
POST /api/chain/force-publish
POST /api/chain/retry
POST /api/chain/delete
POST /api/chain/set-traffic-multiplier
POST /api/chain/reset-traffic
GET  /api/chain/get-traffic-history?chain_id={id}&hop_id={id|0}&days={1..730}

GET  /api/user/list
POST /api/user/create
POST /api/user/update
POST /api/user/set-nodes
POST /api/user/set-external-subscriptions
POST /api/user/delete
POST /api/user/sub-settings
GET  /api/user/traffic-history?user_id={id}
POST /api/user/regenerate-subscription
POST /api/user/reset-subscription-token
GET  /api/user/subscription-preview?user_id={id}&format={format}

GET  /api/subscription/categories
GET  /api/subscription/templates
POST /api/subscription/template/save
POST /api/subscription/template/clone
POST /api/subscription/template/delete
POST /api/subscription/template/refresh

GET  /api/external-subscription/list
POST /api/external-subscription/create
POST /api/external-subscription/update
POST /api/external-subscription/delete
POST /api/external-subscription/sync
GET  /api/external-subscription/chains?id={id}

GET  /api/setting/get
POST /api/setting/update
POST /api/setting/change-password
POST /api/setting/test-alerts
GET  /api/setting/sub
POST /api/setting/sub

POST /api/panel/restart
GET  /api/panel/state
GET  /api/panel/get-version
POST /api/panel/start-update
GET  /api/panel/get-update-status

GET  /api/backup/download

GET  /api/log/list-operations?severity=&category=&server_id=&operator=&q=&from=&to=&limit=&offset=
POST /api/log/clear-operations
GET  /api/log/list-requests?limit=
POST /api/log/clear-requests
```

端点以 `docs/openapi.yaml` 为权威（`panel/contract_test.go` 强制两者一致），本节仅作速览；query 参数为路由白名单，非全部可选组合。

协议例外：

```text
GET /api/agent/ws
GET /sub/{token}
GET /sub/{token}/rules/{version}/{format}/{name}
GET /api/sub/{token}/info
GET /api/sub/{token}/clients
GET /api/sub/{token}/history
GET /healthz
GET /readyz
```

## 4. 严格解析

管理 RPC 使用严格请求解析：

- POST 必须为 `application/json`；
- 无参数动作发送 `{}`，不发送空 body；
- 默认 body 上限 1 MiB，大载荷动作单独声明上限；
- 拒绝未知字段；
- 拒绝类型不匹配、多个连续 JSON 值和尾随垃圾；
- GET 拒绝未知 query 参数和重复的单值参数。

公开订阅、文件、WebSocket Upgrade、健康检查和静态资源使用自身解析规则，
不套用管理 RPC 严格解析器。

## 5. Requester

### 5.1 Frontend Requester

Frontend `Requester` 与 React UI 状态分离。Requester 只负责：

- 构造 GET、POST 和文件下载请求；
- cookie、CSRF、request/trace ID 和幂等键；
- 超时、取消和仅限 GET 的安全自动重试；
- 严格解析统一响应信封；
- 转换业务错误、传输错误和协议错误；
- 发出请求开始/结束生命周期事件。

React `requestState` 适配层可订阅生命周期事件并维护 pending 计数。loading
只影响显示，不改变请求是否发送、取消、重试或成功。

`AUTH_REQUIRED` 由统一适配层清理登录态。其他业务错误交给调用页面处理。
文件下载使用独立 `download()`，根据 Content-Type 区分文件与业务错误信封。

### 5.2 AgentRequester

Backend 当前含义模糊的 `ws.Requester` 重命名为 `AgentRequester`。它只描述
Panel 通过 WS 向 Agent 发起 RPC 的能力，不与 Frontend Requester 或第三方
HTTP client 共用接口。

## 6. 会话与 CSRF

- 登录成功和 `/api/auth/me` 返回与当前 session 绑定的 CSRF token；
- Frontend Requester 对所有已登录 POST 自动添加 `X-CSRF-Token`；
- 改密、登出或 session 到期后 token 自动失效；
- 登录尚无 session，必须验证同源 Origin/Referer；
- 管理 API 不开放跨域 CORS；
- GET、公开订阅和 Agent token 鉴权的 WS 不使用 CSRF。

## 7. 幂等

- GET 可以按策略自动重试；
- POST 默认不自动重试；
- 创建、升级、修复、重启等副作用动作使用 `Idempotency-Key`；
- Backend 按“管理员 + RPC 路由 + Idempotency-Key”持久化首次响应；
- 同 key 重复请求返回首次业务信封，不重复执行；
- 幂等记录存入业务 SQLite 并定期清理。

`request_id` 表示单次传输，`trace_id` 表示完整业务链，
`Idempotency-Key` 表示业务去重，三者不能混用。

## 8. Request ID 与 Trace ID

- ID 格式为 `[0-9a-f]{32}`；
- Frontend 为每次 HTTP 尝试生成 request ID；
- 首次业务操作生成 trace ID；
- 同一操作重试沿用 trace ID、生成新 request ID；
- Backend 校验调用方 ID，缺失或无效时自行生成；
- Backend 通过响应 header 和信封回传最终采用的 ID；
- HTTP 触发的每条 WS 命令生成独立 request ID并继承 trace ID；
- Agent 响应原样回显 request/trace ID；
- 后台任务没有上游请求时自行创建 trace ID。

## 9. WebSocket RPC

WebSocket 请求：

```json
{
  "kind": "request",
  "type": "node.apply",
  "request_id": "...",
  "trace_id": "...",
  "data": {}
}
```

响应：

```json
{
  "kind": "response",
  "type": "node.apply",
  "request_id": "...",
  "trace_id": "...",
  "code": "OK",
  "message": "",
  "data": {}
}
```

Agent 主动事件：

```json
{
  "kind": "event",
  "type": "telemetry.report",
  "request_id": "...",
  "trace_id": "...",
  "data": {}
}
```

规则：

- `kind` 只能为 `request`、`response`、`event`；
- `type` 使用 `domain.action`；
- response 回显 request 的 type/request/trace ID；
- response 必须包含 code/message/data；
- request 和 event 不带无意义的 code；
- 未知 type 返回 `UNSUPPORTED_ACTION`，不直接断开连接；
- JSON 损坏或缺少关键协议字段才关闭连接；
- 心跳继续使用 WS ping/pong 控制帧。

初始动作包括：

```text
agent.session.open
agent.session.ready
agent.credential.commit
panel.lifecycle.changed
node.apply
node.remove
user.add
user.remove
chain-hop.apply
chain-hop.remove
xray.upgrade
agent.upgrade
agent.uninstall
agent.settings.sync
agent.settings.changed
telemetry.report
config.drift
```

## 10. 健康检查

- 后缀 `z` 是基础设施中常见的约定写法，用来降低与业务路由或资源名冲突的概率，
  本身没有额外协议语义；
- `/healthz` 只报告进程可响应 HTTP；
- `/readyz` 检查 Backend 初始化与业务 SQLite 可用性；进入 drain 后立即返回 503；
- 健康检查不依赖 Agent 在线、GitHub 或其他外部服务；
- 部署、安装和反向代理使用 `/readyz`；
- 成功健康轮询不写请求日志，ready 状态变化写操作日志。

## 11. 请求日志

请求日志、操作日志、WS RPC 记录范围、严重程度、脱敏、容量与路由级
`LogPolicy` 的唯一设计来源是 [日志系统设计](logging-design.md)。本文不复制
日志字段或持久化规则，避免两份契约漂移。

## 12. 契约与验证

- 管理 HTTP RPC 使用 [OpenAPI 3.1 契约](openapi.yaml) 作为协议事实来源；
- Frontend 类型从 OpenAPI 生成，Requester 和语义化 API 方法保持手写；
- Backend 使用明确 Go struct，不生成业务 handler；
- WS 使用独立 [JSON Schema](ws-protocol.schema.json) 和 Go 序列化契约测试；
- CI 校验生成类型无未提交差异；
- Backend、Frontend、Agent、脚本、README 和设计文档必须在同一改动中更新。

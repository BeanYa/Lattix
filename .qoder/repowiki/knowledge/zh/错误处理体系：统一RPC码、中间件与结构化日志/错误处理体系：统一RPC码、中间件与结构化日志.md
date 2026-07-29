---
kind: error_handling
name: 错误处理体系：统一RPC码、中间件与结构化日志
category: error_handling
scope:
    - '**'
source_files:
    - src/shared/messages.go
    - src/backend/internal/logging/http.go
    - src/backend/internal/logging/context.go
    - src/backend/internal/logging/request.go
    - src/backend/internal/logging/operation.go
    - src/agent/cmd/agent/runtime_settings.go
---

## 1. 系统/方法概述
本仓库采用 Go 标准库的错误返回模式（error 返回值）为主，辅以少量 panic/recover 作为不可恢复错误的兜底；通过 HTTP 中间件 + WebSocket RPC 信封中的统一 code/message 字段实现跨进程的错误语义传递；后端提供结构化的请求日志与操作日志子系统，将错误以可查询的 JSONL/SQLite 形式持久化。

## 2. 关键文件与包
- 协议与错误码定义：`src/shared/messages.go`（Envelope、Code* 常量、Validate）
- HTTP 请求中间件与 panic 捕获：`src/backend/internal/logging/http.go`（RequestMiddleware、LogWebSocketUpgrade、requestSeverity）
- 请求上下文元数据传播：`src/backend/internal/logging/context.go`（RequestMeta、SetRPCOutcome、AddSafeAttribute）
- 请求日志写入器：`src/backend/internal/logging/request.go`（RequestLog、Append、Tail、Clear）
- 操作日志存储：`src/backend/internal/logging/operation.go`（OperationStore、Record、List）
- Agent 侧自定义错误与重试策略：`src/agent/cmd/agent/runtime_settings.go`（errAuthenticationRejected、errPanelUnavailable、authenticationRejected、reconnectDelay）
- 前端错误展示：`src/frontend/src/lib/app-dialog.ts`、`src/frontend/src/components/AppDialogProvider.tsx`（对话框式错误提示）

## 3. 架构与约定
- **统一 RPC 结果码**：所有 HTTP/RPC 响应共享 `shared.Code*` 常量（OK、ACCEPTED、AUTH_REQUIRED、INVALID_ARGUMENT、NOT_FOUND、CONFLICT、OPERATION_LOCKED、UNSUPPORTED_ACTION、INTERNAL_ERROR、UPSTREAM_ERROR、SERVICE_UNAVAILABLE、SERVER_OFFLINE、PORT_OUT_OF_RANGE、UPDATE_IN_PROGRESS），由调用方根据 code 决定 UI 行为与重试策略。
- **Envelope 校验**：`Envelope.Validate()` 强制要求 request/event 不包含 code/message，response 必须携带 code+message，从协议层避免错误信息丢失或误用。
- **HTTP 中间件统一处理**：`RequestMiddleware` 在 defer 中 recover panic，若未设置状态码则写 500，并将 panic 摘要写入日志；同时为每个请求注入 RequestID/TraceID，并通过 `SetRPCOutcome` 把业务 code/safeMessage 回传到日志。
- **严重级别判定**：`requestSeverity` 根据 HTTP status、RPC code、耗时以及是否 panic 综合决定 info/warning/error，使错误在日志中具备一致的可观测性。
- **Agent 侧错误分类与重试**：使用 `errors.New` 定义哨兵错误（如 errAuthenticationRejected、errPanelUnavailable），配合 `errors.Is` 判断进行差异化重试（指数退避+抖动、服务不可用固定间隔、认证拒绝直接重置等）。
- **结构化日志双通道**：请求级日志（JSONL 文件，按大小分段、支持 tail/clear/status）与操作级日志（SQLite，支持过滤、分页、清理）共同构成错误回溯能力。
- **前端错误呈现**：通过统一的 AppDialogProvider 将后端返回的 code/message 转换为用户友好的弹窗提示，避免裸错误字符串暴露。

## 4. 开发者应遵循的规则
- **不要 panic 正常错误**：仅对“不可能发生”的致命异常（如 crypto/rand 失败、嵌入资源加载失败）使用 panic，并由中间件 recover 兜底。
- **始终返回 error**：函数失败必须返回 error，禁止忽略；上层通过 fmt.Errorf("%w", err) 包装并保留错误链。
- **使用 shared.Code* 表达业务错误**：RPC handler 调用 `logging.SetRPCOutcome(code, safeMessage)` 设置结果码与安全消息，不得直接拼接用户输入到错误信息。
- **敏感信息脱敏**：通过 `AddSafeAttribute` 添加白名单属性，参数名匹配 token/password/secret/key 等会被 `[REDACTED]` 替换。
- **区分可重试与不可重试错误**：Agent 侧对认证拒绝、面板不可用、网络超时等使用专用哨兵错误与延迟策略，避免无限重试。
- **日志记录最小化**：仅在 `/api/` 和 `/sub/` 路径下记录请求日志，高频 event 由调用方在进入 LogWebSocketRPC 前按策略过滤。
- **前端不直接显示原始错误**：统一通过 app-dialog 组件展示，确保用户体验一致且无敏感信息泄露。
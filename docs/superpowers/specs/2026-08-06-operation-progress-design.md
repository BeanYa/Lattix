# 旁路式操作进度系统（AOP 切面 + 进度弹窗）设计

日期：2026-08-06

## 背景

面板中存在一批"长链路"操作（校验 → 写库 → 端点 reconcile → 订阅重生成）与长 IO 操作（外部订阅/模板网络同步、服务器 rebuild/repair 下发）。用户在界面上点击后：

- 分组类操作：写库与 reconcile 同步返回，但**订阅重生成在后台 regenerator 队列异步执行**（150ms 防抖、逐用户发布），前端不知道这步还在进行，也不知道进行到哪；
- 外部订阅同步：网络 IO 可能持续 10s+，界面只有"转圈"；
- 服务器 rebuild/repair：下发命令后等待 agent 回执，无中间状态。

已有的进度机制（面板更新 `UpdateOverlay`、服务器测试、xray/agent 升级回执）保持现状，不纳入新系统。

目标：为上述长操作附加一个**旁路式观察系统**——操作执行过程中弹出进度弹窗（复用面板更新弹窗的视觉：阶段图标列表 + 进度条 + 消息），提示用户操作进行到哪一步、完成了多少、有无部分失败。

## 硬性约束（最高优先级）

1. **不修改原操作的任何逻辑**：校验/写库/队列/错误处理/响应结构全部保持原样。进度系统是纯旁路（observer），只在原流程的自然节点处插入报告调用（Go 语境下的 AOP：join point 插入 advice，不动核心逻辑）。
2. **观察进程崩溃不影响原操作**：若操作 A 含 5 个子动作，观察函数 B 在 A 之前注册启动、A 全部完成后 B 关闭；过程中 B 因任何原因（参数错误、panic 等）崩溃，A 必须继续完整执行。进度报告的任何失败都不得改变业务 handler 的控制流、返回值或错误语义。
3. **观察记录不是可操作的任务**：它不可取消、不可重试、不可管理，只是操作的旁路进度快照。对外命名一律用 **observe** 语义（`observe_id`、`/api/observe-task/get`），不暴露 task 概念。

## 非目标

- 面板更新、服务器测试、xray/agent 升级**不**迁移到新观察系统（已有自己的进度/回执机制）。
- 不做观察取消（cancel）、重试、持久化（进程重启后观察记录丢失，弹窗自然消失）。
- 不做页面刷新后的弹窗恢复。
- 不引入 SSE/WebSocket 推送，统一采用轮询。
- 不修改现有同步/异步边界：校验+写库保持同步（内联报错），级联扇出（reconcile、订阅重生成）保持既有异步路径，观察只做旁路记录。

## 方案

### 总览

```
原逻辑（不变）：  校验 → 写库 → reconcile 端点 → EnqueueUsers → 后台 regenerator 逐用户发布
旁路切面（附加）： |——> Start │     │               │                 │
                  │ report① │ report② │ report③      │ WatchUsers(观察) │
                  │         │         │               │ 回调: 逐用户 x/n   │
                  │                writeJSON 附加 observe_id（envelope 层）  │
前端：调用返回 {data, observeId} → 弹窗轮询 GET /api/observe-task/get?observe_id=… → 阶段/进度/终态
```

### 后端：新增 `internal/progress` 包（观察注册表）

纯内存旁路注册表，不落库。内部实现名可用 `Observation`（对外契约名 observe）。

```go
type Status string // running | done | failed

type Observation struct {
    ID         string    `json:"id"`
    Kind       string    `json:"kind"`   // user_group.update 等
    Title      string    `json:"title"`  // "更新用户分组"
    Stages     []Stage   `json:"stages"` // [{Key:"db", Label:"校验并写入数据库"}, ...]
    Stage      string    `json:"stage"`
    Percent    int       `json:"percent"`
    Message    string    `json:"message"`
    Status     Status    `json:"status"`
    Warnings   []string  `json:"warnings,omitempty"`
    Error      string    `json:"error,omitempty"`
    StartedAt  time.Time `json:"started_at"`
    FinishedAt *time.Time `json:"finished_at,omitempty"`
}
```

- `Registry`：`sync.Mutex` + `map[string]*Observation`；`Start(ctx, kind, title, stages) *Observation`；`Get(id)`；`ListActive()`（预留，YAGNI 可先不做）。
- 报告 API（均对不存在的观察 ID 静默忽略）：
  - `o.Report(stageKey, percent, message)`：推进阶段与百分比；阶段 key 不在 Stages 中时仅更新 percent/message（宽容）。
  - `o.Warn(msg)`：追加警告，完成时随观察记录一起呈现。
  - `o.Finish()` / `o.Fail(err)`：落终态并记录 FinishedAt。
  - `o.Close()`：defer 收尾专用——**约定：观察创建后，handler 正常返回路径由 defer Close 落 done（含已累积警告）；业务失败路径 handler 在写错误响应前显式调用 `o.Fail(err)`**；业务 panic 时 defer Close 仍执行（落 done，观察不悬挂；业务错误响应仍由 http 层照常返回）。
- **崩溃隔离（硬性约束 2 的实现）**：
  - 所有报告方法内部 `defer recover()`：panic 仅 `log.Printf("progress: recover: %v")`，绝不上抛。
  - `Start` 失败（容量满等）返回 `nil`，调用方句柄为 nil 时一切报告为 no-op（nil 安全方法集，调用方无需判空）。
  - 观察状态按 ID 独立存储：一条观察损坏/panic 不影响其他观察与业务。
  - 注册表容量上限（如同时 64 条 running，超出拒绝新建、不影响业务）。
  - TTL：终态观察保留 5 分钟（供轮询收尾/其他页面查询），超时惰性清理。
- **观察者机制（订阅重生成进度）**：`o.WatchUsers(userIDs []int64)`——把观察登记为"等待这批用户发布完成"。regenerator 每发布完一个用户（成功或失败）调用注册表 `NotifyUserPublished(userID, err)`：回调所有 watch 该用户的活跃观察，推进 `Stage="regenerate"` 的 percent（x/n），失败用户计入该观察的 Warnings。注册表内 recover 包裹；无活跃观察时是原子读 + 空 map 返回，零成本路径。**注入**：`progress.Registry` 实例由 panel 构造并注入 sub.Server（regenerator 与 `WatchUsers` 所在的 triggerGroupChange 同源），sub 包只依赖 `progress.Registry` 接口。

### 观察挂点（只插入报告调用，不碰业务行）

| 操作 | 阶段序列 | 报告节点 |
|---|---|---|
| 链路/用户分组 增/删/改 | 校验并写入数据库 → 同步共享端点 → 重新生成订阅文件 | 校验通过后 `Start`；写库后 report①100%；端点 reconcile 循环内逐端点 report②；`EnqueueUsers` 后 `WatchUsers` + report③ 0% |
| 链路 创建/编辑/删除/重试/强制发布 | 校验并写入数据库 → 下发节点配置 → 重新生成订阅文件 | 同分组模式；report② 挂在发布/reconcile 循环（若该操作内无同步下发循环，则该阶段直接 100%） |
| 节点 创建/删除/重试 | 校验并写入数据库 → 下发节点配置 → 重新生成订阅文件 | 同上 |
| 服务器 rebuild/repair/cleanup | 写入数据库 → 下发命令 → 等待 agent 回执 | 命令 Enqueue 后 report 对应阶段；回执等待按现状（命令日志/轮询）不额外跟踪，观察在 handler 结束时 Close |
| 外部订阅 创建/更新/同步 | 拉取远程订阅 → 解析节点 → 写入数据库 → 重发布关联用户 | handler 内同步流程节点处 Report；网络拉取为慢步骤 |
| 订阅模板刷新 | 拉取远程模板 → 解析 → 写入数据库 | 同上 |

- 校验失败**不创建观察**（前端保持内联报错，无弹窗）。
- 观察的生命周期绑定 handler：`o := progress.Start(...)`，`defer o.Close()`；reconcile/发布循环与 enqueue 之间插入 Report。业务错误路径不变（`writeError` 照旧），观察由 defer Close 落终态；若业务失败发生在创建观察之前则无观察。
- 同步型 handler（外部订阅同步等）：观察在 handler 内同步跑完，弹窗轮询可能立即看到终态（显示完整步骤勾选），仍提供"这操作做了哪几步、是否成功"的信息。

### 响应附加 observe_id（向后兼容）

- RPC envelope 增加可选字段 `observe_id`（`writeJSON` 统一注入：从 request context 读取 handler 内 `progress.Attach(ctx, observeID)` 绑定的值）。旧字段顺序不变，前端旧解析器忽略未知字段。
- `Requester` 新增 `postObserved<T>(path, body, options): Promise<{ data: T; observeId?: string }>`：内部解析 envelope 的 `observe_id`；现有 `post` 不变。api.ts 中**仅长操作方法**改用 `postObserved`。
- 无 observe_id（未创建观察/观察已因崩溃消失）时返回 `{ data, observeId: undefined }`，调用点自然跳过弹窗——旁路语义延伸至前端。
- `GET /api/observe-task/get?observe_id=…` 直接返回 `Observation` JSON（终态保留 5 分钟内可查）；404 = 观察不存在或已清理。

### 前端

- 新 `OperationProgressProvider`（挂 Layout，UpdateOverlay 同层）：context 提供 `showOperation(opts: { observeId: string })`（title/stages 全部来自后端观察，前端不硬编码）。弹窗组件 `OperationProgress`：
  - 轮询 `GET /api/observe-task/get?observe_id=…`，400ms 间隔；组件卸载/关闭时停止。
  - 视觉复用 UpdateOverlay：标题 + 图标（loader/check/x）、阶段列表（✓ / spinner / ○ 图标）、进度条 + 百分比 + message、警告明细列表（可展开）、失败 error 文案。
  - 终态行为：`done` 无警告 → 1s 后自动关闭；`done` 有警告 → 展示警告明细 + 关闭按钮；`failed` → 错误信息 + 关闭按钮。
  - 轮询 404（面板重启等）→ 显示"进度已丢失，操作可能仍在后台继续" + 关闭按钮。
  - 同时只允许一个活动弹窗；已有弹窗时替换。
- 调用点改造（每处 2-3 行）：`const { data, observeId } = await api.userGroupUpdate(...)` → `observeId && showOperation({ observeId })`。失败路径保持现状（内联错误提示），不因观察失败重复弹错。
- 涉及页面：Groups、Chains、Nodes、Servers、ExternalSubscriptions、SubscriptionTemplates。

### 边界情况

| 场景 | 处理 |
|---|---|
| 观察在响应返回前已完成 | 弹窗打开立即显示终态，自动关闭 |
| 面板更新/重启中 | UpdateOverlay 全屏遮罩优先；轮询 404 → 显示丢失提示 + 关闭 |
| 页面刷新 | 不恢复弹窗；观察 TTL 5 分钟 |
| 同一操作并发触发 | 观察互相独立；观察者按 ID 隔离；写库并发由现有锁/幂等保护，不改动 |
| 校验失败 | 不创建观察、无 observe_id，内联报错照旧 |
| 观察进程 panic/参数错误 | recover 隔离，业务照常（硬性约束 2） |
| 注册表容量满 | Start 返回 nil，无弹窗，业务照常 |
| 单用户发布失败 | 计入观察 Warnings，整体 done 带警告；不阻断其余用户 |
| 端点 reconcile 失败 | 现状记日志；观察 Warnings 汇总 |

## 测试

- `progress` 包单测：状态流转（report/警告/终态/TTL）、观察者回调（WatchUsers + NotifyUserPublished 推进 x/n、失败计警告）、**崩溃隔离**（报告方法内触发 panic 不影响调用方返回值、Start 返回 nil 后全部报告 no-op、业务 handler panic 时 defer Close 仍落终态）、并发安全（race）。
- handler 测试增补（groups_test.go 等）：响应 envelope 带 observe_id；观察最终状态与警告；校验失败无观察。
- regenerator 观察回调测试：发布循环中 NotifyUserPublished 正确推进登记观察，无观察时零影响。
- 前端：`postObserved` envelope 解析测试（有/无 observe_id）；`OperationProgress` 组件测试（轮询渲染、三终态、404 提示、自动关闭）。
- e2e（可选增补）：groups.sh 中分组保存后断言观察状态端点返回 done 带阶段列表。

## 文件改动清单（预估）

- 新增：`src/backend/internal/progress/registry.go`、`registry_test.go`；`src/frontend/src/lib/operation-progress.ts(x)`、`src/frontend/src/components/OperationProgress.tsx`
- 修改：`src/backend/internal/panel/{groups,chains,nodes,servers,external_subscriptions,panel}.go`（挂点 + envelope observe_id 注入 + `GET /api/observe-task/get`）、`src/backend/internal/sub/regenerate.go`（NotifyUserPublished 一行旁路）、`src/frontend/src/lib/{api.ts,requester.ts,api-contract.generated.ts}`、`src/frontend/src/components/Layout.tsx`（Provider）、六个页面调用点

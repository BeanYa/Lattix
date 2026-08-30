# 第二期修复计划：设计缺陷 / 坏味道 / 流程遗留（2026-08-23）

来源：2026-08-20 全仓库 code review 的设计轴、规范轴坏味道、流程轴遗留项（F1/F2/F4），以及第一期修复过程中各任务审查登记的 deferred minors。第一期（安全轴+B0/P2+流程 7 项）已完成于同一分支。

工作基础：worktree `/home/bean/workspace/Lattix-codex/.worktree/security-fixes`，分支 `fix/security-review`（HEAD 含第一期 23 个 commit）。

## Global Constraints

- 遵守 AGENTS.md：复用现有基础设施（requester、nettrust、store、状态机）；除 Task 16 明确允许前端加 prettier（devDependency）外，不引入新第三方依赖。
- 本期的"重构"以消除具体审查发现为边界：只改该发现要求的范围，不顺手扩大；匹配周边代码风格与注释语言。
- 保留每个被编辑文件的现有行尾（仓库混用 CRLF/LF，diff 不得全文件重写；Task 16 的全量格式化是任务本身，独立 commit）。
- 行为兼容优先：外部可观测行为（API 契约、WS 协议、命令行、文件格式）不变，除非任务明确说明。若某修复必须以破坏兼容为代价，停止并在报告中说明，由控制者裁决。
- 每个任务完成后运行受影响 module 测试并全绿：`cd src/agent && go test ./...`（预存在的 internal/xray 端口绑定环境性失败 TestRebuildXrayNoPreviousConfig 豁免）、`cd src/backend && go test ./...`、`cd src/shared && go test ./...`、前端 `bun run lint && bun run test`（worktree 的 src/frontend 已 bun install）。
- commit message 遵循仓库惯例：`refactor(scope): 中文简述` / `fix(scope): ...` / `ci(...)` 等。
- 重构任务必须保持测试语义不变（测试可随接口调整，但不断言放松）。

## Task 1: D3 agent 连接状态机绕过收拢

背景（设计轴 D3 / 规范轴 S2）：`src/agent/cmd/agent/main.go:340-345` 在 run() 内绕过外层带转换校验的 `saveConnectionStatus` 闭包（main.go:104，经 state.ValidConnectionTransition 校验，state/state.go:53）直接落盘 `ConnStateOnline`，外层再靠裸赋值 `currentConnState = state.ConnStateOnline`（:136）补偿；`st.AuthRejected = true`（:147）也在锁外直写。AGENTS.md 要求状态流转优先复用状态机。

修复：online 写入收进同一校验守卫（saveConnectionStatus），消除外层补偿赋值；AuthRejected 直写纳入 st 的锁保护（或 state 包提供的 setter）。保证连接状态全部经 ValidConnectionTransition 校验。通读 main.go 连接状态流转全路径，确认无其他绕过点。

测试：agent module 全绿；如有连接状态机单测，补 online 转换经守卫的用例。

## Task 2: D4 xray 热操作→重启→回滚段提取

背景（D4）：`src/agent/internal/xray/manager.go` 的 ApplyNode（:135）、RemoveNode（:165）、mutateUser（:226）逐字重复同一段"热操作→失败则重启兜底→回滚"逻辑。

修复：提取 `withRestartFallback`（或贴合现有命名的等价物），三处改调它。语义逐字节不变（含日志与回滚顺序）。

测试：`cd src/agent && go test ./internal/xray/` 全绿（豁免项见全局约束）。

## Task 3: D5 servertest 任务日志状态类型化 + 转换表

背景（D5）：`src/agent/internal/servertest/manager.go:24` 的 taskJournal.State 用裸字符串 `"running"|"result_pending"|"completed"`，比较散落九处（:60/:70/:91/:117/:203/:271/:291/:382/:408），与项目转换表模式（lifecycle/hub/chain FSM）不一致，违反 AGENTS.md 状态机约定。同病：`src/agent/cmd/agent/command_queue.go:18` 的 State。

修复：两处状态改类型化常量 + 转换校验（规模小，可用轻量转换表或合法性函数，风格对齐 shared/server_testing.go:61 的 ServerTestTaskStatus 先例）。非法转换报错或 panic（对齐项目现有 FSM 的做法）。

测试：servertest 与 cmd/agent 相关测试全绿；新增非法转换被拒用例。

## Task 4: agent 坏味道清扫

背景（规范轴 agent 报告 B2/B3/B4/B6）：
- B2 settings 同步机制整体复制两份：`src/shared/server_settings.go:30`（注释自承"照抄 AgentSettingsSyncPayload"）、`src/agent/cmd/agent/server_runtime_settings.go:15`（"照抄 runtimeSettings 模式"）、main.go:883-981 的 send/handle 函数对与 main.go:447-480 的两个周期 pull goroutine 近乎逐行相同。
- B3 原子落盘样板（序列化→.tmp→os.Rename）重复约 11 处：state.go:146/171/207/243、command_queue.go:200、servertest/manager.go:258/449、selfupdate.go:293、xray/upgrade.go:214、ipquality/script.go:100、ookla.go:159；`copyFile` 在 selfupdate.go:287 与 upgrade.go:208 逐字重复；`resolveLatest` 与 `resolveLatestXrayVersion` 重复。
- B4 `main.go:212` run() 与 `main.go:638` handle() 各 14 个参数，同一组依赖成群穿越。
- B6 main.go:450/:467 用 time.Now().UnixNano()%25 做抖动，与 main.go:327 的 math/rand 用法不一致。

修复：
- 在 src/agent/internal/state（或合适的共享位置）提取原子落盘 helper，11 处全部改调；copyFile 合一；resolveLatest 合一。
- B2 的 payload 结构复用（shared/server_settings.go 与 agent settings payload 合一处定义）；两个周期 pull goroutine 提取公共函数；send/handle 对合并。注意保持 § 文档引用注释。
- B4：run()/handle() 的参数组 bundling 为会话上下文 struct（命名贴合项目风格），调用点同步。
- B6：统一用 math/rand 抖动。
- 每小项可独立 commit。telemetary/servertest 等既有测试不得放松断言。

测试：agent module 全绿（豁免项见全局约束）。

## Task 5: S1b speedtest 复用 requester

背景（规范轴 S1/A1）：`src/agent/internal/servertest/speed.go:170-240`（speedHTTPClient/speedDownload/speedUpload）自建 http.Client/Transport。AGENTS.md 规定所有请求相关代码复用 requester。speedtest 的特殊需求：按地址族拨号（tcp4/tcp6）、PUT 流式上传计量、下载吞吐计量、Apple networkQuality 头，现有 requester 接口不覆盖。

修复：扩展 src/shared/requester（而非另起实现）：提供按地址族拨号的传输构造能力（如 `NewDialFuncHTTPClient(network string, ...)` 或导出可组合的 Transport 工厂），speedtest 改用它构造 client；计量逻辑（countingReader 等）留在 servertest。风格与 requester 现有外部请求器一致。speedtest 的自定义 TLS/超时配置保留。

测试：shared 与 agent module 全绿；requester 新增能力带单测。

## Task 6: S1a releases/exchange 复用 requester

背景（规范轴 S1）：`src/backend/internal/panel/releases.go:47,62,139-159` 与 `exchange.go:19,28,50-71` 自建 http.Client 手写 GET/状态码判断/JSON 解码，语义等价于 `requester.ExternalJSONRequester` 却绕过统一错误包装与 URL 脱敏。

修复：两处改经 requester.ExternalJSONRequester（或现有最贴合的 requester 变体），删除手写 HTTP 代码；错误语义与脱敏随 requester 统一。注意保留各自业务特有的状态码处理（如有 404→空目录语义等），需要时用 GetWithOptions 或合理扩展，但优先复用现有接口。

测试：backend module 全绿；相关包测试更新。

## Task 7: dispatch 坏味道清扫

背景（规范轴 backend/store+dispatch 报告 1/2/5/7）：
- `src/backend/internal/dispatch/dispatcher.go`：CleanupXraySync（:200-237）与 RebuildXraySync（:242-279）逐行平行 + 各自 waiter register/unregister/deliver 三件套（:281-344）；Enqueue/enqueueRevisionTask/UninstallWithRetry 的 marshal+requestID+traceID 开头模板重复 5 处。
- handleCommandResponse 成功分支（:1107-1168）与失败分支（:1183-1240）对同一组命令类型做平行 if 级联；类型分派还在 Flush 死信处理（:442-469）与 commandEndpointID（:1286-1300）重复（新增命令类型须同步改 4 处）。
- handleAgentSettingsSync（:806-848）与 handleServerSettingsSync（:852-888）平行结构。
- Middle Man：InvalidateChainForServerDeletion（:724-726）等纯转委托。

修复：提取通用"同步命令+回执等待"机制合并 Cleanup/Rebuild；命令类型的响应处理收敛为共享 handler 表/map（成功/失败/死信/endpoint 归属一处注册）；settings sync 两个 handler 合并为参数化实现；纯转委托函数消除（调用点直调被委托方）。注意 dispatcher.go 是并发密集文件，重构不得改变锁粒度与等待语义。

测试：backend module 全绿（dispatch 包测试不得放松断言）。

## Task 8: store/panel 坏味道清扫（不含订阅格式平行列）

背景（规范轴报告）：
- `src/backend/internal/store`：UpdateChainRevision 与 SetChainRevisionStatus 的 CAS 拼装近乎相同（revisions.go:505-556）；hop 合并块在 publishDesiredRevision（chain.go:394-405）与 ForcePublishRevision（:528-543）重复，发布后端点 reconcile 块（:427-441 vs :567-578）重复；CreateServer/createServer 连续 8 个 string 参数（servers.go:115-119）；SetUserSubSettings 四件套参数（users.go:230）。
- `src/backend/internal/panel`：handleAssign/UnassignSubscriptionTemplate 逐行同构（template_assignment.go:122-263）；dedupeUserIDs 与 dedupeInt64s 两份拷贝（template_assignment.go:49-59、groups.go:467-477）；panel.go:198-203 与 238-243 env duration 解析块同构；同名不同义的 settingInt（cmd/backend/main.go:712 与 settings.go:753）；连接/同步状态裸字符串字面量（servers.go:110-115，项目已有 store.BillingAssumedValid 常量先例）。
- 明确不做：订阅格式平行列的 DB schema 归并（store.go:285-298 等，改动面与风险过大，记录为已知问题）；Command.CreatedAt string→time 若波及 JSON 兼容则放弃并在报告说明。

修复：逐项提取/bundling/重命名/常量收口（裸字符串状态在 store 或 panel 定义常量）。每项保持行为不变。

测试：backend module 全绿。

## Task 9: 死代码清理（sub 包）

背景（规范轴 backend 其余包报告 2）：`src/backend/internal/sub/` 的 serveClash/serveLinks/serveSingbox/serveQuanX（sub.go:539,567、singbox.go:65、quanx.go:14）自订阅改为发布快照后无任何调用方，仅测试触碰其 helper（约 150 行）。

修复：先用 grep 全仓确认无调用方（含前端/脚本/e2e 对实时渲染端点的引用），再删除四个函数与其独占 helper；若 helper 被测试使用且测试因此失去意义，一并删测试；测试若实际覆盖仍在用的共享逻辑，改测共享入口。删除后在报告列出删除清单与每处的"无调用方"证据。

测试：backend module 全绿。

## Task 10: D9 回执 context + D6 PublishUser 分片锁

背景：
- D9：`src/backend/internal/dispatch/dispatcher.go:1074` 及 main.go 各回执处理回调用 context.Background()，关停取消不传播。
- D6：`src/backend/internal/sub/publisher.go:33` 单把全局 publish.Lock 串行所有用户发布，节点 ACK 波及大量用户时成瓶颈。

修复：
- D9：回执处理改用可随关停取消的 context（从 Server/Dispatcher 生命周期派生），main.go 各回调同样接入；不改变处理逻辑。
- D6：全局互斥改按用户分片锁（如 32/64 桶条纹锁）或有界并发池；保证同一用户发布仍互斥（防并发写同一用户快照）。通读发布路径确认无其他依赖全局串行的隐含假设。

测试：backend module 全绿；dispatch/sub 相关并发测试 -race 过。

## Task 11: D7 业务命令门控从 ws 上移到 dispatch

背景（设计轴 D7）：`src/backend/internal/ws/hub.go:198-233` 在 Send 里查生命周期并用硬编码 isBusinessCommand 白名单拦截——业务门控泄漏进传输层，新增命令须改 ws 包。ws 包设计上不依赖业务包（靠 Authenticator/AgentRequester 接口反向注入）。

修复：门控上移到 dispatch 层（所有 Hub.Send 的业务命令调用点上游收敛一处判定）；hub.go 删除 isBusinessCommand 与生命周期查询（若 Lifecycle 字段仅服务于门控则一并移除接线）。注意保持 Task 2（第一期）补全的门控语义逐字节一致：startup/faulted 状态下同一组业务命令返回同等错误（ErrPanelNotActive 或 dispatch 层等价错误），非业务命令（握手/回执/上报）不受影响。错误传播到调用方的语义变化要在报告说明。

测试：backend module 全绿；门控测试从 ws 包随迁到 dispatch 层并更新。

## Task 12: D8 ws 读循环解耦（每连接 handler goroutine）

背景（设计轴 D8）：`src/backend/internal/ws/agent.go:236` 读循环里同步调 h.OnMessage → dispatch.HandleMessage → handleCommandResponse 内多次 SQLite 写；DB 慢会阻塞该 agent 后续全部消息（含 ping/pong 续期之外的协议消息）。

修复：每连接加有界缓冲 channel + 专属 handler goroutine 顺序消费（保持消息顺序语义）；读循环只负责读取、续期、投递 channel。channel 满或连接关闭时的处理对齐 hub 现有慢连接策略（断开并让 agent 重连补发）；关停时 goroutine 正确退出（无泄漏）。pong/lifecycleAck 的快速路径保持在读循环（现状已如此的不动）。

测试：backend module 全绿；ws 包并发测试 -race 过；新增"慢 handler 不阻塞读循环"用例。

## Task 13: D2 Dispatcher 不可变 Options + Events 接口收敛

背景（设计轴 D2）：`src/backend/internal/dispatch/dispatcher.go:30-47` 约 12 个导出字段（DestCandidates/PanelVersion/PanelPublicURL/AgentReleaseBase/PanelLifecycle/OnNodePublished/OnChainPublished/OnEndpointPublished/OnOnlineUsers/Alerter/OperationLog/RequestLog）由 panel.New/main 构造后写入，运行期并发读取，无同步保护。

修复：配置类收敛为不可变 `Options` struct 随 New 注入；事件回调收敛为单个 `Events` interface（或 struct of funcs，择与项目风格贴合者）注入。panel.New/main 接线同步更新。运行期不再有导出可变字段。若个别字段确需运行期可改，用 atomic/锁保护并在报告说明理由。

测试：backend module 全绿。

## Task 14: D1 panel 组件级拆包（第一期：自包含组件）

背景（设计轴 D1）：`src/backend/internal/panel` 76 文件/1.8 万行/97 handler 平铺单包，Server 结构体聚合 25+ 依赖。完整领域拆分是大工程，本期先做审查建议的第一步：把自包含组件拆为独立包。

修复：评估并拆出以下自包含组件（按实际耦合度，能拆几个拆几个，报告说明每个拆/不拆的理由）：taskScheduler（scheduler.go）、release catalog（releases.go）、exchange catalog（exchange.go）、cdn catalog 接线、panelUpdater（update.go）。目标包如 `internal/panel/scheduler` 等；handler 留在 panel 包做 HTTP 适配。拆出的组件不得反向依赖 panel 包（依赖方向：组件包 ← panel）。Server 结构体字段随之减少。

测试：backend module 全绿。

## Task 15: 前端清扫

背景（规范轴前端报告 2/3/6/7）：
- 5 秒轮询循环逐字复制 5 处：Servers.tsx:331、Users.tsx:247、Chains.tsx:596、dashboard/DashboardHig.tsx:90、themes/cream/DashboardCream.tsx:89（AbortController + stopped + setTimeout(poll, 5000)）。
- humanizeBytes 重复定义（Subscription.tsx:169 vs lib/format.ts:2）；formatBytes 在 Settings.tsx:150 与 RequestLogs.tsx:43 近乎重复；CURRENCIES 在 Servers.tsx:44 与 Settings.tsx:108 重复。
- 以展示文本作逻辑键：Chains.tsx:91 ChainStateMark 用中文 label includes 决定 loading 图标。
- 状态→展示映射散落：Chains.tsx:69-88、RequestLogs.tsx:50、ServerTestPanel.tsx:89、LowPolyEarth.tsx:183-185。

修复：提取 `usePolling` hook（lib 或 hooks 目录，风格对齐现有 lib/）替换 5 处；工具函数归并到 lib/format.ts 等既有位置；ChainStateMark 改判 status 枚举；状态映射集中到共享模块（如 lib/status.ts）。行为与渲染结果不变。

测试：`bun run lint && bun run test` 全绿；`bun run build` 通过。

## Task 16: 前端引入 prettier + 一次性全量格式化

背景：仓库前端无 formatter（oxlint 不查格式），tabs/spaces 混用散落多文件。本任务允许新增 devDependency prettier。

修复：
- 加 prettier devDependency + 项目配置（printWidth/semi 等贴近现有主流风格，先看几个文件确认主流是 2 空格缩进、有分号等再定）+ package.json `format`/`format:check` 脚本。
- 一个独立 commit 只做 `prettier --write` 全量格式化（commit message 注明 mechanical）。
- CI（.github/workflows/ci.yml 前端 job）加 format:check 步骤。
- .gitignore/blame 考量：如仓库有 .git-blame-ignore-revs 惯例则加入该 commit，没有则不新建。

测试：format:check、lint、test、build 全绿。

## Task 17: 前端巨型组件拆分

背景（规范轴前端报告 1 Divergent Change）：pages/Settings.tsx（1618 行/74 useState）、Servers.tsx（1500 行/67）、Chains.tsx（1726 行/44）、Users.tsx（1141 行/52）。

修复：每页拆分为分区子组件/自定义 hooks（如 Settings 按 Agent/运行/安全/系统维护分区；Chains 抽创建/编辑表单对话框与表单状态 hook）。约束：纯结构重组，渲染输出与交互行为不变；表单状态泥团（Chains 约 30 个扁平 string state、Users 类似）bundling 为表单状态对象或 useReducer 随拆分一并做；每个页面独立 commit。不追求一步到位拆完——按收益/风险，每页至少把最大的 1-2 个自包含区块（对话框/表单）拆出，报告说明拆分边界与剩余。

测试：`bun run lint && bun run test && bun run build` 全绿；vitest 相关用例更新。

## Task 18: 安全跟进小项集合

来源：第一期各任务审查登记的 deferred minors。
- sessionSecret 进程内缓存（src/backend/internal/panel/auth.go：每次认证请求 2 次 GetSetting SQLite 读；加 atomic 缓存，rotate/首次生成时刷新，注意与 Task 8 的轮换逻辑一致）。
- alert webhook 与 sub 模板拉取接入 SSRF 防护（src/backend/internal/alert/alert.go:61、src/backend/internal/sub/sub.go:83-84：复用 Task 7 的 requester.GuardedDialContext/NewExternalHTTPClient；webhook 是管理员配置 URL，接入防护；注意 alert 可能有自签名 https 需求，检查现有 client 构造）。
- install-agent.sh DEV 分支（约 :640）nohup 传 -token "$BOOTSTRAP_TOKEN" 改 env 注入（与 Task 6 同法）。
- 测试加固：Task 12 的 install_insecure 补 HTTP 响应级断言；Task 2 门控补 active 状态可投递断言。
- M3 的 429 响应补 Retry-After 头（取窗口滑动到可用的秒数）。

测试：backend/agent module 全绿。

## Task 19: P1 schema↔Go 契约 sanity 测试

背景：docs/ws-protocol.schema.json 无任何强制校验（第一期已对齐两处漂移并标注"参考性描述"）。完整 JSON Schema 校验需引入依赖，不做；改做针对性契约测试。

修复：在 src/shared（messages_test.go 或新文件）加测试：对 schema 中有 data 约束的关键消息类型（node.apply、user.add、chain-hop.apply、shared-endpoint.apply 等），断言 Go 序列化产物满足 schema 的 required/nullable 约定（手工针对性断言，如 ApplySharedEndpointPayload{} 序列化后 clients/routes 为 null 且 schema 允许 null；chain-hop.apply 的 revision_id 运行期必赋值——可断言 dispatch 构造点或 payload 文档注释约定）。目的：schema 与 Go 再次漂移时测试变红。范围限"schema 已描述的关键类型"，不求全覆盖。

测试：shared module 全绿。

## Task 20: F4 release notes 取自 CHANGELOG

背景：docs/CHANGELOG.md 精心维护但 66 个 tag 上只有 [Unreleased] 段，release.yml:187 用 --generate-notes 自动生成，changelog 从未出现在 Release。

修复：release.yml 发布步骤改为：用 awk/sed 提取 docs/CHANGELOG.md 的 [Unreleased] 段内容作为 release notes body（空则回退 --generate-notes）。另加 scripts/dev/changelog-cut.sh（或等价小脚本）：把 [Unreleased] 固化为指定版本段，供发布前手动执行（README 或 docs 相应处补一句流程说明）。

校验：yaml 语法校验；提取逻辑本地用历史 CHANGELOG 验证。

## Task 21: F1 安装链路完整性（子安装器纳入 release 校验）

背景：install.sh 按 tag 从 raw.githubusercontent 下载子安装器（scripts/install-panel.sh、install-agent.sh）直接执行，无完整性校验；README 宣称"校验下载的 Release 文件"但脚本本身不在校验链内。

设计（不引入签名密钥管理负担，复用现有 checksums 信任域）：
- release.yml：把 scripts/install-panel.sh、install-agent.sh、latx.sh、latx-ag.sh 作为 release 资产上传，并将它们的 sha256 纳入 checksums.txt（核对现有 checksums.txt 生成步骤，扩展之）。
- install.sh run_child：改为先从该 release 下载 checksums.txt，下载子安装器后校验 sha256 再执行；checksums.txt 中无对应条目（旧版本 release）时打印明显警告后继续（兼容旧 tag）。子安装器优先从 release 资产下载（与 checksums 同域），失败回退 raw.githubusercontent（仍校验哈希）。
- 注意 install-panel.sh/install-agent.sh 内部可能递归调用 install.sh 或引用自身路径——通读确认改动不破坏自引用。

校验：bash -n；本地构造假 release 目录验证校验通过/拒绝两条路径；workflow yaml 语法校验。

## Task 22: F2 e2e 全量进 CI

背景：scripts/e2e/ 20 个脚本（约 3867 行），CI 不跑，release.yml 只跑 install-entry.sh 与 panel.sh；chains.sh、links.sh、usernodes.sh 等仍在活跃修改却无回归门禁。

修复：
- 通读 scripts/e2e/ 全部脚本，搞清现有两脚本如何在 CI 跑起来（环境假设：panel 二进制？docker？xray？），把可同环境运行的脚本以 matrix 纳入 release.yml（或 ci.yml，择与现有结构贴合者）的 e2e job。
- 需要特殊环境（真 systemd、多机、外网）无法进 CI 的脚本：不硬塞，在报告逐一列出并说明原因；能在 CI 内用现有手段（如 fake systemd、容器）满足的尽量满足，但不得为进 CI 而削弱脚本断言。
- e2e 脚本若暴露真实产品 bug：停止，报告控制者，不顺手修产品代码。

校验：workflow yaml 语法；能在本地跑的脚本本地跑一遍。

## Task 23: 产品 bug B——socks/http 节点被订阅编译连坐丢弃

来源：Task 22 e2e（protocols.sh）暴露，main（925cad50b）上同样存在。

背景：sub 发布快照链路中，`publisher.go:316` 附近对面板节点调 `buildSbOutbound`，其 default 分支对不支持协议返回错误（singbox.go:102 附近），`compileNodes` 遇到错误 `continue` 丢弃整个节点；而外部订阅节点路径是降级保留（clash 可表达）。socks/http 是 sing-box 原生支持的 outbound 类型，正确修法是支持而非丢弃。

修复：诊断后修复——首选在 buildSbOutbound 补 socks/http outbound 构造（sing-box 原生支持，字段映射对齐现有 vless 等实现）；同时评估 compileNodes 的连坐语义：单节点构造失败是否应降级为"该节点不进入 singbox 格式但保留于其他格式"，而非整体消失（与外部订阅路径行为对齐）。protocols.sh 的相应断言恢复进 CI matrix（若 Task 22 已排除）。

测试：backend sub 包新增 socks/http 节点的 singbox 快照断言；`cd src/backend && go test ./...` 全绿；本地跑 protocols.sh 相应部分。

## Task 24: 产品 bug A——链条目不进订阅

来源：Task 22 e2e（chains.sh）暴露，main（925cad50b）上同样存在。

背景：链 active + 已分配用户，订阅轮询 10s 始终无链条目（chains.sh:185-197 的"订阅内容"断言失败）。sub 包单测全绿，属集成路径问题。疑似与近期订阅分组/两阶段生成功能（ba2706d9b 等提交）的交互有关。

修复：先诊断根因（用 scripts/e2e/chains.sh 本地复现，沿 分配→发布→快照→渲染 链路定位），修复后恢复 chains.sh 进 CI matrix。诊断结论若表明是设计意图变化（如链条目需显式开启）而非 bug，停止并报告控制者，不强行"修复"。

测试：修复带回归测试；`cd src/backend && go test ./...` 全绿；本地 chains.sh PASS。

## Task 25: e2e 兼容性收尾（SSRF 测试钩子 + 测试 flake 修复 + matrix 补回）

来源：Task 22 审查转办 + Task 10/18 登记的测试 flake。

内容：
- SSRF 防护（requester.GuardedDialContext）为 e2e 留测试钩子：env 开关（如 LATX_ALLOW_PRIVATE_OUTBOUND=1，仅 e2e/开发用，文档注明生产不得设置）放行私网拨号；groups.sh 恢复进 CI matrix（hook 生效验证）。
- 修复预存在的测试 flake：TestHandleSubClientDownloadStartRateLimit 的 TempDir 清理竞态（下载 goroutine 与 RemoveAll 并发；Task 10/18 两次登记，父提交可复现）。修法：测试结束前等待/停止下载 goroutine，或改用 t.Cleanup 同步点。
- Task 23/24 完成后，把 chains.sh、protocols.sh 补回 matrix（若它们的修复任务已合入）。
- CopyFileAtomic 的"perm 仅创建时生效"同款问题（Task 18 登记）顺手修。

测试：backend/agent module 全绿；groups.sh 本地 PASS；flake 修复后 `-count=20` 复跑通过。

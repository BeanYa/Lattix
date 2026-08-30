# 第三期计划：清理 + 文档回写 + README 简化（2026-08-24）

前置：第一期（安全修复 23 commits）与第二期（设计/坏味道/流程 68 commits）已完成并终审通过，均在分支 fix/security-review（worktree .worktree/security-fixes）。本期做收尾：清理死代码、把文档与实现对齐、重写精简 README。

## Global Constraints

- 文档用中文，行文风格对齐各文档现有惯例；代码改动极小且仅限任务列出范围。
- 保留文件行尾；不引入新依赖；不改任何产品行为。
- 代码改动后：`cd src/frontend && bun run lint && bun run build` 全绿（动了前端）；`cd src/backend && go test ./...` 全绿（动了 backend 注释/契约）。
- commit message：`docs(...)` / `refactor(...)` 风格，中文简述。
- 所有工作在 worktree `/home/bean/workspace/Lattix-codex/.worktree/security-fixes`（分支 fix/security-review）。

## Task 1: 清理 + API 契约与 CHANGELOG 回写

背景：二期发现前端 `src/frontend/src/lib/api.ts:303` 的 `setUserAssignments` 已无任何调用方（死代码）；后端 `/api/user/set-nodes`（panel/users.go 的 handleSetUserNodes，约 :689）是遗留兼容端点（现行模型为分组派生链分配，保留它供历史/独立节点场景）。另外第一期 Task 12 给服务器创建/轮换 token 响应加了 `install_insecure` 字段但 docs/openapi.yaml 未同步；Task 10/18 给订阅客户端下载加了 429+Retry-After 限流也未同步。

内容：
- 删除 `setUserAssignments`（api.ts:303-307 附近，含确认无调用方后删除；若有相关类型也变得无人使用则一并删）。`src/frontend/src/lib/api-contract.generated.ts` 是生成文件，端点仍在则不动。
- handleSetUserNodes 的函数注释补一句遗留定位（如"遗留兼容端点：现行授权模型为用户组/节点组派生链分配（§16），该端点保留给历史/独立节点场景"），对齐 framework-design.md 的措辞。
- docs/openapi.yaml 同步：①servers 创建与 token 轮换响应补 `install_insecure: boolean`（先读 src/backend/internal/panel/servers.go 两处响应构造确认字段位置与类型描述）；②订阅客户端下载端点补 429 响应（带 Retry-After 头）——先读 src/backend/internal/sub/client_download.go 确认端点路径与响应体形态；③顺带抽查 openapi.yaml 与现行路由的其他偏差（有契约测试 TestOpenAPIRoutesMatchRegisteredRPCs，跑它确认路由层面一致；schema 层面的明显漂移修正，轻微的不扩大战场，列入报告）。
- docs/CHANGELOG.md 的 [Unreleased] 段补全本分支用户可感知变更（按 Added/Fixed/Security/Changed 分组，条目粒度对齐现有风格；内容参考分支 git log 925cad50b..HEAD，精选用户可感知项：安全修复类、行为变化类如升级后会话失效一次、socks/http 订阅修复、下载限流、明文安装命令警告、e2e/CI 变化等；纯内部重构不列）。

测试/校验：前端 lint+build 绿；backend go test 全绿（openapi 契约测试通过）；CHANGELOG 无需测试。

## Task 2: 设计文档回写（对照本分支全部改动）

背景：本分支 91 个 commit 改了架构细节，docs/ 下设计文档多处描述已与实现不符。第一期 Task 3 已对齐 ws-protocol.schema.json/xray-cleanup/latency_ms，README 有三次小同步，其余未回写。

内容（逐文档核查并修正，以代码现状为准）：
- docs/framework-design.md：
  - §3 仓库结构与技术栈：panel 拆出的五个子包（internal/panel/{scheduler,releases,exchange,cdn,selfupdate}）补入结构描述；agent 新增 internal/fileutil。
  - §5 控制通道协议：ws 读模型变化（每连接读循环 + handlerPump 顺序消费，256 缓冲满断开补发）；业务命令门控位置从 ws 层移到 dispatch 层（语义不变：startup/faulted 拒投 14 类业务命令，清单以 src/backend/internal/dispatch 的 isBusinessCommand 为准）。
  - §12 安全：回写——会话签名密钥独立随机化（设置项 session_secret，改密轮换）；登录限流 per-IP+per-username 双桶；全站安全响应头四件套；出向拨号 SSRF 防护（requester，含 e2e 钩子 LATX_ALLOW_PRIVATE_OUTBOUND）；ip.sh 钉版+SHA256；agent token 经 LATTIX_TOKEN env 注入不进 argv；订阅客户端下载限流 10 次/小时/token；匿名订阅端点错误回显收敛。
  - 其他章节（§6 节点生命周期、§8 用户链路凭证、§16 分配）通读比对，修正已漂移的描述；不扩写新内容。
- docs/panel-lifecycle-state-machine-design.md：门控实现位置描述如有 ws 层字样则更新为 dispatch 层（状态效果不变）。
- docs/frontend.md：补 prettier（format/format:check 命令、CI format:check）；结构描述更新（lib/use-polling.ts、lib/status.ts、lib/format.ts 归并；pages 下 chains/servers/settings/users 子目录的拆分组件）。
- docs/rpc-api-design.md、docs/subscription-routing-design.md、docs/chain-revisions-traffic-design.md：通读找漂移（重点：门控、限流、端点/assignment 相关描述），修正或注明。
- docs/KNOWN_ISSUES.md：定位为部署边界文档，不加代码债；仅在确有用户可感知的部署边界变化时补充（预期无，核查后报告说明）。
- 每处修正引用代码位置核实；docs/superpowers/ 归档目录不动。

校验：无代码测试；报告列出每文档的改动点与对应代码证据。

## Task 3: README 简化重写

背景：README.md 当前 442 行，过于冗杂（安装选项表、e2e 说明、发版流程等都堆在里面）。用户要求：仅介绍项目和简易架构等即可。

内容：
- 重写 README.md，目标 ~100-150 行以内：项目简介（是什么、核心特性 3-5 条）、架构简述（panel/agent/单条 WS/xray gRPC 热操作，可配简单文本图）、快速开始（Docker Compose 推荐 + 原生两种安装各一行命令、默认端口与监听地址、安装后输出管理员凭据）、系统要求（linux/amd64+arm64、root/sudo、curl）、文档链接表（docs/ 各文档一句话索引）。
- 细节内容（安装选项全表、卸载、更新、发版流程、e2e 说明等）若有价值移到 docs/ 对应文档（如安装细节进 docs/ 或 install 脚本注释；发版流程已在 §18 惯例则留 docs），或在报告注明删除理由。不得丢失任何"用户首次安装必需"的信息。
- 保留 README 现有的徽章/头图（如有）与语言风格（中文）。
- 与 Task 2 的 docs 改动避免冲突：本任务只动 README.md（及必要时向 docs 移入内容）。

校验：链接有效性（相对路径文件存在）；事实核查（端口、命令、平台与 install.sh/脚本实际行为一致）。

## Task 4: -reset-admin 轮换 session_secret（代码漂移修复）

来源：Task 2 审查确认的代码漂移（转办）。

背景：`src/backend/cmd/backend/main.go` 的 -reset-admin 路径（约 :103-126）直写 bcrypt 哈希到库并退出，不轮换 session_secret——第一期 Task 8 后会话密钥独立于口令，该路径下已签发会话继续有效；且 flag 帮助/注释/退出提示仍描述旧的"密钥派生自密码哈希/所有会话已失效"语义。

修复：reset-admin 写库成功后同样轮换 session_secret（或删除该设置键让下次启动重新生成——择与现有代码最贴合、最简者）；更新帮助/注释/退出提示文案为实际行为（重置后所有会话失效）。加测试覆盖（reset 后旧会话签名验证失败）。

测试：`cd src/backend && go test ./...` 全绿；commit message：`fix(backend): 中文简述`。

# 安全审查修复计划（2026-08-20 全面 code review 后续）

来源：2026-08-20 全仓库 code review（9 个并行审查代理）的安全轴 + B0/P2/P1/P3/P4 + 流程轴低风险项。
明确不在本计划内（已沟通为重构或需另行设计，禁止顺手做）：panel 拆包/Dispatcher 重构（D1/D2）、重复代码与坏味道清理、前端巨型组件拆分、完整 CSP 策略、F1（install.sh 子安装器签名设计）、F2（e2e 全量 CI 矩阵）、F4（CHANGELOG 发布流程）。

## Global Constraints

- 遵守 AGENTS.md：复用现有基础设施（requester、nettrust、store 设置机制），不引入新第三方依赖（标准库优先）。
- 最小改动：只改修复所需代码，不做顺手重构/清理/改名；匹配周边代码风格与注释语言（中文注释为主）。
- 仓库混用 CRLF/LF 行尾：保留每个被编辑文件的现有行尾；diff 不得出现全文件重写（Task 13 的 Dockerfile 行尾转换除外，那是任务本身）。
- 每个行为修复必须带测试；完成后运行受影响 Go module 的相关测试包并保证全绿（agent: `cd src/agent && go test ./...`；backend: `cd src/backend && go test ./...`；shared: `cd src/shared && go test ./...`）。脚本改动跑 `bash -n`。
- 不改变用户使用流程：install command 形态、bootstrap→长期 token 换发、登录限流 UX（5 次/分钟）、订阅使用保持不变（Task 8 升级后一次性会话失效除外，已与用户确认）。
- commit message 遵循仓库惯例：`type(scope): 中文简述`（参考 git log，如 `fix(panel): ...`）。
- 所有工作在 worktree `.worktree/security-fixes` 的分支 `fix/security-review` 上进行；每个任务至少 1 个 commit。

## Task 1: B0 telemetry forward 跳流量统计前缀修复

背景：xray stats 中 forward 跳 inbound 的实际 tag 由 `shared.ChainForwardTag` 生成为 `chainfwd_<hopID>`（src/shared/chain.go:17），但 `src/agent/cmd/agent/telemetry.go:94-95` 解析 stats 名时匹配的是 `chain_forward_` 前缀——永不匹配，forward 跳流量计数静默为 0。`src/agent/cmd/agent/telemetry_test.go:14-15` 使用同一错误前缀，测试因此假绿。

修复：
- telemetry.go 的 `chain_forward_` 前缀（两处：HasPrefix 与 TrimPrefix）改为 `chainfwd_`；telemetry_test.go 同步改为真实前缀。
- 在两个前缀处加注释引用 `shared.ChainForwardTag`（如 `// 前缀与 shared.ChainForwardTag 的 chainfwd_%d 对应`）。若可以极低成本从 shared 推导前缀（如对 `shared.ChainForwardTag(0)` TrimSuffix "0"）则优先推导，否则仅注释。
- 全仓 `grep -rn "chain_forward_" src` 确认无其他遗漏。`node_`、`shared_endpoint_` 前缀与 `shared.NodeTag`（src/shared/config.go:147）、`shared.SharedEndpointTag` 一致，不要动。

测试：`cd src/agent && go test ./...` 全绿。

## Task 2: P2 startup/faulted 业务命令门控补全

背景：`src/backend/internal/ws/hub.go` 的 `isBusinessCommand`（约 220-233 行）只覆盖 9 类老命令；docs/panel-lifecycle-state-machine-design.md:29,32 规定 startup 状态"业务命令 暂停"、faulted"业务命令 禁止"。后续新增的命令类型未被门控，在 startup/faulted 仍会投递。

修复：isBusinessCommand 补充以下类型（常量均在 src/shared/messages.go）：
- `shared.TypeApplySharedEndpoint`（"shared-endpoint.apply"）、`shared.TypeRemoveSharedEndpoint`（"shared-endpoint.remove"）
- `shared.TypeCleanupXray`（"xray.cleanup"）、`shared.TypeRebuildXray`（"xray.rebuild"）
- server-test.run 对应的 Type 常量（grep src/shared 确认常量名；agent 侧 src/agent/cmd/agent/main.go:703 有该 type 字符串的使用点）

测试：ws 包现有测试保持绿；新增/更新门控单测覆盖全部新加类型（startup 与 faulted 下返回 ErrPanelNotActive）。`cd src/backend && go test ./...` 全绿。

## Task 3: P1/P3/P4 文档与 WS schema 对齐（纯文档，不改代码逻辑）

- P1：docs/ws-protocol.schema.json 与实现漂移，按实现修正 schema（实现为准）：
  1. `shared-endpoint.apply` 的 `clients`/`routes`：Go 侧 `src/shared/messages.go:266-267` 无 omitempty、nil 切片序列化为 `null`，且 docs/framework-design.md:637 要求空端点也下发 apply——schema 将两字段改为允许 `["array","null"]`。
  2. `chain-hop.apply` 的 `revision_id`：Go 侧 messages.go:363 为 `json:"revision_id,omitempty"`——schema 将其从 required 移除（或在描述中注明运行期总会赋值）。
  3. 在 schema 文件能找到的描述位置（或 docs/rpc-api-design.md 引用 schema 处，约 :460）注明"schema 为参考性描述，无运行时/CI 强制，以 Go 序列化为准"。
- P3：docs/xray-cleanup-design.md 过期：文首"待实现"标注去掉；路由段落（约 :159）改为现行 `POST /api/server/cleanup-xray`（body 含 server_id，HTTP 200 业务信封；实现见 src/backend/internal/panel/panel.go:301 与 servers.go:840-863），并注明符合 docs/rpc-api-design.md 的现行路由规范。
- P4：docs/panel-lifecycle-state-machine-design.md:135 与 docs/server-probe-monitoring-design.md:23 的"发送 `latency_ms: null`"改为"缺省或 null"（实现为 `*float64 json:"latency_ms,omitempty"`，src/shared/messages.go:321）。

无代码测试；改完通读相关段落确认与实现引用行号一致。

## Task 4: H2 ip.sh 钉版 + SHA256 校验

背景：`src/agent/internal/servertest/ipquality/`（script.go、run.go、deps.go）每次 IP 质量检测都从上游 main 分支拉取 ip.sh 并以 root 执行，无钉版、无完整性校验；上游仓库被劫持即全网 agent root RCE。正确先例：同目录 ookla.go:35-42 固定版本+硬编码 SHA256。

修复（精确值，已核实）：
- script.go 的 `ScriptURL` 改为钉 commit：`https://raw.githubusercontent.com/xykt/IPQuality/0ee5f192fed70c04615852efba0e4b8bd43546c7/ip.sh`
- 新增常量 `scriptSHA256 = "9823c560e0d19769eb627329a31cb47da655d087166d86e40d9b6c77bc7f32fb"`（该 commit 的 ip.sh 内容 SHA256，文件 108624 字节）。
- `EnsureScript` 流程加校验：下载内容先算 SHA256，不匹配则不写缓存、返回明确错误（信息含期望与实际哈希）；匹配才进入现有版本比较/缓存更新逻辑。
- 缓存回退语义收紧：下载失败回退到本地缓存前，也校验缓存文件 SHA256；缓存哈希不匹配则报错，绝不执行未通过校验的脚本。
- 注释写明：升级上游版本 = 同步更新 ScriptURL 的 commit 与 scriptSHA256 两个值。

测试：ipquality 包新增/更新测试——哈希匹配通过、下载不匹配拒绝（不写缓存）、下载失败但缓存哈希匹配可回退、缓存哈希不匹配拒绝执行。`cd src/agent && go test ./...` 全绿。

## Task 5: L3 订阅端点错误回显收敛

背景：`src/backend/internal/sub/sub.go:448,494,511`（ServeHTTP / ServeRuleHTTP）向匿名客户端 `http.Error(err.Error())` 回显内部错误，泄露 DB 等内部细节。

修复：
- 内部错误（DB 失败、渲染失败等 500 类）：对外返回通用文案（如 `internal error`），原始错误用 `log.Printf` 记录（沿用同文件现有日志脱敏惯例，不打敏感参数）。
- 用户级业务错误（订阅不存在/过期/已禁用、格式不支持等 4xx 类）：保持现有回显文案不变。
- 顺带检查同包 client_download.go 的 `subDownloadErrorStatus`（约 296-300 行）：500 类内部错误同样收敛，4xx 业务错误保持。

测试：sub 包相关测试更新/新增——内部错误响应体不含原始错误细节、业务错误文案不变。`cd src/backend && go test ./...` 全绿。

## Task 6: L4 agent token 移出 systemd argv

背景：`scripts/install-agent.sh`（约 471-516 行）已把 `LATTIX_TOKEN` 写进 0600 的 `$ENV_FILE` 且 unit 有 `EnvironmentFile=`，但 ExecStart 仍展开 `-token "${LATTIX_TOKEN}"` 进 argv，`ps` 对本机所有用户可见。

修复：
- `src/agent/cmd/agent/main.go`：`flag.Parse()` 后，若 `*token` 为空则回退读取 `os.Getenv("LATTIX_TOKEN")`（注释说明：systemd EnvironmentFile 注入路径，使 token 不进 argv）。`-panel` 地址不敏感，不动。
- `scripts/install-agent.sh`：lattix-agent.service 的 ExecStart 删除 `-token "\${LATTIX_TOKEN}"` 片段，其余不变（保留 EnvironmentFile）。
- 检查 `scripts/latx-ag.sh` 是否也以 argv 向 agent 传 token，若有同样改为依赖 EnvironmentFile/环境变量。
- 全部改动后 `bash -n` 校验两个脚本。

测试：env 回退逻辑如可直接测试则加单测（若在 main() 内不可测，提取最小函数再测，保持小改）；`cd src/agent && go test ./...` 全绿。

## Task 7: L2 extsub 拨号时 IP 防护（SSRF DNS 绕过）

背景：`src/backend/internal/extsub/service.go` 的 `validateSubscriptionURL`（约 264-294 行）只拒 IP 字面量与内网后缀，不做 DNS 解析；域名解析到内网即可绕过（SSRF）。

修复：
- 在 `src/shared/requester/` 新增对外拨号防护（新文件，如 guarded_dial.go）：包装 `net.Dialer.DialContext`，先解析主机名，任一解析 IP 为 loopback/private/link-local/unspecified/multicast 或命中保留段时拒绝连接；保留段列表参考 extsub 的 `reservedCIDRs`（移入 requester 或两处共享，extsub 改为复用，避免两份拷贝）。导出构造函数（如 `NewExternalHTTPClient(timeout time.Duration) *http.Client` 或返回可注入的 `*http.Transport`）。
- extsub 生产路径的拉取客户端改用该防护客户端（遵守 AGENTS.md 复用 requester）；URL 层 `validateSubscriptionURL` 保留（双重防护）。
- `src/backend/internal/cdncatalog/catalog.go`（约 155-180 行）自建的 HTTP client 一并接入防护 DialContext（它有自定义 TLS 根与 Accept 头需求——防护只包 DialContext，不动其 TLS/Header 配置，二者组合；不强求改用 ExternalFileRequester，那是另一条重构）。
- 注意：现有测试用 httptest（127.0.0.1）——防护只接入生产构造路径，测试注入点保持可替换（Doer/DialContext 注入），不要为了让防护生效而改坏现有测试。

测试：requester 包新增单测（解析到 127.0.0.1、10.x、169.254.x 的拨号被拒；公网 IP 字面量放行——可用注入 resolver/直接对 guard 函数喂 IP 的方式，避免真出网）。`cd src/shared && go test ./...` 与 `cd src/backend && go test ./...` 全绿。

## Task 8: M2 独立随机会话密钥

背景：`src/backend/internal/panel/auth.go:27-39` 会话签名密钥 = `sha256(credentialKey + "|lattix-session")`，credentialKey 为 bcrypt 哈希或明文 admin-pass（settings.go:725-735 credentialKey）。DB/备份泄露即等价泄露会话主密钥（无需破解 bcrypt 即可伪造会话）；回退模式下截获 cookie 可离线字典破解口令。

修复：
- store 新增设置键常量：`SettingSessionSecret = "session_secret"`（src/backend/internal/store/settings.go 常量区，仿 SettingAdminPassBcrypt 注释风格）。
- panel 的 `sessionSecret` 改为：读该设置；为空则 crypto/rand 生成 32 字节（hex 编码）并 SetSetting 持久化后使用。密钥不再派生自任何口令材料。首次生成存在并发竞态（两个请求同时生成）可接受，后写覆盖，注释说明。
- 保持"改密码即全部会话失效"属性：`handleChangePassword`（settings.go:646）密码修改成功后重新生成并覆盖该密钥。
- 更新 auth.go 中 sessionSecret 的注释：密钥为独立随机值；DB 泄露仍可伪造会话（与会话表方案等同的残余风险），但口令材料不再因此泄露。`credentialKey`/`sessionSecretForCredential` 若无其他调用方则删除，有则保留。
- 与用户确认过的影响：升级后所有已登录会话失效一次（密钥新生成），之后无感。

测试：panel 包——新密钥签发会话可验证、密钥轮换后旧会话失效、改密后旧会话失效。`cd src/backend && go test ./...` 全绿。

## Task 9: M1 登录限流 per-username 兜底

背景：`src/backend/internal/panel/login_limiter.go` 按 IP 计数（5 次失败/1min 窗口→封 5min），IP 取自 `logging.ClientIP`（nettrust 采纳 XFF）。nettrust 默认信任私网对端，能与面板端口直连的同网攻击者随机化 XFF 即可获得新桶，暴力破解保护失效。约束：不能改按 RemoteAddr 计数——反代部署（CF→OpenResty→容器）下所有用户会共享代理 IP 一个桶，一人锁定全员。

修复：
- 复用 loginLimiter，增加 per-username 桶：key 为 `"u:" + strings.ToLower(username)`；新常量：username 桶 10 次失败 / 5min 窗口 → 封 15min（与 IP 桶的 5/1min→5min 并存，互不影响）。
- handleLogin（auth.go，约 91 行起）：两桶任一 blocked 即返回现有限流响应（retryAfter 取两者较大值）；认证失败两桶都记录，认证成功两桶都清零。空用户名也计数（归为同一 "u:" 桶）。
- 代码注释写明残余风险：知晓用户名者可持续失败登录以锁定该账号（fail2ban 同类权衡），防暴力破解优先。

测试：login_limiter / auth 测试——IP 桶行为不变、username 桶独立触发与解封、认证成功两桶清零、同一 username 换不同 XFF 仍累计。`cd src/backend && go test ./...` 全绿。

## Task 10: M3 订阅 token 触发客户端下载限流

背景：`src/backend/internal/sub/client_download.go` 任意有效 sub token（订阅用户，未必受信）可为约 40 个 variant 各触发 ≤512MB（maxClientPackageBytes）GitHub 下载落盘，缓存 TTL（defaultClientCacheTTL=72h）过期后可反复触发，磁盘/带宽消耗无上限。

修复：
- 在新建下载任务的入口（client_download.go 中创建 clientDownloadTask 的 handler/函数，自行定位）加 per-token 限流：内存滑动窗口，每 token 每小时最多新建 10 个下载任务（常量 `maxClientDownloadTasksPerTokenHour = 10`）。超限返回 429 + 通用文案（遵循 Task 5 的错误回显约定）。
- 只对新触发上游下载的任务计数：同 variant 已有活跃任务（activeDownloads 去重）或缓存命中直接服务的路径不计数。
- 实现参考 login_limiter 模式：map[token]时间戳列表 + mutex + 惰性清理 + 跟踪上限（如 4096 个 token，防内存膨胀）。Server 上已有 downloadMu 可复用或新建小 struct，择与现有代码最贴合者。
- ticket 机制（clientDownloadTicketTTL）不变；断点续传的 Range 请求不经过任务创建点，不受影响。

测试：sub 包——窗口内第 11 次新建被拒（429）、窗口滑动后放行、不同 token 互不影响、缓存/活跃任务路径不计数。`cd src/backend && go test ./...` 全绿。

## Task 11: L1 安全响应头中间件

背景：全站无任何安全响应头（无 CSP/nosniff/frame 防护/HSTS）。

修复：
- 在 `src/backend/cmd/backend/main.go` 的 handler 装配点（约 650 行 `http.Server{Handler: handler}`）包一层安全头中间件（放在 main 包或 web 包，择改动最小者；若已有中间件链按现有模式接入）。对所有响应设置：
  - `X-Content-Type-Options: nosniff`
  - `X-Frame-Options: DENY`
  - `Referrer-Policy: strict-origin-when-cross-origin`
  - `Content-Security-Policy: frame-ancestors 'none'`
- 不引入完整 CSP（前端未验证，另行评估）。WebSocket 握手与订阅/下载响应同样经过即可（无害）。

测试：httptest 单测断言四个头存在。`cd src/backend && go test ./...` 全绿。

## Task 12: H1 明文安装命令警告

背景：面板默认无 TLS；非反代、裸 http 公网部署时 install command 为 http（推导 ws://）明文，Agent token 可嗅探、命令可伪造（审查 H1）。本任务只加警告护栏，不改命令形态（用户的 CF+OpenResty+wss 部署不触发警告）。

修复：
- backend：`src/backend/internal/panel/servers.go` 两处返回 install_command 的响应（:357 与 :425）旁增加 `"install_insecure": bool`：当 `s.panelBase(r)` 为 http 且其 host 非 loopback/非私网 IP 时为 true。host 判定用 `net.ParseIP` + `IsLoopback()/IsPrivate()` 本地小函数（主机名为域名时按公网对待，返回 true——保守告警）。panelBase 取值逻辑不动。
- frontend：找到展示 install_command 的组件（src/frontend/src/pages/Servers.tsx 附近），`install_insecure` 为 true 时在命令下方显示一行警告（样式参考项目现有告警/提示组件，主题兼容）：`面板地址为明文 http：Agent 与面板间的控制流量可被窃听或篡改。跨公网部署请改用 https 反向代理。`
- 若同一创建流程在别处（如服务器详情抽屉）也展示 install command，一并处理；找不到第二处就在报告里说明。

测试：backend panel 包——响应含正确 install_insecure 布尔（http+公网 IP=true、https=false、http+127.0.0.1/私网=false）。前端若有现成组件测试则补，否则在报告中说明人工核对方式。`cd src/backend && go test ./...` 全绿；前端 `cd src/frontend && bun run lint`（或 package.json 中的等价命令）。

## Task 13: F 系列流程修复（CI/脚本/Dockerfile）

- F3：`.github/workflows/ci.yml:45` bash -n 语法检查的 `find scripts -maxdepth 1` 去掉 `-maxdepth 1`（与 release.yml:48 对齐，覆盖 scripts/e2e 与 scripts/dev）。
- F5：`.github/workflows/release.yml:132-141` e2e 用 gh release download 直接 unzip xray、不做官方 .dgst 校验——改为下载对应 .dgst 并校验（校验实现参考 scripts/install-agent.sh:413 附近的做法），不降级跳过。
- F6：`scripts/latx.sh:294-307` `latx update` 无回滚——覆盖旧二进制前先 `cp` 备份为 `<bin>.bak`，ready 探测失败时自动还原备份、重启服务、报错退出；成功后删除 .bak。
- F7：`Dockerfile:3` 前端阶段 `oven/bun:1-alpine` 浮动 tag → 钉 `oven/bun:1.3.14-alpine`（与 CI setup-bun 的 1.3.14 一致）。
- F9：`release.yml:219` 与 `scripts/install-panel.sh:189` 硬编码 `ghcr.io/beanya/lattix`——release.yml 改用 `${{ github.repository }}` 派生并小写化；install-panel.sh 用脚本内已有的仓库变量（核对变量名，如 GITHUB_REPO）派生。
- F10：`install.sh:120` usage 行补上 agent 组件说明；`Dockerfile` 全文件 CRLF 行尾转 LF。

校验：所有改动脚本 `bash -n` 通过；workflow yaml 用可用工具做语法校验（python3 -c 'import yaml,sys;yaml.safe_load(open(...))' 或等价）。

## Task 14: F8 GitHub Actions 钉 commit SHA

- `.github/workflows/` 下全部第三方 action 的 `uses:`（actions/checkout、actions/setup-go、actions/setup-bun、actions/upload-artifact、actions/download-artifact、docker/* 等）从浮动 tag 改为 pin 当前大版本最新 release 的 commit SHA，格式 `uses: actions/checkout@<full-sha> # v7`（保留版本注释）。用 GitHub API（api.github.com/repos/<owner>/<repo>/git/refs/tags/<tag> 或 releases/latest）解析各 action 现用 tag 的 commit sha；annotated tag 需解引用到 commit。
- 工作流权限结构（顶层 contents: read 等）与其他步骤一律不动。
- yaml 语法校验通过。

## Task 15: L3 同类收敛——api.go 匿名订阅端点

来源：Task 5 审查发现，与 L3 同类漏洞，范围外追加。

背景：`src/backend/internal/sub/api.go:80,157,201,207,337,342` 的匿名 `/api/sub/{token}/info|clients|status` 端点在 500 时仍回显 `err.Error()`，泄露内部错误细节。

修复：完全套用 Task 5 已建立的 `writeInternalError` 模式（sub.go）：内部错误对外通用文案 + log 记原始错误（沿用脱敏惯例，不打 token）；4xx 业务文案逐字节不变。先通读各调用点错误来源再二分。

约束：最小改动、周边风格、保留 CRLF 行尾、无新依赖；`cd src/backend && go test ./...` 全绿；commit message `fix(backend): 中文简述`。

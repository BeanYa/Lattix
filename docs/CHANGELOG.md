# Changelog

本项目所有显著变更记录于此。格式基于
[Keep a Changelog](https://keepachangelog.com/zh-CN/1.1.0/)，版本号遵循
[Semantic Versioning](https://semver.org/lang/zh-CN/)。

## [Unreleased]

### Changed

- 订阅预编译中间态进一步推迟选项展开：组选项中的选项级占位符
  （`__LATTIX_ALL__`/`__LATTIX_REGIONS__`/`__LATTIX_REGION_XX__`，新增内部占位符
  `__LATTIX_REGION_NONE__` 表示无地区节点）在中间态保持原样，剪枝与引用环检测
  改为占位符感知，直到最终渲染（交付）阶段才统一展开为真实节点/分组名；
  交付内容对全部占位符（条目级 + 选项级）做残留扫描，残留即发布失败。
  最终交付文件行为不变，预编译预览（`stage=pre`）中组选项同样只显示占位符。

### Breaking changes

- `GET /api/billing/stats`（成本统计）响应字段改名：`costs_public` →
  `actual_costs_public`、`costs_custom` → `actual_costs_custom`、
  `totals_public` → `actual_totals_public`、`totals_custom` →
  `actual_totals_custom`。外部 API 消费者需同步更新。

### Added

- 订阅文件生成引入预编译中间态：保证同类节点（面板管理链路节点 / 各外部订阅
  节点）在生成过程中始终作为整体参与分组处理，最后渲染阶段才替换回真实属性
  与格式。发布流程先生成节点区/来源分组区以占位符表示的
  中间文档（`__LATTIX-NODES__`/`__OUTTER-SUBS-NODES__` 节点条目，
  `__LATTIX-GROUP__`/`__OUTER-SUBS-GROUP__` 来源分组条目，后者解压为每个外部
  订阅一组），全部分组处理完成后在交付前解压替换为目标格式的真实节点/分组
  条目；空来源占位符条目解压时删除，交付内容残留占位符即发布失败。原生模板
  作者可手写这些占位符控制节点/分组注入位置（不写则保持既有默认注入行为）。
  每个快照另存 `<format>-pre` 中间态文件（公共订阅端点不可达），用户订阅预览
  对话框新增「正式交付 / 预编译中间态」阶段切换（预览 API 加 `stage=pre`）。
- 新增面板缩写设置 `panel_short`（默认 `Lattix`，设置页「基本设置」可改）：链路命名
  模板新增 `{{PANEL_SHORT}}` 变量（前端预览/补全同步支持）；订阅发布按节点来源
  自动生成分组——「<面板缩写> 分组」包含全部面板管理链路节点，每个外部订阅
  一组（组名 = 订阅名）包含其解析节点；无节点的来源不生成分组，与既有策略组
  重名时跳过并告警；原生模板（Mihomo/sing-box）展开路径同步追加分组；
  `panel_short` 变更保存后全量用户订阅自动异步重发布，新组名即时生效。
- 前端新增可插拔主题系统（`src/frontend/src/themes/`）：设计主题与外观模式（浅色/
  深色）两个维度运行时切换，顶栏调色板菜单选择并持久化；新主题按「目录 + 注册表
  记录」安装即可自动出现在切换菜单（见 `src/frontend/src/themes/README.md`）。
- 经典 Cream Grid 设计保留为可选主题（`cream`）：全量语义令牌覆写 + 仪表盘页面
  覆写，与默认主题可随时互切（亮/暗各两套）。
- 旁路式操作进度观察系统（observe）：链路/节点增删改与重试、链路分组与用户分组
  操作、服务器修复/清理/重建、外部订阅与模板同步等长操作的响应信封可选携带
  `observe_id`，`GET /api/observe-task/get` 查询快照，前端弹窗展示阶段清单、进度、
  警告并自动收口（订阅重生成完成才关闭）；观察尽力而为，不影响业务操作。
- 订阅落地页客户端下载改为会话票据直链：面板代取 GitHub Release 客户端安装包并
  校验发布方 SHA-256（缓存 72 小时、上限 512 MiB），`/api/sub/{token}/client-download/*`
  四端点（start/status/ticket/file）配合浏览器原生下载，支持断点续传；票据 3 小时
  有效并与订阅 token、任务双向绑定。
- 服务器测试新增 speedtest.net 测速目标（Ookla CLI 执行）与 TCP 返回路径 traceroute
  回程探测。
- 服务器离线事件、告警与链路降级重估增加跨 WS 抖动消音：断连后经消音窗口（默认
  10s，`LATTIX_OFFLINE_DEBOUNCE` 可覆盖）仍离线才触发，窗口内重连全部取消。
- 新增 `GET /api/billing/stats/estimated`：对启用统计计费且未过期的服务器，按
  日成本 × 周期天数（日 1 / 月 30 / 年 365）估算每周期成本。
- 成本统计页新增「计算成本」tab：估算日/月/年成本汇总卡片、周期分布图与明细矩阵；
  原统计口径更名为「已生效成本」tab。
- 新增外部订阅管理页：导入第三方订阅 URL（base64 分享链接 / Clash-mihomo YAML /
  v2rayN 自定义格式），解析节点保存到外部链路表，订阅信息（流量/到期/节点数）保存到
  外部订阅表，支持手动与定时同步（每订阅可配间隔）。
- 用户可引入外部订阅并分配（叠加/并入/附加三种模式），订阅流量合并计入用户用量
  统计，新增外部订阅分配 RPC（设置/查询/关联用户级联清理）。
- 外部订阅节点字段保真：clash/mihomo YAML 输出覆盖完整 proxy schema（alpn、
  plugin-opts、smux、h2-opts、fragment、ipv6、anytls idle-session 系列等），
  `skip-cert-verify: false` 等 falsy 值按「字段存在」回填；schema 之外的未知/
  扩展字段（如第三方认证参数）原样透传；sing-box 出站补 alpn/idle-session/
  plugin/ipv6 映射；分享链接抑制已消费的 YAML 键（servername/client-fingerprint
  等）并透传未知标量参数。

### Changed

- 订阅策略分组可达性增强：模板定义的分流组（地区组除外）在保留原有选项
  （含地区/来源分组引用）的基础上，补充 DIRECT/REJECT 兜底并追加全部节点与
  「自动选择」「节点选择」引用，任何分流组的流量既可流向指定节点也可回落
  自动选择、直连或拒绝；「自动选择」自身不补 DIRECT/REJECT（纯节点测速组，
  兜底出站会污染延迟测试结果）。
  「故障转移」等节点池组（选项含 `__LATTIX_ALL__`）与「节点选择」（final）
  的选项仅保留节点与叶子分组（来源分组 + 地区分组 + 无地区分组，及
  DIRECT/REJECT），剔除其他组引用以杜绝引用环，另有发布前引用环检测兜底；
  来源分组（「<面板缩写> 分组」与各外部订阅分组）与地区分组同级，随
  `__LATTIX_REGIONS__` 一同展开、一同剔除（原生模板路径同步）。
- 前端默认设计语言切换为 Apple HIG（原 Cream Grid 设计经令牌层重构；语义令牌名
  保持不变，仅重定义取值，原设计以可选主题保留）。
- 用户与订阅模板指派的订阅发布由请求内同步改为异步：创建用户、停用/启用
  （停权跃迁）、分配/外部订阅变更、订阅设置保存、重新生成订阅、模板指派/取消
  指派不再阻塞请求，统一经 observe 弹窗跟踪至重发布完成（失败以警告呈现）。
  `POST /api/user/regenerate-subscription` 响应不再同步携带发布结果快照
  （`data` 为 null），结果经 `observe_id` 轮询获取；订阅 token 重置保持同步
  发布 + 失败回滚不变量，不受影响。
- CI/发版流程：release e2e job 以 matrix 纳入 14 个可运行脚本（含 chains.sh、
  groups.sh 回归，并注入 SSRF 私网出向测试钩子）；Release notes 改为提取
  CHANGELOG [Unreleased] 段（版本段优先三级回退，提取失败兜底不阻断发布），
  新增 `scripts/dev/changelog-cut.sh` 将 [Unreleased] 固化为版本段；前端 CI
  增加 prettier `format:check`；第三方 GitHub Actions 引用从浮动 tag 钉到
  commit SHA。

### Fixed

- 修复用户订阅预览接口因 RPC 路由查询参数白名单未登记 `stage` 而拒绝请求
  （`unknown query parameter: stage`）的问题：`/api/user/subscription-preview`
  的 `AllowedQuery` 补上 `stage`。
- 反代终止 TLS 的 https 识别修复：`nettrust` 在回环之外内建信任内网/容器网段
  （RFC1918、169.254/16、CGNAT 100.64/10、IPv6 ULA/链路本地），1panel/OpenResty、
  docker compose、局域网 nginx 反代部署无需再手工配置 `trusted_proxies`，
  安装命令/订阅链接即正确推断 https、日志与 agent 地址学习取真实客户端 IP；
  `trusted_proxies` 语义改为在其上追加公网回源网段（如 CDN），公网对端直连的
  `X-Forwarded-*` 伪造声明仍不采信。
- 链状态机补 `applying`/`waiting_for_agent` → `active_failed` 转换边：已发布链编辑
  失败不再永久卡在 applying。
- 链路 revision 状态转换加 CAS 守卫：迟到失败回执不再覆盖已发布 revision；发布窗口
  竞态在链恢复 active 后自动补发发布；Agent 上线复位 `waiting_for_agent` revision。
- 修复用户到期停权后订阅不重发布的缺陷（sweeper 补 EnqueueUsers 触发异步发布队列）。
- Agent：状态落盘互斥修复；自升级基址强制 https 校验；命令 payload 严格解码。
- 计算成本（`GET /api/billing/stats/estimated`）折算模型改为「12 个月 × 30 天」口径：
  - 日成本按计费周期推导：年付 = 年付价 ÷ 周期年数 ÷ 12 ÷ 30，月付 = 月付价 ÷ 周期
    月数 ÷ 30，季付 = 季付价 ÷ 3 ÷ 30；月成本 = 日成本 × 30（年付 = 年付价 ÷ 12），
    年成本 = 日成本 × 360（月付 = 月付价 × 12、季付 = 季付价 × 4）
  - 自定义范围总成本 = 年成本 × 整年数 + 月成本 × 整月数 + 日成本 × 剩余天数
    （整月 30 天、整年 360 天），12 个月视图精确还原年价（如 99 CNY/年 12 个月
    合计 97.68 → 99.00）
  - 响应新增 `monthly_minor` / `annual_minor`（及 custom 变体），前端服务器汇总表
    标注折算公式、汇总卡片改为 ×360
- 计算成本页估算单位随粒度切换（日/月/年）：粒度与计费周期匹配时（如年付 + 年粒度）
  直接使用周期实价（`monthly_minor`/`annual_minor` 精确值），其余按「月成本为主」折算
  （日成本 = 月成本 ÷ 30、月成本 = 年成本 ÷ 12），原价/周期列的换算标注区分实价与估算。
- 外部订阅列表/节点列表为空时返回 `[]` 而非 `null`，修复外部订阅页与用户页打开
  新建/编辑弹窗时的 `Cannot read properties of null (reading 'length')` 崩溃。
- custom 换算模式下，无自定义锚点服务器的周期成本以 public 回退值计入 custom 合计，
  合计列 = 可见单元格之和。
- 外部订阅导入：默认 UA 改用 clash-meta 并支持 UA 预设，导入错误直接显示在对话框。
- 外部订阅节点数改为按实际解析内容统计。
- 外部订阅节点参与订阅策略的区域分组：编译时从节点名称推断国家/地区代码
  （旗帜 emoji 区域指示符优先，其次地区关键词），与面板管理节点同级进入
  `__LATTIX_REGION_XX__` 组及自动生成的地区分组，不再只出现在
  `__LATTIX_ALL__`（自动选择）中；无法推断地区的节点收进固定生成的
  「🌐 无地区」分组（排在地区分组最后，随 `__LATTIX_REGIONS__` 展开），
  保证所有节点在分组层都可达。
- 修复 socks/http 协议节点缺失 sing-box 出站构造、导致订阅编译时同批节点被
  连坐丢弃的问题：sing-box 出站补齐 socks/http 类型，用户名与密码均为用户
  UUID（与 xray 侧 accounts 一致）。
- 修复中转链 forward 跳流量未计入遥测统计的问题：agent 聚合 stats 时使用的
  前缀与实际 `chainfwd_<hop>` inbound tag 不符，现按实际 tag 前缀解析入账。
- `latx update` 覆盖面板二进制前自动备份 `.bak`；更新后版本探测、就绪探测
  或启动失败时自动回滚并重启旧版本。
- 面板 startup/faulted 状态门控补全：共享入口维护、xray 维护与服务器测试等
  业务命令在面板非 active 期间统一拒投，不再漏过门控下发。

### Security

- release 构建拒绝以公开已知的默认密码 `lattix-admin` 启动，必须显式设置
  `-admin-pass`/`LATTIX_ADMIN_PASS`（dev 构建与 e2e 不受限）。
- 请求日志对 `/api/sub/{token}/...` 路径脱敏，订阅 token 不再明文落盘。
- 订阅落地页 subURL/linksURL 经 HTML 转义（防 Host 投毒 XSS）。
- 订阅 token 熵由 64 bit 提升到 128 bit。
- isSecure 与订阅 base 仅信任回环反代的 `X-Forwarded-Proto` 声明。
- 面板下载器新增流式大小上限（默认 512 MiB，超限中止并清理部分文件）。
- 安装链路完整性：Release 资产新增仓库原样的 `install-panel.sh` /
  `install-agent.sh` / `latx.sh` / `latx-ag.sh`，sha256 一并纳入
  `checksums.txt`；根 `install.sh` 加载子安装器改为优先取 Release 资产
  （回退 Git tag 原始文件），执行前按该版本 `checksums.txt` 校验 sha256，
  不匹配即中止；旧版本 Release 无脚本条目时打印明显警告后继续。
- 会话签名密钥改为独立随机生成并持久化（设置项 `session_secret`），不再
  派生自管理员密码哈希：**升级到本版本后现有登录会话失效一次**，需重新
  登录；此后改密经轮换该密钥使全部会话失效。
- 登录限流在 per-IP 桶之外增加 per-username 兜底桶，防止伪造
  `X-Forwarded-For` 绕过 per-IP 限流进行密码爆破。
- 面板出向 HTTP 新增 SSRF 拨号防护：外部订阅拉取、CDN 节点目录、告警
  webhook 与订阅模板刷新等生产客户端拒绝向回环/内网/保留地址段拨号
  （e2e 可经 `LATX_ALLOW_PRIVATE_OUTBOUND` 放行）。
- 新增全站安全响应头：`X-Content-Type-Options: nosniff`、
  `X-Frame-Options: DENY`、`Referrer-Policy: strict-origin-when-cross-origin`、
  CSP `frame-ancestors 'none'`。
- 订阅落地页客户端下载对新建任务按订阅 token 限流（每 token 每小时最多
  10 次），超限返回 HTTP 429 与 `Retry-After`；去重命中、票据签发与文件
  下载（含 Range 断点续传）不受限。
- 面板地址为明文 http 公网链路时，服务器创建/凭证轮换响应标记
  `install_insecure: true`，安装命令对话框同步展示明文传输警告（Agent
  控制流量可被窃听或篡改，跨公网部署建议改用 https 反向代理）。
- Agent bootstrap token 改经 `LATTIX_TOKEN` 环境变量注入，不再出现于进程
  命令行参数。
- 服务器测试的 ip.sh 脚本钉定版本，下载与缓存强制 SHA-256 校验。
- 匿名订阅端点（info/clients/status/history 等）内部错误回显收敛为通用
  文案，不再泄露内部错误细节。
- Agent 原子落盘/文件复制在写后补 `chmod`，收紧遗留临时文件权限。

## [0.1.1] - 2026-08-31

### Added

- 新增可切换的 Signal Rack 主题：仪表盘以已有链路为入口，展示所选链路的服务器拓扑。
- 链路列表新增名称、节点与地区搜索、需关注筛选，以及方向键和 Home/End 导航。
- 拓扑新增可见节点范围、前后节点与返回入口控件，适配窄屏横向浏览。

### Changed

- 节点卡片使用实色面板提高辨识度，节点通过 OUT → IN 插线板线缆连接；
  连通信号沿实际线缆运动，故障和待部署状态采用静态标识。
- 新增链路选中框过渡、节点切换提示和真实读数滚动反馈；信号支持暂停，
  动效遵循减少动态效果偏好，并在页面隐藏时停止。
- 修正导航栏账户区的布局溢出，并统一新增主题代码的格式检查。

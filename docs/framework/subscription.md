# 订阅

> 拆自[框架设计索引](../framework-design.md)，对应原 §9。模板、规则缓存与原子发布契约见[订阅分流与模板](../subscription-routing-design.md)。

`GET /sub/{sub_token}` 按 User-Agent 或 `format` 返回 Mihomo、sing-box、Quantumult X 节点、Quantumult X 完整配置或 base64 分享链接。请求 `Accept` 含 `text/html`（浏览器）时返回订阅落地页。生成、模板缓存、规则制品和原子发布的完整约定见 [订阅分流与模板](../subscription-routing-design.md)。

- 目标客户端：Mihomo 内核系、sing-box 和 Quantumult X。原版 Clash 不支持 VLESS+Reality，不在目标范围。
- 内容：Lattix 节点、用户凭据和链路端点填入用户选择的分流策略；共享 Endpoint 的连接地址和端口取入口，凭据取该链 assignment 的 `access_uuid`，地区归属取出口。Mihomo、sing-box 与 Quantumult X 使用各自结构化渲染器，不做订阅格式互转；独立节点仍使用 `users.uuid`。
- 链路命名在创建时解析并固化；服务器资料或自动端口后续变化不自动重命名。默认模板：
  直连 `{{COUNTRY_FLAG}}{{LOCATION}}-Direct`，中转 `{{EXIT.COUNTRY_FLAG}}-Out`。
- 全局变量 `ID/NAME/COUNTRY/COUNTRY_CODE/COUNTRY_FLAG/LOCATION/ADDRESS/TAG[n]` 取客户端实际连接的服务器：
  直连为唯一服务器，中转为入口；另有 `PROTOCOL/PORT/HOPS`。`PORT` 选择自动端口时解析为 `auto`。
  `PANEL_SHORT` 解析为面板缩写（设置页 `panel_short`，默认 `Lattix`），与服务器拓扑无关。
- 拓扑对象 `ENTRY/EXIT/HOP[n]` 代表服务器，支持属性
  `.ID/.NAME/.COUNTRY/.COUNTRY_CODE/.COUNTRY_FLAG/.LOCATION/.ADDRESS/.TAG[n]`；
  `HOP[0] == ENTRY`，`HOP[最后] == EXIT`，数组索引从 0 开始。直连中三者均指唯一服务器。
- 前端输入 `{{` 后提供光标感知的变量补全；前端即时预览，后端执行同一套最终校验。
  未知变量/属性、非法或越界索引、缺失服务器资料、空结果和超长结果均拒绝创建并定位变量。
  名称中使用自动 `PORT` 时仅提示其会解析为 `auto`，不阻止创建。
- **（链路场景，见[代理链与 NAT](chain-nat.md)）**：引入链后，proxies 条目的 `server`/端口取链**入口**的 address 与 public_port（非 1:1 映射时），public-key/short-id/UUID 取链**出口**；命名中的别名与端口取入口。链 degraded 不剔除入口条目，靠客户端测速规避。
- **跳过原因可见**：发布时收集已分配但未纳入订阅的链的原因（未发布有效修订 / 条目构造失败），持久化在订阅快照 `warnings`（schema v10 新增列），经预览与"重新生成"API 返回，前端以"部分条目未纳入本次订阅"提示，不再静默丢弃。
- **mihomo 开箱即用**：`clash` 格式内置 fake-ip DNS（`enhanced-mode: fake-ip`、198.18.0.1/16、本地域名 filter 与默认/DoH nameserver；节点域名经 `proxy-server-nameserver` 用国内可达解析器直查，不设境外回退，避免节点测速超时）；策略含 GEOSITE/GEOIP 规则时另输出 `geodata-mode` + `geo-auto-update` 与 `geox-url`（MetaCubeX/meta-rules-dat），规则在客户端直接生效。
- **响应头**：`/sub/{token}` 的所有文件格式返回 `subscription-userinfo`，包含 upload、download 以及可选 total、expire、reset_day、plan_name、app_url；同时返回 `profile-update-interval`。
- **用户有效期**：创建或更新用户可带 `expires_at`；过去时间返回 `HTTP 200 + INVALID_ARGUMENT`。`POST /api/user/update` 可修改或清除（null = 长期；省略字段保持不变）。列表 DTO 带 `expires_at`/`expired`/`disabled`。backend sweeper（1 分钟周期，`LATTIX_EXPIRY_SWEEP_INTERVAL` 可覆盖）：`expires_at` 已过且 `expired=0` → 置 1 → 对其已分配节点所在服务器扇出 `user.remove`（显式 nodes 载荷；已 disabled 的用户只补记标记不重复扇出）；管理员延长/清除有效期（expired 1→0）→ 扇出 `user.add` 恢复（disabled 用户除外，见[用户与分组](users-and-groups.md)有效停权态）。过期用户订阅内容请求返回 403（见下方订阅更新拦截），浏览器状态页与 info API 仍可见；`node.apply` 的 `NodeUserUUIDs` 不下发 expired/disabled/quota_exhausted 或未生效用户。
- **用户生命周期（试用/生效时刻/流量配额）**：`users` 另有 `effective_at`
  （NULL=创建即生效）、`started_at`、`is_trial` 与 `quota_exhausted`。生命周期扫描
  （同一 `user.expiry` 任务）按序处理四段：
  ① **试用到期删除**——`is_trial=1` 且 `expires_at <= now`（含旧版本已置 expired=1
  的存量行）：删除前重读用户避免与管理员延长有效期并发误删，随后扇出 remove_user、
  reconcile 分组/共享端点凭证，并删除用户记录与订阅快照（`DeleteUser` 级联），写操作
  日志 `user.trial_expired_deleted`；② **激活**——`effective_at` 已越过后且 `started_at`
  为空：置位标记（幂等去重），未停权者扇出 `user.add` 并重发布订阅（已
  disabled/expired/quota_exhausted/同轮将到期者只补标记）；③ **普通用户到期停权**——
  置 expired 并按既有逻辑扇出 remove_user；④ **流量配额**——所有设置 `traffic_limit>0`
  的用户（试用与普通一致）当期用量（`traffic` 表 node_id=0 行，与订阅 userinfo 同口径）
  达到配额 → 置 `quota_exhausted=1` 停权（与 `expired` 正交：周期重置后可区分停权原因），
  扇出 remove_user + 重发布，与到期同一停权路径；**流量用尽只停用，不删除账号**。
  **未生效、到期与配额停权均为有效停权态**（见[用户与分组](users-and-groups.md)）：`NodeUserUUIDs`、共享端点用户查询
  （`ActiveEndpointAssignments` / `ActiveServiceEndpointUsers` 含分组派生分支）按
  `effective_at`/`quota_exhausted` 过滤——面板离线跨过边界后由 apply 全量下发自愈。
  **订阅更新拦截**：`/sub/{token}` 对已到期（含 `expires_at` 刚越过来不及等 sweeper，
  无状态判定，精确到秒）、配额停权或未生效用户拒绝内容（403 `subscription expired` /
  `subscription quota exhausted` / `subscription not yet effective`），客户端无法再经
  旧链接更新；浏览器状态页与 `/api/sub/{token}/info`（含 `effective_at`/`not_effective`/
  `quota_exhausted`）保持可见。client-download 端点同样拒绝。**创建用户**支持试用参数：
  `is_trial`、`effective_at`（RFC3339，省略=立即；过去时刻视作立即）、`duration_days`
  （与 `expires_at` 二选一，自生效时刻起算天数×24h）与 `user_group_id`（创建时直选用户
  分组，一用户一组）。`is_trial=true` 时**必须**用 `duration_days` 或 `expires_at`
  指定有效期（否则 INVALID_ARGUMENT），到期由生命周期扫描自动删除；未生效用户创建时
  的节点/链分配照常入库但不扇出。
  **配额恢复两条路**：① **周期自动恢复**——`traffic.reset` 任务按各用户
  `traffic_reset_day` 边界归档清零当期流量，配额停权随之解除并扇出 `user.add` 恢复
  （多重停权仅解除配额侧，到期/停用仍需管理员处理）；② **手动重置**——
  `POST /api/user/reset-traffic`（用户列表「流量重置」入口，确认弹窗）：归档清零当期
  用量（`traffic_history` 留痕），配额停权立即解除并恢复；未停权用户仅清零用量并重发布
  （userinfo 头更新）。
  **重新生效**：普通用户延长/清除有效期复位 `expired`（既有逻辑）；用户列表对到期
  普通用户提供「重新激活」入口——弹窗重设流量（默认沿用原配额）与有效期，走
  `/api/user/sub-settings` 触发恢复，订阅链接不变、客户端无感续用；普通用户仅复位
  `expired`、配额停权独立恢复。试用用户到期即删除、无法重新激活，如需续期必须在到期前
  于订阅设置延长有效期（扫描删除前被续期时仍归档清零当期流量并解除配额停权，即全新
  试用期，`ArchiveUserTraffic`）。生效时刻可在订阅设置中编辑（`effective_at` 省略=不变/
  null=立即），推迟即转未生效态并扇出摘除。
- 分享链接集合已实现：`GET /sub/{token}?format=links`（仅含分配的 active 节点）。
- **落地页客户端下载**：面板代取 GitHub Release 客户端安装包并校验发布方 SHA-256
  （缓存 72 小时、流式上限 512 MiB，校验失败即失败并删件）；落地页经
  `/api/sub/{token}/client-download/{start,status,ticket,file}` 四端点获取 3 小时
  会话票据直链，交给浏览器原生下载（`http.ServeFile` 天然支持 Range 断点续传）。
  票据与订阅 token、任务双向绑定，过期或跨订阅使用返回 403；状态端点回显校验值
  与来源供用户独立核验。新建下载任务按订阅 token 滑动窗口限流（每 token 10 次/小时，
  超限 429 + Retry-After；去重命中、票据签发与 Range 续传不计数，见[安全](security.md)）。
- **外部订阅（导入 + 用户关联）**：管理页导入第三方订阅 URL，节点与订阅信息独立落库
  （`external_chains` / `external_subscriptions`，不进入 `chains` 状态机与流量统计），
  用户以「一个外部订阅」为单位引入订阅输出。详见下方小节。

## 外部订阅导入与用户关联

- **导入**：支持三种格式（base64 分享链接、Clash/mihomo YAML `proxies:`、v2rayN 自定义
  base64 JSON 条目）与 12 种协议（vless/vmess/ss/ssr/trojan/hysteria2/tuic/wireguard/
  anytls/snell/socks/http），统一解析为标准化 JSON 存入 `external_chains`
  （`config_sha256` 去重）；订阅记录 `external_subscriptions` 保存
  `subscription-userinfo` 流量（upload/download/total/expire，浮点取整、expire=0 忽略）、
  节点数、识别到的格式与最近同步状态。
- **拉取安全**：URL 仅允许 https，拒绝 localhost / 内网 / CGNAT（100.64/10）/
  TEST-NET 等保留段地址（防 SSRF）；每订阅可配自定义 UA（默认 clash-meta/2.4.0，
  未拿到流量头时自动以 clash UA 重试一次）与跳过证书校验开关；30s 超时、2 MiB 上限。
- **同步**：创建即同步、手动按钮与定时任务三入口；每订阅可配自动同步开关与更新间隔
  （`UpdateIntervalHours`，最小 1 小时、默认 24 小时），调度任务
  `external_subscriptions.sync` 每 15 分钟扫描到期订阅；重同步为事务内全量替换节点
  （事务内校验订阅仍存在，防删除竞态）；失败保留记录并写 `last_error`，可重试。
  同步成功的订阅自动重发布所有关联用户。前端外部订阅页提供列表、增删改、同步与节点查看。
- **用户关联**：`user_external_subscriptions` 表（user × subscription + 模式），三模式：
  **叠加（stack）** total/used 都并入面板池、**并入（merge）** 仅 used 并入（total 不变）、
  **附加（nodes）** 仅引入节点（流量单独展示）；`total=0` 视为额度未知不参与合并。
  合并为读取时实时计算（`extsub.MergeUserTraffic`，纯函数），不物化缓存表；
  `subscription-userinfo` 头与 `/api/sub/{token}/info` 均按合并值发出（头不封底，
  仅前端剩余显示 0 封底；`reset_day` 不合并）。
- **渲染**：订阅快照管线扩展 `proxyItem.external`，为外部节点走独立构建器
  （mihomo YAML / sing-box JSON / Quantumult X / base64 分享链接，与解析器互逆）；
  客户端格式无法表达的协议跳过并记入快照 warning。外部订阅同步成功后自动重发布
  所有关联用户（`UsersByExternalSubscriptionID` + 异步重生成队列）。

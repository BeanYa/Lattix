> **归档说明**：本文是设计分稿，已并入 [../ui-refactor-proposal.md](../ui-refactor-proposal.md) v1.0（口径见该文 13.3）。整套提案已于 2026-10-29 归档：视觉与令牌口径被「液态玻璃」（根 DESIGN.md）取代，本文不再维护。

## 4. 信息架构与导航

> 承接第 0 节决策 3–4 与第 2 节的工作模式分层。信息架构只解决三件事：**对象在哪、异常先看谁、链接能否复现**。现有 11 个平铺入口见 `src/frontend/src/components/Layout.tsx:30-66`；以下导航替换它，但保留全部业务能力。

### 4.1 组织原则

1. **对象优先，工具退后。** 一级入口按「值班 → 网络 → 访问 → 运行 → 设置」排列；服务器、链路、用户是对象，成本、日志、模板是工具或配置。
2. **最多两级导航。** 一级入口固定在左侧栏；二级入口只显示当前域的页面；对象详情用面包屑和页内分区承载，不再增加第三级侧栏。
3. **URL 即状态。** 筛选、选中、分页、页内视图全部可分享、可刷新；列表查询变化使用 `replace`，进入对象详情使用 `push`。
4. **高风险动作有固定落点。** 删除、强制发布等确认沿用 `useAppDialog`；长操作进入操作托盘，不再用全屏遮罩阻断浏览（现实现见 `src/frontend/src/components/OperationProgress.tsx:31-135`）。

### 4.2 一级入口与二级导航

| 一级入口 | 路由前缀 | 回答的问题 | 二级导航（默认项加粗） | 现有页面证据 |
|---|---|---|---|---|
| **值班板** | `/board` | 现在哪里坏了、影响谁、先做什么 | 无（单页；队列展开用 `?queue=all`） | `src/frontend/src/pages/Dashboard.tsx:351-852` |
| **网络** | `/network` | 机群和线路现在是什么形态 | **服务器**、链路、链路分组 | `src/frontend/src/pages/Servers.tsx:465-495`、`src/frontend/src/pages/Chains.tsx:302-603`、`src/frontend/src/pages/Groups.tsx:81-91` |
| **访问** | `/access` | 谁在用、拿到什么订阅、模板怎么发 | **用户**、用户分组、订阅模板、外部订阅 | `src/frontend/src/pages/Users.tsx:295-317`、`src/frontend/src/pages/Groups.tsx:81-91`、`src/frontend/src/pages/SubscriptionTemplates.tsx:167-189`、`src/frontend/src/pages/ExternalSubscriptions.tsx:237-251` |
| **运行** | `/run` | 面板/Agent 是否健康、钱花在哪、发生过什么 | **运行监控**、成本、日志 | `src/frontend/src/pages/RuntimeMonitor.tsx:714-732`、`src/frontend/src/pages/Costs.tsx:480-495`、`src/frontend/src/pages/LogsLayout.tsx:9-26` |
| **设置** | `/settings` | 全局策略、证书、告警、维护 | **Agent**、运行设置、安全通知、系统维护 | `src/frontend/src/pages/Settings.tsx:79-83` |

- 域默认页：`/network` → `/network/servers`；`/access` → `/access/users`；`/run` → `/run/console`；`/settings` → `/settings/agent`，均为 `replace` 重定向。
- 二级导航只渲染当前域；切换一级入口时默认落到该域默认页。侧栏不显示对象数量（除异常徽标），避免把导航变成统计面板。
- 旧「分组」页的两个 tab 被拆开：链路分组归网络，用户分组归访问。旧页默认链分组 tab（`src/frontend/src/pages/Groups.tsx:81-91`），因此 `/groups` 只重定向到 `/network/link-groups`。

### 4.3 路由映射（wouter）

现有路由集中在 `src/frontend/src/App.tsx:78-147`，公开页在 `src/frontend/src/App.tsx:163-176`。新表保留 `Switch/Route/Redirect`，不引入 React Router。

| 现路由 | 新路由 | 处理 | 说明 |
|---|---|---|---|
| `/` | `/board` | `Redirect replace` | 登录后也直接去 `/board`（现 `src/frontend/src/pages/Login.tsx:28-29` 去 `/`） |
| `/servers` | `/network/servers` | `Redirect replace` | 保留 query |
| `/chains` | `/network/chains` | `Redirect replace` | 直连/中转统一列表 |
| `/groups` | `/network/link-groups` | `Redirect replace` | 旧页默认链分组；用户分组见下行 |
| （新） | `/access/user-groups` | 新路由 | 从旧「分组 / 用户分组」拆出，归入访问域 |
| `/users` | `/access/users` | `Redirect replace` | 模板指派用 `?view=assign` |
| `/subscription-templates` | `/access/templates` | `Redirect replace` | 页面标题仍为「订阅模板」 |
| `/external-subscriptions` | `/access/external-subscriptions` | `Redirect replace` | — |
| `/runtime` | `/run/console` | `Redirect replace` | `?tab=panel\|agents` |
| `/costs` | `/run/costs` | `Redirect replace` | `?tab=actual\|estimated` |
| `/logs`、`/logs/operations` | `/run/logs/operations` | `Redirect replace` | 保留 query（如 `severity=error`） |
| `/logs/requests` | `/run/logs/requests` | `Redirect replace` | 保留 query |
| `/settings` | `/settings/agent` | `Redirect replace` | 四个 section 见下 |
| （新） | `/settings/:section` | 规范路由 | `agent\|runtime\|security\|system` |
| `/login` | `/login` | 不变 | 支持 `?next=` |
| `/sub/:token` | `/sub/:token` | 不变 | 独立公开页，不套后台框架 |
| 未匹配路径 | 404 页 | 原地渲染 | 不再静默重定向到首页（现 `src/frontend/src/App.tsx:145-147`） |

新增对象路由：

| 路由 | 对象 | 页面形态 |
|---|---|---|
| `/network/chains/new` | 新建 Chain | 专注模式三步编排器 |
| `/network/chains/:id` | Chain（1–4 跳） | 独立详情页 |
| `/network/chains/:id/edit` | 编辑 Chain | 专注模式编排器，`?step=topology\|protocol\|impact` |
| `/network/nodes/:id` | 独立 XrayNode（存量对象） | 独立详情页；无编辑 API，只提供重试/删除 |
| `/board` | 值班板 | 单页 |
| `/network/link-groups`、`/access/user-groups` | 两类分组 | 列表 + 对话框编辑 |

- **wouter 顺序**：`new` 必须在 `:id` 之前；`:id/edit` 必须在 `:id` 之前；`/network/nodes/:id` 独立于 chains，避免 Chain 与 Node 的 id 空间冲突（现列表把两者混排，见 `src/frontend/src/pages/Chains.tsx:285-300`）。
- **重定向保留 query**：`Redirect` 只接收字符串，需在重定向组件中拼接 `window.location.search`；日志页的 `severity` 现有逻辑见 `src/frontend/src/pages/OperationLogs.tsx:147-153`、`src/frontend/src/lib/log-severity-param.ts:13-23`。
- **动态 id 校验**：`:id` 必须是正整数；非法值直接渲染 404，不请求接口。
- **域内简写**：`/run/logs` 重定向到 `/run/logs/operations`，与旧 `/logs` 行为一致。

### 4.4 全局框架

| 区域 | ≥1440 | 1280–1439 | 1024–1279 | <1024 |
|---|---|---|---|---|
| 顶栏 | 56 | 56 | 56 | 48 |
| 左侧栏 | 224 | 224 | 72（仅图标） | 隐藏，抽屉 280 |
| 内容内边距 | 24 | 24 | 20 | 16 |
| 列表 + 检查器 | 列表自适应 + 400 | 列表 + 376 | 检查器改为 480 浮层 | 检查器全屏页 |
| 操作托盘 | 360，右下 24 | 360 | 360 | 底部全宽，安全区 |

**顶栏（56px）**，从左到右：Lattix 标识（链接 `/board`）→ 面板状态芯片 → 命令面板触发器 → 右侧「N 项待处理」→ 外观切换 → 账户菜单。面板状态沿用 `/api/panel/state` 的 5 秒轮询与 `ACTIVE/STARTUP/UPDATING/FAULTED` 文案（`src/frontend/src/components/Layout.tsx:68-86,132-148`）；「待处理」数字由值班板同一套派生逻辑提供，不是新接口。日期从顶栏移除，放入值班板标题行。

**左侧栏（224px）**：5 个一级项，每项 44px 高、20px 线性图标、14px/600 标签；激活态为 8% 信号蓝底 + 2px 左侧信号蓝标记 + ink 文字，不使用线路色。二级项 36px 高、13px/500、左缩进 32px；当前域展开，其他域不显示二级。栏底显示面板版本与「自托管」说明，不放假入口。≥1024 时侧栏常驻；<1024 由顶栏菜单按钮打开抽屉，焦点进入抽屉并可用 Esc 关闭（现有 Sheet 行为见 `src/frontend/src/components/Layout.tsx:226-269`）。

**命令面板入口**：顶栏触发器 320×36，文案「搜索或跳转…」，右侧 `⌘K` / `Ctrl K`；<1024 只留 36×36 搜索图标。面板 640×min(560, 70vh)，居中，z-index 70；分组为「最近」「跳转」「对象」「操作」，每组最多 5 条；对象索引只覆盖服务器、链路、用户（复用 `api.servers/api.chains/api.users`，见 `src/frontend/src/lib/api.ts:110,255,284`；首次打开拉取并缓存 30 秒），不检索日志正文、订阅正文或 xray 配置。支持 ↑↓、Enter、Esc、焦点陷阱与 `aria-activedescendant`；无结果时给出「没有匹配的对象」和「查看全部服务器」两个出口。不新增全局搜索后端。

**操作托盘挂载点**：在 `AppShell` 内、`<main>` 之后的固定层，单实例，唯一消费者是现有 `OperationProgressProvider`（`src/frontend/src/components/OperationProgressProvider.tsx:8-59`）。右 24 / 下 24，折叠 360×48，展开 420×min(60vh, 480)，z-index 40；不设遮罩、不抢焦点、不阻止页面滚动。标题行显示图标、操作名、当前阶段与百分比；展开显示阶段列表、警告、错误与「查看日志」。终态保留到用户关闭；`lost` 显示现有文案「进度已丢失，操作可能仍在后台继续」。**同一时间只跟踪一个 observeId**，与现有单任务状态机一致（`src/frontend/src/lib/operation-progress-state.ts:5-49`）；新操作到达时若当前仍在运行，动作按钮应禁用并提示「另一个操作正在进行」，不设计多任务队列。面板自更新仍使用全屏 `UpdateOverlay`，因为面板会重启（`src/frontend/src/components/UpdateOverlay.tsx:18-20`）；业务操作不再全屏。

**主内容**：外壳 `height:100dvh; overflow:hidden`，主列内部滚动；内容最大 1600px，居中。每页设置 `document.title`；首个 Tab 焦点是「跳到主内容」链接，目标为现有 `#main-content`（`src/frontend/src/components/Layout.tsx:287`）。

### 4.5 URL 状态

统一语法：`/{域}/{资源}?{筛选}&{排序}&{分页}&{选中}`。默认值一律省略；参数顺序固定，便于比较与分享。

| 页面 | 筛选参数 | 选中 / 视图 | 分页 / 排序 |
|---|---|---|---|
| 值班板 `/board` | `queue=all\|critical\|warning`、`since=12h\|24h` | 无 | 无（队列最多 8 行，展开内联） |
| 服务器 `/network/servers` | `q`、`state`、`region`、`tag`、`drift=1` | `sel=<id>` | `page`、`pageSize`、`sort=name\|state\|cpu\|latency\|created` |
| 链路 `/network/chains` | `q`、`state`、`type=relay\|direct\|node`、`server=<id>` | `sel=<id>`（返回时高亮） | `page`、`pageSize`、`sort=severity\|name\|updated` |
| 用户 `/access/users` | `q`、`state`、`chain=<id>`、`group=<id>` | `sel=<id>`、`view=list\|assign` | `page`、`pageSize`、`sort=created\|name\|expiry` |
| 日志 `/run/logs/*` | `severity`、`category`、`server`、`q`、`from`、`to`（请求日志另有 `method`、`window`） | `entry=<id\|request_id>` | `page`、`pageSize` |

规则：

1. **列表查询一律 `replace`**：搜索、筛选、排序、翻页、选中都不新增历史条目；浏览器后退离开列表页，不会逐行回放。进入 Chain/Node 详情用 `push`，后退可回到带原筛选的列表。
2. **搜索**：输入 250ms 防抖；trim 后最多 80 字符；空值移除参数。`/` 聚焦搜索框，仅在非输入控件内生效。
3. **数组**：逗号分隔，按枚举固定顺序输出；非法值丢弃并 `replace` 重写 URL，保留其余合法参数，不整页重置。
4. **选中**：`sel` 始终是单个整数；对象不存在时移除 `sel`、保留筛选，并在列表头显示「对象不存在或已删除」的 `Notice`。
5. **分页**：1 基；第 1 页省略 `page`；`pageSize` 允许 25/50/100，默认 50，省略默认值。筛选变化重置到第 1 页；页码越界时钳制到末页并 `replace`。日志用服务端 `offset/limit/total`（`src/frontend/src/pages/OperationLogs.tsx:118-138`）；其余列表当前接口一次返回数组（`src/frontend/src/pages/Chains.tsx:132-136`），在 A1 规模内做客户端分页，超过 A1 时再列 P3 服务端分页。
6. **排序**：默认值不写入 URL。服务器默认 `name asc`；链路默认 `severity desc`；用户默认 `created desc`；日志固定时间倒序。用户显式排序后才写 `sort`。
7. **不应放进 URL 的状态**：表格密度、刷新间隔、最近命令面板记录可放 `localStorage`；现有日志页已用 `lattix.logs.*` 存分页与刷新（`src/frontend/src/pages/OperationLogs.tsx:107-116`）。筛选不写入 localStorage，避免分享链接与本地状态不一致。

### 4.6 面包屑与返回

- 面包屑仅在**选中对象、设置分区、第三级路由**出现，例如「网络 / 链路 / HK-JP-US」「网络 / 链路 / HK-JP-US / 编辑拓扑」「服务器 / HK-01」「设置 / 安全通知」「运行 / 日志 / 操作日志」。无选中的一级/二级列表页不显示，避免「网络 / 服务器」这种与侧栏重复的双标题。
- 结构：32px 高、12px 字号；父级为链接（`ink-2`），当前项 `aria-current="page"`、ink 色；分隔符为 12px chevron，`aria-hidden`；最多 4 级。
- **返回键行为**：对象详情页的「返回」优先 `history.back()`，回到原列表 URL（含筛选与 `sel` 高亮）；直接打开深链接、没有同源来源时，`replace` 到该资源的规范列表。面包屑父级使用 `push`；若同一会话存在该资源的上次列表 URL，则恢复它，否则去规范列表。
- **检查器关闭**：Esc 与关闭按钮等价，用 `replace` 移除 `sel` 并保留筛选，焦点回到触发行；浏览器后退不关闭检查器，而是离开列表页（选中不写历史）。对象详情页 Esc 不关闭页面，只有编排器/检查器/命令面板响应 Esc。
- **编排器返回**：有未提交修改时弹出「继续编辑 / 放弃修改」；放弃才允许返回。浏览器后退与关闭标签页同样拦截（内部导航用应用对话框，关闭标签页用 `beforeunload`）。
- **登录返回**：`/login?next=<当前 path+query>`；登录成功后仅接受以 `/` 开头且不以 `/login` 开头的路径，否则去 `/board`。

### 4.7 空态、错误态与权限外

单管理员产品没有角色权限（`PRODUCT.md:11`），因此不存在「某个按钮无权限」；所谓权限外只有**未认证、会话过期、面板不可用**三类。

| 场景 | 呈现 | 主动作 |
|---|---|---|
| 未登录 / 401 | 跳 `/login?next=…`；登录页顶部说明「登录已过期，请重新登录」 | 登录后回原页 |
| 面板更新中 | 顶栏 `UPDATING`；全屏 `UpdateOverlay` 阻断操作，阶段与重启等待沿用现有实现 | 等待自动刷新 |
| 面板故障 | 顶栏 `FAULTED` + 主内容顶部横幅「面板状态异常：{fault}」；数据标注上次同步时间 | 打开「设置 / 系统维护」 |
| 接口失败 | 页级 `Notice tone="danger"`，显示 `RequestError.message`，可展开 request_id / trace_id；保留已加载数据 | 重试 |
| 无服务器（值班板/网络） | 值班板显示三步接入清单：接入服务器 → 创建链路 → 创建用户；网络服务器页空态 | 添加服务器 |
| 无链路 | 链路页空态；若服务器为 0，创建按钮禁用并说明「需要至少 1 台服务器作为出口」 | 添加服务器 / 创建第一条链路 |
| 无用户 | 用户页空态；说明「没有已发布链路时仍可创建用户，但订阅为空」 | 创建用户 |
| 筛选无结果 | 列表内空态，保留筛选条件与「清除筛选」；不显示首次接入引导 | 清除筛选 |
| 未选择对象 | 检查器空态：对象类型图标 + 「从左侧选择…」+ ↑↓ 快捷键提示 + 当前域 3 个统计值 | 无 |
| 对象不存在/已删除 | 列表内 `Notice` 并移除 `sel`；详情页显示「链路不存在或已删除」+ 返回列表 + 「查看已删除」筛选 | 返回列表 |
| 直连节点被删除 | 详情页只读终止态，不提供编辑；说明独立节点已无编辑 API | 返回列表 |
| Agent 离线导致操作不可用 | 按钮 `disabled` + 原因文案（如「Agent 离线，测试将在重连后执行」），不弹权限提示 | 打开服务器 |

---

## 5. 关键页面设计

> 本节只定义**页面骨架、优先级、状态与交互边界**；颜色/字体/阴影的最终值由第 6 节视觉系统给出。所有页面复用现有 `requester`、`usePolling`、`OperationProgressProvider` 与 `PagePrimitives`，不新增请求库或状态库。

### 5.1 值班板、网络域（服务器 / 链路）

#### 5.1.1 值班板 `/board`

**目标**：30 秒内回答「哪里坏了、影响谁、先做什么」。它不是仪表盘拼贴；现有 8 模块布局（`src/frontend/src/pages/Dashboard.tsx:351-852`）被替换为「队列 + 脉搏 + 变更」三段。

**骨架（1440×900）**

| 区域 | 尺寸 / 位置 | 首屏优先级 | 内容 |
|---|---|---|---|
| 页头 | 64px，内容顶部 | P1 | 「值班板」+ 日期 + 最后同步 + 刷新；右侧「N 项待处理」 |
| 待处理队列 | 776×624，左上 | **P0** | 最多 8 行，每行 56px；行内展开「为什么 / 影响 / 动作」 |
| 机群脉搏 | 360×624，右上 | P1 | 24h 吞吐折线（`/api/stats/traffic/series`）、在线/总数、漂移数、同步时间 |
| 最近变更 | 1160×自适应，下方 | P2 | 过去 12h 操作日志（`/api/log/list-operations?limit=20`），按对象分组 |
| 快捷操作 | 仅空态出现 | P3 | 添加服务器 / 创建链路 / 创建用户 |

首屏在 900px 高度内必须完整显示队列前 8 行与整个脉搏模块；「最近变更」允许在首屏下缘被截断，但不得把队列推到折叠线以下。

**队列行规格**：56px 高；列宽 32（类型图标）/ minmax(220px,1.4fr)（对象）/ 1.2fr（原因）/ 120（影响）/ 96（持续时长）/ 自适应（动作）。对象列显示名称 + 类型标签（服务器/链路/节点），原因列最多 2 行、超出省略并在展开区全文显示；影响列显示「42 位用户」，无法取得时显示「影响面未知」而不是 0。动作列最多一个主按钮 + 一个「更多」菜单。

**排序与分级**：

- **P0（故障）**：服务器 `offline/auth_rejected`，链路 `failed/active_failed/invalid`。
- **P1（降级/待处理）**：链路 `degraded/waiting_for_agent/cleanup_pending`，服务器 `config_drift/agent_settings_status=failed`。
- **P2（进行中）**：链路 `pending/applying`、节点 `pending/applying`、`active_unconfirmed`。
- 先按 P0→P1→P2，再按影响用户数降序，再按持续时长降序；同分按对象名。进行中的链路不进入 P0，即使它尚未生效。

**数据派生**：复用现有页面已经做的「服务器 + 链路 + dashboard」三请求（`src/frontend/src/pages/Dashboard.tsx:171-180`），事件派生逻辑参考 `src/frontend/src/pages/Dashboard.tsx:254-283`。影响面需要对 `/api/user/list` 做客户端 join：按 `chain_ids/effective_chain_ids/node_ids` 找到直接分配、分组派生与生效用户，按 user.id 去重；若用户列表请求失败，整列显示「影响面未知」，不得显示 0。A1 规模（100–5,000 用户）内可接受；超出时列为 P3 服务端聚合。

**空态**：无待处理事项时，队列区显示状态绿点 + 「当前无待处理事项」+ 最后检查时间 + 「查看操作日志」；不显示空白卡片。无服务器时，队列区替换为三步接入清单，主按钮「添加服务器」，次按钮「了解链路」。

**刷新**：10 秒静默轮询；页面隐藏时暂停，回到前台立即刷新一次。复用 `usePolling`（默认 5 秒，见 `src/frontend/src/lib/use-polling.ts:7-27`），为值班板传 10,000ms 并加 `visibilitychange` 守卫；不要另写 `setInterval`。

#### 5.1.2 服务器 `/network/servers`

**布局**：列表 + 检查器。≥1440：列表自适应 + 400 检查器 + 24 间距（内容 1160 → 列表 736）；1280–1439：检查器 376；1024–1279：检查器变为 480 右浮层；<1024：列表全宽，选中后进入全屏检查器页（URL 仍为 `?sel=<id>`）。

**列表骨架**

- 页头 64px：标题「服务器」+「12 台 / 10 在线 / 1 漂移」摘要（一行文本，不做统计卡）+ 主按钮「添加服务器」。
- 筛选条 48px：搜索 320px、状态多选、地区、标签、漂移开关、密度切换（44/36）、刷新；滚动时吸顶，表头吸在筛选条下（40px）。
- 表格：表头 40px，行默认 44px（紧凑 36px）；默认列依次为 服务器（别名 + #id）、连接、地区/位置、CPU、内存、磁盘、延迟、速率↑/↓、版本、告警。地址、标签、费用、最近上报为次级列，<1440 隐藏。
- 状态列使用状态令牌 + 文案：`在线/连接中/重连中/离线/未连接/凭据被拒绝`（`src/frontend/src/lib/server-state.ts:9-24`）；`config_drift` 与 `agent_settings_status=failed` 各给一个可筛选的告警标记，不合并成「异常」一个词。
- 排序：别名、连接状态（在线优先）、CPU、延迟、创建时间；默认别名升序。空值始终显示 `--`，不显示 0。
- 首屏：1440×900 下至少显示 12 行；不把检查器内容挤进表格。

**检查器规格（400px）**

- 结构：56px 头部（对象名 + 状态 + 关闭）→ 可滚动主体 → 64px 底部操作条。检查器自身 `position: sticky`，高度 `calc(100dvh - 56px - 48px)`，主体独立滚动。
- 分区顺序：**概览**（连接、地址、Agent/Xray 版本、机器类型、标签）→ **指标**（CPU/内存/磁盘/延迟，1h 趋势用现有 metric history）→ **链路**（引用此服务器的 Chain + 独立 Node，按入口/中转/出口排序）→ **费用**（计费周期、下次续费、状态）→ **运维**（测试、升级 xray/agent、修复、清理、重建、轮换 token、删除）。
- 操作条：主操作随状态变化（离线→「修复 / 查看命令记录」，在线→「编辑」）；测试、升级、删除收进「更多」，删除为危险项。现有详情抽屉主体可直接迁移（`src/frontend/src/components/ServerMonitor.tsx:1088-1382`）。同一列表-检查器规格用于 `/access/users`，具体分区由 5.2 节细化。
- 未选中状态：不自动选中第一台（与现实现 `src/frontend/src/components/ServerMonitor.tsx:1403-1405` 不同），显示「未选择服务器」+ 当前 3 个统计值 + 「↑↓ 选择」；这样 URL 与界面一致，深链接不会伪装成用户选择。
- 键盘：列表获得焦点后 ↑↓ 改变 `sel`（`replace`），Enter 把焦点送入检查器，Esc 关闭检查器；鼠标点击行同样只改 `sel`，不改变历史栈。

**移动端**：每台服务器一张 104px 卡片（名称/状态/地区/延迟/告警），点击进入全屏检查器；操作条固定在底部，危险操作在「更多」中二次确认。

#### 5.1.3 链路列表 `/network/chains`

**对象边界（必须保留现有事实）**：链路页同时承载两类对象：Chain（1 跳 = 直连，2–4 跳 = 中转）与独立 `XrayNode`（存量对象，无 revision）。现实现把 Node 与 Chain 混排并排除作为 relay exit 的 Node（`src/frontend/src/pages/Chains.tsx:276-300`）；新列表保留这一去重规则，但用类型标签区分「直连 / 中转 / 独立节点」，独立节点组默认折叠，数量为 0 时不渲染。

**行规格**：72px；左起状态标记、名称 + #id + 类型、迷你拓扑（最多 4 格，每格 48×28；<1280 显示前 2 格 + `+N`）、影响用户数、流量（有效值，倍率≠1 时在悬浮/展开显示原始值）、发布状态（`published/desired`，仅 Chain）、操作菜单。点击整行进入详情；列表不使用检查器。`sel` 只用于从详情返回时高亮，不打开侧栏。

**首屏优先级**：页头与筛选（P0）→ 前 8 行（P0）→ 状态汇总（P1，文本形式）→ 分页（P2）。默认按严重度排序：`failed/active_failed/invalid` → `degraded` → `waiting_for_agent/cleanup_pending` → `applying/pending` → `active_unconfirmed` → `active` → `deleted`。用户选择显式排序后，异常不再强制置顶，但状态标记仍保留。

**快捷筛选**：全部 / 异常 / 部署中 / 正常；映射 `state` 参数，计数来自当前列表。搜索覆盖名称、入口服务器别名、出口服务器别名、#id；不搜索错误正文（错误在详情诊断区）。

**空态**：0 台服务器时，主按钮「添加服务器」，创建链路按钮禁用并给出原因；有服务器但 0 条链路时，主按钮「创建第一条链路」，次按钮「直连与中转的区别」（打开说明面板，不跳外部文档）。

#### 5.1.4 链路详情 `/network/chains/:id`（独立页）

**骨架（1440）**：面包屑 32 → 状态头部 96（sticky）→ 主体 1160：主列 776 + 右栏 360。状态头部包含：返回、线路色 4px 标记 + 链路名、`#id`、直连/中转标签、状态、`published/desired` revision、强制发布标记、动作组。主列：拓扑面板 320 高 → 页内 tab 44 → 内容（影响 / 当前批次 / 流量 / 诊断）；右栏：影响面、发布状态、危险区。

**动作矩阵**（只允许现有 API；不发明逐跳重试）：

| 状态 | 主操作 | 次操作 / 菜单 | 规则 |
|---|---|---|---|
| `active` | 编辑拓扑 | 流量历史、重置流量、删除 | 正常态 |
| `failed / active_failed` | 重试链路（`/api/chain/retry`） | 编辑配置、强制发布（`desired≠0`）、删除 | 失败跳置顶并标红 |
| `degraded` | 打开离线服务器（有离线跳时），否则「查看诊断」 | 重试链路（有失败跳时）、强制发布 | 横幅说明「已发布 revision 仍生效」；编辑按统一判定 |
| `waiting_for_agent` | 强制发布（`desired≠0`，危险，二次确认） | 编辑、删除 | 说明任务在队列中 |
| `applying / pending / cleanup_pending` | 无（只读「部署中」） | 刷新、查看队列 | 编辑禁用 |
| `active_unconfirmed` | 重新强制发布（`desired≠0`） | 删除 | 标记「已强制发布，配置待确认」；编辑按统一判定 |
| `invalid / deleted` | 无 | 删除（invalid）/ 返回列表 | 只读；不显示编辑 |

**统一编辑判定**：仅当 `desired_revision_id === 0` 或状态 ∈ `{failed, active_failed, waiting_for_agent}` 时可编辑；否则按钮 `disabled`，悬浮说明「当前有在途发布，先完成或失败后再编辑」。该规则直接来自现有实现（`src/frontend/src/pages/Chains.tsx:490-500`），不要在详情页另造一套。

**拓扑面板**：入口→中间（0–2）→出口，最多 4 个节点；节点卡 200×104，间距 56；连接线 2px，端口标签在卡下方；节点显示序号、服务器别名、角色（入口/中转/出口）、状态、端口、共享入口标记、Agent 在线/离线、错误摘要（2 行）。线路色取 `--line-1..6`（具体色值由第 6 节定义），按稳定键 `chain:<id>` / `node:<id>` 哈希分配，跨列表、详情、面包屑一致；线色只用于 4px 标记与 2px 连接线，文字始终 ink，状态始终带文字，不以颜色单独表达（WCAG 1.4.11 图形对比 ≥3:1）。失败节点 1px 危险色边框 + 12% 危险色底；部署中节点用虚线边框 + 旋转点，尊重 `prefers-reduced-motion`。

**右栏「影响面」**：三行——直接分配（`chain_ids/node_ids`）、分组派生（用户分组 → 链路分组）、生效合计（`effective_chain_ids`，按 user.id 去重）；前两行不直接相加，合计单独计算。每行可点，跳 `/access/users?chain=<id>`。0 用户时显示「尚未分配给用户」+ 「分配用户」链接（去用户页并带 `chain` 筛选）。用户列表不可用时显示「影响面暂不可用」+ 重试，不显示 0。

**右栏「发布状态」**：显示 `published_revision_id`、`desired_revision_id`、`revision_forced`，以及 `revision_tasks` 的当前批次任务（apply/cleanup，pending/queued/acked/failed/abandoned），按失败优先排序，最多 10 条；每条可跳到相关服务器。**注意**：现有 API 没有 revision 历史列表，只有当前任务集合，因此这里叫「当前发布批次」，不叫「revision 时间线」；历史变更通过「查看操作日志」查询。这是对第 3.2 节「revision 时间线留下记录」的收敛，不改变目标。

**流量**：24h/7d 切换，数据来自 `/api/chain/get-traffic-history`；有效值与原始值同时存在时，默认显示有效值，倍率与原始值放在图例/悬浮中（现逻辑见 `src/frontend/src/pages/Chains.tsx:75-107`）。无数据时显示「暂无采样」，不画 0 线。

**诊断 tab**：`endpoint_status/endpoint_error`、逐跳错误、清理任务、待处理队列；不显示未提供的「命令详情」或「Agent 日志」。

**移动端**：状态头部不 sticky（高度降为 80），拓扑横向滚动（节点卡 180×96），右栏在主体下方堆叠，主操作固定在底部 56px 操作条；<1024 不显示「编辑拓扑」，改为「在桌面端编辑」说明。

**独立节点 `/network/nodes/:id`**：单节点拓扑 + 状态 + 服务器 + 端口 + 流量摘要 + 引用它的用户（按 `node_ids`）；动作仅「重试节点」「删除节点」（`src/frontend/src/pages/Chains.tsx:356-374`）。没有编辑 API，也没有 revision；不要渲染空的发布区。

#### 5.1.5 拓扑编排器 `/network/chains/new`、`/network/chains/:id/edit`

**入口**：链路列表主按钮「创建链路」；详情头部「编辑拓扑」；行菜单「编辑」。编辑入口遵循 5.1.4 的统一判定，禁用时给出原因。编排器使用**专注模式**：保留 56px 顶栏，隐藏左侧栏；左步骤栏 240、右校验/影响栏 360、中间画布 ≥720；内容最大 1600。顶部有「退出编排」与链路名；退出/浏览器后退/关闭标签页在有修改时都二次确认。

**步骤 1「画线路」**（`?step=topology`）：分段控件「直连 / 中转」；直连只有 1 台服务器（同时是入口与出口），中转为入口 + 0–2 中间 + 出口。服务器通过可搜索选择器加入，节点卡 220×88，支持拖拽重排与 `Alt+↑/↓` 键盘重排。实时校验（与现有提交前校验一致，`src/frontend/src/pages/chains/use-chain-form.ts:504-530`）：

- 1–4 台服务器；同一服务器不可重复；中转必须选入口与出口；
- 入口与中间跳必须具有入站能力（独立 IP，或 NAT 且有端口段）；仅出口档 NAT 只能作出口；
- 入口端口留空 = 自动；与现有链路共享监听时提示「将共享其入口参数」（`src/frontend/src/pages/chains/use-chain-form.ts:288-303`）；
- 逐跳地址留空 = 跟随服务器默认地址；非空值必须属于该服务器地址集合（后端同样校验）。
- 画布下方固定「校验摘要」：错误逐条带「定位到节点」；有错误时「下一步」禁用。

**步骤 2「定协议」**（`?step=protocol`）：分组为「基础」（名称模板 + 变量补全/预览）、「协议与端口」（协议、端口）、「传输与安全」（network/security 矩阵、Reality/TLS/Hysteria 条件字段）、「入口协议」（仅多跳；v1 固定 vless+reality；无入口端点的存量链锁定，见 `src/frontend/src/pages/chains/use-chain-form.ts:365-366`）、「流量倍率」。协议/安全/传输的联动纠偏沿用现有矩阵（`src/frontend/src/pages/chains/use-chain-form.ts:93-105,420-476`），选项变化时即时纠正并说明原因；高级项默认折叠。错误就地显示在字段下，提交时汇总到顶部并聚焦第一个错误。

**步骤 3「看影响再发布」**（`?step=impact`）：显示服务器序列、跳数、入口端口、出口协议、将影响的用户数（编辑时按当前分配计算；新建时为 0，明确写「创建后需在访问中分配用户」）、将被跳过的离线服务器（Agent 离线仍可排队，但标注「发布将等待 Agent」）。主按钮「发布」；点击后调用现有 `createChain/editChain`，拿到 `observeId` 后编排器进入只读「发布中」状态，由操作托盘同步阶段；成功进入详情，失败留在第三步并显示错误与失败跳。

**与第 3.3 节的一处修正**：现有 `/api/chain/create` 与 `/api/chain/edit` 会直接落库并下发任务（`src/frontend/src/pages/chains/use-chain-form.ts:655-684`），没有「保存为未发布」的 API。因此编排器只提供「发布」一个出口，不伪造草稿；若未来增加草稿能力，再恢复双出口。同理，第 3.2 节的「重试该跳」按现有 API 收敛为「重试链路」，失败跳通过拓扑定位，而不是逐跳独立重试。

**移动端**：<1024 显示只读说明页「链路编排需要 ≥1024px 的桌面窗口」，附当前链路摘要与「返回详情」；不把三步表单塞进 390px 宽度（与第 0.1 节一致）。

**可访问性**：所有节点选择与重排可用键盘完成；拖拽有等价按钮；校验摘要 `role="status"`；步骤切换不丢失焦点；颜色不作为唯一状态；按钮最小 44×44（触控），桌面 36×36 视觉尺寸配 44px 热区。

#### 5.1.6 网络域边界情况

| 情况 | 行为 |
|---|---|
| 检查器打开时对象被删/被过滤 | 轮询返回后移除 `sel`，保留筛选，列表头 `Notice`；焦点回到原行位置 |
| 详情打开时链路被删除 | 详情显示终止态横幅，停止轮询；保留「返回列表」与「查看已删除」 |
| 编排期间链路出现新发布 | 顶部横幅「该链路有新的发布任务」；禁用发布，要求刷新/重新打开，避免覆盖 |
| 两标签页同时编辑同一链路 | 无服务端锁；后提交方收到 API 错误，保留表单并显示 request_id；不自动重试写操作（现有 POST 不重试，`src/frontend/src/lib/requester.ts:242-300`） |
| 服务器在编排时离线 | 允许继续；第三步标注「发布将等待 Agent」，不假装成功 |
| 用户列表加载失败 | 影响面显示「未知」并提供重试；列表行的影响列显示 `--`，不写 0 |
| 1 跳 Chain 与独立 Node 同名 | 列表用类型标签区分，详情 URL 不同（chains vs nodes），命令面板结果带类型前缀 |
| 空列表 + 有筛选 | 显示「没有匹配的链路」+ 清除筛选；不显示「创建第一条链路」，避免误导 |

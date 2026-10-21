> **归档说明**：本文是设计分稿，已并入 `docs/ui-refactor-proposal.md` v1.0（口径见该文 13.3）；如两处不一致，以合稿为准。

## 8. 组件体系与页面骨架

> 本节把第 0–3 节的设计概念「线路值班台 Line Board」、五个一级入口与视觉语言落成可实现的组件与页面模板。像素、时长、数量均为实现规格；现状判断附证据路径；无证据的推断标「假设」。

### 8.1 分层、命名与复用约束

| 层 | 建议位置 | 职责 | 允许依赖 |
|---|---|---|---|
| 原语 | `src/frontend/src/components/ui/` | 无业务语义的 Base UI 包装 | `@base-ui/react`、cva、Tailwind |
| 设计系统组件 | `src/frontend/src/components/kit/`（建议） | 三态、密度、键盘、色调 | 原语 + 令牌 |
| 领域组件 | `src/frontend/src/components/domain/`（建议） | 线路、跳、revision、影响面 | kit + `lib/types.ts` |
| 页面模板 | `src/frontend/src/pages/` | 组合模板与数据编排 | 以上 + wouter |

- 不新增依赖：`@base-ui/react` 已在 `src/frontend/package.json`，toast、tooltip、checkbox、popover、combobox 均随包提供。现有 `components/ui` 只有 button、input、select、dialog、sheet、tabs、table、badge、card、progress、skeleton、separator、label、dropdown-menu 十四个文件，缺 Toast、Tooltip、Checkbox、Popover（证据：目录）。
- 请求只走 `lib/requester.ts`；轮询只走 `lib/use-polling.ts`；长操作只经 `OperationProgressProvider` 的 `showOperation({ observeId })`，调用点不改（证据：`pages/Chains.tsx`、`pages/Servers.tsx`、`pages/Groups.tsx`、`components/TemplateAssignmentTab.tsx`）。
- 组件只接收语义 `tone` 与 `state`，不接收色值；状态色只表达状态、信号蓝只表达交互、线路色只表达身份（第 0 节决策 5）。每个数据组件必须有 loading/empty/error/stale 四态，缺一不得合入。
- 领域名直接使用第 2–3 节术语：`LineBadge`、`HopRail`、`ImpactPreview`、`RevisionTimeline`、`OperationTray`，不用「卡片」「面板」这类无对象含义的名字。

### 8.2 组件清单

#### 8.2.1 基础组件

| 组件 | 用途 / 变体 | 状态与边界 | 可访问性 | 映射与证据 |
|---|---|---|---|---|
| Button | 主、次、描边、幽灵、危险；28/32/40 三档；图标 16 | default、hover、active、focus、disabled、loading；loading 保留原文案并 aria-busy | 2px 信号蓝焦点环 + 2px offset；图标按钮必须有 aria-label | 扩展 `ui/button.tsx`（Base UI Button + cva；现为 h-6…h-9、2px 圆角） |
| Input / Field | 文本、密码、数字、搜索；32 高，触屏 44 | focus、invalid、disabled、readOnly；错误 12px 置于字段下 | 可见 label；aria-invalid + aria-describedby | `ui/input.tsx`（Base UI Input）；字段容器新建 |
| Select | 5–20 项枚举；分组、禁用项 | open、selected、disabled；长列表不虚拟化 | aria-expanded；列表键盘导航 | `ui/select.tsx`（Base UI Select） |
| Combobox | 服务器、国家、模板等大于 20 项的可搜索选择 | 输入、无结果、加载、多选 | combobox 模式；aria-activedescendant；IME 组合期不提交 | 复用 `components/CountryCombobox.tsx` 的 Base UI 写法并抽为 kit |
| Checkbox / Switch | 表格选择与确认项 16px、44px 命中区；设置开关 | checked、indeterminate、pending、disabled | 原生 label；indeterminate 用 aria-checked=mixed；pending 用 aria-busy | 现无 ui 原语，页面用原生 checkbox（`pages/Groups.tsx`、`pages/users/CreateUserDialog.tsx`）；新包装 Base UI |
| Segmented | 视图与粒度切换（2–4 项） | selected、disabled | tabs 或 radiogroup 二选一，不混用 | 复用 `ui/tabs.tsx` 的 line 变体 |
| StatusChip | 服务器、链路、跳、用户状态 | neutral、info、success、warning、danger；20/24 高 | 文本 + 6px 方点；禁止只靠颜色 | 扩展 `ui/badge.tsx` 与 `components/StatusBadge.tsx`；文案在 `lib/status.ts` |
| Tooltip / Kbd | 图标按钮与截断文本的补充说明；快捷键提示 | open（延迟 200ms）、关闭 | 不承载唯一信息；hover/focus 均可触发；Esc 关闭 | 新包装 Base UI Tooltip；Kbd 新建 11px 等宽 |
| Skeleton / Spinner | 首载骨架与局部刷新 | 静态骨架、旋转；延迟 150ms | role=status + 文案；尊重 reduced-motion | 扩展 `ui/skeleton.tsx` 与 `PagePrimitives.tsx` 的 LoadingState |

#### 8.2.2 数据展示

| 组件 | 用途 / 变体 | 状态与边界 | 可访问性 | 映射与证据 |
|---|---|---|---|---|
| DataTable | 服务器、链路、用户、日志；44px 行、36px 紧凑、40px 表头 | loading、empty、error、stale、selected、pending | th scope、aria-sort、行可聚焦、Enter 打开、Space 选择 | 扩展 `ui/table.tsx`；现有表格已用该原语（`pages/Users.tsx`、`pages/ExternalSubscriptions.tsx`） |
| TableToolbar / FilterBar | 搜索 320px、筛选、密度、列设置；chip 可清除 | 默认、筛选生效、无结果 | 搜索有 label；结果数 aria-live polite | 新建；日志 severity 已有 URL 先例（`lib/log-severity-param.ts`） |
| MetricReadout / Delta | 机群、服务器、成本数值；Barlow Condensed、tabular；同比环比 | 正常、警告、不可用（--）、无基线 | 数值与单位同一朗读单元；方向不只靠颜色 | 新建；图表沿用 `components/echarts.tsx` |
| Sparkline | 24h 脉搏与吞吐；48px 高 | loading、无数据 | 提供当前/最高/最低文本摘要 | 复用 ECharts 封装 |
| KeyValueList | 检查器事实区；dt/dd 两列 | 默认、缺失值 | dl 语义；缺失显示「—」并注明 | 新建 |
| Pagination | 日志与大列表；每页 50/100 | 首页、末页、加载中 | 按钮 disabled + 当前范围文本 | 复用 `pages/OperationLogs.tsx` 的 offset 模式 |
| EmptyState / ErrorState | 全表空、筛选空、区块错误 | first-use、filtered、all-clear、error | 标题 + 说明 + 单一主操作 | 扩展 `components/PagePrimitives.tsx` |

#### 8.2.3 导航

| 组件 | 用途 / 变体 | 状态与边界 | 可访问性 | 映射与证据 |
|---|---|---|---|---|
| AppShell / MobileNav | 顶栏 56 + 左栏 224/72 + 内容；小于 1024 用 Sheet | 桌面、紧凑、移动 | 跳主内容链接；header/nav/main landmark | 重写 `components/Layout.tsx`；Sheet 复用 `ui/sheet.tsx` |
| PrimaryNav | 值班板、网络、访问、运行、设置；设置固定底部 | active、hover、collapsed | aria-current=page；44px 触屏行高 | 新建；路由继续用 wouter（`App.tsx`） |
| SectionTabs | 网络下的服务器、链路、分组、模板、外部订阅 | active、disabled | tabs 键盘左右移动 | 复用 `ui/tabs.tsx` |
| Breadcrumb | 对象层级；最多 3 级 | 默认、截断 | nav aria-label；当前页 aria-current | 新建 |
| PageHeader | 标题 24–28、描述、主操作、状态摘要 | 默认、紧凑 | h1 唯一；描述不超过 72ch | 扩展 `components/PagePrimitives.tsx` |
| Inspector | 列表右侧 400px 检查器（1280–1439 为 376，1024–1279 覆盖 480） | 打开、关闭、加载、错误 | 焦点进入标题、Esc 关闭、返回触发元素 | 新建；替代现有详情弹窗（证据：`pages/Servers.tsx`） |
| CommandPalette | ⌘K 全局入口 | 见 9.6 | dialog + combobox 模式 | 新建；Base UI Dialog + Combobox |

#### 8.2.4 反馈与操作

| 组件 | 用途 / 变体 | 状态与边界 | 可访问性 | 映射与证据 |
|---|---|---|---|---|
| InlineNotice / Toast | 区块内提示；复制成功等 3 秒反馈 | info、success、warning、danger | danger 用 alert，其余 status；Toast 不承载需行动信息 | 复用 `PagePrimitives.Notice`；新包装 Base UI Toast |
| GlobalBanner | 离线、面板更新、认证过期 | offline、updating、auth | role=status；不遮挡主操作 | 新建；面板更新沿用 `UpdateOverlay.tsx` 的全屏阻断（唯一例外） |
| OperationTray | 非阻塞操作集合 | 见 9.4 | region + aria-live polite | 重写 `OperationProgress.tsx` 呈现层，保留 Provider 与 observe 协议 |
| OperationCard | 单个观察：阶段、百分比、警告、动作 | starting、running、done、warning、failed、lost、stale | 状态文本；进度 aria-valuenow | 新建；数据来自 `Observation`（`lib/operation-progress.ts`） |
| ImpactPreview | 变更前影响面 | computing、ready、blocked、stale、error | 打开聚焦标题；重算 aria-live | 新建；派生规则见 9.3 |
| ConfirmDialog | L2 危险确认 | confirm、destructive、pending | 初始焦点取消；关闭后返回触发元素 | 扩展 `AppDialogProvider.tsx`（destructive 目前只影响按钮样式） |
| PartialFailureList / AuditLink | 部分失败逐项结果；跳操作日志 | 展开、收起、不可用 | ol 语义；每项含对象链接 | 新建；日志参数见 `lib/api.ts` 的 operationLogs |

#### 8.2.5 领域组件

| 组件 | 用途 / 变体 | 状态与边界 | 可访问性 | 映射与证据 |
|---|---|---|---|---|
| LineBadge | 链路身份：线路色 + 名称 + 状态 | active、degraded、failed 等 | 颜色 + 名称；不只色块 | 新建；状态文案复用 `lib/status.ts` |
| ServerStateDot | 服务器连接态 | online、offline、connecting、auth_rejected | 文本标签同现 | 新建；字段 `Server.connection_state` |
| HopRail | 入口、中转、出口的逐跳条 | 1–4 跳；失败跳高亮 | ol 语义；逐跳 aria-label | 新建；替代 `pages/Chains.tsx` 内联渲染 |
| ChainTopology | 链路详情拓扑画布 | 只读、编辑、逐跳错误 | 键盘可达节点；等价文本 | 新建；SVG，不引入 3D |
| RevisionTimeline | revision 与部署事件 | 见 9.7 | ol + time 元素 | 新建；无历史 API，见 9.7 |
| SubscriptionSnapshot | 用户订阅快照状态 | 未发布、有警告、正常 | 状态 + revision + 警告数 | 新建；字段 `SubUser.subscription_snapshot` |
| AccessMatrix | 用户 × 链路/分组矩阵 | 直接、分组派生、未分配 | 表格 + 复选；行列头可读 | 新建；数据 `chain_ids`、`effective_chain_ids` |
| CostMatrix | 服务器 × 周期成本 | 已生效、计算成本 | 表格 + 汇总 | 复用 `pages/Costs.tsx` 结构，替换视觉 |
| DriftChip | 配置漂移与设置未同步 | drift、pending、failed | 文本 + 修复入口 | 新建；字段 `config_drift`、`agent_settings_status` |

### 8.3 页面骨架模板

| 区域 | 尺寸 | 规则 |
|---|---|---|
| 顶栏 | 56px | 品牌 160、⌘K 触发器 240、面板状态、时钟、外观、账户 |
| 左栏 | 224px；1024–1279 用 72px 图标栏 | 小于 1024 改为 Sheet；设置固定底部 |
| 内容区 | 最大 1600px（正文区 1200）；内边距 24 / 16 / 12 | 大于 1600 居中留白；禁止整页横向滚动 |
| 页头 + 工具栏 | 页头 64（标题+描述双行时 72）；工具栏 44 粘顶 | 标题 28/1.1（Noto Sans SC Black）、面包屑 12、主操作 32；工具栏与表格间隔 8 |
| 检查器 | 400px（1280–1439 为 376），独立滚动 | 不小于 1440 内联 400；1280–1439 内联 376；1024–1279 为 480 覆盖抽屉；小于 1024 为全屏 Sheet |
| 圆角与托盘 | 卡片 12、控件 8、chip 6；托盘折叠 360 / 展开 420，右 24 / 下 24 | 8pt 网格；正文不小于 12；托盘有内容时页面底部预留 96px |

| 模板 | 用于 | 区域组成 | 滚动 | 断点行为 |
|---|---|---|---|---|
| T1 值班板 | 值班板 | 待处理队列 8 列 + 机群脉搏 4 列 | 页面滚动，队列不内滚 | 小于 1280 单列；小于 768 每条为卡片 |
| T2 列表 + 检查器 | 服务器、用户、分组 | 工具栏 + 表格 + 检查器 | 表格容器横滚；检查器独立滚动 | 检查器按壳层规则折叠；表格列逐步隐藏 |
| T3 链路详情 | 链路 | 状态条 64 + 拓扑 + 影响面/时间线 | 拓扑最小 480 高；右栏内滚 | 小于 1280 右栏下移；小于 1024 拓扑纵向 |
| T4 读数页 | 运行、成本 | 页签 + 汇总读数 4 张 + 图表 + 矩阵 | 页面滚动；图表自适应 | 汇总 4→2→1 列；矩阵保留首列 |
| T5 设置 | 设置 | 分组卡片 + 粘底保存条 64 | 页面滚动；保存条固定 | 小于 1024 单列；保存条全宽 |
| T6 公开订阅 | /sub/:token | 单列 480，导入动作第一 | 页面滚动 | 移动优先，不套用后台壳层 |

    ┌ 状态条：降级 · 入口 HK-01 离线 · revision 12 保持生效     [重试该跳] ┐
    ├──────────────────────────────────┬─────────────────────────────────┤
    │ 拓扑 480：HK-01 ── JP-02 ── US-03 │ 影响面：42 用户 / 3 分组 / 5 订阅 │
    │ 失败跳红边；旧路径用虚线并标注     │ revision 时间线（最近 12 条）    │
    ├──────────────────────────────────┴─────────────────────────────────┤
    │ 动作区：重试该跳（主）· 打开服务器 · 强制发布（危险，L2）            │
    └────────────────────────────────────────────────────────────────────┘

### 8.4 状态矩阵与断点规则

| 状态 | 触发 | 呈现 | 动作 | 可访问性 |
|---|---|---|---|---|
| 首载 | 首次进入且无缓存 | 与最终几何一致的骨架；延迟 150ms、最短 300ms | 无 | 区域 aria-busy；role=status |
| 刷新 | 轮询或手动 | 保留旧数据；显示「更新于 HH:mm:ss」 | 手动刷新 | 不抢焦点、不重复播报 |
| 空（首次） | 成功但 0 条 | 图标 + 标题 + 原因 + 主操作 | 创建或导入 | 标题层级正确 |
| 空（筛选） | 有数据但筛选后 0 条 | 已选条件 + 「没有符合条件的结果」 | 清除筛选 | 说明筛选条件 |
| 错误 | 请求失败 | 区块级错误卡 + request_id | 重试、复制诊断 | role=alert，焦点不强制移动 |
| 离线 | transport 失败或浏览器离线 | 顶栏 40px 横幅 + 最后数据时间 | 重连；禁用 R2/R3 | role=status；恢复时播报 |
| 部分失败 / 过期 | 多请求部分失败；数据超阈值 | 失败区块就地提示；时间戳 + 过期标记 | 仅重试失败区块；刷新 | 汇总可朗读；「数据可能已过期」文本 |

| 断点 | 导航 | 检查器 | 表格 | 页头与触控 |
|---|---|---|---|---|
| 不小于 1440 | 224px 展开 | 400px 内联 | 全部列 | 标题 28；行 44 |
| 1280–1439 | 224px 展开 | 376px 内联 | 隐藏低优先列 | 标题 28；行 44 |
| 1024–1279 | 72px 图标栏 | 480 覆盖 | 隐藏次要列 + 容器横滚 | 标题 24；行 44 |
| 768–1023 | Sheet 导航 | 全屏 Sheet | 卡片化或保留 4 列 | 标题 24；控件 40 |
| 小于 768 | Sheet 导航 | 全屏 Sheet | 卡片列表；无页面横向滚动 | 标题 20；可点区域不小于 44 |

### 8.5 交付映射与缺口

| 目标 | 现状 | 动作 | 文件 |
|---|---|---|---|
| 基础控件 | shadcn 原语已存在但为 2px 圆角与旧密度 | 扩展尺寸与令牌 | `components/ui/button.tsx`、`input.tsx`、`select.tsx`、`tabs.tsx` |
| 确认弹窗 | confirm/notify 统一但只有布尔结果 | 扩展 L1/L2/L3 与焦点规则 | `components/AppDialogProvider.tsx` |
| 检查器 | 详情以弹窗/抽屉为主 | 新建；路由记录选中对象 | 新建；`components/ui/sheet.tsx` |
| 操作托盘 | 全屏遮罩，同时只允许一个 | 重写呈现层，保留 observe 轮询与状态机 | `components/OperationProgress.tsx`、`OperationProgressProvider.tsx` |
| 命令面板 / 影响预览 | 不存在（依赖中也没有 cmdk） | 新建；Dialog + Combobox；数据客户端派生 | 新建 |
| revision 时间线 | 只有当前 revision 与任务列表 | 新建；日志合并推导 | 新建；`lib/types.ts` |
| 批量选择 | 只有页面内零散 checkbox | 新建 SelectionBar 与三态表头 | 新建 |
| Toast/Tooltip/Checkbox/Popover | 无 ui 包装 | 新包装 Base UI（非新增依赖） | `components/ui/` |

- 无服务端全局搜索：命令面板只索引已加载列表，用户上限 2000，服务端搜索列入 P3（假设 A5、A9）。
- 无 revision 历史端点：时间线由当前 revision、`revision_tasks` 与操作日志推导并标注来源（证据：`docs/openapi.yaml` 只有 create/edit 的 desired revision 描述）。
- 操作日志无 observe_id（证据：`OperationLogEntry` 字段），审计链接只能按对象与时间窗定位；无批量链路分配端点，`api.ts` 仅 `createUser` 接收 chain_ids。

---

## 9. 交互模式与变更安全流程

> 目标：让「改一条链路」和「删一台服务器」走同一条可预期、可回退、可审计的路径；让长操作不再夺走页面。核心判据：**观察完成不等于对象生效，文案必须由验证决定。**

### 9.1 风险分级与四步流程

| 级别 | 判定 | 例子（证据） | 必经步骤 |
|---|---|---|---|
| R0 浏览 | 无副作用 | 筛选、排序、打开检查器、预览订阅（`userSubscriptionPreview`） | 无 |
| R1 低 | 可重复、影响单对象 | 重试服务器测试、刷新模板列表、复制订阅 | 意图 + 即时反馈 |
| R2 中 | 改变线上配置或访问范围 | 创建/编辑/发布链路、批量指派模板、同步外部订阅、停用用户 | 四步 |
| R3 高 | 不可逆或影响面大 | 删除服务器/链路/用户、强制发布、重置订阅 token、恢复备份、升级、清空日志（`Settings.tsx`、`Chains.tsx`） | 四步 + L3 + 审计 |

| 步骤 | 用户问题 | 界面 | 必要数据 | 完成判据 | 失败与中断 |
|---|---|---|---|---|---|
| 1 意图 | 我要改什么 | 对象内对话框或编排器 | 对象、动作、范围 | 参数校验通过 | 留在意图层，字段级报错，不创建观察 |
| 2 影响预览 | 会影响谁、能否回退 | 内联区块、sheet 或对话框 | revision、用户/分组/订阅计数、逐跳 | 无阻塞项或已确认 | 阻止执行，给修复入口 |
| 3 执行 | 现在发生了什么 | 非阻塞操作托盘 | observe_id、阶段、百分比、警告 | 观察进入终态 | failed/lost 卡片 + 重新执行 + 审计 |
| 4 验证 | 真的生效了吗 | 状态条与托盘结果 | 对象最新状态 | 9.5 判据满足 | 「已提交，等待确认」+ 链接 |

- 影响预览只读，不创建观察（证据：校验失败不创建观察，`docs/superpowers/specs/2026-08-06-operation-progress-design.md`）。
- 托盘不阻塞；唯一保留的全屏阻断是面板自更新（`UpdateOverlay.tsx`）与认证过期。
- R3 不允许从命令面板直接执行；R2 可进入预览，但不得跳过预览。

### 9.2 意图（Intent）

- 入口：对象页主操作、行内溢出菜单、检查器动作区、命令面板。动作必须带对象与范围，禁止全局「删除」。
- 标题公式「发布链路『HK-JP-US』」「停用用户『lin』」；描述必须说明改什么、何时生效、能否回滚、影响多少对象（引用 9.3），四项缺一不得进入预览。
- 尺寸：单对象 480、批量 640；内边距 24；标题 20/1.3；正文 14/1.6；底部操作条 64 粘底；按钮 32（触屏 40）；圆角 12；移动端 sheet 90vh。
- 焦点：打开聚焦标题；R2 默认焦点主按钮；R3 默认焦点取消；Esc 关闭并返回触发元素；R1/R2 允许 Enter 提交，R3 必须显式点击。
- 校验：意图内同步完成；错误 12px + aria-describedby；聚焦首个错误；校验失败不进入预览、不创建观察。

### 9.3 影响预览（Impact Preview）

| 分区 | 必须回答 | 示例 |
|---|---|---|
| 变更摘要 | 改哪条对象、revision 如何变化 | 「链路 HK-JP-US：desired #12 → #13；3 跳」 |
| 影响对象 | 用户、分组、订阅、模板各多少 | 「用户 42（直接 17 / 分组派生 25）；分组 3；订阅 42」 |
| 服务影响 | 是否中断、哪一跳、顺序 | 「出口 → 入口逐跳下发；入口切换约 5 秒不可用（假设 A7）」 |
| 不可逆项 | 删除、token 重置、流量清零 | 「将删除 3 个出口节点，不可恢复」 |
| 阻塞项 / 数据时间 | 前置条件；计数基于何时数据 | 「JP-02 离线，可保存不能发布；基于 12:04:31 列表，30 秒内有效」 |

| 变更 | 计算 | 数据源 | 边界 |
|---|---|---|---|
| 发布/编辑链路 | 直接用户 = `chain_ids` 含该链路；分组派生 = `effective_chain_ids` 含该链路 | `/api/user/list`、`/api/link-group/list`、`/api/user-group/list` | 列表必须全部加载；任一失败标「无法计算」，不得按 0 处理 |
| 删除链路 | 同上，另加引用该链路的分组与外部订阅 | 同上 + `/api/external-subscription/list` | 外部订阅节点独立，不随链路删除 |
| 删除服务器 | 该服务器上的 hop → 受影响链路 → 用户与分组 | `/api/chain/list`、`/api/node/list`、`/api/user/list` | 离线仍可计算，但标「状态可能过期」 |
| 批量用户 | 选中数、已是目标态数量、将跳过数 | 当前列表 | 已停用/已删除项单独计数 |
| 服务器设置/升级 | 目标 revision 或版本、在线状态 | `Server.agent_settings_revision` 等 | 离线标「排队等待 Agent」 |

- 数字：能算就精确；0 写「0 位用户」；不能算写「无法计算」并说明缺哪份数据；只有抽样才用「约」并注明样本量。默认列前 5 个对象 + 「查看全部 42」。
- 放置：链路详情/编排器内联并随参数 250ms 防抖重算；列表行操作用 480 右侧 sheet；批量用 640 对话框。
- 状态：computing（骨架 3 行，延迟 150ms）、ready、blocked（禁用执行 + 修复链接）、stale（超过 30 秒）、error（说明失败数据源）。阻塞与过期不得只用颜色。
- 可访问性：打开聚焦标题；h3 + dl；重算完成 aria-live polite 播报「影响面已更新：42 位用户」；执行按钮 aria-describedby 指向阻塞原因。

### 9.4 执行：非阻塞操作托盘

**P1 交付边界**：现有 `OperationProgressProvider` 与 `operation-progress-state.ts` 是单 observeId 状态机（`src/frontend/src/lib/operation-progress-state.ts:5-49`）。P1–P2 按「同一时间只跟踪一个操作」交付（行为见 4.4）；本节的多卡堆叠为 P5 目标态，需要先改造状态层，不得在 P1 直接按多卡实现。

- 固定右下：right 24、bottom 24；折叠宽 360、展开 420；卡片间距 8；层级 40（Dialog 50）；移动端底部全宽 sheet。页面保持可滚动、可点击，无遮罩、不抢焦点；托盘有内容时页面底部预留 96px。
- 卡片：标题 14/600 单行截断 + 对象链接；状态 12px 文本 + 6px 点；阶段轨 4px、阶段名 11px；进度 12px tabular + 「3/5 用户」计数；消息 12/1.5 最多 2 行；动作 28px 高最多 3 个；警告折叠为逐条列表。
- 状态机：

| 状态 | 进入条件 | 呈现 | 动作 | 退出 |
|---|---|---|---|---|
| starting | 点击后至 observe_id 返回 | 「正在提交…」 | 无 | 收到 observe_id 转 running；8 秒无返回转「已提交，未返回进度」并停止等待 |
| running | 轮询返回 running | 进度与阶段实时更新 | 查看对象 | done/failed/lost |
| done / warning | status=done；有 warnings 时为 warning | 「已完成」或「完成，N 项警告」 | 验证结果、展开警告、审计 | 用户关闭 |
| failed | status=failed | 错误色 + 错误文案 | 重新执行、审计、查看对象 | 用户关闭 |
| lost | 轮询 404 | 「进度已丢失，操作可能仍在后台继续」 | 刷新对象、审计 | 用户关闭 |
| stale | 终态快照超过服务端 5 分钟 TTL | 保留最后快照并标「快照已过期」 | 查看对象、审计 | 用户关闭 |

- 堆叠：新操作在最下；同时只有一张展开（优先运行中最久的一条，否则最新）；最多显示 3 张，其余「+N 个操作」；会话内最多 5 条，超出淘汰最旧终态；终态 2 秒后自动收拢但不消失，不采用现有 1 秒自动关闭（`OperationProgressProvider.tsx` 的 AUTO_CLOSE_DELAY_MS）。刷新不恢复托盘（观察不持久化），后台操作继续。
- 重试：

| 级别 | 按钮 | 行为 | 保护 |
|---|---|---|---|
| R1 | 重新执行 | 原参数直接重发 | 防连点；显示 pending |
| R2 | 重新执行 | 重开影响预览，数据重算后提交 | 不允许跳过预览 |
| R3 | 重新执行 | 进入意图并重新完成 L3 | 禁止一键重试 |

- 不自动重试 POST：`requester.ts` 对 POST 只尝试 1 次，每次 POST 自动生成新 Idempotency-Key；手动重试是新请求，可能重复副作用。观察不可取消、不可重试（证据：设计文档「观察记录不是可操作的任务」），因此不提供「取消」，文案说明后台会继续。
- 同一对象已有 running 操作时主操作禁用并提示「该对象正在部署」；不同对象可并行，不排队、不合并。
- 审计：`/logs/operations?category=<category>&server_id=<id>&from=<startedAt-60s>&to=<finishedAt+60s>`，参数与 `api.operationLogs` 一致；done/warning/failed/lost 显示「审计」，running 只显示「查看对象」。日志条目暂无 observe_id（`OperationLogEntry` 字段），链接按对象与时间窗定位；日志未落库时提示「可能延迟数秒」，P3 再写 observe_id 与 trace_id。
- 轮询：500ms 起步，30 秒后 1s，2 分钟 2s；`document.hidden` 暂停，恢复可见立即拉取；404 转 lost；连续 3 次传输错误显示「连接中断，正在重试」但保持 running；复用 `OperationProgressProvider` 骨架，把单条状态改为按 observe_id 的集合，`operation-progress-state.ts` 按卡片复用。

### 9.5 验证（Verify）

| 操作 | 成功判据 | 数据源 | 超时 | 未确认时 |
|---|---|---|---|---|
| 发布/编辑链路 | status=active，published_revision_id = desired_revision_id，所有 hop=active | `/api/chain/list` | 30s | 「已提交，等待 Agent 确认」+ 逐跳状态 |
| 重试失败跳 | 目标 hop=active | 同上 | 30s | 保留失败跳高亮 |
| 删除链路/节点 | 对象从列表消失 | 同上 + `/api/node/list` | 10s | 「可能仍在清理」 |
| 强制发布 | active_unconfirmed 或 active，force 为真 | 同上 | 30s | 标注「尚未确认」 |
| 服务器设置 / 升级 | settings_status=synced 且 revision 相等；版本等于目标且命令 acked | `/api/server/list` + 命令日志 | 30/60s | 「等待 Agent 拉取」并保留命令 id |
| 用户停用/删除 | disabled=true 或用户消失 | `/api/user/list` | 10s | 保留 pending 标记 |
| 模板指派 / 外部同步 | 快照 revision 或时间前进；last_sync_at 前进且 last_error 为空 | `/api/user/list`、`/api/external-subscription/list` | 30s | 显示观察警告与最后错误 |

- 流程：终态 → 静默刷新一次 → 未满足则每 5 秒轮询至 30 秒 → 仍未满足显示「已提交，等待确认」，绝不写「已生效」。
- 「完成」只描述观察，「已生效」只描述验证通过；验证失败不是操作失败，保留警告态与审计链接，不自动回滚。
- 验证后让命令面板索引与列表缓存失效，避免用旧数据做下一次预览。

### 9.6 命令面板 ⌘K

| 分组 | 内容 | 执行 |
|---|---|---|
| 跳转 / 对象 | 五个一级入口、最近访问；服务器、链路、用户、分组、模板、外部订阅 | 直接导航；Enter 打开检查器或详情 |
| 创建 | 链路、用户、服务器 | 打开意图 |
| 安全操作 / 排除 | 重试、刷新模板、复制订阅；删除、强制发布、恢复备份、升级 | 仅 R1 直接执行；R3 不提供执行，只给「查看对象」 |

- 触发：⌘K / Ctrl+K 全局切换，顶栏按钮同效；不劫持输入框内普通按键；Esc 关闭并还原焦点。
- 尺寸：宽 640；距顶 12vh；最大高 min(560px, 70vh)；输入 48；分组头 28 粘顶；结果行 40；底部提示 36；圆角 12；实色表面 + overlay 阴影，不用毛玻璃；120ms 淡入，reduced-motion 取消位移。
- 键盘：↑↓ 移动；Enter 执行；⌘Enter 仅对导航结果在新标签打开；Home/End 到首尾；PageUp/PageDown 翻页；IME 组合期 Enter 不执行。
- 索引：来源为已加载列表；打开时缓存超过 30 秒先刷新；用户上限 2000（假设 A9）；匹配名称、别名、ID、地址的小写包含；排序 exact > prefix > substring；每组最多 8 条、总共 20 条。
- 状态与可访问性：无查询显示最近 5 个对象 + 五入口；无结果显示「没有匹配」+ 创建入口；索引失败显示重试；离线用缓存 + 横幅。role=dialog + aria-modal；输入为 combobox，挂 aria-controls/expanded/activedescendant；结果 listbox/option；结果数 aria-live polite；背景 inert。

### 9.7 revision 时间线

- 位置：链路详情右栏，影响面下方；折叠显示最近 3 条，展开最多 20 条，「查看更多」跳操作日志。
- 来源必须可见：OpenAPI 没有 revision 历史端点（证据：`docs/openapi.yaml` 只有 create/edit 的 desired revision 描述），时间线由 `Chain.published_revision_id`、`desired_revision_id`、`revision_forced`、`revision_tasks` 与操作日志推导，标题旁标「由操作日志推导」。
- 条目：时间（time 元素）、动作（发布/强制发布/重试/清理）、revision（#12 → #12）、结果、逐跳任务、操作者、耗时、审计链接；revision 与任务来自链路对象，时间/操作者/结果来自 `OperationLogEntry`，本次托盘事件可即时插入一条「本地」，刷新后以日志为准。
- 状态：loading、empty（「创建或发布链路后出现」）、partial（日志不可用，仅显示当前 revision）、error；partial 必须说明缺哪部分，不补造中间 revision。
- 交互与边界：点击展开逐跳任务；可按结果或跳筛选；ol + 可聚焦条目；失败条目链接对应跳。不把 desired 显示成 published；强制发布单独标记；不生成不存在的序号。

### 9.8 批量选择与危险操作确认

- 选择：首列 40px、复选框 16px、命中区 44px；表头三态；Shift 连选；⌘/Ctrl 点选；Esc 清空；只在列表挂载期间保留，不写入 URL。选择栏高 56、底部 24 居中、最大宽 720、圆角 12、overlay 阴影；显示「已选 12 · 其中 3 项不在当前筛选」；小于 1280 时位于托盘上方。
- 批量语义必须如实标注，不得把循环调用说成原子操作：

| 动作 | 调用 | 语义 | 级别 | 确认 | 部分失败 |
|---|---|---|---|---|---|
| 指派/取消模板 | `/api/subscription/template/assign`，user_ids[] | 单请求，真实批量 | R2 | L2 | 观察 warnings 逐用户列出 |
| 加入用户分组 | `userGroupUpdate` 替换 user_ids（假设 A8） | 单请求，覆盖成员 | R2 | L2 | 重新拉取后逐项对比 |
| 启用/停用 | `/api/user/update` 逐个 | 串行，最多 3 并发 | R2 | L2 | 逐项结果，失败不回滚成功项 |
| 删除用户 | `/api/user/delete` 逐个 | 串行，不可逆 | R3 | L3 | 逐项结果，停止或继续在意图中选定 |
| 删除链路 | `/api/chain/delete` 逐个 | 串行，每项一个观察 | R3 | L3 | 托盘合并为一张「批次卡片」显示 x/n |

- 批量执行统一用一张「批次卡片」（如「批量停用用户 · 3/12」），内部逐项结果；底层仍是单个请求与观察，不伪造服务端批次任务。
- 危险确认三级：L1 行内（可撤销或可重跑，二次点击或 5 秒撤销条，焦点原位）；L2 对话框（删除单对象、停用用户、重置流量，默认焦点取消）；L3 输入确认（删除服务器/链路、强制发布、恢复备份、重置 token，必须输入对象全名才启用主按钮，焦点在输入框）。
- L3 细节与安全：autocomplete=off；不预填；不匹配时 disabled 并说明「名称不一致」；Enter 匹配前不提交；Esc 取消；错误用 aria-describedby；提交中禁用主按钮；关闭后焦点回触发元素；破坏性按钮不自动聚焦；文案必须含对象名、影响数、是否可逆；已是目标态的条目显示「将跳过 N 项」且不计入影响数。

### 9.9 加载、空、错误、离线与部分失败

| 模式 | 呈现 | 动作 | 文案 | 可访问性 |
|---|---|---|---|---|
| 首载 | 与最终布局同几何的骨架；150ms 延迟、300ms 最短 | 无 | 不用「加载中…」占满整页 | aria-busy；role=status |
| 刷新 | 保留旧数据 + 更新时间 | 手动刷新 | 「更新于 12:04:31」 | 不抢焦点 |
| 空（首次） | 图标、标题、原因、主操作 | 创建或导入 | 「还没有链路。先创建一条，或从模板导入。」 | 标题层级 |
| 空（筛选） | 已选条件 chips + 清除 | 清除筛选 | 「没有符合条件的结果」 | 说明筛选条件 |
| 错误 | 区块级错误卡 + request_id | 重试、复制诊断 | 「读取失败，已重试 3 次」 | role=alert，焦点不跳 |
| 离线 | 顶栏 40px 横幅 + 最后数据时间 | 重连；禁用 R2/R3 | 「连接中断，显示 12:04:31 的数据」 | role=status；恢复播报 |
| 部分失败 / 过期 | 失败区块就地提示；时间戳 + 过期标记 | 仅重试失败区块；刷新 | 「3 个区块中 1 个失败」「数据可能已过期」 | 汇总可朗读 |

- 错误分类：business（后端业务码）字段或区块内联并保留输入，修正后手动重试；transport（网络、超时、HTTP）横幅或区块错误并保留最后数据，自动 3 次 + 手动；protocol（响应格式异常）错误卡 + request_id，手动重试；cancelled（卸载或主动取消）不提示、不重试。
- 失败不得清空整页：只有全部区块失败才升级为页面错误；单区块失败保留其余内容。
- 认证过期是跳转登录，不作为页面错误；离线时 R2/R3 主操作禁用并给出原因，而不是提交后报错。

### 9.10 轮询反馈与乐观更新边界

| 表面 | 间隔 | 隐藏时 | 停止条件 |
|---|---|---|---|
| 操作托盘 | 500ms → 1s → 2s | 暂停 | 终态或 lost |
| 值班板 / 网络列表 | 10s | 暂停 | 卸载 |
| 访问列表 | 15s | 暂停 | 卸载 |
| 运行监控 | 5s | 暂停 | 卸载 |
| 成本 | 60s | 暂停 | 卸载 |
| 日志 / 面板状态 | 手动，或自动刷新 15s；面板 5s（现状 `Layout.tsx`） | 暂停 | 卸载 |

- 反馈分层，禁止重复：顶部 2px 请求条只表示前台请求（`Layout.tsx` 的 foregroundPendingCount）；托盘只表示被观察的长操作；行内 pending 只表示单次变更；骨架只表示首载；Toast 只表示 3 秒确认；横幅只表示全局离线或面板更新。
- 允许乐观更新：外观、密度、导航折叠、页签、筛选 chip 与 URL、复制反馈；失败只回滚局部状态，不伪造服务端结果。
- 禁止乐观更新：链路/服务器/用户状态、revision、订阅快照、设置持久化、成本与计费、任何 R2/R3 操作。
- 变更中的行用「最后确认状态 + pending 标记」（如「正常 · 部署中」），不得提前显示「正常」或「已停用」；响应后重新拉取，失败只移除 pending 并就地报错。
- 幂等与并发：POST 只尝试 1 次，每个 POST 自带 Idempotency-Key（`requester.ts`）；手动重试是新键，R2/R3 必须先重算影响面。所有新轮询复用 `usePolling` 的 AbortController 与 requestRef 序号守卫；页面隐藏暂停，恢复可见且超过间隔时立即刷新。
- 屏幕阅读器：阶段变化与终态播报，百分比不逐跳播报；aria-live 用 polite，只有失败用 assertive。

### 9.11 验收口径

- 任意 R2/R3 变更都能从托盘回到对象，再跳到对应时间窗的审计记录。
- 观察 done 但对象未满足判据时，界面任何位置都不出现「成功」「已生效」。
- 影响预览为 0 时明确写「0 位用户」；无法计算时写「无法计算」并说明缺哪份数据。
- 断网 30 秒再恢复：不整页报错、不重复弹错、不丢失已填表单；批量部分失败逐项可见，绝不显示「全部成功」。
- 仅用键盘可完成：⌘K 找对象 → 选中 → 预览 → 确认 → 关闭托盘。

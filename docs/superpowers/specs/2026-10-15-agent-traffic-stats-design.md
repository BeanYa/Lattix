# 被控服务器流量统计与监控报表设计

日期：2026-10-15

## 背景与动机

运行监控页（`/runtime`）目前只监控 panel 自身进程（CPU/内存/Go runtime/任务表）。被控 agent 的数据每天都在流入 panel（telemetry 含每个入站/链路跳/共享端点/每个用户的上下行累计字节数），但**没有按时间维度落库**，导致核心场景无法满足：

> 服务商页面显示流量陡增 10%，在 Lattix 内无法回答"流量是哪台服务器、哪个用户、在什么时间段消耗的"。

现有存储的缺口：`traffic` 表只有累计值（回答不了"什么时候"）；`chain_traffic_daily` 只有链路维度；`server_network_usage_daily` 是网卡层机器总流量（分不出业务和用户）。

## 目标

1. **流量对账与归因**（核心）：支持"服务器 × 用户 × 时间"三个维度任意下钻，回答陡增排查问题。
2. 运行监控页新增「被控统计」Tab：全网今日/本月上下行总量、每日/每月趋势、Top 服务器 / Top 用户。
3. 服务器详情 Sheet 补上缺失的流量图表（24h 上下行速率），并放入口打开该服务器的下钻弹窗。
4. 监控页显示粗略错误信号（今日错误计数 → 跳转日志页预筛），明细复用现有日志页。

## 非目标（明确不做）

- **不改 agent**：agent 侧 telemetry 上报保持原样（它已每约 60s 上报绝对累计计数器，panel 端游标增量机制已自愈 xray 重启）。
- **不建新的错误采集管道**：错误数据来自现有操作日志与命令错误记录，监控页只做计数和跳转。
- **不补历史数据**：统计从功能上线那一刻开始积累。
- 不做告警/通知。
- 不引入 SSE/WebSocket 推送，前端维持轮询。
- 不做计费口径的多倍率核算（那是 `chain_traffic_daily` 的 effective 字段职责）。

## 硬性约束

1. **轻量化**：panel 侧统计，agent 零改动；不新增外部依赖（无 Prometheus 等时序库，继续用现有 SQLite）。
2. **无"统计粒度"设置项**：数据按分钟级明细桶落库；页面粒度选择（5m/15m/1h/天/月）只是修改数据库查询的聚合范围。
3. 沿用 `chain_traffic_daily` 的模式：**ingest 时事务性累加**，不做事后定时 rollup；时区沿用 `SettingTrafficTimezone`（默认 Asia/Shanghai）。
4. 复用现有基础设施：`requester` 请求客户端、echarts 封装、TrendChart 组件、scheduler 任务模式、API 契约生成流程（openapi.yaml → `generate:api`）。

## 方案总览

```
agent（不变）:  xray stats ──60s──> telemetry.report（绝对累计计数器）
                                     │
panel ingest（扩展点）:              ▼
              Dispatcher.handleTelemetry → Store.ApplyTrafficSnapshot（唯一入库点）
                                     │ 事务内追加：
                                     ├── 明细桶表 traffic_stats_bucket（分钟级，稀疏）
                                     └── 日汇总表 traffic_stats_daily（长期保留）
                                     │
scheduler（新增任务）:  stats.retention —— 清理超出保留期的明细桶
                                     │
API（新增）:  /api/stats/traffic/overview | series | breakdown | server-history
              /api/stats/errors/summary        （查询时按粒度聚合）
                                     │
前端:  运行监控页 Tab「被控统计」＋ 下钻 Dialog ＋ 服务器详情 Sheet 流量图
```

## 统计口径（已与用户确认）

同一笔流量会落在多个维度的计数器上：走链路的用户传 1GB，会在入口服务器产生用户计数器 +1GB 与端点计数器 +1GB、每个中继产生跳计数器 +1GB、出口产生节点计数器 +1GB。为避免总量虚胖 2–4 倍，两种视角采用不同口径：

- **单服务器视角**（Top 服务器榜、按服务器下钻、详情 Sheet 图表、`server-history`）＝ 该服务器上**实体维度合计**（node + chain_hop + endpoint）＝ 实际流过该服务器的字节，可直接与其供应商页面对账。
- **全网总量卡与环比**（`overview`、`series?dim=fleet`）＝ **用户维度合计**，每个用户的流量端到端只计一次。
- 存在链路时 Σ各服务器 ≥ 全网用户总量，这是物理事实，界面需注明两种口径的差异。
- **环比 = 同进度对比**：今日至今 vs 昨日同一时刻；本月至今 vs 上月同一日期时刻。不做"未完成期 vs 完整期"的误导性对比。

## 数据层设计

### 表结构（建议，最终命名/DDL 归实现者）

`traffic_stats_bucket`（明细桶，分钟级）：

| 列 | 说明 |
|---|---|
| `bucket_ts` | 分钟对齐的 Unix 时间（`sample_time` 向下取整到分钟；agent 上报间隔默认 60s 时自然一行一分钟，间隔更长则稀疏） |
| `server_id` | 上报该计数器的 agent 对应服务器 |
| `dim_type` | `user` \| `node` \| `chain_hop` \| `endpoint` |
| `dim_id` | 用户 UUID / 节点 ID / 链路跳标识 / 共享端点 ID |
| `up`, `down` | 该分钟增量字节数（原始值，不乘倍率） |

- 主键/唯一约束：`(bucket_ts, server_id, dim_type, dim_id)`，UPSERT 累加。
- 稀疏写入：有增量才写行。
- 索引：查询走 `(dim_type, dim_id, bucket_ts)` 与 `(server_id, dim_type, bucket_ts)` 组合。

`traffic_stats_daily`（日汇总，长期保留）：同维度结构，`date`（按 `SettingTrafficTimezone` 计算的本地日期）替代 `bucket_ts`，UPSERT 累加。

### 写入路径

- 扩展点：`Store.ApplyTrafficSnapshot`（`src/backend/internal/store/chain_traffic.go:42-136`），**复用它已算好的增量与身份解析结果**（`accessTrafficOwner` 已把 `access:` / `group:<uuid>:<chain>` 归属到用户），不重复实现游标/重启检测逻辑。
- 每个增量同时 UPSERT 明细桶与日汇总（同一事务）。
- 记录**原始字节**（raw，不乘链路倍率）——本系统服务于对账归因，计费口径归 `chain_traffic_daily`。
- 维度覆盖：用户维度（归属用户）+ 实体维度（node / chain_hop / endpoint），满足"两者都要统计、可一路下钻"。

### 保留策略

- 明细桶保留期：新增**运维可编辑**设置项（默认 **60 天**，理由：覆盖"当月 + 上一个完整月"的小时级回看，满足月底对账的环比需求），走 Settings UI/API 全流程，模式同 `SettingTrafficTimezone`（`panel/settings.go` payload/校验 + 设置页表单）。清理任务注册进 `registerCoreTasks`（`src/backend/internal/panel/panel.go:261-308`），模式照 `metrics.retention`（`src/backend/internal/panel/metrics.go` + `store/metrics.go:195-203`）。
- 日汇总不清理（与 `chain_traffic_daily` 同策略，SQLite 行很小）。

## API 层设计

按项目契约流程：先加 `docs/openapi.yaml` → `bun run generate:api` 重新生成 `api-contract.generated.ts` → `api.ts` 方法 + `types.ts` 手写类型（先例：`chainTrafficHistory`，`src/lib/api.ts:260-265`）。

| 端点 | 作用 | 关键参数 |
|---|---|---|
| `GET /api/stats/traffic/overview` | 今日/本月上下行总量、环比、Top 服务器、Top 用户 | `—` |
| `GET /api/stats/traffic/series` | 时序序列（图表主数据） | `dim=server\|user\|fleet`、`dim_id`、`granularity=5m\|15m\|1h\|day\|month`、`range` |
| `GET /api/stats/traffic/breakdown` | 下钻明细（按服务器看用户/实体，或按用户看服务器） | `server_id` 或 `user_id`、`range`、`granularity` |
| `GET /api/stats/traffic/server-history` | 单服务器时序（详情 Sheet 流量图） | `server_id`、`hours=24` |
| `GET /api/stats/errors/summary` | 今日错误计数（数据源：操作日志 severity=error；实现者核验 agent 离线是否已入操作日志，没有则仅计错误日志，不新增采集） | `—` |

查询实现要点：

- 粒度 ≤1h：对明细桶按时间窗口 `GROUP BY`（5m/15m/1h 直接按 `bucket_ts` 分组）；天/月：查日汇总表。
- 天/月视图跨保留期无缝：保留期内由日汇总表兜底（明细桶过期不影响月视图）。
- 路由注册进 `RegisterRoutes`（`src/backend/internal/panel/panel.go:311-504`），统计类只读端点按 `polledRead` 惯例标记。

## 前端设计

### 运行监控页 Tab 化（`src/pages/RuntimeMonitor.tsx`）

- 页顶 Tab：「Panel 运行」（现有内容原样迁入）/「被控统计」（新增）。
- 「被控统计」Tab 内容：
  - 概要卡：今日 ↑↓、本月 ↑↓、在线 Agent、**今日错误 → 点击跳 `/logs/operations` 预筛 severity=error**；
  - 粒度切换：5m / 15m / 1h / 天 / 月（切换 = 重新请求 series，即"修改查询范围"）；
  - 趋势图：echarts 堆叠柱状图（上/下行两系列），复用 `src/pages/costs-shared.tsx` 的 `buildBarOption`/`chartThemeColors` 模式与 `src/components/echarts.tsx` 封装（如需折线再注册 LineChart）；
  - Top 服务器 / Top 用户 双榜（来自 overview）。
- 轮询：概要与趋势 30–60s 轮询即可（统计不是 5s 级实时数据），用现有 `usePolling`/silent 惯例。

### 下钻 Dialog

- 触发：Top 榜点击服务器或用户；服务器详情 Sheet 也有入口。
- 形态：沿用链路流量历史 Dialog 先例（`src/pages/chains/TrafficHistoryDialog.tsx`）+ 其 controller hook 模式（`use-traffic-history.ts`）。
- 内容：时间范围选择（近24h/7天/30天/自定义）+ 时序图（同 echarts 堆叠柱）+ 分解表（按服务器下钻→列出用户与实体行；按用户下钻→列出其跨服务器分布，行可继续点击互换维度）。

### 服务器详情 Sheet（`src/components/ServerMonitor.tsx`）

- 新增第 6 张 TrendChart：「流量」24h 上下行速率（数据 `server-history` 端点，60s 轮询同现有 metric-history 模式），与现有 CPU/内存/磁盘/网速/延迟五张图同款 SVG 组件与排布。
- Sheet 内加「流量统计」入口打开该服务器的下钻 Dialog。

### 错误粗信号

- 只显示计数与跳转，不在监控页展开明细（明细都在 `/logs/operations`）。

## 现有代码锚点（实现者地图）

| 事项 | 位置 |
|---|---|
| telemetry 入库分发 | `src/backend/internal/dispatch/dispatcher.go` `handleTelemetry` `:1015-1059` |
| 唯一入库点/身份解析 | `src/backend/internal/store/chain_traffic.go` `ApplyTrafficSnapshot` `:42-136`、`accessTrafficOwner` `:229-265` |
| 日桶+时区先例 | `chain_traffic_daily` 建表 `store/store.go:498-509`、时区 `chain_traffic.go:305-315` |
| 建表/迁移 | `store/store.go:18-644`、`migrations.go`（`schemaVersion=18`、`ensureColumns` 模式） |
| 定时任务注册 | `panel.go` `registerCoreTasks` `:261-308`；保留任务先例 `metrics.retention` |
| API 契约流程 | `docs/openapi.yaml` + `src/frontend/scripts/generate-api-types.mjs`（`bun run generate:api`，build 跑 `check:api`） |
| 前端请求/轮询惯例 | `src/lib/requester.ts`、`src/lib/api.ts`、`src/lib/use-polling.ts`（轮询 silent） |
| echarts 封装/图样式 | `src/components/echarts.tsx`、`src/pages/costs-shared.tsx` |
| 详情页图表组件 | `src/components/ServerMonitor.tsx` `TrendChart` `:692-910`、`DetailSheet` `:912-1185` |
| 下钻 Dialog 先例 | `src/pages/chains/TrafficHistoryDialog.tsx`、`use-traffic-history.ts` |

无状态机改动：本功能是纯被动观测/统计，不涉及任何 FSM 状态转移（符合 AGENTS.md 对状态机一致性的要求，无需触碰）。

## 拍板项记录（对话中已定或由我拍板，用户过目）

1. 明细桶保留期默认 60 天、做成设置项（用户参与讨论后采纳）。
2. 明细桶 + 日汇总两层表（我拍板：月视图与跨月环比不受明细过期影响，成本极低）。
3. 统计记录原始字节、不乘链路倍率（我拍板：对账归因看真实流量，计费口径归 chain 体系）。
4. `bucket_ts` 按墙钟分钟对齐（我拍板：与 agent 上报间隔解耦，间隔长则稀疏）。
5. 错误粗信号数据源 = 操作日志 severity=error；agent 离线若未入操作日志则不强行新增采集（我拍板，实现者核验）。
6. 趋势图用 echarts 堆叠柱（复用 Costs 先例）；详情 Sheet 流量图用同款 SVG TrendChart（保持 Sheet 内五图一致）（我拍板）。
7. 统计 Tab 轮询 30–60s（我拍板：非实时数据，无需 5s）。
8. 统计口径：单服务器 = 实体维度合计、全网总量 = 用户维度合计、界面注明差异（用户已确认）。
9. 环比 = 同进度对比（今日至今 vs 昨日同时刻；本月至今 vs 上月同日期）（我拍板，用户知悉）。
10. 保留期设置走 Settings UI/API 正式流程（我拍板，用户知悉）。

## 留给实现者的决策

- 两张新表的最终命名、DDL 细节、迁移号与索引具体形式。
- API 字段命名与响应结构细节（遵循 openapi.yaml 现有风格）。
- 分解表分页/截断策略（Top N 即可，无需完整分页）。
- 组件拆分与文件组织（遵循 `pages/<page>/`、hook controller 先例）。
- 测试切面：store 层 UPSERT/时区/保留清理的单元测试、API handler 测试、前端 hook 测试（先例均有）。

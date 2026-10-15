# 被控服务器流量统计与监控报表 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 在 panel 侧新增分钟级流量统计落库与查询聚合，前端运行监控页加「被控统计」Tab（总览/趋势/Top 榜/下钻弹窗），服务器详情 Sheet 补流量图表，错误粗信号跳转日志页。

**Architecture:** agent 零改动；在 `Store.ApplyTrafficSnapshot` 这个唯一入库点事务内追加写两张新表（明细桶 + 日汇总），统计 API 从这两张表查询聚合（≤1h 粒度走明细桶、天/月走日汇总），保留清理交给 scheduler 新任务。口径：单服务器=实体维度（node/chain_hop/endpoint）、全网总量=用户维度（端到端去重）、环比=同进度对比。

**Tech Stack:** Go（panel，SQLite raw database/sql）、React 19 + TS + Vite、echarts（现有封装）、wouter、vitest。

**Spec:** `docs/superpowers/specs/2026-10-15-agent-traffic-stats-design.md`（口径、非目标、拍板项都在其中，实现前必读）

## Global Constraints

- 本机**无 bun**：前端命令用 `npm run <script>`；若 `src/frontend/node_modules` 缺失，先 `npm install --no-package-lock --no-save --legacy-peer-deps`。
- Go 测试：`cd src/backend && go test ./...`（模块在 go.work 中注册，直接跑）。
- 统计只记**原始字节**（raw），不乘链路倍率；计数单位 int64。
- 新端点必须先写 `docs/openapi.yaml` 再 `npm run generate:api`（build 的 `check:api` 会拦截漂移）。
- 复用 `requester`/`usePolling`/echarts 封装/TrendChart/ui 原语，不新增依赖。
- 文案硬编码简体中文（项目无 i18n）。
- 轮询一律 `{ display: 'silent' }`。
- `schemaVersion` 18 → 19（`src/backend/internal/store/migrations.go:14`）。
- 提交信息用仓库惯例 conventional commits（如 `feat(stats): ...`、`feat(frontend): ...`）。
- 执行环境：按 AGENTS.md 用 using-git-worktrees 在 `<repo>/.worktree/<name>` 建工作树。

## Review Focus

实现时除各任务测试外，这些输入类/失败模式必须有测试钉住（均已分配到归属任务）：

1. **xray 重启计数器回退**：新 instance_id 从零计，统计桶不得出现负值或凭空丢失 → Task 1 测试断言桶内累计与 `traffic` 表一致。
2. **同一分钟内多次上报同一计数器**：UPSERT 累加而非覆盖 → Task 1。
3. **时区日界**：`SettingTrafficTimezone` 下 23:59 与 00:01 落入不同 `usage_date` → Task 1。
4. **fleet 口径去重**：全网总量只计用户维度，实体维度行不得混入 → Task 2。
5. **明细桶过期后日/月视图兜底**：删除过期桶后 daily 查询仍完整；空时间范围返回空数组不报错 → Task 2/3。

---

### Task 1: Store — 统计表与写入路径

**Files:**
- Modify: `src/backend/internal/store/store.go`（Schema 常量内追加两张表 + 索引）
- Modify: `src/backend/internal/store/migrations.go:14`（schemaVersion 19）
- Modify: `src/backend/internal/store/chain_traffic.go`（`ApplyTrafficSnapshot` 循环内挂钩）
- Create: `src/backend/internal/store/traffic_stats.go`（写侧 helper）
- Test: `src/backend/internal/store/traffic_stats_test.go`

**Interfaces:**
- Consumes: `ApplyTrafficSnapshot(ctx, serverID int64, instanceID string, counters []TrafficCounterSnapshot, sampledAt time.Time) error`（现有，`chain_traffic.go:42`）；`trafficLocation(ctx)` 返回 `(timezone, *time.Location)`；`accessTrafficOwner` 已在循环内解析归属用户。
- Produces（后续任务依赖，签名逐字）:
  ```go
  // dimType: "user" | "node" | "endpoint" | "chain_hop"；dimID: 用户UUID或十进制ID字符串
  func (s *Store) addTrafficStatTx(tx *sql.Tx, serverID int64, bucketTS int64, usageDate, dimType, dimID string, up, down int64) error
  ```
  表结构（写进 Schema 常量）：
  ```sql
  CREATE TABLE IF NOT EXISTS traffic_stats_bucket (
      server_id INTEGER NOT NULL REFERENCES servers(id),
      dim_type  TEXT    NOT NULL,
      dim_id    TEXT    NOT NULL,
      bucket_ts INTEGER NOT NULL,          -- 分钟对齐 Unix 秒
      up        INTEGER NOT NULL DEFAULT 0,
      down      INTEGER NOT NULL DEFAULT 0,
      PRIMARY KEY (server_id, dim_type, dim_id, bucket_ts)
  );
  CREATE INDEX IF NOT EXISTS idx_traffic_stats_bucket_ts ON traffic_stats_bucket(bucket_ts);
  CREATE INDEX IF NOT EXISTS idx_traffic_stats_bucket_dim ON traffic_stats_bucket(dim_type, dim_id, bucket_ts);
  CREATE TABLE IF NOT EXISTS traffic_stats_daily (
      server_id  INTEGER NOT NULL REFERENCES servers(id),
      dim_type   TEXT    NOT NULL,
      dim_id     TEXT    NOT NULL,
      usage_date TEXT    NOT NULL,         -- SettingTrafficTimezone 本地日期 YYYY-MM-DD
      up         INTEGER NOT NULL DEFAULT 0,
      down       INTEGER NOT NULL DEFAULT 0,
      PRIMARY KEY (server_id, dim_type, dim_id, usage_date)
  );
  CREATE INDEX IF NOT EXISTS idx_traffic_stats_daily_dim_date ON traffic_stats_daily(dim_type, dim_id, usage_date);
  ```
  `addTrafficStatTx` 内部：bucket 表 `INSERT ... ON CONFLICT(server_id, dim_type, dim_id, bucket_ts) DO UPDATE SET up=up+excluded.up, down=down+excluded.down`；daily 表同构（conflict 列换 usage_date）。

- [ ] **Step 1: 写失败测试** `traffic_stats_test.go`（fresh store 用 `Open(":memory:")`，见 `chain_traffic_test.go:13-18`；建 server/user 前置数据照抄该文件的 fixture 做法）：
  - `TestTrafficStatsBucketAccumulatesSameMinute`：同一计数器同一分钟内两次 `ApplyTrafficSnapshot`（sampledAt 相差 30s），断言 bucket 与 daily 均为两次增量之和。
  - `TestTrafficStatsUserDimUsesResolvedOwner`：`group:<uuid>:<chain_id>` 身份（fixture 照抄 `TestApplyTrafficSnapshotGroupIdentity`，`chain_traffic_test.go:150`）落桶后 `dim_id` 为归属用户 UUID。
  - `TestTrafficStatsInstanceResetMatchesTrafficTable`：复用 `TestTrafficSnapshotNewInstanceCountsFromZero` 场景（`chain_traffic_test.go:125`：先 Up 100，换 instanceID 再 Up 25），断言 user 维度桶累计 = 125，与 `traffic` 表一致。
  - `TestTrafficStatsDailyTimezoneBoundary`：设置 `SettingTrafficTimezone = "Asia/Shanghai"`，sampledAt 取 UTC 15:59 与 16:00（即本地 23:59/00:01），断言两行 daily 的 `usage_date` 不同。
  - 查询断言直接 `s.db.QueryRow`（同包测试可访问私有字段，先例见各 store 测试）。
- [ ] **Step 2: 跑测试确认失败** — `cd src/backend && go test ./internal/store/ -run TestTrafficStats -v`，期望 FAIL（表不存在/函数未定义）。
- [ ] **Step 3: 实现** — 表 DDL 进 Schema 常量、bump schemaVersion、新建 `traffic_stats.go` 写 `addTrafficStatTx`、在 `ApplyTrafficSnapshot` 循环内挂钩：每个已算出非零 delta 的 counter，按其身份维度调一次 `addTrafficStatTx`（`User != ""` → user 维度，dimID 用**已解析的 userUUID**；`EndpointID != 0` → endpoint；`NodeID != 0` → node；否则 hop）。`bucketTS = sampledAt.UTC().Truncate(time.Minute).Unix()`，`usageDate` 复用循环外的现有值。
- [ ] **Step 4: 跑测试确认通过** — 同 Step 2 命令，期望 PASS；再跑 `go test ./internal/store/` 全绿（不破坏现有 chain_traffic 测试）。
- [ ] **Step 5: Commit** — `git add -A && git commit -m "feat(stats): traffic stats bucket/daily tables written at telemetry ingest"`

### Task 2: Store — 查询与聚合

**Files:**
- Modify: `src/backend/internal/store/traffic_stats.go`（追加读侧）
- Test: `src/backend/internal/store/traffic_stats_test.go`（追加）

**Interfaces:**
- Produces（Task 5 handler 逐字依赖）:
  ```go
  type TrafficStatPoint struct {
      TS   int64 `json:"ts"`   // 粒度≤1h: 桶起始 Unix 秒；day: 本地当日零点 Unix 秒；month: 本地当月一日零点 Unix 秒
      Up   int64 `json:"up"`
      Down int64 `json:"down"`
  }
  type TrafficVolume struct {
      Up   int64 `json:"up"`
      Down int64 `json:"down"`
  }
  type TrafficStatGranularity string // "5m" | "15m" | "1h" | "day" | "month"
  // dim: "fleet"(=仅user维度) | "server"(=该服务器实体维度 node/chain_hop/endpoint) | "user"(=该用户跨服务器)
  func (s *Store) TrafficStatSeries(ctx context.Context, dim string, serverID int64, userUUID string, g TrafficStatGranularity, from, to time.Time) ([]TrafficStatPoint, error)
  type TrafficStatTopItem struct {
      ServerID int64  `json:"server_id"` // top_users 时为 0
      UserUUID string `json:"user_uuid"` // top_servers 时为 ""
      Label    string `json:"label"`     // servers.alias / users.name
      Up       int64  `json:"up"`
      Down     int64  `json:"down"`
  }
  type TrafficStatsOverview struct {
      Today       TrafficVolume `json:"today"`        // 用户维度，本地今日至今
      TodayPrev   TrafficVolume `json:"today_prev"`   // 用户维度，昨日 [startToday-24h, now-24h)
      Month       TrafficVolume `json:"month"`        // 用户维度，本月至今（daily）
      MonthPrev   TrafficVolume `json:"month_prev"`   // 用户维度，上月同进度
      TopServers  []TrafficStatTopItem `json:"top_servers"` // 实体维度，按月，GROUP BY server_id JOIN servers
      TopUsers    []TrafficStatTopItem `json:"top_users"`   // 用户维度，按月，GROUP BY dim_id JOIN users
  }
  func (s *Store) TrafficStatsOverview(ctx context.Context, topN int) (*TrafficStatsOverview, error)
  type TrafficStatBreakdownRow struct {
      DimType string `json:"dim_type"` // "user"|"node"|"chain_hop"|"endpoint"|"server"
      DimID   string `json:"dim_id"`
      Label   string `json:"label"`
      Up      int64  `json:"up"`
      Down    int64  `json:"down"`
  }
  // serverID>0: 该服务器全部维度行（user 行 JOIN users.name，node 行 JOIN nodes.name，
  //            endpoint/chain_hop 行 label 为空串，前端渲染"端点 #id"/"链路跳 #id"）
  // userUUID!="": 该用户 GROUP BY server_id（dim_type="server"，JOIN servers.alias）
  func (s *Store) TrafficStatBreakdown(ctx context.Context, serverID int64, userUUID string, from, to time.Time) ([]TrafficStatBreakdownRow, error)
  func (s *Store) ServerTrafficBuckets(ctx context.Context, serverID int64, from, to time.Time) ([]TrafficStatPoint, error) // 明细桶逐分钟，实体维度合计
  ```
  聚合规则：`5m/15m/1h` → 明细桶 `GROUP BY (bucket_ts/window)*window`（window=300/900/3600）；`day/month` → daily 表按本地日期/`substr(usage_date,1,7)` 分组。排序按 TS 升序。breakdown 按 up+down 降序 LIMIT 50。
- [ ] **Step 1: 写失败测试**（seed 一律走 `ApplyTrafficSnapshot` 传可控 sampledAt）：
  - `TestTrafficStatSeries5mAlignment`：两分钟数据 5m 粒度聚合为一桶。
  - `TestTrafficStatSeriesFleetUserDimOnly`：同 server 写 user 行 + node 行各 1GB，`dim=fleet` 只返回 1GB（Review Focus #4）。
  - `TestTrafficStatSeriesServerEntityDim`：`dim=server` 返回实体维度合计（user 行不计入）。
  - `TestTrafficStatOverviewSameProgress`：构造昨日与今日数据，断言 TodayPrev 恰为昨日同时刻窗口（今日 10:00 查询时不含昨日 10:00 后数据）。
  - `TestTrafficStatOverviewTopJoinsLabels`：top_servers label=servers.alias、top_users label=users.name。
  - `TestTrafficStatBreakdownByServerAndUser`：双向分组与 label。
  - `TestTrafficStatEmptyRangeReturnsEmpty`：空范围返回 `[]TrafficStatPoint{}` 且无 error（Review Focus #5 前半）。
- [ ] **Step 2: 跑测试确认失败** — `go test ./internal/store/ -run 'TestTrafficStat(Series|Overview|Breakdown)' -v`，期望编译 FAIL（方法未定义）。
- [ ] **Step 3: 实现** — 全部 SQL 放 `traffic_stats.go`；时间窗计算用 `trafficLocation`；月同进度公式：`MonthPrev = SUM(daily 上月 user 维度) - SUM(buckets [上月start+elapsed, 本月start))`，`elapsed = now - 本月start`（该窗口桶龄 ≤ 31 天，Task 3 的保留期最小值 31 保证其存在）。
- [ ] **Step 4: 跑测试确认通过** — Step 2 命令 PASS + `go test ./internal/store/` 全绿。
- [ ] **Step 5: Commit** — `feat(stats): aggregate queries for series/overview/breakdown`

### Task 3: Store — 保留清理与 scheduler 任务

**Files:**
- Modify: `src/backend/internal/store/traffic_stats.go`（`DeleteExpiredTrafficStatBuckets`）
- Modify: `src/backend/internal/store/settings.go`（常量）
- Modify: `src/backend/internal/panel/panel.go:261-308`（registerCoreTasks）
- Modify: `src/backend/internal/panel/metrics.go` 或新建 `stats_retention.go`（cleanup 函数）
- Modify: `src/frontend/src/pages/RuntimeMonitor.tsx:44-55`（taskLabels 加 `'traffic_stats.retention': '流量统计清理'`）
- Test: `src/backend/internal/store/traffic_stats_test.go`（追加）

**Interfaces:**
- Produces:
  ```go
  const SettingTrafficStatsRetentionDays = "traffic_stats_retention_days" // settings.go
  const DefaultTrafficStatsRetentionDays = 60
  func (s *Store) TrafficStatsRetentionDays(ctx context.Context) (int, error) // 读设置，缺省/非法回落默认
  func (s *Store) DeleteExpiredTrafficStatBuckets(ctx context.Context, retentionDays int) (int64, error) // DELETE WHERE bucket_ts < now-days*86400
  ```
- [ ] **Step 1: 写失败测试** `TestDeleteExpiredTrafficStatBucketsKeepsDaily`：seed 新旧两分钟桶（旧桶 61 天前）→ delete(60) → 旧桶消失、daily 行仍在、新桶不受影响（Review Focus #5 后半）。
- [ ] **Step 2: 确认失败** — `go test ./internal/store/ -run TestDeleteExpired -v` FAIL。
- [ ] **Step 3: 实现** store 方法 + 常量；panel 侧照 `metrics.retention` 原样注册（`panel.go:268-272` 模板）：
  ```go
  s.scheduler.Register(scheduler.ScheduledTask{
      Name: "traffic_stats.retention", RunOnStart: false, Timeout: time.Minute,
      Trigger: func(context.Context) scheduler.TaskTrigger { return scheduler.IntervalTrigger(time.Hour) },
      Run:     s.cleanupTrafficStats,
  })
  ```
  `cleanupTrafficStats`：读 `TrafficStatsRetentionDays` → `DeleteExpiredTrafficStatBuckets`。同步在 RuntimeMonitor 的 taskLabels map 加中文标签。
- [ ] **Step 4: 验证** — store 测试 PASS；`go build ./...` 通过。
- [ ] **Step 5: Commit** — `feat(stats): hourly retention cleanup task with retention setting`

### Task 4: 保留期设置项端到端

**Files:**
- Modify: `src/backend/internal/panel/settings.go`（DTO/校验/持久化，`SettingTrafficTimezone` 全流程为模板：DTO `:85`、GET 填充 `:126`、请求字段 `:220`、校验 `:343-348`、set `:463`）
- Modify: `src/frontend/src/pages/Settings.tsx`（表单字段）
- Test: 面板测试追加到 `src/backend/internal/panel/settings_panel_short_test.go` 风格的新/既有文件

**Interfaces:**
- Produces: settings DTO 新字段 `TrafficStatsRetentionDays int \`json:"traffic_stats_retention_days"\``（GET 缺省填 60）；更新请求同名字段；校验整数 31–3650，越界 400 `"流量统计保留天数须为 31-3650 天"`；GET `traffic_timezone` 同款 `firstNonZero` 填充。
- [ ] **Step 1: 写失败测试**：GET 默认 60；合法值 90 持久化后回读 90；30 → 400。
- [ ] **Step 2: 确认失败** — `go test ./internal/panel/ -run TestSettings -v` FAIL。
- [ ] **Step 3: 实现** 后端 + Settings.tsx 加数字输入（label「流量统计保留（天）」，占位说明「明细数据保留天数，最小 31」），随现有表单提交。
- [ ] **Step 4: 验证** — Step 2 PASS；`cd src/frontend && npm run test && npx tsc -b` 通过。
- [ ] **Step 5: Commit** — `feat(stats): retention days setting end-to-end`

### Task 5: Panel API — 5 个端点 + openapi 契约

**Files:**
- Create: `src/backend/internal/panel/stats.go`（handlers）
- Modify: `src/backend/internal/panel/panel.go:311-504`（RegisterRoutes）
- Modify: `docs/openapi.yaml`
- Test: `src/backend/internal/panel/stats_test.go`（panel 测试先例见 `runtime_test.go`/`chains_test.go`：内存 store 构造 Server + httptest 调 handler）

**Interfaces:**
- Consumes: Task 2 全部 store 方法；`logging.OperationStore.List(ctx, OperationFilter{Severity, From}, 1, 0)` 的 total（错误计数）；`panel/runtime.go` 中 agents online/total 的同一数据源。
- Produces: 路由（全部 `polledRead` + `AllowedQuery` 白名单，`registerRPC` 模板见 `panel.go:406-408`）：
  - `GET /api/stats/traffic/overview` → handler 内定义响应 DTO：`type statsOverviewDTO struct { *store.TrafficStatsOverview; AgentsOnline int \`json:"agents_online"\`; AgentsTotal int \`json:"agents_total"\` }`（匿名嵌入打平 JSON；online/total 复用 `panel/runtime.go` 的同一数据源）
  - `GET /api/stats/traffic/series?dim=fleet|server|user&server_id&user_uuid&granularity=5m|15m|1h|day|month&from&to`（from/to Unix 秒，可选）→ `[]TrafficStatPoint`
  - `GET /api/stats/traffic/breakdown?server_id|user_uuid&from&to` → `[]TrafficStatBreakdownRow`
  - `GET /api/stats/traffic/server-history?server_id&hours=24`（1–168）→ `[]TrafficStatPoint`
  - `GET /api/stats/errors/summary` → `{"today_error_count": int}`（`SeverityError` + From=本地今日零点，From 格式与 `panel/logs.go` 的 from 参数一致）
  - 参数校验：dim/granularity 枚举外 → 400；dim=server 缺 server_id、dim=user 缺 user_uuid → 400；from≥to → 400。series 无 from/to 时默认窗：5m/15m→6h、1h→24h、day→30 天、month→12 个月。
  - openapi.yaml 每条照 `/api/chain/get-traffic-history` 条目风格（`docs/openapi.yaml:389-396`），operationId：`statsTrafficOverview`/`statsTrafficSeries`/`statsTrafficBreakdown`/`statsTrafficServerHistory`/`statsErrorsSummary`。
- [ ] **Step 1: 写失败测试** `stats_test.go`：seed `ApplyTrafficSnapshot` 后调各 handler 断言 JSON 形状与数值；枚举外 granularity → 400；dim=server 无 server_id → 400；errors summary 计数正确。
- [ ] **Step 2: 确认失败** — `go test ./internal/panel/ -run TestStats -v` FAIL。
- [ ] **Step 3: 实现** handlers + 注册 + openapi 五条，然后 `cd src/frontend && npm run generate:api` 重新生成契约。
- [ ] **Step 4: 验证** — Step 2 PASS；`cd src/backend && go test ./...` 全绿；`npm run check:api` 通过。
- [ ] **Step 5: Commit** — `feat(stats): panel statistics endpoints and openapi contract`

### Task 6: 前端类型与 api 方法

**Files:**
- Modify: `src/frontend/src/lib/types.ts`、`src/frontend/src/lib/api.ts`

**Interfaces:**
- Consumes: Task 5 生成的 `api-contract.generated.ts` 路径类型。
- Produces（Task 8/9/11 逐字依赖）:
  ```ts
  export interface TrafficVolume { up: number; down: number }
  export interface TrafficStatPoint { ts: number; up: number; down: number }
  export type TrafficStatGranularity = '5m' | '15m' | '1h' | 'day' | 'month'
  export interface TrafficStatTopItem { server_id: number; user_uuid: string; label: string; up: number; down: number }
  export interface TrafficStatsOverview {
    today: TrafficVolume; today_prev: TrafficVolume; month: TrafficVolume; month_prev: TrafficVolume
    agents_online: number; agents_total: number
    top_servers: TrafficStatTopItem[]; top_users: TrafficStatTopItem[]
  }
  export interface TrafficStatBreakdownRow { dim_type: 'user' | 'node' | 'chain_hop' | 'endpoint' | 'server'; dim_id: string; label: string; up: number; down: number }
  ```
  api 方法（模板 `serverMetricHistory`，`api.ts:116-126`）：
  ```ts
  statsTrafficOverview: () => requester.get<TrafficStatsOverview>('/api/stats/traffic/overview', undefined, { display: 'silent' })
  statsTrafficSeries: (p: { dim: 'fleet' | 'server' | 'user'; server_id?: number; user_uuid?: string; granularity: TrafficStatGranularity; from?: number; to?: number }) => requester.get<TrafficStatPoint[]>('/api/stats/traffic/series', { ...p }, { display: 'silent' })
  statsTrafficBreakdown: (p: { server_id?: number; user_uuid?: string; from?: number; to?: number }) => requester.get<TrafficStatBreakdownRow[]>('/api/stats/traffic/breakdown', { ...p }, { display: 'silent' })
  statsServerTrafficHistory: (serverId: number, hours = 24) => requester.get<TrafficStatPoint[]>('/api/stats/traffic/server-history', { server_id: serverId, hours }, { display: 'silent' })
  statsErrorsSummary: () => requester.get<{ today_error_count: number }>('/api/stats/errors/summary', undefined, { display: 'silent' })
  ```
- [ ] **Step 1: 实现类型与五个方法**（本任务无独立测试，由类型系统 + check:api 把关）。
- [ ] **Step 2: 验证** — `cd src/frontend && npm run check:api && npx tsc -b` 通过。
- [ ] **Step 3: Commit** — `feat(frontend): stats api methods and types`

### Task 7: RuntimeMonitor Tab 化

**Files:**
- Modify: `src/frontend/src/pages/RuntimeMonitor.tsx`

**Interfaces:**
- Consumes: `ui/tabs`（`Tabs/TabsList/TabsTrigger/TabsContent`，`TabsList variant="line"`）；受控 Tab 先例 `LogsLayout.tsx:7-28`。
- Produces: 页面结构 `Tabs value={tab}`（`'panel' | 'agents'`，useState 默认 `'panel'`）；现有全部 JSX（topline/hero 起全部内容）原样移入 `TabsContent value="panel"`；`!snapshot` 早退留在 Tabs 之外不变；新建 `TabsContent value="agents"` 本任务暂渲染占位 `<div className="cg-runtime-tab-placeholder">被控统计（构建中）</div>`。
- [ ] **Step 1: 实现** — 最小改动：`export default function RuntimeMonitor()` 顶部加 tab state 与 Tabs 外壳，内容下沉为同文件内部组件 `PanelRuntimeSection()`（不新建文件，避免 693 行大搬家）。
- [ ] **Step 2: 验证** — `npx tsc -b` 通过、`npm run test` 全绿（现有测试不回归）；切到 agents Tab 后 panel 轮询 effect 随卸载停止（结构上由 TabsContent 卸载保证，代码走查确认）。
- [ ] **Step 3: Commit** — `feat(frontend): runtime monitor tab shell (panel / agents)`

### Task 8: 统计控制器 hook 与图表 option builder

**Files:**
- Create: `src/frontend/src/pages/runtime/use-traffic-stats.ts`
- Create: `src/frontend/src/pages/runtime/traffic-chart.ts`
- Test: `src/pages/runtime/use-traffic-stats.test.ts`、`src/pages/runtime/traffic-chart.test.ts`（node 环境 hook 测试先例：`use-chain-form.test.ts` 的 `vi.mock('react')` 手搓状态法 + `vi.mock('@/lib/api')`）

**Interfaces:**
- Consumes: Task 6 api 方法、`usePolling(load, requestRef, intervalMs)`（`use-polling.ts:7-28`）、`chartThemeColors()`（`costs-shared.tsx:32-44`）、`Chart`（`echarts.tsx:25`，props `{ option: ChartOption; className?: string }`，已注册 BarChart 无需加线图）。
- Produces（Task 9/11 依赖）:
  ```ts
  export type TrafficStatsGranularity = ... // 复用 types.ts 的
  export function useTrafficStats({ onError }: { onError: (msg: string) => void }): {
    overview: TrafficStatsOverview | null
    points: TrafficStatPoint[]
    granularity: TrafficStatGranularity      // 默认 '1h'
    loading: boolean
    setGranularity: (g: TrafficStatsGranularity) => void   // 切换后立即重拉 series
    errors: { today_error_count: number } | null
  } // overview+errors 60s 轮询（usePolling, silent）；series 随 granularity 变化重拉
  export function trafficSeriesWindow(g: TrafficStatGranularity, now = Date.now()): { from: number; to: number } // 5m/15m→6h、1h→24h、day→30d、month→365d
  export function buildTrafficBarOption(points: TrafficStatPoint[], g: TrafficStatGranularity, colors: ChartThemeColors): ChartOption // x=按粒度格式化(Intl zh-CN), 双系列堆叠柱 上传/下载, 点数>60 开 dataZoom, tooltip/axis 颜色取 colors, 单位字节（复用 formatBytes 风格 formatter，若 lib/format.ts 无则本文件内实现 GB/MB/KB/B）
  ```
- [ ] **Step 1: 写失败测试**：`trafficSeriesWindow` 各粒度窗口断言；`buildTrafficBarOption` 断言 series 名（'上传'/'下载'）、stack、数据点数；`useTrafficStats` 断言初始加载调 `statsTrafficOverview+statsTrafficSeries(dim:'fleet',granularity:'1h')`、`setGranularity('day')` 触发 series 重拉参数为 day 窗口。
- [ ] **Step 2: 确认失败** — `npm run test` 新用例 FAIL。
- [ ] **Step 3: 实现**（hook 内部：`const req = useRef(0)` + `usePolling(load, req, 60000)`；granularity 变化用独立 useEffect 重拉 series）。
- [ ] **Step 4: 确认通过** — `npm run test` 全绿。
- [ ] **Step 5: Commit** — `feat(frontend): traffic stats controller hook and bar option builder`

### Task 9: TrafficStatsDialog 下钻弹窗

**Files:**
- Create: `src/frontend/src/components/traffic-stats/use-traffic-stats-dialog.ts`
- Create: `src/frontend/src/components/traffic-stats/TrafficStatsDialog.tsx`
- Test: `src/components/traffic-stats/use-traffic-stats-dialog.test.ts`

**Interfaces:**
- Consumes: Task 6/8（api、`buildTrafficBarOption`、`Chart`）；`ui/dialog`（用法照 `TrafficHistoryDialog.tsx:225,237`：`open={scope !== null}` + `onOpenChange` 关闭回调）。
- Produces（Task 10/11 依赖）:
  ```ts
  export interface TrafficStatsScope { kind: 'server' | 'user'; serverId?: number; serverName?: string; userUuid?: string; userName?: string }
  export type TrafficStatsRangeKey = '24h' | '7d' | '30d' | 'custom'
  export function useTrafficStatsDialog({ onError }: { onError: (msg: string) => void }): {
    scope: TrafficStatsScope | null
    rangeKey: TrafficStatsRangeKey   // 默认 '7d'
    customRange: { from: string; to: string }   // 'YYYY-MM-DD'，仅 rangeKey='custom' 使用；setCustomRange 变更即重拉
    setCustomRange: (r: { from: string; to: string }) => void
    points: TrafficStatPoint[]
    rows: TrafficStatBreakdownRow[]
    loading: boolean
    open: (scope: TrafficStatsScope) => void
    close: () => void
    setRange: (r: TrafficStatsRangeKey) => void
    drillRow: (row: TrafficStatBreakdownRow) => void // server 视图点 user 行→user 视图；user 视图点 server 行→server 视图；其余行忽略
  } // range→查询参数: '24h'→{granularity:'15m',from:now-24h}, '7d'→{granularity:'1h',from:now-7d}, '30d'→{granularity:'day',from:now-30d}, 'custom'→{granularity:'day',from:custom.from当日零点,to:custom.to当日末}（非法/倒置区间置 onError 不发请求）
  export function TrafficStatsDialog({ controller }: { controller: TrafficStatsDialogController }) // ReturnType 别名同 use-traffic-history.ts:95 风格
  ```
  Dialog 内容：标题（server 用 serverName、user 用 userName）+ range 四个 `TabsTrigger`（近24h/近7天/近30天/自定义，自定义选中时显示两个 `<Input type="date">`）+ `Chart` + 分解表（`ui/table`，列：名称/维度标签、↑、↓；user 行 label 空时渲染 `用户 ${dim_id.slice(0,8)}`，node/endpoint/chain_hop 渲染 `节点|端点|链路跳 #${dim_id}`，server 行用 label）；可下钻行（dim_type user/server）加「下钻 ›」按钮调 `drillRow`。
- [ ] **Step 1: 写失败测试**：open(server) 拉取 series(dim:'server')+breakdown(server_id)；setRange('30d') 参数变 day 粒度；setRange('custom')+setCustomRange 倒置区间 → onError 且不发请求、合法区间 → day 粒度带 from/to；drillRow(user 行) 后 scope.kind='user' 且重拉 breakdown(user_uuid)；close 置空。
- [ ] **Step 2: 确认失败** — `npm run test` 新用例 FAIL。
- [ ] **Step 3: 实现** controller + Dialog UI。
- [ ] **Step 4: 确认通过** — `npm run test` 全绿 + `npx tsc -b`。
- [ ] **Step 5: Commit** — `feat(frontend): traffic stats drill-down dialog`

### Task 10: AgentsStatsTab 页面组装

**Files:**
- Create: `src/frontend/src/pages/runtime/AgentsStatsTab.tsx`
- Modify: `src/frontend/src/pages/RuntimeMonitor.tsx`（占位替换为 `<AgentsStatsTab />`，lazy 可选——照 App.tsx 惰性惯例不强求）
- Modify: `src/frontend/src/pages/runtime-monitor.css` 或新增 `src/frontend/src/pages/runtime/agents-stats.css`（类名前缀 `cg-rstats-*`，风格对齐 runtime-monitor.css）

**Interfaces:**
- Consumes: Task 8 `useTrafficStats`、Task 9 `useTrafficStatsDialog`+`TrafficStatsDialog`、wouter `Link`。
- Produces: 完整 Tab UI：概要卡行（今日 ↑↓、本月 ↑↓、环比两卡（`today_prev.up+down>0` 时算百分比，否则显示「—」，tooltip 注明"同进度对比"）、在线 Agent `agents_online/agents_total`、今日错误卡 `<Link href="/logs/operations?severity=error">`）；粒度 `Tabs`（5分钟/15分钟/1小时/天/月）；`Chart` 主图；Top 服务器 / Top 用户 双列表（行点击 `dialogController.open({kind:'server',...})` / `{kind:'user',...}`，label 空用 dim 兜底）；页脚一行口径说明「全网为用户端到端口径；单服务器为实体口径，两者含链路时不相等」。
- [ ] **Step 1: 实现**（数据全来自 hook，组件保持展示层；异常用现有 `Notice` 模式）。
- [ ] **Step 2: 验证** — `npx tsc -b` + `npm run test` 全绿；手动/代码走查：切换粒度重拉、Top 行点击开弹窗、错误卡跳转带 query。
- [ ] **Step 3: Commit** — `feat(frontend): agents traffic stats tab`

### Task 11: 服务器详情 Sheet 流量图与下钻入口

**Files:**
- Modify: `src/frontend/src/components/ServerMonitor.tsx`

**Interfaces:**
- Consumes: Task 6 `statsServerTrafficHistory`、Task 9 `useTrafficStatsDialog`+`TrafficStatsDialog`、现有 `TrendChart`（props 见 `ServerMonitor.tsx:686-706`：`title/unit/series: ChartSeries[]/timestamps/timezone/expanded`；`ChartSeries.color` 仅 `'success'|'info'|'warning'`）、`DetailSheet` 的 60s 轮询 effect（`:926-941` 模板）。
- Produces: `ServerMonitorGrid` 内挂 `useTrafficStatsDialog` 并渲染 `<TrafficStatsDialog controller={...} />`；`DetailSheet` 接收 `onOpenTrafficStats: (server: Server) => void`；DetailSheet 内：①新 state `trafficHistory: TrafficStatPoint[]`，轮询 effect 照 metric-history 模板调 `statsServerTrafficHistory(server.id)`；②第 6 张 `TrendChart title="流量速率" unit="KB/s" series=[{label:'上传',color:'success',values:upRate},{label:'下载',color:'info',values:downRate}]`，`upRate[i] = points[i].up/60/1024`（KB/s，null 透传对齐 timestamps 长度——与 tx/rx 推导 `:943-953` 同法）；③图上方 ghost Button「流量统计」调 `onOpenTrafficStats(server)`。
- [ ] **Step 1: 实现**（注意 `Server` 类型来自 `lib/types.ts`，Sheet 打开才轮询、关闭即清 effect）。
- [ ] **Step 2: 验证** — `npx tsc -b` + `npm run test` + `npm run lint` 全绿；走查 Sheet 打开 60s 轮询一次、按钮开弹窗 scope 正确。
- [ ] **Step 3: Commit** — `feat(frontend): server detail traffic chart and stats entry`

### Task 12: OperationLogs severity URL 预筛

**Files:**
- Create: `src/frontend/src/lib/log-severity-param.ts`
- Test: `src/frontend/src/lib/log-severity-param.test.ts`
- Modify: `src/frontend/src/pages/OperationLogs.tsx:96`（`const [severity, setSeverity] = useState<LogSeverity | ''>('')` 改为从 URL 初始化）

**Interfaces:**
- Produces:
  ```ts
  export function initialSeverityFromSearch(search: string): LogSeverity | '' // 解析 ?severity=，仅接受 'info'|'warning'|'error'，否则 ''
  ```
  OperationLogs：`import { useSearch, useLocation } from 'wouter'`；初始 state 用 helper；挂载后 `navigate('/logs/operations', { replace: true })` 剥掉参数（避免刷新/重置滤镜困扰）。
- [ ] **Step 1: 写失败测试**：`?severity=error`→'error'；`?severity=bogus`/空→''。
- [ ] **Step 2: 确认失败** → **Step 3: 实现** → **Step 4: `npm run test` 全绿 + `npx tsc -b`**。
- [ ] **Step 5: Commit** — `feat(frontend): operation logs severity prefilter via url param`

### Task 13: 全量验证

- [ ] **Step 1** — `cd src/backend && go test ./...` 全绿。
- [ ] **Step 2** — `cd src/frontend && npm run test && npm run lint && npm run build`（含 check:api 与 chunk 体积守卫）全绿。
- [ ] **Step 3** — 对照 Spec 逐节走查覆盖：口径两卡、粒度切换、下钻双向、Top 榜、详情流量图、错误卡跳转、保留设置、非目标未越界（agent 无改动：`git diff --stat src/agent src/shared` 为空）。
- [ ] **Step 4** — `git log --oneline` 核对提交序列完整；至此分支可交 finishing-a-development-branch 流程。

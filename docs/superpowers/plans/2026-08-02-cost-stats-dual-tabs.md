# 成本统计双口径（已生效成本 / 计算成本）实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 将成本统计页拆为两个 tab：「已生效成本」（现有按服务期摊算的实现，后端字段改名 `actual_*`）与「计算成本」（新增：日成本 × 周期天数的估算，字段 `estimated_*`）。

**Architecture:** 后端在 `cost_stats.go` 抽取共享装配核心 `loadStatsRows`（元数据 + `convertCosts` 两套日成本），两个 handler（`handleBillingStats` / `handleEstimatedBillingStats`）复用其按口径分支计算单元格。前端 `Costs.tsx` 顶部加 Tabs，现有内容抽为 `ActualCostsTab`，新增 `EstimatedCostsTab`，各自独立请求。前端类型与 OpenAPI 契约同步改名/增补。

**Tech Stack:** Go（backend/panel）、React 19 + ECharts + base-ui Tabs（frontend）、openapi-typescript 契约生成。

## Global Constraints

- 已生效成本计算逻辑不变，仅 DTO JSON 字段改名：`costs_public→actual_costs_public`、`costs_custom→actual_costs_custom`、`totals_public→actual_totals_public`、`totals_custom→actual_totals_custom`
- 计算成本参与范围：`enabled=true` 且 `status != expired`；不按服务期裁剪
- 计算成本单元格：`round(daily_rat × 周期天数)`，周期天数 day=1 / month=30 / year=365
- 前端不做任何货币换算；金额复用 divisor 规则（JPY/KRW/ISK → 1，其余 → 100）
- 后端所有路由必须与 `docs/openapi.yaml` 同步（`contract_test.go` 强制）
- 请求客户端统一走现有 `requester`；不新增依赖
- 验证命令：后端 `go test ./src/backend/...`（仓库根目录）；前端在 `src/frontend` 下 `bun run lint`、`bun run build`（含 API 契约 --check）、`bun run test`

---

### Task 1: 后端 — 共享装配核心 + 已生效成本字段改名

**Files:**
- Modify: `src/backend/internal/panel/cost_stats.go`
- Modify: `src/backend/internal/panel/cost_stats_test.go`

**Interfaces:**
- Produces: `billingServerStatsBase`（嵌入的元数据基座）、`billingStatsMeta`（嵌入的响应头基座）、`billingServerStatsDTO`（字段 `ActualCostsPublic`/`ActualCostsCustom`）、`billingStatsDTO`（字段 `ActualTotalsPublic`/`ActualTotalsCustom`）、`statsRow`（装配结果：`billing`/`base`/`dailyPublic`/`dailyCustom`/`customDiffers`/`rateDate`）、`loadStatsRows(ctx, participate func(store.ServerBilling) bool) ([]statsRow, error)`、`periodDays(gran string) int64`

- [ ] **Step 1: 更新测试引用新字段名（先编译失败）**

在 `src/backend/internal/panel/cost_stats_test.go` 中全部替换：
- `usd.CostsPublic` → `usd.ActualCostsPublic`（2 处：第 301、332 行）
- `cny.CostsPublic` → `cny.ActualCostsPublic`（2 处：第 309、419 行）
- `dto.TotalsPublic` → `dto.ActualTotalsPublic`（2 处：第 314、422 行）
- `dto.TotalsCustom` → `dto.ActualTotalsCustom`（第 365 行）
- `dto.Servers[i].CostsCustom` → `dto.Servers[i].ActualCostsCustom`（2 处：第 377、394 行）
- `pubDTO.Servers[i].CostsCustom != nil || pubDTO.TotalsCustom != nil` → `pubDTO.Servers[i].ActualCostsCustom != nil || pubDTO.ActualTotalsCustom != nil`

- [ ] **Step 2: 运行测试确认编译失败**

Run: `go test ./src/backend/internal/panel/ -run 'TestBillingStats|TestEstimated'`
Expected: FAIL — `billingStatsDTO has no field CostsPublic`（结构体尚未改名）

- [ ] **Step 3: 重构 cost_stats.go（改名 + 抽取共享核心）**

将 `src/backend/internal/panel/cost_stats.go` 中第 30-62 行的两个 DTO 定义替换为基座 + 派生结构：

```go
// billingServerStatsBase 是两种成本统计口径共用的单台服务器元数据。
type billingServerStatsBase struct {
	ServerID         int64  `json:"server_id"`
	Alias            string `json:"alias"`
	CountryCode      string `json:"country_code"`
	Location         string `json:"location"`
	Currency         string `json:"currency"`
	AmountMinor      int64  `json:"amount_minor"`
	IntervalCount    int    `json:"interval_count"`
	IntervalUnit     string `json:"interval_unit"`
	ServiceStartedOn string `json:"service_started_on"`
	Status           string `json:"status"`
	DaysActive       int    `json:"days_active"`
	DailyMinor       int64  `json:"daily_minor"`
	DailyCustomMinor int64  `json:"daily_custom_minor,omitempty"`
}

// billingServerStatsDTO 是已生效成本统计中单台服务器的周期成本序列（§成本统计设计）。
type billingServerStatsDTO struct {
	billingServerStatsBase
	ActualCostsPublic []int64 `json:"actual_costs_public"`
	ActualCostsCustom []int64 `json:"actual_costs_custom,omitempty"`
}

// estimatedBillingServerStatsDTO 是计算成本统计中单台服务器的周期成本序列。
type estimatedBillingServerStatsDTO struct {
	billingServerStatsBase
	EstimatedCostsPublic []int64 `json:"estimated_costs_public"`
	EstimatedCostsCustom []int64 `json:"estimated_costs_custom,omitempty"`
}

// billingStatsMeta 是两种成本统计响应共用的头部字段（匿名嵌入使 JSON 平铺）。
type billingStatsMeta struct {
	ReportingCurrency string   `json:"reporting_currency"`
	Granularity       string   `json:"granularity"`
	From              string   `json:"from"`
	To                string   `json:"to"`
	RateMode          string   `json:"rate_mode"`
	RateDate          string   `json:"rate_date,omitempty"`
	CustomAvailable   bool     `json:"custom_available"`
	Periods           []string `json:"periods"`
}

// billingStatsDTO 是 GET /api/billing/stats 的响应（已生效成本）。
type billingStatsDTO struct {
	billingStatsMeta
	Servers            []billingServerStatsDTO `json:"servers"`
	ActualTotalsPublic []int64                 `json:"actual_totals_public"`
	ActualTotalsCustom []int64                 `json:"actual_totals_custom,omitempty"`
}

// estimatedBillingStatsDTO 是 GET /api/billing/stats/estimated 的响应（计算成本）。
type estimatedBillingStatsDTO struct {
	billingStatsMeta
	Servers               []estimatedBillingServerStatsDTO `json:"servers"`
	EstimatedTotalsPublic []int64                          `json:"estimated_totals_public"`
	EstimatedTotalsCustom []int64                          `json:"estimated_totals_custom,omitempty"`
}
```

在 `intervalDailyCost` 函数（第 78-84 行）之后新增：

```go
// periodDays 返回粒度对应的固定周期天数：day=1、month=30、year=365（与 intervalDays 口径一致）。
func periodDays(gran string) int64 {
	switch gran {
	case costGranDay:
		return 1
	case costGranMonth:
		return 30
	case costGranYear:
		return 365
	}
	return 0
}

// statsRow 是成本统计中单台参与服务器的装配结果（两种口径共用）。
type statsRow struct {
	billing       store.ServerBilling
	base          billingServerStatsBase
	dailyPublic   *big.Rat
	dailyCustom   *big.Rat
	customDiffers bool
	rateDate      string
}

// loadStatsRows 装配参与统计的服务器行：元数据 + convertCosts 两套日成本（big.Rat 精确值），
// 按 server_id 升序稳定排列。participate 在 enabled 之上决定口径参与范围。
func (s *Server) loadStatsRows(ctx context.Context, participate func(b store.ServerBilling) bool) ([]statsRow, error) {
	servers, err := s.st.ListServers(ctx)
	if err != nil {
		return nil, err
	}
	billing, err := s.st.ServerBillingMap(ctx)
	if err != nil {
		return nil, err
	}
	serversByID := make(map[int64]store.Server, len(servers))
	for _, srv := range servers {
		serversByID[srv.ID] = srv
	}
	var rows []statsRow
	for _, b := range billing {
		if !b.Enabled || !participate(b) {
			continue
		}
		srv, ok := serversByID[b.ServerID]
		if !ok {
			continue
		}
		public, custom, err := s.convertCosts(ctx, b.AmountMinor, b.Currency)
		if err != nil {
			return nil, err
		}
		dailyPublic := intervalDailyCost(public.AmountMinor, b.IntervalCount, b.IntervalUnit)
		row := statsRow{
			billing:     b,
			dailyPublic: dailyPublic,
			rateDate:    public.RateDate,
			base: billingServerStatsBase{
				ServerID: b.ServerID, Alias: srv.Alias, CountryCode: srv.CountryCode, Location: srv.Location,
				Currency: b.Currency, AmountMinor: b.AmountMinor, IntervalCount: b.IntervalCount,
				IntervalUnit: b.IntervalUnit, ServiceStartedOn: b.ServiceStartedOn, Status: b.Status,
				DailyMinor: roundRat(dailyPublic),
			},
		}
		if custom != nil {
			row.dailyCustom = intervalDailyCost(custom.AmountMinor, b.IntervalCount, b.IntervalUnit)
			row.base.DailyCustomMinor = roundRat(row.dailyCustom)
			row.customDiffers = custom.AmountMinor != public.AmountMinor
		}
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].base.ServerID < rows[j].base.ServerID })
	return rows, nil
}
```

将 `handleBillingStats` 整体替换为（计算逻辑不变；custom 一套改为先扫描 `customDiffers` 再统一装配，修复原实现中"排序靠前的服务器在 custom 模式下缺 `costs_custom`"的边界问题）：

```go
// handleBillingStats 处理 GET /api/billing/stats（已生效成本）：对所有启用统计计费的服务器按
// 服务期与日/月/年周期重叠摊算成本（复用 convertCosts 换算到统计币种），返回周期 × 服务器矩阵。
func (s *Server) handleBillingStats(w http.ResponseWriter, r *http.Request) {
	loc := s.inspectionLocation(r.Context())
	from, to, gran, mode, err := parseBillingStatsQuery(r.URL.Query(), loc)
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}
	rows, err := s.loadStatsRows(r.Context(), func(store.ServerBilling) bool { return true })
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	periods := costPeriods(from, to, gran)
	dto := billingStatsDTO{
		billingStatsMeta: billingStatsMeta{
			ReportingCurrency: s.reportingCurrency(r.Context()),
			Granularity:       gran,
			From:              from.Format("2006-01-02"),
			To:                to.Format("2006-01-02"),
			RateMode:          mode,
			Periods:           periods,
		},
		Servers:            []billingServerStatsDTO{},
		ActualTotalsPublic: make([]int64, len(periods)),
	}
	for _, row := range rows {
		if row.customDiffers {
			dto.CustomAvailable = true
		}
		if dto.RateDate == "" && row.rateDate != "" {
			dto.RateDate = row.rateDate
		}
	}
	var totalsCustom []int64
	if mode == costModeCustom && dto.CustomAvailable {
		totalsCustom = make([]int64, len(periods))
	}
	for _, row := range rows {
		start, err := time.ParseInLocation("2006-01-02", row.base.ServiceStartedOn, loc)
		if err != nil {
			continue
		}
		end := billingServiceEnd(row.billing, loc)
		item := billingServerStatsDTO{
			billingServerStatsBase: row.base,
			ActualCostsPublic:      make([]int64, len(periods)),
		}
		var costsCustom []int64
		if totalsCustom != nil {
			costsCustom = make([]int64, len(periods))
		}
		for i, label := range periods {
			pStart, pEnd := periodBounds(label, gran, loc)
			days := overlapDays(start, end, pStart, pEnd)
			if days <= 0 {
				continue
			}
			item.DaysActive += days
			cost := new(big.Rat).Mul(row.dailyPublic, new(big.Rat).SetInt64(int64(days)))
			item.ActualCostsPublic[i] = roundRat(cost)
			dto.ActualTotalsPublic[i] += item.ActualCostsPublic[i]
			if costsCustom != nil && row.dailyCustom != nil {
				cost := new(big.Rat).Mul(row.dailyCustom, new(big.Rat).SetInt64(int64(days)))
				costsCustom[i] = roundRat(cost)
				totalsCustom[i] += costsCustom[i]
			}
		}
		if costsCustom != nil {
			if row.dailyCustom == nil {
				costsCustom = append([]int64(nil), item.ActualCostsPublic...)
			}
			item.ActualCostsCustom = costsCustom
		}
		dto.Servers = append(dto.Servers, item)
	}
	if totalsCustom != nil {
		dto.ActualTotalsCustom = totalsCustom
	}
	writeJSON(w, http.StatusOK, dto)
}
```

- [ ] **Step 4: 运行后端测试确认通过**

Run: `go test ./src/backend/...`
Expected: PASS（含 `TestBillingStatsHandler*`、`TestOpenAPIRoutesMatchRegisteredRPCs` 契约测试——本任务未动路由，契约仍一致）

- [ ] **Step 5: Commit**

```bash
git add src/backend/internal/panel/cost_stats.go src/backend/internal/panel/cost_stats_test.go
git commit -m "refactor(panel): shared cost stats rows; rename actual cost fields"
```

---

### Task 2: 后端 — 计算成本端点 `GET /api/billing/stats/estimated`

**Files:**
- Modify: `src/backend/internal/panel/cost_stats.go`
- Modify: `src/backend/internal/panel/panel.go:258-260`（在 `/api/billing/stats` 注册之后）
- Modify: `docs/openapi.yaml`（path 128-147 之后 + schemas 776-820）
- Modify: `src/backend/internal/panel/cost_stats_test.go`

**Interfaces:**
- Consumes: Task 1 的 `loadStatsRows` / `statsRow` / `periodDays` / `estimatedBillingStatsDTO` / `estimatedBillingServerStatsDTO`
- Produces: `handleEstimatedBillingStats(w, r)`、路由 `GET /api/billing/stats/estimated`、OpenAPI `billingStatsEstimated` operationId、`BillingActualStats`/`BillingActualServerStats`/`BillingEstimatedStats`/`BillingEstimatedServerStats` schemas

- [ ] **Step 1: 写失败测试**

在 `src/backend/internal/panel/cost_stats_test.go` 末尾追加：

```go
func getEstimatedBillingStats(t *testing.T, s *Server, query string) (*estimatedBillingStatsDTO, *rpcResponse) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/billing/stats/estimated?"+query, nil)
	recorder := httptest.NewRecorder()
	s.handleEstimatedBillingStats(recorder, req)
	var envelope rpcResponse
	if err := json.NewDecoder(recorder.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	var dto estimatedBillingStatsDTO
	if err := json.Unmarshal([]byte(jsonMarshal(envelope.Data)), &dto); err != nil {
		return nil, &envelope
	}
	return &dto, &envelope
}

func seedExpiredBillingServer(t *testing.T, st *store.Store) int64 {
	t.Helper()
	id, err := st.CreateServerWithPlans(context.Background(), "HK-Expired", "", "token-exp", store.MachineTypeDirect,
		"", "", "HK", "Hong Kong", &store.ServerBilling{
			Enabled: true, ProviderID: 1, AmountMinor: 300, Currency: "CNY",
			ServiceStartedOn: "2025-06-01", IntervalCount: 1, IntervalUnit: "month",
			NextRenewalOn: "2026-02-01", Status: store.BillingExpired,
		}, store.ServerTrafficPlan{AccountingMode: "outbound", ResetAnchorOn: "2025-06-01", ResetCount: 1, ResetUnit: "month", TrackingStartedOn: "2025-06-01"})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestEstimatedBillingStatsHandlerMonthView(t *testing.T) {
	s, st := statsTestServer(t)
	usdID, _, cnyID := seedBillingServers(t, st)
	expiredID := seedExpiredBillingServer(t, st)

	dto, envelope := getEstimatedBillingStats(t, s, "from=2026-01-01&to=2026-03-31&granularity=month")
	if envelope.Code != "OK" {
		t.Fatalf("response code = %q message %q", envelope.Code, envelope.Message)
	}
	if len(dto.Servers) != 2 {
		t.Fatalf("servers = %d, want 2 (disabled and expired excluded)", len(dto.Servers))
	}
	for _, srv := range dto.Servers {
		if srv.ServerID == expiredID {
			t.Fatal("expired billing server leaked into estimated stats")
		}
	}
	var usd, cny *estimatedBillingServerStatsDTO
	for i := range dto.Servers {
		switch dto.Servers[i].ServerID {
		case usdID:
			usd = &dto.Servers[i]
		case cnyID:
			cny = &dto.Servers[i]
		}
	}
	if usd == nil || cny == nil {
		t.Fatal("expected usd and cny servers in estimated stats")
	}
	// USD $12/月 → 7200 minor CNY/月 → 240/天；月单元格 = 240×30 = 7200（忽略 01-15 才开通）。
	if usd.DailyMinor != 240 || usd.DaysActive != 90 {
		t.Fatalf("usd daily/days = %d/%d, want 240/90", usd.DailyMinor, usd.DaysActive)
	}
	wantUSD := []int64{7200, 7200, 7200}
	for i := range wantUSD {
		if usd.EstimatedCostsPublic[i] != wantUSD[i] {
			t.Fatalf("usd estimated = %v, want %v", usd.EstimatedCostsPublic, wantUSD)
		}
	}
	// CNY 年付 ¥60/年 → 6000/365/天；月单元格 = round(6000/365×30) = round(493.15) = 493。
	wantCNY := []int64{493, 493, 493}
	for i := range wantCNY {
		if cny.EstimatedCostsPublic[i] != wantCNY[i] {
			t.Fatalf("cny estimated = %v, want %v", cny.EstimatedCostsPublic, wantCNY)
		}
	}
	wantTotals := []int64{7200 + 493, 7200 + 493, 7200 + 493}
	for i := range wantTotals {
		if dto.EstimatedTotalsPublic[i] != wantTotals[i] {
			t.Fatalf("estimated totals = %v, want %v", dto.EstimatedTotalsPublic, wantTotals)
		}
	}
	if dto.CustomAvailable {
		t.Fatal("custom_available should be false without anchors")
	}
	if dto.RateDate != "2026-07-29" {
		t.Fatalf("rate_date = %q, want 2026-07-29", dto.RateDate)
	}
}

func TestEstimatedBillingStatsHandlerGranularities(t *testing.T) {
	s, st := statsTestServer(t)
	usdID, _, cnyID := seedBillingServers(t, st)

	// 日视图：5 个周期，每单元格 = 240 与 round(6000/365) = 16。
	dayDTO, _ := getEstimatedBillingStats(t, s, "from=2026-01-01&to=2026-01-05&granularity=day")
	if dayDTO == nil {
		t.Fatal("day view failed")
	}
	for i := range dayDTO.Servers {
		switch dayDTO.Servers[i].ServerID {
		case usdID:
			for j, cell := range dayDTO.Servers[i].EstimatedCostsPublic {
				if cell != 240 {
					t.Fatalf("usd day cell %d = %d, want 240", j, cell)
				}
			}
		case cnyID:
			for j, cell := range dayDTO.Servers[i].EstimatedCostsPublic {
				if cell != 16 {
					t.Fatalf("cny day cell %d = %d, want 16", j, cell)
				}
			}
		}
	}
	if dayDTO.Servers[0].DaysActive != 5 {
		t.Fatalf("days_active = %d, want 5 (range days)", dayDTO.Servers[0].DaysActive)
	}

	// 年视图：6000/365 × 365 精确还原年价 6000。
	yearDTO, _ := getEstimatedBillingStats(t, s, "from=2025-01-01&to=2025-12-31&granularity=year")
	if yearDTO == nil {
		t.Fatal("year view failed")
	}
	for i := range yearDTO.Servers {
		if yearDTO.Servers[i].ServerID != cnyID {
			continue
		}
		if yearDTO.Servers[i].EstimatedCostsPublic[0] != 6000 {
			t.Fatalf("cny year cell = %d, want 6000", yearDTO.Servers[i].EstimatedCostsPublic[0])
		}
	}
}

func TestEstimatedBillingStatsHandlerCustomRates(t *testing.T) {
	ctx := context.Background()
	s, st := statsTestServer(t)
	usdID, _, _ := seedBillingServers(t, st)
	if _, err := st.UpsertCustomExchangeRate(ctx, store.CustomExchangeRate{
		SourceCurrency: "USD", SourceAmount: "1", TargetCurrency: "CNY", TargetAmount: "7", Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}

	dto, _ := getEstimatedBillingStats(t, s, "from=2026-01-01&to=2026-03-31&granularity=month&rate_mode=custom")
	if dto == nil || !dto.CustomAvailable {
		t.Fatal("custom_available should be true with matching anchor")
	}
	if dto.EstimatedTotalsCustom == nil || dto.EstimatedTotalsPublic == nil {
		t.Fatal("custom mode should carry both totals")
	}
	for i := range dto.Servers {
		if dto.Servers[i].ServerID != usdID {
			continue
		}
		// 12 USD → 84 CNY = 8400 minor/月 → 280/天；月单元格 = 280×30 = 8400。
		if dto.Servers[i].DailyCustomMinor != 280 {
			t.Fatalf("custom daily = %d, want 280", dto.Servers[i].DailyCustomMinor)
		}
		for j, cell := range dto.Servers[i].EstimatedCostsCustom {
			if cell != 8400 {
				t.Fatalf("custom cell %d = %d, want 8400", j, cell)
			}
		}
		if dto.Servers[i].DailyMinor != 240 {
			t.Fatalf("public daily = %d, want 240", dto.Servers[i].DailyMinor)
		}
	}

	// public 模式不携带 custom 数值，但 custom_available 仍是数据属性。
	pubDTO, _ := getEstimatedBillingStats(t, s, "from=2026-01-01&to=2026-03-31&granularity=month&rate_mode=public")
	if pubDTO == nil || !pubDTO.CustomAvailable {
		t.Fatal("custom_available should be independent of rate_mode")
	}
	for i := range pubDTO.Servers {
		if pubDTO.Servers[i].EstimatedCostsCustom != nil || pubDTO.EstimatedTotalsCustom != nil {
			t.Fatal("public mode must not carry custom payloads")
		}
	}
}

func TestEstimatedBillingStatsHandlerValidation(t *testing.T) {
	s, _ := statsTestServer(t)
	cases := []string{
		"from=2026-01-01&to=2026-03-31&granularity=week",
		"from=2026-03-31&to=2026-01-01&granularity=month",
		"from=bad&to=2026-01-01&granularity=month",
		"from=2026-01-01&to=2027-01-15&granularity=day",
	}
	for _, query := range cases {
		req := httptest.NewRequest(http.MethodGet, "/api/billing/stats/estimated?"+query, nil)
		recorder := httptest.NewRecorder()
		s.handleEstimatedBillingStats(recorder, req)
		var envelope rpcResponse
		if err := json.NewDecoder(recorder.Body).Decode(&envelope); err != nil {
			t.Fatalf("decode response for %q: %v", query, err)
		}
		if envelope.Code == "OK" {
			t.Fatalf("invalid query accepted: %s", query)
		}
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./src/backend/internal/panel/ -run 'TestEstimated'`
Expected: FAIL — 编译错误 `undefined: handleEstimatedBillingStats`（或 `estimatedBillingStatsDTO` 不存在，视任务顺序；若 Task 1 已完成则仅 handler 未定义）

- [ ] **Step 3: 实现 estimated 端点（cost_stats.go）**

在 `handleBillingStats` 之后追加：

```go
// handleEstimatedBillingStats 处理 GET /api/billing/stats/estimated（计算成本）：对启用统计计费
// 且未过期的服务器按"日成本 × 周期天数"估算每周期成本（忽略服务期与已生效部分）。
func (s *Server) handleEstimatedBillingStats(w http.ResponseWriter, r *http.Request) {
	loc := s.inspectionLocation(r.Context())
	from, to, gran, mode, err := parseBillingStatsQuery(r.URL.Query(), loc)
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}
	rows, err := s.loadStatsRows(r.Context(), func(b store.ServerBilling) bool {
		return b.Status != store.BillingExpired
	})
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	periods := costPeriods(from, to, gran)
	spanDays := daysBetween(from, to) + 1
	dto := estimatedBillingStatsDTO{
		billingStatsMeta: billingStatsMeta{
			ReportingCurrency: s.reportingCurrency(r.Context()),
			Granularity:       gran,
			From:              from.Format("2006-01-02"),
			To:                to.Format("2006-01-02"),
			RateMode:          mode,
			Periods:           periods,
		},
		Servers:               []estimatedBillingServerStatsDTO{},
		EstimatedTotalsPublic: make([]int64, len(periods)),
	}
	for _, row := range rows {
		if row.customDiffers {
			dto.CustomAvailable = true
		}
		if dto.RateDate == "" && row.rateDate != "" {
			dto.RateDate = row.rateDate
		}
	}
	var totalsCustom []int64
	if mode == costModeCustom && dto.CustomAvailable {
		totalsCustom = make([]int64, len(periods))
	}
	perPeriod := periodDays(gran)
	for _, row := range rows {
		base := row.base
		base.DaysActive = spanDays
		item := estimatedBillingServerStatsDTO{
			billingServerStatsBase:   base,
			EstimatedCostsPublic:     make([]int64, len(periods)),
		}
		var costsCustom []int64
		if totalsCustom != nil {
			costsCustom = make([]int64, len(periods))
		}
		for i := range periods {
			cost := new(big.Rat).Mul(row.dailyPublic, new(big.Rat).SetInt64(perPeriod))
			item.EstimatedCostsPublic[i] = roundRat(cost)
			dto.EstimatedTotalsPublic[i] += item.EstimatedCostsPublic[i]
			if costsCustom != nil && row.dailyCustom != nil {
				cost := new(big.Rat).Mul(row.dailyCustom, new(big.Rat).SetInt64(perPeriod))
				costsCustom[i] = roundRat(cost)
				totalsCustom[i] += costsCustom[i]
			}
		}
		if costsCustom != nil {
			if row.dailyCustom == nil {
				costsCustom = append([]int64(nil), item.EstimatedCostsPublic...)
			}
			item.EstimatedCostsCustom = costsCustom
		}
		dto.Servers = append(dto.Servers, item)
	}
	if totalsCustom != nil {
		dto.EstimatedTotalsCustom = totalsCustom
	}
	writeJSON(w, http.StatusOK, dto)
}
```

- [ ] **Step 4: 注册路由（panel.go）**

在 `src/backend/internal/panel/panel.go` 第 260 行（`s.handleBillingStats` 注册）之后追加：

```go
	s.registerRPC(mux, http.MethodGet, "/api/billing/stats/estimated",
		rpcRouteOptions{Auth: true, AllowedQuery: []string{"from", "to", "granularity", "rate_mode"}},
		s.handleEstimatedBillingStats)
```

- [ ] **Step 5: 同步 OpenAPI 契约（docs/openapi.yaml）**

在 `/api/billing/stats:` path（第 128-147 行）之后追加：

```yaml
  /api/billing/stats/estimated:
    get:
      operationId: billingStatsEstimated
      parameters:
        - name: from
          in: query
          required: true
          schema: {type: string, format: date}
        - name: to
          in: query
          required: true
          schema: {type: string, format: date}
        - name: granularity
          in: query
          required: true
          schema: {$ref: '#/components/schemas/BillingStatsGranularity'}
        - name: rate_mode
          in: query
          schema: {$ref: '#/components/schemas/BillingStatsRateMode'}
      responses: {'200': {$ref: '#/components/responses/RPCResponse'}, default: {$ref: '#/components/responses/ProtocolErrorResponse'}}
```

将 schemas（第 783-820 行）的 `BillingServerStats` 更名为 `BillingActualServerStats`、`BillingStats` 更名为 `BillingActualStats`，字段改名：
- `required` 中 `costs_public` → `actual_costs_public`；properties 中 `costs_public: {type: array, items: {type: integer}}` → `actual_costs_public: {type: array, items: {type: integer}}`、`costs_custom` 描述与键 → `actual_costs_custom`（描述中 "equals costs_public" → "equals actual_costs_public"）
- `BillingActualStats` 的 `required` 中 `totals_public` → `actual_totals_public`；properties 中 `servers` items → `#/components/schemas/BillingActualServerStats`、`totals_public` → `actual_totals_public`、`totals_custom` → `actual_totals_custom`（描述同步）
- 描述中 "aligned with BillingStats.periods" → "aligned with BillingActualStats.periods"

在 `BillingActualStats` schema 之后追加：

```yaml
    BillingEstimatedServerStats:
      type: object
      description: Per-server estimated cost series aligned with BillingEstimatedStats.periods, computed as daily cost times fixed period days (day=1, month=30, year=365) regardless of service dates. Costs are in the reporting currency minor unit, rounded per cell; custom arrays are present when custom_available and rate_mode=custom.
      additionalProperties: false
      required: [server_id, alias, country_code, location, currency, amount_minor, interval_count, interval_unit, service_started_on, status, days_active, daily_minor, estimated_costs_public]
      properties:
        server_id: {type: integer}
        alias: {type: string}
        country_code: {type: string, description: ISO 3166-1 alpha-2.}
        location: {type: string}
        currency: {$ref: '#/components/schemas/Currency'}
        amount_minor: {type: integer, description: Original price in the server currency minor unit.}
        interval_count: {type: integer, minimum: 1, maximum: 10000}
        interval_unit: {type: string, enum: [day, month, year]}
        service_started_on: {type: string, format: date}
        status: {type: string, description: Billing lifecycle status.}
        days_active: {type: integer, description: Days in the requested range (constant for every server).}
        daily_minor: {type: integer, description: Public daily cost in reporting minor unit.}
        daily_custom_minor: {type: integer, description: Custom daily cost; present when the server has a custom conversion.}
        estimated_costs_public: {type: array, items: {type: integer}}
        estimated_costs_custom: {type: array, items: {type: integer}, description: Present for every server when custom_available and rate_mode=custom; equals estimated_costs_public for servers without a custom conversion.}
    BillingEstimatedStats:
      type: object
      description: Projected cost statistics for non-expired servers with statistical billing enabled, estimated as daily cost times fixed period days per period and converted to the reporting currency.
      additionalProperties: false
      required: [reporting_currency, granularity, from, to, rate_mode, custom_available, periods, servers, estimated_totals_public]
      properties:
        reporting_currency: {$ref: '#/components/schemas/Currency'}
        granularity: {$ref: '#/components/schemas/BillingStatsGranularity'}
        from: {type: string, format: date}
        to: {type: string, format: date}
        rate_mode: {$ref: '#/components/schemas/BillingStatsRateMode'}
        rate_date: {type: string, description: Public exchange rate cache date; empty when no conversion was needed.}
        custom_available: {type: boolean, description: True when at least one server's custom conversion differs from its public conversion; independent of rate_mode.}
        periods: {type: array, items: {type: string}, description: Every calendar period covering from/to.}
        servers: {type: array, items: {$ref: '#/components/schemas/BillingEstimatedServerStats'}, description: Non-expired servers with billing enabled, ordered by server_id.}
        estimated_totals_public: {type: array, items: {type: integer}, description: Per-period sum of estimated_costs_public.}
        estimated_totals_custom: {type: array, items: {type: integer}, description: Present when custom_available and rate_mode=custom.}
```

- [ ] **Step 6: 运行后端测试确认通过**

Run: `go test ./src/backend/...`
Expected: PASS（含新的 `TestEstimatedBillingStatsHandler*` 与 `TestOpenAPIRoutesMatchRegisteredRPCs`）

- [ ] **Step 7: Commit**

```bash
git add src/backend/internal/panel/cost_stats.go src/backend/internal/panel/panel.go src/backend/internal/panel/cost_stats_test.go docs/openapi.yaml
git commit -m "feat(panel): estimated cost stats endpoint"
```

---

### Task 3: 前端 — 类型改名 + estimated 客户端 + 契约重新生成

**Files:**
- Modify: `src/frontend/src/lib/types.ts:114-147`
- Modify: `src/frontend/src/lib/api.ts:319-327`
- Modify: `src/frontend/src/pages/Costs.tsx`（仅机械改名，结构不动）
- Regenerate: `src/frontend/src/lib/api-contract.generated.ts`（`bun run generate:api`）

**Interfaces:**
- Produces: `BillingActualStats` / `BillingActualServerStats` / `BillingEstimatedStats` / `BillingEstimatedServerStats` 类型；`api.billingStats()` 返回 `BillingActualStats`；`api.billingStatsEstimated()` 返回 `BillingEstimatedStats`

- [ ] **Step 1: 更新 types.ts**

将 `src/frontend/src/lib/types.ts` 第 114-147 行的类型定义替换为：

```ts
export type BillingStatsGranularity = 'day' | 'month' | 'year'
export type BillingStatsRateMode = 'public' | 'custom'

export interface BillingActualServerStats {
  server_id: number
  alias: string
  country_code: string
  location: string
  currency: string
  amount_minor: number
  interval_count: number
  interval_unit: IntervalUnit
  service_started_on: string
  status: BillingStatus
  days_active: number
  daily_minor: number
  daily_custom_minor?: number
  actual_costs_public: number[]
  actual_costs_custom?: number[]
}

export interface BillingActualStats {
  reporting_currency: string
  granularity: BillingStatsGranularity
  from: string
  to: string
  rate_mode: BillingStatsRateMode
  rate_date?: string
  custom_available: boolean
  periods: string[]
  servers: BillingActualServerStats[]
  actual_totals_public: number[]
  actual_totals_custom?: number[]
}

export interface BillingEstimatedServerStats {
  server_id: number
  alias: string
  country_code: string
  location: string
  currency: string
  amount_minor: number
  interval_count: number
  interval_unit: IntervalUnit
  service_started_on: string
  status: BillingStatus
  days_active: number
  daily_minor: number
  daily_custom_minor?: number
  estimated_costs_public: number[]
  estimated_costs_custom?: number[]
}

export interface BillingEstimatedStats {
  reporting_currency: string
  granularity: BillingStatsGranularity
  from: string
  to: string
  rate_mode: BillingStatsRateMode
  rate_date?: string
  custom_available: boolean
  periods: string[]
  servers: BillingEstimatedServerStats[]
  estimated_totals_public: number[]
  estimated_totals_custom?: number[]
}
```

- [ ] **Step 2: 更新 api.ts**

将 `src/frontend/src/lib/api.ts` 第 319-327 行替换为：

```ts
  billingStats: (
    params: {
      from: string
      to: string
      granularity: BillingStatsGranularity
      rate_mode?: BillingStatsRateMode
    },
    options?: RequestOptions,
  ) => requester.get<BillingActualStats>('/api/billing/stats', params, { display: 'silent', ...options }),

  billingStatsEstimated: (
    params: {
      from: string
      to: string
      granularity: BillingStatsGranularity
      rate_mode?: BillingStatsRateMode
    },
    options?: RequestOptions,
  ) => requester.get<BillingEstimatedStats>('/api/billing/stats/estimated', params, { display: 'silent', ...options }),
```

并更新 api.ts 第 5-7 行 import：`BillingStats` → `BillingActualStats`，新增 `BillingEstimatedStats`（保持字母序）：

```ts
import type {
  BillingActualStats,
  BillingEstimatedStats,
  BillingStatsGranularity,
  BillingStatsRateMode,
} from '@/lib/types'
```

- [ ] **Step 3: 机械改名 Costs.tsx**

在 `src/frontend/src/pages/Costs.tsx` 中替换：
- import：`BillingServerStats` → `BillingActualServerStats`、`BillingStats` → `BillingActualStats`
- `function costsOf(server: BillingServerStats, ...)` → `function costsOf(server: BillingActualServerStats, ...)`，函数体内 `server.costs_custom` → `server.actual_costs_custom`、`server.costs_public` → `server.actual_costs_public`
- 第 86 行同一函数内其余引用同步（`server.costs_custom`、`server.costs_public` 各 1 处）
- `interface ServerRow { ... server: BillingServerStats ... }` → `server: BillingActualServerStats`
- `useState<BillingStats | null>` → `useState<BillingActualStats | null>`
- `stats?.totals_custom` → `stats?.actual_totals_custom`（第 206 行）、`stats.totals_public` → `stats.actual_totals_public`（第 206 行）
- `server.costs_custom` → `server.actual_costs_custom`（第 86 行）、`server.costs_public` → `server.actual_costs_public`（第 86 行）

（`daily_minor` / `daily_custom_minor` / `days_active` 等共用字段名不变。）

- [ ] **Step 4: 重新生成 API 契约**

Run（workdir `src/frontend`）: `bun run generate:api`
Expected: `api-contract.generated.ts` 更新（`BillingActualServerStats`/`BillingActualStats`/`BillingEstimatedServerStats`/`BillingEstimatedStats` interfaces 出现，`billingStatsEstimated` operation 出现）

- [ ] **Step 5: 验证前端编译**

Run（workdir `src/frontend`）: `bun run build && bun run lint && bun run test`
Expected: PASS（tsc + 契约 --check + vite + oxlint + vitest）

- [ ] **Step 6: Commit**

```bash
git add src/frontend/src/lib/types.ts src/frontend/src/lib/api.ts src/frontend/src/lib/api-contract.generated.ts src/frontend/src/pages/Costs.tsx
git commit -m "refactor(frontend): rename actual cost stats types; add estimated client"
```

---

### Task 4: 前端 — Costs.tsx 双 tab 重构 + EstimatedCostsTab

**Files:**
- Modify: `src/frontend/src/pages/Costs.tsx`

**Interfaces:**
- Consumes: Task 3 的 `BillingEstimatedStats` / `BillingEstimatedServerStats` / `api.billingStatsEstimated`
- Produces: `Costs()`（外层：标题 + Tabs 切换）、`ActualCostsTab()`、`EstimatedCostsTab()`、共享助手 `useEarliestStart()` / `StatsControls` 组件 / 图表构建函数 `buildBarOption` / `buildDonutOption`

- [ ] **Step 1: 抽取共享助手**

在 `src/frontend/src/pages/Costs.tsx` 中新增（放在 `money` 函数之后）：

```tsx
function useEarliestStart(): string {
  const [earliestStart, setEarliestStart] = useState('')
  useEffect(() => {
    let active = true
    void api.servers({ display: 'silent' }).then((servers) => {
      if (!active) return
      const starts = servers
        .filter((server) => server.billing?.enabled && server.billing.service_started_on)
        .map((server) => server.billing.service_started_on)
      setEarliestStart(starts.length > 0 ? starts.sort()[0] : '')
    }).catch(() => {})
    return () => { active = false }
  }, [])
  return earliestStart
}

interface CostsSeriesServer {
  alias: string
  costs: number[]
}

function buildBarOption(options: {
  periods: string[]
  servers: CostsSeriesServer[]
  granularity: BillingStatsGranularity
  currency: string
  textColor: string
  axisColor: string
}): ChartOption {
  return {
    color: SERVER_PALETTE,
    tooltip: {
      trigger: 'axis',
      axisPointer: { type: 'shadow' },
      valueFormatter: (value: unknown) => `${money(Number(value), options.currency)} ${options.currency}`,
    },
    legend: {
      type: 'scroll',
      bottom: 0,
      textStyle: { color: options.textColor },
      data: options.servers.map((server) => server.alias),
    },
    grid: {
      left: 8,
      right: 16,
      top: 24,
      bottom: options.granularity === 'day' ? 56 : 28,
      containLabel: true,
    },
    xAxis: {
      type: 'category',
      data: options.periods,
      axisLabel: { color: options.textColor },
      axisLine: { lineStyle: { color: options.axisColor } },
      axisTick: { show: false },
    },
    yAxis: {
      type: 'value',
      axisLabel: { color: options.textColor },
      splitLine: { lineStyle: { color: options.axisColor } },
    },
    dataZoom: options.granularity === 'day'
      ? [{ type: 'inside' }, { type: 'slider', bottom: 24, height: 18, borderColor: options.axisColor }]
      : [],
    series: options.servers.map((server) => ({
      name: server.alias,
      type: 'bar',
      stack: 'cost',
      emphasis: { focus: 'series' },
      barMaxWidth: 36,
      data: server.costs,
    })),
  }
}

function buildDonutOption(
  data: Array<{ name: string; value: number }>,
  currency: string,
  textColor: string,
  theme: string,
): ChartOption {
  return {
    color: SERVER_PALETTE,
    tooltip: {
      trigger: 'item',
      valueFormatter: (value: unknown) => `${money(Number(value), currency)} ${currency}`,
    },
    legend: {
      type: 'scroll',
      orient: 'vertical',
      right: 8,
      top: 'middle',
      textStyle: { color: textColor },
    },
    series: [{
      name: '成本占比',
      type: 'pie',
      radius: ['42%', '68%'],
      center: ['38%', '50%'],
      avoidLabelOverlap: true,
      itemStyle: {
        borderColor: theme === 'dark' ? '#1c1e2e' : '#ffffff',
        borderWidth: 2,
      },
      label: { color: textColor, formatter: '{b}\n{d}%' },
      data,
    }],
  }
}

interface StatsControlsProps {
  granularity: BillingStatsGranularity
  from: string
  to: string
  rateMode: BillingStatsRateMode
  customAvailable: boolean
  rateDate?: string
  presetsDisabled: boolean
  onGranularity: (value: string) => void
  onFrom: (value: string) => void
  onTo: (value: string) => void
  onPreset: (preset: 'month' | '12months' | '3years' | 'all') => void
  onRateMode: (value: BillingStatsRateMode) => void
}

function StatsControls({
  granularity, from, to, rateMode, customAvailable, rateDate, presetsDisabled,
  onGranularity, onFrom, onTo, onPreset, onRateMode,
}: StatsControlsProps) {
  return (
    <Card>
      <CardContent className="flex flex-wrap items-end gap-x-4 gap-y-3">
        <div className="space-y-2">
          <span className="text-xs font-medium text-muted-foreground">统计粒度</span>
          <Tabs value={granularity} onValueChange={onGranularity}>
            <TabsList>
              {(Object.keys(GRANULARITY_LABEL) as BillingStatsGranularity[]).map((gran) => (
                <TabsTrigger key={gran} value={gran}>{GRANULARITY_LABEL[gran]}</TabsTrigger>
              ))}
            </TabsList>
          </Tabs>
        </div>
        <div className="grid grid-cols-2 items-end gap-2">
          <div className="space-y-2">
            <span className="text-xs font-medium text-muted-foreground">起始日期</span>
            <Input type="date" value={from} max={to} onChange={(event) => onFrom(event.target.value)} className="w-40" />
          </div>
          <div className="space-y-2">
            <span className="text-xs font-medium text-muted-foreground">结束日期</span>
            <Input type="date" value={to} min={from} onChange={(event) => onTo(event.target.value)} className="w-40" />
          </div>
        </div>
        <div className="flex flex-wrap gap-2">
          <Button type="button" variant="outline" size="sm" onClick={() => onPreset('month')}>本月</Button>
          <Button type="button" variant="outline" size="sm" onClick={() => onPreset('12months')}>近 12 个月</Button>
          <Button type="button" variant="outline" size="sm" onClick={() => onPreset('3years')}>近 3 年</Button>
          <Button type="button" variant="outline" size="sm" disabled={presetsDisabled} onClick={() => onPreset('all')}>全部</Button>
        </div>
        {customAvailable ? (
          <div className="space-y-2">
            <span className="text-xs font-medium text-muted-foreground">换算方式</span>
            <Select
              value={rateMode}
              onValueChange={(value) => value && onRateMode(value as BillingStatsRateMode)}
            >
              <SelectTrigger className="w-40" size="sm">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="custom">自定义锚点</SelectItem>
                <SelectItem value="public">公共汇率</SelectItem>
              </SelectContent>
            </Select>
          </div>
        ) : null}
        {rateDate ? (
          <span className="inline-flex items-center gap-2 rounded-full border bg-card px-3 py-1.5 text-sm font-medium">
            <CoinsIcon className="size-4" />
            汇率日期 {rateDate}
          </span>
        ) : null}
      </CardContent>
    </Card>
  )
}
```

- [ ] **Step 2: 抽出 ActualCostsTab 并改写外层 Costs**

将 `export default function Costs()` 改写为外层（仅保留标题 + tab 切换），原组件体改为 `function ActualCostsTab()`：

- 原 `export default function Costs() {` → `function ActualCostsTab() {`
- 原第 106 行的 `const [earliestStart, setEarliestStart] = useState('')` 与第 112-122 行的 `useEffect`（`api.servers` 计算最早开通日）删除，改为 `const earliestStart = useEarliestStart()`
- 原第 124 行起 `load` 中 `api.billingStats(...)` 的返回类型推断已随 Task 3 变为 `BillingActualStats`，其余逻辑不变
- 原 `barOption` / `donutOption` 两个 `useMemo` 的完整 ECharts 配置改为调用共享构建函数：

```tsx
  const barOption = useMemo<ChartOption>(() => {
    if (!stats) return {}
    return buildBarOption({
      periods: stats.periods,
      servers: stats.servers.map((server) => ({
        alias: server.alias,
        costs: costsOf(server, rateMode),
      })),
      granularity,
      currency: reportingCurrency,
      textColor,
      axisColor,
    })
  }, [stats, rateMode, granularity, reportingCurrency, textColor, axisColor])

  const donutOption = useMemo<ChartOption>(() => {
    if (!stats || rows.length === 0 || totalAll <= 0) return {}
    return buildDonutOption(
      rows.map((row) => ({ name: row.server.alias, value: row.total })),
      reportingCurrency,
      textColor,
      theme,
    )
  }, [stats, rows, totalAll, reportingCurrency, textColor, theme])
```

- 原第 334-341 行错误分支与第 343-356 行主返回中的 `PageHeader` 改为如下（badge 移入 `StatsControls`）：

```tsx
  const errorPage = (
    <Notice tone="danger" title="成本统计加载失败">{error}</Notice>
  )
```

  并将主返回结构替换为：

```tsx
  return (
    <>
      <StatsControls
        granularity={granularity}
        from={from}
        to={to}
        rateMode={rateMode}
        customAvailable={stats?.custom_available ?? false}
        rateDate={stats?.rate_date}
        presetsDisabled={!earliestStart}
        onGranularity={changeGranularity}
        onFrom={setFrom}
        onTo={setTo}
        onPreset={applyPreset}
        onRateMode={(value) => setRateMode(value)}
      />
      {error ? errorPage : null}
      {loading && !stats ? (
        <LoadingState className="py-16">正在统计成本…</LoadingState>
      ) : stats && stats.servers.length === 0 ? (
        <EmptyState
          icon={<CoinsIcon className="size-8" />}
          title="暂无启用统计计费的服务器"
          description="在「服务器」页为服务器开启统计计费并填写周期价格后，这里会展示已生效成本。"
        />
      ) : stats ? (
        <>（原 415-573 行的卡片/图表/表格内容原样保留，字段已随 Task 3 改名）</>
      ) : null}
    </>
  )
```

- 注意删除原第 334-341 行的错误早退 `return (...)`（错误改为条件渲染 `{error ? errorPage : null}`），删除原第 343-356 行外的 `Page`/`PageHeader` 包裹（由外层提供）

新外层 `Costs()`（放在文件末尾）：

```tsx
export default function Costs() {
  const [tab, setTab] = useState<'actual' | 'estimated'>('actual')
  return (
    <Page>
      <PageHeader
        title="成本统计"
        description="已生效成本按服务期摊算实际花费；计算成本按日成本估算各周期成本，统一以统计币种展示"
      />
      <Tabs value={tab} onValueChange={(value) => value && setTab(value as 'actual' | 'estimated')}>
        <TabsList>
          <TabsTrigger value="actual">已生效成本</TabsTrigger>
          <TabsTrigger value="estimated">计算成本</TabsTrigger>
        </TabsList>
      </Tabs>
      {tab === 'actual' ? <ActualCostsTab /> : <EstimatedCostsTab />}
    </Page>
  )
}
```

- [ ] **Step 3: 新增 EstimatedCostsTab**

在 `ActualCostsTab` 之后追加（完整实现；估算总成本 = Σ 日成本 × 周期天数，汇总卡片为估算日/月/年成本）：

```tsx
function costsOfEstimated(server: BillingEstimatedServerStats, rateMode: BillingStatsRateMode): number[] {
  return rateMode === 'custom' && server.estimated_costs_custom
    ? server.estimated_costs_custom
    : server.estimated_costs_public
}

function EstimatedCostsTab() {
  const { theme } = useTheme()
  const [granularity, setGranularity] = useState<BillingStatsGranularity>('month')
  const [from, setFrom] = useState(() => firstOfMonth(addMonths(localDate(), -11)))
  const [to, setTo] = useState(() => localDate())
  const [rateMode, setRateMode] = useState<BillingStatsRateMode>('custom')
  const [stats, setStats] = useState<BillingEstimatedStats | null>(null)
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(true)
  const [sort, setSort] = useState<{ key: 'name' | 'total' | 'daily' | 'share'; dir: 1 | -1 }>({
    key: 'total',
    dir: -1,
  })
  const earliestStart = useEarliestStart()

  const load = useCallback(async (signal?: AbortSignal) => {
    try {
      const result = await api.billingStatsEstimated({
        from,
        to,
        granularity,
        rate_mode: rateMode,
      }, signal ? { signal, display: 'silent' } : { display: 'silent' })
      if (signal?.aborted) return
      setStats(result)
      setError('')
    } catch (err) {
      if (signal?.aborted) return
      setError(errorMessage(err))
    } finally {
      if (!signal?.aborted) setLoading(false)
    }
  }, [from, to, granularity, rateMode])

  useEffect(() => {
    const controller = new AbortController()
    void load(controller.signal)
    return () => controller.abort()
  }, [load])

  const changeGranularity = (value: string) => {
    const gran = value as BillingStatsGranularity
    const [clampedFrom, clampedTo] = clampRange(gran, from, to)
    setFrom(clampedFrom)
    setTo(clampedTo)
    setGranularity(gran)
  }

  const applyPreset = (preset: 'month' | '12months' | '3years' | 'all') => {
    const today = localDate()
    switch (preset) {
      case 'month':
        setFrom(firstOfMonth(today))
        setTo(today)
        break
      case '12months':
        setFrom(firstOfMonth(addMonths(today, -11)))
        setTo(today)
        break
      case '3years':
        setFrom(`${String(Number(today.slice(0, 4)) - 2)}-01-01`)
        setTo(today)
        break
      case 'all':
        if (earliestStart) setFrom(earliestStart)
        setTo(today)
        break
    }
  }

  const dailyOf = (server: BillingEstimatedServerStats): number =>
    rateMode === 'custom' ? server.daily_custom_minor ?? server.daily_minor : server.daily_minor

  const rows = useMemo(() => {
    if (!stats) return []
    const totalAll = stats.servers.reduce(
      (sum, server) => sum + costsOfEstimated(server, rateMode).reduce((acc, value) => acc + value, 0),
      0,
    )
    return stats.servers.map((server) => {
      const costs = costsOfEstimated(server, rateMode)
      const total = costs.reduce((sum, value) => sum + value, 0)
      return {
        name: server.alias,
        server,
        total,
        share: totalAll > 0 ? total / totalAll : 0,
        daily: dailyOf(server),
      }
    }).sort((a, b) => {
      const left = a[sort.key]
      const right = b[sort.key]
      if (typeof left === 'string' && typeof right === 'string') {
        return left.localeCompare(right) * sort.dir
      }
      return ((left as number) - (right as number)) * sort.dir
    })
  }, [stats, rateMode, sort])

  const totalAll = useMemo(() => rows.reduce((sum, row) => sum + row.total, 0), [rows])
  const dailyTotal = useMemo(() => rows.reduce((sum, row) => sum + row.daily, 0), [rows])
  const totals = rateMode === 'custom' && stats?.estimated_totals_custom
    ? stats.estimated_totals_custom
    : stats?.estimated_totals_public ?? []
  const topServer = useMemo(() => {
    if (!stats || stats.servers.length === 0) return null
    return stats.servers
      .map((server) => ({
        server,
        total: costsOfEstimated(server, rateMode).reduce((sum, value) => sum + value, 0),
      }))
      .sort((a, b) => b.total - a.total)[0]
  }, [stats, rateMode])

  const reportingCurrency = stats?.reporting_currency ?? 'CNY'
  const textColor = theme === 'dark' ? '#c9cbe2' : '#686a7c'
  const axisColor = theme === 'dark' ? '#42466f' : '#d4d6e0'

  const barOption = useMemo<ChartOption>(() => {
    if (!stats) return {}
    return buildBarOption({
      periods: stats.periods,
      servers: stats.servers.map((server) => ({
        alias: server.alias,
        costs: costsOfEstimated(server, rateMode),
      })),
      granularity,
      currency: reportingCurrency,
      textColor,
      axisColor,
    })
  }, [stats, rateMode, granularity, reportingCurrency, textColor, axisColor])

  const donutOption = useMemo<ChartOption>(() => {
    if (!stats || rows.length === 0 || totalAll <= 0) return {}
    return buildDonutOption(
      rows.map((row) => ({ name: row.server.alias, value: row.total })),
      reportingCurrency,
      textColor,
      theme,
    )
  }, [stats, rows, totalAll, reportingCurrency, textColor, theme])

  const toggleSort = (key: 'name' | 'total' | 'daily' | 'share') => {
    setSort((current) => current.key === key
      ? { key, dir: current.dir === 1 ? -1 : 1 }
      : { key, dir: key === 'name' ? 1 : -1 })
  }

  const sortHeader = (key: 'name' | 'total' | 'daily' | 'share', label: string, className?: string) => (
    <button
      type="button"
      onClick={() => toggleSort(key)}
      className={cn('inline-flex items-center gap-1 hover:text-foreground', className)}
    >
      {label}
      <span className={cn('text-[10px] opacity-60', sort.key === key ? 'opacity-100' : 'invisible')}>
        {sort.dir === 1 ? '↑' : '↓'}
      </span>
    </button>
  )

  const periodLabel = (period: string) => {
    if (granularity === 'day') return period
    if (granularity === 'year') return period
    return period.replace('-', ' 年 ').concat(' 月')
  }

  return (
    <>
      <StatsControls
        granularity={granularity}
        from={from}
        to={to}
        rateMode={rateMode}
        customAvailable={stats?.custom_available ?? false}
        rateDate={stats?.rate_date}
        presetsDisabled={!earliestStart}
        onGranularity={changeGranularity}
        onFrom={setFrom}
        onTo={setTo}
        onPreset={applyPreset}
        onRateMode={(value) => setRateMode(value)}
      />
      {error ? <Notice tone="danger" title="计算成本加载失败">{error}</Notice> : null}
      {loading && !stats ? (
        <LoadingState className="py-16">正在估算成本…</LoadingState>
      ) : stats && stats.servers.length === 0 ? (
        <EmptyState
          icon={<CoinsIcon className="size-8" />}
          title="暂无启用计费且未过期的服务器"
          description="在「服务器」页为服务器开启统计计费并填写周期价格后，这里会展示计算成本估算。"
        />
      ) : stats ? (
        <>
          <div className="grid gap-4 sm:grid-cols-2 xl:grid-cols-4">
            <Card>
              <CardHeader>
                <CardDescription>估算日成本</CardDescription>
                <CardTitle className="text-2xl tabular-nums">
                  {money(dailyTotal, reportingCurrency)}
                  <span className="ml-1 text-sm font-normal text-muted-foreground">/ 天 · {reportingCurrency}</span>
                </CardTitle>
              </CardHeader>
            </Card>
            <Card>
              <CardHeader>
                <CardDescription>估算月成本（×30）</CardDescription>
                <CardTitle className="text-2xl tabular-nums">
                  {money(dailyTotal * 30, reportingCurrency)}
                  <span className="ml-1 text-sm font-normal text-muted-foreground">/ 月 · {reportingCurrency}</span>
                </CardTitle>
              </CardHeader>
            </Card>
            <Card>
              <CardHeader>
                <CardDescription>估算年成本（×365）</CardDescription>
                <CardTitle className="text-2xl tabular-nums">
                  {money(dailyTotal * 365, reportingCurrency)}
                  <span className="ml-1 text-sm font-normal text-muted-foreground">/ 年 · {reportingCurrency}</span>
                </CardTitle>
              </CardHeader>
            </Card>
            <Card>
              <CardHeader>
                <CardDescription>启用计费服务器</CardDescription>
                <CardTitle className="text-2xl tabular-nums">{stats.servers.length} 台</CardTitle>
              </CardHeader>
            </Card>
          </div>

          <div className="grid gap-4 lg:grid-cols-3">
            <Card className="lg:col-span-2">
              <CardHeader>
                <CardTitle>周期估算成本分布</CardTitle>
                <CardDescription>每台服务器一个色段，悬停查看明细；图例可点击隐藏单台服务器。</CardDescription>
              </CardHeader>
              <CardContent>
                <Chart option={barOption} className="h-80 w-full" />
              </CardContent>
            </Card>
            <Card>
              <CardHeader>
                <CardTitle>成本占比</CardTitle>
                <CardDescription>范围内各服务器估算成本占比。</CardDescription>
              </CardHeader>
              <CardContent>
                {totalAll > 0
                  ? <Chart option={donutOption} className="h-80 w-full" />
                  : <EmptyState title="范围内暂无成本" description="调整时间范围后查看占比。" className="h-80" />}
              </CardContent>
            </Card>
          </div>

          <Card>
            <CardHeader>
              <CardTitle>服务器汇总</CardTitle>
              <CardDescription>
                原价与周期以服务器币种展示；其余数值按 {reportingCurrency} 估算，点击列头排序。
              </CardDescription>
            </CardHeader>
            <CardContent>
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>{sortHeader('name', '服务器')}</TableHead>
                    <TableHead className="text-right">原价 / 周期</TableHead>
                    <TableHead className="text-right">{sortHeader('daily', `估算日成本 (${reportingCurrency})`)}</TableHead>
                    <TableHead className="text-right">{sortHeader('total', `估算总成本 (${reportingCurrency})`)}</TableHead>
                    <TableHead className="text-right">{sortHeader('share', '占比')}</TableHead>
                    <TableHead className="text-right">状态</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {rows.map((row) => {
                    const { server } = row
                    return (
                      <TableRow key={server.server_id}>
                        <TableCell>
                          <span className="flex min-w-0 items-center gap-2">
                            <CountryFlag code={server.country_code} label={server.country_code} className="rounded-[2px] text-base" />
                            <span className="truncate font-medium" title={server.alias}>{server.alias}</span>
                            {server.location ? (
                              <span className="hidden truncate text-muted-foreground sm:inline">{server.location}</span>
                            ) : null}
                          </span>
                        </TableCell>
                        <TableCell className="text-right tabular-nums whitespace-nowrap">
                          {money(server.amount_minor, server.currency)} {server.currency}
                          <span className="text-muted-foreground"> / {server.interval_count} {GRANULARITY_LABEL[server.interval_unit]}</span>
                        </TableCell>
                        <TableCell className="text-right tabular-nums">{money(row.daily, reportingCurrency)}</TableCell>
                        <TableCell className="text-right tabular-nums font-medium">{money(row.total, reportingCurrency)}</TableCell>
                        <TableCell className="text-right tabular-nums">{(row.share * 100).toFixed(1)}%</TableCell>
                        <TableCell className="text-right">
                          {billingStatusLabel[server.status] ? (
                            <Badge variant={billingStatusVariant[server.status] ?? 'outline'}>
                              {billingStatusLabel[server.status]}
                            </Badge>
                          ) : null}
                        </TableCell>
                      </TableRow>
                    )
                  })}
                </TableBody>
              </Table>
            </CardContent>
          </Card>

          <Card>
            <CardHeader>
              <CardTitle>周期明细矩阵</CardTitle>
              <CardDescription>行 = 周期，列 = 服务器；单元格为对应周期估算成本（{reportingCurrency}），行尾为周期合计。</CardDescription>
            </CardHeader>
            <CardContent className="overflow-x-auto">
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead className="sticky left-0 bg-card">周期</TableHead>
                    {stats.servers.map((server) => (
                      <TableHead key={server.server_id} className="max-w-36 truncate text-right" title={server.alias}>
                        {server.alias}
                      </TableHead>
                    ))}
                    <TableHead className="text-right">合计</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {stats.periods.map((period, index) => (
                    <TableRow key={period}>
                      <TableCell className="sticky left-0 bg-card font-medium whitespace-nowrap">
                        {periodLabel(period)}
                      </TableCell>
                      {stats.servers.map((server) => {
                        const costs = costsOfEstimated(server, rateMode)
                        return (
                          <TableCell key={server.server_id} className="text-right tabular-nums">
                            {costs[index] ? money(costs[index], reportingCurrency) : '—'}
                          </TableCell>
                        )
                      })}
                      <TableCell className="text-right font-medium tabular-nums">
                        {totals[index] ? money(totals[index], reportingCurrency) : '—'}
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            </CardContent>
          </Card>
        </>
      ) : null}
    </>
  )
}
```

- [ ] **Step 4: 补充 import**

`src/frontend/src/pages/Costs.tsx` 顶部 import 从 `@/lib/types` 增补 `BillingEstimatedServerStats`、`BillingEstimatedStats`：

```tsx
import type {
  BillingActualServerStats,
  BillingActualStats,
  BillingEstimatedServerStats,
  BillingEstimatedStats,
  BillingStatsGranularity,
  BillingStatsRateMode,
} from '@/lib/types'
```

- [ ] **Step 5: 验证前端**

Run（workdir `src/frontend`）: `bun run build && bun run lint && bun run test`
Expected: PASS（`topServer` 变量若在 ActualCostsTab 重构后未使用，检查并保留——原「成本最高服务器」卡片仍在使用）

- [ ] **Step 6: Commit**

```bash
git add src/frontend/src/pages/Costs.tsx
git commit -m "feat(frontend): dual-mode cost stats tabs (actual / estimated)"
```

---

### Task 5: 设计文档实现状态回填 + 全量验证

**Files:**
- Modify: `docs/superpowers/specs/2026-08-02-cost-stats-dual-tabs-design.md`（追加「实现状态」节）
- 验证所有命令

- [ ] **Step 1: 回填实现状态**

在 `docs/superpowers/specs/2026-08-02-cost-stats-dual-tabs-design.md` 末尾追加「## 实现状态」节，列出变更文件、与设计一致/偏差记录（预计无偏差；若实施中有偏差则如实记录）。

- [ ] **Step 2: 全量验证**

Run（仓库根目录）: `go test ./src/backend/...`
Run（workdir `src/frontend`）: `bun run build && bun run lint && bun run test`
Expected: 全部 PASS

- [ ] **Step 3: Commit**

```bash
git add docs/superpowers/specs/2026-08-02-cost-stats-dual-tabs-design.md
git commit -m "docs: mark dual-mode cost stats implementation status"
```

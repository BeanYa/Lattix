package panel

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"lattix/backend/internal/store"
)

func TestCostPeriods(t *testing.T) {
	loc := time.UTC
	parse := func(value string) time.Time {
		t.Helper()
		parsed, err := time.ParseInLocation("2006-01-02", value, loc)
		if err != nil {
			t.Fatal(err)
		}
		return parsed
	}

	days := costPeriods(parse("2025-12-30"), parse("2026-01-02"), costGranDay)
	wantDays := []string{"2025-12-30", "2025-12-31", "2026-01-01", "2026-01-02"}
	if len(days) != len(wantDays) {
		t.Fatalf("day periods = %v, want %v", days, wantDays)
	}
	for i := range wantDays {
		if days[i] != wantDays[i] {
			t.Fatalf("day periods = %v, want %v", days, wantDays)
		}
	}

	months := costPeriods(parse("2024-01-15"), parse("2024-03-10"), costGranMonth)
	wantMonths := []string{"2024-01", "2024-02", "2024-03"}
	if len(months) != len(wantMonths) {
		t.Fatalf("month periods = %v, want %v", months, wantMonths)
	}
	for i := range wantMonths {
		if months[i] != wantMonths[i] {
			t.Fatalf("month periods = %v, want %v", months, wantMonths)
		}
	}

	years := costPeriods(parse("2024-06-01"), parse("2026-05-31"), costGranYear)
	wantYears := []string{"2024", "2025", "2026"}
	if len(years) != len(wantYears) {
		t.Fatalf("year periods = %v, want %v", years, wantYears)
	}
	for i := range wantYears {
		if years[i] != wantYears[i] {
			t.Fatalf("year periods = %v, want %v", years, wantYears)
		}
	}
}

func TestOverlapDaysAndLeapFebruary(t *testing.T) {
	loc := time.UTC
	parse := func(value string) time.Time {
		t.Helper()
		parsed, err := time.ParseInLocation("2006-01-02", value, loc)
		if err != nil {
			t.Fatal(err)
		}
		return parsed
	}

	// 闰年 2 月：2024-02-01 → 2024-03-01 共 29 天。
	if got := daysBetween(parse("2024-02-01"), parse("2024-03-01")); got != 29 {
		t.Fatalf("leap february days = %d, want 29", got)
	}
	if got := daysBetween(parse("2025-02-01"), parse("2025-03-01")); got != 28 {
		t.Fatalf("february days = %d, want 28", got)
	}

	// 服务期部分落在周期内：[01-15, 04-15) 与 1 月 [01-01, 02-01) 重叠 17 天。
	if got := overlapDays(parse("2026-01-15"), parse("2026-04-15"), parse("2026-01-01"), parse("2026-02-01")); got != 17 {
		t.Fatalf("overlap days = %d, want 17", got)
	}
	// 不相交区间。
	if got := overlapDays(parse("2026-01-15"), parse("2026-04-15"), parse("2025-01-01"), parse("2025-12-31")); got != 0 {
		t.Fatalf("disjoint overlap = %d, want 0", got)
	}
	// 区间顺序无关。
	if got := overlapDays(parse("2026-01-01"), parse("2026-02-01"), parse("2026-01-15"), parse("2026-04-15")); got != 17 {
		t.Fatalf("reversed overlap days = %d, want 17", got)
	}
}

func TestBillingServiceEnd(t *testing.T) {
	loc := time.UTC
	cases := []struct {
		name   string
		billing store.ServerBilling
		want   string
	}{
		{
			name: "active ends at renewal",
			billing: store.ServerBilling{Status: store.BillingActive, NextRenewalOn: "2026-04-15"},
			want: "2026-04-15",
		},
		{
			name: "due today ends at renewal",
			billing: store.ServerBilling{Status: store.BillingDueToday, NextRenewalOn: "2026-04-15"},
			want: "2026-04-15",
		},
		{
			name: "assumed valid extends through last online day",
			billing: store.ServerBilling{Status: store.BillingAssumedValid, NextRenewalOn: "2026-03-15", AssumedValidThrough: "2026-03-20"},
			want: "2026-03-21",
		},
		{
			name: "expired falls back to renewal when never assumed valid",
			billing: store.ServerBilling{Status: store.BillingExpired, NextRenewalOn: "2026-03-15"},
			want: "2026-03-15",
		},
		{
			name: "expired keeps later assumed valid through",
			billing: store.ServerBilling{Status: store.BillingExpired, NextRenewalOn: "2026-03-15", AssumedValidThrough: "2026-04-01"},
			want: "2026-04-02",
		},
		{
			name: "grace before renewal never shrinks the paid period",
			billing: store.ServerBilling{Status: store.BillingExpired, NextRenewalOn: "2026-04-15", AssumedValidThrough: "2026-03-01"},
			want: "2026-04-15",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := billingServiceEnd(tc.billing, loc).Format("2006-01-02")
			if got != tc.want {
				t.Fatalf("service end = %s, want %s", got, tc.want)
			}
		})
	}
}

func TestIntervalDailyCost(t *testing.T) {
	if got := intervalDailyCost(8640, 1, "month").RatString(); got != "288" {
		t.Fatalf("monthly daily cost = %s, want 288", got)
	}
	if got := intervalDailyCost(6000, 1, "year").RatString(); got != "1200/73" {
		t.Fatalf("yearly daily cost = %s, want 1200/73", got)
	}
	if got := intervalDailyCost(123, 3, "day").RatString(); got != "41" {
		t.Fatalf("daily cost = %s, want 41", got)
	}
	if intervalDailyCost(100, 1, "fortnight") != nil {
		t.Fatal("unknown unit should produce nil daily cost")
	}
}

func TestEstimatedDailyCost(t *testing.T) {
	// 估算口径：月付 3 个月（季付）= 金额/90；年付 = 金额/(n×360)。
	if got := estimatedDailyCost(30000, 1, "year").RatString(); got != "250/3" {
		t.Fatalf("yearly estimated daily = %s, want 250/3", got)
	}
	if got := estimatedDailyCost(10000, 3, "month").RatString(); got != "1000/9" {
		t.Fatalf("quarter estimated daily = %s, want 1000/9", got)
	}
	if got := estimatedDailyCost(5000, 1, "month").RatString(); got != "500/3" {
		t.Fatalf("monthly estimated daily = %s, want 500/3", got)
	}
	if got := estimatedDailyCost(123, 3, "day").RatString(); got != "41" {
		t.Fatalf("daily estimated cost = %s, want 41", got)
	}
	if estimatedDailyCost(100, 1, "fortnight") != nil {
		t.Fatal("unknown unit should produce nil estimated daily cost")
	}
}

func TestEstimateDays(t *testing.T) {
	loc := time.UTC
	parse := func(value string) time.Time {
		tm, err := time.ParseInLocation("2006-01-02", value, loc)
		if err != nil {
			t.Fatal(err)
		}
		return tm
	}
	cases := []struct {
		from, to string
		want     int64
	}{
		{"2025-05-01", "2025-05-31", 30},  // 整月 = 30 天
		{"2025-05-01", "2025-05-06", 5},   // 剩余天数
		{"2025-01-01", "2025-12-31", 360}, // 整年 = 11 月 + 30 天 = 360
		{"2023-01-01", "2025-05-06", 845}, // 2 年 4 个月 5 天 = 2×360 + 4×30 + 5
		{"2023-06-15", "2025-05-06", 681}, // 跨月借位：22 月 + 21 天
		{"2025-01-01", "2026-01-01", 360}, // 完整年区间
		{"2025-05-06", "2025-05-07", 1},   // 单日
	}
	for _, tc := range cases {
		got := estimateDays(parse(tc.from), parse(tc.to))
		if got != tc.want {
			t.Fatalf("estimateDays(%s, %s) = %d, want %d", tc.from, tc.to, got, tc.want)
		}
	}
}

func TestParseBillingStatsQuery(t *testing.T) {
	loc := time.UTC
	valid := url.Values{"from": {"2026-01-01"}, "to": {"2026-03-31"}, "granularity": {"month"}}
	if _, _, _, _, err := parseBillingStatsQuery(valid, loc); err != nil {
		t.Fatalf("valid query rejected: %v", err)
	}
	// 默认 rate_mode = custom。
	_, _, _, mode, err := parseBillingStatsQuery(valid, loc)
	if err != nil || mode != costModeCustom {
		t.Fatalf("default mode = %q err %v, want custom", mode, err)
	}

	bad := []struct {
		name   string
		values url.Values
	}{
		{"bad from", url.Values{"from": {"2026/01/01"}, "to": {"2026-03-31"}, "granularity": {"month"}}},
		{"bad to", url.Values{"from": {"2026-01-01"}, "to": {"03-31"}, "granularity": {"month"}}},
		{"from after to", url.Values{"from": {"2026-03-31"}, "to": {"2026-01-01"}, "granularity": {"month"}}},
		{"bad granularity", url.Values{"from": {"2026-01-01"}, "to": {"2026-03-31"}, "granularity": {"week"}}},
		{"bad rate mode", url.Values{"from": {"2026-01-01"}, "to": {"2026-03-31"}, "granularity": {"month"}, "rate_mode": {"frankfurter"}}},
		{"day span over limit", url.Values{"from": {"2026-01-01"}, "to": {"2027-01-15"}, "granularity": {"day"}}},
		{"month span over limit", url.Values{"from": {"2006-01-01"}, "to": {"2026-01-01"}, "granularity": {"month"}}},
	}
	for _, tc := range bad {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, _, _, err := parseBillingStatsQuery(tc.values, loc); err == nil {
				t.Fatal("invalid query accepted")
			}
		})
	}
}

// statsTestServer 构造带汇率与统计币种的面板测试服务，时区固定为 UTC。
func statsTestServer(t *testing.T) (*Server, *store.Store) {
	t.Helper()
	rates := multiBaseRates(map[string]string{"EUR": "1", "USD": "1.2", "CNY": "7.2"})
	s := newExchangeTestServer(t, "CNY", rates)
	if err := s.st.SetSetting(context.Background(), store.SettingTimezone, "UTC"); err != nil {
		t.Fatal(err)
	}
	return s, s.st
}

func seedBillingServers(t *testing.T, st *store.Store) (usdID, disabledID, cnyID int64) {
	t.Helper()
	ctx := context.Background()
	usdID, err := st.CreateServerWithPlans(ctx, store.ServerDraft{Alias: "US-LA-Direct", BootstrapToken: "token-usd", MachineType: store.MachineTypeDirect, CountryCode: "US", Location: "Los Angeles"},
		&store.ServerBilling{
			Enabled: true, ProviderID: 1, AmountMinor: 1200, Currency: "USD",
			ServiceStartedOn: "2026-01-15", IntervalCount: 1, IntervalUnit: "month",
			NextRenewalOn: "2026-04-15", Status: store.BillingActive,
		}, store.ServerTrafficPlan{AccountingMode: "outbound", ResetAnchorOn: "2026-01-01", ResetCount: 1, ResetUnit: "month", TrackingStartedOn: "2026-01-01"})
	if err != nil {
		t.Fatal(err)
	}
	disabledID, err = st.CreateServerWithPlans(ctx, store.ServerDraft{Alias: "US-NY-Old", BootstrapToken: "token-old", MachineType: store.MachineTypeDirect, CountryCode: "US", Location: "New York"},
		&store.ServerBilling{
			Enabled: false, ProviderID: 1, AmountMinor: 500, Currency: "USD",
			ServiceStartedOn: "2025-01-01", IntervalCount: 1, IntervalUnit: "month",
			NextRenewalOn: "2026-01-01", Status: store.BillingDisabled,
		}, store.ServerTrafficPlan{AccountingMode: "outbound", ResetAnchorOn: "2025-01-01", ResetCount: 1, ResetUnit: "month", TrackingStartedOn: "2025-01-01"})
	if err != nil {
		t.Fatal(err)
	}
	cnyID, err = st.CreateServerWithPlans(ctx, store.ServerDraft{Alias: "CN-BJ-Yearly", BootstrapToken: "token-cny", MachineType: store.MachineTypeDirect, CountryCode: "CN", Location: "Beijing"},
		&store.ServerBilling{
			Enabled: true, ProviderID: 1, AmountMinor: 6000, Currency: "CNY",
			ServiceStartedOn: "2025-01-01", IntervalCount: 1, IntervalUnit: "year",
			NextRenewalOn: "2026-12-31", Status: store.BillingActive,
		}, store.ServerTrafficPlan{AccountingMode: "outbound", ResetAnchorOn: "2025-01-01", ResetCount: 1, ResetUnit: "month", TrackingStartedOn: "2025-01-01"})
	if err != nil {
		t.Fatal(err)
	}
	return usdID, disabledID, cnyID
}

func getBillingStats(t *testing.T, s *Server, query string) (*billingStatsDTO, *rpcResponse) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/billing/stats?"+query, nil)
	recorder := httptest.NewRecorder()
	s.handleBillingStats(recorder, req)
	var envelope rpcResponse
	if err := json.NewDecoder(recorder.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	var dto billingStatsDTO
	if err := json.Unmarshal([]byte(jsonMarshal(envelope.Data)), &dto); err != nil {
		return nil, &envelope
	}
	return &dto, &envelope
}

func jsonMarshal(value any) string {
	raw, _ := json.Marshal(value)
	return string(raw)
}

func TestBillingStatsHandlerMonthView(t *testing.T) {
	s, st := statsTestServer(t)
	usdID, disabledID, cnyID := seedBillingServers(t, st)

	dto, envelope := getBillingStats(t, s, "from=2026-01-01&to=2026-03-31&granularity=month")
	if envelope.Code != "OK" {
		t.Fatalf("response code = %q message %q", envelope.Code, envelope.Message)
	}
	if dto.ReportingCurrency != "CNY" || dto.Granularity != "month" || dto.RateMode != costModeCustom {
		t.Fatalf("dto header = %+v", dto)
	}
	wantPeriods := []string{"2026-01", "2026-02", "2026-03"}
	if len(dto.Periods) != 3 {
		t.Fatalf("periods = %v, want %v", dto.Periods, wantPeriods)
	}
	if len(dto.Servers) != 2 {
		t.Fatalf("servers = %d, want 2 (disabled excluded)", len(dto.Servers))
	}
	for _, srv := range dto.Servers {
		if srv.ServerID == disabledID {
			t.Fatal("disabled billing server leaked into stats")
		}
	}

	var usd, cny *billingServerStatsDTO
	for i := range dto.Servers {
		switch dto.Servers[i].ServerID {
		case usdID:
			usd = &dto.Servers[i]
		case cnyID:
			cny = &dto.Servers[i]
		}
	}
	if usd == nil || cny == nil {
		t.Fatal("expected usd and cny servers in stats")
	}
	// USD $12/月 → 1 USD = 6 CNY → 7200 minor CNY/月 → 240/天。
	// 1 月服务 01-15..01-31 = 17 天 → 4080；2 月 28 天 → 6720；3 月 31 天 → 7440。
	if usd.DailyMinor != 240 {
		t.Fatalf("usd daily = %d, want 240", usd.DailyMinor)
	}
	if usd.DaysActive != 76 {
		t.Fatalf("usd days_active = %d, want 76", usd.DaysActive)
	}
	wantUSD := []int64{4080, 6720, 7440}
	for i := range wantUSD {
		if usd.ActualCostsPublic[i] != wantUSD[i] {
			t.Fatalf("usd costs = %v, want %v", usd.ActualCostsPublic, wantUSD)
		}
	}
	// CNY 年付 ¥60/年 → 6000/365/天；1 月 31 天 → 510，2 月 28 天 → 460，3 月 31 天 → 510。
	if cny.DaysActive != 90 {
		t.Fatalf("cny days_active = %d, want 90", cny.DaysActive)
	}
	wantCNY := []int64{510, 460, 510}
	for i := range wantCNY {
		if cny.ActualCostsPublic[i] != wantCNY[i] {
			t.Fatalf("cny costs = %v, want %v", cny.ActualCostsPublic, wantCNY)
		}
	}
	if got := dto.ActualTotalsPublic; len(got) != 3 || got[0] != 4080+510 || got[1] != 6720+460 || got[2] != 7440+510 {
		t.Fatalf("totals_public = %v", got)
	}
	if dto.CustomAvailable {
		t.Fatal("custom_available should be false without anchors")
	}
	if dto.RateDate != "2026-07-29" {
		t.Fatalf("rate_date = %q, want 2026-07-29", dto.RateDate)
	}

	// 日视图单元格之和必须等于月视图（同一舍入口径）。
	dayDTO, _ := getBillingStats(t, s, "from=2026-01-15&to=2026-03-31&granularity=day")
	if dayDTO == nil {
		t.Fatal("day view failed")
	}
	var usdDays []int64
	for i := range dayDTO.Servers {
		if dayDTO.Servers[i].ServerID == usdID {
			usdDays = dayDTO.Servers[i].ActualCostsPublic
		}
	}
	jan, feb, mar := int64(0), int64(0), int64(0)
	for i, label := range dayDTO.Periods {
		switch label[:7] {
		case "2026-01":
			jan += usdDays[i]
		case "2026-02":
			feb += usdDays[i]
		case "2026-03":
			mar += usdDays[i]
		}
	}
	if jan != 4080 || feb != 6720 || mar != 7440 {
		t.Fatalf("day sums = %d/%d/%d, want 4080/6720/7440", jan, feb, mar)
	}
}

func TestBillingStatsHandlerCustomRates(t *testing.T) {
	ctx := context.Background()
	s, st := statsTestServer(t)
	usdID, _, cnyID := seedBillingServers(t, st)
	if _, err := st.UpsertCustomExchangeRate(ctx, store.CustomExchangeRate{
		SourceCurrency: "USD", SourceAmount: "1", TargetCurrency: "CNY", TargetAmount: "7", Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}

	dto, _ := getBillingStats(t, s, "from=2026-01-01&to=2026-03-31&granularity=month&rate_mode=custom")
	if dto == nil || !dto.CustomAvailable {
		t.Fatal("custom_available should be true with matching anchor")
	}
	if dto.ActualTotalsCustom == nil || dto.ActualTotalsPublic == nil {
		t.Fatal("custom mode should carry both totals")
	}
	for i := range dto.Servers {
		if dto.Servers[i].ServerID != usdID {
			continue
		}
		// 12 USD → 84 CNY = 8400 minor/月 → 280/天。
		if dto.Servers[i].DailyCustomMinor != 280 {
			t.Fatalf("custom daily = %d, want 280", dto.Servers[i].DailyCustomMinor)
		}
		want := []int64{4760, 7840, 8680}
		for j := range want {
			if dto.Servers[i].ActualCostsCustom[j] != want[j] {
				t.Fatalf("custom costs = %v, want %v", dto.Servers[i].ActualCostsCustom, want)
			}
		}
		// 公共口径仍为 240/天。
		if dto.Servers[i].DailyMinor != 240 {
			t.Fatalf("public daily = %d, want 240", dto.Servers[i].DailyMinor)
		}
	}

	// 无自定义锚点的服务器回退到公共口径单元格，且计入 custom 合计。
	var cny *billingServerStatsDTO
	for i := range dto.Servers {
		if dto.Servers[i].ServerID == cnyID {
			cny = &dto.Servers[i]
		}
	}
	if cny == nil {
		t.Fatal("expected cny server in stats")
	}
	wantFallback := []int64{510, 460, 510}
	for j := range wantFallback {
		if cny.ActualCostsCustom[j] != wantFallback[j] {
			t.Fatalf("cny custom fallback = %v, want %v", cny.ActualCostsCustom, wantFallback)
		}
	}
	wantTotals := []int64{4760 + 510, 7840 + 460, 8680 + 510}
	for j := range wantTotals {
		if dto.ActualTotalsCustom[j] != wantTotals[j] {
			t.Fatalf("totals_custom = %v, want %v", dto.ActualTotalsCustom, wantTotals)
		}
	}

	// public 模式不携带 custom 数值，但 custom_available 仍是数据属性。
	pubDTO, _ := getBillingStats(t, s, "from=2026-01-01&to=2026-03-31&granularity=month&rate_mode=public")
	if pubDTO == nil || !pubDTO.CustomAvailable {
		t.Fatal("custom_available should be independent of rate_mode")
	}
	for i := range pubDTO.Servers {
		if pubDTO.Servers[i].ActualCostsCustom != nil || pubDTO.ActualTotalsCustom != nil {
			t.Fatal("public mode must not carry custom payloads")
		}
	}
}

func TestBillingStatsHandlerYearView(t *testing.T) {
	s, st := statsTestServer(t)
	_, _, cnyID := seedBillingServers(t, st)

	// 年视图：¥60/年 的单元格应精确还原为 6000 minor（6000/365 × 365 精确抵消），
	// 而非逐日舍入后累加的 365 × 16 = 5840。
	dto, envelope := getBillingStats(t, s, "from=2025-01-01&to=2025-12-31&granularity=year")
	if envelope.Code != "OK" {
		t.Fatalf("response code = %q message %q", envelope.Code, envelope.Message)
	}
	if len(dto.Periods) != 1 || dto.Periods[0] != "2025" {
		t.Fatalf("periods = %v, want [2025]", dto.Periods)
	}
	var cny *billingServerStatsDTO
	for i := range dto.Servers {
		if dto.Servers[i].ServerID == cnyID {
			cny = &dto.Servers[i]
		}
	}
	if cny == nil || cny.DaysActive != 365 || cny.ActualCostsPublic[0] != 6000 {
		t.Fatalf("year view server = %+v, want days 365 cost 6000", cny)
	}
	if dto.ActualTotalsPublic[0] != 6000 {
		t.Fatalf("year totals = %v, want [6000]", dto.ActualTotalsPublic)
	}
}

func TestBillingStatsHandlerValidation(t *testing.T) {
	s, _ := statsTestServer(t)
	cases := []string{
		"from=2026-01-01&to=2026-03-31&granularity=week",
		"from=2026-03-31&to=2026-01-01&granularity=month",
		"from=bad&to=2026-01-01&granularity=month",
		"from=2026-01-01&to=2027-01-15&granularity=day",
	}
	for _, query := range cases {
		req := httptest.NewRequest(http.MethodGet, "/api/billing/stats?"+query, nil)
		recorder := httptest.NewRecorder()
		s.handleBillingStats(recorder, req)
		var envelope rpcResponse
		if err := json.NewDecoder(recorder.Body).Decode(&envelope); err != nil {
			t.Fatalf("decode response for %q: %v", query, err)
		}
		if envelope.Code == "OK" {
			t.Fatalf("invalid query accepted: %s", query)
		}
	}
}

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
	id, err := st.CreateServerWithPlans(context.Background(), store.ServerDraft{Alias: "HK-Expired", BootstrapToken: "token-exp", MachineType: store.MachineTypeDirect, CountryCode: "HK", Location: "Hong Kong"},
		&store.ServerBilling{
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
		if srv.EstimatedCostsCustom != nil {
			t.Fatal("estimated_costs_custom should be nil without anchors")
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
	// USD $12/月 → 7200 minor CNY/月 → 240/天；月成本 = 实际月付 7200、年成本 = ×12。
	// 月单元格 = 240×30 = 7200（估算口径整月恒为 30 天）。
	if usd.DailyMinor != 240 || usd.DaysActive != 90 {
		t.Fatalf("usd daily/days = %d/%d, want 240/90", usd.DailyMinor, usd.DaysActive)
	}
	if usd.MonthlyMinor != 7200 || usd.AnnualMinor != 86400 {
		t.Fatalf("usd monthly/annual = %d/%d, want 7200/86400", usd.MonthlyMinor, usd.AnnualMinor)
	}
	wantUSD := []int64{7200, 7200, 7200}
	for i := range wantUSD {
		if usd.EstimatedCostsPublic[i] != wantUSD[i] {
			t.Fatalf("usd estimated = %v, want %v", usd.EstimatedCostsPublic, wantUSD)
		}
	}
	// CNY 年付 ¥60/年：日成本 = 6000/360 = 16.67 → 17；月成本 = 年成本/12 = 500；
	// 月单元格 = round(6000/360×30) = 500。
	wantCNY := []int64{500, 500, 500}
	for i := range wantCNY {
		if cny.EstimatedCostsPublic[i] != wantCNY[i] {
			t.Fatalf("cny estimated = %v, want %v", cny.EstimatedCostsPublic, wantCNY)
		}
	}
	if cny.DailyMinor != 17 || cny.MonthlyMinor != 500 || cny.AnnualMinor != 6000 {
		t.Fatalf("cny daily/monthly/annual = %d/%d/%d, want 17/500/6000",
			cny.DailyMinor, cny.MonthlyMinor, cny.AnnualMinor)
	}
	wantTotals := []int64{7200 + 500, 7200 + 500, 7200 + 500}
	for i := range wantTotals {
		if dto.EstimatedTotalsPublic[i] != wantTotals[i] {
			t.Fatalf("estimated totals = %v, want %v", dto.EstimatedTotalsPublic, wantTotals)
		}
	}
	if dto.CustomAvailable {
		t.Fatal("custom_available should be false without anchors")
	}
	if dto.EstimatedTotalsCustom != nil {
		t.Fatal("estimated_totals_custom should be nil without anchors")
	}
	if dto.RateDate != "2026-07-29" {
		t.Fatalf("rate_date = %q, want 2026-07-29", dto.RateDate)
	}
}

func TestEstimatedBillingStatsHandlerGranularities(t *testing.T) {
	s, st := statsTestServer(t)
	usdID, _, cnyID := seedBillingServers(t, st)

	// 日视图：5 个周期，每单元格 = 240 与 round(6000/360) = 17。
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
				if cell != 17 {
					t.Fatalf("cny day cell %d = %d, want 17", j, cell)
				}
			}
		}
	}
	if dayDTO.Servers[0].DaysActive != 5 {
		t.Fatalf("days_active = %d, want 5 (range days)", dayDTO.Servers[0].DaysActive)
	}

	// 年视图：6000/360 × 360 精确还原年价 6000。
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

func TestEstimatedBillingStatsHandlerCustomRange(t *testing.T) {
	s, st := statsTestServer(t)
	ctx := context.Background()
	// 年付 ¥300/年：估算公式 = 300×2 + (300/12)×4 + ((300/12)/30)×5 = 704.17。
	yearID, err := st.CreateServerWithPlans(ctx, store.ServerDraft{Alias: "YR-300", BootstrapToken: "token-yr300", MachineType: store.MachineTypeDirect, CountryCode: "NL", Location: "Amsterdam"},
		&store.ServerBilling{
			Enabled: true, ProviderID: 1, AmountMinor: 30000, Currency: "CNY",
			ServiceStartedOn: "2023-01-01", IntervalCount: 1, IntervalUnit: "year",
			NextRenewalOn: "2030-01-01", Status: store.BillingActive,
		}, store.ServerTrafficPlan{AccountingMode: "outbound", ResetAnchorOn: "2023-01-01", ResetCount: 1, ResetUnit: "month", TrackingStartedOn: "2023-01-01"})
	if err != nil {
		t.Fatal(err)
	}
	// 季付 ¥100/季（3 个月）：年成本 = 100×4、月成本 = 100/3、日成本 = (100/3)/30。
	quarterID, err := st.CreateServerWithPlans(ctx, store.ServerDraft{Alias: "QR-100", BootstrapToken: "token-qr100", MachineType: store.MachineTypeDirect, CountryCode: "US", Location: "Los Angeles"}, &store.ServerBilling{
			Enabled: true, ProviderID: 1, AmountMinor: 10000, Currency: "CNY", ServiceStartedOn: "2023-01-01", IntervalCount: 3, IntervalUnit: "month", NextRenewalOn: "2030-01-01", Status: store.BillingActive, }, store.ServerTrafficPlan{AccountingMode: "outbound", ResetAnchorOn: "2023-01-01", ResetCount: 1, ResetUnit: "month", TrackingStartedOn: "2023-01-01"})
	if err != nil {
		t.Fatal(err)
	}

	// 自定义范围 2 年 4 个月 5 天（2023-01-01 ~ 2025-05-06），月视图：28 个整月 × 月成本 + 5 天 × 日成本。
	dto, envelope := getEstimatedBillingStats(t, s, "from=2023-01-01&to=2025-05-06&granularity=month")
	if envelope.Code != "OK" {
		t.Fatalf("response code = %q message %q", envelope.Code, envelope.Message)
	}
	if len(dto.Periods) != 29 {
		t.Fatalf("periods = %d, want 29", len(dto.Periods))
	}
	for i := range dto.Servers {
		var wantMonthly, wantDaily, wantTotal, wantPartial int64
		switch dto.Servers[i].ServerID {
		case yearID:
			// 年付：月成本 2500、日成本 2500/30 = 83.33；28 个整月 × 2500 + 月末 5 天
			// round(83.33×5) = 417 → 合计 70417（≈ 用户公式 300×2 + 25×4 + 25/30×5）。
			wantMonthly, wantDaily, wantTotal, wantPartial = 2500, 83, 70417, 417
		case quarterID:
			// 季付：月成本 10000/3 = 3333.33、日成本 (10000/3)/30 = 111.11；
			// 28×3333 + round(111.11×5) = 556 → 合计 93880。
			wantMonthly, wantDaily, wantTotal, wantPartial = 3333, 111, 93880, 556
		}
		if dto.Servers[i].MonthlyMinor != wantMonthly || dto.Servers[i].DailyMinor != wantDaily {
			t.Fatalf("server %d monthly/daily = %d/%d, want %d/%d",
				dto.Servers[i].ServerID, dto.Servers[i].MonthlyMinor, dto.Servers[i].DailyMinor,
				wantMonthly, wantDaily)
		}
		var total int64
		for _, cell := range dto.Servers[i].EstimatedCostsPublic {
			total += cell
		}
		if total != wantTotal {
			t.Fatalf("server %d custom range total = %d, want %d", dto.Servers[i].ServerID, total, wantTotal)
		}
		if got := dto.Servers[i].EstimatedCostsPublic[28]; got != wantPartial {
			t.Fatalf("server %d last partial month cell = %d, want %d", dto.Servers[i].ServerID, got, wantPartial)
		}
	}
}

func TestEstimatedBillingStatsHandlerCustomRates(t *testing.T) {
	ctx := context.Background()
	s, st := statsTestServer(t)
	usdID, _, cnyID := seedBillingServers(t, st)
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
		if dto.Servers[i].MonthlyCustomMinor != 8400 || dto.Servers[i].AnnualCustomMinor != 100800 {
			t.Fatalf("custom monthly/annual = %d/%d, want 8400/100800",
				dto.Servers[i].MonthlyCustomMinor, dto.Servers[i].AnnualCustomMinor)
		}
		wantCells := []int64{8400, 8400, 8400}
		for j, cell := range dto.Servers[i].EstimatedCostsCustom {
			if cell != wantCells[j] {
				t.Fatalf("custom cell %d = %d, want %d", j, cell, wantCells[j])
			}
		}
		if dto.Servers[i].DailyMinor != 240 {
			t.Fatalf("public daily = %d, want 240", dto.Servers[i].DailyMinor)
		}
	}

	// 无自定义锚点的服务器回退到公共口径单元格，且计入 custom 合计。
	var cny *estimatedBillingServerStatsDTO
	for i := range dto.Servers {
		if dto.Servers[i].ServerID == cnyID {
			cny = &dto.Servers[i]
		}
	}
	if cny == nil {
		t.Fatal("expected cny server in stats")
	}
	wantFallback := []int64{500, 500, 500}
	for j := range wantFallback {
		if cny.EstimatedCostsCustom[j] != wantFallback[j] {
			t.Fatalf("cny custom fallback = %v, want %v", cny.EstimatedCostsCustom, wantFallback)
		}
	}
	wantTotals := []int64{8400 + 500, 8400 + 500, 8400 + 500}
	for j := range wantTotals {
		if dto.EstimatedTotalsCustom[j] != wantTotals[j] {
			t.Fatalf("totals_custom = %v, want %v", dto.EstimatedTotalsCustom, wantTotals)
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

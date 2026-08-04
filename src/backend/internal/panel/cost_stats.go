package panel

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"time"

	"lattix/backend/internal/store"
)

const (
	costGranDay   = "day"
	costGranMonth = "month"
	costGranYear  = "year"

	costModePublic = "public"
	costModeCustom = "custom"

	// 跨度上限：日粒度 ≤ 372 天（覆盖一年有余），月/年粒度 ≤ 10 年。
	costMaxDaySpan   = 372
	costMaxMonthSpan = 3660
)

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
// MonthlyMinor/AnnualMinor 为换算后月/年成本（= 日成本 × 30 / × 360，估算口径）。
type estimatedBillingServerStatsDTO struct {
	billingServerStatsBase
	MonthlyMinor       int64   `json:"monthly_minor"`
	AnnualMinor        int64   `json:"annual_minor"`
	MonthlyCustomMinor int64   `json:"monthly_custom_minor,omitempty"`
	AnnualCustomMinor  int64   `json:"annual_custom_minor,omitempty"`
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

// intervalDays 把计费周期折算成天数：日 = count、月 = count*30、年 = count*365。
func intervalDays(unit string, count int) int64 {
	switch unit {
	case "day":
		return int64(count)
	case "month":
		return int64(count) * 30
	case "year":
		return int64(count) * 365
	}
	return 0
}

// intervalDailyCost 返回换算后整周期金额的日均成本（统计币种最小单位，big.Rat 精确值）。
func intervalDailyCost(amountMinor int64, count int, unit string) *big.Rat {
	days := intervalDays(unit, count)
	if days <= 0 {
		return nil
	}
	return new(big.Rat).SetFrac(new(big.Int).SetInt64(amountMinor), big.NewInt(days))
}

// estimatedIntervalDays 把计费周期折算成计算成本口径天数：日 = count、月 = count*30、
// 年 = count*360（12 个月 × 30 天）。年按 360 天计使年付价可精确还原为年成本
// （月成本 = 年成本/12、日成本 = 年成本/360），与已生效口径的 365 天年区分。
func estimatedIntervalDays(unit string, count int) int64 {
	switch unit {
	case "day":
		return int64(count)
	case "month":
		return int64(count) * 30
	case "year":
		return int64(count) * 360
	}
	return 0
}

// estimatedDailyCost 返回计算成本口径下换算后整周期金额的日均成本（big.Rat 精确值）。
func estimatedDailyCost(amountMinor int64, count int, unit string) *big.Rat {
	days := estimatedIntervalDays(unit, count)
	if days <= 0 {
		return nil
	}
	return new(big.Rat).SetFrac(new(big.Int).SetInt64(amountMinor), big.NewInt(days))
}

// estimateDays 返回计算成本口径下 [from, to) 区间的计费天数：整月按 30 天、整年按 360 天、
// 剩余天数按日计，等价于「年成本 × 整年 + 月成本 × 整月 + 日成本 × 剩余天」公式
// （月差借位按 30 天处理）。完整月/年区间恒得 30/360，天然覆盖各粒度。
func estimateDays(from, to time.Time) int64 {
	months := int64(to.Year()-from.Year())*12 + int64(to.Month()-from.Month())
	days := int64(to.Day() - from.Day())
	if days < 0 {
		days += 30
		months--
	}
	return months*30 + days
}

// statsRow 是成本统计中单台参与服务器的装配结果（两种口径共用）。
// dailyPublic/dailyCustom 为已生效口径日成本（365 天年）；estDailyPublic/estDailyCustom
// 为计算成本口径日成本（360 天年，月成本 × 30 / 年成本 × 360 精确）。
type statsRow struct {
	billing          store.ServerBilling
	base             billingServerStatsBase
	dailyPublic      *big.Rat
	dailyCustom      *big.Rat
	estDailyPublic   *big.Rat
	estDailyCustom   *big.Rat
	customDiffers    bool
	rateDate         string
}

// errCostConversion 标记 convertCosts 换算失败（上游汇率获取错误，映射 502）。
var errCostConversion = errors.New("cost conversion failed")

// writeStatsLoadError 把 loadStatsRows 的错误映射为 HTTP 状态：换算失败 → 502，其余 → 500。
func writeStatsLoadError(w http.ResponseWriter, err error) {
	if errors.Is(err, errCostConversion) {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeError(w, 500, err.Error())
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
			return nil, fmt.Errorf("%w: %v", errCostConversion, err)
		}
		dailyPublic := intervalDailyCost(public.AmountMinor, b.IntervalCount, b.IntervalUnit)
		row := statsRow{
			billing:        b,
			dailyPublic:    dailyPublic,
			estDailyPublic: estimatedDailyCost(public.AmountMinor, b.IntervalCount, b.IntervalUnit),
			rateDate:       public.RateDate,
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
			row.estDailyCustom = estimatedDailyCost(custom.AmountMinor, b.IntervalCount, b.IntervalUnit)
			row.customDiffers = custom.AmountMinor != public.AmountMinor
		}
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].base.ServerID < rows[j].base.ServerID })
	return rows, nil
}

// billingServiceEnd 返回服务器服务期的排他截止日（含起不含止）。
// active/due_today：已付费到续费日；assumed_valid/expired：宽限到最后一个推定有效日 +1。
func billingServiceEnd(b store.ServerBilling, loc *time.Location) time.Time {
	end, err := time.ParseInLocation("2006-01-02", b.NextRenewalOn, loc)
	if err != nil {
		return time.Time{}
	}
	switch b.Status {
	case store.BillingAssumedValid, store.BillingExpired:
		if through, err := time.ParseInLocation("2006-01-02", b.AssumedValidThrough, loc); err == nil {
			grace := through.AddDate(0, 0, 1)
			if grace.After(end) {
				return grace
			}
		}
	}
	return end
}

// costPeriods 生成覆盖 [from, to] 的所有日历周期标签：
// day = YYYY-MM-DD、month = YYYY-MM、year = YYYY。
func costPeriods(from, to time.Time, gran string) []string {
	var out []string
	switch gran {
	case costGranDay:
		for d := from; !d.After(to); d = d.AddDate(0, 0, 1) {
			out = append(out, d.Format("2006-01-02"))
		}
	case costGranMonth:
		year, month := from.Year(), from.Month()
		for year < to.Year() || (year == to.Year() && month <= to.Month()) {
			out = append(out, fmt.Sprintf("%04d-%02d", year, month))
			if month == time.December {
				year++
				month = time.January
			} else {
				month++
			}
		}
	case costGranYear:
		for year := from.Year(); year <= to.Year(); year++ {
			out = append(out, strconv.Itoa(year))
		}
	}
	return out
}

// periodBounds 返回周期标签对应的 [起, 止) 日历区间。
func periodBounds(label string, gran string, loc *time.Location) (time.Time, time.Time) {
	switch gran {
	case costGranDay:
		if t, err := time.ParseInLocation("2006-01-02", label, loc); err == nil {
			return t, t.AddDate(0, 0, 1)
		}
	case costGranMonth:
		if t, err := time.ParseInLocation("2006-01", label, loc); err == nil {
			return t, t.AddDate(0, 1, 0)
		}
	case costGranYear:
		if t, err := time.ParseInLocation("2006", label, loc); err == nil {
			return t, t.AddDate(1, 0, 0)
		}
	}
	return time.Time{}, time.Time{}
}

// daysBetween 返回 [a, b) 之间的日历天数（UTC 计算，不受 DST 影响）。
func daysBetween(a, b time.Time) int {
	da := time.Date(a.Year(), a.Month(), a.Day(), 0, 0, 0, 0, time.UTC)
	db := time.Date(b.Year(), b.Month(), b.Day(), 0, 0, 0, 0, time.UTC)
	return int(db.Sub(da).Hours() / 24)
}

// overlapDays 返回两个 [起, 止) 区间重叠的日历天数。
func overlapDays(aStart, aEnd, bStart, bEnd time.Time) int {
	start, end := aStart, aEnd
	if bStart.After(start) {
		start = bStart
	}
	if bEnd.Before(end) {
		end = bEnd
	}
	if !end.After(start) {
		return 0
	}
	return daysBetween(start, end)
}

func parseBillingStatsQuery(q url.Values, loc *time.Location) (from, to time.Time, gran, mode string, err error) {
	gran = q.Get("granularity")
	mode = q.Get("rate_mode")
	if mode == "" {
		mode = costModeCustom
	}
	if gran != costGranDay && gran != costGranMonth && gran != costGranYear {
		return time.Time{}, time.Time{}, "", "", errors.New("无效的统计粒度")
	}
	if mode != costModePublic && mode != costModeCustom {
		return time.Time{}, time.Time{}, "", "", errors.New("无效的换算方式")
	}
	from, err = time.ParseInLocation("2006-01-02", q.Get("from"), loc)
	if err != nil {
		return time.Time{}, time.Time{}, "", "", errors.New("起始日期格式无效")
	}
	to, err = time.ParseInLocation("2006-01-02", q.Get("to"), loc)
	if err != nil {
		return time.Time{}, time.Time{}, "", "", errors.New("结束日期格式无效")
	}
	if from.After(to) {
		return time.Time{}, time.Time{}, "", "", errors.New("起始日期不能晚于结束日期")
	}
	span := daysBetween(from, to) + 1
	if gran == costGranDay {
		if span > costMaxDaySpan {
			return time.Time{}, time.Time{}, "", "", fmt.Errorf("日粒度范围不能超过 %d 天", costMaxDaySpan)
		}
	} else if span > costMaxMonthSpan {
		return time.Time{}, time.Time{}, "", "", errors.New("月/年粒度范围不能超过 10 年")
	}
	return from, to, gran, mode, nil
}

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
		writeStatsLoadError(w, err)
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
			if costsCustom != nil {
				if row.dailyCustom != nil {
					cost := new(big.Rat).Mul(row.dailyCustom, new(big.Rat).SetInt64(int64(days)))
					costsCustom[i] = roundRat(cost)
				} else {
					costsCustom[i] = item.ActualCostsPublic[i]
				}
				totalsCustom[i] += costsCustom[i]
			}
		}
		if costsCustom != nil {
			item.ActualCostsCustom = costsCustom
		}
		dto.Servers = append(dto.Servers, item)
	}
	if totalsCustom != nil {
		dto.ActualTotalsCustom = totalsCustom
	}
	writeJSON(w, http.StatusOK, dto)
}

// handleEstimatedBillingStats 处理 GET /api/billing/stats/estimated（计算成本）：对启用统计计费
// 且未过期的服务器按"日成本 × 周期天数"估算每周期成本（忽略服务期与已生效部分）。估算口径
// 按 12 个月 × 30 天计：日成本 = 周期金额 ÷ (周期数 × 每周期天数)，月成本 = 日成本 × 30、
// 年成本 = 日成本 × 360；范围成本 = 年成本 × 整年 + 月成本 × 整月 + 日成本 × 剩余天。
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
		writeStatsLoadError(w, err)
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
	for _, row := range rows {
		base := row.base
		base.DaysActive = spanDays
		base.DailyMinor = roundRat(row.estDailyPublic)
		if row.estDailyCustom != nil {
			base.DailyCustomMinor = roundRat(row.estDailyCustom)
		}
		item := estimatedBillingServerStatsDTO{
			billingServerStatsBase: base,
			MonthlyMinor:           roundRat(new(big.Rat).Mul(row.estDailyPublic, big.NewRat(30, 1))),
			AnnualMinor:            roundRat(new(big.Rat).Mul(row.estDailyPublic, big.NewRat(360, 1))),
			EstimatedCostsPublic:   make([]int64, len(periods)),
		}
		if row.estDailyCustom != nil {
			item.MonthlyCustomMinor = roundRat(new(big.Rat).Mul(row.estDailyCustom, big.NewRat(30, 1)))
			item.AnnualCustomMinor = roundRat(new(big.Rat).Mul(row.estDailyCustom, big.NewRat(360, 1)))
		}
		var costsCustom []int64
		if totalsCustom != nil {
			costsCustom = make([]int64, len(periods))
		}
		for i, label := range periods {
			pStart, pEnd := periodBounds(label, gran, loc)
			days := int64(0)
			if gran == costGranDay {
				days = 1
			} else {
				ovStart, ovEnd := pStart, pEnd
				if from.After(ovStart) {
					ovStart = from
				}
				if to.Before(ovEnd) {
					ovEnd = to
				}
				if ovEnd.After(ovStart) {
					days = estimateDays(ovStart, ovEnd)
				}
			}
			cost := new(big.Rat).Mul(row.estDailyPublic, new(big.Rat).SetInt64(days))
			item.EstimatedCostsPublic[i] = roundRat(cost)
			dto.EstimatedTotalsPublic[i] += item.EstimatedCostsPublic[i]
			if costsCustom != nil {
				if row.estDailyCustom != nil {
					cost := new(big.Rat).Mul(row.estDailyCustom, new(big.Rat).SetInt64(days))
					costsCustom[i] = roundRat(cost)
				} else {
					costsCustom[i] = item.EstimatedCostsPublic[i]
				}
				totalsCustom[i] += costsCustom[i]
			}
		}
		if costsCustom != nil {
			item.EstimatedCostsCustom = costsCustom
		}
		dto.Servers = append(dto.Servers, item)
	}
	if totalsCustom != nil {
		dto.EstimatedTotalsCustom = totalsCustom
	}
	writeJSON(w, http.StatusOK, dto)
}

func (s *Server) reportingCurrency(ctx context.Context) string {
	currency := s.getSetting(ctx, store.SettingReportingCurrency)
	if currency == "" {
		return "CNY"
	}
	return currency
}

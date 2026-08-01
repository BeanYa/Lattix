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

// billingServerStatsDTO 是成本统计中单台服务器的周期成本序列（§成本统计设计）。
type billingServerStatsDTO struct {
	ServerID         int64   `json:"server_id"`
	Alias            string  `json:"alias"`
	CountryCode      string  `json:"country_code"`
	Location         string  `json:"location"`
	Currency         string  `json:"currency"`
	AmountMinor      int64   `json:"amount_minor"`
	IntervalCount    int     `json:"interval_count"`
	IntervalUnit     string  `json:"interval_unit"`
	ServiceStartedOn string  `json:"service_started_on"`
	Status           string  `json:"status"`
	DaysActive       int     `json:"days_active"`
	DailyMinor       int64   `json:"daily_minor"`
	DailyCustomMinor int64   `json:"daily_custom_minor,omitempty"`
	CostsPublic      []int64 `json:"costs_public"`
	CostsCustom      []int64 `json:"costs_custom,omitempty"`
}

// billingStatsDTO 是 GET /api/billing/stats 的响应。
type billingStatsDTO struct {
	ReportingCurrency string                    `json:"reporting_currency"`
	Granularity       string                    `json:"granularity"`
	From              string                    `json:"from"`
	To                string                    `json:"to"`
	RateMode          string                    `json:"rate_mode"`
	RateDate          string                    `json:"rate_date,omitempty"`
	CustomAvailable   bool                      `json:"custom_available"`
	Periods           []string                  `json:"periods"`
	Servers           []billingServerStatsDTO   `json:"servers"`
	TotalsPublic      []int64                   `json:"totals_public"`
	TotalsCustom      []int64                   `json:"totals_custom,omitempty"`
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

// handleBillingStats 处理 GET /api/billing/stats：对所有启用统计计费的服务器按
// 日/月/年周期摊算成本（复用 convertCosts 换算到统计币种），返回周期 × 服务器矩阵。
func (s *Server) handleBillingStats(w http.ResponseWriter, r *http.Request) {
	loc := s.inspectionLocation(r.Context())
	from, to, gran, mode, err := parseBillingStatsQuery(r.URL.Query(), loc)
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}
	servers, err := s.st.ListServers(r.Context())
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	billing, err := s.st.ServerBillingMap(r.Context())
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	serversByID := make(map[int64]store.Server, len(servers))
	for _, srv := range servers {
		serversByID[srv.ID] = srv
	}

	enabled := make([]store.ServerBilling, 0, len(billing))
	for _, b := range billing {
		if b.Enabled {
			enabled = append(enabled, b)
		}
	}
	sort.Slice(enabled, func(i, j int) bool { return enabled[i].ServerID < enabled[j].ServerID })

	periods := costPeriods(from, to, gran)
	totalsPublic := make([]int64, len(periods))
	totalsCustom := make([]int64, len(periods))
	dto := billingStatsDTO{
		ReportingCurrency: s.reportingCurrency(r.Context()),
		Granularity:       gran,
		From:              from.Format("2006-01-02"),
		To:                to.Format("2006-01-02"),
		RateMode:          mode,
		Periods:           periods,
		Servers:           []billingServerStatsDTO{},
	}

	for _, b := range enabled {
		srv, ok := serversByID[b.ServerID]
		if !ok {
			continue
		}
		public, custom, err := s.convertCosts(r.Context(), b.AmountMinor, b.Currency)
		if err != nil {
			writeError(w, http.StatusBadGateway, err.Error())
			return
		}
		start, err := time.ParseInLocation("2006-01-02", b.ServiceStartedOn, loc)
		if err != nil {
			continue
		}
		end := billingServiceEnd(b, loc)
		dailyPublic := intervalDailyCost(public.AmountMinor, b.IntervalCount, b.IntervalUnit)
		var dailyCustom *big.Rat
		if custom != nil {
			dailyCustom = intervalDailyCost(custom.AmountMinor, b.IntervalCount, b.IntervalUnit)
			if custom.AmountMinor != public.AmountMinor {
				dto.CustomAvailable = true
			}
		}

		item := billingServerStatsDTO{
			ServerID: b.ServerID, Alias: srv.Alias, CountryCode: srv.CountryCode, Location: srv.Location,
			Currency: b.Currency, AmountMinor: b.AmountMinor, IntervalCount: b.IntervalCount,
			IntervalUnit: b.IntervalUnit, ServiceStartedOn: b.ServiceStartedOn, Status: b.Status,
			DailyMinor: roundRat(dailyPublic),
			CostsPublic: make([]int64, len(periods)),
		}
		var costsCustom []int64
		if custom != nil {
			item.DailyCustomMinor = roundRat(dailyCustom)
			costsCustom = make([]int64, len(periods))
		}
		for i, label := range periods {
			pStart, pEnd := periodBounds(label, gran, loc)
			days := overlapDays(start, end, pStart, pEnd)
			if days <= 0 {
				continue
			}
			item.DaysActive += days
			cost := new(big.Rat).Mul(dailyPublic, new(big.Rat).SetInt64(int64(days)))
			item.CostsPublic[i] = roundRat(cost)
			totalsPublic[i] += item.CostsPublic[i]
			if costsCustom != nil {
				cost := new(big.Rat).Mul(dailyCustom, new(big.Rat).SetInt64(int64(days)))
				costsCustom[i] = roundRat(cost)
				totalsCustom[i] += costsCustom[i]
			}
		}
		if mode == costModeCustom && dto.CustomAvailable {
			if costsCustom == nil {
				costsCustom = append([]int64(nil), item.CostsPublic...)
			}
			item.CostsCustom = costsCustom
		}
		if dto.RateDate == "" && public.RateDate != "" {
			dto.RateDate = public.RateDate
		}
		dto.Servers = append(dto.Servers, item)
	}

	if mode == costModeCustom && dto.CustomAvailable {
		dto.TotalsCustom = totalsCustom
	}
	dto.TotalsPublic = totalsPublic
	writeJSON(w, http.StatusOK, dto)
}

func (s *Server) reportingCurrency(ctx context.Context) string {
	currency := s.getSetting(ctx, store.SettingReportingCurrency)
	if currency == "" {
		return "CNY"
	}
	return currency
}

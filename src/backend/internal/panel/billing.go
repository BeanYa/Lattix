package panel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"lattix/backend/internal/panel/scheduler"
	"lattix/backend/internal/store"
)

var supportedCurrencies = map[string]bool{
	"AUD": true, "BRL": true, "CAD": true, "CHF": true, "CNY": true, "CZK": true,
	"DKK": true, "EUR": true, "GBP": true, "HKD": true, "HUF": true, "IDR": true,
	"ILS": true, "INR": true, "ISK": true, "JPY": true, "KRW": true, "MXN": true,
	"MYR": true, "NOK": true, "NZD": true, "PHP": true, "PLN": true, "RON": true,
	"SEK": true, "SGD": true, "THB": true, "TRY": true, "USD": true, "ZAR": true,
}

type billingInput struct {
	Enabled          bool   `json:"enabled"`
	ProviderID       int64  `json:"provider_id"`
	AmountMinor      int64  `json:"amount_minor"`
	Currency         string `json:"currency"`
	ServiceStartedOn string `json:"service_started_on"`
	IntervalCount    int    `json:"interval_count"`
	IntervalUnit     string `json:"interval_unit"`
	NextRenewalOn    string `json:"next_renewal_on"`
}

type trafficPlanInput struct {
	QuotaBytes     *int64 `json:"quota_bytes"`
	AccountingMode string `json:"accounting_mode"`
	ResetAnchorOn  string `json:"reset_anchor_on"`
	ResetCount     int    `json:"reset_count"`
	ResetUnit      string `json:"reset_unit"`
}

type billingDTO struct {
	Enabled             bool            `json:"enabled"`
	Provider            *store.Provider `json:"provider"`
	AmountMinor         int64           `json:"amount_minor"`
	Currency            string          `json:"currency"`
	ServiceStartedOn    string          `json:"service_started_on"`
	IntervalCount       int             `json:"interval_count"`
	IntervalUnit        string          `json:"interval_unit"`
	NextRenewalOn       string          `json:"next_renewal_on"`
	Status              string          `json:"status"`
	AssumedValidThrough string          `json:"assumed_valid_through"`
	StatusChangedAt     string          `json:"status_changed_at"`
	PublicConverted     *convertedCost  `json:"public_converted,omitempty"`
	CustomConverted     *convertedCost  `json:"custom_converted,omitempty"`
}

type convertedCost struct {
	AmountMinor    int64  `json:"amount_minor"`
	Currency       string `json:"currency"`
	RateDate       string `json:"rate_date"`
	Source         string `json:"source"`
	AnchorCurrency string `json:"anchor_currency,omitempty"`
}

type trafficPlanDTO struct {
	QuotaBytes      *int64 `json:"quota_bytes"`
	AccountingMode  string `json:"accounting_mode"`
	ResetAnchorOn   string `json:"reset_anchor_on"`
	ResetCount      int    `json:"reset_count"`
	ResetUnit       string `json:"reset_unit"`
	PeriodStartedOn string `json:"period_started_on"`
	NextResetOn     string `json:"next_reset_on"`
	TXBytes         int64  `json:"tx_bytes"`
	RXBytes         int64  `json:"rx_bytes"`
	UsedBytes       int64  `json:"used_bytes"`
	Complete        bool   `json:"complete"`
}

func defaultTrafficPlan(serverID int64, date string) store.ServerTrafficPlan {
	return store.ServerTrafficPlan{ServerID: serverID, AccountingMode: "outbound", ResetAnchorOn: date,
		ResetCount: 1, ResetUnit: "month", TrackingStartedOn: date}
}

func validateBillingInput(ctx context.Context, st *store.Store, in billingInput, today string) (store.ServerBilling, error) {
	b := store.ServerBilling{Enabled: in.Enabled, ProviderID: in.ProviderID, AmountMinor: in.AmountMinor,
		Currency: strings.ToUpper(strings.TrimSpace(in.Currency)), ServiceStartedOn: in.ServiceStartedOn,
		IntervalCount: in.IntervalCount, IntervalUnit: in.IntervalUnit, NextRenewalOn: in.NextRenewalOn}
	if !in.Enabled {
		b.Status = store.BillingDisabled
		return b, nil
	}
	if in.ProviderID <= 0 {
		return b, errors.New("请选择服务商")
	}
	if _, err := st.ProviderByID(ctx, in.ProviderID); err != nil {
		return b, errors.New("服务商不存在")
	}
	if in.AmountMinor < 0 {
		return b, errors.New("费用不能为负数")
	}
	if !supportedCurrencies[b.Currency] {
		return b, errors.New("不支持的币种")
	}
	if err := store.ValidateInterval(in.IntervalCount, in.IntervalUnit); err != nil {
		return b, err
	}
	started, err := store.ParseDate(in.ServiceStartedOn)
	if err != nil {
		return b, errors.New("开通日期格式无效")
	}
	todayDate, _ := store.ParseDate(today)
	if started.After(todayDate) {
		return b, errors.New("开通日期不能晚于今天")
	}
	renewal, err := store.ParseDate(in.NextRenewalOn)
	if err != nil {
		return b, errors.New("下次续费日不能为空")
	}
	if !renewal.After(started) {
		return b, errors.New("下次续费日必须晚于开通日期")
	}
	b.Status = billingStatus(true, in.NextRenewalOn, today, false)
	return b, nil
}

func validateTrafficInput(in trafficPlanInput) error {
	if in.QuotaBytes != nil && *in.QuotaBytes <= 0 {
		return errors.New("流量额度必须大于 0")
	}
	switch in.AccountingMode {
	case "outbound", "bidirectional", "max":
	default:
		return errors.New("无效的流量计费方式")
	}
	if _, err := store.ParseDate(in.ResetAnchorOn); err != nil {
		return errors.New("流量重置锚点格式无效")
	}
	return store.ValidateInterval(in.ResetCount, in.ResetUnit)
}

// billingManualTransitions 定义计费状态机的手动转换边（billing-scheduler-design §生命周期状态机）：
// 续费确认允许 due_today/assumed_valid/expired → active，以及 active → active（仅改期）。
// 巡检路径（inspectBilling）为派生计算，天然满足状态图，不走本表。
var billingManualTransitions = map[string]map[string]bool{
	store.BillingActive: {
		store.BillingActive: true, // 更新续费日
	},
	store.BillingDueToday: {
		store.BillingActive: true,
	},
	store.BillingAssumedValid: {
		store.BillingActive: true,
	},
	store.BillingExpired: {
		store.BillingActive: true,
	},
}

// validBillingTransition 校验计费状态的手动转换是否合法（from == to 幂等允许）。
func validBillingTransition(from, to string) bool {
	if from == to {
		return true
	}
	targets, ok := billingManualTransitions[from]
	if !ok {
		return false
	}
	return targets[to]
}

func billingStatus(enabled bool, renewal, today string, online bool) string {
	if !enabled {
		return store.BillingDisabled
	}
	if renewal > today {
		return store.BillingActive
	}
	if renewal == today {
		return store.BillingDueToday
	}
	if online {
		return store.BillingAssumedValid
	}
	return store.BillingExpired
}

func (s *Server) inspectBilling(ctx context.Context) error {
	today := time.Now().In(s.inspectionLocation(ctx)).Format("2006-01-02")
	items, err := s.st.InspectableBilling(ctx)
	if err != nil {
		return err
	}
	for _, item := range items {
		if item.LastInspectedOn == today {
			continue
		}
		item.Status = billingStatus(true, item.NextRenewalOn, today, s.req.IsOnline(item.ServerID))
		if item.Status == store.BillingAssumedValid {
			item.AssumedValidThrough = today
		}
		item.LastInspectedOn = today
		if err := s.st.UpsertServerBilling(ctx, item); err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) billingInspectionSchedule(ctx context.Context) scheduler.InspectionSchedule {
	def := scheduler.InspectionSchedule{Every: 1, Unit: "day", At: "00:05"}
	raw := s.getSetting(ctx, store.SettingBillingInspection)
	if raw == "" {
		return def
	}
	var value scheduler.InspectionSchedule
	if json.Unmarshal([]byte(raw), &value) != nil || value.Unit != "day" || value.Validate() != nil {
		return def
	}
	return value
}

func (s *Server) handleListProviders(w http.ResponseWriter, r *http.Request) {
	items, err := s.st.ListProviders(r.Context())
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	if items == nil {
		items = []store.Provider{}
	}
	writeJSON(w, http.StatusOK, items)
}

func validateProvider(name, website string) (string, string, error) {
	name, website = strings.TrimSpace(name), strings.TrimSpace(website)
	if name == "" || len([]rune(name)) > 100 {
		return "", "", errors.New("服务商名称须为 1-100 个字符")
	}
	if website != "" {
		u, err := url.Parse(website)
		if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
			return "", "", errors.New("官网地址须为完整的 http(s) URL")
		}
	}
	return name, website, nil
}

func (s *Server) handleCreateProvider(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name       string `json:"name"`
		WebsiteURL string `json:"website_url"`
	}
	if readJSON(r, &req) != nil {
		writeProtocolError(w, 400, "invalid request body")
		return
	}
	name, website, err := validateProvider(req.Name, req.WebsiteURL)
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}
	p, err := s.st.CreateProvider(r.Context(), name, website)
	if err != nil {
		writeError(w, 400, "服务商名称已存在")
		return
	}
	writeJSON(w, http.StatusCreated, p)
}

func (s *Server) handleUpdateProvider(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID         int64  `json:"id"`
		Name       string `json:"name"`
		WebsiteURL string `json:"website_url"`
	}
	if readJSON(r, &req) != nil {
		writeProtocolError(w, 400, "invalid request body")
		return
	}
	name, website, err := validateProvider(req.Name, req.WebsiteURL)
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}
	if err := s.st.UpdateProvider(r.Context(), req.ID, name, website); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	p, _ := s.st.ProviderByID(r.Context(), req.ID)
	writeJSON(w, http.StatusOK, p)
}

func (s *Server) handleDeleteProvider(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID int64 `json:"id"`
	}
	if readJSON(r, &req) != nil {
		writeProtocolError(w, 400, "invalid request body")
		return
	}
	if err := s.st.DeleteProvider(r.Context(), req.ID); err != nil {
		writeError(w, 400, "服务商正在使用，无法删除")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"deleted": true})
}

func (s *Server) handleConfirmRenewal(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ServerID      int64  `json:"server_id"`
		NextRenewalOn string `json:"next_renewal_on"`
	}
	if readJSON(r, &req) != nil {
		writeProtocolError(w, 400, "invalid request body")
		return
	}
	items, err := s.st.ServerBillingMap(r.Context())
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	b, ok := items[req.ServerID]
	if !ok || !b.Enabled {
		writeError(w, 400, "服务器未开启统计计费")
		return
	}
	if !validBillingTransition(b.Status, store.BillingActive) {
		writeError(w, http.StatusConflict, fmt.Sprintf("当前计费状态 %s 不允许直接确认续费", b.Status))
		return
	}
	today := time.Now().In(s.inspectionLocation(r.Context())).Format("2006-01-02")
	if req.NextRenewalOn <= today {
		writeError(w, 400, "下次续费日必须晚于今天")
		return
	}
	if _, err := store.ParseDate(req.NextRenewalOn); err != nil {
		writeError(w, 400, "日期格式无效")
		return
	}
	b.NextRenewalOn, b.Status, b.AssumedValidThrough, b.LastInspectedOn = req.NextRenewalOn, store.BillingActive, "", today
	if err := s.st.UpsertServerBilling(r.Context(), b); err != nil {
		writeError(w, 500, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": b.Status, "next_renewal_on": b.NextRenewalOn})
}

func intervalDate(anchor time.Time, count int, unit string, step int) time.Time {
	if unit == "day" {
		return anchor.AddDate(0, 0, count*step)
	}
	year, month := anchor.Year(), int(anchor.Month())
	if unit == "year" {
		year += count * step
	} else {
		total := year*12 + month - 1 + count*step
		year, month = total/12, total%12+1
	}
	last := time.Date(year, time.Month(month)+1, 0, 0, 0, 0, 0, anchor.Location()).Day()
	day := anchor.Day()
	if day > last {
		day = last
	}
	return time.Date(year, time.Month(month), day, 0, 0, 0, 0, anchor.Location())
}

func trafficPeriod(anchor, today time.Time, count int, unit string) (time.Time, time.Time) {
	step := 0
	for intervalDate(anchor, count, unit, step+1).Compare(today) <= 0 {
		step++
	}
	return intervalDate(anchor, count, unit, step), intervalDate(anchor, count, unit, step+1)
}

func trafficUsed(mode string, tx, rx int64) int64 {
	switch mode {
	case "bidirectional":
		return tx + rx
	case "max":
		if rx > tx {
			return rx
		}
	}
	return tx
}

func (s *Server) enrichServerBilling(ctx context.Context, dto *serverDTO, billing map[int64]store.ServerBilling, plans map[int64]store.ServerTrafficPlan, providers map[int64]store.Provider) error {
	if b, ok := billing[dto.ID]; ok {
		value := billingDTO{Enabled: b.Enabled, AmountMinor: b.AmountMinor, Currency: b.Currency, ServiceStartedOn: b.ServiceStartedOn,
			IntervalCount: b.IntervalCount, IntervalUnit: b.IntervalUnit, NextRenewalOn: b.NextRenewalOn, Status: b.Status,
			AssumedValidThrough: b.AssumedValidThrough, StatusChangedAt: b.StatusChangedAt}
		if p, found := providers[b.ProviderID]; found {
			copy := p
			value.Provider = &copy
		}
		if b.Enabled {
			value.PublicConverted, value.CustomConverted, _ = s.convertCosts(ctx, b.AmountMinor, b.Currency)
		}
		dto.Billing = &value
	} else {
		dto.Billing = &billingDTO{Enabled: false, Currency: "CNY", IntervalCount: 1, IntervalUnit: "month", Status: store.BillingDisabled}
	}
	p, ok := plans[dto.ID]
	if !ok {
		return nil
	}
	loc := s.inspectionLocation(ctx)
	today := time.Now().In(loc)
	anchor, err := time.ParseInLocation("2006-01-02", p.ResetAnchorOn, loc)
	if err != nil {
		return err
	}
	start, next := trafficPeriod(anchor, today, p.ResetCount, p.ResetUnit)
	tx, rx, err := s.st.ServerNetworkUsage(ctx, dto.ID, start.Format("2006-01-02"), today.Format("2006-01-02"))
	if err != nil {
		return err
	}
	dto.TrafficPlan = trafficPlanDTO{QuotaBytes: p.QuotaBytes, AccountingMode: p.AccountingMode, ResetAnchorOn: p.ResetAnchorOn,
		ResetCount: p.ResetCount, ResetUnit: p.ResetUnit, PeriodStartedOn: start.Format("2006-01-02"), NextResetOn: next.Format("2006-01-02"),
		TXBytes: tx, RXBytes: rx, UsedBytes: trafficUsed(p.AccountingMode, tx, rx), Complete: p.TrackingStartedOn <= start.Format("2006-01-02")}
	return nil
}

func providerMap(items []store.Provider) map[int64]store.Provider {
	out := map[int64]store.Provider{}
	for _, p := range items {
		out[p.ID] = p
	}
	return out
}

func (s *Server) saveServerPlans(ctx context.Context, serverID int64, billing *billingInput, traffic *trafficPlanInput) error {
	today := time.Now().In(s.inspectionLocation(ctx)).Format("2006-01-02")
	if billing != nil {
		b, err := validateBillingInput(ctx, s.st, *billing, today)
		if err != nil {
			return err
		}
		b.ServerID = serverID
		b.Status = billingStatus(b.Enabled, b.NextRenewalOn, today, s.req.IsOnline(serverID))
		if err := s.st.UpsertServerBilling(ctx, b); err != nil {
			return err
		}
	}
	if traffic != nil {
		if err := validateTrafficInput(*traffic); err != nil {
			return err
		}
		plans, _ := s.st.ServerTrafficPlanMap(ctx)
		tracking := today
		if old, ok := plans[serverID]; ok {
			tracking = old.TrackingStartedOn
		}
		return s.st.UpsertServerTrafficPlan(ctx, store.ServerTrafficPlan{ServerID: serverID, QuotaBytes: traffic.QuotaBytes,
			AccountingMode: traffic.AccountingMode, ResetAnchorOn: traffic.ResetAnchorOn, ResetCount: traffic.ResetCount,
			ResetUnit: traffic.ResetUnit, TrackingStartedOn: tracking})
	}
	return nil
}

func (s *Server) billingDefaultDate(ctx context.Context) string {
	return time.Now().In(s.inspectionLocation(ctx)).Format("2006-01-02")
}

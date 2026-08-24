package panel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"os"
	"strings"
	"time"

	"lattix/backend/internal/panel/scheduler"
	"lattix/backend/internal/store"
	external "lattix/shared/requester"
)

type exchangeCatalog struct {
	s       *Server
	api     external.ExternalJSONRequester
	apiBase string
}

func newExchangeCatalog(s *Server) *exchangeCatalog {
	base := strings.TrimRight(strings.TrimSpace(os.Getenv("LATX_FRANKFURTER_API_BASE")), "/")
	if base == "" {
		base = "https://api.frankfurter.app"
	}
	return &exchangeCatalog{s: s, api: external.ExternalJSONRequester{Doer: &http.Client{Timeout: 30 * time.Second}}, apiBase: base}
}

// pivotBases 是需要拉取的基准货币列表；EUR 始终排在首位，convertCosts 使用 EUR 行做交叉汇率。
var pivotBases = []string{"EUR", "USD", "CNY", "JPY", "CAD"}

func (c *exchangeCatalog) refresh(ctx context.Context) error {
	var all []store.ExchangeRate
	for _, base := range pivotBases {
		rates, err := c.fetchBase(ctx, base)
		if err != nil {
			return err
		}
		all = append(all, rates...)
	}
	if len(all) == 0 {
		return errors.New("Frankfurter 未返回支持的汇率")
	}
	return c.s.st.ReplaceExchangeRates(ctx, all)
}

func (c *exchangeCatalog) fetchBase(ctx context.Context, base string) ([]store.ExchangeRate, error) {
	var payload struct {
		Base  string                 `json:"base"`
		Date  string                 `json:"date"`
		Rates map[string]json.Number `json:"rates"`
	}
	if err := c.api.GetJSON(ctx, c.apiBase+"/latest?from="+base, &payload); err != nil {
		return nil, fmt.Errorf("拉取 Frankfurter 汇率失败 (%s): %w", base, err)
	}
	payload.Rates[base] = json.Number("1")
	now := time.Now().UTC().Format(time.RFC3339)
	rates := make([]store.ExchangeRate, 0, len(payload.Rates))
	for currency, rate := range payload.Rates {
		if supportedCurrencies[currency] {
			rates = append(rates, store.ExchangeRate{BaseCurrency: base, QuoteCurrency: currency, Rate: rate.String(), RateDate: payload.Date, Source: "frankfurter", FetchedAt: now})
		}
	}
	return rates, nil
}

func (s *Server) exchangeInspectionSchedule(ctx context.Context) scheduler.InspectionSchedule {
	def := scheduler.InspectionSchedule{Every: 1, Unit: "day", At: "02:30"}
	raw := s.getSetting(ctx, store.SettingExchangeInspection)
	if raw == "" {
		return def
	}
	var value scheduler.InspectionSchedule
	if json.Unmarshal([]byte(raw), &value) != nil || value.Unit != "day" || value.Validate() != nil {
		return def
	}
	return value
}

func currencyDigits(currency string) int {
	if currency == "JPY" || currency == "KRW" || currency == "ISK" {
		return 0
	}
	return 2
}

func pow10(n int) *big.Rat {
	value := int64(1)
	for i := 0; i < n; i++ {
		value *= 10
	}
	return new(big.Rat).SetInt64(value)
}

func roundRat(value *big.Rat) int64 {
	q, r := new(big.Int), new(big.Int)
	q.QuoRem(value.Num(), value.Denom(), r)
	if new(big.Int).Lsh(new(big.Int).Abs(r), 1).Cmp(new(big.Int).Abs(value.Denom())) >= 0 {
		if value.Sign() >= 0 {
			q.Add(q, big.NewInt(1))
		} else {
			q.Sub(q, big.NewInt(1))
		}
	}
	return q.Int64()
}

func publicRate(rates map[string]*big.Rat, source, target string) (*big.Rat, bool) {
	s, sok := rates[source]
	t, tok := rates[target]
	if !sok || !tok || s.Sign() == 0 {
		return nil, false
	}
	return new(big.Rat).Quo(t, s), true
}

func convertedAmount(amountMinor int64, source, target string, rate *big.Rat, rateDate, rateSource string) *convertedCost {
	major := new(big.Rat).Quo(new(big.Rat).SetInt64(amountMinor), pow10(currencyDigits(source)))
	targetMinor := new(big.Rat).Mul(new(big.Rat).Mul(major, rate), pow10(currencyDigits(target)))
	return &convertedCost{AmountMinor: roundRat(targetMinor), Currency: target, RateDate: rateDate, Source: rateSource}
}

func isPivotBase(currency string) bool {
	for _, b := range pivotBases {
		if b == currency {
			return true
		}
	}
	return false
}

func (s *Server) convertCosts(ctx context.Context, amountMinor int64, source string) (public, custom *convertedCost, err error) {
	target := strings.ToUpper(s.getSetting(ctx, store.SettingReportingCurrency))
	if target == "" {
		target = "CNY"
	}
	rows, err := s.st.ExchangeRates(ctx)
	if err != nil {
		return nil, nil, err
	}
	// 统计币种命中公开汇率基准时直接使用该基准的汇率表；否则回退到 EUR 交叉汇率。
	pivot := "EUR"
	if isPivotBase(target) {
		pivot = target
	}
	rates := map[string]*big.Rat{}
	rateDate := ""
	for _, row := range rows {
		if row.BaseCurrency != pivot {
			continue
		}
		value, ok := new(big.Rat).SetString(row.Rate)
		if ok {
			rates[row.QuoteCurrency] = value
			rateDate = row.RateDate
		}
	}
	if source == target {
		public = &convertedCost{AmountMinor: amountMinor, Currency: target, Source: "identity"}
	} else if pivot == target {
		// 基准即目标：1 target = rates[source] source → source→target 汇率 = 1/rates[source]
		if r, ok := rates[source]; ok && r.Sign() > 0 {
			public = convertedAmount(amountMinor, source, target, new(big.Rat).Inv(r), rateDate, "frankfurter")
		} else {
			return nil, nil, errors.New("缺少可用公共汇率")
		}
	} else if rate, ok := publicRate(rates, source, target); ok {
		public = convertedAmount(amountMinor, source, target, rate, rateDate, "frankfurter")
	} else {
		return nil, nil, errors.New("缺少可用公共汇率")
	}

	customRates, err := s.st.ListCustomExchangeRates(ctx)
	if err != nil {
		return public, nil, err
	}
	for _, item := range customRates {
		if !item.Enabled || item.TargetCurrency != target {
			continue
		}
		sa, sok := new(big.Rat).SetString(item.SourceAmount)
		ta, tok := new(big.Rat).SetString(item.TargetAmount)
		if !sok || !tok || sa.Sign() <= 0 || ta.Sign() <= 0 {
			continue
		}
		customRate := new(big.Rat).Quo(ta, sa)
		if source == target {
			custom = &convertedCost{AmountMinor: amountMinor, Currency: target, RateDate: rateDate, Source: "custom_anchor"}
		} else if source == item.SourceCurrency {
			custom = convertedAmount(amountMinor, source, target, customRate, rateDate, "custom_anchor")
		} else if bridge, found := publicRate(rates, source, item.SourceCurrency); found {
			custom = convertedAmount(amountMinor, source, target, new(big.Rat).Mul(bridge, customRate), rateDate, "custom_anchor")
		}
		if custom != nil {
			custom.AnchorCurrency = item.SourceCurrency
		}
		break
	}
	return public, custom, nil
}

func (s *Server) handleExchangeRates(w http.ResponseWriter, r *http.Request) {
	rates, err := s.st.ExchangeRates(r.Context())
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	custom, err := s.st.ListCustomExchangeRates(r.Context())
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	currency := s.getSetting(r.Context(), store.SettingReportingCurrency)
	if currency == "" {
		currency = "CNY"
	}
	writeJSON(w, http.StatusOK, map[string]any{"reporting_currency": currency, "rates": rates, "custom_rates": custom})
}

func (s *Server) handleRefreshExchangeRates(w http.ResponseWriter, r *http.Request) {
	if err := s.exchange.refresh(r.Context()); err != nil {
		writeError(w, 502, err.Error())
		return
	}
	s.handleExchangeRates(w, r)
}

func (s *Server) handleSaveCustomExchangeRate(w http.ResponseWriter, r *http.Request) {
	var req store.CustomExchangeRate
	if readJSON(r, &req) != nil {
		writeProtocolError(w, 400, "invalid request body")
		return
	}
	req.SourceCurrency, req.TargetCurrency = strings.ToUpper(req.SourceCurrency), strings.ToUpper(req.TargetCurrency)
	target := strings.ToUpper(s.getSetting(r.Context(), store.SettingReportingCurrency))
	if target == "" {
		target = "CNY"
	}
	if req.ID == 0 {
		req.TargetCurrency = target
	}
	if req.SourceCurrency == req.TargetCurrency || !supportedCurrencies[req.SourceCurrency] || !supportedCurrencies[req.TargetCurrency] {
		writeError(w, 400, "源币种与目标币种必须不同且受支持")
		return
	}
	sa, sok := new(big.Rat).SetString(req.SourceAmount)
	ta, tok := new(big.Rat).SetString(req.TargetAmount)
	if !sok || !tok || sa.Sign() <= 0 || ta.Sign() <= 0 {
		writeError(w, 400, "自定义汇率金额必须大于 0")
		return
	}
	one := big.NewRat(1, 1)
	if sa.Cmp(one) != 0 && ta.Cmp(one) != 0 {
		writeError(w, 400, "自定义汇率须将源币种或展示币种的一侧金额设为 1")
		return
	}
	id, err := s.st.UpsertCustomExchangeRate(r.Context(), req)
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}
	req.ID = id
	writeJSON(w, http.StatusOK, req)
}

func (s *Server) handleDeleteCustomExchangeRate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID int64 `json:"id"`
	}
	if readJSON(r, &req) != nil {
		writeProtocolError(w, 400, "invalid request body")
		return
	}
	if err := s.st.DeleteCustomExchangeRate(r.Context(), req.ID); err != nil {
		writeError(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]bool{"deleted": true})
}

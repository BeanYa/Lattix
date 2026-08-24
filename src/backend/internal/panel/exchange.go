package panel

import (
	"context"
	"encoding/json"
	"math/big"
	"net/http"
	"strings"

	"lattix/backend/internal/panel/exchange"
	"lattix/backend/internal/panel/scheduler"
	"lattix/backend/internal/store"
)

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

func (s *Server) convertCosts(ctx context.Context, amountMinor int64, source string) (public, custom *convertedCost, err error) {
	target := strings.ToUpper(s.getSetting(ctx, store.SettingReportingCurrency))
	if target == "" {
		target = "CNY"
	}
	rows, err := s.st.ExchangeRates(ctx)
	if err != nil {
		return nil, nil, err
	}
	return exchange.Convert(amountMinor, source, target, rows, func() ([]store.CustomExchangeRate, error) {
		return s.st.ListCustomExchangeRates(ctx)
	})
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
	if err := s.exchange.Refresh(r.Context()); err != nil {
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
	if req.SourceCurrency == req.TargetCurrency || !exchange.SupportedCurrencies[req.SourceCurrency] || !exchange.SupportedCurrencies[req.TargetCurrency] {
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

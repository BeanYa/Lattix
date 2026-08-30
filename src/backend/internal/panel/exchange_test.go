package panel

import (
	"context"
	"math/big"
	"testing"

	"lattix/backend/internal/panel/exchange"
	"lattix/backend/internal/store"
)

func newExchangeTestServer(t *testing.T, reportingCurrency string, rates []store.ExchangeRate) *Server {
	t.Helper()
	st, err := store.Open(t.TempDir() + "/panel.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	ctx := context.Background()
	if err := st.SetSetting(ctx, store.SettingReportingCurrency, reportingCurrency); err != nil {
		t.Fatal(err)
	}
	if err := st.ReplaceExchangeRates(ctx, rates); err != nil {
		t.Fatal(err)
	}
	return &Server{st: st}
}

// multiBaseRates 从 EUR 基准汇率表生成所有 exchange.PivotBases 的一致汇率数据。
func multiBaseRates(eurValues map[string]string) []store.ExchangeRate {
	var out []store.ExchangeRate
	eurRats := map[string]*big.Rat{}
	for c, v := range eurValues {
		r, _ := new(big.Rat).SetString(v)
		eurRats[c] = r
		out = append(out, store.ExchangeRate{BaseCurrency: "EUR", QuoteCurrency: c, Rate: v, RateDate: "2026-07-29", Source: "frankfurter", FetchedAt: "2026-07-29T02:30:00Z"})
	}
	for _, base := range exchange.PivotBases[1:] {
		baseR, ok := eurRats[base]
		if !ok || baseR.Sign() == 0 {
			continue
		}
		for c, r := range eurRats {
			quote := new(big.Rat).Quo(r, baseR) // 1 base = (eurRates[c]/eurRates[base]) c
			out = append(out, store.ExchangeRate{BaseCurrency: base, QuoteCurrency: c, Rate: quote.RatString(), RateDate: "2026-07-29", Source: "frankfurter", FetchedAt: "2026-07-29T02:30:00Z"})
		}
	}
	return out
}

func TestConvertCostUsesDirectBaseRate(t *testing.T) {
	// 统计币种 CNY 命中公开汇率基准，直接使用 CNY 基准表。
	rates := multiBaseRates(map[string]string{"EUR": "1", "USD": "1.2", "CNY": "7.2"})
	s := newExchangeTestServer(t, "CNY", rates)
	got, custom, err := s.convertCosts(context.Background(), 1000, "USD")
	if err != nil {
		t.Fatal(err)
	}
	if custom != nil {
		t.Fatalf("custom = %+v, want nil", custom)
	}
	// CNY 基准: 1 CNY = 1.2/7.2 USD = 1/6 USD → 1 USD = 6 CNY → 10 USD = 60 CNY = 6000 minor
	if got.AmountMinor != 6000 || got.Currency != "CNY" || got.Source != "frankfurter" {
		t.Fatalf("converted = %+v, want 6000 CNY from frankfurter", got)
	}
}

func TestConvertCostFallsBackToEURCrossRate(t *testing.T) {
	// 统计币种 GBP 不是 pivot base，回退到 EUR 交叉汇率。
	rates := multiBaseRates(map[string]string{"EUR": "1", "USD": "1.2", "GBP": "0.8", "CNY": "7.2"})
	s := newExchangeTestServer(t, "GBP", rates)
	got, _, err := s.convertCosts(context.Background(), 1000, "USD")
	if err != nil {
		t.Fatal(err)
	}
	// EUR 交叉: GBP/USD = 0.8/1.2 = 2/3 → 10 USD = 6.67 GBP = 667 minor
	if got.AmountMinor != 667 || got.Currency != "GBP" || got.Source != "frankfurter" {
		t.Fatalf("converted = %+v, want 667 GBP from EUR cross-rate", got)
	}
}

func TestConvertCostUsesCustomAnchorBridge(t *testing.T) {
	rates := multiBaseRates(map[string]string{"EUR": "1", "USD": "1.2", "CAD": "1.5", "CNY": "7.2"})
	s := newExchangeTestServer(t, "CNY", rates)
	_, err := s.st.UpsertCustomExchangeRate(context.Background(), store.CustomExchangeRate{
		SourceCurrency: "USD", SourceAmount: "1", TargetCurrency: "CNY", TargetAmount: "7.2", Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, got, err := s.convertCosts(context.Background(), 1000, "CAD")
	if err != nil {
		t.Fatal(err)
	}
	// bridge(CAD→USD) 从 CNY 基准: (1.2/7.2)/(1.5/7.2) = 0.8; custom = 7.2
	// rate = 0.8*7.2 = 5.76; 10 CAD → 57.6 CNY = 5760 minor
	if got.AmountMinor != 5760 || got.Source != "custom_anchor" {
		t.Fatalf("converted = %+v, want 5760 CNY from custom anchor", got)
	}
}

func TestConvertCostRoundsZeroDecimalCurrency(t *testing.T) {
	// JPY 是 pivot base，直接使用 JPY 基准表。
	rates := multiBaseRates(map[string]string{"EUR": "1", "USD": "1.2", "JPY": "180"})
	s := newExchangeTestServer(t, "JPY", rates)
	got, _, err := s.convertCosts(context.Background(), 123, "USD")
	if err != nil {
		t.Fatal(err)
	}
	// JPY 基准: 1 JPY = 1.2/180 USD = 1/150 USD → 1 USD = 150 JPY → 1.23 USD → 184.5 → 185 (四舍五入)
	if got.AmountMinor != 185 || got.Currency != "JPY" {
		t.Fatalf("converted = %+v, want 185 JPY", got)
	}
}

func TestCustomRateFollowsReportingCurrency(t *testing.T) {
	ctx := context.Background()
	rates := multiBaseRates(map[string]string{"EUR": "1", "USD": "1.2", "CNY": "7.2"})
	s := newExchangeTestServer(t, "CNY", rates)
	if _, err := s.st.UpsertCustomExchangeRate(ctx, store.CustomExchangeRate{
		SourceCurrency: "USD", SourceAmount: "1", TargetCurrency: "CNY", TargetAmount: "7", Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	// 切换统计币种到 EUR：自定义汇率目标为 CNY，不匹配 → custom = nil
	if err := s.st.SetSetting(ctx, store.SettingReportingCurrency, "EUR"); err != nil {
		t.Fatal(err)
	}
	_, custom, err := s.convertCosts(ctx, 1000, "USD")
	if err != nil {
		t.Fatal(err)
	}
	if custom != nil {
		t.Fatalf("custom = %+v, want nil for unmatched target", custom)
	}
	// 切回 CNY：自定义汇率目标匹配 → custom 生效
	if err := s.st.SetSetting(ctx, store.SettingReportingCurrency, "CNY"); err != nil {
		t.Fatal(err)
	}
	_, custom, err = s.convertCosts(ctx, 1000, "USD")
	if err != nil {
		t.Fatal(err)
	}
	if custom == nil || custom.AmountMinor != 7000 {
		t.Fatalf("custom = %+v, want 7000 CNY after target matches again", custom)
	}
}

func TestMixedCurrencyTotalsUseSelectedCustomPivot(t *testing.T) {
	ctx := context.Background()
	rates := multiBaseRates(map[string]string{
		"EUR": "1", "USD": "1.2", "CAD": "1.5", "JPY": "180", "CNY": "7.2",
	})
	s := newExchangeTestServer(t, "CNY", rates)
	costs := []struct {
		amount   int64
		currency string
	}{{1000, "USD"}, {500, "CAD"}, {300, "EUR"}, {100, "JPY"}, {10000, "CNY"}}

	sum := func() (publicTotal, customTotal int64) {
		t.Helper()
		for _, cost := range costs {
			public, custom, err := s.convertCosts(ctx, cost.amount, cost.currency)
			if err != nil {
				t.Fatal(err)
			}
			publicTotal += public.AmountMinor
			if custom == nil {
				t.Fatalf("missing custom result for %s", cost.currency)
			}
			customTotal += custom.AmountMinor
		}
		return
	}

	if _, err := s.st.UpsertCustomExchangeRate(ctx, store.CustomExchangeRate{
		SourceCurrency: "USD", SourceAmount: "1", TargetCurrency: "CNY", TargetAmount: "7", Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	publicTotal, customTotal := sum()
	if publicTotal != 20960 || customTotal != 22787 {
		t.Fatalf("USD pivot totals = public %d custom %d, want 20960 and 22787", publicTotal, customTotal)
	}

	if _, err := s.st.UpsertCustomExchangeRate(ctx, store.CustomExchangeRate{
		SourceCurrency: "EUR", SourceAmount: "1", TargetCurrency: "CNY", TargetAmount: "10", Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	publicTotal, customTotal = sum()
	if publicTotal != 20960 || customTotal != 25222 {
		t.Fatalf("EUR pivot totals = public %d custom %d, want 20960 and 25222", publicTotal, customTotal)
	}
}

func TestCustomRatesAreUniqueBySourceAndSingleActivePerTarget(t *testing.T) {
	ctx := context.Background()
	s := newExchangeTestServer(t, "CNY", nil)
	if _, err := s.st.UpsertCustomExchangeRate(ctx, store.CustomExchangeRate{
		SourceCurrency: "USD", SourceAmount: "1", TargetCurrency: "CNY", TargetAmount: "7", Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.st.UpsertCustomExchangeRate(ctx, store.CustomExchangeRate{
		SourceCurrency: "USD", SourceAmount: "1", TargetCurrency: "EUR", TargetAmount: "0.9", Enabled: true,
	}); err == nil {
		t.Fatal("duplicate source currency was accepted")
	}
	if _, err := s.st.UpsertCustomExchangeRate(ctx, store.CustomExchangeRate{
		SourceCurrency: "EUR", SourceAmount: "1", TargetCurrency: "CNY", TargetAmount: "10", Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	rates, err := s.st.ListCustomExchangeRates(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(rates) != 2 || rates[0].Enabled || !rates[1].Enabled {
		t.Fatalf("rates = %+v, want only the later CNY anchor enabled", rates)
	}
}

func TestConvertCostPrefersTargetBaseOverEUR(t *testing.T) {
	// 多基准行共存时，统计币种 CNY 命中 pivot → 优先使用 CNY 基准行。
	rates := multiBaseRates(map[string]string{"EUR": "1", "USD": "1.2", "CNY": "7.2"})
	s := newExchangeTestServer(t, "CNY", rates)
	got, _, err := s.convertCosts(context.Background(), 1000, "USD")
	if err != nil {
		t.Fatal(err)
	}
	// CNY 基准直接汇率: 1 CNY = 1/6 USD → 1 USD = 6 CNY → 10 USD = 60 CNY = 6000
	if got.AmountMinor != 6000 || got.Currency != "CNY" || got.Source != "frankfurter" {
		t.Fatalf("converted = %+v, want 6000 CNY (CNY base direct)", got)
	}
}

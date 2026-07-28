package panel

import (
	"context"
	"testing"

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

func testRates(values map[string]string) []store.ExchangeRate {
	rates := make([]store.ExchangeRate, 0, len(values))
	for currency, rate := range values {
		rates = append(rates, store.ExchangeRate{
			BaseCurrency: "EUR", QuoteCurrency: currency, Rate: rate,
			RateDate: "2026-07-29", Source: "frankfurter", FetchedAt: "2026-07-29T02:30:00Z",
		})
	}
	return rates
}

func TestConvertCostUsesPublicCrossRate(t *testing.T) {
	s := newExchangeTestServer(t, "CNY", testRates(map[string]string{"EUR": "1", "USD": "1.2", "CNY": "7.2"}))
	got, custom, err := s.convertCosts(context.Background(), 1000, "USD")
	if err != nil {
		t.Fatal(err)
	}
	if custom != nil {
		t.Fatalf("custom = %+v, want nil", custom)
	}
	if got.AmountMinor != 6000 || got.Currency != "CNY" || got.Source != "frankfurter" {
		t.Fatalf("converted = %+v, want 6000 CNY from frankfurter", got)
	}
}

func TestConvertCostUsesCustomAnchorBridge(t *testing.T) {
	s := newExchangeTestServer(t, "CNY", testRates(map[string]string{"EUR": "1", "USD": "1.2", "CAD": "1.5", "CNY": "7.2"}))
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
	if got.AmountMinor != 5760 || got.Source != "custom_anchor" {
		t.Fatalf("converted = %+v, want 5760 CNY from custom anchor", got)
	}
}

func TestConvertCostRoundsZeroDecimalCurrency(t *testing.T) {
	s := newExchangeTestServer(t, "JPY", testRates(map[string]string{"EUR": "1", "USD": "1.2", "JPY": "180"}))
	got, _, err := s.convertCosts(context.Background(), 123, "USD")
	if err != nil {
		t.Fatal(err)
	}
	if got.AmountMinor != 185 || got.Currency != "JPY" {
		t.Fatalf("converted = %+v, want 185 JPY", got)
	}
}

func TestCustomRateFollowsReportingCurrency(t *testing.T) {
	ctx := context.Background()
	s := newExchangeTestServer(t, "CNY", testRates(map[string]string{"EUR": "1", "USD": "1.2", "CNY": "7.2"}))
	if _, err := s.st.UpsertCustomExchangeRate(ctx, store.CustomExchangeRate{
		SourceCurrency: "USD", SourceAmount: "1", TargetCurrency: "CNY", TargetAmount: "7", Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
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
	s := newExchangeTestServer(t, "CNY", testRates(map[string]string{
		"EUR": "1", "USD": "1.2", "CAD": "1.5", "JPY": "180", "CNY": "7.2",
	}))
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

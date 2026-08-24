// Package exchange 维护 Frankfurter 公开汇率目录（多基准拉取并落库），并提供
// 面向成本统计的币种换算：pivot 基准直取或 EUR 交叉汇率，叠加自定义锚点汇率。
package exchange

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

	"lattix/backend/internal/store"
	external "lattix/shared/requester"
)

// SupportedCurrencies 是面板支持的成本统计币种集合（汇率拉取过滤与币种校验共用）。
var SupportedCurrencies = map[string]bool{
	"AUD": true, "BRL": true, "CAD": true, "CHF": true, "CNY": true, "CZK": true,
	"DKK": true, "EUR": true, "GBP": true, "HKD": true, "HUF": true, "IDR": true,
	"ILS": true, "INR": true, "ISK": true, "JPY": true, "KRW": true, "MXN": true,
	"MYR": true, "NOK": true, "NZD": true, "PHP": true, "PLN": true, "RON": true,
	"SEK": true, "SGD": true, "THB": true, "TRY": true, "USD": true, "ZAR": true,
}

type Catalog struct {
	st      *store.Store
	api     external.ExternalJSONRequester
	apiBase string
}

func New(st *store.Store) *Catalog {
	base := strings.TrimRight(strings.TrimSpace(os.Getenv("LATX_FRANKFURTER_API_BASE")), "/")
	if base == "" {
		base = "https://api.frankfurter.app"
	}
	return &Catalog{st: st, api: external.ExternalJSONRequester{Doer: &http.Client{Timeout: 30 * time.Second}}, apiBase: base}
}

// PivotBases 是需要拉取的基准货币列表；EUR 始终排在首位，Convert 使用 EUR 行做交叉汇率。
var PivotBases = []string{"EUR", "USD", "CNY", "JPY", "CAD"}

func (c *Catalog) Refresh(ctx context.Context) error {
	var all []store.ExchangeRate
	for _, base := range PivotBases {
		rates, err := c.fetchBase(ctx, base)
		if err != nil {
			return err
		}
		all = append(all, rates...)
	}
	if len(all) == 0 {
		return errors.New("Frankfurter 未返回支持的汇率")
	}
	return c.st.ReplaceExchangeRates(ctx, all)
}

func (c *Catalog) fetchBase(ctx context.Context, base string) ([]store.ExchangeRate, error) {
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
		if SupportedCurrencies[currency] {
			rates = append(rates, store.ExchangeRate{BaseCurrency: base, QuoteCurrency: currency, Rate: rate.String(), RateDate: payload.Date, Source: "frankfurter", FetchedAt: now})
		}
	}
	return rates, nil
}

// Converted 是一笔成本换算结果（公开汇率或自定义锚点）。
type Converted struct {
	AmountMinor    int64  `json:"amount_minor"`
	Currency       string `json:"currency"`
	RateDate       string `json:"rate_date"`
	Source         string `json:"source"`
	AnchorCurrency string `json:"anchor_currency,omitempty"`
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

// RoundRat 把 big.Rat 四舍五入到最近整数（半数向远离零方向进位）。
func RoundRat(value *big.Rat) int64 {
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

func convertedAmount(amountMinor int64, source, target string, rate *big.Rat, rateDate, rateSource string) *Converted {
	major := new(big.Rat).Quo(new(big.Rat).SetInt64(amountMinor), pow10(currencyDigits(source)))
	targetMinor := new(big.Rat).Mul(new(big.Rat).Mul(major, rate), pow10(currencyDigits(target)))
	return &Converted{AmountMinor: RoundRat(targetMinor), Currency: target, RateDate: rateDate, Source: rateSource}
}

func isPivotBase(currency string) bool {
	for _, b := range PivotBases {
		if b == currency {
			return true
		}
	}
	return false
}

// Convert 把 amountMinor（source 币种最小单位）换算到统计币种 target：
// public 走公开汇率（统计币种命中公开汇率基准时直接使用该基准的汇率表，
// 否则回退到 EUR 交叉汇率）；custom 走启用的自定义锚点汇率（若命中）。
// fetchCustom 在 public 换算成功后才调用（延迟读取自定义汇率），
// 其读取失败时保留 public 并返回错误。
func Convert(amountMinor int64, source, target string, rows []store.ExchangeRate, fetchCustom func() ([]store.CustomExchangeRate, error)) (public, custom *Converted, err error) {
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
		public = &Converted{AmountMinor: amountMinor, Currency: target, Source: "identity"}
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

	customRates, err := fetchCustom()
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
			custom = &Converted{AmountMinor: amountMinor, Currency: target, RateDate: rateDate, Source: "custom_anchor"}
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

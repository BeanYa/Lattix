package servertest

import (
	"encoding/json"
	"net/netip"
	"strings"
	"testing"
)

func TestParseAbuserScore(t *testing.T) {
	tests := []struct {
		raw      string
		want     float64
		wantRisk string
	}{
		{raw: `0.12`, want: 12, wantRisk: "very_low"},
		{raw: `75`, want: 75, wantRisk: "high"},
		{raw: `"0.92 (Very High)"`, want: 92, wantRisk: "very_high"},
	}
	for _, test := range tests {
		score, risk, ok := parseAbuserScore(json.RawMessage(test.raw))
		if !ok || score != test.want || risk != test.wantRisk {
			t.Fatalf("parseAbuserScore(%s) = %v, %q, %v", test.raw, score, risk, ok)
		}
	}
}

func TestParseDBIP(t *testing.T) {
	body := []byte(`<p>Estimated threat level for this IP address is <span>Medium</span></p>
<code class="language-json">{"countryCode":"DE"}</code>
<tr><th class='text-center'>Crawler</th><td><span class="sr-only">No</span></td></tr>
<tr><th class='text-center'>Proxy</th><td><span class="sr-only">Yes</span></td></tr>`)
	result := parseDBIP(body)
	if result.RiskLevel != "medium" || result.Score == nil || *result.Score != 50 || result.CountryCode != "DE" {
		t.Fatalf("unexpected DB-IP result: %#v", result)
	}
	if result.FactorHits != 1 || result.EffectiveFactors != 2 || !result.Factors["proxy"] {
		t.Fatalf("unexpected DB-IP factors: %#v", result.Factors)
	}
}

func TestRedactProviderError(t *testing.T) {
	address := netip.MustParseAddr("203.0.113.9")
	err := redactProviderError(assertError(`Get "https://api.example/203.0.113.9?key=secret": timeout`), address)
	if strings.Contains(err.Error(), address.String()) || strings.Contains(err.Error(), "secret") {
		t.Fatalf("sensitive provider request data was not redacted: %s", err)
	}
}

type assertError string

func (e assertError) Error() string { return string(e) }

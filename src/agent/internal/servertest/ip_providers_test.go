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

func TestParseDBIPAPI(t *testing.T) {
	body := []byte(`{"ipAddress":"1.1.1.1","continentCode":"OC","countryCode":"AU","countryName":"Australia","stateProv":"New South Wales","city":"Sydney"}`)
	result, err := parseDBIPAPI(body)
	if err != nil {
		t.Fatalf("parseDBIPAPI: %v", err)
	}
	if result.Status != "available" || result.CountryCode != "AU" {
		t.Fatalf("unexpected DB-IP result: %#v", result)
	}
	if result.Score != nil || result.EffectiveFactors != 0 {
		t.Fatalf("DB-IP free tier must not fabricate risk data: %#v", result)
	}
	if _, err := parseDBIPAPI([]byte(`not json`)); err == nil {
		t.Fatal("parseDBIPAPI accepted invalid JSON")
	}
}

func TestIPregistryDemoKeyFromPage(t *testing.T) {
	page := []byte(`<html>var config = { apiKey: "abc123def456" };</html>`)
	if key := ipregistryDemoKeyFromPage(page); key != ipregistryDemoKey {
		t.Fatalf("expected fallback key %q for keyless page, got %q", ipregistryDemoKey, key)
	}
	if key := ipregistryDemoKeyFromPage([]byte(`<html>apiKey="abc123def456"</html>`)); key != "abc123def456" {
		t.Fatalf("expected page key, got %q", key)
	}
	if key := ipregistryDemoKeyFromPage([]byte(`<html>no key here</html>`)); key != ipregistryDemoKey {
		t.Fatalf("expected fallback key %q, got %q", ipregistryDemoKey, key)
	}
	if key := ipregistryDemoKeyFromPage(nil); key != ipregistryDemoKey {
		t.Fatalf("expected fallback key %q for empty page, got %q", ipregistryDemoKey, key)
	}
}

func TestParseMaxMind(t *testing.T) {
	result, err := parseMaxMind([]byte(`{"Country":{"IsoCode":"US"},"City":{"Name":"Los Angeles"}}`))
	if err != nil {
		t.Fatalf("parseMaxMind: %v", err)
	}
	if result.Status != "available" || result.CountryCode != "US" {
		t.Fatalf("unexpected MaxMind result: %#v", result)
	}
}

func TestParseScamalytics(t *testing.T) {
	body := []byte(`{
		"external_datasources": {
			"maxmind_geolite2": {"ip_country_code": "DE"},
			"firehol": {"is_proxy": true},
			"x4bnet": {"is_tor": false, "is_blacklisted_spambot": true, "is_bot_operamini": false, "is_bot_semrush": false}
		},
		"scamalytics": {
			"scamalytics_score": 87,
			"is_blacklisted_external": false,
			"scamalytics_proxy": {"is_vpn": true, "is_datacenter": false}
		}
	}`)
	result, err := parseScamalytics(body)
	if err != nil {
		t.Fatalf("parseScamalytics: %v", err)
	}
	if result.Status != "available" || result.CountryCode != "DE" || result.Score == nil || *result.Score != 87 || result.RiskLevel != "very_high" {
		t.Fatalf("unexpected Scamalytics result: %#v", result)
	}
	if !result.Factors["proxy"] || !result.Factors["vpn"] || result.Factors["tor"] || !result.Factors["robot"] {
		t.Fatalf("unexpected Scamalytics factors: %#v", result.Factors)
	}
}

func TestParseAbuseIPDB(t *testing.T) {
	body := []byte(`{"data":{"usageType":"Data Center/Web Hosting/Transit","abuseConfidenceScore":95}}`)
	result, err := parseAbuseIPDB(body)
	if err != nil {
		t.Fatalf("parseAbuseIPDB: %v", err)
	}
	if result.Status != "available" || result.UsageType != "hosting" || result.Score == nil || *result.Score != 95 || result.RiskLevel != "very_high" {
		t.Fatalf("unexpected AbuseIPDB result: %#v", result)
	}
	if usage := normalizedAbuseIPDBType("Fixed Line ISP"); usage != "isp" {
		t.Fatalf("unexpected AbuseIPDB type mapping: %q", usage)
	}
	if usage := normalizedAbuseIPDBType("Search Engine Spider"); usage != "other" {
		t.Fatalf("unexpected AbuseIPDB type mapping: %q", usage)
	}
}

func TestParseIP2Location(t *testing.T) {
	body := []byte(`{
		"country_code": "JP", "usage_type": "DCH/ISP", "as_info": {"as_usage_type": "COM"},
		"is_proxy": true, "proxy": {"is_public_proxy": false, "is_web_proxy": false, "is_tor": true,
		"is_vpn": false, "is_data_center": true, "is_spammer": false, "is_web_crawler": false,
		"is_scanner": true, "is_botnet": false}, "fraud_score": 72
	}`)
	result, err := parseIP2Location(body)
	if err != nil {
		t.Fatalf("parseIP2Location: %v", err)
	}
	if result.Status != "available" || result.CountryCode != "JP" || result.UsageType != "hosting" || result.CompanyType != "business" {
		t.Fatalf("unexpected IP2Location result: %#v", result)
	}
	if result.Score == nil || *result.Score != 72 || result.RiskLevel != "high" {
		t.Fatalf("unexpected IP2Location score: %#v", result)
	}
	if !result.Factors["proxy"] || !result.Factors["tor"] || !result.Factors["server"] || !result.Factors["robot"] {
		t.Fatalf("unexpected IP2Location factors: %#v", result.Factors)
	}
}

func TestParseIPData(t *testing.T) {
	body := []byte(`{"country_code": "CA", "threat": {"is_proxy": true, "is_tor": false,
		"is_datacenter": false, "is_threat": false, "is_known_abuser": true, "is_known_attacker": false}}`)
	result, err := parseIPData(body)
	if err != nil {
		t.Fatalf("parseIPData: %v", err)
	}
	if result.Status != "available" || result.CountryCode != "CA" || !result.Factors["proxy"] || !result.Factors["abuser"] {
		t.Fatalf("unexpected ipdata result: %#v", result)
	}
}

func TestParseIPQS(t *testing.T) {
	body := []byte(`{"country_code": "US", "fraud_score": 30, "proxy": false, "tor": false,
		"vpn": true, "recent_abuse": false, "bot_status": false}`)
	result, err := parseIPQS(body)
	if err != nil {
		t.Fatalf("parseIPQS: %v", err)
	}
	if result.Status != "available" || result.CountryCode != "US" || result.Score == nil || *result.Score != 30 || result.RiskLevel != "low" {
		t.Fatalf("unexpected IPQS result: %#v", result)
	}
	if !result.Factors["vpn"] || result.Factors["tor"] {
		t.Fatalf("unexpected IPQS factors: %#v", result.Factors)
	}
}

func TestProviderResultHasData(t *testing.T) {
	if providerResultHasData(ipProviderResult{}) {
		t.Fatal("empty provider result must not count as having data")
	}
	if !providerResultHasData(ipProviderResult{Status: "available", CountryCode: "US"}) {
		t.Fatal("country-only provider result must count as having data")
	}
	if !providerResultHasData(ipProviderResult{Status: "available", Score: &[]float64{10}[0]}) {
		t.Fatal("score-only provider result must count as having data")
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

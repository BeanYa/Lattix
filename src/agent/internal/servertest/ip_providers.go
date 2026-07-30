package servertest

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
)

const providerResponseLimit = 1 << 20

type ipProviderResult struct {
	Name             string          `json:"name"`
	Status           string          `json:"status"`
	Score            *float64        `json:"score,omitempty"`
	RiskLevel        string          `json:"risk_level,omitempty"`
	UsageType        string          `json:"usage_type,omitempty"`
	CompanyType      string          `json:"company_type,omitempty"`
	CountryCode      string          `json:"country_code,omitempty"`
	Factors          map[string]bool `json:"factors,omitempty"`
	FactorHits       int             `json:"factor_hits"`
	EffectiveFactors int             `json:"effective_factors"`
	ErrorCode        string          `json:"error_code,omitempty"`
	ErrorMessage     string          `json:"error_message,omitempty"`
}

type ipProviderCheck struct {
	name string
	run  func(context.Context, *http.Client, netip.Addr) ipProviderResult
}

var directIPProviderChecks = []ipProviderCheck{
	{name: "IPinfo", run: checkIPinfo},
	{name: "IPregistry", run: checkIPregistry},
	{name: "ipapi.is", run: checkIPAPI},
	{name: "DB-IP", run: checkDBIP},
}

var unavailableIPProviders = []string{
	"MaxMind", "Scamalytics", "AbuseIPDB", "IP2Location", "ipdata", "IPQS",
}

func runIPProviders(ctx context.Context, client *http.Client, address netip.Addr) []ipProviderResult {
	results := make([]ipProviderResult, len(directIPProviderChecks))
	var group sync.WaitGroup
	for index, check := range directIPProviderChecks {
		group.Add(1)
		go func(index int, check ipProviderCheck) {
			defer group.Done()
			results[index] = check.run(ctx, client, address)
			results[index].Name = check.name
		}(index, check)
	}
	group.Wait()
	for _, name := range unavailableIPProviders {
		results = append(results, ipProviderResult{
			Name: name, Status: "provider_access_unavailable", ErrorCode: "provider_access_unavailable",
			ErrorMessage: "this provider requires a private API credential or an upstream proxy",
		})
	}
	sort.SliceStable(results, func(i, j int) bool { return results[i].Name < results[j].Name })
	return results
}

func checkIPinfo(ctx context.Context, client *http.Client, address netip.Addr) ipProviderResult {
	var response struct {
		Data struct {
			Country string `json:"country"`
			ASN     struct {
				Type string `json:"type"`
			} `json:"asn"`
			Company struct {
				Type string `json:"type"`
			} `json:"company"`
			Privacy struct {
				Proxy   *bool `json:"proxy"`
				Tor     *bool `json:"tor"`
				VPN     *bool `json:"vpn"`
				Hosting *bool `json:"hosting"`
			} `json:"privacy"`
		} `json:"data"`
	}
	result := ipProviderResult{Status: "available"}
	endpoint := "https://ipinfo.io/widget/demo/" + address.String()
	if err := getJSON(ctx, client, endpoint, address, &response); err != nil {
		return providerFailure(address, err)
	}
	result.UsageType = normalizedType(response.Data.ASN.Type)
	result.CompanyType = normalizedType(response.Data.Company.Type)
	result.CountryCode = normalizedCountry(response.Data.Country)
	result.Factors = boolFactors(map[string]*bool{
		"proxy": response.Data.Privacy.Proxy, "tor": response.Data.Privacy.Tor,
		"vpn": response.Data.Privacy.VPN, "server": response.Data.Privacy.Hosting,
	})
	result.FactorHits, result.EffectiveFactors = factorCounts(result.Factors)
	return result
}

func checkIPAPI(ctx context.Context, client *http.Client, address netip.Addr) ipProviderResult {
	var response struct {
		ASN struct {
			Type string `json:"type"`
		} `json:"asn"`
		Company struct {
			Type        string          `json:"type"`
			AbuserScore json.RawMessage `json:"abuser_score"`
		} `json:"company"`
		Location struct {
			CountryCode string `json:"country_code"`
		} `json:"location"`
		IsProxy      *bool `json:"is_proxy"`
		IsTor        *bool `json:"is_tor"`
		IsVPN        *bool `json:"is_vpn"`
		IsDatacenter *bool `json:"is_datacenter"`
		IsAbuser     *bool `json:"is_abuser"`
		IsCrawler    *bool `json:"is_crawler"`
	}
	if err := getJSON(ctx, client, "https://api.ipapi.is/?q="+address.String(), address, &response); err != nil {
		return providerFailure(address, err)
	}
	result := ipProviderResult{
		Status: "available", UsageType: normalizedType(response.ASN.Type), CompanyType: normalizedType(response.Company.Type),
		CountryCode: normalizedCountry(response.Location.CountryCode),
	}
	if score, risk, ok := parseAbuserScore(response.Company.AbuserScore); ok {
		result.Score = &score
		result.RiskLevel = risk
	}
	result.Factors = boolFactors(map[string]*bool{
		"proxy": response.IsProxy, "tor": response.IsTor, "vpn": response.IsVPN,
		"server": response.IsDatacenter, "abuser": response.IsAbuser, "robot": response.IsCrawler,
	})
	result.FactorHits, result.EffectiveFactors = factorCounts(result.Factors)
	return result
}

var ipregistryKeyPattern = regexp.MustCompile(`(?i)apiKey=["']([a-z0-9]+)["']`)
var providerKeyPattern = regexp.MustCompile(`([?&]key=)[^&" ]+`)

func checkIPregistry(ctx context.Context, client *http.Client, address netip.Addr) ipProviderResult {
	page, err := getBody(ctx, client, "https://ipregistry.co", address)
	if err != nil {
		return providerFailure(address, fmt.Errorf("demo page: %w", err))
	}
	match := ipregistryKeyPattern.FindSubmatch(page)
	if len(match) != 2 {
		return ipProviderResult{Status: "provider_access_unavailable", ErrorCode: "demo_key_unavailable", ErrorMessage: "IPregistry public demo key was not present in the provider page"}
	}
	var response struct {
		Connection struct {
			Type string `json:"type"`
		} `json:"connection"`
		Company struct {
			Type string `json:"type"`
		} `json:"company"`
		Location struct {
			Country struct {
				Code string `json:"code"`
			} `json:"country"`
		} `json:"location"`
		Security struct {
			IsProxy         *bool `json:"is_proxy"`
			IsTor           *bool `json:"is_tor"`
			IsTorExit       *bool `json:"is_tor_exit"`
			IsVPN           *bool `json:"is_vpn"`
			IsCloudProvider *bool `json:"is_cloud_provider"`
			IsAbuser        *bool `json:"is_abuser"`
		} `json:"security"`
	}
	endpoint := "https://api.ipregistry.co/" + address.String() + "?hostname=true&key=" + string(match[1])
	if err := getJSONWithHeaders(ctx, client, endpoint, address, &response, map[string]string{
		"Origin": "https://ipregistry.co", "Referer": "https://ipregistry.co/",
	}); err != nil {
		return providerFailure(address, err)
	}
	tor := combineBool(response.Security.IsTor, response.Security.IsTorExit)
	result := ipProviderResult{
		Status: "available", UsageType: normalizedType(response.Connection.Type), CompanyType: normalizedType(response.Company.Type),
		CountryCode: normalizedCountry(response.Location.Country.Code),
	}
	result.Factors = boolFactors(map[string]*bool{
		"proxy": response.Security.IsProxy, "tor": tor, "vpn": response.Security.IsVPN,
		"server": response.Security.IsCloudProvider, "abuser": response.Security.IsAbuser,
	})
	result.FactorHits, result.EffectiveFactors = factorCounts(result.Factors)
	return result
}

var (
	dbipRiskPattern    = regexp.MustCompile(`(?is)Estimated threat level for this IP address is\s*<span[^>]*>\s*([^<]+)`)
	dbipCountryPattern = regexp.MustCompile(`(?is)"countryCode"\s*:\s*"([A-Za-z]{2})"`)
	dbipFactorPattern  = regexp.MustCompile(`(?is)<th[^>]*>\s*(Crawler|Proxy|Attack source)\s*</th>.*?<span[^>]*class="sr-only"[^>]*>\s*(Yes|No)`)
)

func checkDBIP(ctx context.Context, client *http.Client, address netip.Addr) ipProviderResult {
	body, err := getBody(ctx, client, "https://db-ip.com/"+address.String(), address)
	if err != nil {
		return providerFailure(address, err)
	}
	result := parseDBIP(body)
	if result.RiskLevel == "" && result.CountryCode == "" && result.EffectiveFactors == 0 {
		return ipProviderResult{Status: "failed", ErrorCode: "provider_response_unrecognized", ErrorMessage: "DB-IP returned an unrecognized page layout"}
	}
	result.Status = "available"
	return result
}

func parseDBIP(body []byte) ipProviderResult {
	result := ipProviderResult{Factors: make(map[string]bool)}
	if match := dbipRiskPattern.FindSubmatch(body); len(match) == 2 {
		result.RiskLevel = strings.ToLower(strings.TrimSpace(string(match[1])))
		score := map[string]float64{"low": 0, "medium": 50, "high": 100}[result.RiskLevel]
		result.Score = &score
	}
	if match := dbipCountryPattern.FindSubmatch(body); len(match) == 2 {
		result.CountryCode = normalizedCountry(string(match[1]))
	}
	for _, match := range dbipFactorPattern.FindAllSubmatch(body, -1) {
		name := map[string]string{"crawler": "robot", "proxy": "proxy", "attack source": "abuser"}[strings.ToLower(strings.TrimSpace(string(match[1])))]
		result.Factors[name] = strings.EqualFold(strings.TrimSpace(string(match[2])), "yes")
	}
	result.FactorHits, result.EffectiveFactors = factorCounts(result.Factors)
	return result
}

func getJSON(ctx context.Context, client *http.Client, endpoint string, address netip.Addr, target any) error {
	return getJSONWithHeaders(ctx, client, endpoint, address, target, nil)
}

func getJSONWithHeaders(ctx context.Context, client *http.Client, endpoint string, address netip.Addr, target any, headers map[string]string) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return redactProviderError(err, address)
	}
	request.Header.Set("User-Agent", "Mozilla/5.0 Lattix-Agent")
	request.Header.Set("Accept", "application/json")
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	response, err := client.Do(request)
	if err != nil {
		return redactProviderError(err, address)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", response.StatusCode)
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, providerResponseLimit))
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}

func getBody(ctx context.Context, client *http.Client, endpoint string, address netip.Addr) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, redactProviderError(err, address)
	}
	request.Header.Set("User-Agent", "Mozilla/5.0 Lattix-Agent")
	response, err := client.Do(request)
	if err != nil {
		return nil, redactProviderError(err, address)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", response.StatusCode)
	}
	return io.ReadAll(io.LimitReader(response.Body, providerResponseLimit))
}

func providerFailure(address netip.Addr, err error) ipProviderResult {
	message := redactProviderError(err, address).Error()
	return ipProviderResult{Status: "failed", ErrorCode: "provider_request_failed", ErrorMessage: message}
}

func redactProviderError(err error, address netip.Addr) error {
	if err == nil {
		return nil
	}
	message := strings.ReplaceAll(err.Error(), address.String(), "[redacted]")
	message = providerKeyPattern.ReplaceAllString(message, `${1}[redacted]`)
	return fmt.Errorf("%s", message)
}

func boolFactors(values map[string]*bool) map[string]bool {
	result := make(map[string]bool, len(values))
	for name, value := range values {
		if value != nil {
			result[name] = *value
		}
	}
	return result
}

func factorCounts(factors map[string]bool) (hits, effective int) {
	for _, value := range factors {
		effective++
		if value {
			hits++
		}
	}
	return hits, effective
}

func combineBool(values ...*bool) *bool {
	found, combined := false, false
	for _, value := range values {
		if value == nil {
			continue
		}
		found = true
		combined = combined || *value
	}
	if !found {
		return nil
	}
	return &combined
}

func normalizedType(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	switch value {
	case "business", "isp", "hosting", "education", "government", "banking", "organization", "military", "library", "cdn", "mobile", "reserved":
		return value
	case "":
		return ""
	default:
		return "other"
	}
}

func normalizedCountry(value string) string {
	value = strings.ToUpper(strings.TrimSpace(value))
	if len(value) != 2 {
		return ""
	}
	return value
}

func parseAbuserScore(raw json.RawMessage) (float64, string, bool) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return 0, "", false
	}
	var number float64
	if json.Unmarshal(trimmed, &number) == nil {
		if number <= 1 {
			number *= 100
		}
		return number, riskLevel(number), true
	}
	var text string
	if json.Unmarshal(trimmed, &text) != nil {
		return 0, "", false
	}
	fields := strings.Fields(text)
	if len(fields) == 0 {
		return 0, "", false
	}
	value, err := strconv.ParseFloat(strings.TrimSuffix(fields[0], "%"), 64)
	if err != nil {
		return 0, "", false
	}
	if !strings.HasSuffix(fields[0], "%") && value <= 1 {
		value *= 100
	}
	risk := ""
	if open := strings.IndexByte(text, '('); open >= 0 {
		if close := strings.IndexByte(text[open+1:], ')'); close >= 0 {
			risk = strings.ToLower(strings.ReplaceAll(strings.TrimSpace(text[open+1:open+1+close]), " ", "_"))
		}
	}
	if risk == "" {
		risk = riskLevel(value)
	}
	return value, risk, true
}

func riskLevel(score float64) string {
	switch {
	case score < 20:
		return "very_low"
	case score < 40:
		return "low"
	case score < 60:
		return "medium"
	case score < 80:
		return "high"
	default:
		return "very_high"
	}
}

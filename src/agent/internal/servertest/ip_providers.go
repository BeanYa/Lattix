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
	{name: "AbuseIPDB", run: checkAbuseIPDB},
	{name: "DB-IP", run: checkDBIP},
	{name: "IP2Location", run: checkIP2Location},
	{name: "IPQS", run: checkIPQS},
	{name: "IPinfo", run: checkIPinfo},
	{name: "IPregistry", run: checkIPregistry},
	{name: "MaxMind", run: checkMaxMind},
	{name: "Scamalytics", run: checkScamalytics},
	{name: "ipapi.is", run: checkIPAPI},
	{name: "ipdata", run: checkIPData},
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

// ipregistryDemoKey is the public demo key IPregistry historically embedded in
// its landing page. The page stopped exposing it, so the key is pinned as the
// fallback and any key still present in the page overrides it.
const ipregistryDemoKey = "sb69ksjcajfs4c"

func ipregistryDemoKeyFromPage(page []byte) string {
	if match := ipregistryKeyPattern.FindSubmatch(page); len(match) == 2 {
		return string(match[1])
	}
	return ipregistryDemoKey
}

func checkIPregistry(ctx context.Context, client *http.Client, address netip.Addr) ipProviderResult {
	key := ipregistryDemoKey
	if page, err := getBody(ctx, client, "https://ipregistry.co", address); err == nil {
		key = ipregistryDemoKeyFromPage(page)
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
	endpoint := "https://api.ipregistry.co/" + address.String() + "?hostname=true&key=" + key
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

// checkDBIP uses the official free API: the db-ip.com website serves a
// Cloudflare challenge to non-browser requests and no longer allows scraping.
// The free tier only exposes geolocation; threat level and proxy/crawler
// factors require credentials, so they are omitted rather than fabricated.
func checkDBIP(ctx context.Context, client *http.Client, address netip.Addr) ipProviderResult {
	body, err := getBody(ctx, client, "https://api.db-ip.com/v2/free/"+address.String(), address)
	if err != nil {
		return providerFailure(address, err)
	}
	result, err := parseDBIPAPI(body)
	if err != nil {
		return ipProviderResult{Status: "failed", ErrorCode: "provider_response_unrecognized", ErrorMessage: "DB-IP returned an unrecognized API response"}
	}
	return result
}

func parseDBIPAPI(body []byte) (ipProviderResult, error) {
	var response struct {
		CountryCode string `json:"countryCode"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return ipProviderResult{}, err
	}
	return ipProviderResult{
		Status: "available", CountryCode: normalizedCountry(response.CountryCode),
	}, nil
}

// ipQualityProxyBase is the upstream aggregation proxy used by the
// xykt/IPQuality ecosystem. It resolves MaxMind, Scamalytics, AbuseIPDB,
// IP2Location, ipdata and IPQS lookups server-side; credentials stay on the
// proxy and no key is shipped with the agent. Response formats mirror the
// upstream ip.sh jq paths.
const ipQualityProxyBase = "https://ipinfo.check.place/"

func checkProxyProvider(ctx context.Context, client *http.Client, address netip.Addr, query string, parse func([]byte) (ipProviderResult, error)) ipProviderResult {
	body, err := getBody(ctx, client, ipQualityProxyBase+address.String()+"?"+query, address)
	if err != nil {
		return providerFailure(address, err)
	}
	result, err := parse(body)
	if err != nil || !providerResultHasData(result) {
		return ipProviderResult{Status: "failed", ErrorCode: "provider_response_unrecognized", ErrorMessage: "proxy returned an unrecognized response for " + query}
	}
	return result
}

func providerResultHasData(result ipProviderResult) bool {
	return result.CountryCode != "" || result.UsageType != "" || result.CompanyType != "" || result.Score != nil || result.EffectiveFactors > 0
}

func checkMaxMind(ctx context.Context, client *http.Client, address netip.Addr) ipProviderResult {
	return checkProxyProvider(ctx, client, address, "lang=en", parseMaxMind)
}

func parseMaxMind(body []byte) (ipProviderResult, error) {
	var response struct {
		Country struct {
			IsoCode string `json:"IsoCode"`
		} `json:"Country"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return ipProviderResult{}, err
	}
	return ipProviderResult{
		Status: "available", CountryCode: normalizedCountry(response.Country.IsoCode),
	}, nil
}

func checkScamalytics(ctx context.Context, client *http.Client, address netip.Addr) ipProviderResult {
	return checkProxyProvider(ctx, client, address, "db=scamalytics", parseScamalytics)
}

func parseScamalytics(body []byte) (ipProviderResult, error) {
	var response struct {
		External struct {
			Maxmind struct {
				IPCountryCode string `json:"ip_country_code"`
			} `json:"maxmind_geolite2"`
			Firehol struct {
				IsProxy *bool `json:"is_proxy"`
			} `json:"firehol"`
			X4BNet struct {
				IsTor                *bool `json:"is_tor"`
				IsBlacklistedSpambot *bool `json:"is_blacklisted_spambot"`
				IsBotOperaMini       *bool `json:"is_bot_operamini"`
				IsBotSemrush         *bool `json:"is_bot_semrush"`
			} `json:"x4bnet"`
		} `json:"external_datasources"`
		Scamalytics struct {
			Score                  *float64 `json:"scamalytics_score"`
			IsBlacklistedExternal  *bool    `json:"is_blacklisted_external"`
			Proxy                  struct {
				IsVPN         *bool `json:"is_vpn"`
				IsDatacenter  *bool `json:"is_datacenter"`
			} `json:"scamalytics_proxy"`
		} `json:"scamalytics"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return ipProviderResult{}, err
	}
	result := ipProviderResult{
		Status: "available", CountryCode: normalizedCountry(response.External.Maxmind.IPCountryCode),
	}
	if response.Scamalytics.Score != nil {
		score := *response.Scamalytics.Score
		result.Score = &score
		result.RiskLevel = riskLevel(score)
	}
	result.Factors = boolFactors(map[string]*bool{
		"proxy": response.External.Firehol.IsProxy, "tor": response.External.X4BNet.IsTor,
		"vpn": response.Scamalytics.Proxy.IsVPN, "server": response.Scamalytics.Proxy.IsDatacenter,
		"abuser": response.Scamalytics.IsBlacklistedExternal,
		"robot": combineBool(response.External.X4BNet.IsBlacklistedSpambot, response.External.X4BNet.IsBotOperaMini, response.External.X4BNet.IsBotSemrush),
	})
	result.FactorHits, result.EffectiveFactors = factorCounts(result.Factors)
	return result, nil
}

func checkAbuseIPDB(ctx context.Context, client *http.Client, address netip.Addr) ipProviderResult {
	return checkProxyProvider(ctx, client, address, "db=abuseipdb", parseAbuseIPDB)
}

func parseAbuseIPDB(body []byte) (ipProviderResult, error) {
	var response struct {
		Data struct {
			UsageType             string   `json:"usageType"`
			AbuseConfidenceScore  *float64 `json:"abuseConfidenceScore"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return ipProviderResult{}, err
	}
	result := ipProviderResult{
		Status: "available", UsageType: normalizedAbuseIPDBType(response.Data.UsageType),
	}
	if response.Data.AbuseConfidenceScore != nil {
		score := *response.Data.AbuseConfidenceScore
		result.Score = &score
		result.RiskLevel = riskLevel(score)
	}
	return result, nil
}

func checkIP2Location(ctx context.Context, client *http.Client, address netip.Addr) ipProviderResult {
	return checkProxyProvider(ctx, client, address, "db=ip2location", parseIP2Location)
}

func parseIP2Location(body []byte) (ipProviderResult, error) {
	var response struct {
		CountryCode string `json:"country_code"`
		UsageType   string `json:"usage_type"`
		ASInfo      struct {
			UsageType string `json:"as_usage_type"`
		} `json:"as_info"`
		IsProxy    *bool `json:"is_proxy"`
		Proxy      struct {
			IsPublicProxy *bool `json:"is_public_proxy"`
			IsWebProxy    *bool `json:"is_web_proxy"`
			IsTor         *bool `json:"is_tor"`
			IsVPN         *bool `json:"is_vpn"`
			IsDataCenter  *bool `json:"is_data_center"`
			IsSpammer     *bool `json:"is_spammer"`
			IsWebCrawler  *bool `json:"is_web_crawler"`
			IsScanner     *bool `json:"is_scanner"`
			IsBotnet      *bool `json:"is_botnet"`
		} `json:"proxy"`
		FraudScore *float64 `json:"fraud_score"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return ipProviderResult{}, err
	}
	result := ipProviderResult{
		Status: "available", UsageType: normalizedIP2LocationType(response.UsageType),
		CompanyType: normalizedIP2LocationType(response.ASInfo.UsageType),
		CountryCode: normalizedCountry(response.CountryCode),
	}
	if response.FraudScore != nil {
		score := *response.FraudScore
		result.Score = &score
		result.RiskLevel = riskLevel(score)
	}
	result.Factors = boolFactors(map[string]*bool{
		"proxy": combineBool(response.IsProxy, response.Proxy.IsPublicProxy, response.Proxy.IsWebProxy),
		"tor": response.Proxy.IsTor, "vpn": response.Proxy.IsVPN, "server": response.Proxy.IsDataCenter,
		"abuser": response.Proxy.IsSpammer,
		"robot": combineBool(response.Proxy.IsWebCrawler, response.Proxy.IsScanner, response.Proxy.IsBotnet),
	})
	result.FactorHits, result.EffectiveFactors = factorCounts(result.Factors)
	return result, nil
}

func checkIPData(ctx context.Context, client *http.Client, address netip.Addr) ipProviderResult {
	return checkProxyProvider(ctx, client, address, "db=ipdata", parseIPData)
}

func parseIPData(body []byte) (ipProviderResult, error) {
	var response struct {
		CountryCode string `json:"country_code"`
		Threat      struct {
			IsProxy         *bool `json:"is_proxy"`
			IsTor           *bool `json:"is_tor"`
			IsDatacenter    *bool `json:"is_datacenter"`
			IsThreat        *bool `json:"is_threat"`
			IsKnownAbuser   *bool `json:"is_known_abuser"`
			IsKnownAttacker *bool `json:"is_known_attacker"`
		} `json:"threat"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return ipProviderResult{}, err
	}
	result := ipProviderResult{
		Status: "available", CountryCode: normalizedCountry(response.CountryCode),
	}
	result.Factors = boolFactors(map[string]*bool{
		"proxy": response.Threat.IsProxy, "tor": response.Threat.IsTor, "server": response.Threat.IsDatacenter,
		"abuser": combineBool(response.Threat.IsThreat, response.Threat.IsKnownAbuser, response.Threat.IsKnownAttacker),
	})
	result.FactorHits, result.EffectiveFactors = factorCounts(result.Factors)
	return result, nil
}

func checkIPQS(ctx context.Context, client *http.Client, address netip.Addr) ipProviderResult {
	return checkProxyProvider(ctx, client, address, "db=ipqualityscore", parseIPQS)
}

func parseIPQS(body []byte) (ipProviderResult, error) {
	var response struct {
		CountryCode string   `json:"country_code"`
		FraudScore  *float64 `json:"fraud_score"`
		Proxy       *bool    `json:"proxy"`
		Tor         *bool    `json:"tor"`
		VPN         *bool    `json:"vpn"`
		RecentAbuse *bool    `json:"recent_abuse"`
		BotStatus   *bool    `json:"bot_status"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return ipProviderResult{}, err
	}
	result := ipProviderResult{
		Status: "available", CountryCode: normalizedCountry(response.CountryCode),
	}
	if response.FraudScore != nil {
		score := *response.FraudScore
		result.Score = &score
		result.RiskLevel = riskLevel(score)
	}
	result.Factors = boolFactors(map[string]*bool{
		"proxy": response.Proxy, "tor": response.Tor, "vpn": response.VPN,
		"abuser": response.RecentAbuse, "robot": response.BotStatus,
	})
	result.FactorHits, result.EffectiveFactors = factorCounts(result.Factors)
	return result, nil
}

func normalizedAbuseIPDBType(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "commercial":
		return "business"
	case "data center/web hosting/transit":
		return "hosting"
	case "university/college/school":
		return "education"
	case "government":
		return "government"
	case "banking":
		return "banking"
	case "organization":
		return "organization"
	case "military":
		return "military"
	case "library":
		return "library"
	case "content delivery network":
		return "cdn"
	case "fixed line isp":
		return "isp"
	case "mobile isp":
		return "mobile"
	case "reserved":
		return "reserved"
	case "":
		return ""
	default:
		return "other"
	}
}

func normalizedIP2LocationType(value string) string {
	value = strings.ToUpper(strings.SplitN(strings.TrimSpace(value), "/", 2)[0])
	switch value {
	case "COM":
		return "business"
	case "DCH":
		return "hosting"
	case "EDU":
		return "education"
	case "GOV":
		return "government"
	case "ORG":
		return "organization"
	case "MIL":
		return "military"
	case "LIB":
		return "library"
	case "CDN":
		return "cdn"
	case "ISP":
		return "isp"
	case "MOB":
		return "mobile"
	case "RSV":
		return "reserved"
	case "":
		return ""
	default:
		return "other"
	}
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

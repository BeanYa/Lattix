package servertest

import (
	"bufio"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"

	"lattix/shared"
)

const ipQualityTimeout = 10 * time.Minute

var streamingChecks = []struct {
	name string
	url  string
}{
	{"TikTok", "https://www.tiktok.com/"},
	{"Disney+", "https://www.disneyplus.com/"},
	{"Netflix", "https://www.netflix.com/title/81215567"},
	{"YouTube Premium", "https://www.youtube.com/premium"},
	{"Prime Video", "https://www.primevideo.com/"},
	{"Reddit", "https://www.reddit.com/"},
	{"ChatGPT", "https://chatgpt.com/"},
}

func (r *Runner) runIPQuality(parent context.Context, category shared.ServerTestCategory, _ []shared.ServerTestTarget, update func(int, int, string)) shared.ServerTestCategoryResult {
	ctx, cancel := context.WithTimeout(parent, ipQualityTimeout)
	defer cancel()
	families := []shared.ServerTestAddressFamily{shared.ServerTestIPv4, shared.ServerTestIPv6}
	items := make([]map[string]any, 0, len(families))
	available := 0
	limited := false
	for index, family := range families {
		item := runIPFamilyQuality(ctx, family)
		items = append(items, item)
		if item["status"] == "available" || item["status"] == "limited" {
			available++
		}
		limited = limited || item["status"] == "limited"
		update(index+1, len(families), string(family))
	}
	status := "available"
	if available == 0 {
		status = "unavailable"
	} else if limited || available != len(families) {
		status = "limited"
	}
	return shared.ServerTestCategoryResult{
		Category: category, Status: status,
		Summary: map[string]any{
			"families": len(families), "available_families": available,
			"overall_score": nil, "runtime": runtimeSummary(),
		},
		Items: items,
	}
}

func runIPFamilyQuality(ctx context.Context, family shared.ServerTestAddressFamily) map[string]any {
	client := forcedFamilyClient(family, 20*time.Second)
	identity, identityErr := cloudflareIdentity(ctx, client)
	item := map[string]any{
		"address_family": family,
		"status":         "unavailable",
	}
	if identityErr != nil {
		item["error_code"] = "public_address_unavailable"
		item["error_message"] = identityErr.Error()
		return item
	}
	// The public address is used only in memory for provider/DNSBL requests and
	// is deliberately omitted from the report.
	_ = identity.address
	item["network"] = map[string]any{
		"country":       identity.country,
		"warp":          identity.warp,
		"http_protocol": identity.httpProtocol,
		"asn":           runASNEnrichment(ctx, identity.address),
	}
	providers := runIPProviders(ctx, client, identity.address)
	providerSummary := summarizeIPProviders(providers)
	item["providers"] = providers
	item["provider_summary"] = providerSummary
	if providerSummary["available_providers"].(int) > 0 {
		item["status"] = "available"
	} else {
		item["status"] = "limited"
	}
	item["dnsbl"] = runDNSBL(ctx, identity.address)
	streaming := make([]map[string]any, 0, len(streamingChecks))
	for _, check := range streamingChecks {
		streaming = append(streaming, checkStreaming(ctx, client, check.name, check.url))
	}
	item["streaming"] = streaming
	return item
}

func summarizeIPProviders(providers []ipProviderResult) map[string]any {
	available, failed, factorHits, effectiveFactors := 0, 0, 0, 0
	var scoreTotal float64
	scoreCount := 0
	for _, provider := range providers {
		switch provider.Status {
		case "available":
			available++
		case "failed":
			failed++
		}
		factorHits += provider.FactorHits
		effectiveFactors += provider.EffectiveFactors
		if provider.Score != nil {
			scoreTotal += *provider.Score
			scoreCount++
		}
	}
	var overallScore any
	if scoreCount > 0 {
		overallScore = scoreTotal / float64(scoreCount)
	}
	return map[string]any{
		"total_providers": len(providers), "available_providers": available, "failed_providers": failed,
		"scored_providers": scoreCount, "overall_score": overallScore,
		"factor_hits": factorHits, "effective_factors": effectiveFactors,
	}
}

type publicIdentity struct {
	address      netip.Addr
	country      string
	warp         string
	httpProtocol string
}

func cloudflareIdentity(ctx context.Context, client *http.Client) (publicIdentity, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://www.cloudflare.com/cdn-cgi/trace", nil)
	if err != nil {
		return publicIdentity{}, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return publicIdentity{}, fmt.Errorf("Cloudflare preflight: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return publicIdentity{}, fmt.Errorf("Cloudflare preflight: HTTP %d", resp.StatusCode)
	}
	identity := publicIdentity{}
	scanner := bufio.NewScanner(io.LimitReader(resp.Body, 32<<10))
	for scanner.Scan() {
		key, value, ok := strings.Cut(scanner.Text(), "=")
		if !ok {
			continue
		}
		switch key {
		case "ip":
			identity.address, _ = netip.ParseAddr(value)
		case "loc":
			identity.country = value
		case "warp":
			identity.warp = value
		case "http":
			identity.httpProtocol = value
		}
	}
	if err := scanner.Err(); err != nil {
		return publicIdentity{}, err
	}
	if !publicAddress(identity.address.Unmap()) {
		return publicIdentity{}, fmt.Errorf("Cloudflare preflight returned no public address")
	}
	return identity, nil
}

func forcedFamilyClient(family shared.ServerTestAddressFamily, timeout time.Duration) *http.Client {
	network := "tcp4"
	if family == shared.ServerTestIPv6 {
		network = "tcp6"
	}
	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: -1}
	transport := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: func(ctx context.Context, _, address string) (net.Conn, error) {
			return dialer.DialContext(ctx, network, address)
		},
		TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12},
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 10 * time.Second,
		DisableKeepAlives:     true,
	}
	return &http.Client{
		Transport: transport, Timeout: timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return fmt.Errorf("too many redirects")
			}
			if err := validateRedirectURL(req.URL); err != nil {
				return err
			}
			return nil
		},
	}
}

func validateRedirectURL(target *url.URL) error {
	if target.Scheme != "https" || target.Hostname() == "" {
		return fmt.Errorf("target_policy_rejected: redirect must use HTTPS")
	}
	return nil
}

func checkStreaming(ctx context.Context, client *http.Client, name, rawURL string) map[string]any {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return map[string]any{"name": name, "status": "indeterminate", "error": err.Error()}
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 Lattix-Agent")
	resp, err := client.Do(req)
	if err != nil {
		return map[string]any{"name": name, "status": "indeterminate", "error": err.Error()}
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	_ = resp.Body.Close()
	status := "available"
	if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusUnavailableForLegalReasons {
		status = "unavailable"
	} else if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
		status = "indeterminate"
	}
	return map[string]any{"name": name, "status": status, "http_status": resp.StatusCode}
}

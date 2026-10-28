package servertest

import (
	"context"
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"sort"
	"strconv"
	"sync"
	"time"

	"lattix/shared"
)

const (
	// connectivityProbeAttempts mirrors the ip.net.coffee reference: one
	// unmeasured warmup round, then N measured rounds with the median taken
	// as the site latency.
	connectivityProbeAttempts  = 4
	connectivityConcurrency    = 12
	connectivityRequestTimeout = 3 * time.Second
	connectivityTargetTimeout  = 30 * time.Second
	connectivityAttemptGap     = 120 * time.Millisecond
	connectivityDrainLimit     = 4 << 10
	connectivityUserAgent      = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0 Safari/537.36"
)

type connectivityTargetResult struct {
	ID            string    `json:"id"`
	Label         string    `json:"label"`
	Group         string    `json:"group,omitempty"`
	Host          string    `json:"host"`
	AddressFamily string    `json:"address_family"`
	ProbeMethod   string    `json:"probe_method"`
	Sent          int       `json:"sent"`
	Received      int       `json:"received"`
	LossPercent   float64   `json:"loss_percent"`
	RTTMedianMS   *float64  `json:"rtt_median_ms,omitempty"`
	RTTMinMS      *float64  `json:"rtt_min_ms,omitempty"`
	RTTMaxMS      *float64  `json:"rtt_max_ms,omitempty"`
	Samples       []float64 `json:"samples,omitempty"`
	ErrorCode     string    `json:"error_code,omitempty"`
	ErrorMessage  string    `json:"error_message,omitempty"`
}

// connectivityClientFactory is a variable so tests can install a client that
// trusts the httptest TLS server's self-signed certificate.
var connectivityClientFactory = newConnectivityClient

func (r *Runner) runConnectivity(ctx context.Context, category shared.ServerTestCategory, targets []shared.ServerTestTarget, update func(int, int, string)) shared.ServerTestCategoryResult {
	if len(targets) == 0 {
		return unsupportedResult(category, "catalog_targets_unavailable", "no targets were supplied for this test category")
	}
	client := connectivityClientFactory(false)
	results := make([]connectivityTargetResult, len(targets))
	jobs := make(chan int)
	var wg sync.WaitGroup
	workers := connectivityConcurrency
	if len(targets) < workers {
		workers = len(targets)
	}
	completed := 0
	var completedMu sync.Mutex
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for index := range jobs {
				results[index] = runConnectivityTarget(ctx, targets[index], client)
				completedMu.Lock()
				completed++
				current := completed
				completedMu.Unlock()
				update(current, len(targets), targets[index].Label)
			}
		}()
	}
sendJobs:
	for index := range targets {
		select {
		case jobs <- index:
		case <-ctx.Done():
			break sendJobs
		}
	}
	close(jobs)
	wg.Wait()
	for index := range results {
		if results[index].ID == "" {
			results[index] = interruptedConnectivityTarget(targets[index], ctx.Err())
		}
	}
	items := make([]map[string]any, 0, len(results))
	failed := 0
	for _, result := range results {
		encoded := map[string]any{
			"id": result.ID, "label": result.Label, "group": result.Group, "host": result.Host,
			"address_family": result.AddressFamily, "probe_method": result.ProbeMethod,
			"sent": result.Sent, "received": result.Received, "loss_percent": result.LossPercent,
		}
		if result.RTTMedianMS != nil {
			encoded["rtt_median_ms"] = *result.RTTMedianMS
			encoded["rtt_min_ms"], encoded["rtt_max_ms"] = *result.RTTMinMS, *result.RTTMaxMS
			encoded["samples"] = result.Samples
		}
		if result.ErrorCode != "" {
			encoded["error_code"], encoded["error_message"] = result.ErrorCode, result.ErrorMessage
			failed++
		}
		items = append(items, encoded)
	}
	status := "available"
	if failed == len(results) {
		status = "unavailable"
	} else if failed > 0 {
		status = "limited"
	}
	return shared.ServerTestCategoryResult{
		Category: category, Status: status,
		Summary: map[string]any{"targets": len(results), "failed_targets": failed},
		Items:   items,
	}
}

// runConnectivityTarget measures the HTTPS latency to one site: a warmup
// request establishes TCP+TLS, then connectivityProbeAttempts measured
// requests on the reused connection produce the median. Any HTTP response
// counts as reachable; dial/TLS/timeout errors count as loss.
func runConnectivityTarget(parent context.Context, target shared.ServerTestTarget, client *http.Client) connectivityTargetResult {
	result := connectivityTargetResult{
		ID: target.ID, Label: target.Label, Group: target.Province, Host: target.Host,
		AddressFamily: string(target.AddressFamily), ProbeMethod: "https_get",
	}
	ctx, cancel := context.WithTimeout(parent, connectivityTargetTimeout)
	defer cancel()
	url := "https://" + net.JoinHostPort(target.Host, strconv.Itoa(target.Port)) + target.Path
	attempt := func() (float64, error) {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return 0, err
		}
		request.Header.Set("User-Agent", connectivityUserAgent)
		start := time.Now()
		response, err := client.Do(request)
		if err != nil {
			return 0, err
		}
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, connectivityDrainLimit))
		_ = response.Body.Close()
		return float64(time.Since(start).Microseconds()) / 1000, nil
	}
	_, _ = attempt() // warmup: pays TCP+TLS so samples measure the route, not the handshake
	samples := make([]float64, 0, connectivityProbeAttempts)
	for i := 0; i < connectivityProbeAttempts && ctx.Err() == nil; i++ {
		result.Sent++
		if latency, err := attempt(); err == nil {
			result.Received++
			samples = append(samples, latency)
		}
		if i < connectivityProbeAttempts-1 {
			select {
			case <-time.After(connectivityAttemptGap):
			case <-ctx.Done():
			}
		}
	}
	if len(samples) > 0 {
		median, min, max := medianMinMax(samples)
		result.RTTMedianMS, result.RTTMinMS, result.RTTMaxMS = &median, &min, &max
		result.Samples = samples
	}
	if result.Received == 0 {
		result.LossPercent = 100
		result.ErrorCode, result.ErrorMessage = "target_unreachable", "全部探测轮次失败（超时或连接被拒）"
	} else {
		result.LossPercent = float64(result.Sent-result.Received) / float64(result.Sent) * 100
	}
	return result
}

func interruptedConnectivityTarget(target shared.ServerTestTarget, cause error) connectivityTargetResult {
	message := "the target was not executed before the task stopped"
	if cause != nil {
		message += ": " + cause.Error()
	}
	return connectivityTargetResult{
		ID: target.ID, Label: target.Label, Group: target.Province, Host: target.Host,
		AddressFamily: string(target.AddressFamily), ProbeMethod: "https_get",
		LossPercent:  100,
		ErrorCode:    "task_interrupted",
		ErrorMessage: message,
	}
}

func newConnectivityClient(insecureSkipVerify bool) *http.Client {
	return &http.Client{
		// 直连测量：不使用环境代理，避免把代理链路误当成服务器自身连通性。
		Transport: &http.Transport{
			Proxy: nil,
			DialContext: (&net.Dialer{
				Timeout: connectivityRequestTimeout,
			}).DialContext,
			TLSClientConfig: &tls.Config{
				MinVersion:         tls.VersionTLS12,
				InsecureSkipVerify: insecureSkipVerify,
			},
			TLSHandshakeTimeout:   connectivityRequestTimeout,
			ResponseHeaderTimeout: connectivityRequestTimeout,
			MaxIdleConns:          1,
			MaxIdleConnsPerHost:   1,
			IdleConnTimeout:       connectivityTargetTimeout,
			ForceAttemptHTTP2:     true,
		},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		Timeout:       connectivityRequestTimeout,
	}
}

func medianMinMax(samples []float64) (median, min, max float64) {
	sorted := append([]float64(nil), samples...)
	sort.Float64s(sorted)
	min, max = sorted[0], sorted[len(sorted)-1]
	middle := len(sorted) / 2
	if len(sorted)%2 == 1 {
		return sorted[middle], min, max
	}
	return (sorted[middle-1] + sorted[middle]) / 2, min, max
}

package servertest

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"lattix/shared"
	"lattix/shared/requester"
)

const (
	speedProbeDuration = 15 * time.Second
	speedDownloadLimit = int64(768 << 20)
	speedUploadLimit   = int64(2 << 30)
)

type speedTargetResult struct {
	ID            string  `json:"id"`
	Label         string  `json:"label"`
	AddressFamily string  `json:"address_family"`
	Status        string  `json:"status"`
	DownloadMbps  float64 `json:"download_mbps,omitempty"`
	UploadMbps    float64 `json:"upload_mbps,omitempty"`
	DownloadBytes int64   `json:"download_bytes,omitempty"`
	UploadBytes   int64   `json:"upload_bytes,omitempty"`
	DownloadMS    int64   `json:"download_ms,omitempty"`
	UploadMS      int64   `json:"upload_ms,omitempty"`
	HTTPStatus    int     `json:"http_status,omitempty"`
	LatencyMS     float64 `json:"latency_ms,omitempty"`
	ResultURL     string  `json:"result_url,omitempty"`
	ErrorCode     string  `json:"error_code,omitempty"`
	ErrorMessage  string  `json:"error_message,omitempty"`
}

func (r *Runner) runSpeed(parent context.Context, category shared.ServerTestCategory, targets []shared.ServerTestTarget, update func(int, int, string)) shared.ServerTestCategoryResult {
	if len(targets) == 0 {
		return unsupportedResult(category, "catalog_targets_unavailable", "no speed targets were supplied")
	}
	ooklaBin, ooklaErr := "", error(nil)
	for _, target := range targets {
		if target.OoklaServerID != "" {
			ooklaBin, ooklaErr = EnsureOoklaCLI(parent, ooklaCLIFetcher(), filepath.Join(r.DataDir, "ookla"))
			break
		}
	}
	items := make([]map[string]any, 0, len(targets))
	available, failed, unavailable := 0, 0, 0
	for index, target := range targets {
		result := runSpeedTarget(parent, target, ooklaBin, ooklaErr)
		encoded := map[string]any{
			"id": result.ID, "label": result.Label, "address_family": result.AddressFamily,
			"status": result.Status,
		}
		if result.DownloadBytes > 0 {
			encoded["download_mbps"], encoded["download_bytes"], encoded["download_ms"] = result.DownloadMbps, result.DownloadBytes, result.DownloadMS
		}
		if result.UploadBytes > 0 {
			encoded["upload_mbps"], encoded["upload_bytes"], encoded["upload_ms"] = result.UploadMbps, result.UploadBytes, result.UploadMS
		}
		if result.HTTPStatus > 0 {
			encoded["http_status"] = result.HTTPStatus
		}
		if result.LatencyMS > 0 {
			encoded["latency_ms"] = result.LatencyMS
		}
		if result.ResultURL != "" {
			encoded["result_url"] = result.ResultURL
		}
		if result.ErrorCode != "" {
			encoded["error_code"], encoded["error_message"] = result.ErrorCode, result.ErrorMessage
		}
		switch result.Status {
		case "available", "limited":
			available++
		case "provider_access_unavailable":
			unavailable++
		default:
			failed++
		}
		items = append(items, encoded)
		update(index+1, len(targets), target.Label)
	}
	status := "available"
	if available == 0 {
		status = "unavailable"
	} else if failed > 0 || unavailable > 0 {
		status = "limited"
	}
	return shared.ServerTestCategoryResult{
		Category: category, Status: status,
		Summary: map[string]any{
			"targets": len(targets), "available_targets": available, "failed_targets": failed,
			"unavailable_targets": unavailable, "probe_seconds": int(speedProbeDuration.Seconds()),
			"apple_download_limit_bytes": speedDownloadLimit, "apple_upload_limit_bytes": speedUploadLimit,
		},
		Items: items,
	}
}

func runSpeedTarget(parent context.Context, target shared.ServerTestTarget, ooklaBin string, ooklaErr error) speedTargetResult {
	result := speedTargetResult{ID: target.ID, Label: target.Label, AddressFamily: string(target.AddressFamily)}
	if target.OoklaServerID != "" {
		return runOoklaSpeedTarget(parent, target, ooklaBin, ooklaErr)
	}
	if !strings.EqualFold(target.Host, "mensura.cdn-apple.com") || target.Path == "" || target.UploadPath == "" {
		result.Status = "provider_access_unavailable"
		result.ErrorCode = "provider_access_unavailable"
		result.ErrorMessage = "Volcengine TOS probing requires an authorized public access method"
		return result
	}
	if !validSpeedPath(target.Path) || !validSpeedPath(target.UploadPath) {
		result.Status, result.ErrorCode, result.ErrorMessage = "failed", "target_policy_rejected", "speed target path must be an absolute path without a query or fragment"
		return result
	}
	client := speedHTTPClient(target.AddressFamily)
	downloadURL := "https://" + target.Host + target.Path
	uploadURL := "https://" + target.Host + target.UploadPath
	result.DownloadMbps, result.DownloadBytes, result.DownloadMS, result.HTTPStatus, result.ErrorMessage = speedDownload(parent, client, downloadURL)
	uploadMbps, uploadBytes, uploadMS, _, uploadError := speedUpload(parent, client, uploadURL)
	result.UploadMbps, result.UploadBytes, result.UploadMS = uploadMbps, uploadBytes, uploadMS
	if uploadError != "" {
		if result.ErrorMessage != "" {
			result.ErrorMessage += "; "
		}
		result.ErrorMessage += uploadError
	}
	if result.DownloadBytes == 0 && result.UploadBytes == 0 {
		result.Status = "failed"
		result.ErrorCode = "speed_probe_failed"
		if result.ErrorMessage == "" {
			result.ErrorMessage = "Apple CDN transferred no data"
		}
		return result
	}
	result.Status = "available"
	if result.ErrorMessage != "" {
		result.Status = "limited"
		result.ErrorCode = "speed_probe_partial"
	}
	return result
}

// runOoklaSpeedTarget probes a speedtest.net server through the official CLI.
func runOoklaSpeedTarget(parent context.Context, target shared.ServerTestTarget, ooklaBin string, ooklaErr error) speedTargetResult {
	result := speedTargetResult{ID: target.ID, Label: target.Label, AddressFamily: string(target.AddressFamily)}
	if ooklaErr != nil {
		result.Status, result.ErrorCode = "failed", "ookla_cli_unavailable"
		result.ErrorMessage = ooklaErr.Error()
		return result
	}
	outcome, err := runOoklaServer(parent, ooklaBin, target.OoklaServerID)
	if err != nil {
		result.Status, result.ErrorCode, result.ErrorMessage = "failed", "speed_probe_failed", err.Error()
		return result
	}
	result.Status = "available"
	result.DownloadMbps, result.DownloadBytes, result.DownloadMS = outcome.DownloadMbps, outcome.DownloadBytes, outcome.DownloadMS
	result.UploadMbps, result.UploadBytes, result.UploadMS = outcome.UploadMbps, outcome.UploadBytes, outcome.UploadMS
	result.LatencyMS, result.ResultURL = outcome.LatencyMS, outcome.ResultURL
	return result
}

func speedHTTPClient(family shared.ServerTestAddressFamily) *http.Client {
	network := "tcp4"
	if family == shared.ServerTestIPv6 {
		network = "tcp6"
	}
	return requester.NewNetworkHTTPClient(requester.NetworkHTTPClientConfig{
		Network:               network,
		DialTimeout:           5 * time.Second,
		TLSMinVersion:         tls.VersionTLS12,
		TLSHandshakeTimeout:   5 * time.Second,
		ResponseHeaderTimeout: 10 * time.Second,
	})
}

func speedDownload(parent context.Context, client *http.Client, endpoint string) (float64, int64, int64, int, string) {
	ctx, cancel := context.WithTimeout(parent, speedProbeDuration)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return 0, 0, 0, 0, err.Error()
	}
	setAppleSpeedHeaders(request)
	started := time.Now()
	response, err := client.Do(request)
	if err != nil {
		return 0, 0, time.Since(started).Milliseconds(), 0, speedError(err)
	}
	bytesRead, readErr := io.Copy(io.Discard, io.LimitReader(response.Body, speedDownloadLimit))
	_ = response.Body.Close()
	duration := time.Since(started)
	message := ""
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		message = fmt.Sprintf("download returned HTTP %d", response.StatusCode)
	} else if readErr != nil && bytesRead == 0 {
		message = speedError(readErr)
	}
	return speedMbps(bytesRead, duration), bytesRead, duration.Milliseconds(), response.StatusCode, message
}

func speedUpload(parent context.Context, client *http.Client, endpoint string) (float64, int64, int64, int, string) {
	ctx, cancel := context.WithTimeout(parent, speedProbeDuration)
	defer cancel()
	reader := &countingReader{remaining: speedUploadLimit}
	request, err := http.NewRequestWithContext(ctx, http.MethodPut, endpoint, reader)
	if err != nil {
		return 0, 0, 0, 0, err.Error()
	}
	request.ContentLength = speedUploadLimit
	setAppleSpeedHeaders(request)
	request.Header.Set("Upload-Draft-Interop-Version", "6")
	request.Header.Set("Upload-Complete", "?1")
	started := time.Now()
	response, err := client.Do(request)
	duration := time.Since(started)
	bytesSent := reader.count.Load()
	if err != nil {
		if bytesSent > 0 && ctx.Err() != nil {
			return speedMbps(bytesSent, duration), bytesSent, duration.Milliseconds(), 0, ""
		}
		return 0, bytesSent, duration.Milliseconds(), 0, speedError(err)
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64<<10))
	_ = response.Body.Close()
	message := ""
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		message = fmt.Sprintf("upload returned HTTP %d", response.StatusCode)
	}
	return speedMbps(bytesSent, duration), bytesSent, duration.Milliseconds(), response.StatusCode, message
}

func setAppleSpeedHeaders(request *http.Request) {
	request.Header.Set("User-Agent", "networkQuality/194.80.3 CFNetwork/3860.400.51 Darwin/25.3.0")
	request.Header.Set("Accept", "*/*")
	request.Header.Set("Accept-Language", "zh-CN,zh-Hans;q=0.9")
	request.Header.Set("Accept-Encoding", "identity")
}

func validSpeedPath(path string) bool {
	return strings.HasPrefix(path, "/") && !strings.ContainsAny(path, "?#")
}

func speedMbps(bytes int64, duration time.Duration) float64 {
	if bytes <= 0 || duration <= 0 {
		return 0
	}
	return float64(bytes) * 8 / duration.Seconds() / 1_000_000
}

func speedError(err error) string {
	if err == nil {
		return ""
	}
	if strings.Contains(err.Error(), "context deadline exceeded") {
		return "probe deadline exceeded before any data was transferred"
	}
	return fmt.Sprintf("speed request failed: %s", err)
}

type countingReader struct {
	remaining int64
	count     atomic.Int64
}

func (r *countingReader) Read(buffer []byte) (int, error) {
	if r.remaining <= 0 {
		return 0, io.EOF
	}
	if int64(len(buffer)) > r.remaining {
		buffer = buffer[:r.remaining]
	}
	clear(buffer)
	n := len(buffer)
	r.remaining -= int64(n)
	r.count.Add(int64(n))
	return n, nil
}

var _ io.Reader = (*countingReader)(nil)

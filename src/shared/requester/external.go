// Package requester contains narrowly scoped clients for third-party HTTP calls.
// External services retain their own HTTP status and payload semantics; they do
// not use Lattix's internal RPC response envelope.
package requester

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
)

// HTTPDoer is the shared transport seam used by all external requesters.
type HTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}

// JSONRequester reads third-party JSON APIs.
type JSONRequester interface {
	GetJSON(context.Context, string, any) error
}

// WebhookRequester posts JSON to third-party webhook-style APIs.
type WebhookRequester interface {
	PostJSON(context.Context, string, any) error
}

// FileRequester downloads third-party files without applying Lattix RPC rules.
type FileRequester interface {
	GetText(context.Context, string, int64) (string, error)
	Download(context.Context, string, string, func(float64)) error
	DownloadLimited(context.Context, string, string, int64, func(float64)) error
}

type ExternalJSONRequester struct{ Doer HTTPDoer }

func (r ExternalJSONRequester) GetJSON(ctx context.Context, url string, dst any) error {
	resp, err := do(ctx, r.Doer, http.MethodGet, url, "", nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if err := require2xx(url, resp); err != nil {
		return err
	}
	if err := json.NewDecoder(resp.Body).Decode(dst); err != nil {
		return fmt.Errorf("%s: decode JSON: %w", redactedDestination(url), err)
	}
	return nil
}

// GitHubLatestReleaseTag resolves the latest release tag through the GitHub
// API: GET {apiRepos}/releases/latest, where apiRepos looks like
// https://api.github.com/repos/<org>/<repo>.
func GitHubLatestReleaseTag(ctx context.Context, doer HTTPDoer, apiRepos string) (string, error) {
	var rel struct {
		TagName string `json:"tag_name"`
	}
	if err := (ExternalJSONRequester{Doer: doer}).GetJSON(ctx, apiRepos+"/releases/latest", &rel); err != nil {
		return "", fmt.Errorf("解析 latest 失败: %w", err)
	}
	if rel.TagName == "" {
		return "", fmt.Errorf("解析 latest 失败: 无法读取 tag_name")
	}
	return rel.TagName, nil
}

type ExternalWebhookRequester struct{ Doer HTTPDoer }

func (r ExternalWebhookRequester) PostJSON(ctx context.Context, url string, value any) error {
	body, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode webhook payload: %w", err)
	}
	resp, err := do(ctx, r.Doer, http.MethodPost, url, "application/json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return require2xx(url, resp)
}

type ExternalFileRequester struct{ Doer HTTPDoer }

// FileRequestOptions 控制单次文件拉取的请求细节。
type FileRequestOptions struct {
	UserAgent string
}

// FileFetchResult 携带响应体与响应头，供需要读取头信息（如
// subscription-userinfo）的调用方使用。
type FileFetchResult struct {
	Body   string
	Header http.Header
}

// GetWithOptions 拉取文件并返回响应体与响应头。
func (r ExternalFileRequester) GetWithOptions(
	ctx context.Context, url string, maxBytes int64, opts FileRequestOptions,
) (FileFetchResult, error) {
	if r.Doer == nil {
		return FileFetchResult{}, fmt.Errorf("%s: external HTTP client is nil", redactedDestination(url))
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return FileFetchResult{}, wrapExternalURLError(url, "build request", err)
	}
	if opts.UserAgent != "" {
		req.Header.Set("User-Agent", opts.UserAgent)
	}
	resp, err := r.Doer.Do(req)
	if err != nil {
		return FileFetchResult{}, wrapExternalURLError(url, "request", err)
	}
	defer resp.Body.Close()
	if err := require2xx(url, resp); err != nil {
		return FileFetchResult{}, err
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		return FileFetchResult{}, fmt.Errorf("%s: read body: %w", redactedDestination(url), err)
	}
	if int64(len(body)) > maxBytes {
		return FileFetchResult{}, fmt.Errorf("%s: response exceeds %d bytes", redactedDestination(url), maxBytes)
	}
	return FileFetchResult{Body: string(body), Header: resp.Header.Clone()}, nil
}

// GetTextWithOptions 拉取文件文本，可携带自定义请求头选项。
func (r ExternalFileRequester) GetTextWithOptions(
	ctx context.Context, url string, maxBytes int64, opts FileRequestOptions,
) (string, error) {
	result, err := r.GetWithOptions(ctx, url, maxBytes, opts)
	if err != nil {
		return "", err
	}
	return result.Body, nil
}

// GetText 拉取文件文本。
func (r ExternalFileRequester) GetText(ctx context.Context, url string, maxBytes int64) (string, error) {
	return r.GetTextWithOptions(ctx, url, maxBytes, FileRequestOptions{})
}

// defaultDownloadLimit 是 Download 的默认流式大小上限（防御异常上游，评审 P3）。
const defaultDownloadLimit = int64(512 << 20)

func (r ExternalFileRequester) Download(
	ctx context.Context, url, path string, onProgress func(float64),
) error {
	return r.DownloadLimited(ctx, url, path, defaultDownloadLimit, onProgress)
}

// DownloadLimited 与 Download 相同，但流式写入超过 maxBytes 时中止并删除部分文件
// （防御恶意/异常上游在下载期间撑满磁盘，评审 P2/P3）。
func (r ExternalFileRequester) DownloadLimited(
	ctx context.Context, url, path string, maxBytes int64, onProgress func(float64),
) error {
	resp, err := do(ctx, r.Doer, http.MethodGet, url, "", nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if err := require2xx(url, resp); err != nil {
		return err
	}
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer file.Close()

	buffer := make([]byte, 64*1024)
	var downloaded int64
	for {
		n, readErr := resp.Body.Read(buffer)
		if n > 0 {
			if _, err := file.Write(buffer[:n]); err != nil {
				return err
			}
			downloaded += int64(n)
			if maxBytes > 0 && downloaded > maxBytes {
				file.Close()
				_ = os.Remove(path)
				return fmt.Errorf("%s: download exceeds %d bytes", redactedDestination(url), maxBytes)
			}
			if onProgress != nil && resp.ContentLength > 0 {
				onProgress(float64(downloaded) / float64(resp.ContentLength))
			}
		}
		if readErr == io.EOF {
			return nil
		}
		if readErr != nil {
			return readErr
		}
	}
}

func do(
	ctx context.Context, client HTTPDoer, method, url, contentType string, body io.Reader,
) (*http.Response, error) {
	if client == nil {
		return nil, fmt.Errorf("%s: external HTTP client is nil", redactedDestination(url))
	}
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return nil, wrapExternalURLError(url, "build request", err)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, wrapExternalURLError(url, "request", err)
	}
	return resp, nil
}

func require2xx(url string, resp *http.Response) error {
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("%s: HTTP %s", redactedDestination(url), resp.Status)
	}
	return nil
}

type redactedExternalError struct {
	message string
	cause   error
}

func (e *redactedExternalError) Error() string { return e.message }
func (e *redactedExternalError) Unwrap() error { return e.cause }

func wrapExternalURLError(rawURL, operation string, err error) error {
	destination := redactedDestination(rawURL)
	message := err.Error()
	for _, candidate := range equivalentURLStrings(rawURL) {
		if candidate == "" {
			continue
		}
		message = strings.ReplaceAll(message, candidate, destination)
	}
	return &redactedExternalError{
		message: fmt.Sprintf("%s: %s: %s", destination, operation, message),
		cause:   err,
	}
}

// redactedDestination intentionally retains only the origin. Webhook and bot
// endpoints commonly place credentials in userinfo, path segments, or query
// parameters, so retaining a "cleaned" path is not generally safe.
func redactedDestination(rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "[redacted URL]"
	}
	return parsed.Scheme + "://" + parsed.Host
}

func equivalentURLStrings(rawURL string) []string {
	values := []string{rawURL}
	if parsed, err := url.Parse(rawURL); err == nil {
		values = append(values, parsed.String(), parsed.Redacted())
	}
	return values
}

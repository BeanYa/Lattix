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
	"os"
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
		return fmt.Errorf("%s: decode JSON: %w", url, err)
	}
	return nil
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

func (r ExternalFileRequester) GetText(ctx context.Context, url string, maxBytes int64) (string, error) {
	resp, err := do(ctx, r.Doer, http.MethodGet, url, "", nil)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if err := require2xx(url, resp); err != nil {
		return "", err
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		return "", fmt.Errorf("%s: read body: %w", url, err)
	}
	if int64(len(body)) > maxBytes {
		return "", fmt.Errorf("%s: response exceeds %d bytes", url, maxBytes)
	}
	return string(body), nil
}

func (r ExternalFileRequester) Download(
	ctx context.Context, url, path string, onProgress func(float64),
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
		return nil, fmt.Errorf("%s: external HTTP client is nil", url)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return nil, fmt.Errorf("%s: build request: %w", url, err)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s: request: %w", url, err)
	}
	return resp, nil
}

func require2xx(url string, resp *http.Response) error {
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("%s: HTTP %s", url, resp.Status)
	}
	return nil
}

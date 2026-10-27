// Package cdn 是 zstatic CDN 节点目录的面板侧接线：定时拉取源站数据，
// 完整源解析校验通过后才落库（保留上一份可用目录），并记录刷新状态供 API 查询。
package cdn

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"lattix/backend/internal/cdncatalog"
	"lattix/backend/internal/store"
)

const (
	// RefreshIntervalDefault 是 CDN 目录的默认刷新周期（LATTIX_CDN_REFRESH_INTERVAL 可覆盖）。
	RefreshIntervalDefault = 6 * time.Hour
	// SourceURLEnv 支持以逗号分隔的源列表覆盖内置候选（LATTIX_CDN_SOURCE_URL）。
	SourceURLEnv = "LATTIX_CDN_SOURCE_URL"
)

type Catalog struct {
	st         *store.Store
	client     *http.Client
	clientErr  error
	sourceURLs []string
	now        func() time.Time
	mu         sync.Mutex
}

func New(st *store.Store) *Catalog {
	client, err := cdncatalog.NewHTTPClient(30 * time.Second)
	return &Catalog{
		st: st, client: client, clientErr: err,
		sourceURLs: SourceURLs(), now: time.Now,
	}
}

// SourceURLs 返回依序尝试的目录源：LATTIX_CDN_SOURCE_URL（逗号分隔）优先，
// 空值或全部无效时回退内置候选。
func SourceURLs() []string {
	if raw := os.Getenv(SourceURLEnv); raw != "" {
		urls := make([]string, 0, 2)
		for _, part := range strings.Split(raw, ",") {
			if trimmed := strings.TrimSpace(part); trimmed != "" {
				urls = append(urls, trimmed)
			}
		}
		if len(urls) > 0 {
			return urls
		}
	}
	return cdncatalog.DefaultSourceURLs
}

func (c *Catalog) primarySourceURL() string {
	if len(c.sourceURLs) > 0 {
		return c.sourceURLs[0]
	}
	return ""
}

type Status struct {
	Available     bool       `json:"available"`
	Refreshing    bool       `json:"refreshing"`
	SourceURL     string     `json:"source_url"`
	FetchedAt     *time.Time `json:"fetched_at,omitempty"`
	CatalogSHA256 string     `json:"catalog_sha256,omitempty"`
	LastAttemptAt time.Time  `json:"last_attempt_at"`
	LastError     string     `json:"last_error,omitempty"`
	LastErrorAt   *time.Time `json:"last_error_at,omitempty"`
}

// Refresh is the fixed panel refresh function used by the scheduler.
// It walks the configured sources in order and persists the first complete
// catalog; persistence happens only after the full source has parsed and
// validated, preserving the last good catalog when every source fails.
func (c *Catalog) Refresh(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now().UTC()
	if c.clientErr != nil {
		err := fmt.Errorf("initialize CDN catalog HTTP client: %w", c.clientErr)
		c.recordStatus(ctx, now, c.primarySourceURL(), err)
		return err
	}
	var (
		document  cdncatalog.Document
		sourceURL string
		attempts  []error
	)
	for _, candidate := range c.sourceURLs {
		if err := ctx.Err(); err != nil {
			attempts = append(attempts, err)
			break
		}
		fetched, err := cdncatalog.Fetch(ctx, c.client, candidate, now)
		if err == nil {
			document, sourceURL = fetched, candidate
			attempts = nil
			break
		}
		attempts = append(attempts, err)
	}
	if len(attempts) > 0 {
		err := joinAttemptErrors(attempts)
		c.recordStatus(ctx, now, c.primarySourceURL(), err)
		return err
	}
	encoded, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return fmt.Errorf("encode CDN catalog: %w", err)
	}
	if err := c.st.SetSetting(ctx, store.SettingCDNNodeCatalog, string(encoded)); err != nil {
		c.recordStatus(ctx, now, sourceURL, fmt.Errorf("save CDN catalog: %w", err))
		return fmt.Errorf("save CDN catalog: %w", err)
	}
	c.recordStatus(ctx, now, sourceURL, nil)
	return nil
}

func (c *Catalog) Status(ctx context.Context) (Status, error) {
	status := Status{SourceURL: c.primarySourceURL()}
	if raw, err := c.st.GetSetting(ctx, store.SettingCDNNodeCatalogStatus); err != nil {
		return status, err
	} else if raw != "" {
		if err := json.Unmarshal([]byte(raw), &status); err != nil {
			return status, fmt.Errorf("decode CDN catalog status: %w", err)
		}
	}
	raw, err := c.st.GetSetting(ctx, store.SettingCDNNodeCatalog)
	if err != nil {
		return status, fmt.Errorf("load CDN catalog: %w", err)
	}
	if raw == "" {
		status.Available = false
		return status, nil
	}
	var document cdncatalog.Document
	if err := json.Unmarshal([]byte(raw), &document); err != nil {
		status.Available = false
		status.LastError = "decode cached CDN catalog: " + err.Error()
		return status, nil
	}
	status.Available = document.Version == cdncatalog.SchemaVersion && len(document.Provinces) > 0
	status.FetchedAt = &document.Source.FetchedAt
	status.CatalogSHA256 = document.Source.CatalogSHA256
	return status, nil
}

func (c *Catalog) recordStatus(ctx context.Context, attemptedAt time.Time, sourceURL string, refreshErr error) {
	status, _ := c.Status(ctx)
	status.SourceURL = sourceURL
	status.LastAttemptAt = attemptedAt
	status.Refreshing = false
	if refreshErr != nil {
		status.LastError = refreshErr.Error()
		status.LastErrorAt = &attemptedAt
	} else {
		status.LastError = ""
		status.LastErrorAt = nil
		status.Available = true
	}
	encoded, err := json.Marshal(status)
	if err == nil {
		_ = c.st.SetSetting(ctx, store.SettingCDNNodeCatalogStatus, string(encoded))
	}
}

// joinAttemptErrors 把每个候选源的失败按尝试顺序拼成一行，便于状态页一次看全。
func joinAttemptErrors(errs []error) error {
	messages := make([]string, 0, len(errs))
	for _, err := range errs {
		messages = append(messages, err.Error())
	}
	return errors.New(strings.Join(messages, "; "))
}

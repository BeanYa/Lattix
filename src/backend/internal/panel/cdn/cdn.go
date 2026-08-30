// Package cdn 是 zstatic CDN 节点目录的面板侧接线：定时拉取源站数据，
// 完整源解析校验通过后才落库（保留上一份可用目录），并记录刷新状态供 API 查询。
package cdn

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"lattix/backend/internal/cdncatalog"
	"lattix/backend/internal/store"
)

const (
	// RefreshIntervalDefault 是 CDN 目录的默认刷新周期（LATTIX_CDN_REFRESH_INTERVAL 可覆盖）。
	RefreshIntervalDefault = 6 * time.Hour
)

type Catalog struct {
	st        *store.Store
	client    *http.Client
	clientErr error
	sourceURL string
	now       func() time.Time
	mu        sync.Mutex
}

func New(st *store.Store) *Catalog {
	client, err := cdncatalog.NewHTTPClient(30 * time.Second)
	return &Catalog{
		st: st, client: client, clientErr: err,
		sourceURL: cdncatalog.DefaultSourceURL, now: time.Now,
	}
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
// Persistence happens only after the complete source has parsed and
// validated, preserving the last good catalog on download or parse failure.
func (c *Catalog) Refresh(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now().UTC()
	if c.clientErr != nil {
		err := fmt.Errorf("initialize CDN catalog HTTP client: %w", c.clientErr)
		c.recordStatus(ctx, now, err)
		return err
	}
	document, err := cdncatalog.Fetch(ctx, c.client, c.sourceURL, now)
	if err != nil {
		c.recordStatus(ctx, now, err)
		return err
	}
	encoded, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return fmt.Errorf("encode CDN catalog: %w", err)
	}
	if err := c.st.SetSetting(ctx, store.SettingCDNNodeCatalog, string(encoded)); err != nil {
		c.recordStatus(ctx, now, err)
		return fmt.Errorf("save CDN catalog: %w", err)
	}
	c.recordStatus(ctx, now, nil)
	return nil
}

func (c *Catalog) Status(ctx context.Context) (Status, error) {
	status := Status{SourceURL: c.sourceURL}
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

func (c *Catalog) recordStatus(ctx context.Context, attemptedAt time.Time, refreshErr error) {
	status, _ := c.Status(ctx)
	status.SourceURL = c.sourceURL
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

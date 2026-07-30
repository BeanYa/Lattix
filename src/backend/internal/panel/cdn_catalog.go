package panel

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
	cdnCatalogRefreshIntervalDefault = 6 * time.Hour
)

type cdnCatalog struct {
	s         *Server
	client    *http.Client
	sourceURL string
	now       func() time.Time
	mu        sync.Mutex
}

func newCDNCatalog(s *Server) *cdnCatalog {
	return &cdnCatalog{
		s: s, client: &http.Client{Timeout: 30 * time.Second},
		sourceURL: cdncatalog.DefaultSourceURL, now: time.Now,
	}
}

type cdnCatalogStatus struct {
	Available     bool       `json:"available"`
	Refreshing    bool       `json:"refreshing"`
	SourceURL     string     `json:"source_url"`
	FetchedAt     *time.Time `json:"fetched_at,omitempty"`
	CatalogSHA256 string     `json:"catalog_sha256,omitempty"`
	LastAttemptAt time.Time  `json:"last_attempt_at"`
	LastError     string     `json:"last_error,omitempty"`
	LastErrorAt   *time.Time `json:"last_error_at,omitempty"`
}

// refreshZstaticCDNCatalog is the fixed panel refresh function used by the
// scheduler. Persistence happens only after the complete source has parsed and
// validated, preserving the last good catalog on download or parse failure.
func (c *cdnCatalog) refreshZstaticCDNCatalog(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now().UTC()
	document, err := cdncatalog.Fetch(ctx, c.client, c.sourceURL, now)
	if err != nil {
		c.recordStatus(ctx, now, err)
		return err
	}
	encoded, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return fmt.Errorf("encode CDN catalog: %w", err)
	}
	if err := c.s.st.SetSetting(ctx, store.SettingCDNNodeCatalog, string(encoded)); err != nil {
		c.recordStatus(ctx, now, err)
		return fmt.Errorf("save CDN catalog: %w", err)
	}
	c.recordStatus(ctx, now, nil)
	return nil
}

func (c *cdnCatalog) status(ctx context.Context) (cdnCatalogStatus, error) {
	status := cdnCatalogStatus{SourceURL: c.sourceURL}
	if raw, err := c.s.st.GetSetting(ctx, store.SettingCDNNodeCatalogStatus); err != nil {
		return status, err
	} else if raw != "" {
		if err := json.Unmarshal([]byte(raw), &status); err != nil {
			return status, fmt.Errorf("decode CDN catalog status: %w", err)
		}
	}
	raw, err := c.s.st.GetSetting(ctx, store.SettingCDNNodeCatalog)
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

func (c *cdnCatalog) recordStatus(ctx context.Context, attemptedAt time.Time, refreshErr error) {
	status, _ := c.status(ctx)
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
		_ = c.s.st.SetSetting(ctx, store.SettingCDNNodeCatalogStatus, string(encoded))
	}
}

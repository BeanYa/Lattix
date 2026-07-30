package panel

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"lattix/backend/internal/cdncatalog"
	"lattix/backend/internal/store"
)

const (
	cdnCatalogRefreshIntervalDefault = 6 * time.Hour
	cdnDNSInspectionIntervalDefault  = 24 * time.Hour
	cdnDNSInspectionIntervalMinimum  = time.Hour
)

type cdnCatalog struct {
	s         *Server
	client    *http.Client
	resolver  cdncatalog.Resolver
	sourceURL string
	now       func() time.Time
	mu        sync.Mutex
}

func newCDNCatalog(s *Server) *cdnCatalog {
	sourceURL := strings.TrimSpace(os.Getenv("LATX_ZSTATICCDN_SOURCE_URL"))
	if sourceURL == "" {
		sourceURL = cdncatalog.DefaultSourceURL
	}
	return &cdnCatalog{
		s: s, client: &http.Client{Timeout: 30 * time.Second}, resolver: net.DefaultResolver,
		sourceURL: sourceURL, now: time.Now,
	}
}

// refreshZstaticCDNCatalog is the fixed panel refresh function used by the
// scheduler. Persistence happens only after the complete source and every DNS
// address have been validated, preserving the last good catalog on failure.
func (c *cdnCatalog) refreshZstaticCDNCatalog(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	document, err := cdncatalog.Fetch(ctx, c.client, c.resolver, c.sourceURL, c.now())
	if err != nil {
		return err
	}
	encoded, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return fmt.Errorf("encode CDN catalog: %w", err)
	}
	if err := c.s.st.SetSetting(ctx, store.SettingCDNNodeCatalog, string(encoded)); err != nil {
		return fmt.Errorf("save CDN catalog: %w", err)
	}
	return nil
}

// inspectZstaticCDNDNS is intentionally separate from catalog refresh: it
// compares current DNS answers with the last successful stored snapshot.
func (c *cdnCatalog) inspectZstaticCDNDNS(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	raw, err := c.s.st.GetSetting(ctx, store.SettingCDNNodeCatalog)
	if err != nil {
		return fmt.Errorf("load CDN catalog: %w", err)
	}
	if raw == "" {
		return fmt.Errorf("CDN catalog has not been generated")
	}
	var document cdncatalog.Document
	if err := json.Unmarshal([]byte(raw), &document); err != nil {
		return fmt.Errorf("decode CDN catalog: %w", err)
	}
	mismatches, err := cdncatalog.CheckDNS(ctx, c.resolver, &document)
	if err != nil {
		return err
	}
	encoded, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return fmt.Errorf("encode inspected CDN catalog: %w", err)
	}
	if err := c.s.st.SetSetting(ctx, store.SettingCDNNodeCatalog, string(encoded)); err != nil {
		return fmt.Errorf("save inspected CDN catalog: %w", err)
	}
	if len(mismatches) == 0 {
		return nil
	}
	first := mismatches[0]
	detail := "resolved=" + strings.Join(first.ResolvedIPs, ",")
	if first.Error != "" {
		detail = "error=" + first.Error
	}
	return fmt.Errorf(
		"CDN DNS mismatch count=%d: %s %s %s target=%s stored=%s %s",
		len(mismatches), first.Province, first.ISP, first.Role, first.Target,
		first.ExpectedIP, detail,
	)
}

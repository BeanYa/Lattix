package cdn

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lattix/backend/internal/cdncatalog"
	"lattix/backend/internal/store"
)

const panelCDNSource = `window.nodeData = {
  provinceBaseData: [{province: "河北", carriers: {
    unicom: "he-cu-v4.ip.zstaticcdn.com:80",
    mobile: "he-cm-v4.ip.zstaticcdn.com:80",
    telecom: "he-ct-v4.ip.zstaticcdn.com:80",
  }}],
  cityKeyList: ["he-xiongan-ct-v4"],
  extraCityNodeMeta: {},
};`

func TestCDNCatalogRefreshPersistsWithoutDNSAndPreservesLastGoodOnFailure(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	fail := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if fail {
			http.Error(w, "upstream unavailable", http.StatusBadGateway)
			return
		}
		_, _ = w.Write([]byte(panelCDNSource))
	}))
	t.Cleanup(server.Close)
	now := time.Date(2026, time.July, 30, 13, 15, 25, 0, time.UTC)
	catalog := &Catalog{st: st, client: server.Client(), sourceURL: server.URL, now: func() time.Time { return now }}
	if err := catalog.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	raw, err := st.GetSetting(context.Background(), store.SettingCDNNodeCatalog)
	if err != nil {
		t.Fatal(err)
	}
	var document cdncatalog.Document
	if err := json.Unmarshal([]byte(raw), &document); err != nil {
		t.Fatal(err)
	}
	if len(document.Provinces) != 1 || document.Counts.ProvinceTargets != 9 {
		t.Fatalf("unexpected persisted catalog: %+v", document)
	}
	status, err := catalog.Status(context.Background())
	if err != nil || !status.Available || status.CatalogSHA256 == "" || status.LastError != "" {
		t.Fatalf("unexpected catalog status: status=%+v err=%v", status, err)
	}

	fail = true
	now = now.Add(time.Hour)
	if err := catalog.Refresh(context.Background()); err == nil || !strings.Contains(err.Error(), "HTTP 502") {
		t.Fatalf("refresh error = %v, want exact upstream failure", err)
	}
	afterFailure, err := st.GetSetting(context.Background(), store.SettingCDNNodeCatalog)
	if err != nil || afterFailure != raw {
		t.Fatalf("last good catalog changed: err=%v equal=%v", err, afterFailure == raw)
	}
	status, err = catalog.Status(context.Background())
	if err != nil || !status.Available || !strings.Contains(status.LastError, "HTTP 502") || status.LastErrorAt == nil {
		t.Fatalf("failure status: status=%+v err=%v", status, err)
	}
}

func TestCDNCatalogClientInitializationFailureIsReported(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	now := time.Date(2026, time.July, 31, 1, 2, 3, 0, time.UTC)
	catalog := &Catalog{
		st: st, clientErr: errors.New("invalid embedded root"),
		sourceURL: cdncatalog.DefaultSourceURL, now: func() time.Time { return now },
	}
	refreshErr := catalog.Refresh(context.Background())
	if refreshErr == nil || !strings.Contains(refreshErr.Error(), "invalid embedded root") {
		t.Fatalf("refresh error = %v", refreshErr)
	}
	status, err := catalog.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.Available || status.LastErrorAt == nil ||
		!strings.Contains(status.LastError, "initialize CDN catalog HTTP client") {
		t.Fatalf("unexpected catalog status: %+v", status)
	}
}

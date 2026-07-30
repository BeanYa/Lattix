package panel

import (
	"context"
	"encoding/json"
	"net"
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

type panelCDNResolver map[string][]net.IP

func (r panelCDNResolver) LookupIP(_ context.Context, _ string, host string) ([]net.IP, error) {
	addresses, ok := r[host]
	if !ok {
		return nil, &net.DNSError{Err: "not found", Name: host, IsNotFound: true}
	}
	return addresses, nil
}

func TestCDNCatalogRefreshPersistsAndInspectionDetectsMismatch(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(panelCDNSource))
	}))
	t.Cleanup(server.Close)
	resolver := panelCDNResolver{
		"he-ct-v4.ip.zstaticcdn.com":         {net.ParseIP("219.148.62.1")},
		"he-xiongan-ct-v4.ip.zstaticcdn.com": {net.ParseIP("144.7.111.241")},
		"he-cu-v4.ip.zstaticcdn.com":         {net.ParseIP("110.249.198.60")},
		"he-cm-v4.ip.zstaticcdn.com":         {net.ParseIP("111.62.113.1")},
	}
	panel := &Server{st: st}
	catalog := &cdnCatalog{
		s: panel, client: server.Client(), resolver: resolver, sourceURL: server.URL,
		now: func() time.Time { return time.Date(2026, time.July, 30, 13, 15, 25, 0, time.UTC) },
	}
	if err := catalog.refreshZstaticCDNCatalog(context.Background()); err != nil {
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
	if len(document.CDN) != 3 || len(document.Notes) != 3 {
		t.Fatalf("unexpected persisted catalog: %+v", document)
	}
	if err := catalog.inspectZstaticCDNDNS(context.Background()); err != nil {
		t.Fatalf("unchanged DNS inspection failed: %v", err)
	}
	resolver["he-xiongan-ct-v4.ip.zstaticcdn.com"] = []net.IP{net.ParseIP("144.7.111.242")}
	if err := catalog.inspectZstaticCDNDNS(context.Background()); err == nil || !strings.Contains(err.Error(), "backup") {
		t.Fatalf("inspection error = %v, want backup mismatch", err)
	}
	raw, err = st.GetSetting(context.Background(), store.SettingCDNNodeCatalog)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(raw), &document); err != nil {
		t.Fatal(err)
	}
	if document.CDN[0].Status != cdncatalog.StatusNormal || document.CDN[0].Backup.Status != cdncatalog.StatusFailed {
		t.Fatalf("inspection did not persist status-only result: %+v", document.CDN[0])
	}
	if document.CDN[0].IP != "219.148.62.1" || document.CDN[0].Backup.IP != "144.7.111.241" ||
		document.GeneratedAt != time.Date(2026, time.July, 30, 13, 15, 25, 0, time.UTC) {
		t.Fatalf("inspection changed immutable snapshot data: %+v", document)
	}
}

func TestCoreTasksRegisterCDNRefreshAndDNSInspectionSeparately(t *testing.T) {
	t.Setenv("LATTIX_CDN_DNS_INSPECTION_INTERVAL", "5m")
	panel := &Server{
		releases:  &releaseCatalog{},
		exchange:  &exchangeCatalog{},
		cdn:       &cdnCatalog{},
		scheduler: newTaskScheduler(func(context.Context) *time.Location { return time.UTC }),
	}
	panel.registerCoreTasks()
	tasks := panel.scheduler.snapshot()
	refresh, found := tasks["cdn.catalog.refresh"]
	if !found || !refresh.runOnStart {
		t.Fatalf("catalog refresh task = %+v, found=%v", refresh, found)
	}
	inspection, found := tasks["cdn.dns.inspect"]
	if !found || inspection.runOnStart {
		t.Fatalf("DNS inspection task = %+v, found=%v", inspection, found)
	}
	now := time.Date(2026, time.July, 30, 0, 0, 0, 0, time.UTC)
	if got := inspection.trigger(context.Background()).next(now, time.UTC); !got.Equal(now.Add(24 * time.Hour)) {
		t.Fatalf("next DNS inspection = %s, want minimum-safe default %s", got, now.Add(24*time.Hour))
	}
}

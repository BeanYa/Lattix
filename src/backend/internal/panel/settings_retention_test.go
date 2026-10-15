package panel

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"lattix/backend/internal/store"
)

// TestSettingsTrafficStatsRetentionDays 验证流量统计保留天数设置项端到端：
// 未设置时 GET 回落默认 60；合法值（90）持久化后回读一致；越界值（30/3651）被 400 拒绝。
func TestSettingsTrafficStatsRetentionDays(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	serverAPI := &Server{st: st}

	getRetention := func() int {
		t.Helper()
		rec := httptest.NewRecorder()
		serverAPI.handleGetSettings(rec, httptest.NewRequest(http.MethodGet, "/api/setting/get", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("get settings status = %d, body = %s", rec.Code, rec.Body.String())
		}
		var dto struct {
			TrafficStatsRetentionDays int `json:"traffic_stats_retention_days"`
		}
		if err := json.Unmarshal(decodeRPC(t, rec).Data, &dto); err != nil {
			t.Fatal(err)
		}
		return dto.TrafficStatsRetentionDays
	}
	update := func(body string) *httptest.ResponseRecorder {
		t.Helper()
		rec := httptest.NewRecorder()
		serverAPI.handleUpdateSettings(rec, httptest.NewRequest(http.MethodPost, "/api/setting/update",
			strings.NewReader(body)))
		return rec
	}

	// 未设置：GET 填充默认 60。
	if got := getRetention(); got != store.DefaultTrafficStatsRetentionDays {
		t.Fatalf("default retention days = %d, want %d", got, store.DefaultTrafficStatsRetentionDays)
	}

	// 合法值 90：持久化并回读 90。
	rec := update(`{"traffic_stats_retention_days":90}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("update status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if raw, _ := st.GetSetting(ctx, store.SettingTrafficStatsRetentionDays); raw != "90" {
		t.Fatalf("stored setting = %q, want 90", raw)
	}
	if got := getRetention(); got != 90 {
		t.Fatalf("retention days after update = %d, want 90", got)
	}

	// 越界拒绝：低于 31 与超过 3650（writeError 映射 400 → INVALID_ARGUMENT 信封码）。
	for _, body := range []string{
		`{"traffic_stats_retention_days":30}`,
		`{"traffic_stats_retention_days":3651}`,
	} {
		rec := update(body)
		if env := decodeRPC(t, rec); env.Code != "INVALID_ARGUMENT" || env.Message != "流量统计保留天数须为 31-3650 天" {
			t.Fatalf("out-of-range update (%s) code = %q message = %q, body = %s", body, env.Code, env.Message, rec.Body.String())
		}
	}

	// 0（字段缺省）回落默认 60 而非报错（与 operation_log_limit 同语义）。
	rec = update(`{"traffic_stats_retention_days":0}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("zero update status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if got := getRetention(); got != store.DefaultTrafficStatsRetentionDays {
		t.Fatalf("retention days after zero = %d, want %d", got, store.DefaultTrafficStatsRetentionDays)
	}
}

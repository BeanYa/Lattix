package panel

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"lattix/backend/internal/dispatch"
	"lattix/backend/internal/store"
	"lattix/shared"
)

type settingsRequester struct {
	online map[int64]bool
}

func (f *settingsRequester) Send(context.Context, int64, shared.Envelope) error { return nil }
func (f *settingsRequester) IsOnline(serverID int64) bool                       { return f.online[serverID] }

// rpcEnvelope 是面板响应信封的通用解码结构。
type rpcEnvelope struct {
	Code    string          `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

func decodeRPC(t *testing.T, rec *httptest.ResponseRecorder) rpcEnvelope {
	t.Helper()
	var env rpcEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode rpc envelope: %v, body = %s", err, rec.Body.String())
	}
	return env
}

func TestSettingsServerSettingsRoundTrip(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	requester := &settingsRequester{online: map[int64]bool{}}
	serverAPI := &Server{st: st, disp: dispatch.New(st, requester), req: requester}

	// 初始默认 latest（revision 1）。
	rec := httptest.NewRecorder()
	serverAPI.handleGetSettings(rec, httptest.NewRequest(http.MethodGet, "/api/setting/get", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("get settings status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var dto struct {
		ServerSettings         shared.ServerSettings `json:"server_settings"`
		ServerSettingsRevision int64                 `json:"server_settings_revision"`
	}
	if err := json.Unmarshal(decodeRPC(t, rec).Data, &dto); err != nil {
		t.Fatal(err)
	}
	if dto.ServerSettingsRevision != 1 || dto.ServerSettings.XrayVersion == nil || *dto.ServerSettings.XrayVersion != "latest" {
		t.Fatalf("default = %+v rev=%d", dto.ServerSettings, dto.ServerSettingsRevision)
	}

	// 保存默认覆盖值 → revision+1。
	rec = httptest.NewRecorder()
	serverAPI.handleUpdateSettings(rec, httptest.NewRequest(http.MethodPost, "/api/setting/update",
		strings.NewReader(`{"server_settings":{"xray_version":"v1.8.24"}}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("update settings status = %d, body = %s", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	serverAPI.handleGetSettings(rec, httptest.NewRequest(http.MethodGet, "/api/setting/get", nil))
	if err := json.Unmarshal(decodeRPC(t, rec).Data, &dto); err != nil {
		t.Fatal(err)
	}
	if dto.ServerSettingsRevision != 2 || dto.ServerSettings.XrayVersion == nil || *dto.ServerSettings.XrayVersion != "v1.8.24" {
		t.Fatalf("after update = %+v rev=%d", dto.ServerSettings, dto.ServerSettingsRevision)
	}

	// 非法版本被拒绝。
	rec = httptest.NewRecorder()
	serverAPI.handleUpdateSettings(rec, httptest.NewRequest(http.MethodPost, "/api/setting/update",
		strings.NewReader(`{"server_settings":{"xray_version":"nope"}}`)))
	if env := decodeRPC(t, rec); env.Code != "INVALID_ARGUMENT" {
		t.Fatalf("invalid version code = %s, body = %s", env.Code, rec.Body.String())
	}
	_ = ctx
}

func TestServerCustomSettingsOverrideLifecycle(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	requester := &settingsRequester{online: map[int64]bool{}}
	serverAPI := &Server{st: st, disp: dispatch.New(st, requester), req: requester}
	serverID, err := st.CreateServer(ctx, "s1", "", "tok", store.MachineTypeDirect, "", "", "US", "Test")
	if err != nil {
		t.Fatal(err)
	}
	update := func(body string) *httptest.ResponseRecorder {
		t.Helper()
		rec := httptest.NewRecorder()
		serverAPI.handleUpdateServer(rec, httptest.NewRequest(http.MethodPost, "/api/server/update", strings.NewReader(body)))
		return rec
	}
	list := func() []serverDTO {
		t.Helper()
		rec := httptest.NewRecorder()
		serverAPI.handleListServers(rec, httptest.NewRequest(http.MethodGet, "/api/server/list", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("list status = %d, body = %s", rec.Code, rec.Body.String())
		}
		var servers []serverDTO
		if err := json.Unmarshal(decodeRPC(t, rec).Data, &servers); err != nil {
			t.Fatal(err)
		}
		return servers
	}
	find := func(servers []serverDTO) serverDTO {
		for _, item := range servers {
			if item.ID == serverID {
				return item
			}
		}
		t.Fatal("server not in list")
		return serverDTO{}
	}

	// 无覆盖：custom_settings nil，effective = 默认 latest。
	update(fmt.Sprintf(`{"server_id":%d,"alias":"s1","address":""}`, serverID))
	dto := find(list())
	if dto.CustomSettings != nil {
		t.Fatalf("custom_settings = %+v, want nil", dto.CustomSettings)
	}
	if dto.EffectiveXrayVersion != "latest" {
		t.Fatalf("effective_xray_version = %q, want latest", dto.EffectiveXrayVersion)
	}

	// 设置覆盖 v1.8.10。
	update(fmt.Sprintf(`{"server_id":%d,"alias":"s1","address":"","custom_settings":{"xray_version":"v1.8.10"}}`, serverID))
	dto = find(list())
	if dto.CustomSettings == nil || dto.CustomSettings.XrayVersion == nil || *dto.CustomSettings.XrayVersion != "v1.8.10" {
		t.Fatalf("custom_settings = %+v", dto.CustomSettings)
	}
	if dto.EffectiveXrayVersion != "v1.8.10" {
		t.Fatalf("effective_xray_version = %q, want v1.8.10", dto.EffectiveXrayVersion)
	}

	// 清除覆盖（{}）回退默认。
	update(fmt.Sprintf(`{"server_id":%d,"alias":"s1","address":"","custom_settings":{}}`, serverID))
	dto = find(list())
	if dto.CustomSettings != nil {
		t.Fatalf("custom_settings after clear = %+v, want nil", dto.CustomSettings)
	}
	if dto.EffectiveXrayVersion != "latest" {
		t.Fatalf("effective_xray_version after clear = %q, want latest", dto.EffectiveXrayVersion)
	}

	// 非法版本拒绝。
	rec := update(fmt.Sprintf(`{"server_id":%d,"alias":"s1","address":"","custom_settings":{"xray_version":"bad"}}`, serverID))
	if env := decodeRPC(t, rec); env.Code != "INVALID_ARGUMENT" {
		t.Fatalf("invalid version code = %s, body = %s", env.Code, rec.Body.String())
	}
}

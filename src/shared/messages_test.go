package shared

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestEnvelopeMarshalSeparatesRequestAndResponseFields(t *testing.T) {
	id := "0123456789abcdef0123456789abcdef"
	request, err := json.Marshal(Envelope{
		Kind: KindRequest, Type: TypeApplyNode, RequestID: id, TraceID: id,
		Data: json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(request), `"code"`) || strings.Contains(string(request), `"message"`) {
		t.Fatalf("request contains response-only fields: %s", request)
	}

	response, err := json.Marshal(Envelope{
		Kind: KindResponse, Type: TypeApplyNode, RequestID: id, TraceID: id,
		Code: CodeOK, Message: "", Data: json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(response), `"code":"OK"`) ||
		!strings.Contains(string(response), `"message":""`) {
		t.Fatalf("response omitted required fields: %s", response)
	}
}

func TestEnvelopeValidate(t *testing.T) {
	id := "0123456789abcdef0123456789abcdef"
	valid := Envelope{
		Kind: KindEvent, Type: TypeTelemetry, RequestID: id, TraceID: id,
		Data: json.RawMessage(`{"xray_running":true}`),
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid envelope rejected: %v", err)
	}
	valid.RequestID = "not-an-id"
	if err := valid.Validate(); err == nil {
		t.Fatal("invalid request ID accepted")
	}
}

func TestTelemetryPayloadOmitsEmptyOnlineUsers(t *testing.T) {
	payload := TelemetryPayload{XrayVersion: "v1.8.0", XrayRunning: true}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "online_users") {
		t.Fatalf("empty online_users should be omitted by omitempty: %s", raw)
	}
}

func TestTelemetryPayloadMarshalsOnlineUsers(t *testing.T) {
	payload := TelemetryPayload{
		XrayVersion: "v1.8.0",
		XrayRunning: true,
		OnlineUsers: []OnlineUserStat{
			{User: "11111111-2222-3333-4444-555555555555", IPs: []string{"1.2.3.4", "5.6.7.8"}},
			{User: "66666666-7777-8888-9999-000000000000", IPs: []string{"9.9.9.9"}},
		},
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	want := `"online_users":[{"user":"11111111-2222-3333-4444-555555555555","ips":["1.2.3.4","5.6.7.8"]},{"user":"66666666-7777-8888-9999-000000000000","ips":["9.9.9.9"]}]`
	if !strings.Contains(string(raw), want) {
		t.Fatalf("online_users marshalled incorrectly, want substring %s, got %s", want, raw)
	}

	var decoded TelemetryPayload
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded.OnlineUsers) != 2 ||
		decoded.OnlineUsers[0].User != "11111111-2222-3333-4444-555555555555" ||
		len(decoded.OnlineUsers[0].IPs) != 2 {
		t.Fatalf("online_users roundtrip mismatch: %+v", decoded.OnlineUsers)
	}
}

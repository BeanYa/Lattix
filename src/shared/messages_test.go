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

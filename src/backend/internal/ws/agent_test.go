package ws

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"lattix/shared"
)

// clientIP（§9）：直连取 RemoteAddr 的 host；受信回环代理取 XFF 首个 IP；
// 非回环对端不信任 XFF（防伪造）。
func TestClientIP(t *testing.T) {
	cases := []struct {
		name       string
		remoteAddr string
		xff        string
		want       string
	}{
		{"直连取 RemoteAddr host", "1.2.3.4:5678", "", "1.2.3.4"},
		{"回环代理取 XFF 首 IP", "127.0.0.1:8080", "9.9.9.9, 10.0.0.1", "9.9.9.9"},
		{"回环代理 XFF 含空格", "127.0.0.1:8080", " 9.9.9.9 , 10.0.0.1", "9.9.9.9"},
		{"回环代理无 XFF 回退回环", "127.0.0.1:8080", "", "127.0.0.1"},
		{"IPv6 回环代理取 XFF", "[::1]:8080", "9.9.9.9", "9.9.9.9"},
		{"非回环对端不信任 XFF", "1.2.3.4:5678", "9.9.9.9", "1.2.3.4"},
		{"无端口的 RemoteAddr", "1.2.3.4", "", "1.2.3.4"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := &http.Request{RemoteAddr: c.remoteAddr, Header: http.Header{}}
			if c.xff != "" {
				r.Header.Set("X-Forwarded-For", c.xff)
			}
			if got := clientIP(r); got != c.want {
				t.Errorf("clientIP(%q, xff=%q) = %q, want %q", c.remoteAddr, c.xff, got, c.want)
			}
		})
	}
}

func TestHandshakeAuthenticationErrorIsStructuredRPCResponse(t *testing.T) {
	recorder := httptest.NewRecorder()
	writeHandshakeError(recorder, http.StatusForbidden, shared.CodeAuthInvalidCredentials, "authentication failed")
	response := recorder.Result()
	defer response.Body.Close()
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d", response.StatusCode)
	}
	if got := response.Header.Get(protocolHeader); got != protocolVersion {
		t.Fatalf("protocol header = %q", got)
	}
	var envelope shared.Envelope
	if err := json.NewDecoder(response.Body).Decode(&envelope); err != nil {
		t.Fatal(err)
	}
	if err := envelope.Validate(); err != nil {
		t.Fatalf("invalid envelope: %v", err)
	}
	if envelope.Kind != shared.KindResponse || envelope.Type != shared.TypeSessionOpen ||
		envelope.Code != shared.CodeAuthInvalidCredentials {
		t.Fatalf("unexpected envelope: %#v", envelope)
	}
}

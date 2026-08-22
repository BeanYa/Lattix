package requester

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestNewNetworkHTTPClient(t *testing.T) {
	client := NewNetworkHTTPClient(NetworkHTTPClientConfig{
		Network:               "tcp4",
		DialTimeout:           5 * time.Second,
		TLSMinVersion:         tls.VersionTLS12,
		TLSHandshakeTimeout:   5 * time.Second,
		ResponseHeaderTimeout: 10 * time.Second,
	})
	transport, ok := client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("transport = %T, want *http.Transport", client.Transport)
	}
	if transport.DialContext == nil || transport.Proxy == nil {
		t.Error("transport must keep pinned DialContext and ProxyFromEnvironment")
	}
	if transport.TLSClientConfig == nil || transport.TLSClientConfig.MinVersion != tls.VersionTLS12 {
		t.Errorf("TLSClientConfig = %+v, want MinVersion TLS 1.2", transport.TLSClientConfig)
	}
	if transport.TLSHandshakeTimeout != 5*time.Second ||
		transport.ResponseHeaderTimeout != 10*time.Second {
		t.Errorf("timeouts = %v/%v, want 5s/10s",
			transport.TLSHandshakeTimeout, transport.ResponseHeaderTimeout)
	}
	if !transport.DisableKeepAlives {
		t.Error("DisableKeepAlives = false, want true")
	}
}

// 拨号必须固定在配置的地址族：tcp4 客户端可连通 IPv4 监听者，tcp6 不能。
func TestNetworkHTTPClientPinsAddressFamily(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	config := NetworkHTTPClientConfig{Network: "tcp4", DialTimeout: 2 * time.Second}
	resp, err := NewNetworkHTTPClient(config).Get(server.URL)
	if err != nil {
		t.Fatalf("tcp4 GET = %v, want success", err)
	}
	_ = resp.Body.Close()

	config.Network = "tcp6"
	if _, err := NewNetworkHTTPClient(config).Get(server.URL); err == nil {
		t.Fatal("tcp6 GET = nil, want dial failure against IPv4 listener")
	}
}

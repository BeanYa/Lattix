package requester

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"time"
)

// NetworkHTTPClientConfig 描述按固定地址族拨号的 HTTP 客户端配置。
type NetworkHTTPClientConfig struct {
	// Network 固定拨号网络（如 tcp4 / tcp6），用于按地址族探测。
	Network string
	// DialTimeout 是单次拨号超时。
	DialTimeout time.Duration
	// TLSMinVersion 是 TLS 最低版本（0 表示沿用 Go 默认值）。
	TLSMinVersion uint16
	// TLSHandshakeTimeout 是 TLS 握手超时。
	TLSHandshakeTimeout time.Duration
	// ResponseHeaderTimeout 是等待响应头的超时。
	ResponseHeaderTimeout time.Duration
}

// NewNetworkHTTPClient 构造按固定地址族拨号的一次性探测客户端：
// 走环境代理，不复用连接（DisableKeepAlives 且关闭 TCP keepalive 探测）。
// 请求的整体超时与取消由调用方通过 context 控制。
func NewNetworkHTTPClient(config NetworkHTTPClientConfig) *http.Client {
	dialer := &net.Dialer{Timeout: config.DialTimeout, KeepAlive: -1}
	return &http.Client{Transport: &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: func(ctx context.Context, _, address string) (net.Conn, error) {
			return dialer.DialContext(ctx, config.Network, address)
		},
		TLSClientConfig:       &tls.Config{MinVersion: config.TLSMinVersion},
		TLSHandshakeTimeout:   config.TLSHandshakeTimeout,
		ResponseHeaderTimeout: config.ResponseHeaderTimeout,
		DisableKeepAlives:     true,
	}}
}

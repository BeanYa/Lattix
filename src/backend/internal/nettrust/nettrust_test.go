package nettrust

import (
	"net/http"
	"testing"
)

func request(remote string, headers map[string]string) *http.Request {
	r, err := http.NewRequest(http.MethodGet, "/api/agent/ws", nil)
	if err != nil {
		panic(err)
	}
	r.RemoteAddr = remote
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	return r
}

func TestValidate(t *testing.T) {
	valid := []string{
		"",
		"172.17.0.0/16",
		"10.0.0.0/8, 192.168.1.1", // 混合 CIDR 与裸 IP
		"fd00::/8",
		"  172.16.0.0/12 ,\t10.0.0.1  ", // 分隔符容错
	}
	for _, raw := range valid {
		if err := Validate(raw); err != nil {
			t.Errorf("Validate(%q) = %v, want nil", raw, err)
		}
	}
	invalid := []string{
		"172.17.0.0/33",
		"not-an-ip",
		"example.com/24",
		"10.0.0.0/8, nope",
	}
	for _, raw := range invalid {
		if err := Validate(raw); err == nil {
			t.Errorf("Validate(%q) = nil, want error", raw)
		}
	}
}

func TestTrustedPeer(t *testing.T) {
	trust := &Trust{}
	if err := trust.Configure("172.17.0.0/16"); err != nil {
		t.Fatal(err)
	}
	cases := map[string]bool{
		"127.0.0.1:1234":  true,  // 回环恒可信
		"[::1]:443":       true,  // IPv6 回环
		"172.17.0.5:9000": true,  // 配置网段（docker 网桥）
		"172.18.0.5:9000": false, // 网段外
		"203.0.113.9:80":  false, // 公网直连不可信
		"localhost:1234":  false, // 非 IP 对端
	}
	for remote, want := range cases {
		if got := trust.TrustedPeer(request(remote, nil)); got != want {
			t.Errorf("TrustedPeer(%s) = %v, want %v", remote, got, want)
		}
	}
}

func TestForwardedHTTPS(t *testing.T) {
	trust := &Trust{}
	if err := trust.Configure("172.17.0.0/16"); err != nil {
		t.Fatal(err)
	}
	https := map[string]string{"X-Forwarded-Proto": "https"}
	cases := []struct {
		remote string
		header map[string]string
		want   bool
	}{
		{"127.0.0.1:1", https, true},  // 本机反代声明
		{"172.17.0.5:1", https, true}, // 配置网段反代声明（1panel/openresty 容器）
		{"172.17.0.5:1", nil, false},  // 可信对端但未声明
		{"172.17.0.5:1", map[string]string{"X-Forwarded-Proto": "http"}, false},
		{"172.17.0.5:1", map[string]string{"X-Forwarded-Proto": "https,http"}, true}, // 多值取首
		{"203.0.113.9:1", https, false},                                              // 公网直连伪造声明不采信
	}
	for _, c := range cases {
		if got := trust.ForwardedHTTPS(request(c.remote, c.header)); got != c.want {
			t.Errorf("ForwardedHTTPS(%s, %v) = %v, want %v", c.remote, c.header, got, c.want)
		}
	}
}

func TestClientIPWalksRightToLeft(t *testing.T) {
	trust := &Trust{}
	if err := trust.Configure("172.17.0.0/16"); err != nil {
		t.Fatal(err)
	}
	// 代理追加语义：客户端伪造左侧条目，真实地址由可信代理追加在右侧。
	r := request("172.17.0.5:9000", map[string]string{
		"X-Forwarded-For": "198.51.100.7, 203.0.113.9",
	})
	if got := trust.ClientIP(r); got != "203.0.113.9" {
		t.Errorf("ClientIP = %q, want 203.0.113.9（最右侧非可信地址）", got)
	}
	// 链条全为可信代理时回落对端地址。
	r = request("127.0.0.1:9000", map[string]string{
		"X-Forwarded-For": "127.0.0.2, 172.17.0.9",
	})
	if got := trust.ClientIP(r); got != "127.0.0.1" {
		t.Errorf("ClientIP = %q, want 127.0.0.1（全链可信回落对端）", got)
	}
	// 不可信对端忽略 XFF。
	r = request("203.0.113.9:80", map[string]string{
		"X-Forwarded-For": "198.51.100.7",
	})
	if got := trust.ClientIP(r); got != "203.0.113.9" {
		t.Errorf("ClientIP = %q, want 203.0.113.9（不可信对端返回自身）", got)
	}
	// 无 XFF 返回对端。
	if got := trust.ClientIP(request("172.17.0.5:9000", nil)); got != "172.17.0.5" {
		t.Errorf("ClientIP = %q, want 172.17.0.5", got)
	}
	// 链条含非 IP 条目：右侧合法 IP 即代理亲见的客户端地址，左侧伪造条目不采信。
	r = request("172.17.0.5:9000", map[string]string{
		"X-Forwarded-For": "evil.example, 203.0.113.9",
	})
	if got := trust.ClientIP(r); got != "203.0.113.9" {
		t.Errorf("ClientIP = %q, want 203.0.113.9（最右侧合法 IP 优先）", got)
	}
	// 最右侧本身非 IP：代理写入的值不可解析，回落对端地址。
	r = request("172.17.0.5:9000", map[string]string{
		"X-Forwarded-For": "203.0.113.9, evil.example",
	})
	if got := trust.ClientIP(r); got != "172.17.0.5" {
		t.Errorf("ClientIP = %q, want 172.17.0.5（最右侧不可解析回落对端）", got)
	}
}

func TestConfigureAtomicFallback(t *testing.T) {
	trust := &Trust{}
	if err := trust.Configure("10.0.0.0/8"); err != nil {
		t.Fatal(err)
	}
	// 无效配置被拒绝且不影响已生效配置。
	if err := trust.Configure("bogus"); err == nil {
		t.Fatal("Configure(bogus) = nil, want error")
	}
	if !trust.TrustedPeer(request("10.1.2.3:1", nil)) {
		t.Error("previous configuration must survive a rejected Configure")
	}
}

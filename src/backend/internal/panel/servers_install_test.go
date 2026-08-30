package panel

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"lattix/backend/internal/store"
)

func TestInstallCommandUsesUnifiedGitHubEntrypoint(t *testing.T) {
	server := &Server{cfg: Config{GitHubRepo: "BeanYa/Lattix", Version: "v1.2.3"}}
	command := server.installCommand("https://panel.example.com", "bootstrap")
	for _, want := range []string{
		"curl -fsSL https://raw.githubusercontent.com/BeanYa/Lattix/main/install.sh",
		"| bash -s -- agent --version v1.2.3",
		"--panel https://panel.example.com",
		"--token bootstrap",
	} {
		if !strings.Contains(command, want) {
			t.Fatalf("install command %q does not contain %q", command, want)
		}
	}
	if strings.Contains(command, "--xray-version") {
		t.Fatalf("install command unexpectedly pins an xray version: %q", command)
	}
}

func TestDevInstallCommandDefaultsToLatest(t *testing.T) {
	server := &Server{cfg: Config{GitHubRepo: "BeanYa/Lattix", Version: "dev"}}
	command := server.installCommand("http://127.0.0.1:8080", "bootstrap")
	if strings.Contains(command, "--version") {
		t.Fatalf("dev install command unexpectedly pins a version: %q", command)
	}
}

// TestPanelBaseInfersSchemeFromContainerProxy 复现 1panel 部署形态：面板以 HTTP
// 容器运行，同宿主机 openresty 反代终止 TLS。反代对端落在 docker 桥接网段，
// 其 X-Forwarded-Proto 声明应被采纳，安装命令/订阅链接据此推断 https。
// 直连（无声明或公网对端伪造声明）仍推断 http。
func TestPanelBaseInfersSchemeFromContainerProxy(t *testing.T) {
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	server := &Server{st: st, cfg: Config{GitHubRepo: "BeanYa/Lattix", Version: "v0.0.56"}}

	newRequest := func(remote string, header map[string]string) *http.Request {
		r := httptest.NewRequest(http.MethodGet, "http://lattix.example.com/api/servers/install", nil)
		r.RemoteAddr = remote
		r.Host = "lattix.example.com"
		for k, v := range header {
			r.Header.Set(k, v)
		}
		return r
	}

	proxied := newRequest("172.18.0.3:52318", map[string]string{"X-Forwarded-Proto": "https"})
	if got := server.panelBase(proxied); got != "https://lattix.example.com" {
		t.Errorf("panelBase(proxied) = %q, want https://lattix.example.com", got)
	}
	if command := server.installCommand(server.panelBase(proxied), "bootstrap"); !strings.Contains(command, "--panel https://lattix.example.com") {
		t.Errorf("install command %q must carry the inferred https panel base", command)
	}

	// 直连未声明协议：保持 http（裸机/局域网 HTTP 部署不受影响）。
	direct := newRequest("192.168.1.23:52318", nil)
	if got := server.panelBase(direct); got != "http://lattix.example.com" {
		t.Errorf("panelBase(direct) = %q, want http://lattix.example.com", got)
	}
	// 公网对端伪造声明：不采信。
	spoofed := newRequest("198.51.100.7:52318", map[string]string{"X-Forwarded-Proto": "https"})
	if got := server.panelBase(spoofed); got != "http://lattix.example.com" {
		t.Errorf("panelBase(spoofed) = %q, want http://lattix.example.com", got)
	}
	// public_url 设置优先于推断。
	if err := st.SetSetting(context.Background(), store.SettingPublicURL, "https://panel.example.com:8443/"); err != nil {
		t.Fatal(err)
	}
	configured := newRequest("203.0.113.9:52318", nil)
	if got := server.panelBase(configured); got != "https://panel.example.com:8443" {
		t.Errorf("panelBase(configured) = %q, want https://panel.example.com:8443", got)
	}
}

// TestInstallInsecure 覆盖明文告警判定（审查 H1）：仅 http + 公网地址（含域名）告警；
// https、回环与私网地址不告警。
func TestInstallInsecure(t *testing.T) {
	cases := []struct {
		base string
		want bool
	}{
		{"http://203.0.113.10:8080", true}, // 裸 http 公网 IP
		{"http://panel.example.com", true}, // 域名保守按公网对待
		{"http://panel.example.com:8080", true},
		{"https://panel.example.com", false}, // 反代终止 TLS
		{"https://203.0.113.10", false},
		{"http://127.0.0.1:8080", false}, // 本机回环
		{"http://[::1]:8080", false},
		{"http://192.168.1.10:8080", false}, // 私网
		{"http://10.0.0.8", false},
		{"http://172.16.0.3", false},
	}
	for _, c := range cases {
		if got := installInsecure(c.base); got != c.want {
			t.Errorf("installInsecure(%q) = %v, want %v", c.base, got, c.want)
		}
	}
}

// TestInstallInsecureHTTPResponse 在 HTTP 响应级断言 install_insecure（审查 H1）：
// POST /api/server/create 的响应体按对外地址形态给出明文告警位（前端据此展示警告），
// 覆盖 handleCreateServer → panelBase → installInsecure 的完整装配。
func TestInstallInsecureHTTPResponse(t *testing.T) {
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	server := &Server{st: st, cfg: Config{GitHubRepo: "BeanYa/Lattix", Version: "v1.2.3"},
		req: &settingsRequester{online: map[int64]bool{}}}

	create := func(alias, remote, host, forwardedProto string) bool {
		t.Helper()
		r := httptest.NewRequest(http.MethodPost, "/api/server/create",
			strings.NewReader(fmt.Sprintf(`{"alias":%q,"country_code":"US","location":"Test"}`, alias)))
		r.RemoteAddr = remote
		r.Host = host
		if forwardedProto != "" {
			r.Header.Set("X-Forwarded-Proto", forwardedProto)
		}
		rec := httptest.NewRecorder()
		server.handleCreateServer(rec, r)
		if rec.Code != http.StatusOK {
			t.Fatalf("create %s status = %d, body = %s", alias, rec.Code, rec.Body.String())
		}
		var resp struct {
			InstallCommand  string `json:"install_command"`
			InstallInsecure bool   `json:"install_insecure"`
		}
		if err := json.Unmarshal(decodeRPC(t, rec).Data, &resp); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(resp.InstallCommand, "--token ") {
			t.Fatalf("create %s install_command missing bootstrap token: %q", alias, resp.InstallCommand)
		}
		return resp.InstallInsecure
	}

	// 裸 http + 公网域名（公网对端直连）→ 告警。
	if !create("insecure-http", "198.51.100.7:52318", "panel.example.com", "") {
		t.Error("install_insecure = false, want true（http 公网）")
	}
	// 反代终止 TLS（docker 桥接对端的 X-Forwarded-Proto 声明可信）→ 不告警。
	if create("secure-proxy", "172.18.0.3:52318", "panel.example.com", "https") {
		t.Error("install_insecure = true, want false（https 反代）")
	}
	// 回环 http → 不告警。
	if create("loopback", "127.0.0.1:52318", "127.0.0.1:8080", "") {
		t.Error("install_insecure = true, want false（回环）")
	}
}

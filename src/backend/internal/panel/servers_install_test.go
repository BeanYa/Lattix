package panel

import (
	"context"
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

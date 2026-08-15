package sub

import (
	"fmt"
	"net/http"
	"strings"

	"lattix/backend/internal/store"
	"lattix/shared"
)

// serveQuanX 输出 Quantumult X 格式（[server_local] 段纯文本）。
// Quantumult X 的 VLESS 支持有限，首版仅输出 VLESS+Reality 节点。
func (s *Server) serveQuanX(w http.ResponseWriter, r *http.Request, user *store.User, items []proxyItem) {
	var lines []string
	for _, it := range items {
		credential := it.credential
		if credential == "" {
			credential = user.UUID
		}
		line := buildQuanXLine(it.node, it.rc, credential)
		if line != "" {
			lines = append(lines, line)
		}
	}
	body := strings.Join(lines, "\n") + "\n"
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Write([]byte(body))
}

// buildQuanXLine 构造单条 Quantumult X server_local 行。
// 格式：vless=host:port, method=none, password=uuid, obfs=over-tls, ...
// Quantumult X 对 Reality 的支持通过 tls13 + obfs-host 实现。
func buildQuanXLine(n store.Node, rc shared.RealizedConfig, uuid string) string {
	if rc.Port == 0 {
		return ""
	}
	if rc.Network == "" {
		rc.Network = shared.NetworkTCP
	}
	name := nodeName(n, rc)
	addr := shared.HostPort(n.ServerAddress, rc.Port) // IPv6 字面量自动加 []（§9）

	switch n.Protocol {
	case shared.ProtocolVLESS:
		// Quantumult X vless 格式（1.5.0+）
		parts := []string{
			fmt.Sprintf("vless=%s", addr),
			"method=none",
			fmt.Sprintf("password=%s", uuid),
			"obfs=over-tls",
			fmt.Sprintf("obfs-host=%s", rc.ServerName),
			"tls13=true",
			"fast-open=false",
			"udp-relay=true",
			fmt.Sprintf("tag=%s", name),
		}
		if rc.Flow != "" {
			parts = append(parts, fmt.Sprintf("vless-flow=%s", rc.Flow))
		}
		// Reality 公钥（Quantumult X 1.5.0+ 支持 reality-pubkey）
		if rc.PublicKey != "" {
			parts = append(parts, fmt.Sprintf("reality-pubkey=%s", rc.PublicKey))
		}
		if rc.ShortID != "" {
			parts = append(parts, fmt.Sprintf("reality-hexid=%s", rc.ShortID))
		}
		return strings.Join(parts, ", ")
	case shared.ProtocolTrojan:
		parts := []string{
			fmt.Sprintf("trojan=%s", addr),
			fmt.Sprintf("password=%s", uuid),
			"obfs=over-tls",
			fmt.Sprintf("obfs-host=%s", rc.ServerName),
			"tls13=true",
			"fast-open=false",
			"udp-relay=true",
			fmt.Sprintf("tag=%s", name),
		}
		return strings.Join(parts, ", ")
	case shared.ProtocolShadowsocks:
		password := shared.SSUserPassword(uuid, rc.Method)
		if shared.Is2022Method(rc.Method) {
			password = rc.PSK + ":" + password
		}
		parts := []string{
			fmt.Sprintf("shadowsocks=%s", addr),
			fmt.Sprintf("method=%s", rc.Method),
			fmt.Sprintf("password=%s", password),
			"fast-open=false",
			"udp-relay=true",
			fmt.Sprintf("tag=%s", name),
		}
		return strings.Join(parts, ", ")
	default:
		return "" // vmess/socks/http 暂不支持 QuanX 格式
	}
}

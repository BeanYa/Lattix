package sub

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"lattix/backend/internal/store"
	"lattix/shared"
)

// HandleLinks 处理 GET /sub/{token}/links（§14 `vless://` 链接订阅）：
// 返回 base64 编码的换行分隔分享链接集合（vless/trojan/vmess/ss），
// 仅含分配到该用户的 active 节点（§16）；dokodemo/socks/http 无标准分享链接，跳过。
// 有效停权态（expired=1 或 disabled=1，§9/§16）的用户返回空集合；不做 Accept 分流（仅 YAML 端点分流落地页）。
func (s *Server) HandleLinks(w http.ResponseWriter, r *http.Request) {
	user, nodes, err := s.assignedActiveNodes(r)
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, store.ErrNotFound) {
			status = http.StatusNotFound
		}
		http.Error(w, err.Error()+"\n", status)
		return
	}
	s.setSubHeaders(w, r, user)
	if user.Expired || user.Disabled {
		nodes = nil // 有效停权态（§9/§16）：链接集合为空
	}
	links := []string{}
	for _, it := range s.subscriptionItems(r, user, nodes) {
		if link, ok := buildShareLink(it.node, it.rc, user.UUID); ok {
			links = append(links, link)
		}
	}
	body := base64.StdEncoding.EncodeToString([]byte(strings.Join(links, "\n")))
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Write([]byte(body + "\n"))
}

// buildShareLink 按协议构造分享链接；不支持的协议返回 false。
func buildShareLink(n store.Node, rc shared.RealizedConfig, uuid string) (string, bool) {
	if rc.Port == 0 {
		return "", false
	}
	if rc.Network == "" {
		rc.Network = shared.NetworkTCP
	}
	if rc.Fingerprint == "" {
		rc.Fingerprint = shared.FingerprintChrome
	}
	name := shareName(n, rc)
	addr := fmt.Sprintf("%s:%d", n.ServerAddress, rc.Port)
	switch n.Protocol {
	case shared.ProtocolVLESS:
		q := url.Values{}
		q.Set("type", rc.Network)
		q.Set("security", "reality")
		q.Set("pbk", rc.PublicKey)
		q.Set("sid", rc.ShortID)
		q.Set("sni", rc.ServerName)
		q.Set("fp", rc.Fingerprint)
		if rc.Flow != "" {
			q.Set("flow", rc.Flow)
		}
		if rc.Encryption != "" {
			q.Set("encryption", rc.Encryption)
		} else {
			q.Set("encryption", "none") // 新版客户端要求显式声明
		}
		setTransportQuery(q, rc)
		return fmt.Sprintf("vless://%s@%s?%s#%s", uuid, addr, q.Encode(), name), true
	case shared.ProtocolTrojan:
		q := url.Values{}
		q.Set("type", rc.Network)
		q.Set("security", "reality")
		q.Set("pbk", rc.PublicKey)
		q.Set("sid", rc.ShortID)
		q.Set("sni", rc.ServerName)
		q.Set("fp", rc.Fingerprint)
		setTransportQuery(q, rc)
		return fmt.Sprintf("trojan://%s@%s?%s#%s", uuid, addr, q.Encode(), name), true
	case shared.ProtocolVMess:
		// vmess 分享为 base64(JSON)；reality 扩展字段按 v2rayN 惯例携带。
		port := fmt.Sprintf("%d", rc.Port)
		j, _ := json.Marshal(map[string]string{
			"v": "2", "ps": name, "add": n.ServerAddress, "port": port,
			"id": uuid, "aid": "0", "scy": "auto",
			"net": rc.Network, "type": "none",
			"host": rc.Host, "path": rc.Path,
			"tls": "reality", "sni": rc.ServerName, "fp": rc.Fingerprint,
			"pbk": rc.PublicKey, "sid": rc.ShortID,
		})
		return "vmess://" + base64.StdEncoding.EncodeToString(j), true
	case shared.ProtocolShadowsocks:
		password := shared.SSUserPassword(uuid, rc.Method)
		if shared.Is2022Method(rc.Method) {
			password = rc.PSK + ":" + password // 2022-blake3 多用户："节点PSK:用户密钥"
		}
		// SIP002：ss://base64(method:password)@host:port#name
		userinfo := base64.RawURLEncoding.EncodeToString([]byte(rc.Method + ":" + password))
		return fmt.Sprintf("ss://%s@%s#%s", userinfo, addr, name), true
	}
	return "", false
}

// setTransportQuery 写入 grpc/xhttp 的传输参数。
func setTransportQuery(q url.Values, rc shared.RealizedConfig) {
	switch rc.Network {
	case shared.NetworkGRPC:
		q.Set("serviceName", rc.ServiceName)
	case shared.NetworkXHTTP:
		q.Set("path", rc.Path)
		q.Set("mode", rc.Mode)
		if rc.Host != "" {
			q.Set("host", rc.Host)
		}
	}
}

// shareName 是分享链接的节点名（与 mihomo 订阅一致，URL fragment 编码）。
func shareName(n store.Node, rc shared.RealizedConfig) string {
	return url.PathEscape(fmt.Sprintf("%s-%s-%d", n.ServerAlias, n.Protocol, rc.Port))
}

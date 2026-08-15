package sub

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"

	"lattix/backend/internal/store"
	"lattix/shared"
)

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
	addr := shared.HostPort(n.ServerAddress, rc.Port) // IPv6 字面量自动加 []（§9）
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

// shareName 是分享链接的节点名（URL fragment 编码）。
func shareName(n store.Node, rc shared.RealizedConfig) string {
	return url.PathEscape(nodeName(n, rc))
}

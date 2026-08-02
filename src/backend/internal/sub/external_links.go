package sub

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"lattix/backend/internal/extsub"
)

// buildExternalLink 把外部订阅节点序列化为分享链接（与 extsub 解析器互逆）；
// 不支持的协议返回 ok=false。
func buildExternalLink(n extsub.Node) (string, bool) {
	e := externalYAMLFallback(n.Extra)
	name := url.QueryEscape(n.Name)
	hostPort := fmt.Sprintf("%s:%d", n.Server, n.Port)
	switch n.Type {
	case "vless":
		return "vless://" + extStr(e, "id") + "@" + hostPort + "?" + externalQuery(e, "id") + "#" + name, true
	case "trojan":
		return "trojan://" + extStr(e, "password") + "@" + hostPort + "?" + externalQuery(e, "password") + "#" + name, true
	case "hysteria2":
		return "hysteria2://" + extStr(e, "password") + "@" + hostPort + "?" + externalQuery(e, "password") + "#" + name, true
	case "tuic":
		return "tuic://" + extStr(e, "uuid") + ":" + extStr(e, "password") + "@" + hostPort + "?" + externalQuery(e, "uuid", "password") + "#" + name, true
	case "anytls":
		return "anytls://" + extStr(e, "password") + "@" + hostPort + "?" + externalQuery(e, "password") + "#" + name, true
	case "snell":
		return "snell://" + extStr(e, "psk") + "@" + hostPort + "?" + externalQuery(e, "psk") + "#" + name, true
	case "socks":
		cred := extStr(e, "username")
		if pwd := extStr(e, "password"); pwd != "" {
			cred += ":" + pwd
		}
		return "socks://" + cred + "@" + hostPort + "#" + name, true
	case "http":
		cred := extStr(e, "username")
		if pwd := extStr(e, "password"); pwd != "" {
			cred += ":" + pwd
		}
		return "http://" + cred + "@" + hostPort + "#" + name, true
	case "vmess":
		payload := map[string]any{
			"v": "2", "ps": n.Name, "add": n.Server, "port": strconv.Itoa(n.Port),
		}
		for key, value := range e {
			payload[key] = value
		}
		raw, err := json.Marshal(payload)
		if err != nil {
			return "", false
		}
		return "vmess://" + base64.RawURLEncoding.EncodeToString(raw), true
	case "ss":
		cred := base64.RawURLEncoding.EncodeToString([]byte(extStr(e, "method") + ":" + extStr(e, "password")))
		query := externalQuery(e, "method", "password")
		if query != "" {
			return "ss://" + cred + "@" + hostPort + "?" + query + "#" + name, true
		}
		return "ss://" + cred + "@" + hostPort + "#" + name, true
	case "ssr":
		payload := fmt.Sprintf("%s:%d:%s:%s:%s:%s", n.Server, n.Port,
			extStr(e, "protocol"), extStr(e, "method"), extStr(e, "obfs"),
			base64.StdEncoding.EncodeToString([]byte(extStr(e, "password"))))
		query := externalQuery(e, "protocol", "method", "obfs", "password", "remarks")
		remarks := "remarks=" + url.QueryEscape(base64.StdEncoding.EncodeToString([]byte(n.Name)))
		if query != "" {
			query += "&" + remarks
		} else {
			query = remarks
		}
		return "ssr://" + base64.StdEncoding.EncodeToString([]byte(payload)) + "?" + query, true
	case "wireguard":
		query := externalQuery(e, "private_key", "endpoint", "pk", "public_key")
		params := []string{
			"endpoint=" + url.QueryEscape(hostPort),
			"private_key=" + url.QueryEscape(extStr(e, "private_key")),
		}
		if query != "" {
			params = append(params, query)
		}
		return "wireguard://?" + strings.Join(params, "&") + "#" + name, true
	}
	return "", false
}

// externalQuery 把 Extra 剩余键值重建为 URL query（排序保证确定性）。
func externalQuery(extra map[string]any, skip ...string) string {
	skipped := map[string]bool{}
	for _, key := range skip {
		skipped[key] = true
	}
	keys := make([]string, 0, len(extra))
	for key := range extra {
		if !skipped[key] {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, url.QueryEscape(key)+"="+url.QueryEscape(extStr(extra, key)))
	}
	return strings.Join(parts, "&")
}

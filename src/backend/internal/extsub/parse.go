// Package extsub 实现外部订阅的拉取、解析与同步（设计文档：
// docs/superpowers/specs/2026-08-02-external-subscriptions-design.md）。
package extsub

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

var (
	stdBase64 = base64.StdEncoding
	urlBase64 = base64.RawURLEncoding
)

// Node 是标准化后的外部节点。Extra 保留协议专有字段（键为小写，
// 值已做 URL 解码）。config JSON 即对 Node 的 json.Marshal 结果。
type Node struct {
	Name   string         `json:"name"`
	Type   string         `json:"type"`
	Server string         `json:"server"`
	Port   int            `json:"port"`
	Extra  map[string]any `json:"extra,omitempty"`
}

// ParseSubscription 识别订阅内容格式并解析节点。返回格式为
// "yaml"|"v2ray"|"v2rayn"；无任何可解析节点时返回错误。
func ParseSubscription(body []byte) ([]Node, string, error) {
	text := strings.TrimSpace(string(body))
	if text == "" {
		return nil, "", fmt.Errorf("subscription body is empty")
	}
	if nodes, ok := parseYAML([]byte(text)); ok && len(nodes) > 0 {
		return nodes, "yaml", nil
	}
	// 先按原文逐行解析（v2rayN 无 scheme 的 base64 JSON 行在此命中）；
	// 原文无任何节点时才回退到整体 base64 解码后的行解析。
	if nodes, format, ok := parseLinkLinesWithFormat(text); ok {
		return nodes, format, nil
	}
	if nodes, format, ok := parseLinkLinesWithFormat(decodeBase64Layers(text)); ok {
		return nodes, format, nil
	}
	return nil, "", fmt.Errorf("no supported nodes found")
}

func parseLinkLinesWithFormat(text string) ([]Node, string, bool) {
	nodes, sawJSON := parseLinkLines(text)
	if len(nodes) == 0 {
		return nil, "", false
	}
	format := "v2ray"
	if sawJSON {
		format = "v2rayn"
	}
	return nodes, format, true
}

// decodeBase64Layers 尝试最多三层 base64 解码；解码结果看起来不像
// 纯 base64（含换行或 URI scheme 前缀）即停止。
func decodeBase64Layers(text string) string {
	current := text
	for i := 0; i < 3; i++ {
		candidate, err := tryBase64Decode(current)
		if err != nil {
			return current
		}
		if !looksLikeBase64(candidate) {
			return candidate
		}
		current = candidate
	}
	return current
}

func tryBase64Decode(text string) (string, error) {
	trimmed := strings.TrimSpace(text)
	for _, enc := range []*base64.Encoding{stdBase64, urlBase64} {
		if decoded, err := enc.DecodeString(trimmed); err == nil {
			return string(decoded), nil
		}
	}
	padded := trimmed
	if pad := len(trimmed) % 4; pad != 0 {
		padded += strings.Repeat("=", 4-pad)
	}
	for _, enc := range []*base64.Encoding{stdBase64, urlBase64} {
		if decoded, err := enc.DecodeString(padded); err == nil {
			return string(decoded), nil
		}
	}
	return "", fmt.Errorf("not base64")
}

// looksLikeBase64 判断解码产物是否仍像 base64（由解码者自己决定是否再解一层）。
func looksLikeBase64(text string) bool {
	if strings.ContainsAny(text, "\n\r") {
		return false
	}
	if strings.HasPrefix(text, "http://") || strings.HasPrefix(text, "https://") {
		return false
	}
	if isKnownScheme(text) {
		return false
	}
	if strings.Contains(text, ":") {
		return false
	}
	return true
}

func isKnownScheme(text string) bool {
	for _, scheme := range []string{
		"vless://", "vmess://", "ss://", "ssr://", "trojan://",
		"hysteria2://", "hy2://", "tuic://", "wireguard://", "wg://",
		"anytls://", "snell://", "socks://", "socks5://", "http://", "https://",
	} {
		if strings.HasPrefix(text, scheme) {
			return true
		}
	}
	return false
}

// parseLinkLines 逐行解析分享链接；返回节点数与是否出现过 base64 JSON 条目。
func parseLinkLines(text string) ([]Node, bool) {
	var nodes []Node
	sawJSON := false
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if node, ok := parseURI(line); ok {
			nodes = append(nodes, node)
			continue
		}
		// v2rayN 自定义格式：无 scheme 的 base64 JSON 条目
		if decoded, err := tryBase64Decode(line); err == nil && looksLikeVmessJSON(decoded) {
			if node, ok := parseVmessJSON(decoded); ok {
				sawJSON = true
				nodes = append(nodes, node)
			}
		}
	}
	return nodes, sawJSON
}

func looksLikeVmessJSON(text string) bool {
	var probe map[string]any
	if err := json.Unmarshal([]byte(text), &probe); err != nil {
		return false
	}
	_, hasAdd := probe["add"]
	_, hasPs := probe["ps"]
	return hasAdd && hasPs
}

func parseURI(uri string) (Node, bool) {
	u, err := url.Parse(uri)
	if err != nil || u.Hostname() == "" {
		return Node{}, false
	}
	switch u.Scheme {
	case "vless", "trojan", "hysteria2", "hy2", "tuic", "anytls", "snell", "socks", "socks5", "http":
		return parseCredentialURI(u), true
	case "vmess":
		return parseVmessURI(u)
	case "ss":
		return parseSSURI(u)
	case "ssr":
		return parseSSRURI(u)
	case "wireguard", "wg":
		return parseWireguardURI(u)
	}
	return Node{}, false
}

func portFrom(u *url.URL, fallback int) int {
	if u.Port() != "" {
		if port, err := strconv.Atoi(u.Port()); err == nil && port > 0 {
			return port
		}
	}
	return fallback
}

func nodeName(fragment string) string {
	if name, err := url.QueryUnescape(fragment); err == nil {
		return strings.TrimSpace(name)
	}
	return strings.TrimSpace(fragment)
}

func extraFromQuery(u *url.URL) map[string]any {
	extra := make(map[string]any)
	for key, values := range u.Query() {
		if len(values) > 0 {
			extra[key] = values[0]
		}
	}
	return extra
}

func extraWith(extra map[string]any, key string, value any) map[string]any {
	if value == nil {
		return extra
	}
	extra[key] = value
	return extra
}

// parseCredentialURI 处理 userinfo 携带口令的协议（vless/trojan/hy2/tuic/...）。
func parseCredentialURI(u *url.URL) Node {
	node := Node{
		Name:   nodeName(u.Fragment),
		Type:   u.Scheme,
		Server: u.Hostname(),
		Port:   portFrom(u, 443),
		Extra:  extraFromQuery(u),
	}
	switch u.Scheme {
	case "vless":
		node.Extra = extraWith(node.Extra, "id", u.User.Username())
	case "trojan", "hysteria2", "hy2", "anytls":
		node.Extra = extraWith(node.Extra, "password", u.User.Username())
	case "tuic":
		node.Extra = extraWith(node.Extra, "uuid", u.User.Username())
		if pwd, ok := u.User.Password(); ok {
			node.Extra = extraWith(node.Extra, "password", pwd)
		}
	case "snell":
		node.Extra = extraWith(node.Extra, "psk", u.User.Username())
	case "socks", "socks5":
		node.Type = "socks"
		node.Extra = extraWith(node.Extra, "username", u.User.Username())
		if pwd, ok := u.User.Password(); ok {
			node.Extra = extraWith(node.Extra, "password", pwd)
		}
	}
	if node.Type == "" || node.Server == "" || node.Port == 0 {
		return Node{}
	}
	return node
}

func parseVmessURI(u *url.URL) (Node, bool) {
	payload := u.Host + u.Path
	decoded, err := tryBase64Decode(payload)
	if err != nil {
		return Node{}, false
	}
	return parseVmessJSON(decoded)
}

// parseVmessJSON 解析 vmess 的 base64 JSON 载荷。
func parseVmessJSON(decoded string) (Node, bool) {
	var payload map[string]any
	if err := json.Unmarshal([]byte(decoded), &payload); err != nil {
		return Node{}, false
	}
	node := Node{
		Name:   nodeName(toString(payload["ps"])),
		Type:   "vmess",
		Server: toString(payload["add"]),
		Port:   toInt(payload["port"]),
		Extra:  make(map[string]any),
	}
	if node.Server == "" || node.Port == 0 {
		return Node{}, false
	}
	for key, value := range payload {
		if key == "ps" || key == "add" || key == "port" {
			continue
		}
		node.Extra[key] = value
	}
	return node, true
}

func parseSSURI(u *url.URL) (Node, bool) {
	userinfo := u.User.String()
	if userinfo == "" && u.Host != "" {
		userinfo = u.Host
	}
	decoded, err := tryBase64Decode(userinfo)
	if err != nil {
		return Node{}, false
	}
	// SIP002: method:password@host:port?plugin=...
	at := strings.LastIndex(decoded, "@")
	hostPort := decoded
	if at >= 0 {
		hostPort = decoded[at+1:]
	}
	var server string
	var port int
	if hp := strings.SplitN(hostPort, ":", 2); len(hp) == 2 {
		server = hp[0]
		port, _ = strconv.Atoi(hp[1])
	}
	if server == "" || port == 0 {
		// 旧格式：ss://base64(method:password@host:port)#name
		hostPort = u.Host
		if hp := strings.SplitN(hostPort, ":", 2); len(hp) == 2 {
			server = hp[0]
			port, _ = strconv.Atoi(hp[1])
		}
		if server == "" || port == 0 {
			return Node{}, false
		}
	}
	node := Node{
		Name:   nodeName(u.Fragment),
		Type:   "ss",
		Server: server,
		Port:   port,
		Extra:  extraFromQuery(u),
	}
	credentials := decoded
	if at >= 0 {
		credentials = decoded[:at]
	}
	parts := strings.SplitN(credentials, ":", 2)
	node.Extra = extraWith(node.Extra, "method", parts[0])
	if len(parts) == 2 {
		node.Extra = extraWith(node.Extra, "password", parts[1])
	}
	return node, true
}

func parseSSRURI(u *url.URL) (Node, bool) {
	payload := u.Host + u.Path
	decoded, err := tryBase64Decode(payload)
	if err != nil {
		return Node{}, false
	}
	// host:port:protocol:method:obfs:base64(password)?query
	parts := strings.SplitN(decoded, ":", 6)
	if len(parts) < 6 {
		return Node{}, false
	}
	port, _ := strconv.Atoi(parts[1])
	if port == 0 {
		return Node{}, false
	}
	password, err := tryBase64Decode(parts[5])
	if err != nil {
		return Node{}, false
	}
	node := Node{
		Name:   nodeName(u.Fragment),
		Type:   "ssr",
		Server: parts[0],
		Port:   port,
		Extra:  extraFromQuery(u),
	}
	node.Extra = extraWith(node.Extra, "protocol", parts[2])
	node.Extra = extraWith(node.Extra, "method", parts[3])
	node.Extra = extraWith(node.Extra, "obfs", parts[4])
	node.Extra = extraWith(node.Extra, "password", password)
	if query := u.Query(); query.Get("remarks") != "" {
		node.Name = nodeName(query.Get("remarks"))
	}
	return node, true
}

func parseWireguardURI(u *url.URL) (Node, bool) {
	query := u.Query()
	endpoint := query.Get("endpoint")
	var server string
	var port int
	if hp := strings.SplitN(endpoint, ":", 2); len(hp) == 2 {
		server = hp[0]
		port, _ = strconv.Atoi(hp[1])
	}
	if server == "" || port == 0 {
		return Node{}, false
	}
	extra := extraFromQuery(u)
	delete(extra, "endpoint")
	extra = extraWith(extra, "private_key", query.Get("private_key"))
	return Node{
		Name:   nodeName(u.Fragment),
		Type:   "wireguard",
		Server: server,
		Port:   port,
		Extra:  extra,
	}, true
}

func toString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func toInt(v any) int {
	switch t := v.(type) {
	case float64:
		return int(t)
	case int:
		return t
	case int64:
		return int(t)
	case uint64:
		return int(t)
	case string:
		if n, err := strconv.Atoi(t); err == nil {
			return n
		}
	}
	return 0
}

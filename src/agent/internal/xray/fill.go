package xray

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"lattix/shared"
)

// destCheckTimeout 是 dest 预检的单次超时（§6）。
const destCheckTimeout = 4 * time.Second

// fillTemplate 填充模板占位符（§7）并做 dest 预检（§6 步骤 2）。
// 返回填充后的 inbound JSON 与提取出的实际生效值（apply_result 上报用）。
func (m *Manager) fillTemplate(tag string, vc shared.VirtualConfig, userUUIDs []string, destCandidates []string) (json.RawMessage, *shared.RealizedConfig, error) {
	port, err := pickPort(vc.Port)
	if err != nil {
		return nil, nil, err
	}

	t := string(vc.Template)
	// 仅 reality 协议模板含私钥占位符（ss/socks/http/dokodemo 无 Reality 密钥对）。
	pub := ""
	if strings.Contains(t, shared.PlaceholderRealityPrivateKey) {
		priv, p, err := m.x25519()
		if err != nil {
			return nil, nil, err
		}
		pub = p
		t = strings.ReplaceAll(t, shared.PlaceholderRealityPrivateKey, priv)
	}
	// VLESS Encryption：仅当模板含 decryption 占位符时执行 `xray vlessenc` 生成（§15），
	// decryption 填入模板，encryption（客户端字符串）随 realized 上报。
	encClient := ""
	if strings.Contains(t, shared.PlaceholderVLessDecryption) {
		dec, enc, err := m.vlessEnc(vc.Encryption)
		if err != nil {
			return nil, nil, err
		}
		// vision 拼接 Encryption 时客户端须用 1-RTT 模式（§15）。
		if vc.Flow != "" {
			enc = strings.Replace(enc, ".0rtt.", ".1rtt.", 1)
		}
		encClient = enc
		t = strings.ReplaceAll(t, shared.PlaceholderVLessDecryption, dec)
	}

	// PORT/CLIENTS 约定为带引号的字符串占位符，连引号一起替换为 JSON 值；
	// 同时兜底未加引号的写法。dokodemo 模板不含 CLIENTS 占位符。
	t = strings.ReplaceAll(t, `"`+shared.PlaceholderPort+`"`, strconv.Itoa(port))
	t = strings.ReplaceAll(t, shared.PlaceholderPort, strconv.Itoa(port))
	t = strings.ReplaceAll(t, shared.PlaceholderTag, tag)
	if strings.Contains(t, shared.PlaceholderClients) {
		clients := make([]map[string]any, 0, len(userUUIDs))
		for _, u := range userUUIDs {
			clients = append(clients, clientEntry(vc.Protocol, vc.Flow, vc.Method, u))
		}
		clientsJSON, err := json.Marshal(clients)
		if err != nil {
			return nil, nil, err
		}
		t = strings.ReplaceAll(t, `"`+shared.PlaceholderClients+`"`, string(clientsJSON))
		t = strings.ReplaceAll(t, shared.PlaceholderClients, string(clientsJSON))
	}

	// 填充后必须是合法 JSON。
	var tmpl map[string]json.RawMessage
	if err := json.Unmarshal([]byte(t), &tmpl); err != nil {
		return nil, nil, fmt.Errorf("模板填充后不是合法 JSON: %w", err)
	}
	// dest 预检：模板 dest 不可达时按白名单逐个尝试（§6 步骤 2）；非 reality 模板自动跳过。
	if err := ensureDestReachable(tmpl, destCandidates); err != nil {
		return nil, nil, err
	}

	// 提取实际生效值（§7 realized_config）：须在 dest 最终确定之后。
	final, err := json.Marshal(tmpl)
	if err != nil {
		return nil, nil, err
	}
	var probe struct {
		Port     int `json:"port"`
		Settings struct {
			Password string `json:"password"` // ss 2022-blake3 节点级 PSK
		} `json:"settings"`
		StreamSettings struct {
			Network         string `json:"network"`
			RealitySettings struct {
				ServerNames []string `json:"serverNames"`
				ShortIDs    []string `json:"shortIds"`
			} `json:"realitySettings"`
			GrpcSettings struct {
				ServiceName string `json:"serviceName"`
			} `json:"grpcSettings"`
			XHTTPSettings struct {
				Path string `json:"path"`
				Mode string `json:"mode"`
				Host string `json:"host"`
			} `json:"xhttpSettings"`
		} `json:"streamSettings"`
	}
	if err := json.Unmarshal(final, &probe); err != nil {
		return nil, nil, err
	}
	if probe.Port == 0 {
		return nil, nil, fmt.Errorf("模板缺少端口（既无 %s 占位符也无固定 port）", shared.PlaceholderPort)
	}
	fingerprint := vc.Fingerprint
	if fingerprint == "" {
		fingerprint = shared.FingerprintChrome
	}
	realized := &shared.RealizedConfig{
		Port:        probe.Port,
		PublicKey:   pub,
		ShortID:     firstOrEmpty(probe.StreamSettings.RealitySettings.ShortIDs),
		ServerName:  firstOrEmpty(probe.StreamSettings.RealitySettings.ServerNames),
		Flow:        vc.Flow,
		Fingerprint: fingerprint,
		Network:     probe.StreamSettings.Network,
		ServiceName: probe.StreamSettings.GrpcSettings.ServiceName,
		Path:        probe.StreamSettings.XHTTPSettings.Path,
		Mode:        probe.StreamSettings.XHTTPSettings.Mode,
		Host:        probe.StreamSettings.XHTTPSettings.Host,
		Method:      vc.Method,
		PSK:         probe.Settings.Password,
		Encryption:  encClient,
	}
	return json.RawMessage(final), realized, nil
}

// clientEntry 按协议构造一个用户条目（settings.clients 或 settings.accounts 数组元素，§8）。
// email 与 vless 的 RemoveUserOperation 匹配键一致；socks/http 的 accounts 以 user 为匹配键。
// clients 带 level: 0 以启用用户级流量统计（§13，policy levels["0"]）。
func clientEntry(protocol, flow, method, uuid string) map[string]any {
	switch protocol {
	case shared.ProtocolVMess:
		return map[string]any{"id": uuid, "email": uuid, "level": 0} // 新版 xray 已移除 alterId
	case shared.ProtocolTrojan:
		return map[string]any{"password": uuid, "email": uuid, "level": 0}
	case shared.ProtocolShadowsocks:
		if shared.Is2022Method(method) {
			// 2022-blake3 多用户：clients 不带 method，password 为定长 base64 密钥。
			return map[string]any{"password": shared.SSUserPassword(uuid, method), "email": uuid, "level": 0}
		}
		return map[string]any{"method": method, "password": uuid, "email": uuid, "level": 0}
	case shared.ProtocolSocks, shared.ProtocolHTTP:
		return map[string]any{"user": uuid, "pass": uuid}
	default: // vless
		e := map[string]any{"id": uuid, "email": uuid, "level": 0}
		if flow != "" {
			e["flow"] = flow
		}
		return e
	}
}

// ensureDestReachable 检查模板 realitySettings.dest 的 TCP+TLS1.3 可达性：
// 可达则保留；不可达（或留空）则按白名单逐个尝试并改写 dest/serverNames；
// 全部失败返回 error。非 reality 模板（无 realitySettings）跳过，交由 xray -test 校验。
func ensureDestReachable(tmpl map[string]json.RawMessage, candidates []string) error {
	ssRaw, ok := tmpl["streamSettings"]
	if !ok {
		return nil
	}
	var ss map[string]json.RawMessage
	if err := json.Unmarshal(ssRaw, &ss); err != nil {
		return nil
	}
	rsRaw, ok := ss["realitySettings"]
	if !ok {
		return nil
	}
	var rs map[string]json.RawMessage
	if err := json.Unmarshal(rsRaw, &rs); err != nil {
		return nil
	}

	var dest string
	_ = json.Unmarshal(rs["dest"], &dest)
	var names []string
	_ = json.Unmarshal(rs["serverNames"], &names)

	sni := hostOf(dest)
	if len(names) > 0 && names[0] != "" {
		sni = names[0]
	}
	if dest != "" && destReachable(dest, sni) {
		return nil
	}
	for _, c := range candidates {
		if c == dest {
			continue
		}
		host := hostOf(c)
		if !destReachable(c, host) {
			continue
		}
		d, _ := json.Marshal(c)
		n, _ := json.Marshal([]string{host})
		rs["dest"] = d
		rs["serverNames"] = n
		rsRaw, _ := json.Marshal(rs)
		ss["realitySettings"] = rsRaw
		ssRaw, _ := json.Marshal(ss)
		tmpl["streamSettings"] = ssRaw
		log.Printf("xray: dest %q 不可达，fallback 到白名单候选 %q", dest, c)
		return nil
	}
	return fmt.Errorf("dest %q 不可达且白名单 %d 个候选全部失败", dest, len(candidates))
}

// destReachable 以 TCP+TLS1.3 握手探测 dest 可达性（Reality 借用证书的前提）。
func destReachable(dest, serverName string) bool {
	conn, err := tls.DialWithDialer(&net.Dialer{Timeout: destCheckTimeout}, "tcp", dest, &tls.Config{
		ServerName:         serverName,
		InsecureSkipVerify: true, // 仅探测可达性，不校验证书链
		MinVersion:         tls.VersionTLS13,
	})
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

// hostOf 取 "host:port" 的 host 部分。
func hostOf(addr string) string {
	if h, _, err := net.SplitHostPort(addr); err == nil {
		return h
	}
	return addr
}

// pickPort 确定监听端口（§7）：指定则检查占用（冲突报错），留空则挑空闲端口。
func pickPort(preferred int) (int, error) {
	if preferred != 0 {
		l, err := net.Listen("tcp", fmt.Sprintf(":%d", preferred))
		if err != nil {
			return 0, fmt.Errorf("端口 %d 被占用: %w", preferred, err)
		}
		l.Close()
		return preferred, nil
	}
	l, err := net.Listen("tcp", ":0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

// vlessEnc 执行 `xray vlessenc` 生成 VLESS Encryption 的 decryption/encryption 对（§15）。
// auth 为 shared.VLessEncX25519 / VLessEncMLKEM768（默认），选择对应认证方式的一对；
// decryption 为服务端私钥侧（不出服务器），encryption 为客户端订阅字符串。
func (m *Manager) vlessEnc(auth string) (dec, enc string, err error) {
	out, err := exec.Command(m.bin, "vlessenc").CombinedOutput()
	if err != nil {
		return "", "", fmt.Errorf("xray vlessenc 执行失败: %v: %s", err, strings.TrimSpace(string(out)))
	}
	want := "Authentication: ML-KEM-768"
	if auth == shared.VLessEncX25519 {
		want = "Authentication: X25519"
	}
	section := ""
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "Authentication:") {
			section = line
			continue
		}
		if !strings.HasPrefix(section, want) {
			continue
		}
		if strings.HasPrefix(line, `"decryption":`) {
			dec = jsonStringValue(line)
		}
		if strings.HasPrefix(line, `"encryption":`) {
			enc = jsonStringValue(line)
		}
	}
	if dec == "" || enc == "" {
		return "", "", fmt.Errorf("无法解析 xray vlessenc 输出（%s 认证）: %s", auth, strings.TrimSpace(string(out)))
	}
	return dec, enc, nil
}

// jsonStringValue 提取 `"key": "value"` 行中的 value。
func jsonStringValue(line string) string {
	parts := strings.Split(line, `"`)
	if len(parts) < 4 {
		return ""
	}
	return parts[3]
}

// x25519 执行 `xray x25519` 生成 Reality 密钥对（§7：私钥不出服务器）。// 兼容新旧输出格式（Private key/Public key 与 PrivateKey/Password）。
func (m *Manager) x25519() (priv, pub string, err error) {
	out, err := exec.Command(m.bin, "x25519").CombinedOutput()
	if err != nil {
		return "", "", fmt.Errorf("xray x25519 执行失败: %v: %s", err, strings.TrimSpace(string(out)))
	}
	for _, line := range strings.Split(string(out), "\n") {
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		// 归一化键名后按前缀匹配：兼容 "Private key:"、"PrivateKey:"、
		// "Public key:"、"PublicKey:"、"Password:"、"Password (PublicKey):"。
		nk := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(k), " ", ""))
		switch {
		case strings.HasPrefix(nk, "privatekey"):
			priv = strings.TrimSpace(v)
		case strings.HasPrefix(nk, "publickey"), strings.HasPrefix(nk, "password"):
			pub = strings.TrimSpace(v)
		}
	}
	if priv == "" || pub == "" {
		return "", "", fmt.Errorf("无法解析 xray x25519 输出: %s", strings.TrimSpace(string(out)))
	}
	return priv, pub, nil
}

func firstOrEmpty(s []string) string {
	if len(s) > 0 {
		return s[0]
	}
	return ""
}

package xray

import (
	"encoding/json"
	"fmt"
	"net"
	"os/exec"
	"strconv"
	"strings"

	"lattix/shared"
)

// fillTemplate 填充模板占位符（§7）：端口、Reality 私钥、用户列表、tag。
// 返回填充后的 inbound JSON 与提取出的实际生效值（apply_result 上报用）。
func (m *Manager) fillTemplate(tag string, vc shared.VirtualConfig, userUUIDs []string) (json.RawMessage, *shared.RealizedConfig, error) {
	port, err := pickPort(vc.Port)
	if err != nil {
		return nil, nil, err
	}
	priv, pub, err := m.x25519()
	if err != nil {
		return nil, nil, err
	}

	clients := make([]map[string]string, 0, len(userUUIDs))
	for _, u := range userUUIDs {
		clients = append(clients, map[string]string{"id": u, "flow": shared.FlowVision, "email": u})
	}
	clientsJSON, err := json.Marshal(clients)
	if err != nil {
		return nil, nil, err
	}

	t := string(vc.Template)
	// PORT/CLIENTS 约定为带引号的字符串占位符，连引号一起替换为 JSON 值；
	// 同时兜底未加引号的写法。
	t = strings.ReplaceAll(t, `"`+shared.PlaceholderPort+`"`, strconv.Itoa(port))
	t = strings.ReplaceAll(t, shared.PlaceholderPort, strconv.Itoa(port))
	t = strings.ReplaceAll(t, shared.PlaceholderRealityPrivateKey, priv)
	t = strings.ReplaceAll(t, shared.PlaceholderTag, tag)
	t = strings.ReplaceAll(t, `"`+shared.PlaceholderClients+`"`, string(clientsJSON))
	t = strings.ReplaceAll(t, shared.PlaceholderClients, string(clientsJSON))

	// 填充后必须是合法 JSON；顺带提取实际生效值（§7 realized_config）。
	var probe struct {
		Port           int `json:"port"`
		StreamSettings struct {
			RealitySettings struct {
				ServerNames []string `json:"serverNames"`
				ShortIDs    []string `json:"shortIds"`
			} `json:"realitySettings"`
		} `json:"streamSettings"`
	}
	if err := json.Unmarshal([]byte(t), &probe); err != nil {
		return nil, nil, fmt.Errorf("模板填充后不是合法 JSON: %w", err)
	}
	if probe.Port == 0 {
		return nil, nil, fmt.Errorf("模板缺少端口（既无 %s 占位符也无固定 port）", shared.PlaceholderPort)
	}
	realized := &shared.RealizedConfig{
		Port:        probe.Port,
		PublicKey:   pub,
		ShortID:     firstOrEmpty(probe.StreamSettings.RealitySettings.ShortIDs),
		ServerName:  firstOrEmpty(probe.StreamSettings.RealitySettings.ServerNames),
		Flow:        shared.FlowVision,
		Fingerprint: shared.FingerprintChrome,
	}
	return json.RawMessage(t), realized, nil
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

// x25519 执行 `xray x25519` 生成 Reality 密钥对（§7：私钥不出服务器）。
// 兼容新旧输出格式（Private key/Public key 与 PrivateKey/Password）。
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

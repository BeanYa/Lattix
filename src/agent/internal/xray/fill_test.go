package xray

import (
	"encoding/json"
	"testing"

	"lattix/shared"
)

// TestFillTemplateWSRealized 验证 ws 模板的 realized 提取：path/host（headers.Host）
// 与 security=none（无 realitySettings 无公钥）。
func TestFillTemplateWSRealized(t *testing.T) {
	vc := shared.VirtualConfig{
		Protocol: shared.ProtocolVMess,
		Security: shared.SecurityNone,
		Template: json.RawMessage(`{
			"tag": "{{TAG}}", "protocol": "vmess", "port": "{{PORT}}",
			"settings": {"clients": "{{CLIENTS}}"},
			"streamSettings": {"network": "ws", "security": "none",
				"wsSettings": {"path": "/p", "headers": {"Host": "h.example.com"}}}
		}`),
	}
	m := &Manager{}
	_, realized, err := m.fillTemplate(23401, "node_1", vc, []string{"u1"}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if realized.Network != "ws" || realized.Path != "/p" || realized.Host != "h.example.com" {
		t.Errorf("ws realized 提取不符: %+v", realized)
	}
	if realized.Security != shared.SecurityNone {
		t.Errorf("security 应为 none，实际 %q", realized.Security)
	}
	if realized.PublicKey != "" || realized.ShortID != "" || realized.ServerName != "" {
		t.Errorf("非 reality 模板不应有 reality 字段: %+v", realized)
	}
}

// TestFillTemplateHTTPUpgradeRealized 验证 httpupgrade 模板（httpupgradeSettings 平铺 path/host）。
func TestFillTemplateHTTPUpgradeRealized(t *testing.T) {
	vc := shared.VirtualConfig{
		Protocol: shared.ProtocolVMess,
		Security: shared.SecurityNone,
		Template: json.RawMessage(`{
			"tag": "{{TAG}}", "protocol": "vmess", "port": "{{PORT}}",
			"settings": {"clients": "{{CLIENTS}}"},
			"streamSettings": {"network": "httpupgrade", "security": "none",
				"httpupgradeSettings": {"path": "/hu", "host": "cdn.example.com"}}
		}`),
	}
	m := &Manager{}
	_, realized, err := m.fillTemplate(23402, "node_2", vc, []string{"u1"}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if realized.Network != "httpupgrade" || realized.Path != "/hu" || realized.Host != "cdn.example.com" {
		t.Errorf("httpupgrade realized 提取不符: %+v", realized)
	}
	if realized.Security != shared.SecurityNone {
		t.Errorf("security 应为 none，实际 %q", realized.Security)
	}
}

// TestFillTemplateRealitySecurity 回归：reality 模板提取 security=reality。
func TestFillTemplateRealitySecurity(t *testing.T) {
	vc := shared.VirtualConfig{
		Protocol: shared.ProtocolVLESS,
		Security: shared.SecurityReality,
		Template: json.RawMessage(`{
			"tag": "{{TAG}}", "protocol": "vless", "port": "{{PORT}}",
			"settings": {"clients": "{{CLIENTS}}", "decryption": "none"},
			"streamSettings": {"network": "tcp", "security": "reality",
				"realitySettings": {"dest": "dl.google.com:443", "serverNames": ["dl.google.com"],
					"privateKey": "{{PRIVATE_KEY}}", "shortIds": ["ab12"]}}
		}`),
	}
	orig := destReachable
	destReachable = func(string, string) bool { return true }
	t.Cleanup(func() { destReachable = orig })
	m, _ := newRebuildTestManager(t)
	_, realized, err := m.fillTemplate(23403, "node_3", vc, []string{"u1"}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if realized.Security != shared.SecurityReality {
		t.Errorf("reality 模板 security 应为 reality，实际 %q", realized.Security)
	}
	if realized.ShortID != "ab12" || realized.ServerName != "dl.google.com" {
		t.Errorf("reality realized 回归不符: %+v", realized)
	}
}

// TestPickRandomFreePortLayered 验证 :0 兜底返回端口在指定层全部空闲
// （P1 缺口：自动分配分支不探测 UDP 层）。
func TestPickRandomFreePortLayered(t *testing.T) {
	p, err := pickRandomFreePort("tcp,udp")
	if err != nil {
		t.Fatal(err)
	}
	if p <= 0 {
		t.Fatalf("端口应 >0，实际 %d", p)
	}
	if err := probePortFree("tcp,udp", p); err != nil {
		t.Errorf("返回端口 %d 应 tcp+udp 双层空闲: %v", p, err)
	}
}

// TestTemplateSecurity 验证纯函数按模板内容推导 security。
func TestTemplateSecurity(t *testing.T) {
	reality := map[string]json.RawMessage{
		"streamSettings": json.RawMessage(`{"network":"tcp","realitySettings":{}}`),
	}
	if got := templateSecurity(reality); got != shared.SecurityReality {
		t.Errorf("含 realitySettings 应为 reality，实际 %q", got)
	}
	plain := map[string]json.RawMessage{
		"streamSettings": json.RawMessage(`{"network":"ws"}`),
	}
	if got := templateSecurity(plain); got != shared.SecurityNone {
		t.Errorf("无 realitySettings 应为 none，实际 %q", got)
	}
	if got := templateSecurity(map[string]json.RawMessage{}); got != "" {
		t.Errorf("无 streamSettings 应为空串，实际 %q", got)
	}
}

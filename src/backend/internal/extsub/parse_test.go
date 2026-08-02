package extsub

import "testing"

const vlessLink = "vless://11111111-2222-3333-4444-555555555555@example.com:443?type=tcp&security=reality&pbk=Pbk&sid=abcd&sni=cdn.example.com&fp=chrome&flow=xtls-rprx-vision#%E4%B8%9C%E4%BA%AC%2001"

func TestParseLinksVless(t *testing.T) {
	nodes, format, err := ParseSubscription([]byte(vlessLink))
	if err != nil || len(nodes) != 1 {
		t.Fatalf("nodes %v, format %q, err %v", nodes, format, err)
	}
	n := nodes[0]
	if n.Name != "东京 01" || n.Type != "vless" || n.Server != "example.com" || n.Port != 443 {
		t.Fatalf("node = %+v", n)
	}
	if n.Extra["id"] != "11111111-2222-3333-4444-555555555555" || n.Extra["security"] != "reality" || n.Extra["flow"] != "xtls-rprx-vision" {
		t.Fatalf("extra = %+v", n.Extra)
	}
}

func TestParseLinksBase64Bundle(t *testing.T) {
	body := base64Encode(vlessLink + "\n" + "ss://" + base64urlEncode("aes-128-gcm:pass") + "@1.2.3.4:8388#ss-01")
	nodes, format, err := ParseSubscription([]byte(body))
	if err != nil || format != "v2ray" {
		t.Fatalf("format %q err %v", format, err)
	}
	if len(nodes) != 2 {
		t.Fatalf("nodes = %+v", nodes)
	}
	ss := nodes[1]
	if ss.Type != "ss" || ss.Server != "1.2.3.4" || ss.Port != 8388 || ss.Extra["method"] != "aes-128-gcm" || ss.Extra["password"] != "pass" {
		t.Fatalf("ss node = %+v", ss)
	}
}

func TestParseLinksVmessAndV2rayN(t *testing.T) {
	vmessJSON := `{"v":"2","ps":"vmess-01","add":"5.6.7.8","port":"443","id":"aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee","aid":"0","scy":"auto","net":"ws","type":"none","host":"h.example.com","path":"/p","tls":"tls","sni":"h.example.com"}`
	vmessLink := "vmess://" + base64urlEncode(vmessJSON)
	// v2rayN 自定义：无 scheme 的 base64 JSON 行
	customLine := base64urlEncode(`{"v":"2","ps":"custom-01","add":"9.9.9.9","port":"8443","id":"id-id-id","net":"tcp","tls":"","type":"none"}`)
	nodes, format, err := ParseSubscription([]byte(vmessLink + "\n" + customLine))
	if err != nil || format != "v2rayn" {
		t.Fatalf("format %q err %v", format, err)
	}
	if len(nodes) != 2 {
		t.Fatalf("nodes = %+v", nodes)
	}
	if nodes[0].Type != "vmess" || nodes[0].Name != "vmess-01" || nodes[0].Server != "5.6.7.8" || nodes[0].Port != 443 {
		t.Fatalf("vmess node = %+v", nodes[0])
	}
	if nodes[1].Name != "custom-01" || nodes[1].Server != "9.9.9.9" || nodes[1].Port != 8443 {
		t.Fatalf("custom node = %+v", nodes[1])
	}
}

func TestParseYAMLMihomo(t *testing.T) {
	yamlBody := `proxies:
  - name: "香港 01"
    type: hysteria2
    server: hk.example.com
    port: 443
    password: "p1"
    sni: hk.example.com
  - name: "美国 02"
    type: vless
    server: us.example.com
    port: 443
    uuid: "11111111-2222-3333-4444-555555555555"
    network: ws
    ws-opts:
      path: /ws
    reality-opts:
      public-key: "pub"
      short-id: "1234"
    client-fingerprint: chrome
`
	nodes, format, err := ParseSubscription([]byte(yamlBody))
	if err != nil || format != "yaml" {
		t.Fatalf("format %q err %v", format, err)
	}
	if len(nodes) != 2 {
		t.Fatalf("nodes = %+v", nodes)
	}
	hy2 := nodes[0]
	if hy2.Type != "hysteria2" || hy2.Server != "hk.example.com" || hy2.Port != 443 || hy2.Extra["password"] != "p1" || hy2.Extra["sni"] != "hk.example.com" {
		t.Fatalf("hy2 node = %+v", hy2)
	}
	vless := nodes[1]
	if vless.Type != "vless" || vless.Extra["uuid"] != "11111111-2222-3333-4444-555555555555" || vless.Extra["client-fingerprint"] != "chrome" {
		t.Fatalf("vless node = %+v", vless)
	}
}

func TestParseRejectsGarbage(t *testing.T) {
	if _, _, err := ParseSubscription([]byte("hello world")); err == nil {
		t.Fatal("garbage unexpectedly parsed")
	}
	if _, _, err := ParseSubscription([]byte("")); err == nil {
		t.Fatal("empty body unexpectedly parsed")
	}
}

func base64Encode(s string) string    { return stdBase64.EncodeToString([]byte(s)) }
func base64urlEncode(s string) string { return urlBase64.EncodeToString([]byte(s)) }

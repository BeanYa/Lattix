package extsub

import (
	"strings"
	"testing"
)

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
	t.Run("vmess link", func(t *testing.T) {
		nodes, format, err := ParseSubscription([]byte(vmessLink))
		if err != nil || format != "v2ray" {
			t.Fatalf("format %q err %v", format, err)
		}
		if len(nodes) != 1 {
			t.Fatalf("nodes = %+v", nodes)
		}
		if nodes[0].Type != "vmess" || nodes[0].Name != "vmess-01" || nodes[0].Server != "5.6.7.8" || nodes[0].Port != 443 {
			t.Fatalf("vmess node = %+v", nodes[0])
		}
	})
	t.Run("v2rayn all scheme-less", func(t *testing.T) {
		// v2rayN 自定义格式：全部为无 scheme 的 base64 JSON 行
		customLine := base64urlEncode(`{"v":"2","ps":"custom-01","add":"9.9.9.9","port":"8443","id":"id-id-id","net":"tcp","tls":"","type":"none"}`)
		customLine2 := base64urlEncode(`{"v":"2","ps":"custom-02","add":"8.8.8.8","port":"8444","id":"id2-id2-id2","net":"tcp","tls":"","type":"none"}`)
		nodes, format, err := ParseSubscription([]byte(customLine + "\n" + customLine2))
		if err != nil || format != "v2rayn" {
			t.Fatalf("format %q err %v", format, err)
		}
		if len(nodes) != 2 {
			t.Fatalf("nodes = %+v", nodes)
		}
		if nodes[0].Name != "custom-01" || nodes[0].Type != "vmess" || nodes[0].Server != "9.9.9.9" || nodes[0].Port != 8443 {
			t.Fatalf("custom node = %+v", nodes[0])
		}
		if nodes[1].Name != "custom-02" || nodes[1].Server != "8.8.8.8" || nodes[1].Port != 8444 {
			t.Fatalf("custom node = %+v", nodes[1])
		}
	})
}

func TestParseLinksV2rayNSingleEntry(t *testing.T) {
	entry := base64urlEncode(`{"v":"2","ps":"solo-01","add":"1.2.3.4","port":"8388","id":"solo-id","net":"tcp","tls":"","type":"none"}`)
	nodes, format, err := ParseSubscription([]byte(entry))
	if err != nil || format != "v2rayn" {
		t.Fatalf("format %q err %v", format, err)
	}
	if len(nodes) != 1 {
		t.Fatalf("nodes = %+v", nodes)
	}
	if nodes[0].Name != "solo-01" || nodes[0].Server != "1.2.3.4" || nodes[0].Port != 8388 {
		t.Fatalf("node = %+v", nodes[0])
	}
}

func TestParseLinksTrojanAndHysteria2(t *testing.T) {
	body := "trojan://pass@example.org:8443?type=ws&path=/p&sni=example.org#Trojan01\n" +
		"hysteria2://pass@hk.example.com:443?sni=hk.example.com&insecure=1#HK02"
	nodes, format, err := ParseSubscription([]byte(body))
	if err != nil || format != "v2ray" {
		t.Fatalf("format %q err %v", format, err)
	}
	if len(nodes) != 2 {
		t.Fatalf("nodes = %+v", nodes)
	}
	trojan := nodes[0]
	if trojan.Type != "trojan" || trojan.Server != "example.org" || trojan.Port != 8443 || trojan.Name != "Trojan01" {
		t.Fatalf("trojan node = %+v", trojan)
	}
	if trojan.Extra["password"] != "pass" || trojan.Extra["path"] != "/p" {
		t.Fatalf("trojan extra = %+v", trojan.Extra)
	}
	hy2 := nodes[1]
	if hy2.Type != "hysteria2" || hy2.Server != "hk.example.com" || hy2.Port != 443 || hy2.Name != "HK02" {
		t.Fatalf("hy2 node = %+v", hy2)
	}
	if hy2.Extra["password"] != "pass" || hy2.Extra["sni"] != "hk.example.com" || hy2.Extra["insecure"] != "1" {
		t.Fatalf("hy2 extra = %+v", hy2.Extra)
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

func TestParseLinksMoreProtocols(t *testing.T) {
	parseOne := func(t *testing.T, link string) Node {
		t.Helper()
		nodes, format, err := ParseSubscription([]byte(link))
		if err != nil || format != "v2ray" || len(nodes) != 1 {
			t.Fatalf("nodes %v format %q err %v", nodes, format, err)
		}
		return nodes[0]
	}

	t.Run("ssr", func(t *testing.T) {
		// 真实 ssr 链接：整个 server:port:protocol:method:obfs:base64(password)
		// 载荷整体 base64 编码（RawURLEncoding），remarks 参数亦为 base64。
		payload := base64urlEncode(
			"1.2.3.4:9000:origin:aes-128-cfb:plain:" + base64urlEncode("pass123"))
		link := "ssr://" + payload + "?obfsparam=abc&remarks=" + base64urlEncode("SSR-01")
		n := parseOne(t, link)
		if n.Type != "ssr" || n.Server != "1.2.3.4" || n.Port != 9000 || n.Name != "SSR-01" {
			t.Fatalf("node = %+v", n)
		}
		if n.Extra["method"] != "aes-128-cfb" || n.Extra["protocol"] != "origin" ||
			n.Extra["obfs"] != "plain" || n.Extra["password"] != "pass123" {
			t.Fatalf("extra = %+v", n.Extra)
		}
	})

	t.Run("tuic", func(t *testing.T) {
		link := "tuic://uuid-here:pass@example.com:443?sni=example.com&congestion_control=bbr#TUIC01"
		n := parseOne(t, link)
		if n.Type != "tuic" || n.Server != "example.com" || n.Port != 443 || n.Name != "TUIC01" {
			t.Fatalf("node = %+v", n)
		}
		if n.Extra["uuid"] != "uuid-here" || n.Extra["password"] != "pass" ||
			n.Extra["sni"] != "example.com" || n.Extra["congestion_control"] != "bbr" {
			t.Fatalf("extra = %+v", n.Extra)
		}
	})

	t.Run("anytls", func(t *testing.T) {
		link := "anytls://pass@example.com:8443?sni=example.com&insecure=1#ANY01"
		n := parseOne(t, link)
		if n.Type != "anytls" || n.Server != "example.com" || n.Port != 8443 || n.Name != "ANY01" {
			t.Fatalf("node = %+v", n)
		}
		if n.Extra["password"] != "pass" || n.Extra["sni"] != "example.com" || n.Extra["insecure"] != "1" {
			t.Fatalf("extra = %+v", n.Extra)
		}
	})

	t.Run("snell", func(t *testing.T) {
		link := "snell://psksecret@example.com:8388?obfs=http&obfs-host=example.com#SNELL01"
		n := parseOne(t, link)
		if n.Type != "snell" || n.Server != "example.com" || n.Port != 8388 || n.Name != "SNELL01" {
			t.Fatalf("node = %+v", n)
		}
		if n.Extra["psk"] != "psksecret" || n.Extra["obfs"] != "http" || n.Extra["obfs-host"] != "example.com" {
			t.Fatalf("extra = %+v", n.Extra)
		}
	})

	t.Run("socks", func(t *testing.T) {
		link := "socks://user1:pass1@example.com:1080#SOCKS01"
		n := parseOne(t, link)
		if n.Type != "socks" || n.Server != "example.com" || n.Port != 1080 || n.Name != "SOCKS01" {
			t.Fatalf("node = %+v", n)
		}
		if n.Extra["username"] != "user1" || n.Extra["password"] != "pass1" {
			t.Fatalf("extra = %+v", n.Extra)
		}
	})

	t.Run("wireguard", func(t *testing.T) {
		link := "wireguard://?address=10.0.0.2%2F32&private_key=priv&public_key=pub&endpoint=example.com:51820#WG01"
		n := parseOne(t, link)
		if n.Type != "wireguard" || n.Server != "example.com" || n.Port != 51820 || n.Name != "WG01" {
			t.Fatalf("node = %+v", n)
		}
		if n.Extra["private_key"] != "priv" || n.Extra["public_key"] != "pub" {
			t.Fatalf("extra = %+v", n.Extra)
		}
		if _, hasEndpoint := n.Extra["endpoint"]; hasEndpoint {
			t.Fatalf("endpoint should be consumed, extra = %+v", n.Extra)
		}
	})

	t.Run("hy2 alias normalized to hysteria2", func(t *testing.T) {
		n := parseOne(t, "hy2://pass@hy.example.com:443?sni=hy.example.com#HY03")
		if n.Type != "hysteria2" {
			t.Fatalf("type = %q, want hysteria2", n.Type)
		}
		if n.Extra["password"] != "pass" || n.Extra["sni"] != "hy.example.com" {
			t.Fatalf("extra = %+v", n.Extra)
		}
	})

	t.Run("vmess std base64 payload containing slash", func(t *testing.T) {
		// StdEncoding 可能产生 /，url.Parse 会把载荷拆进 Host 与 Path；
		// 解析器须按 u.Host + u.Path 拼接后解码。纯 ASCII 字节流的 base64
		// 不可能出现 /（需要 6 个连续 1 位），故 ps 值须含非 ASCII 字符。
		ps := "vmess-slash-01"
		encoded := ""
		for i := 0; i < 200; i++ {
			vmessJSON := `{"v":"2","ps":"` + ps + `","add":"5.6.7.8","port":"443","id":"aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee","aid":"0","scy":"auto","net":"ws","type":"none","host":"h.example.com","path":"/p","tls":"tls","sni":"h.example.com"}`
			encoded = base64Encode(vmessJSON)
			if strings.Contains(encoded, "/") {
				break
			}
			ps += string(rune(0x4E00 + i))
		}
		if !strings.Contains(encoded, "/") {
			t.Fatal("failed to craft a vmess payload whose std base64 contains /")
		}
		n := parseOne(t, "vmess://"+encoded)
		if n.Type != "vmess" || n.Server != "5.6.7.8" || n.Port != 443 {
			t.Fatalf("node = %+v", n)
		}
		if n.Extra["id"] != "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee" {
			t.Fatalf("extra = %+v", n.Extra)
		}
	})
}

func base64Encode(s string) string    { return stdBase64.EncodeToString([]byte(s)) }
func base64urlEncode(s string) string { return urlBase64.EncodeToString([]byte(s)) }

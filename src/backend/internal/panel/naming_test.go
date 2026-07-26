package panel

import "testing"

func TestResolveNodeNameTemplate(t *testing.T) {
	port := 8443
	got, err := resolveNameTemplate("{{LOCATION}}-{{PROTOCOL}}-{{TAG_1}}-{{PORT}}", nameTemplateValues{
		Location: "日本",
		ServerID: 7,
		Protocol: "vless",
		Port:     &port,
		Tags:     []string{"inbound"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if want := "日本-vless-inbound-8443"; got != want {
		t.Fatalf("解析结果 = %q，期望 %q", got, want)
	}
}

func TestResolveChainNameTemplate(t *testing.T) {
	got, err := resolveNameTemplate("{{ENTRY}}→{{EXIT}}-{{PROTOCOL}}-{{HOPS}}跳", nameTemplateValues{
		Location: "美国",
		ServerID: 1,
		Protocol: "vless",
		Entry:    "美国",
		EntryID:  1,
		Exit:     "日本",
		ExitID:   2,
		Hops:     2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if want := "美国→日本-vless-2跳"; got != want {
		t.Fatalf("解析结果 = %q，期望 %q", got, want)
	}
}

func TestResolveNameTemplateRejectsUnknownAndMissingTag(t *testing.T) {
	values := nameTemplateValues{Location: "日本", Protocol: "vless"}
	for _, tmpl := range []string{"{{UNKNOWN}}", "{{TAG_1}}"} {
		if _, err := resolveNameTemplate(tmpl, values); err == nil {
			t.Fatalf("模板 %q 应解析失败", tmpl)
		}
	}
}

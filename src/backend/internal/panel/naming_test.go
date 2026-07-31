package panel

import "testing"

func TestResolveNodeNameTemplate(t *testing.T) {
	port := 8443
	got, err := resolveNameTemplate("{{COUNTRY_FLAG}}{{LOCATION}}-{{TAG[0]}}-{{PORT}}", nameTemplateValues{
		Protocol: "vless",
		Port:     &port,
		Servers: []nameTemplateServer{{
			ID: 7, Name: "JP Hyper", CountryCode: "JP", Location: "Tokyo", Tags: []string{"inbound"},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if want := "🇯🇵Tokyo-inbound-8443"; got != want {
		t.Fatalf("解析结果 = %q，期望 %q", got, want)
	}
}

func TestResolveChainNameTemplate(t *testing.T) {
	got, err := resolveNameTemplate("{{ENTRY.NAME}}→{{HOP[1].COUNTRY_FLAG}}{{HOP[1].LOCATION}}→{{EXIT.COUNTRY_FLAG}}-{{HOPS}}跳", nameTemplateValues{
		Protocol: "vless",
		Servers: []nameTemplateServer{
			{ID: 1, Name: "US Entry", CountryCode: "US", Location: "Los Angeles"},
			{ID: 2, Name: "JP Exit", CountryCode: "JP", Location: "Tokyo"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if want := "US Entry→🇯🇵Tokyo→🇯🇵-2跳"; got != want {
		t.Fatalf("解析结果 = %q，期望 %q", got, want)
	}
}

func TestResolveChainNameTemplateRequiresServerScope(t *testing.T) {
	values := nameTemplateValues{Protocol: "vless", Servers: []nameTemplateServer{
		{ID: 1, Name: "US Entry", CountryCode: "US", Location: "Los Angeles", Tags: []string{"entry"}},
		{ID: 2, Name: "JP Exit", CountryCode: "JP", Location: "Tokyo"},
	}}
	for _, tmpl := range []string{
		"{{SERVER}}", "{{SERVER_ID}}", "{{NAME}}", "{{ID}}", "{{COUNTRY}}",
		"{{COUNTRY_CODE}}", "{{COUNTRY_FLAG}}", "{{LOCATION}}", "{{ADDRESS}}", "{{TAG[0]}}",
	} {
		if _, err := resolveNameTemplate(tmpl, values); err == nil {
			t.Fatalf("多跳模板 %q 的无作用域服务器变量应解析失败", tmpl)
		}
	}
}

func TestResolveNameTemplateRejectsUnknownAndMissingTag(t *testing.T) {
	values := nameTemplateValues{Protocol: "vless", Servers: []nameTemplateServer{{
		ID: 1, Name: "JP", CountryCode: "JP", Location: "Tokyo",
	}}}
	for _, tmpl := range []string{"{{UNKNOWN}}", "{{TAG[0]}}", "{{ENTRY.UNKNOWN}}", "{{HOP[1].NAME}}"} {
		if _, err := resolveNameTemplate(tmpl, values); err == nil {
			t.Fatalf("模板 %q 应解析失败", tmpl)
		}
	}
}

func TestResolveDirectEntryExitAliases(t *testing.T) {
	values := nameTemplateValues{Servers: []nameTemplateServer{{
		ID: 7, Name: "only", CountryCode: "SG", Location: "Singapore",
	}}}
	got, err := resolveNameTemplate("{{ENTRY.NAME}}={{EXIT.NAME}}={{HOP[0].NAME}}", values)
	if err != nil {
		t.Fatal(err)
	}
	if got != "only=only=only" {
		t.Fatalf("解析结果 = %q", got)
	}
}

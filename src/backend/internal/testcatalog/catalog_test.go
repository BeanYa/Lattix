package testcatalog

import "testing"

func TestLoadStaticCatalogs(t *testing.T) {
	catalog, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog.Education) != 31 || len(catalog.Speed) != 14 || len(catalog.Connectivity) != 76 ||
		len(catalog.Hashes["education"]) != 64 || len(catalog.Hashes["speed"]) != 64 ||
		len(catalog.Hashes["connectivity"]) != 64 {
		t.Fatalf("unexpected catalog: %+v", catalog)
	}
	ookla := 0
	for _, target := range catalog.Speed {
		if target.OoklaServerID != "" {
			ookla++
		}
	}
	if ookla != 12 {
		t.Fatalf("ookla speed targets = %d, want 12", ookla)
	}
	groups := make(map[string]int)
	hosts := make(map[string]struct{})
	for _, target := range catalog.Connectivity {
		groups[target.Group]++
		if _, exists := hosts[target.Host]; exists {
			t.Fatalf("duplicate connectivity host %q", target.Host)
		}
		hosts[target.Host] = struct{}{}
	}
	// 76 = ip.net.coffee 48 站点 ∪ TcpQuality 国际站点（同品牌去重）。
	if groups["cn"] != 12 || groups["jp"] != 4 || groups["us"] != 32 || groups["gl"] != 28 {
		t.Fatalf("connectivity group counts = %v, want cn=12 jp=4 us=32 gl=28", groups)
	}
}

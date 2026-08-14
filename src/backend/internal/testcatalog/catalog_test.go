package testcatalog

import "testing"

func TestLoadStaticCatalogs(t *testing.T) {
	catalog, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog.International) != 44 || len(catalog.Education) != 31 || len(catalog.Speed) != 14 ||
		len(catalog.Hashes["international"]) != 64 || len(catalog.Hashes["education"]) != 64 || len(catalog.Hashes["speed"]) != 64 {
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
}

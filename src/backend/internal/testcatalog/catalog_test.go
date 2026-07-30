package testcatalog

import "testing"

func TestLoadStaticCatalogs(t *testing.T) {
	catalog, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog.International) != 44 || len(catalog.Education) != 31 || len(catalog.Speed) != 11 ||
		len(catalog.Hashes["international"]) != 64 || len(catalog.Hashes["education"]) != 64 || len(catalog.Hashes["speed"]) != 64 {
		t.Fatalf("unexpected catalog: %+v", catalog)
	}
}

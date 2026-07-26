package panel

import "testing"

func TestNormalizeServerGeography(t *testing.T) {
	code, location, err := normalizeServerGeography(" jp ", " Tokyo ")
	if err != nil {
		t.Fatal(err)
	}
	if code != "JP" || location != "Tokyo" {
		t.Fatalf("normalize = %q/%q", code, location)
	}
	if got := countryFlag(code); got != "🇯🇵" {
		t.Fatalf("countryFlag = %q", got)
	}
	if got := countryName(code); got == "" {
		t.Fatal("countryName 不能为空")
	}
}

func TestNormalizeServerGeographyRejectsMissingOrUnknown(t *testing.T) {
	for _, tc := range []struct {
		code     string
		location string
	}{
		{"", "Tokyo"},
		{"ZZ", "Unknown"},
		{"JP", ""},
	} {
		if _, _, err := normalizeServerGeography(tc.code, tc.location); err == nil {
			t.Fatalf("%q/%q 应校验失败", tc.code, tc.location)
		}
	}
}

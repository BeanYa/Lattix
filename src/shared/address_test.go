package shared

import "testing"

func TestAddressFamily(t *testing.T) {
	cases := map[string]string{
		"1.2.3.4":        AddressFamilyIPv4,
		"203.0.113.7":    AddressFamilyIPv4,
		"2400:cb00::1":   AddressFamilyIPv6,
		"::1":            AddressFamilyIPv6,
		"2001:db8::ffff": AddressFamilyIPv6,
		"example.com":    AddressFamilyDomain,
		"v6.example.com": AddressFamilyDomain,
		"":               AddressFamilyDomain,
		"1.2.3.456":      AddressFamilyDomain, // 非法 IPv4 按域名处理
	}
	for addr, want := range cases {
		if got := AddressFamily(addr); got != want {
			t.Errorf("AddressFamily(%q) = %q, want %q", addr, got, want)
		}
	}
}

func TestHostPort(t *testing.T) {
	cases := []struct {
		addr string
		port int
		want string
	}{
		{"1.2.3.4", 443, "1.2.3.4:443"},
		{"2400:cb00::1", 443, "[2400:cb00::1]:443"},
		{"example.com", 8443, "example.com:8443"},
	}
	for _, c := range cases {
		if got := HostPort(c.addr, c.port); got != c.want {
			t.Errorf("HostPort(%q, %d) = %q, want %q", c.addr, c.port, got, c.want)
		}
	}
}

func TestDedupeAddresses(t *testing.T) {
	got := DedupeAddresses([]string{"1.2.3.4", "2400::1", "", "1.2.3.4", "5.6.7.8", "2400::1"})
	want := []string{"1.2.3.4", "2400::1", "5.6.7.8"}
	if len(got) != len(want) {
		t.Fatalf("DedupeAddresses = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("DedupeAddresses = %v, want %v", got, want)
		}
	}
	if got := DedupeAddresses(nil); len(got) != 0 {
		t.Errorf("DedupeAddresses(nil) = %v, want empty", got)
	}
}

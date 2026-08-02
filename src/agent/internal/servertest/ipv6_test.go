package servertest

import (
	"errors"
	"net"
	"net/netip"
	"testing"
)

var errTestInterfaces = errors.New("interfaces unavailable")

func TestUsableGlobalIPv6(t *testing.T) {
	cases := []struct {
		ip   string
		want bool
	}{
		{"2400:3200::1", true},
		{"2606:4700::1111", true},
		{"2001:db8::1", true},
		{"::1", false},
		{"fe80::1", false},
		{"fc00::1", false},
		{"fd12:3456:789a::1", false},
		{"::ffff:192.0.2.1", false},
		{"192.168.1.1", false},
		{"", false},
	}
	for _, tc := range cases {
		ip, err := netip.ParseAddr(tc.ip)
		if err != nil {
			ip = netip.Addr{}
		}
		if got := usableGlobalIPv6(ip); got != tc.want {
			t.Errorf("usableGlobalIPv6(%q) = %v, want %v", tc.ip, got, tc.want)
		}
	}
}

func TestHasGlobalIPv6(t *testing.T) {
	originalList, originalAddrs := listInterfaces, interfaceAddresses
	defer func() { listInterfaces, interfaceAddresses = originalList, originalAddrs }()

	t.Run("global address present", func(t *testing.T) {
		listInterfaces = func() ([]net.Interface, error) {
			return []net.Interface{{Index: 1, Name: "eth0"}, {Index: 2, Name: "lo"}}, nil
		}
		interfaceAddresses = func(iface net.Interface) ([]net.Addr, error) {
			if iface.Name == "lo" {
				return []net.Addr{&net.IPAddr{IP: net.ParseIP("::1")}}, nil
			}
			return []net.Addr{&net.IPAddr{IP: net.ParseIP("2400:3200::1")}}, nil
		}
		if !hasGlobalIPv6() {
			t.Error("hasGlobalIPv6() = false, want true")
		}
	})

	t.Run("link-local and ula only", func(t *testing.T) {
		listInterfaces = func() ([]net.Interface, error) {
			return []net.Interface{{Index: 1, Name: "eth0"}}, nil
		}
		interfaceAddresses = func(iface net.Interface) ([]net.Addr, error) {
			return []net.Addr{
				&net.IPAddr{IP: net.ParseIP("fe80::1")},
				&net.IPAddr{IP: net.ParseIP("fd12:3456::1")},
			}, nil
		}
		if hasGlobalIPv6() {
			t.Error("hasGlobalIPv6() = true, want false")
		}
	})

	t.Run("global address present as ipnet", func(t *testing.T) {
		listInterfaces = func() ([]net.Interface, error) {
			return []net.Interface{{Index: 1, Name: "eth0"}}, nil
		}
		interfaceAddresses = func(iface net.Interface) ([]net.Addr, error) {
			return []net.Addr{&net.IPNet{IP: net.ParseIP("2400:3200::1"), Mask: net.CIDRMask(64, 128)}}, nil
		}
		if !hasGlobalIPv6() {
			t.Error("hasGlobalIPv6() = false, want true")
		}
	})

	t.Run("address enumeration error", func(t *testing.T) {
		listInterfaces = func() ([]net.Interface, error) {
			return []net.Interface{{Index: 1, Name: "eth0"}}, nil
		}
		interfaceAddresses = func(iface net.Interface) ([]net.Addr, error) { return nil, errTestInterfaces }
		if hasGlobalIPv6() {
			t.Error("hasGlobalIPv6() = true, want false")
		}
	})

	t.Run("interface enumeration error", func(t *testing.T) {
		listInterfaces = func() ([]net.Interface, error) { return nil, errTestInterfaces }
		interfaceAddresses = func(iface net.Interface) ([]net.Addr, error) { return nil, nil }
		if hasGlobalIPv6() {
			t.Error("hasGlobalIPv6() = true, want false")
		}
	})
}

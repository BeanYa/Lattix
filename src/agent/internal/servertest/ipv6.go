package servertest

import (
	"net"
	"net/netip"
)

var (
	listInterfaces     = func() ([]net.Interface, error) { return net.Interfaces() }
	interfaceAddresses = func(iface net.Interface) ([]net.Addr, error) { return iface.Addrs() }
)

// hasGlobalIPv6 reports whether the machine has at least one usable global
// unicast IPv6 address (excluding loopback, link-local, ULA and IPv4-mapped).
func hasGlobalIPv6() bool {
	ifaces, err := listInterfaces()
	if err != nil {
		return false
	}
	for _, iface := range ifaces {
		addrs, err := interfaceAddresses(iface)
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			if usableGlobalIPv6(addrFromNetAddr(addr)) {
				return true
			}
		}
	}
	return false
}

func addrFromNetAddr(addr net.Addr) netip.Addr {
	switch value := addr.(type) {
	case *net.IPNet:
		ip, _ := netip.AddrFromSlice(value.IP)
		return ip
	case *net.IPAddr:
		ip, _ := netip.AddrFromSlice(value.IP)
		return ip
	}
	return netip.Addr{}
}

// usableGlobalIPv6 accepts global unicast IPv6 addresses that are neither
// loopback, link-local, ULA nor IPv4-mapped.
func usableGlobalIPv6(ip netip.Addr) bool {
	return ip.Is6() && !ip.Is4In6() && ip.IsGlobalUnicast() && !ip.IsLinkLocalUnicast() && !ip.IsPrivate()
}

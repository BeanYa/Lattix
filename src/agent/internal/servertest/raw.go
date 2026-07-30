//go:build linux

package servertest

import (
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"time"

	"golang.org/x/sys/unix"
)

var errRawPermission = errors.New("raw socket permission denied")

type rawProber struct {
	fd          int
	family      int
	source      netip.Addr
	destination netip.Addr
	destPort    int
}

func rawSocketCapability() error {
	fd, err := unix.Socket(unix.AF_INET, unix.SOCK_RAW|unix.SOCK_CLOEXEC, unix.IPPROTO_TCP)
	if err != nil {
		if errors.Is(err, unix.EPERM) || errors.Is(err, unix.EACCES) {
			return errRawPermission
		}
		return err
	}
	return unix.Close(fd)
}

func newRawProber(destination netip.Addr, port int) (*rawProber, error) {
	destination = destination.Unmap()
	family := unix.AF_INET
	network := "udp4"
	remote := &net.UDPAddr{IP: net.IP(destination.AsSlice()), Port: port}
	if destination.Is6() {
		family, network = unix.AF_INET6, "udp6"
	}
	connection, err := net.DialUDP(network, nil, remote)
	if err != nil {
		return nil, fmt.Errorf("select raw probe source: %w", err)
	}
	local := connection.LocalAddr().(*net.UDPAddr)
	_ = connection.Close()
	source, ok := netip.AddrFromSlice(local.IP)
	if !ok {
		return nil, errors.New("select raw probe source: invalid local address")
	}
	source = source.Unmap()
	fd, err := unix.Socket(family, unix.SOCK_RAW|unix.SOCK_CLOEXEC, unix.IPPROTO_TCP)
	if err != nil {
		if errors.Is(err, unix.EPERM) || errors.Is(err, unix.EACCES) {
			return nil, errRawPermission
		}
		return nil, err
	}
	if family == unix.AF_INET6 {
		if err := unix.SetsockoptInt(fd, unix.IPPROTO_IPV6, unix.IPV6_CHECKSUM, 16); err != nil {
			_ = unix.Close(fd)
			return nil, fmt.Errorf("enable IPv6 TCP checksum: %w", err)
		}
	}
	return &rawProber{fd: fd, family: family, source: source, destination: destination, destPort: port}, nil
}

func (p *rawProber) Close() error { return unix.Close(p.fd) }

func (p *rawProber) Probe(packetSize int, timeout time.Duration) (string, time.Duration, error) {
	sourcePort, sequence, err := randomProbeIdentity()
	if err != nil {
		return "", 0, err
	}
	ipHeaderSize := 20
	if p.destination.Is6() {
		ipHeaderSize = 40
	}
	payloadSize := 0
	if packetSize > ipHeaderSize+20 {
		payloadSize = packetSize - ipHeaderSize - 20
	}
	segment := make([]byte, 20+payloadSize)
	binary.BigEndian.PutUint16(segment[0:2], sourcePort)
	binary.BigEndian.PutUint16(segment[2:4], uint16(p.destPort))
	binary.BigEndian.PutUint32(segment[4:8], sequence)
	segment[12] = 5 << 4
	segment[13] = 0x02 // SYN
	binary.BigEndian.PutUint16(segment[14:16], 64240)
	binary.BigEndian.PutUint16(segment[16:18], tcpChecksum(p.source, p.destination, segment))

	var sockaddr unix.Sockaddr
	if p.family == unix.AF_INET {
		value := p.destination.As4()
		sockaddr = &unix.SockaddrInet4{Port: p.destPort, Addr: value}
	} else {
		value := p.destination.As16()
		sockaddr = &unix.SockaddrInet6{Port: p.destPort, Addr: value}
	}
	started := time.Now()
	if err := unix.Sendto(p.fd, segment, 0, sockaddr); err != nil {
		return "", 0, fmt.Errorf("send raw SYN: %w", err)
	}
	deadline := started.Add(timeout)
	buffer := make([]byte, 64<<10)
	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return "", 0, nil
		}
		milliseconds := int(remaining.Milliseconds())
		if milliseconds < 1 {
			milliseconds = 1
		}
		poll := []unix.PollFd{{Fd: int32(p.fd), Events: unix.POLLIN}}
		count, err := unix.Poll(poll, milliseconds)
		if err != nil {
			if errors.Is(err, unix.EINTR) {
				continue
			}
			return "", 0, fmt.Errorf("wait raw SYN response: %w", err)
		}
		if count == 0 {
			return "", 0, nil
		}
		length, from, err := unix.Recvfrom(p.fd, buffer, 0)
		if err != nil {
			if errors.Is(err, unix.EINTR) || errors.Is(err, unix.EAGAIN) {
				continue
			}
			return "", 0, fmt.Errorf("receive raw SYN response: %w", err)
		}
		if !sockaddrMatches(from, p.destination) {
			continue
		}
		segmentOffset := 0
		if length >= 20 && buffer[0]>>4 == 4 {
			segmentOffset = int(buffer[0]&0x0f) * 4
		} else if length >= 40 && buffer[0]>>4 == 6 {
			segmentOffset = 40
		}
		if length < segmentOffset+20 {
			continue
		}
		tcp := buffer[segmentOffset:length]
		if binary.BigEndian.Uint16(tcp[0:2]) != uint16(p.destPort) || binary.BigEndian.Uint16(tcp[2:4]) != sourcePort {
			continue
		}
		flags := tcp[13]
		if flags&0x12 == 0x12 {
			return "syn_ack", time.Since(started), nil
		}
		if flags&0x04 != 0 {
			return "rst", time.Since(started), nil
		}
	}
}

func randomProbeIdentity() (uint16, uint32, error) {
	var raw [6]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return 0, 0, err
	}
	port := uint16(32768 + binary.BigEndian.Uint16(raw[0:2])%28231)
	return port, binary.BigEndian.Uint32(raw[2:6]), nil
}

func sockaddrMatches(sockaddr unix.Sockaddr, expected netip.Addr) bool {
	switch value := sockaddr.(type) {
	case *unix.SockaddrInet4:
		return expected.Is4() && value.Addr == expected.As4()
	case *unix.SockaddrInet6:
		return expected.Is6() && value.Addr == expected.As16()
	default:
		return false
	}
}

func tcpChecksum(source, destination netip.Addr, segment []byte) uint16 {
	length := len(segment)
	pseudoLength := 12
	if source.Is6() {
		pseudoLength = 40
	}
	buffer := make([]byte, pseudoLength+length)
	if source.Is4() {
		source4, destination4 := source.As4(), destination.As4()
		copy(buffer[0:4], source4[:])
		copy(buffer[4:8], destination4[:])
		buffer[9] = unix.IPPROTO_TCP
		binary.BigEndian.PutUint16(buffer[10:12], uint16(length))
	} else {
		source6, destination6 := source.As16(), destination.As16()
		copy(buffer[0:16], source6[:])
		copy(buffer[16:32], destination6[:])
		binary.BigEndian.PutUint32(buffer[32:36], uint32(length))
		buffer[39] = unix.IPPROTO_TCP
	}
	copy(buffer[pseudoLength:], segment)
	var sum uint32
	for index := 0; index+1 < len(buffer); index += 2 {
		sum += uint32(binary.BigEndian.Uint16(buffer[index : index+2]))
	}
	if len(buffer)%2 != 0 {
		sum += uint32(buffer[len(buffer)-1]) << 8
	}
	for sum>>16 != 0 {
		sum = sum&0xffff + sum>>16
	}
	return ^uint16(sum)
}

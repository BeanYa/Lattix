//go:build linux

package servertest

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"net/netip"
	"time"

	"golang.org/x/sys/unix"
)

func traceUDPErrorQueue(ctx context.Context, destination netip.Addr, maxHops, probesPerHop int, probeTimeout time.Duration, maxSilentHops int) ([]map[string]any, bool, error) {
	destination = destination.Unmap()
	family, protocol, recvErrOption := unix.AF_INET, unix.IPPROTO_IP, unix.IP_RECVERR
	if destination.Is6() {
		family, protocol, recvErrOption = unix.AF_INET6, unix.IPPROTO_IPV6, unix.IPV6_RECVERR
	}
	fd, err := unix.Socket(family, unix.SOCK_DGRAM|unix.SOCK_CLOEXEC|unix.SOCK_NONBLOCK, unix.IPPROTO_UDP)
	if err != nil {
		return nil, false, err
	}
	defer unix.Close(fd)
	if err := unix.SetsockoptInt(fd, protocol, recvErrOption, 1); err != nil {
		return nil, false, fmt.Errorf("enable UDP error queue: %w", err)
	}
	if destination.Is4() {
		value := destination.As4()
		err = unix.Connect(fd, &unix.SockaddrInet4{Port: 33434, Addr: value})
	} else {
		value := destination.As16()
		err = unix.Connect(fd, &unix.SockaddrInet6{Port: 33434, Addr: value})
	}
	if err != nil {
		return nil, false, fmt.Errorf("connect UDP traceroute socket: %w", err)
	}
	var hops []map[string]any
	silent := 0
	for ttl := 1; ttl <= maxHops; ttl++ {
		if err := ctx.Err(); err != nil {
			return hops, false, classifyCtxError(err)
		}
		if destination.Is4() {
			err = unix.SetsockoptInt(fd, unix.IPPROTO_IP, unix.IP_TTL, ttl)
		} else {
			err = unix.SetsockoptInt(fd, unix.IPPROTO_IPV6, unix.IPV6_UNICAST_HOPS, ttl)
		}
		if err != nil {
			return hops, false, fmt.Errorf("set traceroute hop limit: %w", err)
		}
		hop := map[string]any{"hop": ttl, "probes": probesPerHop}
		var address string
		var rtts []float64
		timeouts := 0
		reached := false
		for probe := 0; probe < probesPerHop; probe++ {
			drainErrorQueue(fd)
			payload := []byte{byte(ttl), byte(probe), 0x4c, 0x58}
			started := time.Now()
			if _, err := unix.Write(fd, payload); err != nil && !errors.Is(err, unix.ECONNREFUSED) {
				return hops, false, fmt.Errorf("send traceroute probe: %w", err)
			}
			offender, terminal, ok, err := waitUDPError(ctx, fd, destination.Is6(), probeTimeout)
			if err != nil {
				if reached {
					return appendHop(hops, hop, address, rtts, timeouts), true, nil
				}
				return appendHop(hops, hop, address, rtts, timeouts), false, classifyCtxError(err)
			}
			if !ok {
				timeouts++
				continue
			}
			if offender.IsValid() {
				address = offender.String()
			}
			rtts = append(rtts, float64(time.Since(started).Microseconds())/1000)
			reached = reached || terminal
		}
		if timeouts == probesPerHop {
			silent++
		} else {
			silent = 0
		}
		hops = appendHop(hops, hop, address, rtts, timeouts)
		if reached {
			return hops, true, nil
		}
		if silent >= maxSilentHops {
			return hops, false, errRouteSilentLimit
		}
	}
	return hops, false, nil
}

func appendHop(hops []map[string]any, hop map[string]any, address string, rtts []float64, timeouts int) []map[string]any {
	if address != "" {
		hop["address"] = address
	}
	hop["timeouts"] = timeouts
	if len(rtts) > 0 {
		hop["rtt_ms"] = rtts
	}
	return append(hops, hop)
}

// classifyCtxError maps a probe context timeout to the incomplete-report
// sentinel so a deadline mid-trace keeps the collected hops; other context
// outcomes (e.g. task cancellation) pass through as probe failures.
func classifyCtxError(err error) error {
	if errors.Is(err, context.DeadlineExceeded) {
		return errRouteProbeDeadline
	}
	return err
}

func waitUDPError(ctx context.Context, fd int, ipv6 bool, timeout time.Duration) (netip.Addr, bool, bool, error) {
	deadline := time.Now().Add(timeout)
	for {
		if err := ctx.Err(); err != nil {
			return netip.Addr{}, false, false, err
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return netip.Addr{}, false, false, nil
		}
		poll := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLERR}}
		count, err := unix.Poll(poll, max(1, int(remaining.Milliseconds())))
		if err != nil {
			if errors.Is(err, unix.EINTR) {
				continue
			}
			return netip.Addr{}, false, false, err
		}
		if count == 0 {
			return netip.Addr{}, false, false, nil
		}
		buffer, control := make([]byte, 256), make([]byte, 512)
		_, controlLength, _, _, err := unix.Recvmsg(fd, buffer, control, unix.MSG_ERRQUEUE)
		if err != nil {
			if errors.Is(err, unix.EAGAIN) {
				continue
			}
			return netip.Addr{}, false, false, err
		}
		messages, err := unix.ParseSocketControlMessage(control[:controlLength])
		if err != nil {
			return netip.Addr{}, false, false, err
		}
		for _, message := range messages {
			if len(message.Data) < 24 {
				continue
			}
			icmpType, icmpCode := message.Data[5], message.Data[6]
			offender := parseErrorQueueOffender(message.Data[16:], ipv6)
			terminal := (!ipv6 && icmpType == 3 && icmpCode == 3) || (ipv6 && icmpType == 1 && icmpCode == 4)
			return offender, terminal, true, nil
		}
	}
}

func parseErrorQueueOffender(data []byte, ipv6 bool) netip.Addr {
	if len(data) < 8 {
		return netip.Addr{}
	}
	family := binary.LittleEndian.Uint16(data[0:2])
	if !ipv6 && family == unix.AF_INET && len(data) >= 8 {
		return netip.AddrFrom4([4]byte{data[4], data[5], data[6], data[7]})
	}
	if ipv6 && family == unix.AF_INET6 && len(data) >= 24 {
		var address [16]byte
		copy(address[:], data[8:24])
		return netip.AddrFrom16(address)
	}
	return netip.Addr{}
}

func drainErrorQueue(fd int) {
	for {
		_, _, _, _, err := unix.Recvmsg(fd, make([]byte, 64), make([]byte, 128), unix.MSG_ERRQUEUE|unix.MSG_DONTWAIT)
		if err != nil {
			return
		}
	}
}

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

// traceTCPErrorQueue runs a TCP SYN traceroute: every probe is a non-blocking
// connect to the destination's service port with a bounded TTL. Intermediate
// ICMP time-exceeded messages arrive through the socket error queue with the
// offender address; a completed handshake (or RST) means the destination was
// reached. TCP probing survives carrier filtering far better than UDP.
func traceTCPErrorQueue(ctx context.Context, destination netip.Addr, port uint16, maxHops, probesPerHop int, probeTimeout time.Duration, maxSilentHops int) ([]map[string]any, bool, error) {
	destination = destination.Unmap()
	var hops []map[string]any
	silent := 0
	answered := false
	for ttl := 1; ttl <= maxHops; ttl++ {
		if err := ctx.Err(); err != nil {
			return hops, false, classifyCtxError(err)
		}
		hop := map[string]any{"hop": ttl, "probes": probesPerHop}
		var address string
		var rtts []float64
		timeouts := 0
		reached := false
		for probe := 0; probe < probesPerHop; probe++ {
			offender, terminal, rttMS, ok, err := probeTCPHop(ctx, destination, port, ttl, probeTimeout)
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
			rtts = append(rtts, rttMS)
			reached = reached || terminal
		}
		if timeouts == probesPerHop {
			silent++
		} else {
			silent = 0
			answered = true
		}
		hops = appendHop(hops, hop, address, rtts, timeouts)
		if reached {
			return hops, true, nil
		}
		// Bail out early only when nothing has ever answered: the path is
		// almost certainly filtered end to end. Silent stretches after a
		// responding hop (carrier cores hiding hops) must not abort the
		// trace before the destination can answer.
		if silent >= maxSilentHops && !answered {
			return hops, false, errRouteSilentLimit
		}
	}
	return hops, false, nil
}

// probeTCPHop sends one TTL-limited SYN and waits for the error queue, the
// handshake, or the deadline. The bool result reports whether anything
// answered; rttMS is the send-to-answer time.
func probeTCPHop(ctx context.Context, destination netip.Addr, port uint16, ttl int, timeout time.Duration) (netip.Addr, bool, float64, bool, error) {
	ipv6 := destination.Is6()
	family, protocol, recvErrOption, hopOption := unix.AF_INET, unix.IPPROTO_IP, unix.IP_RECVERR, unix.IP_TTL
	if ipv6 {
		family, protocol, recvErrOption, hopOption = unix.AF_INET6, unix.IPPROTO_IPV6, unix.IPV6_RECVERR, unix.IPV6_UNICAST_HOPS
	}
	fd, err := unix.Socket(family, unix.SOCK_STREAM|unix.SOCK_CLOEXEC|unix.SOCK_NONBLOCK, unix.IPPROTO_TCP)
	if err != nil {
		return netip.Addr{}, false, 0, false, err
	}
	defer unix.Close(fd)
	if err := unix.SetsockoptInt(fd, protocol, recvErrOption, 1); err != nil {
		return netip.Addr{}, false, 0, false, fmt.Errorf("enable TCP error queue: %w", err)
	}
	if err := unix.SetsockoptInt(fd, protocol, hopOption, ttl); err != nil {
		return netip.Addr{}, false, 0, false, fmt.Errorf("set traceroute hop limit: %w", err)
	}
	var sockaddr unix.Sockaddr
	if ipv6 {
		value := destination.As16()
		sockaddr = &unix.SockaddrInet6{Port: int(port), Addr: value}
	} else {
		value := destination.As4()
		sockaddr = &unix.SockaddrInet4{Port: int(port), Addr: value}
	}
	started := time.Now()
	if err := unix.Connect(fd, sockaddr); err != nil && !errors.Is(err, unix.EINPROGRESS) {
		return netip.Addr{}, false, 0, false, fmt.Errorf("connect TCP traceroute socket: %w", err)
	}
	deadline := time.Now().Add(timeout)
	for {
		if err := ctx.Err(); err != nil {
			return netip.Addr{}, false, 0, false, err
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return netip.Addr{}, false, 0, false, nil
		}
		poll := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLERR | unix.POLLOUT}}
		count, err := unix.Poll(poll, max(1, int(remaining.Milliseconds())))
		if err != nil {
			if errors.Is(err, unix.EINTR) {
				continue
			}
			return netip.Addr{}, false, 0, false, err
		}
		if count == 0 {
			return netip.Addr{}, false, 0, false, nil
		}
		rttMS := float64(time.Since(started).Microseconds()) / 1000
		// The error queue carries the ICMP offender address, so it wins
		// over the pending socket error it also raises.
		offender, terminal, ok, err := readTCPErrorQueue(fd, ipv6)
		if err != nil {
			return netip.Addr{}, false, 0, false, err
		}
		if ok {
			return offender, terminal, rttMS, true, nil
		}
		soerr, err := unix.GetsockoptInt(fd, unix.SOL_SOCKET, unix.SO_ERROR)
		if err != nil {
			return netip.Addr{}, false, 0, false, err
		}
		switch {
		case soerr == 0, soerr == int(unix.ECONNREFUSED):
			// SYN-ACK completed the handshake; RST means the destination
			// host answered. Both prove the path reached the target.
			return destination, true, rttMS, true, nil
		default:
			// EHOSTUNREACH and friends without error-queue data carry no
			// usable hop address; treat the probe as unanswered.
			return netip.Addr{}, false, 0, false, nil
		}
	}
}

// readTCPErrorQueue drains one extended error from the socket. terminal is
// true when the destination itself answered with an ICMP unreachable.
func readTCPErrorQueue(fd int, ipv6 bool) (netip.Addr, bool, bool, error) {
	buffer, control := make([]byte, 256), make([]byte, 512)
	_, controlLength, _, _, err := unix.Recvmsg(fd, buffer, control, unix.MSG_ERRQUEUE)
	if err != nil {
		if errors.Is(err, unix.EAGAIN) {
			return netip.Addr{}, false, false, nil
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
		icmpType := message.Data[5]
		offender := parseErrorQueueOffender(message.Data[16:], ipv6)
		terminal := (!ipv6 && icmpType == 3) || (ipv6 && icmpType == 1)
		return offender, terminal, true, nil
	}
	return netip.Addr{}, false, false, nil
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

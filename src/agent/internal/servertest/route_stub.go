//go:build !linux

package servertest

import (
	"context"
	"errors"
	"net/netip"
	"time"
)

func traceUDPErrorQueue(context.Context, netip.Addr, int, int, time.Duration) ([]map[string]any, bool, error) {
	return nil, false, errors.New("UDP error queue traceroute is unsupported on this platform")
}

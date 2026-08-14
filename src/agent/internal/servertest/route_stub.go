//go:build !linux

package servertest

import (
	"context"
	"errors"
	"net/netip"
	"time"
)

func traceTCPErrorQueue(context.Context, netip.Addr, uint16, int, int, time.Duration, int) ([]map[string]any, bool, error) {
	return nil, false, errors.New("TCP error queue traceroute is unsupported on this platform")
}

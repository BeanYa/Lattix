//go:build !linux

package servertest

import (
	"errors"
	"net/netip"
	"time"
)

var errRawPermission = errors.New("raw socket permission denied")

type rawProber struct{}

func rawSocketCapability() error { return errors.New("raw sockets are unsupported on this platform") }
func newRawProber(netip.Addr, int) (*rawProber, error) {
	return nil, errors.New("raw sockets are unsupported on this platform")
}
func (p *rawProber) Close() error { return nil }
func (p *rawProber) Probe(int, time.Duration) (string, time.Duration, error) {
	return "", 0, errors.New("raw sockets are unsupported on this platform")
}

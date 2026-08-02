package servertest

import (
	"context"
	"fmt"
	"net/netip"
	"strings"
	"testing"

	"lattix/shared"
)

func TestApplyProbeSamplesMarksZeroResponses(t *testing.T) {
	result := tcpTargetResult{Sent: 30}
	applyProbeSamples(&result, 30, nil)
	if result.Received != 0 || result.LossPercent != 100 || result.ErrorCode != "probe_no_response" {
		t.Fatalf("unexpected zero-response result: %+v", result)
	}
}

func TestApplyProbeSamplesPreservesExistingProbeError(t *testing.T) {
	result := tcpTargetResult{Sent: 30, ErrorCode: "raw_probe_failed", ErrorMessage: "socket closed"}
	applyProbeSamples(&result, 30, nil)
	if result.ErrorCode != "raw_probe_failed" || result.ErrorMessage != "socket closed" {
		t.Fatalf("probe error was replaced: %+v", result)
	}
}

func TestApplyProbeSamplesMarksNoProbeWindow(t *testing.T) {
	result := tcpTargetResult{}
	applyProbeSamples(&result, 0, nil)
	if result.ErrorCode != "probe_not_run" || result.LossPercent != 0 {
		t.Fatalf("unexpected no-probe result: %+v", result)
	}
}

func TestResolvePublicTargetUsesFamilyLookupNetwork(t *testing.T) {
	tests := []struct {
		name    string
		family  shared.ServerTestAddressFamily
		wantNet string
	}{
		{name: "ipv4", family: shared.ServerTestIPv4, wantNet: "ip4"},
		{name: "ipv6", family: shared.ServerTestIPv6, wantNet: "ip6"},
		{name: "dualstack", family: shared.ServerTestDualStack, wantNet: "ip4"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var gotNet string
			address, err := resolvePublicTargetWithLookup(context.Background(), shared.ServerTestTarget{
				Host: "probe.example", AddressFamily: test.family,
			}, func(_ context.Context, network, _ string) ([]netip.Addr, error) {
				gotNet = network
				return []netip.Addr{netip.MustParseAddr("192.0.2.10")}, nil
			})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if gotNet != test.wantNet {
				t.Fatalf("lookup network = %q, want %q", gotNet, test.wantNet)
			}
			if address != netip.MustParseAddr("192.0.2.10") {
				t.Fatalf("address = %v", address)
			}
		})
	}
}

func TestResolvePublicTargetRejectsInvalidLookupNetwork(t *testing.T) {
	_, err := resolvePublicTargetWithLookup(context.Background(), shared.ServerTestTarget{
		Host: "probe.example", AddressFamily: shared.ServerTestIPv4,
	}, func(_ context.Context, network, _ string) ([]netip.Addr, error) {
		return nil, fmt.Errorf("unknown network %s", network)
	})
	if err == nil {
		t.Fatal("expected error for invalid lookup network")
	}
}

func TestResolvePublicTargetFiltersNonPublicAddress(t *testing.T) {
	_, err := resolvePublicTargetWithLookup(context.Background(), shared.ServerTestTarget{
		Host: "probe.example", AddressFamily: shared.ServerTestIPv4,
	}, func(_ context.Context, _, _ string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("127.0.0.1")}, nil
	})
	if err == nil || !strings.Contains(err.Error(), "no public address") {
		t.Fatalf("unexpected error: %v", err)
	}
}

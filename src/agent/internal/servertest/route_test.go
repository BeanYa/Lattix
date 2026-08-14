package servertest

import (
	"context"
	"errors"
	"net/netip"
	"testing"
	"time"

	"lattix/shared"
)

func routeTestTarget() shared.ServerTestTarget {
	return shared.ServerTestTarget{
		ID: "test:1", Label: "测试目标", AddressFamily: shared.ServerTestIPv4,
		Host: "8.8.8.8", Source: "test",
	}
}

func stubTraceRoute(probe func(context.Context, netip.Addr, uint16, int, int, time.Duration, int) ([]map[string]any, bool, error)) func() {
	original := traceRoute
	traceRoute = probe
	return func() { traceRoute = original }
}

func TestRunRouteTargetErrorMapping(t *testing.T) {
	hops := []map[string]any{{"hop": 1, "address": "8.8.8.8"}}
	cases := []struct {
		name      string
		probe     func(context.Context, netip.Addr, uint16, int, int, time.Duration, int) ([]map[string]any, bool, error)
		wantCode  string
		wantMsg   string
		wantHops  bool
	}{
		{
			name: "silent limit maps to incomplete",
			probe: func(context.Context, netip.Addr, uint16, int, int, time.Duration, int) ([]map[string]any, bool, error) {
				return hops, false, errRouteSilentLimit
			},
			wantCode: "route_incomplete", wantMsg: errRouteSilentLimit.Error(), wantHops: true,
		},
		{
			name: "probe deadline maps to incomplete",
			probe: func(context.Context, netip.Addr, uint16, int, int, time.Duration, int) ([]map[string]any, bool, error) {
				return hops, false, errRouteProbeDeadline
			},
			wantCode: "route_incomplete", wantMsg: errRouteProbeDeadline.Error(), wantHops: true,
		},
		{
			name: "hard error maps to probe failed",
			probe: func(context.Context, netip.Addr, uint16, int, int, time.Duration, int) ([]map[string]any, bool, error) {
				return nil, false, errors.New("boom")
			},
			wantCode: "route_probe_failed", wantMsg: "boom",
		},
		{
			name: "cancellation maps to probe failed",
			probe: func(context.Context, netip.Addr, uint16, int, int, time.Duration, int) ([]map[string]any, bool, error) {
				return nil, false, context.Canceled
			},
			wantCode: "route_probe_failed", wantMsg: context.Canceled.Error(),
		},
		{
			name: "silent hops without error map to incomplete",
			probe: func(context.Context, netip.Addr, uint16, int, int, time.Duration, int) ([]map[string]any, bool, error) {
				return hops, false, nil
			},
			wantCode: "route_incomplete", wantMsg: "destination was not reached within 30 hops", wantHops: true,
		},
		{
			name: "reached maps to success",
			probe: func(context.Context, netip.Addr, uint16, int, int, time.Duration, int) ([]map[string]any, bool, error) {
				return hops, true, nil
			},
			wantHops: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			restore := stubTraceRoute(tc.probe)
			defer restore()
			result := runRouteTarget(context.Background(), routeTestTarget())
			if result.ErrorCode != tc.wantCode {
				t.Errorf("ErrorCode = %q, want %q", result.ErrorCode, tc.wantCode)
			}
			if result.ErrorMessage != tc.wantMsg {
				t.Errorf("ErrorMessage = %q, want %q", result.ErrorMessage, tc.wantMsg)
			}
			if tc.wantHops && len(result.Hops) != 1 {
				t.Errorf("Hops = %v, want 1 hop preserved", result.Hops)
			}
			if tc.name == "reached maps to success" && !result.Reached {
				t.Error("Reached = false, want true")
			}
		})
	}
}

func TestClassifyCtxError(t *testing.T) {
	if got := classifyCtxError(context.DeadlineExceeded); !errors.Is(got, errRouteProbeDeadline) {
		t.Errorf("classifyCtxError(DeadlineExceeded) = %v, want %v", got, errRouteProbeDeadline)
	}
	if got := classifyCtxError(context.Canceled); !errors.Is(got, context.Canceled) {
		t.Errorf("classifyCtxError(Canceled) = %v, want %v", got, context.Canceled)
	}
}

func TestRunRouteTargetPerTargetDeadline(t *testing.T) {
	var captured context.Context
	restore := stubTraceRoute(func(ctx context.Context, _ netip.Addr, _ uint16, _, _ int, _ time.Duration, _ int) ([]map[string]any, bool, error) {
		captured = ctx
		return nil, false, errRouteProbeDeadline
	})
	defer restore()

	parent, cancel := context.WithTimeout(context.Background(), routeTimeout)
	defer cancel()
	runRouteTarget(parent, routeTestTarget())

	if captured == nil {
		t.Fatal("probe context was not captured")
	}
	deadline, ok := captured.Deadline()
	if !ok {
		t.Fatal("probe context has no deadline")
	}
	remaining := time.Until(deadline)
	if remaining > routeTargetTimeout {
		t.Errorf("probe deadline allows %v, want at most routeTargetTimeout %v", remaining, routeTargetTimeout)
	}
	if remaining < routeTargetTimeout-5*time.Second {
		t.Errorf("probe deadline allows only %v, want close to routeTargetTimeout %v", remaining, routeTargetTimeout)
	}
}

package main

import (
	"errors"
	"testing"

	"lattix/shared"
)

func TestAggregateTrafficCounters(t *testing.T) {
	counters := aggregateTrafficCounters(map[string]int64{
		"inbound>>>node_7>>>traffic>>>uplink":             10,
		"inbound>>>node_7>>>traffic>>>downlink":           20,
		"inbound>>>chain_forward_11>>>traffic>>>uplink":   30,
		"inbound>>>chain_forward_11>>>traffic>>>downlink": 40,
		"user>>>user-id>>>traffic>>>uplink":               50,
		"user>>>user-id>>>traffic>>>downlink":             60,
		"inbound>>>api>>>traffic>>>uplink":                70,
	})
	if len(counters) != 3 {
		t.Fatalf("got %d counters: %+v", len(counters), counters)
	}
	seen := map[string][2]int64{}
	for _, counter := range counters {
		key := counter.User
		if counter.NodeID != 0 {
			key = "node"
		} else if counter.HopID != 0 {
			key = "hop"
		}
		seen[key] = [2]int64{counter.Up, counter.Down}
	}
	if seen["node"] != [2]int64{10, 20} || seen["hop"] != [2]int64{30, 40} || seen["user-id"] != [2]int64{50, 60} {
		t.Fatalf("unexpected counters: %+v", seen)
	}
}

type fakeTelemetryManager struct {
	users    []shared.OnlineUserStat
	usersErr error
}

func (f *fakeTelemetryManager) Version() (string, bool) { return "v1.8.0", true }

func (f *fakeTelemetryManager) StatsInstanceID() string { return "inst" }

func (f *fakeTelemetryManager) QueryStats() (map[string]int64, error) { return nil, nil }

func (f *fakeTelemetryManager) QueryOnlineUsers() ([]shared.OnlineUserStat, error) {
	return f.users, f.usersErr
}

func TestOnlineUsersCollectSuccess(t *testing.T) {
	users := []shared.OnlineUserStat{
		{User: "11111111-2222-3333-4444-555555555555", IPs: []string{"1.2.3.4", "5.6.7.8"}},
		{User: "99999999-8888-7777-6666-555555555555", IPs: []string{"10.0.0.1"}},
	}
	mgr := &fakeTelemetryManager{users: users}
	payload := (&telemetry{mgr: mgr}).collect()
	if len(payload.OnlineUsers) != 2 {
		t.Fatalf("got %d online users: %+v", len(payload.OnlineUsers), payload.OnlineUsers)
	}
	if payload.OnlineUsers[0].User != users[0].User || len(payload.OnlineUsers[0].IPs) != 2 ||
		payload.OnlineUsers[1].User != users[1].User || len(payload.OnlineUsers[1].IPs) != 1 {
		t.Fatalf("online users mismatch: %+v", payload.OnlineUsers)
	}
}

func TestOnlineUsersCollectDegradesOnError(t *testing.T) {
	mgr := &fakeTelemetryManager{usersErr: errors.New("rpc error: code = Unimplemented")}
	tm := &telemetry{mgr: mgr}
	if users := tm.onlineUsers(); users != nil {
		t.Fatalf("expected nil on error, got %+v", users)
	}
	payload := tm.collect()
	if payload.OnlineUsers != nil {
		t.Fatalf("expected empty online users in payload, got %+v", payload.OnlineUsers)
	}
}

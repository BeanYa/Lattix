package main

import "testing"

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

package main

import (
	"net"
	"sync"
	"testing"
	"time"

	"lattix/shared"
)

func TestRunLatencyProbeRoundReportsEveryTarget(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			_ = conn.Close()
		}
	}()
	port := listener.Addr().(*net.TCPAddr).Port
	results := runLatencyProbeRound([]shared.LatencyProbeTarget{
		{ServerID: 2, Kind: shared.LatencyKindTCP, Address: "127.0.0.1", Port: port},
		{ServerID: 3, Kind: shared.LatencyKindICMP, Address: "127.0.0.1"},         // 本地回环 ICMP 不可达也必须有结果
		{ServerID: 4, Kind: shared.LatencyKindTCP, Address: "127.0.0.1", Port: 1}, // 拒连
	}, 3*time.Second)
	if len(results) != 3 {
		t.Fatalf("got %d results, want one per target", len(results))
	}
	if results[0].LatencyMS == nil || *results[0].LatencyMS < 0 {
		t.Fatalf("reachable tcp target should report latency: %+v", results[0])
	}
	if results[0].Port != port || results[0].Kind != shared.LatencyKindTCP {
		t.Fatalf("result identity mismatch: %+v", results[0])
	}
	for _, result := range results[1:] {
		if result.LatencyMS != nil {
			t.Fatalf("failed target must keep nil latency: %+v", result)
		}
	}
}

func TestRunLatencyProbeRoundHonorsDeadline(t *testing.T) {
	// 全部目标不可达：整轮应在截止时间附近完成，而不是等到每个目标的独立超时叠加。
	start := time.Now()
	results := runLatencyProbeRound([]shared.LatencyProbeTarget{
		{ServerID: 2, Kind: shared.LatencyKindTCP, Address: "127.0.0.1", Port: 1},
	}, 300*time.Millisecond)
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("round exceeded deadline: %v", elapsed)
	}
	if len(results) != 1 || results[0].LatencyMS != nil {
		t.Fatalf("unreachable target must report nil latency: %+v", results)
	}
}

func TestProbeRunnerSingleFlight(t *testing.T) {
	runner := newProbeRunner()
	if !runner.tryStart("round-1") {
		t.Fatal("idle runner must accept start")
	}
	if runner.tryStart("round-2") {
		t.Fatal("busy runner must reject start")
	}
	runner.done()
	if !runner.tryStart("round-2") {
		t.Fatal("runner must accept start after done")
	}
}

// 防御：并发 done/tryStart 不应死锁或双占用。
func TestProbeRunnerConcurrent(t *testing.T) {
	runner := newProbeRunner()
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if runner.tryStart("round") {
				time.Sleep(time.Millisecond)
				runner.done()
			}
		}()
	}
	wg.Wait()
}

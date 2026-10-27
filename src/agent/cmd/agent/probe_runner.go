package main

import "sync"

// probeRunner 是 latency.probe 轮次的进程内单飞守卫（§链路统计）：
// 同一时刻仅允许一轮在途；panel 侧已有在途去重，此处兜底防面板重复下发并发执行。
type probeRunner struct {
	mu    sync.Mutex
	round string // 在途轮次 ID；空 = 空闲
}

func newProbeRunner() *probeRunner { return &probeRunner{} }

// tryStart 空闲时占用并返回 true；busy 返回 false。
func (r *probeRunner) tryStart(roundID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.round != "" {
		return false
	}
	r.round = roundID
	return true
}

// done 释放守卫（轮次完成/失败后调用）。
func (r *probeRunner) done() {
	r.mu.Lock()
	r.round = ""
	r.mu.Unlock()
}

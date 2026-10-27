package dispatch

import (
	"context"
	"encoding/json"
	"log"
	"time"

	"lattix/backend/internal/store"
	"lattix/shared"
)

// 服务器间延迟测速调度（§链路统计）：panel 每 5 分钟（面板调度器，可 env 覆盖）与
// 链路发布成功时各触发一轮；每轮为原子任务——对每台在线服务器的 m 个目标并行测速，
// 整轮截止 60s，全部完成/失败/超时即视为完成。同一服务器同时仅一轮在途（TTL 过期
// 自动放行），轮次依次执行。
const (
	latencyProbeTimeoutMS   = 60_000
	latencyRoundTTL         = 2 * time.Minute
	latencyProbeFreshWindow = 15 * time.Minute // > 2 轮间隔（5min），漏一轮仍有样本
)

// latencyRound 是一台服务器在途的测速轮次。
type latencyRound struct {
	roundID string
	sentAt  time.Time
}

// RunLatencyProbeRound 执行一轮全量测速：计算目标集并为每台在线且无在途轮次的
// 服务器下发 latency.probe。由面板 5 分钟调度任务与链路发布触发共同调用。
func (d *Dispatcher) RunLatencyProbeRound(ctx context.Context) {
	targets, err := d.st.LatencyProbeTargets(ctx)
	if err != nil {
		log.Printf("dispatch: latency probe targets: %v", err)
		return
	}
	if len(targets) == 0 {
		return
	}
	d.latencyMu.Lock()
	now := time.Now()
	for serverID, round := range d.latencyRounds {
		if now.Sub(round.sentAt) > latencyRoundTTL {
			delete(d.latencyRounds, serverID) // 回执丢失的轮次按超时放行下一轮
		}
	}
	pending := make(map[int64]string, len(d.latencyRounds))
	for serverID, round := range d.latencyRounds {
		pending[serverID] = round.roundID
	}
	d.latencyMu.Unlock()
	for serverID, list := range targets {
		if _, ok := pending[serverID]; ok {
			continue // 该服务器上一轮未完成，本轮跳过（下一轮依次执行）
		}
		if !d.req.IsOnline(serverID) {
			continue // 离线服务器不下发（轮次无补发语义，等待其上线后的轮次）
		}
		d.enqueueLatencyProbe(ctx, serverID, list)
	}
}

// enqueueLatencyProbe 为单台服务器入队并投递一轮测速；投递成功后登记在途轮次。
func (d *Dispatcher) enqueueLatencyProbe(ctx context.Context, serverID int64, targets []store.ServerLatencyTarget) {
	roundID := shared.NewMessageID()
	payload := shared.LatencyProbePayload{RoundID: roundID, TimeoutMS: latencyProbeTimeoutMS}
	for _, target := range targets {
		payload.Targets = append(payload.Targets, shared.LatencyProbeTarget{
			ServerID: target.ToServer, Address: target.Address, Kind: target.Kind, Port: target.Port,
		})
	}
	if len(payload.Targets) == 0 {
		return
	}
	if _, err := d.Enqueue(ctx, serverID, shared.TypeLatencyProbe, payload); err != nil {
		log.Printf("dispatch: server %d: enqueue latency probe: %v", serverID, err)
		return
	}
	d.latencyMu.Lock()
	d.latencyRounds[serverID] = latencyRound{roundID: roundID, sentAt: time.Now()}
	d.latencyMu.Unlock()
}

// handleLatencyReport 落库一轮测速结果（§链路统计）：校验在途轮次归属后覆盖最新值
// 并插入历史；失败/超时样本（latency 缺省）照常落库作连通性参考。
func (d *Dispatcher) handleLatencyReport(serverID int64, env shared.Envelope) {
	var p shared.LatencyProbeReportPayload
	if err := json.Unmarshal(env.Data, &p); err != nil {
		log.Printf("dispatch: server %d: bad latency report data: %v", serverID, err)
		return
	}
	d.latencyMu.Lock()
	round, ok := d.latencyRounds[serverID]
	if !ok || round.roundID != p.RoundID {
		d.latencyMu.Unlock()
		// 迟到/重复/跨服务器伪造的回报：无在途轮次归属，直接丢弃。
		log.Printf("dispatch: server %d: drop unmatched latency report round=%s", serverID, p.RoundID)
		return
	}
	delete(d.latencyRounds, serverID)
	d.latencyMu.Unlock()
	samples := make([]store.ServerLatencySample, 0, len(p.Results))
	for _, result := range p.Results {
		samples = append(samples, store.ServerLatencySample{
			ToServer: result.ServerID, Kind: result.Kind, Port: result.Port, LatencyMS: result.LatencyMS,
		})
	}
	if err := d.st.SaveServerLatencyProbes(d.baseCtx(), serverID, samples, time.Now().UTC()); err != nil {
		log.Printf("dispatch: server %d: save latency report: %v", serverID, err)
	}
}

// clearLatencyRound 清除服务器的在途轮次（命令失败/死信时放行下一轮）。
func (d *Dispatcher) clearLatencyRound(serverID int64) {
	d.latencyMu.Lock()
	delete(d.latencyRounds, serverID)
	d.latencyMu.Unlock()
}

// FreshLatencyWindow 暴露新鲜度窗口给消费方（panel 统计查询）。
const FreshLatencyWindow = latencyProbeFreshWindow

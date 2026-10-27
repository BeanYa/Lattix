package panel

import (
	"net/http"
	"time"

	"lattix/backend/internal/store"
)

// chainProbeFreshWindow 是段探测最新值的新鲜度窗口：窗口外样本视为失效
// （Agent 离线或拓扑变更后旧段不再计入链路延迟与段列表）。
const chainProbeFreshWindow = 10 * time.Minute

// chainSegmentStatDTO 是链一段出向探测的最新结果：HopID 为执行探测的本跳，
// Kind 区分 forward 直连段 / bridge 反向段；LatencyMS 为 null 表示最近一次探测失败/超时。
type chainSegmentStatDTO struct {
	HopID         int64     `json:"hop_id"`
	Kind          string    `json:"kind"`
	TargetAddress string    `json:"target_address"`
	TargetPort    int       `json:"target_port"`
	LatencyMS     *float64  `json:"latency_ms"`
	SampledAt     time.Time `json:"sampled_at"`
}

// chainStatsDTO 是一条链的链路一览统计（§链路统计）：段延迟明细、链路总延迟与今日负载。
// LatencyMS 为各段 RTT 之和（TCP 握手口径）；段探测不完整（任一段缺新鲜样本）时为 null。
type chainStatsDTO struct {
	ChainID   int64                 `json:"chain_id"`
	LatencyMS *float64              `json:"latency_ms"`
	Segments  []chainSegmentStatDTO `json:"segments"`
	Today     *chainTrafficDTO      `json:"today,omitempty"`
}

// chainStatsResponse 是 GET /api/chain/get-stats 的响应；今日合计为各链折算口径之和。
type chainStatsResponse struct {
	Chains    []chainStatsDTO `json:"chains"`
	TodayUp   int64           `json:"today_up"`
	TodayDown int64           `json:"today_down"`
}

// handleGetChainStats 处理 GET /api/chain/get-stats（链路一览轮询：
// 一次取全量链路的段延迟与今日流量，避免逐链请求）。
func (s *Server) handleGetChainStats(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	chains, err := s.st.ListChains(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	probes, err := s.st.FreshChainHopProbes(ctx, chainProbeFreshWindow)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	probesByHop := map[int64][]store.ChainHopProbe{}
	for _, probe := range probes {
		probesByHop[probe.HopID] = append(probesByHop[probe.HopID], probe)
	}
	_, location := s.st.TrafficLocation(ctx)
	today := time.Now().In(location).Format("2006-01-02")
	buckets, err := s.st.ChainDailyTrafficByDate(ctx, today)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	todayByChain := map[int64]*chainTrafficDTO{}
	for _, bucket := range buckets {
		if bucket.HopID != 0 {
			continue
		}
		todayByChain[bucket.ChainID] = &chainTrafficDTO{RawUp: bucket.RawUp, RawDown: bucket.RawDown,
			EffectiveUp: bucket.EffectiveUp, EffectiveDown: bucket.EffectiveDown}
	}
	out := chainStatsResponse{Chains: make([]chainStatsDTO, 0, len(chains))}
	for _, chain := range chains {
		hops, err := s.st.ChainHops(ctx, chain.ID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		dto := chainStatsDTO{ChainID: chain.ID, Segments: []chainSegmentStatDTO{}}
		latencySum := 0.0
		complete := len(hops) > 1
		for _, hop := range hops {
			for _, probe := range probesByHop[hop.ID] {
				dto.Segments = append(dto.Segments, chainSegmentStatDTO{
					HopID: probe.HopID, Kind: probe.Kind,
					TargetAddress: probe.TargetAddress, TargetPort: probe.TargetPort,
					LatencyMS: probe.LatencyMS, SampledAt: probe.SampledAt,
				})
				if probe.LatencyMS == nil {
					complete = false
					continue
				}
				latencySum += *probe.LatencyMS
			}
		}
		if complete && len(dto.Segments) == len(hops)-1 {
			dto.LatencyMS = &latencySum
		}
		dto.Today = todayByChain[chain.ID]
		if dto.Today != nil {
			out.TodayUp += dto.Today.EffectiveUp
			out.TodayDown += dto.Today.EffectiveDown
		}
		out.Chains = append(out.Chains, dto)
	}
	writeJSON(w, http.StatusOK, out)
}

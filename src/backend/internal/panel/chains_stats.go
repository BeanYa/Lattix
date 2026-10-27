package panel

import (
	"net/http"
	"time"

	"lattix/backend/internal/store"
)

// chainStatsFreshWindow 是服务器间延迟样本的新鲜度窗口（§链路统计）：测速轮次间隔
// 5 分钟，窗口取约 3 轮；窗口外样本视为失效（Agent 离线或拓扑变更后未再刷新的
// 服务器对不再计入链路延迟与段列表）。
const chainStatsFreshWindow = 15 * time.Minute

// chainSegmentStatDTO 是链一段物理测速边的最新结果：HopID 为该段所属的上游跳
// （连线归属）；Kind 为优先来源（tcp 复用链路监听端口，回退 icmp）；LatencyMS 为
// null 表示最近一次探测失败/超时（连通性参考）。
type chainSegmentStatDTO struct {
	HopID         int64     `json:"hop_id"`
	Kind          string    `json:"kind"`
	TargetAddress string    `json:"target_address"`
	TargetPort    int       `json:"target_port"`
	LatencyMS     *float64  `json:"latency_ms"`
	SampledAt     time.Time `json:"sampled_at"`
}

// chainStatsDTO 是一条链的链路一览统计（§链路统计）：段延迟明细、链路总延迟与今日负载。
// LatencyMS 为各段首选来源 RTT 之和；任一段缺新鲜样本时为 null。
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
	edges, err := s.st.PublishedLatencyEdges(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	edgesByChain := map[int64][]store.ServerLatencyEdge{}
	for _, edge := range edges {
		edgesByChain[edge.ChainID] = append(edgesByChain[edge.ChainID], edge)
	}
	fresh, err := s.st.FreshServerLatencyProbes(ctx, chainStatsFreshWindow)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	type latencyKey struct {
		from, to int64
		kind     string
		port     int
	}
	freshByKey := map[latencyKey]store.ServerLatencyRow{}
	for _, row := range fresh {
		freshByKey[latencyKey{row.FromServer, row.ToServer, row.Kind, row.Port}] = row
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
		for i := 0; i+1 < len(hops); i++ {
			// 每段（上游跳 hop[i] 的连线）取首选来源样本：tcp 优先，无 tcp 行回退 icmp。
			linkSegments := make([]chainSegmentStatDTO, 0, 2)
			var preferred *chainSegmentStatDTO
			for _, edge := range edgesByChain[chain.ID] {
				if edge.UpstreamHopID != hops[i].ID {
					continue
				}
				row, ok := freshByKey[latencyKey{edge.FromServer, edge.ToServer, edge.Kind, edge.Port}]
				if !ok {
					continue
				}
				segment := chainSegmentStatDTO{
					HopID: hops[i].ID, Kind: edge.Kind,
					TargetAddress: edge.TargetAddress, TargetPort: edge.Port,
					LatencyMS: row.LatencyMS, SampledAt: row.MeasuredAt,
				}
				linkSegments = append(linkSegments, segment)
				if preferred == nil || (preferred.Kind != "tcp" && segment.Kind == "tcp") {
					preferred = &linkSegments[len(linkSegments)-1]
				}
			}
			dto.Segments = append(dto.Segments, linkSegments...)
			if preferred == nil {
				complete = false
				continue
			}
			if preferred.LatencyMS == nil {
				complete = false
				continue
			}
			latencySum += *preferred.LatencyMS
		}
		if complete && len(hops) > 1 {
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

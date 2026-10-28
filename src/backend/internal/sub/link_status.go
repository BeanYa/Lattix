package sub

import (
	"context"
	"time"

	"lattix/backend/internal/store"
)

// 订阅页链路状态（§链路状态）：入口服务器的三网目录延迟 + 链路内部段延迟加总，
// 与面板链路详情同源（latency.probe 轮次落库的 server_catalog_probes /
// server_latency_probes，tcp 优先、回退 icmp）。数据做全局缓存：面板仅每 5 分钟
//（与测速轮次同节奏）和订阅文件快照更新时刷新，读请求只查缓存、按订阅用户过滤。

// linkStatusTTL 是链路状态缓存的刷新间隔（与 chain-latency.probe 轮次同节奏）。
const linkStatusTTL = 5 * time.Minute

// linkStatusFreshWindow 是探针样本的新鲜度窗口（约 3 个测速轮次，
// 与面板链路统计口径一致）。
const linkStatusFreshWindow = 15 * time.Minute

// SubLinkCarrierStatus 是入口服务器到一个三网目录测速点（各地公开 CDN 节点，
// 如「广东电信」）的最新延迟。窗口内无新鲜样本时 SampledAt 为 nil；有样本但
// LatencyMS 为 null 表示最近一次探测失败/超时。
type SubLinkCarrierStatus struct {
	Label     string     `json:"label"`
	LatencyMS *float64   `json:"latency_ms"`
	SampledAt *time.Time `json:"sampled_at,omitempty"`
}

// SubLinkStatus 是一个实际出现在订阅内的直连或中转链路。服务器标识不对外暴露
// （Entry 仅角色标签），InternalMS 为链路内部各段首选 RTT 之和，段不全时为 null；
// 单跳直连链路无内部段，恒为 0。
type SubLinkStatus struct {
	Label      string                 `json:"label"`
	Entry      string                 `json:"entry"`
	Carriers   []SubLinkCarrierStatus `json:"carriers"`
	InternalMS *float64               `json:"internal_ms"`
}

// SubLinkStatusResponse 是 GET /api/sub/{token}/status 的响应。
type SubLinkStatusResponse struct {
	UpdatedAt time.Time       `json:"updated_at"`
	Links     []SubLinkStatus `json:"links"`
}

// catalogSample 是一个（服务器, 目录目标）的最新探针样本。
type catalogSample struct {
	latency    *float64
	measuredAt time.Time
}

// linkStatusData 是缓存的全量链路测速数据（与具体订阅用户无关，按用户过滤在
// 读取侧进行）。
type linkStatusData struct {
	builtAt time.Time
	// carriers 是当前测速区域的三网目录目标（Label 如「广东电信」，电信→联通→移动）。
	carriers []store.CatalogProbeTarget
	// catalogByServer 是服务器 → 目录目标 key → 最新样本。
	catalogByServer map[int64]map[string]catalogSample
	// internalByChain 是链路 → 内部段首选 RTT 加总（nil = 任一段缺新鲜样本/超时）。
	internalByChain map[int64]*float64
}

// invalidateLinkStatus 在订阅文件快照更新后标记缓存失效（下一次读取时重建）。
func (s *Server) invalidateLinkStatus() {
	s.linkMu.Lock()
	s.linkDirty = true
	s.linkMu.Unlock()
}

// linkStatus 返回缓存的链路测速数据；缓存超过 TTL 或被快照发布标记失效时重建。
// 重建在互斥下进行，并发读请求最多触发一次；失败时回退旧缓存（有旧数据可用则
// 不让订阅页因测速表抖动而 5xx）。
func (s *Server) linkStatus(ctx context.Context) (*linkStatusData, error) {
	data, dirty := s.cachedLinkStatus()
	if data != nil && !dirty && time.Since(data.builtAt) < linkStatusTTL {
		return data, nil
	}
	s.linkBuildMu.Lock()
	defer s.linkBuildMu.Unlock()
	// 双检：等锁期间可能已被其他请求重建。
	data, dirty = s.cachedLinkStatus()
	if data != nil && !dirty && time.Since(data.builtAt) < linkStatusTTL {
		return data, nil
	}
	built, err := s.buildLinkStatus(ctx)
	if err != nil {
		if data != nil {
			return data, nil
		}
		return nil, err
	}
	s.linkMu.Lock()
	s.linkData = built
	s.linkDirty = false
	s.linkMu.Unlock()
	return built, nil
}

func (s *Server) cachedLinkStatus() (*linkStatusData, bool) {
	s.linkMu.Lock()
	defer s.linkMu.Unlock()
	return s.linkData, s.linkDirty
}

// buildLinkStatus 从探针表构建全量链路测速数据。
func (s *Server) buildLinkStatus(ctx context.Context) (*linkStatusData, error) {
	carriers, _, err := s.st.CatalogProbeTargets(ctx)
	if err != nil {
		return nil, err
	}
	data := &linkStatusData{
		builtAt:         time.Now(),
		carriers:        carriers,
		catalogByServer: make(map[int64]map[string]catalogSample),
		internalByChain: make(map[int64]*float64),
	}
	catalogRows, err := s.st.FreshServerCatalogProbes(ctx, linkStatusFreshWindow)
	if err != nil {
		return nil, err
	}
	for _, row := range catalogRows {
		if data.catalogByServer[row.FromServer] == nil {
			data.catalogByServer[row.FromServer] = make(map[string]catalogSample)
		}
		data.catalogByServer[row.FromServer][row.TargetKey] = catalogSample{
			latency: row.LatencyMS, measuredAt: row.MeasuredAt,
		}
	}

	// 链路内部延迟加总：每段取首选来源样本，tcp 优先（含 tcp 失败样本——有 tcp 行
	// 即以 tcp 为准，超时如实呈现）、无 tcp 行回退 icmp；任一段缺新鲜样本或超时
	// 则整条链路的加总为 null（与面板链路统计口径一致）。
	pairRows, err := s.st.FreshServerLatencyProbes(ctx, linkStatusFreshWindow)
	if err != nil {
		return nil, err
	}
	type pairKey struct {
		from, to int64
		kind     string
		port     int
	}
	pairByKey := make(map[pairKey]store.ServerLatencyRow, len(pairRows))
	for _, row := range pairRows {
		pairByKey[pairKey{row.FromServer, row.ToServer, row.Kind, row.Port}] = row
	}
	edges, err := s.st.PublishedLatencyEdges(ctx)
	if err != nil {
		return nil, err
	}
	type hopKey struct {
		chain, hop int64
	}
	segments := make(map[hopKey]store.ServerLatencyRow)
	segmentHops := make(map[hopKey]bool)
	for _, edge := range edges {
		key := hopKey{edge.ChainID, edge.UpstreamHopID}
		segmentHops[key] = true
		row, ok := pairByKey[pairKey{edge.FromServer, edge.ToServer, edge.Kind, edge.Port}]
		if !ok {
			continue
		}
		if cur, exists := segments[key]; exists && (cur.Kind == "tcp" || edge.Kind != "tcp") {
			continue
		}
		segments[key] = row
	}
	type chainSum struct {
		value    float64
		complete bool
	}
	sums := make(map[int64]*chainSum)
	for key := range segmentHops {
		agg, ok := sums[key.chain]
		if !ok {
			agg = &chainSum{complete: true}
			sums[key.chain] = agg
		}
		row, has := segments[key]
		if !has || row.LatencyMS == nil {
			agg.complete = false
			continue
		}
		agg.value += *row.LatencyMS
	}
	for chainID, agg := range sums {
		if agg.complete {
			value := agg.value
			data.internalByChain[chainID] = &value
		}
	}
	return data, nil
}

// linkCarriers 组装入口服务器对三网目录目标的最新延迟（顺序与目录目标一致）。
func linkCarriers(data *linkStatusData, entryServer int64) []SubLinkCarrierStatus {
	carriers := make([]SubLinkCarrierStatus, 0, len(data.carriers))
	samples := data.catalogByServer[entryServer]
	for _, target := range data.carriers {
		carrier := SubLinkCarrierStatus{Label: target.Label}
		if sample, ok := samples[target.Key]; ok {
			latency := sample.latency
			measuredAt := sample.measuredAt
			carrier.LatencyMS = latency
			carrier.SampledAt = &measuredAt
		}
		carriers = append(carriers, carrier)
	}
	return carriers
}

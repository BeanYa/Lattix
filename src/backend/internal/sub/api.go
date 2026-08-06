package sub

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"lattix/backend/internal/extsub"
	"lattix/backend/internal/store"
)

// SubInfoResponse 是 GET /api/sub/{token}/info 的响应。
type SubInfoResponse struct {
	Name           string `json:"name"`
	ExpiresAt      *int64 `json:"expires_at,omitempty"` // unix 秒
	Expired        bool   `json:"expired"`
	Disabled       bool   `json:"disabled"`
	UsedUp         int64  `json:"used_up"`
	UsedDown       int64  `json:"used_down"`
	TrafficLimit   int64  `json:"traffic_limit"` // 0=不限
	NodesCount     int    `json:"nodes_count"`
	Title          string `json:"title"`
	Announcement   string `json:"announcement"` // Markdown
	UpdateInterval string `json:"update_interval"`
}

// ClientInfo 是客户端导入选项。
type ClientInfo struct {
	Name             string                  `json:"name"`
	Platform         string                  `json:"platform"` // ios / android / windows / macos / universal
	Deeplink         string                  `json:"deeplink"` // 完整 deeplink URL
	AppStoreURL      string                  `json:"app_store_url,omitempty"`
	Format           string                  `json:"format"` // 对应的 ?format= 值
	DownloadVariants []ClientDownloadVariant `json:"download_variants,omitempty"`
}

// SubLatencySample 是订阅状态页使用的一次延迟探测结果。服务器标识不对外暴露，
// 以免订阅链接持有者据此枚举面板基础设施。
type SubLatencySample struct {
	LatencyMS *float64 `json:"latency_ms"`
	UpdatedAt string   `json:"updated_at"`
}

// SubLinkHopStatus 是订阅中一台服务器的匿名化状态。链路内仅以入口、出口和
// 中转序号标识；直连节点统一标为“服务器”。
type SubLinkHopStatus struct {
	Label   string             `json:"label"`
	Samples []SubLatencySample `json:"samples"`
}

// SubLinkStatus 是一个实际出现在订阅内的直连或中转链路，Label 为链路名称。
type SubLinkStatus struct {
	Label string             `json:"label"`
	Hops  []SubLinkHopStatus `json:"hops"`
}

// SubLinkStatusResponse 是 GET /api/sub/{token}/status 的响应。
type SubLinkStatusResponse struct {
	Links []SubLinkStatus `json:"links"`
}

func disableSharedCache(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "private, no-store")
}

// HandleSubInfo 处理 GET /api/sub/{token}/info（仅凭 token 鉴权，无需登录）。
func (s *Server) HandleSubInfo(w http.ResponseWriter, r *http.Request) {
	disableSharedCache(w)
	token := r.PathValue("token")
	user, err := s.st.UserBySubToken(r.Context(), token)
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, store.ErrNotFound) {
			status = http.StatusNotFound
		}
		http.Error(w, err.Error()+"\n", status)
		return
	}

	t, _ := s.st.UserTraffic(r.Context(), user.UUID)
	assigned, _ := s.st.UserNodeIDs(r.Context(), user.ID)
	// 分组用户的直连节点被遮蔽，快照生成失败时回退不得计入直连节点数
	// （itemsForUser 已反映真实计数，回退仅在快照生成失败时生效）。
	if groupIDs, err := s.st.UserGroupIDsForUser(r.Context(), user.ID); err == nil && len(groupIDs) > 0 {
		assigned = []int64{}
	}
	// 节点数与订阅快照内容一致：直连节点 + 分配链路 + 外部订阅节点。
	items, _, _ := s.itemsForUser(r.Context(), user)
	nodesCount := len(items)
	if nodesCount == 0 {
		nodesCount = len(assigned) // 快照生成失败时回退到直连节点数
	}
	var panelExpire *int64
	if user.ExpiresAt != nil {
		v := user.ExpiresAt.Unix()
		panelExpire = &v
	}
	attached, _ := s.st.EffectiveUserExternalSubscriptions(r.Context(), user.ID)
	merged := extsub.MergeUserTraffic(time.Now(), extsub.Traffic{
		Upload: t.Up, Download: t.Down, Total: user.TrafficLimit, Expire: panelExpire,
	}, attached)

	// 标题：用户级覆盖 > 全局设置 > 默认。
	title := user.SubTitle
	if title == "" {
		title, _ = s.st.GetSetting(r.Context(), store.SettingSubTitle)
	}
	if title == "" {
		title = "Lattix 订阅"
	}
	// 公告：用户级覆盖 > 全局设置。
	announcement := user.SubAnnouncement
	if announcement == "" {
		announcement, _ = s.st.GetSetting(r.Context(), store.SettingSubAnnouncement)
	}
	// 更新间隔。
	interval := "24"
	if global, _ := s.st.GetSetting(r.Context(), store.SettingSubUpdateInterval); global != "" {
		interval = global
	}

	resp := SubInfoResponse{
		Name:           user.Name,
		Expired:        user.Expired,
		Disabled:       user.Disabled,
		UsedUp:         merged.Upload,
		UsedDown:       merged.Download,
		TrafficLimit:   merged.Total,
		NodesCount:     nodesCount,
		Title:          title,
		Announcement:   announcement,
		UpdateInterval: interval,
	}
	if merged.Expire != nil {
		v := *merged.Expire
		resp.ExpiresAt = &v
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(resp)
}

// HandleSubClients 处理 GET /api/sub/{token}/clients（返回客户端导入列表）。
func (s *Server) HandleSubClients(w http.ResponseWriter, r *http.Request) {
	disableSharedCache(w)
	token := r.PathValue("token")
	user, err := s.st.UserBySubToken(r.Context(), token)
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, store.ErrNotFound) {
			status = http.StatusNotFound
		}
		http.Error(w, err.Error()+"\n", status)
		return
	}

	base := s.base(r)
	subURL := fmt.Sprintf("%s/sub/%s", base, user.SubToken)
	encSub := url.QueryEscape(subURL)
	importName := url.QueryEscape("Lattix-" + user.Name)

	clients := []ClientInfo{
		{Name: "Clash Verge", Platform: "windows", Deeplink: fmt.Sprintf("clash://install-config?url=%s&name=%s", encSub, importName), Format: "clash", DownloadVariants: clientDownloadVariants("clash-verge-windows-x64", "clash-verge-windows-arm64")},
		{Name: "Clash Verge", Platform: "macos", Deeplink: fmt.Sprintf("clash://install-config?url=%s&name=%s", encSub, importName), Format: "clash", DownloadVariants: clientDownloadVariants("clash-verge-macos-x64", "clash-verge-macos-arm64", "clash-verge-macos-x64-portable", "clash-verge-macos-arm64-portable")},
		{Name: "mihomo-party", Platform: "windows", Deeplink: fmt.Sprintf("clash://install-config?url=%s&name=%s", encSub, importName), Format: "clash", DownloadVariants: clientDownloadVariants("mihomo-party-windows-x64", "mihomo-party-windows-arm64", "mihomo-party-windows-x64-portable", "mihomo-party-windows-arm64-portable")},
		{Name: "mihomo-party", Platform: "macos", Deeplink: fmt.Sprintf("clash://install-config?url=%s&name=%s", encSub, importName), Format: "clash", DownloadVariants: clientDownloadVariants("mihomo-party-macos-x64", "mihomo-party-macos-arm64")},
		{Name: "FlClash", Platform: "android", Deeplink: fmt.Sprintf("clash://install-config?url=%s&name=%s", encSub, importName), Format: "clash", DownloadVariants: clientDownloadVariants("flclash-android-arm64", "flclash-android-armv7", "flclash-android-x64")},
		{Name: "FlClash", Platform: "windows", Deeplink: fmt.Sprintf("clash://install-config?url=%s&name=%s", encSub, importName), Format: "clash", DownloadVariants: clientDownloadVariants("flclash-windows-x64", "flclash-windows-arm64", "flclash-windows-x64-portable", "flclash-windows-arm64-portable")},
		{Name: "FlClash", Platform: "macos", Deeplink: fmt.Sprintf("clash://install-config?url=%s&name=%s", encSub, importName), Format: "clash", DownloadVariants: clientDownloadVariants("flclash-macos-x64", "flclash-macos-arm64")},
		{Name: "Stash", Platform: "ios", Deeplink: fmt.Sprintf("stash://install-config?url=%s&name=%s", encSub, importName), AppStoreURL: "https://apps.apple.com/us/app/stash-rule-based-proxy/id1596063349", Format: "clash"},
		{Name: "Loon", Platform: "ios", Deeplink: fmt.Sprintf("loon://import?sub=%s", encSub), AppStoreURL: "https://apps.apple.com/us/app/loon/id1373567447", Format: "clash"},
		{Name: "Egern", Platform: "ios", Deeplink: fmt.Sprintf("clash://install-config?url=%s&name=%s", encSub, importName), AppStoreURL: "https://apps.apple.com/us/app/egern/id1616105820", Format: "clash"},
		{Name: "Surfboard", Platform: "android", Deeplink: fmt.Sprintf("clash://install-config?url=%s&name=%s", encSub, importName), Format: "clash", DownloadVariants: clientDownloadVariants("surfboard-android-arm64", "surfboard-android-armv7", "surfboard-android-x64", "surfboard-android-x86")},
		{Name: "sing-box (SFA)", Platform: "android", Deeplink: fmt.Sprintf("sing-box://import-remote-profile?url=%s", encSub), Format: "singbox", DownloadVariants: clientDownloadVariants("singbox-android-arm64", "singbox-android-armv7", "singbox-android-x64", "singbox-android-x86")},
		{Name: "sing-box (SFI)", Platform: "ios", Deeplink: fmt.Sprintf("sing-box://import-remote-profile?url=%s", encSub), AppStoreURL: "https://apps.apple.com/us/search?term=sing-box", Format: "singbox"},
		{Name: "Shadowrocket", Platform: "ios", Deeplink: fmt.Sprintf("shadowrocket://add/sub://%s", encSub), AppStoreURL: "https://apps.apple.com/us/app/shadowrocket/id932747118", Format: "links"},
		{Name: "Quantumult X", Platform: "ios", Deeplink: fmt.Sprintf("quantumult-x:///add?remote-resource=%s", encSub), AppStoreURL: "https://apps.apple.com/us/app/quantumult-x/id1443988620", Format: "quanx"},
		{Name: "v2rayNG", Platform: "android", Deeplink: "", Format: "links", DownloadVariants: clientDownloadVariants("v2rayng-android-arm64", "v2rayng-android-armv7", "v2rayng-android-x64", "v2rayng-android-x86")},
		{Name: "NekoBox", Platform: "android", Deeplink: "", Format: "links", DownloadVariants: clientDownloadVariants("nekobox-android-arm64", "nekobox-android-armv7", "nekobox-android-x64", "nekobox-android-x86")},
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(clients)
}

// HandleSubStatus 处理 GET /api/sub/{token}/status。该接口只返回订阅实际可用
// 路径上的匿名拓扑标签和既有延迟探测样本，不返回服务器名称、地址或内部 ID。
func (s *Server) HandleSubStatus(w http.ResponseWriter, r *http.Request) {
	disableSharedCache(w)
	token := r.PathValue("token")
	user, err := s.st.UserBySubToken(r.Context(), token)
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, store.ErrNotFound) {
			status = http.StatusNotFound
		}
		http.Error(w, err.Error()+"\n", status)
		return
	}

	links, err := s.subscriptionLinkStatus(r, user)
	if err != nil {
		http.Error(w, err.Error()+"\n", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(SubLinkStatusResponse{Links: links})
}

func (s *Server) subscriptionLinkStatus(r *http.Request, user *store.User) ([]SubLinkStatus, error) {
	if user.Expired || user.Disabled {
		return []SubLinkStatus{}, nil
	}
	samplesByServer, err := s.st.RecentServerMetricSamples(r.Context(), 30)
	if err != nil {
		return nil, err
	}
	assignedNodes, err := s.st.UserNodeIDs(r.Context(), user.ID)
	if err != nil {
		return nil, err
	}
	allowedNodes := make(map[int64]bool, len(assignedNodes))
	for _, nodeID := range assignedNodes {
		allowedNodes[nodeID] = true
	}
	exitNodes, err := s.st.ChainExitNodeIDs(r.Context())
	if err != nil {
		return nil, err
	}
	links := make([]SubLinkStatus, 0)
	seenDirectServers := make(map[int64]bool)
	for _, nodeID := range assignedNodes {
		if exitNodes[nodeID] {
			continue
		}
		node, err := s.st.NodeByID(r.Context(), nodeID)
		if err != nil || len(node.RealizedConfig) == 0 || seenDirectServers[node.ServerID] {
			continue
		}
		seenDirectServers[node.ServerID] = true
		name := strings.TrimSpace(node.Name)
		if name == "" {
			name = fmt.Sprintf("直连链路 %d", len(links)+1)
		}
		links = append(links, SubLinkStatus{
			Label: name,
			Hops:  []SubLinkHopStatus{{Label: "服务器", Samples: subLatencySamples(samplesByServer[node.ServerID])}},
		})
	}

	assignments, err := s.st.UserChainAssignments(r.Context(), user.ID)
	if err != nil {
		return nil, err
	}
	assignmentByChain := make(map[int64]store.UserChainAssignment, len(assignments))
	for _, assignment := range assignments {
		assignmentByChain[assignment.ChainID] = assignment
	}
	chains, err := s.st.ListChains(r.Context())
	if err != nil {
		return nil, err
	}
	chainNumber := 0
	for _, chain := range chains {
		if chain.PublishedRevisionID == 0 || chain.Status == store.ChainStatusInvalid || chain.Status == store.ChainStatusDeleted {
			continue
		}
		assignment, assigned := assignmentByChain[chain.ID]
		if chain.EndpointID != 0 && !assigned {
			continue
		}
		var assignmentArgs []store.UserChainAssignment
		if assigned {
			assignmentArgs = []store.UserChainAssignment{assignment}
		}
		// Reuse the subscription compiler's eligibility checks so the monitor
		// cannot reveal a chain that was omitted from the actual subscription.
		if _, err := s.chainSubscriptionItem(r, chain, allowedNodes, assignmentArgs...); err != nil {
			continue
		}
		revision, err := s.st.PublishedChainRevision(r.Context(), chain.ID)
		if err != nil || len(revision.Snapshot.Hops) == 0 {
			continue
		}
		chainNumber++
		hops := make([]SubLinkHopStatus, 0, len(revision.Snapshot.Hops))
		for index, hop := range revision.Snapshot.Hops {
			hops = append(hops, SubLinkHopStatus{
				Label:   subscriptionHopLabel(index, len(revision.Snapshot.Hops)),
				Samples: subLatencySamples(samplesByServer[hop.ServerID]),
			})
		}
		name := strings.TrimSpace(chain.Name)
		if name == "" {
			name = fmt.Sprintf("中转链路 %d", chainNumber)
		}
		links = append(links, SubLinkStatus{Label: name, Hops: hops})
	}
	return links, nil
}

func subLatencySamples(samples []store.ServerMetrics) []SubLatencySample {
	out := make([]SubLatencySample, 0, len(samples))
	for _, sample := range samples {
		out = append(out, SubLatencySample{LatencyMS: sample.LatencyMS, UpdatedAt: sample.UpdatedAt})
	}
	return out
}

func subscriptionHopLabel(index, count int) string {
	if count == 1 {
		return "入口 / 出口"
	}
	if index == 0 {
		return "入口"
	}
	if index == count-1 {
		return "出口"
	}
	return fmt.Sprintf("中转 %d", index)
}

// HandleSubHistory 处理 GET /api/sub/{token}/history（返回用户流量历史）。
func (s *Server) HandleSubHistory(w http.ResponseWriter, r *http.Request) {
	disableSharedCache(w)
	token := r.PathValue("token")
	user, err := s.st.UserBySubToken(r.Context(), token)
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, store.ErrNotFound) {
			status = http.StatusNotFound
		}
		http.Error(w, err.Error()+"\n", status)
		return
	}
	history, err := s.st.ListUserTrafficHistory(r.Context(), user.UUID, 50)
	if err != nil {
		http.Error(w, err.Error()+"\n", http.StatusInternalServerError)
		return
	}
	if history == nil {
		history = []store.TrafficHistoryRow{}
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(history)
}

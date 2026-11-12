package sub

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"lattix/backend/internal/store"
	"lattix/shared"
)

// SubInfoResponse 是 GET /api/sub/{token}/info 的响应。
type SubInfoResponse struct {
	Name           string `json:"name"`
	ExpiresAt      *int64 `json:"expires_at,omitempty"` // unix 秒
	Expired        bool   `json:"expired"`
	Disabled       bool   `json:"disabled"`
	QuotaExhausted bool   `json:"quota_exhausted"`        // 流量配额停权（§9）
	EffectiveAt    *int64 `json:"effective_at,omitempty"` // unix 秒（null=立即生效）
	NotEffective   bool   `json:"not_effective"`          // 尚未到达生效时刻（§9）
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

// SubLinkStatus / SubLinkStatusResponse 见 link_status.go（链路状态数据结构）。

func disableSharedCache(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "private, no-store")
}

// HandleSubInfo 处理 GET /api/sub/{token}/info（仅凭 token 鉴权，无需登录）。
func (s *Server) HandleSubInfo(w http.ResponseWriter, r *http.Request) {
	disableSharedCache(w)
	token := r.PathValue("token")
	user, err := s.st.UserBySubToken(r.Context(), token)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			http.Error(w, err.Error()+"\n", http.StatusNotFound)
			return
		}
		writeInternalError(w, "serve sub info", err)
		return
	}

	t, _ := s.st.UserTraffic(r.Context(), user.UUID)
	items, _, err := s.itemsForUser(r.Context(), user)
	if err != nil {
		writeInternalError(w, "serve sub info", err)
		return
	}
	nodesCount := len(items)
	merged := s.mergedUserTraffic(r.Context(), user, t)

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
		QuotaExhausted: user.QuotaExhausted,
		NotEffective:   user.NotYetEffective(time.Now()),
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
	if user.EffectiveAt != nil {
		v := user.EffectiveAt.Unix()
		resp.EffectiveAt = &v
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
		if errors.Is(err, store.ErrNotFound) {
			http.Error(w, err.Error()+"\n", http.StatusNotFound)
			return
		}
		writeInternalError(w, "serve sub clients", err)
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
// 路径上的匿名拓扑标签与既有测速结果（入口三网目录延迟 + 链路内部段延迟加总，
// 读全局缓存），不返回服务器名称、地址或内部 ID。
func (s *Server) HandleSubStatus(w http.ResponseWriter, r *http.Request) {
	disableSharedCache(w)
	token := r.PathValue("token")
	user, err := s.st.UserBySubToken(r.Context(), token)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			http.Error(w, err.Error()+"\n", http.StatusNotFound)
			return
		}
		writeInternalError(w, "serve sub status", err)
		return
	}

	data, err := s.linkStatus(r.Context())
	if err != nil {
		writeInternalError(w, "serve sub status", err)
		return
	}
	links, err := s.subscriptionLinkStatus(r, user, data)
	if err != nil {
		writeInternalError(w, "serve sub status", err)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(SubLinkStatusResponse{
		UpdatedAt:         data.builtAt,
		UnreachableRegion: data.unreachableRegion,
		Links:             links,
	})
}

func (s *Server) subscriptionLinkStatus(r *http.Request, user *store.User, data *linkStatusData) ([]SubLinkStatus, error) {
	if blocked, _ := subscriptionBlocked(user, time.Now()); blocked || user.Disabled {
		return []SubLinkStatus{}, nil
	}
	hidden, err := s.st.UserHiddenSubscriptionItems(r.Context(), user.ID)
	if err != nil {
		return nil, err
	}
	return s.catalogLinkStatus(r, user, data, hidden)
}

func (s *Server) catalogLinkStatus(r *http.Request, user *store.User, data *linkStatusData, hidden map[string]bool) ([]SubLinkStatus, error) {
	ctx := r.Context()
	assignedNodes, err := s.st.UserNodeIDs(ctx, user.ID)
	if err != nil {
		return nil, err
	}
	allowedNodes, assignments, err := s.userAccess(ctx, user)
	if err != nil {
		return nil, err
	}
	exitNodes, err := s.st.ChainExitNodeIDs(ctx)
	if err != nil {
		return nil, err
	}
	links := make([]SubLinkStatus, 0)
	directNumber := 0
	for _, nodeID := range assignedNodes {
		if exitNodes[nodeID] {
			continue
		}
		node, err := s.st.NodeByID(ctx, nodeID)
		if err != nil || node.Status != store.NodeStatusActive {
			continue
		}
		var realized shared.RealizedConfig
		if json.Unmarshal(node.RealizedConfig, &realized) != nil || realized.Port == 0 {
			continue
		}
		directNumber++
		id := subscriptionItemID("node", nodeID)
		if hidden[id] {
			continue
		}
		name := strings.TrimSpace(node.Name)
		if name == "" {
			name = fmt.Sprintf("直连链路 %d", directNumber)
		}
		links = append(links, SubLinkStatus{
			ID:          id,
			Label:       name,
			Entry:       "服务器",
			Carriers:    linkCarriers(data, node.ServerID),
			InternalMS:  float64Ptr(0),
			EntryBanned: data.bannedByServer[node.ServerID],
		})
	}

	assignmentByChain := make(map[int64]store.UserChainAssignment, len(assignments))
	for _, assignment := range assignments {
		assignmentByChain[assignment.ChainID] = assignment
	}
	chains, err := s.st.ListChains(ctx)
	if err != nil {
		return nil, err
	}
	chainNumber := 0
	for _, chain := range chains {
		assignment, available := assignmentByChain[chain.ID]
		if !available {
			continue
		}
		if _, err := s.chainSubscriptionItem(ctx, chain, allowedNodes, assignment); err != nil {
			continue
		}
		revision, err := s.st.PublishedChainRevision(ctx, chain.ID)
		if err != nil || len(revision.Snapshot.Hops) == 0 {
			continue
		}
		chainNumber++
		id := subscriptionItemID("chain", chain.ID)
		if hidden[id] {
			continue
		}
		entry := revision.Snapshot.Hops[0]
		internal := data.internalByChain[chain.ID]
		if len(revision.Snapshot.Hops) == 1 {
			internal = float64Ptr(0)
		}
		name := strings.TrimSpace(chain.Name)
		if name == "" {
			name = fmt.Sprintf("中转链路 %d", chainNumber)
		}
		links = append(links, SubLinkStatus{
			ID:          id,
			Label:       name,
			Entry:       subscriptionEntryLabel(len(revision.Snapshot.Hops)),
			Carriers:    linkCarriers(data, entry.ServerID),
			InternalMS:  internal,
			EntryBanned: data.bannedByServer[entry.ServerID],
		})
	}
	return links, nil
}

type subscriptionExternalItem struct {
	ID    string   `json:"id"`
	Label string   `json:"label"`
	Nodes []string `json:"nodes"`
}

func (s *Server) subscriptionVisibilityCatalog(r *http.Request, user *store.User) ([]SubLinkStatus, []subscriptionExternalItem, error) {
	ctx := r.Context()
	data, err := s.linkStatus(ctx)
	if err != nil {
		return nil, nil, err
	}
	links, err := s.catalogLinkStatus(r, user, data, nil)
	if err != nil {
		return nil, nil, err
	}
	sources, err := s.st.ListExternalSubscriptions(ctx)
	if err != nil {
		return nil, nil, err
	}
	external := make([]subscriptionExternalItem, 0, len(sources))
	for _, source := range sources {
		chains, err := s.st.ListExternalChains(ctx, source.ID)
		if err != nil {
			return nil, nil, err
		}
		item := subscriptionExternalItem{
			ID: subscriptionItemID("external", source.ID), Label: source.Name,
			Nodes: make([]string, 0, len(chains)),
		}
		for _, chain := range chains {
			item.Nodes = append(item.Nodes, chain.Name)
		}
		external = append(external, item)
	}
	return links, external, nil
}

// subscriptionEntryLabel 返回链路入口的角色标签（直连节点统一标为“服务器”）。
func subscriptionEntryLabel(hopCount int) string {
	if hopCount == 1 {
		return "服务器"
	}
	return "入口"
}

func float64Ptr(v float64) *float64 {
	return &v
}

// HandleSubHistory 处理 GET /api/sub/{token}/history（返回用户流量历史）。
func (s *Server) HandleSubHistory(w http.ResponseWriter, r *http.Request) {
	disableSharedCache(w)
	token := r.PathValue("token")
	user, err := s.st.UserBySubToken(r.Context(), token)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			http.Error(w, err.Error()+"\n", http.StatusNotFound)
			return
		}
		writeInternalError(w, "serve sub history", err)
		return
	}
	history, err := s.st.ListUserTrafficHistory(r.Context(), user.UUID, 50)
	if err != nil {
		writeInternalError(w, "serve sub history", err)
		return
	}
	if history == nil {
		history = []store.TrafficHistoryRow{}
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(history)
}

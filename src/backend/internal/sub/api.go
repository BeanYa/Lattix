package sub

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"

	"lattix/backend/internal/store"
)

// SubInfoResponse 是 GET /api/sub/{token}/info 的响应。
type SubInfoResponse struct {
	Name            string `json:"name"`
	ExpiresAt       *int64 `json:"expires_at,omitempty"` // unix 秒
	Expired         bool   `json:"expired"`
	Disabled        bool   `json:"disabled"`
	UsedUp          int64  `json:"used_up"`
	UsedDown        int64  `json:"used_down"`
	TrafficLimit    int64  `json:"traffic_limit"` // 0=不限
	NodesCount      int    `json:"nodes_count"`
	Title           string `json:"title"`
	Announcement    string `json:"announcement"` // Markdown
	UpdateInterval  string `json:"update_interval"`
}

// ClientInfo 是客户端导入选项。
type ClientInfo struct {
	Name     string `json:"name"`
	Platform string `json:"platform"` // ios / android / windows / macos / universal
	Deeplink string `json:"deeplink"` // 完整 deeplink URL
	Format   string `json:"format"`   // 对应的 ?format= 值
}

// HandleSubInfo 处理 GET /api/sub/{token}/info（仅凭 token 鉴权，无需登录）。
func (s *Server) HandleSubInfo(w http.ResponseWriter, r *http.Request) {
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
		UsedUp:         t.Up,
		UsedDown:       t.Down,
		TrafficLimit:   user.TrafficLimit,
		NodesCount:     len(assigned),
		Title:          title,
		Announcement:   announcement,
		UpdateInterval: interval,
	}
	if user.ExpiresAt != nil {
		v := user.ExpiresAt.Unix()
		resp.ExpiresAt = &v
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(resp)
}

// HandleSubClients 处理 GET /api/sub/{token}/clients（返回客户端导入列表）。
func (s *Server) HandleSubClients(w http.ResponseWriter, r *http.Request) {
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
		{Name: "Clash Verge", Platform: "windows", Deeplink: fmt.Sprintf("clash://install-config?url=%s&name=%s", encSub, importName), Format: "clash"},
		{Name: "mihomo-party", Platform: "windows", Deeplink: fmt.Sprintf("clash://install-config?url=%s&name=%s", encSub, importName), Format: "clash"},
		{Name: "FlClash", Platform: "android", Deeplink: fmt.Sprintf("clash://install-config?url=%s&name=%s", encSub, importName), Format: "clash"},
		{Name: "Stash", Platform: "ios", Deeplink: fmt.Sprintf("stash://install-config?url=%s&name=%s", encSub, importName), Format: "clash"},
		{Name: "Loon", Platform: "ios", Deeplink: fmt.Sprintf("loon://import?sub=%s", encSub), Format: "clash"},
		{Name: "Egern", Platform: "ios", Deeplink: fmt.Sprintf("clash://install-config?url=%s&name=%s", encSub, importName), Format: "clash"},
		{Name: "Surfboard", Platform: "android", Deeplink: fmt.Sprintf("clash://install-config?url=%s&name=%s", encSub, importName), Format: "clash"},
		{Name: "sing-box (SFA)", Platform: "android", Deeplink: fmt.Sprintf("sing-box://import-remote-profile?url=%s", encSub), Format: "singbox"},
		{Name: "sing-box (SFI)", Platform: "ios", Deeplink: fmt.Sprintf("sing-box://import-remote-profile?url=%s", encSub), Format: "singbox"},
		{Name: "Shadowrocket", Platform: "ios", Deeplink: fmt.Sprintf("shadowrocket://add/sub://%s", encSub), Format: "links"},
		{Name: "Quantumult X", Platform: "ios", Deeplink: fmt.Sprintf("quantumult-x:///add?remote-resource=%s", encSub), Format: "quanx"},
		{Name: "v2rayNG", Platform: "android", Deeplink: "", Format: "links"},
		{Name: "NekoBox", Platform: "universal", Deeplink: "", Format: "links"},
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(clients)
}

// HandleSubHistory 处理 GET /api/sub/{token}/history（返回用户流量历史）。
func (s *Server) HandleSubHistory(w http.ResponseWriter, r *http.Request) {
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

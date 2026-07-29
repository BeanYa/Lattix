package panel

import (
	"net/http"
	"strconv"
	"strings"

	"lattix/backend/internal/store"
)

// subSettingsDTO 是 GET /api/setting/sub 的响应。
type subSettingsDTO struct {
	Title              string `json:"title"`
	Announcement       string `json:"announcement"`
	CustomCSS          string `json:"custom_css"`
	UpdateInterval     int    `json:"update_interval"`      // 小时
	TrafficHistoryKeep int    `json:"traffic_history_keep"` // 保留周期数
}

// handleGetSubSettings 处理 GET /api/setting/sub。
func (s *Server) handleGetSubSettings(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	dto := subSettingsDTO{
		Title:              s.getSetting(ctx, store.SettingSubTitle),
		Announcement:       s.getSetting(ctx, store.SettingSubAnnouncement),
		CustomCSS:          s.getSetting(ctx, store.SettingSubCustomCSS),
		UpdateInterval:     settingInt(s.getSetting(ctx, store.SettingSubUpdateInterval), 24),
		TrafficHistoryKeep: settingInt(s.getSetting(ctx, store.SettingTrafficHistoryKeep), 6),
	}
	writeJSON(w, http.StatusOK, dto)
}

// updateSubSettingsRequest 是 POST /api/setting/sub 的请求体。
type updateSubSettingsRequest struct {
	Title              string `json:"title"`
	Announcement       string `json:"announcement"`
	CustomCSS          string `json:"custom_css"`
	UpdateInterval     int    `json:"update_interval"`
	TrafficHistoryKeep int    `json:"traffic_history_keep"`
}

// handleUpdateSubSettings 处理 POST /api/setting/sub。
func (s *Server) handleUpdateSubSettings(w http.ResponseWriter, r *http.Request) {
	var req updateSubSettingsRequest
	if err := readJSON(r, &req); err != nil {
		writeProtocolError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.UpdateInterval <= 0 {
		req.UpdateInterval = 24
	}
	if req.UpdateInterval > 720 {
		req.UpdateInterval = 720
	}
	if req.TrafficHistoryKeep <= 0 {
		req.TrafficHistoryKeep = 6
	}
	if req.TrafficHistoryKeep > 60 {
		req.TrafficHistoryKeep = 60
	}

	ctx := r.Context()
	mutations := []store.SettingMutation{
		{Key: store.SettingSubTitle, Value: strings.TrimSpace(req.Title)},
		{Key: store.SettingSubAnnouncement, Value: req.Announcement},
		{Key: store.SettingSubCustomCSS, Value: req.CustomCSS},
		{Key: store.SettingSubUpdateInterval, Value: strconv.Itoa(req.UpdateInterval)},
		{Key: store.SettingTrafficHistoryKeep, Value: strconv.Itoa(req.TrafficHistoryKeep)},
	}
	if _, err := s.st.ApplySettings(ctx, mutations, nil); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	s.audit(r, "sub_settings.updated", nil, nil, map[string]any{
		"title":                req.Title,
		"update_interval":      req.UpdateInterval,
		"traffic_history_keep": req.TrafficHistoryKeep,
	})
	s.handleGetSubSettings(w, r)
}

package panel

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"lattix/backend/internal/store"
)

// subscriptionDefaultDTO 是默认级模板配置（全局一份，模板指派页维护）。
// 建议规则分组与中立模板二选一；均空 = 未配置默认级。
type subscriptionDefaultDTO struct {
	SuggestedCategories []string `json:"suggested_categories"`
	PortableTemplateID  string   `json:"portable_template_id"`
	RegionGroupsEnabled bool     `json:"region_groups_enabled"`
}

func defaultConfigToDTO(cfg store.SubscriptionDefaultConfig) subscriptionDefaultDTO {
	ids := []string{}
	if cfg.SuggestedCategories != "" {
		_ = json.Unmarshal([]byte(cfg.SuggestedCategories), &ids)
	}
	return subscriptionDefaultDTO{
		SuggestedCategories: ids,
		PortableTemplateID:  cfg.PortableTemplateID,
		RegionGroupsEnabled: !cfg.DisableRegionGroups,
	}
}

// handleGetSubscriptionTemplateDefault 处理 GET /api/subscription/template/default。
func (s *Server) handleGetSubscriptionTemplateDefault(w http.ResponseWriter, r *http.Request) {
	cfg, err := s.st.SubscriptionDefaultConfig(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, defaultConfigToDTO(cfg))
}

// handleSaveSubscriptionTemplateDefault 处理 POST /api/subscription/template/default：
// 保存默认级模板配置（建议规则分组或中立模板二选一，均空 = 清除默认级）。
// 默认级影响所有未配置面板级/用户级的回落用户，保存后全量重发订阅快照。
func (s *Server) handleSaveSubscriptionTemplateDefault(w http.ResponseWriter, r *http.Request) {
	var req struct {
		SuggestedCategories []string `json:"suggested_categories"`
		PortableTemplateID  string   `json:"portable_template_id"`
		RegionGroupsEnabled *bool    `json:"region_groups_enabled"`
	}
	if err := readJSON(r, &req); err != nil {
		writeProtocolError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	var cfg store.SubscriptionDefaultConfig
	switch {
	case len(req.SuggestedCategories) > 0:
		normalized, err := normalizeSuggestedCategories(req.SuggestedCategories)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		raw, err := json.Marshal(normalized)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		cfg.SuggestedCategories = string(raw)
	default:
		id := strings.TrimSpace(req.PortableTemplateID)
		if id == "" {
			break // 均空 = 清除默认级
		}
		template, err := s.st.SubscriptionTemplateByID(r.Context(), id)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				writeError(w, http.StatusNotFound, "订阅模板不存在")
				return
			}
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if template.Kind != "portable" && template.Kind != "acl4ssr" {
			writeError(w, http.StatusBadRequest, "默认级模板须为主策略（中立/ACL4SSR）类型")
			return
		}
		if strings.TrimSpace(template.Content) == "" {
			writeError(w, http.StatusBadRequest, "订阅模板尚无有效缓存")
			return
		}
		cfg.PortableTemplateID = id
	}
	if req.RegionGroupsEnabled != nil {
		cfg.DisableRegionGroups = !*req.RegionGroupsEnabled
	}
	if err := s.st.SaveSubscriptionDefaultConfig(r.Context(), cfg); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// 默认级是所有回落用户的兜底：全量重发订阅快照（发布转异步，观察收口）。
	o := s.observeStart(r, "subscription.template.default", "保存默认级模板配置", userPublishObserveStages)
	defer o.CloseIfPending()
	o.Report("db", 100, "默认级配置已保存")
	if s.subscriptions != nil {
		users, err := s.st.ListUsers(r.Context())
		if err == nil && len(users) > 0 {
			userIDs := make([]int64, 0, len(users))
			for _, u := range users {
				userIDs = append(userIDs, u.ID)
			}
			o.WatchUsers(userIDs)
			s.subscriptions.EnqueueUsers(userIDs, s.panelBase(r))
			o.Report("regenerate", 0, "等待订阅重生成")
		}
	}
	s.audit(r, "subscription.template.default_saved", nil, nil, map[string]any{
		"suggested_categories": req.SuggestedCategories,
		"portable_template_id": req.PortableTemplateID,
	})
	writeJSON(w, http.StatusOK, defaultConfigToDTO(cfg))
}

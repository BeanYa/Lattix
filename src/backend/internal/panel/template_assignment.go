package panel

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"lattix/backend/internal/store"
	"lattix/backend/internal/sub"
)

// applyTemplateAssignment 按模板 kind 把指派写入对应槽位；acl4ssr 归主策略槽位。
func applyTemplateAssignment(profile *store.SubscriptionProfile, kind, templateID string, forced bool) error {
	switch kind {
	case "portable", "acl4ssr":
		profile.AssignedPortableTemplateID, profile.AssignForcedPortable = templateID, forced
	case "mihomo":
		profile.AssignedMihomoTemplateID, profile.AssignForcedMihomo = templateID, forced
	case "singbox":
		profile.AssignedSingboxTemplateID, profile.AssignForcedSingbox = templateID, forced
	case "quanx":
		profile.AssignedQuanXTemplateID, profile.AssignForcedQuanX = templateID, forced
	default:
		return fmt.Errorf("不支持的模板类型 %q", kind)
	}
	return nil
}

// clearTemplateAssignment 清除模板 kind 对应槽位的指派与强制标记（用户自选不触碰）。
func clearTemplateAssignment(profile *store.SubscriptionProfile, kind string) error {
	switch kind {
	case "portable", "acl4ssr":
		profile.AssignedPortableTemplateID, profile.AssignForcedPortable = "", false
	case "mihomo":
		profile.AssignedMihomoTemplateID, profile.AssignForcedMihomo = "", false
	case "singbox":
		profile.AssignedSingboxTemplateID, profile.AssignForcedSingbox = "", false
	case "quanx":
		profile.AssignedQuanXTemplateID, profile.AssignForcedQuanX = "", false
	default:
		return fmt.Errorf("不支持的模板类型 %q", kind)
	}
	return nil
}

func dedupeUserIDs(ids []int64) []int64 {
	seen := make(map[int64]bool, len(ids))
	out := make([]int64, 0, len(ids))
	for _, id := range ids {
		if id > 0 && !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}

// normalizeSuggestedCategories 校验并规范化分组列表：未知 id → 错误；空 → 错误；去重并按内置顺序排序。
func normalizeSuggestedCategories(raw []string) ([]string, error) {
	known := make(map[string]bool)
	order := make(map[string]int)
	for index, category := range sub.Categories() {
		known[category.ID] = true
		order[category.ID] = index
	}
	seen := make(map[string]bool)
	out := make([]string, 0, len(raw))
	for _, id := range raw {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			continue
		}
		if !known[id] {
			return nil, fmt.Errorf("未知分组 %q", id)
		}
		seen[id] = true
		out = append(out, id)
	}
	if len(out) == 0 {
		return nil, errors.New("suggested_categories 不能为空")
	}
	sort.SliceStable(out, func(i, j int) bool { return order[out[i]] < order[out[j]] })
	return out, nil
}

// assignmentTarget 解析指派目标：template_id 与 suggested_categories 二选一（均空或均非空 → 400）。
func (s *Server) assignmentTarget(w http.ResponseWriter, r *http.Request, templateID string, suggestedCategories []string) (target *store.SubscriptionTemplate, categories []string, ok bool) {
	templateID = strings.TrimSpace(templateID)
	if (templateID == "") == (len(suggestedCategories) == 0) {
		writeError(w, http.StatusBadRequest, "template_id 与 suggested_categories 必须二选一")
		return nil, nil, false
	}
	if len(suggestedCategories) > 0 {
		normalized, err := normalizeSuggestedCategories(suggestedCategories)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return nil, nil, false
		}
		return nil, normalized, true
	}
	template, err := s.st.SubscriptionTemplateByID(r.Context(), templateID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "订阅模板不存在")
			return nil, nil, false
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return nil, nil, false
	}
	if strings.TrimSpace(template.Content) == "" {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("订阅模板 %q 尚无有效缓存", template.Name))
		return nil, nil, false
	}
	return &template, nil, true
}

// handleAssignSubscriptionTemplate 处理 POST /api/subscription/template/assign：
// 多选用户批量指派模板或建议规则预设到主策略槽位，可强制覆盖用户自选；指派后重发各用户订阅快照。
func (s *Server) handleAssignSubscriptionTemplate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		UserIDs              []int64  `json:"user_ids"`
		TemplateID           string   `json:"template_id"`
		SuggestedCategories  []string `json:"suggested_categories"`
		Forced               bool     `json:"forced"`
	}
	if err := readJSON(r, &req); err != nil {
		writeProtocolError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	userIDs := dedupeUserIDs(req.UserIDs)
	if len(userIDs) == 0 {
		writeError(w, http.StatusBadRequest, "user_ids 不能为空")
		return
	}
	template, categories, ok := s.assignmentTarget(w, r, req.TemplateID, req.SuggestedCategories)
	if !ok {
		return
	}
	// 批量指派逐个用户重发布订阅：发布转异步，观察跟踪至全部用户发布完成
	// （WatchUsers 先于 EnqueueUsers 登记，消除完成回调竞态）。
	o := s.observeStart(r, "subscription.template.assign", "指派订阅模板", userPublishObserveStages)
	defer o.CloseIfPending()
	for _, userID := range userIDs {
		if _, err := s.st.UserByID(r.Context(), userID); err != nil {
			o.Fail(err)
			if errors.Is(err, store.ErrNotFound) {
				writeError(w, http.StatusNotFound, "用户不存在")
				return
			}
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		profile, err := s.st.UserSubscriptionProfile(r.Context(), userID)
		if err != nil {
			o.Fail(err)
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if categories != nil {
			// 建议规则指派与模板指派同为主策略槽位，互斥。
			profile.AssignedPortableTemplateID = ""
			raw, _ := json.Marshal(categories)
			profile.AssignedSuggestedCategories = string(raw)
			profile.AssignForcedPortable = req.Forced
		} else {
			if err := applyTemplateAssignment(&profile, template.Kind, template.ID, req.Forced); err != nil {
				o.Fail(err)
				writeError(w, http.StatusBadRequest, err.Error())
				return
			}
			profile.AssignedSuggestedCategories = ""
		}
		if err := s.st.SaveUserSubscriptionProfile(r.Context(), profile); err != nil {
			o.Fail(err)
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	o.Report("db", 100, "指派已保存")
	if s.subscriptions != nil {
		o.WatchUsers(userIDs)
		s.subscriptions.EnqueueUsers(userIDs, s.panelBase(r))
		o.Report("regenerate", 0, "等待订阅重生成")
	}
	s.audit(r, "subscription.template.assigned", nil, nil, map[string]any{
		"template_id": req.TemplateID, "suggested_categories": categories, "user_ids": userIDs, "forced": req.Forced,
	})
	writeJSON(w, http.StatusOK, map[string]any{
		"user_ids": userIDs, "template_id": req.TemplateID, "suggested_categories": categories, "forced": req.Forced,
	})
}

// handleUnassignSubscriptionTemplate 处理 POST /api/subscription/template/unassign：
// 清除用户对应指派（模板或建议规则），用户自选值保留，并重发订阅快照。
func (s *Server) handleUnassignSubscriptionTemplate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		UserIDs              []int64  `json:"user_ids"`
		TemplateID           string   `json:"template_id"`
		SuggestedCategories  []string `json:"suggested_categories"`
	}
	if err := readJSON(r, &req); err != nil {
		writeProtocolError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	userIDs := dedupeUserIDs(req.UserIDs)
	if len(userIDs) == 0 {
		writeError(w, http.StatusBadRequest, "user_ids 不能为空")
		return
	}
	template, categories, ok := s.assignmentTarget(w, r, req.TemplateID, req.SuggestedCategories)
	if !ok {
		return
	}
	o := s.observeStart(r, "subscription.template.unassign", "取消指派订阅模板", userPublishObserveStages)
	defer o.CloseIfPending()
	for _, userID := range userIDs {
		if _, err := s.st.UserByID(r.Context(), userID); err != nil {
			o.Fail(err)
			if errors.Is(err, store.ErrNotFound) {
				writeError(w, http.StatusNotFound, "用户不存在")
				return
			}
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		profile, err := s.st.UserSubscriptionProfile(r.Context(), userID)
		if err != nil {
			o.Fail(err)
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if categories != nil {
			profile.AssignedSuggestedCategories = ""
			profile.AssignForcedPortable = false
		} else {
			if err := clearTemplateAssignment(&profile, template.Kind); err != nil {
				o.Fail(err)
				writeError(w, http.StatusBadRequest, err.Error())
				return
			}
		}
		if err := s.st.SaveUserSubscriptionProfile(r.Context(), profile); err != nil {
			o.Fail(err)
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	o.Report("db", 100, "指派已清除")
	if s.subscriptions != nil {
		o.WatchUsers(userIDs)
		s.subscriptions.EnqueueUsers(userIDs, s.panelBase(r))
		o.Report("regenerate", 0, "等待订阅重生成")
	}
	s.audit(r, "subscription.template.unassigned", nil, nil, map[string]any{
		"template_id": req.TemplateID, "suggested_categories": categories, "user_ids": userIDs,
	})
	writeJSON(w, http.StatusOK, map[string]any{
		"user_ids": userIDs, "template_id": req.TemplateID, "suggested_categories": categories,
	})
}

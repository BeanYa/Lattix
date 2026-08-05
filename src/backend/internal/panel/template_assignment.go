package panel

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"lattix/backend/internal/store"
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

func validSuggestedPreset(preset string) bool {
	switch preset {
	case "minimal", "balanced", "comprehensive":
		return true
	default:
		return false
	}
}

// assignmentTarget 解析指派目标：template_id 与 suggested_preset 二选一（均空或均非空 → 400）。
func (s *Server) assignmentTarget(w http.ResponseWriter, r *http.Request, templateID, suggestedPreset string) (target *store.SubscriptionTemplate, preset string, ok bool) {
	templateID = strings.TrimSpace(templateID)
	suggestedPreset = strings.TrimSpace(suggestedPreset)
	if (templateID == "") == (suggestedPreset == "") {
		writeError(w, http.StatusBadRequest, "template_id 与 suggested_preset 必须二选一")
		return nil, "", false
	}
	if suggestedPreset != "" {
		if !validSuggestedPreset(suggestedPreset) {
			writeError(w, http.StatusBadRequest, "suggested_preset 无效")
			return nil, "", false
		}
		return nil, suggestedPreset, true
	}
	template, err := s.st.SubscriptionTemplateByID(r.Context(), templateID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "订阅模板不存在")
			return nil, "", false
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return nil, "", false
	}
	if strings.TrimSpace(template.Content) == "" {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("订阅模板 %q 尚无有效缓存", template.Name))
		return nil, "", false
	}
	return &template, "", true
}

// handleAssignSubscriptionTemplate 处理 POST /api/subscription/template/assign：
// 多选用户批量指派模板或建议规则预设到主策略槽位，可强制覆盖用户自选；指派后重发各用户订阅快照。
func (s *Server) handleAssignSubscriptionTemplate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		UserIDs         []int64 `json:"user_ids"`
		TemplateID      string  `json:"template_id"`
		SuggestedPreset string  `json:"suggested_preset"`
		Forced          bool    `json:"forced"`
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
	template, suggestedPreset, ok := s.assignmentTarget(w, r, req.TemplateID, req.SuggestedPreset)
	if !ok {
		return
	}
	for _, userID := range userIDs {
		if _, err := s.st.UserByID(r.Context(), userID); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				writeError(w, http.StatusNotFound, "用户不存在")
				return
			}
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		profile, err := s.st.UserSubscriptionProfile(r.Context(), userID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if suggestedPreset != "" {
			// 建议规则指派与模板指派同为主策略槽位，互斥。
			profile.AssignedPortableTemplateID = ""
			profile.AssignedSuggestedPreset = suggestedPreset
			profile.AssignForcedPortable = req.Forced
		} else {
			if err := applyTemplateAssignment(&profile, template.Kind, template.ID, req.Forced); err != nil {
				writeError(w, http.StatusBadRequest, err.Error())
				return
			}
			profile.AssignedSuggestedPreset = ""
		}
		if err := s.st.SaveUserSubscriptionProfile(r.Context(), profile); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	if s.subscriptions != nil {
		for _, userID := range userIDs {
			if _, err := s.subscriptions.PublishUser(r.Context(), userID, s.panelBase(r)); err != nil {
				writeError(w, http.StatusBadRequest, "生成订阅失败: "+err.Error())
				return
			}
		}
	}
	s.audit(r, "subscription.template.assigned", nil, nil, map[string]any{
		"template_id": req.TemplateID, "suggested_preset": suggestedPreset, "user_ids": userIDs, "forced": req.Forced,
	})
	writeJSON(w, http.StatusOK, map[string]any{
		"user_ids": userIDs, "template_id": req.TemplateID, "suggested_preset": suggestedPreset, "forced": req.Forced,
	})
}

// handleUnassignSubscriptionTemplate 处理 POST /api/subscription/template/unassign：
// 清除用户对应指派（模板或建议规则），用户自选值保留，并重发订阅快照。
func (s *Server) handleUnassignSubscriptionTemplate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		UserIDs         []int64 `json:"user_ids"`
		TemplateID      string  `json:"template_id"`
		SuggestedPreset string  `json:"suggested_preset"`
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
	template, suggestedPreset, ok := s.assignmentTarget(w, r, req.TemplateID, req.SuggestedPreset)
	if !ok {
		return
	}
	for _, userID := range userIDs {
		if _, err := s.st.UserByID(r.Context(), userID); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				writeError(w, http.StatusNotFound, "用户不存在")
				return
			}
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		profile, err := s.st.UserSubscriptionProfile(r.Context(), userID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if suggestedPreset != "" {
			profile.AssignedSuggestedPreset = ""
			profile.AssignForcedPortable = false
		} else {
			if err := clearTemplateAssignment(&profile, template.Kind); err != nil {
				writeError(w, http.StatusBadRequest, err.Error())
				return
			}
		}
		if err := s.st.SaveUserSubscriptionProfile(r.Context(), profile); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	if s.subscriptions != nil {
		for _, userID := range userIDs {
			if _, err := s.subscriptions.PublishUser(r.Context(), userID, s.panelBase(r)); err != nil {
				writeError(w, http.StatusBadRequest, "生成订阅失败: "+err.Error())
				return
			}
		}
	}
	s.audit(r, "subscription.template.unassigned", nil, nil, map[string]any{
		"template_id": req.TemplateID, "suggested_preset": suggestedPreset, "user_ids": userIDs,
	})
	writeJSON(w, http.StatusOK, map[string]any{
		"user_ids": userIDs, "template_id": req.TemplateID, "suggested_preset": suggestedPreset,
	})
}

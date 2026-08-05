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

// handleAssignSubscriptionTemplate 处理 POST /api/subscription/template/assign：
// 多选用户批量指派模板到对应 kind 槽位，可强制覆盖用户自选；指派后重发各用户订阅快照。
func (s *Server) handleAssignSubscriptionTemplate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		UserIDs    []int64 `json:"user_ids"`
		TemplateID string  `json:"template_id"`
		Forced     bool    `json:"forced"`
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
	templateID := strings.TrimSpace(req.TemplateID)
	if templateID == "" {
		writeError(w, http.StatusBadRequest, "template_id 不能为空")
		return
	}
	template, err := s.st.SubscriptionTemplateByID(r.Context(), templateID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "订阅模板不存在")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if strings.TrimSpace(template.Content) == "" {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("订阅模板 %q 尚无有效缓存", template.Name))
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
		if err := applyTemplateAssignment(&profile, template.Kind, template.ID, req.Forced); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
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
		"template_id": templateID, "user_ids": userIDs, "forced": req.Forced,
	})
	writeJSON(w, http.StatusOK, map[string]any{
		"user_ids": userIDs, "template_id": templateID, "forced": req.Forced,
	})
}

// handleUnassignSubscriptionTemplate 处理 POST /api/subscription/template/unassign：
// 清除用户对应 kind 槽位的指派与强制标记（用户自选值保留），并重发订阅快照。
func (s *Server) handleUnassignSubscriptionTemplate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		UserIDs    []int64 `json:"user_ids"`
		TemplateID string  `json:"template_id"`
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
	templateID := strings.TrimSpace(req.TemplateID)
	if templateID == "" {
		writeError(w, http.StatusBadRequest, "template_id 不能为空")
		return
	}
	template, err := s.st.SubscriptionTemplateByID(r.Context(), templateID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "订阅模板不存在")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
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
		if err := clearTemplateAssignment(&profile, template.Kind); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
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
		"template_id": templateID, "user_ids": userIDs,
	})
	writeJSON(w, http.StatusOK, map[string]any{
		"user_ids": userIDs, "template_id": templateID,
	})
}

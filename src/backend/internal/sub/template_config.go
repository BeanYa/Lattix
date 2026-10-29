package sub

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"lattix/backend/internal/store"
)

// SubRoutingConfig 是订阅页模板配置的用户级视图（规则来源、预设、生效分组与模板槽位）。
type SubRoutingConfig struct {
	Mode               string   `json:"mode"`
	Preset             string   `json:"preset"`
	Categories         []string `json:"categories"`
	PortableTemplateID string   `json:"portable_template_id"`
	MihomoTemplateID   string   `json:"mihomo_template_id"`
	SingboxTemplateID  string   `json:"singbox_template_id"`
	QuanXTemplateID    string   `json:"quanx_template_id"`
}

// SubTemplateOption 是订阅页可选的模板项（仅含有缓存内容的模板）。
type SubTemplateOption struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Kind string `json:"kind"`
}

// SubTemplateConfigResponse 是 GET/POST /api/sub/{token}/template-config 的响应。
// User 是用户级原始配置（表单初值），Effective 是按 强制级 > 用户级 > 面板级 > 默认级
// 解析后的实际生效配置，EffectiveSource 标注主策略当前由哪一级决定。
type SubTemplateConfigResponse struct {
	UserConfigured bool `json:"user_configured"`
	// EffectiveSource：forced / user / panel / default / unset。
	EffectiveSource string `json:"effective_source"`
	// 地区分组开关（用户可在订阅页调整；面板各级配置保存不覆盖它，指派除外）。
	// 取四级解析后的实际生效值：用户级缺席时随面板级/默认级一并生效，订阅页据此
	// 展示表单初值，保证页面与订阅文件一致。
	RegionGroupsEnabled bool `json:"region_groups_enabled"`
	// 强制级（覆盖一切用户设置）。
	ForcedPortable bool   `json:"forced_portable"`
	ForcedMihomo   bool   `json:"forced_mihomo"`
	ForcedSingbox  bool   `json:"forced_singbox"`
	ForcedQuanX    bool   `json:"forced_quanx"`
	ForcedSummary  string `json:"forced_summary,omitempty"`
	// 面板级非强制指派（用户级优先；用户级缺席时生效）。
	PanelAssignedSummary string `json:"panel_assigned_summary,omitempty"`
	AssignedMihomoID     string `json:"assigned_mihomo_template_id,omitempty"`
	AssignedSingboxID    string `json:"assigned_singbox_template_id,omitempty"`
	AssignedQuanXID      string `json:"assigned_quanx_template_id,omitempty"`
	// 默认级（面板与用户均未配置时的全局兜底，模板指派页维护）。
	DefaultSummary string              `json:"default_summary,omitempty"`
	User           SubRoutingConfig    `json:"user"`
	Effective      SubRoutingConfig    `json:"effective"`
	Templates      []SubTemplateOption `json:"templates"`
	Categories     []CategoryDTO       `json:"categories"`
}

// subRoutingConfigOf 把 profile 的用户级字段折叠为订阅页配置视图。
func subRoutingConfigOf(p store.SubscriptionProfile) SubRoutingConfig {
	categories := []string{}
	_ = json.Unmarshal([]byte(p.CategoriesJSON), &categories)
	return SubRoutingConfig{
		Mode: p.Mode, Preset: p.Preset, Categories: categories,
		PortableTemplateID: p.PortableTemplateID, MihomoTemplateID: p.MihomoTemplateID,
		SingboxTemplateID: p.SingboxTemplateID, QuanXTemplateID: p.QuanXTemplateID,
	}
}

// categoryLabels 把分组 id 列表格式化为「图标 名称」串，供指派摘要文案使用。
func categoryLabels(ids []string) string {
	byID := make(map[string]CategoryDTO, len(ids))
	for _, category := range Categories() {
		byID[category.ID] = category
	}
	labels := make([]string, 0, len(ids))
	for _, id := range ids {
		if category, ok := byID[id]; ok {
			labels = append(labels, category.Icon+" "+category.Label)
			continue
		}
		labels = append(labels, id)
	}
	return strings.Join(labels, "、")
}

// jsonCategories 解码 JSON 分组数组（空串 → nil）。
func jsonCategories(raw string) []string {
	if raw == "" {
		return nil
	}
	ids := []string{}
	_ = json.Unmarshal([]byte(raw), &ids)
	return ids
}

// templateName 查模板名（查不到时回退 id），供指派摘要文案使用。
func (s *Server) templateName(ctx context.Context, id string) string {
	template, err := s.st.SubscriptionTemplateByID(ctx, id)
	if err != nil {
		return id
	}
	return template.Name
}

// templateConfigResponse 汇总用户订阅模板配置的四级状态与可选模板/分组目录。
func (s *Server) templateConfigResponse(r *http.Request, user *store.User) (SubTemplateConfigResponse, error) {
	profile, err := s.st.UserSubscriptionProfile(r.Context(), user.ID)
	if err != nil {
		return SubTemplateConfigResponse{}, err
	}
	defaultCfg, err := s.st.SubscriptionDefaultConfig(r.Context())
	if err != nil {
		return SubTemplateConfigResponse{}, err
	}
	effective := store.EffectiveProfile(profile, defaultCfg)
	resp := SubTemplateConfigResponse{
		UserConfigured: profile.UserConfigured,
		// 地区分组开关按实际生效值回显（用户级缺席时随面板级/默认级生效），
		// 否则订阅页显示与订阅文件会不一致。
		RegionGroupsEnabled: !effective.DisableRegionGroups,
		ForcedPortable:      profile.AssignForcedPortable,
		ForcedMihomo:        profile.AssignForcedMihomo,
		ForcedSingbox:       profile.AssignForcedSingbox,
		ForcedQuanX:         profile.AssignForcedQuanX,
		AssignedMihomoID:    profile.AssignedMihomoTemplateID,
		AssignedSingboxID:   profile.AssignedSingboxTemplateID,
		AssignedQuanXID:     profile.AssignedQuanXTemplateID,
		User:                subRoutingConfigOf(profile),
		Categories:          Categories(),
	}
	// 强制级摘要（主策略槽位）。
	if profile.AssignForcedPortable {
		resp.EffectiveSource = "forced"
		if profile.AssignedSuggestedCategories != "" {
			resp.ForcedSummary = "管理员已强制指派建议规则（" + categoryLabels(jsonCategories(profile.AssignedSuggestedCategories)) + "）"
		} else if profile.AssignedPortableTemplateID != "" {
			resp.ForcedSummary = "管理员已强制指派模板「" + s.templateName(r.Context(), profile.AssignedPortableTemplateID) + "」"
		}
	} else {
		if profile.UserConfigured {
			resp.EffectiveSource = "user"
		} else if profile.AssignedSuggestedCategories != "" || profile.AssignedPortableTemplateID != "" {
			resp.EffectiveSource = "panel"
			// 面板级非强制指派：用户级优先，缺席时生效。
			if profile.AssignedSuggestedCategories != "" {
				resp.PanelAssignedSummary = "管理员已设置默认建议规则（" + categoryLabels(jsonCategories(profile.AssignedSuggestedCategories)) + "），你的设置将优先生效"
			} else {
				resp.PanelAssignedSummary = "管理员已设置默认模板「" + s.templateName(r.Context(), profile.AssignedPortableTemplateID) + "」，你的设置将优先生效"
			}
		} else if !defaultCfg.Empty() {
			resp.EffectiveSource = "default"
			if defaultCfg.SuggestedCategories != "" {
				resp.DefaultSummary = "正在使用系统默认建议规则（" + categoryLabels(jsonCategories(defaultCfg.SuggestedCategories)) + "）"
			} else {
				resp.DefaultSummary = "正在使用系统默认模板「" + s.templateName(r.Context(), defaultCfg.PortableTemplateID) + "」"
			}
		} else {
			resp.EffectiveSource = "unset"
		}
	}
	// 实际生效配置（含四级解析结果）。
	resp.Effective = subRoutingConfigOf(effective)
	// 可选模板目录：仅含有缓存内容的模板可被选为生效配置。
	templates, err := s.st.ListSubscriptionTemplates(r.Context())
	if err != nil {
		return SubTemplateConfigResponse{}, err
	}
	resp.Templates = make([]SubTemplateOption, 0, len(templates))
	for _, template := range templates {
		if strings.TrimSpace(template.Content) == "" {
			continue
		}
		resp.Templates = append(resp.Templates, SubTemplateOption{
			ID: template.ID, Name: template.Name, Kind: template.Kind,
		})
	}
	return resp, nil
}

// HandleSubTemplateConfig 处理 GET /api/sub/{token}/template-config（仅凭 token 鉴权）。
func (s *Server) HandleSubTemplateConfig(w http.ResponseWriter, r *http.Request) {
	disableSharedCache(w)
	user, ok := s.userBySubToken(w, r)
	if !ok {
		return
	}
	resp, err := s.templateConfigResponse(r, user)
	if err != nil {
		writeInternalError(w, "serve sub template config", err)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(resp)
}

// panelRoutingConfigured 报告管理员是否已为该用户写入面板级/强制级指派槽位：
// 这些指派保存的地区分组开关属于管理员设置，用户「恢复默认」时不得覆盖。
func panelRoutingConfigured(profile store.SubscriptionProfile) bool {
	return profile.AssignedPortableTemplateID != "" || profile.AssignedSuggestedCategories != "" ||
		profile.AssignedMihomoTemplateID != "" || profile.AssignedSingboxTemplateID != "" ||
		profile.AssignedQuanXTemplateID != "" ||
		profile.AssignForcedPortable || profile.AssignForcedMihomo ||
		profile.AssignForcedSingbox || profile.AssignForcedQuanX
}

// userBySubToken 校验订阅 token 并返回用户；失败时已写好响应。
func (s *Server) userBySubToken(w http.ResponseWriter, r *http.Request) (*store.User, bool) {
	token := r.PathValue("token")
	user, err := s.st.UserBySubToken(r.Context(), token)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			http.Error(w, err.Error()+"\n", http.StatusNotFound)
			return nil, false
		}
		writeInternalError(w, "resolve subscription token", err)
		return nil, false
	}
	return user, true
}

// writeSubError 输出订阅公开 API 的 JSON 错误体（{"error": "..."}），
// 供订阅页表单直接展示校验信息。
func writeSubError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": message})
}

// HandleSubUpdateTemplateConfig 处理 POST /api/sub/{token}/template-config：
// 用户在订阅页保存自己的模板配置（写入用户级，优先于面板级与默认级），
// 保存后立即排队重生成订阅快照。面板侧各级配置不受影响。
func (s *Server) HandleSubUpdateTemplateConfig(w http.ResponseWriter, r *http.Request) {
	disableSharedCache(w)
	var input struct {
		Mode                string   `json:"mode"`
		Preset              string   `json:"preset"`
		Categories          []string `json:"categories"`
		PortableTemplateID  string   `json:"portable_template_id"`
		MihomoTemplateID    string   `json:"mihomo_template_id"`
		SingboxTemplateID   string   `json:"singbox_template_id"`
		QuanXTemplateID     string   `json:"quanx_template_id"`
		RegionGroupsEnabled *bool    `json:"region_groups_enabled"`
	}
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		writeSubError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	user, ok := s.userBySubToken(w, r)
	if !ok {
		return
	}
	if err := s.saveUserRouting(r, user.ID, input.Mode, input.Preset, input.Categories,
		input.PortableTemplateID, input.MihomoTemplateID, input.SingboxTemplateID,
		input.QuanXTemplateID, input.RegionGroupsEnabled); err != nil {
		writeSubError(w, http.StatusBadRequest, err.Error())
		return
	}
	// 用户修改后立即刷新订阅文件快照（异步去抖，与面板指派同一管线）。
	s.EnqueueUsers([]int64{user.ID}, s.base(r))
	s.writeTemplateConfig(w, r, user)
}

// saveUserRouting 校验并写入用户级路由配置（user_configured=1）。
func (s *Server) saveUserRouting(r *http.Request, userID int64,
	mode, preset string, categories []string,
	portableID, mihomoID, singboxID, quanxID string,
	regionGroupsEnabled *bool,
) error {
	current, err := s.st.UserSubscriptionProfile(r.Context(), userID)
	if err != nil {
		return err
	}
	// 只写用户级槽位：面板侧的指派/强制标记与地区分组开关原样保留。
	profile := store.SubscriptionProfile{
		UserID:             userID,
		Mode:               strings.TrimSpace(mode),
		Preset:             strings.TrimSpace(preset),
		PortableTemplateID: strings.TrimSpace(portableID),
		MihomoTemplateID:   strings.TrimSpace(mihomoID),
		SingboxTemplateID:  strings.TrimSpace(singboxID),
		QuanXTemplateID:    strings.TrimSpace(quanxID),
		GenerationStatus:   store.SubscriptionGenerationPending,
	}
	profile, err = NormalizeRouting(profile, categories)
	if err != nil {
		return err
	}
	if err := s.st.ValidateSubscriptionProfileTemplates(r.Context(), profile); err != nil {
		return err
	}
	profile.AssignedPortableTemplateID = current.AssignedPortableTemplateID
	profile.AssignForcedPortable = current.AssignForcedPortable
	profile.AssignedMihomoTemplateID = current.AssignedMihomoTemplateID
	profile.AssignForcedMihomo = current.AssignForcedMihomo
	profile.AssignedSingboxTemplateID = current.AssignedSingboxTemplateID
	profile.AssignForcedSingbox = current.AssignForcedSingbox
	profile.AssignedQuanXTemplateID = current.AssignedQuanXTemplateID
	profile.AssignForcedQuanX = current.AssignForcedQuanX
	profile.AssignedSuggestedCategories = current.AssignedSuggestedCategories
	profile.DisableRegionGroups = current.DisableRegionGroups
	if regionGroupsEnabled != nil {
		profile.DisableRegionGroups = !*regionGroupsEnabled
	}
	profile.UserConfigured = true
	return s.st.SaveUserSubscriptionProfile(r.Context(), profile)
}

// HandleSubResetTemplateConfig 处理 POST /api/sub/{token}/template-config/reset：
// 用户清除自己的用户级配置（清除与修改只发生在订阅页，面板侧只读），
// 回落到面板级 → 默认级；清除后立即重生成订阅快照。
func (s *Server) HandleSubResetTemplateConfig(w http.ResponseWriter, r *http.Request) {
	disableSharedCache(w)
	user, ok := s.userBySubToken(w, r)
	if !ok {
		return
	}
	current, err := s.st.UserSubscriptionProfile(r.Context(), user.ID)
	if err != nil {
		writeSubError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if current.UserConfigured {
		// 用户级的地区分组开关随「恢复默认」一并清除：无管理员指派时写内置默认
		// （开启），默认级开关仍由 EffectiveProfile 在生效时覆盖，不把默认级的值
		// 固化进共享列（否则之后清除默认级会残留旧状态）。该列由管理员指派与用户
		// 共用，存在面板级/强制级指派时只能保留当前值、无法还原管理员写入值。
		disableRegionGroups := false
		if panelRoutingConfigured(current) {
			disableRegionGroups = current.DisableRegionGroups
		}
		reset := store.SubscriptionProfile{
			UserID:                      user.ID,
			Mode:                        store.SubscriptionModeSuggested,
			Preset:                      "balanced",
			CategoriesJSON:              `["ai","youtube","google","private","domestic","telegram","github","overseas"]`,
			GenerationStatus:            store.SubscriptionGenerationPending,
			AssignedPortableTemplateID:  current.AssignedPortableTemplateID,
			AssignForcedPortable:        current.AssignForcedPortable,
			AssignedMihomoTemplateID:    current.AssignedMihomoTemplateID,
			AssignForcedMihomo:          current.AssignForcedMihomo,
			AssignedSingboxTemplateID:   current.AssignedSingboxTemplateID,
			AssignForcedSingbox:         current.AssignForcedSingbox,
			AssignedQuanXTemplateID:     current.AssignedQuanXTemplateID,
			AssignForcedQuanX:           current.AssignForcedQuanX,
			AssignedSuggestedCategories: current.AssignedSuggestedCategories,
			DisableRegionGroups:         disableRegionGroups,
		}
		if err := s.st.SaveUserSubscriptionProfile(r.Context(), reset); err != nil {
			writeSubError(w, http.StatusInternalServerError, "internal error")
			return
		}
		s.EnqueueUsers([]int64{user.ID}, s.base(r))
	}
	s.writeTemplateConfig(w, r, user)
}

// writeTemplateConfig 以 JSON 输出当前模板配置响应。
func (s *Server) writeTemplateConfig(w http.ResponseWriter, r *http.Request, user *store.User) {
	resp, err := s.templateConfigResponse(r, user)
	if err != nil {
		writeInternalError(w, "serve sub template config", err)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(resp)
}

package panel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"lattix/backend/internal/extsub"
	"lattix/backend/internal/progress"
	"lattix/backend/internal/store"
	"lattix/shared"
)

// userPublishObserveStages 用户变更类操作的观察阶段：写库后订阅发布转异步，
// 由 regenerator 完成回调收口（WatchUsers 先于 EnqueueUsers 登记，消除竞态）。
var userPublishObserveStages = []progress.Stage{
	{Key: "db", Label: "校验并写入数据库"},
	{Key: "regenerate", Label: "重新生成订阅文件"},
}

// userDTO 是用户对象的 API 表示。
type userDTO struct {
	ID                    int64                            `json:"id"`
	Name                  string                           `json:"name"`
	UUID                  string                           `json:"uuid"`
	SubToken              string                           `json:"sub_token"`
	SubURL                string                           `json:"sub_url"`
	SubLinksURL           string                           `json:"sub_links_url"`
	NodeIDs               []int64                          `json:"node_ids"`
	ChainIDs              []int64                          `json:"chain_ids"`
	EffectiveChainIDs     []int64                          `json:"effective_chain_ids"` // 生效链路：分组用户为分组派生，非分组用户同 chain_ids
	ChainAssignments      []userChainAssignmentDTO         `json:"chain_assignments"`
	ExternalSubscriptions []userExternalSubscriptionDTO    `json:"external_subscriptions"`
	MergedTraffic         *mergedTrafficDTO                `json:"merged_traffic,omitempty"`
	Traffic               *trafficDTO                      `json:"traffic"`
	ExpiresAt             *time.Time                       `json:"expires_at"`
	Expired               bool                             `json:"expired"`
	Disabled              bool                             `json:"disabled"`
	EffectiveAt           *time.Time                       `json:"effective_at"` // 生效时刻（null=立即生效）
	IsTrial               bool                             `json:"is_trial"`
	QuotaExhausted        bool                             `json:"quota_exhausted"` // 流量配额停权（§9）
	TrafficLimit          int64                            `json:"traffic_limit"`
	TrafficResetDay       int                              `json:"traffic_reset_day"`
	SubTitle              string                           `json:"sub_title"`
	SubAnnouncement       string                           `json:"sub_announcement"`
	PlanName              string                           `json:"plan_name"`
	AppURL                string                           `json:"app_url"`
	Routing               subscriptionProfileDTO           `json:"routing"`
	SubscriptionSnapshot  store.SubscriptionSnapshotStatus `json:"subscription_snapshot"`
	OnlineConnections     int                              `json:"online_connections"` // 跨服务器去重后的在线连接数（telemetry 快照）
	UserGroupIDs          []int64                          `json:"user_group_ids"`
	CreatedAt             time.Time                        `json:"created_at"`
}

type userChainAssignmentDTO struct {
	ID         int64  `json:"id"`
	ChainID    int64  `json:"chain_id"`
	EndpointID int64  `json:"endpoint_id"`
	AccessUUID string `json:"access_uuid"`
}

type userExternalSubscriptionDTO struct {
	SubscriptionID int64  `json:"subscription_id"`
	Name           string `json:"name"`
	Mode           string `json:"mode"`
	Upload         int64  `json:"upload"`
	Download       int64  `json:"download"`
	Total          int64  `json:"total"`
	Expire         *int64 `json:"expire,omitempty"`
	Remaining      *int64 `json:"remaining"` // total=0（未知额度）时为 null
	NodeCount      int    `json:"node_count"`
}

type mergedTrafficDTO struct {
	Upload   int64  `json:"upload"`
	Download int64  `json:"download"`
	Total    int64  `json:"total"`
	Expire   *int64 `json:"expire,omitempty"`
}

func (s *Server) toUserDTO(r *http.Request, u store.User, nodeIDs []int64) userDTO {
	dto := userDTO{
		ID:              u.ID,
		Name:            u.Name,
		UUID:            u.UUID,
		SubToken:        u.SubToken,
		SubURL:          fmt.Sprintf("%s/sub/%s", s.panelBase(r), u.SubToken),
		SubLinksURL:     fmt.Sprintf("%s/sub/%s?format=links", s.panelBase(r), u.SubToken),
		NodeIDs:         nodeIDs,
		ExpiresAt:       u.ExpiresAt,
		Expired:         u.Expired,
		Disabled:        u.Disabled,
		EffectiveAt:     u.EffectiveAt,
		IsTrial:         u.IsTrial,
		QuotaExhausted:  u.QuotaExhausted,
		TrafficLimit:    u.TrafficLimit,
		TrafficResetDay: u.TrafficResetDay,
		SubTitle:        u.SubTitle,
		SubAnnouncement: u.SubAnnouncement,
		PlanName:        u.PlanName,
		AppURL:          u.AppURL,
		CreatedAt:       u.CreatedAt,
	}
	if assignments, err := s.st.UserChainAssignments(r.Context(), u.ID); err == nil {
		for _, assignment := range assignments {
			dto.ChainIDs = append(dto.ChainIDs, assignment.ChainID)
			dto.ChainAssignments = append(dto.ChainAssignments, userChainAssignmentDTO{
				ID: assignment.ID, ChainID: assignment.ChainID, EndpointID: assignment.EndpointID,
				AccessUUID: assignment.AccessUUID,
			})
		}
	}
	// 生效链路（分组派生或直接，§分组）：用户卡片按此展示实际订阅内容。
	if effective, err := s.st.EffectiveUserChainAssignments(r.Context(), u.ID); err == nil {
		for _, assignment := range effective {
			dto.EffectiveChainIDs = append(dto.EffectiveChainIDs, assignment.ChainID)
		}
	}
	if dto.ChainIDs == nil {
		dto.ChainIDs = []int64{}
	}
	if dto.EffectiveChainIDs == nil {
		dto.EffectiveChainIDs = []int64{}
	}
	if dto.ChainAssignments == nil {
		dto.ChainAssignments = []userChainAssignmentDTO{}
	}
	if profile, err := s.st.UserSubscriptionProfile(r.Context(), u.ID); err == nil {
		dto.Routing = subscriptionProfileToDTO(profile)
	}
	if snapshot, err := s.st.SubscriptionSnapshotStatus(r.Context(), u.ID); err == nil {
		dto.SubscriptionSnapshot = snapshot
	} else {
		dto.SubscriptionSnapshot = store.SubscriptionSnapshotStatus{
			Status: store.SubscriptionGenerationError,
			Error:  fmt.Sprintf("读取订阅快照状态失败：%v", err),
		}
	}
	attached, err := s.st.EffectiveUserExternalSubscriptions(r.Context(), u.ID)
	if err == nil && len(attached) > 0 {
		dto.ExternalSubscriptions = make([]userExternalSubscriptionDTO, 0, len(attached))
		var panelTraffic store.TrafficTotals
		if t, err := s.st.UserTraffic(r.Context(), u.UUID); err == nil {
			panelTraffic = t
		}
		var panelExpire *int64
		if u.ExpiresAt != nil {
			v := u.ExpiresAt.Unix()
			panelExpire = &v
		}
		for _, sub := range attached {
			var remaining *int64
			if sub.Total > 0 {
				v := sub.Total - sub.Upload - sub.Download
				if v < 0 {
					v = 0
				}
				remaining = &v
			}
			dto.ExternalSubscriptions = append(dto.ExternalSubscriptions, userExternalSubscriptionDTO{
				SubscriptionID: sub.SubscriptionID, Name: sub.Name, Mode: sub.Mode,
				Upload: sub.Upload, Download: sub.Download, Total: sub.Total,
				Expire: sub.Expire, Remaining: remaining, NodeCount: sub.NodeCount,
			})
		}
		merged := extsub.MergeUserTraffic(time.Now(), extsub.Traffic{
			Upload: panelTraffic.Up, Download: panelTraffic.Down,
			Total: u.TrafficLimit, Expire: panelExpire,
		}, attached)
		dto.MergedTraffic = &mergedTrafficDTO{
			Upload: merged.Upload, Download: merged.Download,
			Total: merged.Total, Expire: merged.Expire,
		}
	}
	if dto.ExternalSubscriptions == nil {
		dto.ExternalSubscriptions = []userExternalSubscriptionDTO{}
	}
	if groupIDs, err := s.st.UserGroupIDsForUser(r.Context(), u.ID); err == nil {
		dto.UserGroupIDs = groupIDs
	} else {
		dto.UserGroupIDs = []int64{}
	}
	if dto.UserGroupIDs == nil {
		dto.UserGroupIDs = []int64{}
	}
	return dto
}

type subscriptionProfileInput struct {
	RegionGroupsEnabled *bool    `json:"region_groups_enabled"`
	Mode                string   `json:"mode"`
	Preset              string   `json:"preset"`
	Categories          []string `json:"categories"`
	PortableTemplateID  string   `json:"portable_template_id"`
	MihomoTemplateID    string   `json:"mihomo_template_id"`
	SingboxTemplateID   string   `json:"singbox_template_id"`
	QuanXTemplateID     string   `json:"quanx_template_id"`
	Forced              bool     `json:"forced"` // 勾选覆盖 → 强制级；不勾选 → 面板级
	// 以下为 GET DTO 的只读回显字段：前端把 routing 对象原样回传，本接口忽略它们
	//（用户级字段仅在订阅页修改，面板侧只读，保存时原样保留）。
	AssignedPortableTemplateID  string   `json:"assigned_portable_template_id"`
	AssignForcedPortable        bool     `json:"assign_forced_portable"`
	AssignedMihomoTemplateID    string   `json:"assigned_mihomo_template_id"`
	AssignForcedMihomo          bool     `json:"assign_forced_mihomo"`
	AssignedSingboxTemplateID   string   `json:"assigned_singbox_template_id"`
	AssignForcedSingbox         bool     `json:"assign_forced_singbox"`
	AssignedQuanXTemplateID     string   `json:"assigned_quanx_template_id"`
	AssignForcedQuanX           bool     `json:"assign_forced_quanx"`
	AssignedSuggestedCategories []string `json:"assigned_suggested_categories"`
}

type subscriptionProfileDTO struct {
	RegionGroupsEnabled         bool     `json:"region_groups_enabled"`
	Mode                        string   `json:"mode"`
	Preset                      string   `json:"preset"`
	Categories                  []string `json:"categories"`
	PortableTemplateID          string   `json:"portable_template_id"`
	MihomoTemplateID            string   `json:"mihomo_template_id"`
	SingboxTemplateID           string   `json:"singbox_template_id"`
	QuanXTemplateID             string   `json:"quanx_template_id"`
	UserConfigured              bool     `json:"user_configured"`
	AssignedPortableTemplateID  string   `json:"assigned_portable_template_id"`
	AssignForcedPortable        bool     `json:"assign_forced_portable"`
	AssignedMihomoTemplateID    string   `json:"assigned_mihomo_template_id"`
	AssignForcedMihomo          bool     `json:"assign_forced_mihomo"`
	AssignedSingboxTemplateID   string   `json:"assigned_singbox_template_id"`
	AssignForcedSingbox         bool     `json:"assign_forced_singbox"`
	AssignedQuanXTemplateID     string   `json:"assigned_quanx_template_id"`
	AssignForcedQuanX           bool     `json:"assign_forced_quanx"`
	AssignedSuggestedCategories []string `json:"assigned_suggested_categories"`
}

func assignedSuggestedCategories(profile store.SubscriptionProfile) []string {
	ids := []string{}
	_ = json.Unmarshal([]byte(profile.AssignedSuggestedCategories), &ids)
	return ids
}

func subscriptionProfileToDTO(profile store.SubscriptionProfile) subscriptionProfileDTO {
	var categories []string
	_ = json.Unmarshal([]byte(profile.CategoriesJSON), &categories)
	return subscriptionProfileDTO{
		RegionGroupsEnabled: !profile.DisableRegionGroups,
		Mode:                profile.Mode, Preset: profile.Preset, Categories: categories,
		PortableTemplateID: profile.PortableTemplateID, MihomoTemplateID: profile.MihomoTemplateID,
		SingboxTemplateID: profile.SingboxTemplateID, QuanXTemplateID: profile.QuanXTemplateID,
		UserConfigured:              profile.UserConfigured,
		AssignedPortableTemplateID:  profile.AssignedPortableTemplateID,
		AssignForcedPortable:        profile.AssignForcedPortable,
		AssignedMihomoTemplateID:    profile.AssignedMihomoTemplateID,
		AssignForcedMihomo:          profile.AssignForcedMihomo,
		AssignedSingboxTemplateID:   profile.AssignedSingboxTemplateID,
		AssignForcedSingbox:         profile.AssignForcedSingbox,
		AssignedQuanXTemplateID:     profile.AssignedQuanXTemplateID,
		AssignForcedQuanX:           profile.AssignForcedQuanX,
		AssignedSuggestedCategories: assignedSuggestedCategories(profile),
	}
}

// routingSlotValues 是管理员路由表单折算出的面板级/强制级槽位写入值。
type routingSlotValues struct {
	assignedPortable    string
	assignedSuggested   string
	assignedMihomo      string
	assignedSingbox     string
	assignedQuanx       string
	forcedPortable      bool
	forcedMihomo        bool
	forcedSingbox       bool
	forcedQuanx         bool
	disableRegionGroups bool
	regionGroupsTouched bool
}

// templateCheckProfile 把槽位值折叠为用户槽位视图以复用模板存在性校验。
func (v routingSlotValues) templateCheckProfile() store.SubscriptionProfile {
	return store.SubscriptionProfile{
		PortableTemplateID: v.assignedPortable,
		MihomoTemplateID:   v.assignedMihomo,
		SingboxTemplateID:  v.assignedSingbox,
		QuanXTemplateID:    v.assignedQuanx,
	}
}

// routingSlotsFromInput 把管理员路由表单折算为指派槽位写入值（不含用户级字段）：
// 勾选覆盖 → 强制级（forced=1），不勾选 → 面板级（forced=0）。主策略建议规则分组与
// 中立模板互斥；两者均空 = 清除该级主策略配置（用户回落默认级）。
func routingSlotsFromInput(input *subscriptionProfileInput) (routingSlotValues, error) {
	if input == nil {
		return routingSlotValues{}, nil
	}
	var slots routingSlotValues
	switch mode := strings.TrimSpace(input.Mode); mode {
	case "", store.SubscriptionModeSuggested:
		// 空分组 = 清除面板级主策略（用户回落面板级指派空缺 → 默认级），不是错误。
		var categories []string
		if len(input.Categories) > 0 {
			normalized, err := normalizeSuggestedCategories(input.Categories)
			if err != nil {
				return routingSlotValues{}, err
			}
			categories = normalized
			raw, err := json.Marshal(categories)
			if err != nil {
				return routingSlotValues{}, err
			}
			slots.assignedSuggested = string(raw)
		}
		slots.forcedPortable = input.Forced && slots.assignedSuggested != ""
	case store.SubscriptionModeTemplate:
		slots.assignedPortable = strings.TrimSpace(input.PortableTemplateID)
		slots.forcedPortable = input.Forced && slots.assignedPortable != ""
	default:
		return routingSlotValues{}, errors.New("订阅规则模式无效")
	}
	slots.assignedMihomo = strings.TrimSpace(input.MihomoTemplateID)
	slots.assignedSingbox = strings.TrimSpace(input.SingboxTemplateID)
	slots.assignedQuanx = strings.TrimSpace(input.QuanXTemplateID)
	slots.forcedMihomo = input.Forced && slots.assignedMihomo != ""
	slots.forcedSingbox = input.Forced && slots.assignedSingbox != ""
	slots.forcedQuanx = input.Forced && slots.assignedQuanx != ""
	if input.RegionGroupsEnabled != nil {
		slots.disableRegionGroups = !*input.RegionGroupsEnabled
		slots.regionGroupsTouched = true
	}
	return slots, nil
}

// applyRoutingSlots 把表单折算的槽位写入 profile：仅覆盖面板级/强制级槽位与地区分组
// 开关（表单显式给出时），用户级字段与 user_configured 原样保留（面板侧只读）。
func applyRoutingSlots(profile *store.SubscriptionProfile, slots routingSlotValues) {
	profile.AssignedPortableTemplateID = slots.assignedPortable
	profile.AssignedSuggestedCategories = slots.assignedSuggested
	profile.AssignedMihomoTemplateID = slots.assignedMihomo
	profile.AssignedSingboxTemplateID = slots.assignedSingbox
	profile.AssignedQuanXTemplateID = slots.assignedQuanx
	profile.AssignForcedPortable = slots.forcedPortable
	profile.AssignForcedMihomo = slots.forcedMihomo
	profile.AssignForcedSingbox = slots.forcedSingbox
	profile.AssignForcedQuanX = slots.forcedQuanx
	if slots.regionGroupsTouched {
		profile.DisableRegionGroups = slots.disableRegionGroups
	}
}

func (s *Server) validateSubscriptionProfileTemplates(ctx context.Context, profile store.SubscriptionProfile) error {
	return s.st.ValidateSubscriptionProfileTemplates(ctx, profile)
}

// handleListUsers 处理 GET /api/users。
func (s *Server) handleListUsers(w http.ResponseWriter, r *http.Request) {
	users, err := s.st.ListUsers(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	traffic, err := s.st.TrafficByUser(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]userDTO, 0, len(users))
	for _, u := range users {
		nodeIDs, err := s.st.UserNodeIDs(r.Context(), u.ID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		dto := s.toUserDTO(r, u, nodeIDs)
		dto.OnlineConnections = s.OnlineUsers().ConnectionsByUser(u.UUID, time.Now().UTC())
		if t, ok := traffic[u.UUID]; ok {
			dto.Traffic = &trafficDTO{Up: t.Up, Down: t.Down}
		}
		out = append(out, dto)
	}
	writeJSON(w, http.StatusOK, out)
}

// handleCreateUser 处理 POST /api/users：生成 UUID 与 sub_token；可带 expires_at（RFC3339，§9）。
// 新用户默认全关（§16）：不分配任何节点，不下发 add_user；
// 管理员经 PUT /api/users/{id}/nodes 分配后才增量扇出。
// 创建时可选带 node_ids 预选产品层链路（内部仍映射业务 node_id），省略则维持默认全关（§16）。
// 试用用户（is_trial，§9）：生效时刻 effective_at（RFC3339，省略=立即）、有效期必须用
// duration_days（自生效时刻起算，精确到秒）或 expires_at 二选一表达、可直选用户分组
// user_group_id；traffic_limit 对试用用户强制执行（用尽即停权、不删除），到期由 sweeper 自动删除。
// 生效时刻在未来的用户为未生效态：预选节点/链路照常入库，但不扇出 add_user（到点由 sweeper 激活）。
func (s *Server) handleCreateUser(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name      string  `json:"name"`
		ExpiresAt *string `json:"expires_at"` // RFC3339，省略/null = 长期
		NodeIDs   []int64 `json:"node_ids"`   // 可选：预选链路对应的业务节点
		ChainIDs  []int64 `json:"chain_ids"`
		// 试用调度（§9）：IsTrial + EffectiveAt（省略=立即生效）；DurationDays 与
		// ExpiresAt 二选一（同时给出 = 400），天数自生效时刻起算、精确到秒。
		IsTrial      bool    `json:"is_trial"`
		EffectiveAt  *string `json:"effective_at"` // RFC3339，省略 = 立即生效
		DurationDays int     `json:"duration_days"`
		UserGroupID  int64   `json:"user_group_id"` // 0 = 不入组
		// 可选订阅设置（省略保持默认；用户级覆盖全局，§9）。
		TrafficLimit          int64                           `json:"traffic_limit"`
		TrafficResetDay       int                             `json:"traffic_reset_day"`
		PlanName              string                          `json:"plan_name"`
		AppURL                string                          `json:"app_url"`
		Routing               *subscriptionProfileInput       `json:"routing"`
		ExternalSubscriptions []userExternalSubscriptionInput `json:"external_subscriptions"`
	}
	if err := readJSON(r, &req); err != nil {
		writeProtocolError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "name 不能为空")
		return
	}
	effectiveAt, err := parseEffectiveAt(req.EffectiveAt)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.DurationDays < 0 {
		writeError(w, http.StatusBadRequest, "duration_days 不能为负")
		return
	}
	expiresAt, err := parseExpiresAt(req.ExpiresAt)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.DurationDays > 0 && req.ExpiresAt != nil && *req.ExpiresAt != "" {
		writeError(w, http.StatusBadRequest, "expires_at 与 duration_days 只能二选一")
		return
	}
	base := time.Now()
	if effectiveAt != nil && effectiveAt.After(base) {
		base = *effectiveAt
	}
	if req.DurationDays > 0 {
		t := base.Add(time.Duration(req.DurationDays) * 24 * time.Hour)
		expiresAt = &t
	}
	if expiresAt != nil && !expiresAt.After(time.Now()) {
		writeError(w, http.StatusBadRequest, "expires_at 不能是过去的时间")
		return
	}
	if effectiveAt != nil && expiresAt != nil && !expiresAt.After(*effectiveAt) {
		writeError(w, http.StatusBadRequest, "expires_at 必须晚于生效时间")
		return
	}
	if req.IsTrial && expiresAt == nil {
		writeError(w, http.StatusBadRequest, "试用用户必须设置有效期（按天数或到期时间）")
		return
	}
	if req.TrafficLimit < 0 {
		req.TrafficLimit = 0
	}
	if err := validateTrafficResetDay(req.TrafficResetDay); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	var userGroup *store.UserGroup
	if req.UserGroupID > 0 {
		userGroup, err = s.st.UserGroupByID(r.Context(), req.UserGroupID)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				writeError(w, http.StatusBadRequest, "用户分组不存在")
				return
			}
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	if err := s.st.ValidateAssignableChains(r.Context(), req.ChainIDs); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	u := store.User{
		Name:        req.Name,
		UUID:        uuid.NewString(),
		SubToken:    randomHex(16), // 128-bit（评审 P3：订阅 URL 为长期 bearer 凭据）
		ExpiresAt:   expiresAt,
		EffectiveAt: effectiveAt,
		IsTrial:     req.IsTrial,
	}
	o := s.observeStart(r, "user.create", "创建用户", userPublishObserveStages)
	defer o.CloseIfPending()
	id, err := s.st.InsertUserFull(r.Context(), u)
	if err != nil {
		o.Fail(err)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	created, err := s.st.UserByID(r.Context(), id)
	if err != nil {
		o.Fail(err)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// 路由表单写入面板级/强制级槽位；用户级字段保持初始默认（suggested/balanced），
	// 未配置面板级时新用户回落默认级。主策略建议规则分组与中立模板互斥。
	profile := store.SubscriptionProfile{
		UserID: id, Mode: store.SubscriptionModeSuggested, Preset: "balanced",
		CategoriesJSON:   `["ai","youtube","google","private","domestic","telegram","github","overseas"]`,
		GenerationStatus: store.SubscriptionGenerationPending,
	}
	if req.Routing != nil {
		slots, err := routingSlotsFromInput(req.Routing)
		if err != nil {
			o.Fail(err)
			_ = s.st.DeleteUser(r.Context(), id)
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if err := s.validateSubscriptionProfileTemplates(r.Context(), slots.templateCheckProfile()); err != nil {
			o.Fail(err)
			_ = s.st.DeleteUser(r.Context(), id)
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		applyRoutingSlots(&profile, slots)
	}
	if err := s.st.SaveUserSubscriptionProfile(r.Context(), profile); err != nil {
		o.Fail(err)
		_ = s.st.DeleteUser(r.Context(), id)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// 可选订阅设置：任一项非默认时写入（用户级覆盖全局）。
	if req.TrafficLimit != 0 || req.TrafficResetDay != 0 || req.PlanName != "" || req.AppURL != "" {
		if err := s.st.SetUserSubSettings(r.Context(), id, store.UserSubSettings{
			TrafficLimit: req.TrafficLimit, ResetDay: req.TrafficResetDay,
			PlanName: strings.TrimSpace(req.PlanName), AppURL: strings.TrimSpace(req.AppURL),
		}); err != nil {
			o.Fail(err)
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		created.TrafficLimit = req.TrafficLimit
		created.TrafficResetDay = req.TrafficResetDay
		created.PlanName = strings.TrimSpace(req.PlanName)
		created.AppURL = strings.TrimSpace(req.AppURL)
	}

	// 直选用户分组（§分组）：一用户一组，加入即按分组派生链路（直接分配被遮蔽）。
	// 关联链路分组的共享端点随后 reconcile，使分组派生凭据到点生效。
	if userGroup != nil {
		if err := s.st.SetUserGroupMembership(r.Context(), id, userGroup.ID); err != nil {
			o.Fail(err)
			_ = s.st.DeleteUser(r.Context(), id)
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		s.triggerGroupChange(r.Context(), nil, s.endpointIDsForLinkGroups(r.Context(), userGroup.LinkGroupIDs))
	}

	// 未生效态（§9）：分配照常入库，但不扇出 add_user —— 生效时刻到点由 sweeper 激活。
	notYetEffective := created.NotYetEffective(time.Now())

	// 可选预选链路（§16）：校验底层业务节点存在后按差量扇出 add_user。
	nodeIDs := []int64{}
	if len(req.NodeIDs) > 0 {
		nodes, err := s.st.ListNodes(r.Context())
		if err != nil {
			o.Fail(err)
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		validNodes := map[int64]bool{}
		for _, n := range nodes {
			validNodes[n.ID] = true
		}
		seen := map[int64]bool{}
		for _, nodeID := range req.NodeIDs {
			if !validNodes[nodeID] {
				err := fmt.Errorf("链路对应节点 %d 不存在", nodeID)
				o.Fail(err)
				writeError(w, http.StatusBadRequest, err.Error())
				return
			}
			if !seen[nodeID] {
				seen[nodeID] = true
				nodeIDs = append(nodeIDs, nodeID)
			}
		}
		added, _, err := s.st.SetUserNodes(r.Context(), id, nodeIDs)
		if err != nil {
			o.Fail(err)
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if !notYetEffective {
			s.fanoutUserDiff(r.Context(), created.UUID, nodes, added, nil)
		}
	}
	if len(req.ChainIDs) > 0 {
		added, _, err := s.st.SetUserChains(r.Context(), id, req.ChainIDs)
		if err != nil {
			o.Fail(err)
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		s.reconcileAssignmentEndpoints(r.Context(), added, nil)
	}
	if len(req.ExternalSubscriptions) > 0 {
		items, err := s.validateExternalSubscriptions(r.Context(), req.ExternalSubscriptions)
		if err != nil {
			o.Fail(err)
			_ = s.st.DeleteUser(r.Context(), id)
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		for i := range items {
			items[i].UserID = id
		}
		if err := s.st.SetUserExternalSubscriptions(r.Context(), id, items); err != nil {
			o.Fail(err)
			_ = s.st.DeleteUser(r.Context(), id)
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	o.Report("db", 100, "用户已保存")
	if s.subscriptions != nil {
		// 发布转异步：观察先行登记，regenerator 完成回调收口；失败以警告呈现。
		o.WatchUsers([]int64{id})
		s.subscriptions.EnqueueUsers([]int64{id}, s.panelBase(r))
		o.Report("regenerate", 0, "等待订阅重生成")
	}
	s.audit(r, "user.create", nil, nil, map[string]any{
		"user_id": created.ID, "name": created.Name, "node_count": len(nodeIDs),
		"is_trial": created.IsTrial, "effective_at": logTime(created.EffectiveAt),
	})
	writeJSON(w, http.StatusCreated, s.toUserDTO(r, *created, nodeIDs))
}

func validateTrafficResetDay(day int) error {
	if day < 0 || day > 31 {
		return errors.New("重置日须为 0 或 1–31（0=创建日）")
	}
	return nil
}

// parseExpiresAt 解析 RFC3339 有效期；nil/空串 = 长期。
func parseExpiresAt(raw *string) (*time.Time, error) {
	if raw == nil || *raw == "" {
		return nil, nil
	}
	t, err := time.Parse(time.RFC3339, *raw)
	if err != nil {
		return nil, fmt.Errorf("expires_at 格式无效（需 RFC3339）")
	}
	return &t, nil
}

// parseEffectiveAt 解析 RFC3339 生效时刻（§9）；nil/空串 = 立即生效。
// 允许过去时刻：语义上等同立即生效（不回退管理员已错过的计划时间）。
func parseEffectiveAt(raw *string) (*time.Time, error) {
	if raw == nil || *raw == "" {
		return nil, nil
	}
	t, err := time.Parse(time.RFC3339, *raw)
	if err != nil {
		return nil, fmt.Errorf("effective_at 格式无效（需 RFC3339）")
	}
	return &t, nil
}

// handleUpdateUser 处理 PATCH /api/users/{id}：修改/清除有效期（§9）与显式停用/启用（§16）。
// 载荷 {"expires_at": "RFC3339" 或 null, "disabled": bool}，省略的字段保持不变；
// expires_at 传 null = 清除（长期）。与创建一致，expires_at 不允许设为过去时间（400）——
// "借到期立即停权"由 disabled 开关承担。
// 有效停权态 = disabled OR expired OR 未生效（effective_at 在未来，§9）：add_user/remove_user
// 扇出只在有效停权态跃迁时发生——已 expired 的用户再 disable（或反之）不重复扇出；
// 恢复需全部解除（未生效态的解除由生效时刻到点 sweeper 激活，不经本端点）。
func (s *Server) handleUpdateUser(w http.ResponseWriter, r *http.Request) {
	var req struct {
		UserID    int64           `json:"user_id"`
		ExpiresAt json.RawMessage `json:"expires_at"` // 省略 = 不变；null = 清除（长期）
		Disabled  *bool           `json:"disabled"`   // 省略 = 不变
	}
	if err := readJSON(r, &req); err != nil {
		writeProtocolError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	id := req.UserID
	if id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid user id")
		return
	}
	u, err := s.st.UserByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "用户不存在")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	now := time.Now()
	stoppedBefore := u.Disabled || u.Expired || u.QuotaExhausted || u.NotYetEffective(now)

	expiryTouched := len(req.ExpiresAt) > 0
	if expiryTouched {
		var expiresAt *time.Time
		if string(req.ExpiresAt) != "null" {
			var raw string
			if err := json.Unmarshal(req.ExpiresAt, &raw); err != nil {
				writeError(w, http.StatusBadRequest, "expires_at 格式无效（需 RFC3339 或 null）")
				return
			}
			t, err := time.Parse(time.RFC3339, raw)
			if err != nil {
				writeError(w, http.StatusBadRequest, "expires_at 格式无效（需 RFC3339）")
				return
			}
			expiresAt = &t
		}
		if expiresAt != nil && !expiresAt.After(now) {
			writeError(w, http.StatusBadRequest, "expires_at 不能是过去的时间")
			return
		}
		if err := s.st.SetUserExpiry(r.Context(), id, expiresAt, now); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}

	if req.Disabled != nil && *req.Disabled != u.Disabled {
		if err := s.st.SetUserDisabled(r.Context(), id, *req.Disabled); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}

	// 有效停权态跃迁才扇出（§9/§16）。恢复判定基于写入后的状态：SetUserExpiry 对
	// 到期试用用户会一并解除配额停权，用变更前的 u 计算会漏发 add_user。
	after, err := s.st.UserByID(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// 停权跃迁需重发布订阅：此时注册观察（nil 安全，未注册时方法全为 no-op），发布转异步。
	var o *progress.Observation
	defer func() { o.CloseIfPending() }()
	stoppedAfter := after.Disabled || after.Expired || after.QuotaExhausted || after.NotYetEffective(now)
	if stoppedBefore != stoppedAfter {
		nodes, err := s.st.ListNodes(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		assigned, err := s.st.UserNodeIDs(r.Context(), id)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if stoppedAfter {
			s.fanoutUserDiff(r.Context(), u.UUID, nodes, nil, assigned)
			log.Printf("panel: user %d (%s) 已停权，扇出 remove_user (%d 节点)", id, u.Name, len(assigned))
		} else {
			s.fanoutUserDiff(r.Context(), u.UUID, nodes, assigned, nil)
			log.Printf("panel: user %d (%s) 已恢复，扇出 add_user (%d 节点)", id, u.Name, len(assigned))
		}
		if assignments, err := s.st.UserChainAssignments(r.Context(), id); err == nil {
			s.reconcileUserEndpoints(r.Context(), id, assignments)
		}
		o = s.observeStart(r, "user.update", "更新用户状态", userPublishObserveStages)
		o.Report("db", 100, "已保存")
		if s.subscriptions != nil {
			o.WatchUsers([]int64{id})
			s.subscriptions.EnqueueUsers([]int64{id}, s.panelBase(r))
			o.Report("regenerate", 0, "等待订阅重生成")
		}
	}
	updated, err := s.st.UserByID(r.Context(), id)
	if err != nil {
		o.Fail(err)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	nodeIDs, err := s.st.UserNodeIDs(r.Context(), id)
	if err != nil {
		o.Fail(err)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	changes := changedValues(
		map[string]any{"expires_at": logTime(u.ExpiresAt), "disabled": u.Disabled},
		map[string]any{"expires_at": logTime(updated.ExpiresAt), "disabled": updated.Disabled},
	)
	if len(changes) > 0 {
		changes["user"] = map[string]any{"id": updated.ID, "name": updated.Name}
		s.audit(r, "user.updated", nil, nil, changes)
	}
	writeJSON(w, http.StatusOK, s.toUserDTO(r, *updated, nodeIDs))
}

func logTime(value *time.Time) any {
	if value == nil {
		return nil
	}
	return value.UTC().Format(time.RFC3339)
}

// handleSetUserNodes 处理 POST /api/user/set-nodes：整体替换用户的节点/链分配（§16），
// 按差量向相关服务器扇出 add_user / remove_user（载荷仅含受影响的节点）。
// 遗留兼容端点：现行授权模型为用户组/链路分组派生链分配（§22），本端点保留给历史/独立节点场景（§8）。
func (s *Server) handleSetUserNodes(w http.ResponseWriter, r *http.Request) {
	var req struct {
		UserID   int64   `json:"user_id"`
		NodeIDs  []int64 `json:"node_ids"`
		ChainIDs []int64 `json:"chain_ids"`
	}
	if err := readJSON(r, &req); err != nil {
		writeProtocolError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	id := req.UserID
	if id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid user id")
		return
	}
	u, err := s.st.UserByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "用户不存在")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	nodes, err := s.st.ListNodes(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	valid := map[int64]bool{}
	for _, n := range nodes {
		valid[n.ID] = true
	}
	for _, nid := range req.NodeIDs {
		if !valid[nid] {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("节点 %d 不存在", nid))
			return
		}
	}
	if err := s.st.ValidateAssignableChains(r.Context(), req.ChainIDs); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	beforeNodeIDs, err := s.st.UserNodeIDs(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	o := s.observeStart(r, "user.set_nodes", "更新用户分配", userPublishObserveStages)
	defer o.CloseIfPending()
	added, removed, err := s.st.SetUserNodes(r.Context(), id, req.NodeIDs)
	if err != nil {
		o.Fail(err)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// 有效停权态（disabled/expired/配额停权/未生效，§9/§16）只扇出 remove 侧：
	// add 侧留给停权解除（含 sweeper 到点激活/周期配额恢复），否则停权用户会被提前推上节点。
	now := time.Now()
	if u.Disabled || u.Expired || u.QuotaExhausted || u.NotYetEffective(now) {
		s.fanoutUserDiff(r.Context(), u.UUID, nodes, nil, removed)
	} else {
		s.fanoutUserDiff(r.Context(), u.UUID, nodes, added, removed)
	}
	addedChains, removedChains, err := s.st.SetUserChains(r.Context(), id, req.ChainIDs)
	if err != nil {
		o.Fail(err)
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.reconcileAssignmentEndpoints(r.Context(), addedChains, removedChains)
	o.Report("db", 100, "分配已保存")
	if s.subscriptions != nil {
		o.WatchUsers([]int64{id})
		s.subscriptions.EnqueueUsers([]int64{id}, s.panelBase(r))
		o.Report("regenerate", 0, "等待订阅重生成")
	}
	if len(added) > 0 || len(removed) > 0 {
		s.audit(r, "user.nodes_updated", nil, nil, map[string]any{
			"user":     map[string]any{"id": u.ID, "name": u.Name},
			"node_ids": map[string]any{"before": beforeNodeIDs, "after": req.NodeIDs},
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"node_ids": req.NodeIDs, "chain_ids": req.ChainIDs})
}

type userExternalSubscriptionInput struct {
	SubscriptionID int64  `json:"subscription_id"`
	Mode           string `json:"mode"`
}

// validExtSubMode 判断外部订阅引入模式。
func validExtSubMode(mode string) bool {
	return mode == store.ExtSubModeStack || mode == store.ExtSubModeMerge || mode == store.ExtSubModeNodes
}

// validateExternalSubscriptions 校验外部订阅列表（去重、模式、存在性）。
func (s *Server) validateExternalSubscriptions(ctx context.Context, items []userExternalSubscriptionInput) ([]store.UserExternalSubscription, error) {
	seen := map[int64]bool{}
	out := make([]store.UserExternalSubscription, 0, len(items))
	for _, item := range items {
		if !validExtSubMode(item.Mode) {
			return nil, errors.New("mode 必须是 stack/merge/nodes")
		}
		if item.SubscriptionID <= 0 || seen[item.SubscriptionID] {
			return nil, errors.New("外部订阅重复或 id 非法")
		}
		seen[item.SubscriptionID] = true
		if _, err := s.st.ExternalSubscriptionByID(ctx, item.SubscriptionID); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				return nil, fmt.Errorf("外部订阅 %d 不存在: %w", item.SubscriptionID, store.ErrNotFound)
			}
			return nil, err
		}
		out = append(out, store.UserExternalSubscription{SubscriptionID: item.SubscriptionID, Mode: item.Mode})
	}
	return out, nil
}

func (s *Server) handleSetUserExternalSubscriptions(w http.ResponseWriter, r *http.Request) {
	var req struct {
		UserID int64                           `json:"user_id"`
		Items  []userExternalSubscriptionInput `json:"items"`
	}
	if err := readJSON(r, &req); err != nil {
		writeProtocolError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	id := req.UserID
	if id <= 0 {
		writeError(w, http.StatusBadRequest, "用户 id 非法")
		return
	}
	if _, err := s.st.UserByID(r.Context(), id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "用户不存在")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	items, err := s.validateExternalSubscriptions(r.Context(), req.Items)
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, store.ErrNotFound) {
			status = http.StatusNotFound
		}
		writeError(w, status, err.Error())
		return
	}
	for i := range items {
		items[i].UserID = id
	}
	o := s.observeStart(r, "user.set_external_subscriptions", "更新用户外部订阅", userPublishObserveStages)
	defer o.CloseIfPending()
	if err := s.st.SetUserExternalSubscriptions(r.Context(), id, items); err != nil {
		o.Fail(err)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	o.Report("db", 100, "外部订阅已保存")
	if s.subscriptions != nil {
		o.WatchUsers([]int64{id})
		s.subscriptions.EnqueueUsers([]int64{id}, s.panelBase(r))
		o.Report("regenerate", 0, "等待订阅重生成")
	}
	s.audit(r, "user.external_subscriptions_updated", nil, nil, map[string]any{
		"user_id": id, "items": req.Items,
	})
	writeJSON(w, http.StatusOK, map[string]any{"items": req.Items})
}

func (s *Server) reconcileAssignmentEndpoints(ctx context.Context, groups ...[]store.UserChainAssignment) {
	endpointIDs := map[int64]bool{}
	for _, id := range s.st.SharedEndpointIDsForAssignments(groups...) {
		endpointIDs[id] = true
	}
	// 端到端 hy2 链（P4）：业务用户在出口共享监听上，分配变更须 reconcile 出口端点。
	for _, group := range groups {
		for _, a := range group {
			if chain, err := s.st.ChainByID(ctx, a.ChainID); err == nil &&
				chain.ServiceEndpointID != 0 && chain.EndpointID == 0 {
				endpointIDs[chain.ServiceEndpointID] = true
			}
		}
	}
	for endpointID := range endpointIDs {
		if err := s.disp.ReconcileSharedEndpoint(ctx, endpointID); err != nil {
			log.Printf("panel: reconcile shared endpoint %d: %v", endpointID, err)
		}
	}
}

// reconcileUserEndpoints reconcile 用户直连分配与其所在用户分组派生的共享端点。
// 分组成员的生效链路来自分组派生（直接分配被遮蔽），仅按直接分配反查会漏掉分组端点
// ——生命周期停权/激活（§9）与重新生效都必须覆盖两侧。
func (s *Server) reconcileUserEndpoints(ctx context.Context, userID int64, assignments []store.UserChainAssignment) {
	s.reconcileAssignmentEndpoints(ctx, assignments, nil)
	groupIDs, err := s.st.UserGroupIDsForUser(ctx, userID)
	if err != nil {
		return
	}
	for _, groupID := range groupIDs {
		g, err := s.st.UserGroupByID(ctx, groupID)
		if err != nil {
			continue
		}
		s.triggerGroupChange(ctx, nil, s.endpointIDsForLinkGroups(ctx, g.LinkGroupIDs))
	}
}

// fanoutUserDiff 按分配差量扇出：新增节点 → add_user（仅含这些节点），
// 移除节点 → remove_user（仅含这些节点）。
func (s *Server) fanoutUserDiff(ctx context.Context, uuid string, nodes []store.Node, added, removed []int64) {
	inSet := func(id int64, set []int64) bool {
		for _, x := range set {
			if x == id {
				return true
			}
		}
		return false
	}
	var addNodes, removeNodes []store.Node
	for _, n := range nodes {
		if inSet(n.ID, added) {
			addNodes = append(addNodes, n)
		}
		if inSet(n.ID, removed) {
			removeNodes = append(removeNodes, n)
		}
	}
	// hy2 出口共享链（P4）：出口监听由共享端点承载，add/remove_user 不扇出到这些节点
	//（其用户由 reconcileAssignmentEndpoints 的出口端点 reconcile 承载）。
	// 只查实际扇出的差量节点，避免全量节点的 N+1 反查。
	exclude := map[int64]bool{}
	for _, n := range append(addNodes, removeNodes...) {
		if chain, err := s.st.ChainByServiceNode(ctx, n.ID); err == nil && chain != nil &&
			chain.ServiceEndpointID != 0 {
			exclude[n.ID] = true
		}
	}
	filter := func(in []store.Node) []store.Node {
		out := in[:0]
		for _, n := range in {
			if !exclude[n.ID] {
				out = append(out, n)
			}
		}
		return out
	}
	addNodes, removeNodes = filter(addNodes), filter(removeNodes)
	for srvID, params := range nodeParamsByServer(addNodes) {
		if _, err := s.disp.Enqueue(ctx, srvID, shared.TypeAddUser,
			shared.AddUserPayload{UUID: uuid, Nodes: params}); err != nil {
			log.Printf("panel: fanout add_user user=%s server=%d: %v", uuid, srvID, err)
		}
	}
	for srvID, params := range nodeParamsByServer(removeNodes) {
		if _, err := s.disp.Enqueue(ctx, srvID, shared.TypeRemoveUser,
			shared.RemoveUserPayload{UUID: uuid, Nodes: params}); err != nil {
			log.Printf("panel: fanout remove_user user=%s server=%d: %v", uuid, srvID, err)
		}
	}
}

// handleDeleteUser 处理 DELETE /api/users/{id}：扇出 remove_user 后删除（§8）。
func (s *Server) handleDeleteUser(w http.ResponseWriter, r *http.Request) {
	var req struct {
		UserID int64 `json:"user_id"`
	}
	if err := readJSON(r, &req); err != nil {
		writeProtocolError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	id := req.UserID
	if id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid user id")
		return
	}
	u, err := s.st.UserByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "用户不存在")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	assignments, _ := s.st.UserChainAssignments(r.Context(), id)
	s.fanoutRemoveUser(r, u.UUID)
	if err := s.st.DeleteUser(r.Context(), id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.reconcileAssignmentEndpoints(r.Context(), assignments, nil)
	// 删除后对象不存在，审计行存 name 快照留痕（§log）。
	s.audit(r, "user.delete", nil, nil, map[string]any{"user_id": u.ID, "name": u.Name})
	writeJSON(w, http.StatusOK, nil)
}

// nodeParamsByServer 按服务器分组节点的用户条目参数（dokodemo 节点无用户概念，排除）。
func nodeParamsByServer(nodes []store.Node) map[int64]map[string]shared.UserNodeParams {
	byServer := map[int64]map[string]shared.UserNodeParams{}
	for _, n := range nodes {
		var vc shared.VirtualConfig
		if err := json.Unmarshal(n.ConfigTemplate, &vc); err != nil {
			continue // 模板损坏的节点跳过（异常留在 nodes 表）
		}
		if !shared.HasUserList(vc.Protocol) {
			continue
		}
		m := byServer[n.ServerID]
		if m == nil {
			m = map[string]shared.UserNodeParams{}
			byServer[n.ServerID] = m
		}
		m[shared.NodeTag(n.ID)] = shared.UserNodeParams{Protocol: vc.Protocol, Flow: vc.Flow, Method: vc.Method}
	}
	return byServer
}

// fanoutRemoveUser 扇出 remove_user（删除用户时调用）：向有用户节点的服务器下发，
// Agent 按 email/user 从各 inbound 幂等移除，未分配过该用户的节点为 no-op；
// 无用户节点的服务器跳过（Agent 要求 nodes 非空，空载荷只会回执错误）。
func (s *Server) fanoutRemoveUser(r *http.Request, uuid string) {
	nodes, err := s.st.ListNodes(r.Context())
	if err != nil {
		log.Printf("panel: fanout remove_user user=%s: list nodes: %v", uuid, err)
		return
	}
	byServer := nodeParamsByServer(nodes)
	servers, err := s.st.ListServers(r.Context())
	if err != nil {
		log.Printf("panel: fanout remove_user user=%s: list servers: %v", uuid, err)
		return
	}
	for _, srv := range servers {
		params := byServer[srv.ID]
		if len(params) == 0 {
			continue
		}
		payload := shared.RemoveUserPayload{UUID: uuid, Nodes: params}
		if _, err := s.disp.Enqueue(r.Context(), srv.ID, shared.TypeRemoveUser, payload); err != nil {
			log.Printf("panel: fanout remove_user user=%s server=%d: %v", uuid, srv.ID, err)
		}
	}
}

// handleUpdateUserSubSettings 处理 POST /api/user/sub-settings：更新用户订阅设置。
// 路由表单写入面板级/强制级槽位（forced 勾选决定级别），用户级字段只读保留；
// 可带 expires_at（RFC3339 或 null，§9）：省略 = 不变；null = 清除（长期）。
// 可带 effective_at（RFC3339 或 null，§9）：省略 = 不变；null = 立即生效；
// 推迟到未来即转为未生效态（已激活的会被扇出摘除，到点由 sweeper 重新激活）。
// 与 handleUpdateUser 一致，expires_at 不允许设为过去时间（400）。
func (s *Server) handleUpdateUserSubSettings(w http.ResponseWriter, r *http.Request) {
	var req struct {
		UserID          int64                     `json:"user_id"`
		TrafficLimit    int64                     `json:"traffic_limit"`     // 字节，0=不限
		TrafficResetDay int                       `json:"traffic_reset_day"` // 0=创建日，1-31
		SubTitle        string                    `json:"sub_title"`
		SubAnnouncement string                    `json:"sub_announcement"`
		PlanName        string                    `json:"plan_name"` // 套餐名（空=用全局）
		AppURL          string                    `json:"app_url"`   // 客户端跳转链接（空=用全局）
		Routing         *subscriptionProfileInput `json:"routing"`
		ExpiresAt       json.RawMessage           `json:"expires_at"`   // 省略 = 不变；null = 清除（长期）
		EffectiveAt     json.RawMessage           `json:"effective_at"` // 省略 = 不变；null = 立即生效
	}
	if err := readJSON(r, &req); err != nil {
		writeProtocolError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.UserID <= 0 {
		writeError(w, http.StatusBadRequest, "invalid user id")
		return
	}
	u, err := s.st.UserByID(r.Context(), req.UserID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "用户不存在")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if req.TrafficLimit < 0 {
		req.TrafficLimit = 0
	}
	if err := validateTrafficResetDay(req.TrafficResetDay); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	var routingProfile *store.SubscriptionProfile
	if req.Routing != nil {
		// 路由表单只写面板级/强制级槽位：勾选覆盖 → 强制级，不勾选 → 面板级；
		// 用户级字段与 user_configured 原样保留（仅订阅页可清除和修改）。
		current, err := s.st.UserSubscriptionProfile(r.Context(), req.UserID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		slots, err := routingSlotsFromInput(req.Routing)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if err := s.validateSubscriptionProfileTemplates(r.Context(), slots.templateCheckProfile()); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		profile := current
		applyRoutingSlots(&profile, slots)
		profile.GenerationStatus = store.SubscriptionGenerationPending
		routingProfile = &profile
	}

	// 有效期/生效时刻（§9）：改动可能复位 expired 停权标记或进出未生效态，
	// 有效停权态跃迁时扇出 add_user 恢复 / remove_user 摘除。配额停权（quota_exhausted）
	// 一般由周期重置或手动流量重置解除；试用用户从 expired 恢复时 SetUserExpiry
	// 会一并归档清零流量并解除配额停权（"重新生效 = 全新试用期"）。
	now := time.Now()
	expiryTouched := len(req.ExpiresAt) > 0
	effectiveTouched := len(req.EffectiveAt) > 0
	stoppedBefore := u.Disabled || u.Expired || u.QuotaExhausted || u.NotYetEffective(now)
	var effectiveAfter *time.Time
	if effectiveTouched {
		if string(req.EffectiveAt) != "null" {
			var raw string
			if err := json.Unmarshal(req.EffectiveAt, &raw); err != nil {
				writeError(w, http.StatusBadRequest, "effective_at 格式无效（需 RFC3339 或 null）")
				return
			}
			t, err := time.Parse(time.RFC3339, raw)
			if err != nil {
				writeError(w, http.StatusBadRequest, "effective_at 格式无效（需 RFC3339）")
				return
			}
			effectiveAfter = &t
		}
		if err := s.st.SetUserEffective(r.Context(), req.UserID, effectiveAfter, now); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	if expiryTouched {
		var expiresAt *time.Time
		if string(req.ExpiresAt) != "null" {
			var raw string
			if err := json.Unmarshal(req.ExpiresAt, &raw); err != nil {
				writeError(w, http.StatusBadRequest, "expires_at 格式无效（需 RFC3339 或 null）")
				return
			}
			t, err := time.Parse(time.RFC3339, raw)
			if err != nil {
				writeError(w, http.StatusBadRequest, "expires_at 格式无效（需 RFC3339）")
				return
			}
			expiresAt = &t
		}
		if expiresAt != nil && !expiresAt.After(now) {
			writeError(w, http.StatusBadRequest, "expires_at 不能是过去的时间")
			return
		}
		if err := s.st.SetUserExpiry(r.Context(), req.UserID, expiresAt, now); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	// 恢复判定必须基于写入后的状态：SetUserExpiry 在试用用户从 expired 恢复时
	// 会一并归档清零流量并解除 quota_exhausted；若继续用变更前的 u 计算，复合停权
	// （到期 + 配额）会被误判为仍停权，只发 remove_user 而漏发 add_user，
	// 客户端订阅恢复但节点凭证缺失。
	after, err := s.st.UserByID(r.Context(), req.UserID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	stoppedAfter := after.Disabled || after.Expired || after.QuotaExhausted || after.NotYetEffective(now)
	if stoppedBefore != stoppedAfter {
		nodes, err := s.st.ListNodes(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		assigned, err := s.st.UserNodeIDs(r.Context(), req.UserID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if stoppedAfter {
			s.fanoutUserDiff(r.Context(), u.UUID, nodes, nil, assigned)
			log.Printf("panel: user %d (%s) 已停权，扇出 remove_user (%d 节点)", req.UserID, u.Name, len(assigned))
		} else {
			s.fanoutUserDiff(r.Context(), u.UUID, nodes, assigned, nil)
			log.Printf("panel: user %d (%s) 已恢复，扇出 add_user (%d 节点)", req.UserID, u.Name, len(assigned))
		}
		if assignments, err := s.st.UserChainAssignments(r.Context(), req.UserID); err == nil {
			s.reconcileUserEndpoints(r.Context(), req.UserID, assignments)
		}
	}

	o := s.observeStart(r, "user.sub_settings", "保存订阅设置", userPublishObserveStages)
	defer o.CloseIfPending()
	if err := s.st.SetUserSubSettings(r.Context(), req.UserID, store.UserSubSettings{
		TrafficLimit: req.TrafficLimit, ResetDay: req.TrafficResetDay, SubTitle: req.SubTitle,
		SubAnnouncement: req.SubAnnouncement, PlanName: strings.TrimSpace(req.PlanName), AppURL: strings.TrimSpace(req.AppURL),
	}); err != nil {
		o.Fail(err)
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "用户不存在")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if routingProfile != nil {
		if err := s.st.SaveUserSubscriptionProfile(r.Context(), *routingProfile); err != nil {
			o.Fail(err)
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	o.Report("db", 100, "订阅设置已保存")
	if s.subscriptions != nil {
		o.WatchUsers([]int64{req.UserID})
		s.subscriptions.EnqueueUsers([]int64{req.UserID}, s.panelBase(r))
		o.Report("regenerate", 0, "等待订阅重生成")
	}
	updated, err := s.st.UserByID(r.Context(), req.UserID)
	if err != nil {
		o.Fail(err)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.audit(r, "user.sub_settings.updated", nil, nil, map[string]any{
		"user_id":           req.UserID,
		"traffic_limit":     req.TrafficLimit,
		"traffic_reset_day": req.TrafficResetDay,
		"expires_at":        logTime(updated.ExpiresAt),
		"effective_at":      logTime(updated.EffectiveAt),
		"is_trial":          updated.IsTrial,
	})
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleRegenerateUserSubscription 根据当前设置重新发布用户订阅，成功响应确认全部格式已落盘。
func (s *Server) handleRegenerateUserSubscription(w http.ResponseWriter, r *http.Request) {
	var req struct {
		UserID int64 `json:"user_id"`
	}
	if err := readJSON(r, &req); err != nil {
		writeProtocolError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.UserID <= 0 || s.subscriptions == nil {
		writeError(w, http.StatusBadRequest, "invalid user id or subscription service unavailable")
		return
	}
	if _, err := s.st.UserByID(r.Context(), req.UserID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "用户不存在")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	o := s.observeStart(r, "user.regenerate", "重新生成订阅", []progress.Stage{
		{Key: "regenerate", Label: "重新生成订阅文件"},
	})
	defer o.Close()
	o.Report("regenerate", 0, "正在重新生成订阅快照")
	if err := s.subscriptions.RegenerateUser(r.Context(), req.UserID, s.panelBase(r)); err != nil {
		o.Fail(err)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	o.Report("regenerate", 100, "全部订阅格式已发布")
	s.audit(r, "user.subscription.regenerated", nil, nil, map[string]any{
		"user_id": req.UserID,
	})
	writeJSON(w, http.StatusOK, nil)
}

// handleRegenerateAllUserSubscriptions 处理 POST /api/user/regenerate-all-subscriptions：
// 触发全部用户的订阅快照重发布。与批量模板指派同构：发布转异步，观察先登记全部
// 用户再入队（消除完成回调竞态），由 regenerator 完成回调推进百分比并收口；
// 单个用户发布失败计入观察警告，不阻断其余用户。
func (s *Server) handleRegenerateAllUserSubscriptions(w http.ResponseWriter, r *http.Request) {
	if s.subscriptions == nil {
		writeError(w, http.StatusServiceUnavailable, "subscription service unavailable")
		return
	}
	users, err := s.st.ListUsers(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	userIDs := make([]int64, 0, len(users))
	for _, u := range users {
		userIDs = append(userIDs, u.ID)
	}
	o := s.observeStart(r, "user.regenerate_all", "刷新全部订阅快照", userPublishObserveStages)
	defer o.CloseIfPending()
	o.Report("db", 100, fmt.Sprintf("已登记 %d 个用户", len(userIDs)))
	if len(userIDs) > 0 {
		o.WatchUsers(userIDs)
		s.subscriptions.EnqueueUsers(userIDs, s.panelBase(r))
		o.Report("regenerate", 0, "等待订阅重生成")
	}
	s.audit(r, "user.subscription.regenerated_all", nil, nil, map[string]any{
		"user_count": len(userIDs),
	})
	writeJSON(w, http.StatusOK, map[string]any{"user_count": len(userIDs)})
}

// handleResetUserSubscriptionToken 处理 POST /api/user/reset-subscription-token：
// 更换 sub_token 生成全新订阅地址（旧链接立即失效），并重新发布全部格式。
// UUID 不变，不触发节点扇出（§7/§8）。
func (s *Server) handleResetUserSubscriptionToken(w http.ResponseWriter, r *http.Request) {
	var req struct {
		UserID int64 `json:"user_id"`
	}
	if err := readJSON(r, &req); err != nil {
		writeProtocolError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.UserID <= 0 || s.subscriptions == nil {
		writeError(w, http.StatusBadRequest, "invalid user id or subscription service unavailable")
		return
	}
	u, err := s.st.UserByID(r.Context(), req.UserID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "user not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	var token string
	for i := 0; i < 5; i++ {
		candidate := randomHex(16)
		if _, err := s.st.UserBySubToken(r.Context(), candidate); err != nil {
			if !errors.Is(err, store.ErrNotFound) {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			token = candidate
			break
		}
	}
	if token == "" {
		writeError(w, http.StatusInternalServerError, "failed to generate unique subscription token")
		return
	}
	if err := s.st.SetUserSubToken(r.Context(), req.UserID, token); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "用户不存在")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	base := s.panelBase(r)
	if _, err := s.subscriptions.PublishUser(r.Context(), req.UserID, base); err != nil {
		// 发布失败时回滚 token：保持"轮换成功 ⇔ 发布成功"的不变量，
		// 失败则旧链接继续可用（避免新链接内容仍含旧 token 而旧链接已死）。
		log.Printf("panel: reset sub token user %d (%s): publish failed: %v; rolling back token", req.UserID, u.Name, err)
		if rbErr := s.st.SetUserSubToken(r.Context(), req.UserID, u.SubToken); rbErr != nil {
			log.Printf("panel: reset sub token user %d: rollback failed: %v", req.UserID, rbErr)
		}
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.audit(r, "user.subscription_token.reset", nil, nil, map[string]any{
		"user_id": req.UserID,
	})
	writeJSON(w, http.StatusOK, map[string]string{
		"sub_token":     token,
		"sub_url":       fmt.Sprintf("%s/sub/%s", base, token),
		"sub_links_url": fmt.Sprintf("%s/sub/%s?format=links", base, token),
	})
}

// handleResetUserTraffic 处理 POST /api/user/reset-traffic：手动重置当期流量（§9）。
// 归档并清零当期用量（traffic_history 留痕）；因配额停权（quota_exhausted）的用户立即
// 恢复——扇出 add_user + 重发布订阅（多重停权时仅解除配额侧，不扇出）；未停权用户仅
// 清零用量并重发布（subscription-userinfo 头更新）。试用/普通用户一致。
func (s *Server) handleResetUserTraffic(w http.ResponseWriter, r *http.Request) {
	var req struct {
		UserID int64 `json:"user_id"`
	}
	if err := readJSON(r, &req); err != nil {
		writeProtocolError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.UserID <= 0 {
		writeError(w, http.StatusBadRequest, "invalid user id")
		return
	}
	u, err := s.st.UserByID(r.Context(), req.UserID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "用户不存在")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	now := time.Now()
	stoppedBefore := u.Disabled || u.Expired || u.QuotaExhausted || u.NotYetEffective(now)
	o := s.observeStart(r, "user.reset_traffic", "重置流量", userPublishObserveStages)
	defer o.CloseIfPending()
	if err := s.st.ArchiveUserTraffic(r.Context(), u.UUID, now); err != nil {
		o.Fail(err)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if u.QuotaExhausted {
		if err := s.st.SetUserQuotaExhausted(r.Context(), req.UserID, false); err != nil {
			o.Fail(err)
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	o.Report("db", 100, "流量已重置")
	stoppedAfter := u.Disabled || u.Expired || u.NotYetEffective(now)
	if stoppedBefore != stoppedAfter {
		nodes, err := s.st.ListNodes(r.Context())
		if err != nil {
			o.Fail(err)
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		assigned, err := s.st.UserNodeIDs(r.Context(), req.UserID)
		if err != nil {
			o.Fail(err)
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		s.fanoutUserDiff(r.Context(), u.UUID, nodes, assigned, nil)
		log.Printf("panel: user %d (%s) 流量重置后恢复，扇出 add_user (%d 节点)", req.UserID, u.Name, len(assigned))
		if assignments, err := s.st.UserChainAssignments(r.Context(), req.UserID); err == nil {
			s.reconcileUserEndpoints(r.Context(), req.UserID, assignments)
		}
	}
	if s.subscriptions != nil {
		o.WatchUsers([]int64{req.UserID})
		s.subscriptions.EnqueueUsers([]int64{req.UserID}, s.panelBase(r))
		o.Report("regenerate", 0, "等待订阅重生成")
	}
	s.audit(r, "user.traffic.reset", nil, nil, map[string]any{
		"user_id": req.UserID, "quota_released": u.QuotaExhausted,
	})
	writeJSON(w, http.StatusOK, nil)
}

func (s *Server) handleUserSubscriptionPreview(w http.ResponseWriter, r *http.Request) {
	userID, err := strconv.ParseInt(r.URL.Query().Get("user_id"), 10, 64)
	if err != nil || userID <= 0 {
		writeError(w, http.StatusBadRequest, "invalid user_id")
		return
	}
	format := r.URL.Query().Get("format")
	if format != "clash" && format != "singbox" && format != "quanx" && format != "quanx-config" && format != "links" {
		writeError(w, http.StatusBadRequest, "invalid subscription format")
		return
	}
	// stage=pre 查看预编译中间态（占位符未解压），默认正式交付内容。
	stage := r.URL.Query().Get("stage")
	if stage != "" && stage != "final" && stage != "pre" {
		writeError(w, http.StatusBadRequest, "invalid stage")
		return
	}
	lookupFormat := format
	if stage == "pre" {
		lookupFormat = format + "-pre"
	}
	file, err := s.st.PublishedSubscriptionFile(r.Context(), userID, lookupFormat)
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, store.ErrNotFound) {
			status = http.StatusNotFound
		}
		writeError(w, status, err.Error())
		return
	}
	s.audit(r, "user.subscription.previewed", nil, nil, map[string]any{
		"user_id": userID, "format": format, "stage": stage, "revision": file.Revision,
	})
	writeJSON(w, http.StatusOK, map[string]any{
		"format": format, "stage": stage, "revision": file.Revision, "content_type": file.ContentType,
		"content": string(file.Content), "generated_at": file.GeneratedAt, "warnings": file.Warnings,
	})
}

// handleUserTrafficHistory 处理 GET /api/user/traffic-history?user_id=N。
func (s *Server) handleUserTrafficHistory(w http.ResponseWriter, r *http.Request) {
	idParam := r.URL.Query().Get("user_id")
	id, err := strconv.ParseInt(idParam, 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid user_id")
		return
	}
	u, err := s.st.UserByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "用户不存在")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	history, err := s.st.ListUserTrafficHistory(r.Context(), u.UUID, 12)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, history)
}

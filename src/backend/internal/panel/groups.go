package panel

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"lattix/backend/internal/progress"
	"lattix/backend/internal/store"
)

// observeStart 创建旁路观察并绑定到请求 context；返回观察句柄（可能为 nil）。
func (s *Server) observeStart(r *http.Request, kind, title string, stages []progress.Stage) *progress.Observation {
	o := s.observes.Start(kind, title, stages)
	if o != nil {
		*r = *r.WithContext(s.observes.Attach(r.Context(), o.ID))
	}
	return o
}

type linkGroupInput struct {
	ID                    int64                           `json:"id"`
	Name                  string                          `json:"name"`
	ChainIDs              []int64                         `json:"chain_ids"`
	ExternalSubscriptions []userExternalSubscriptionInput `json:"external_subscriptions"`
}

var linkGroupObserveStages = []progress.Stage{{Key: "db", Label: "校验并写入数据库"}}

// validateLinkGroup 校验链路分组输入：名称非空唯一、链路存在且带共享入口、外部订阅存在且模式合法。
// chain_ids 与 external_subscriptions 先去重（保留首次出现），重复 id 不再触发主键冲突 500。
func (s *Server) validateLinkGroup(ctx context.Context, input linkGroupInput, isCreate bool) (string, []int64, []store.LinkGroupExternalSubscription, error) {
	name := strings.TrimSpace(input.Name)
	if name == "" {
		return "", nil, nil, errors.New("分组名称不能为空")
	}
	excludeID := int64(0)
	if !isCreate {
		excludeID = input.ID
	}
	if taken, err := s.st.LinkGroupNameTaken(ctx, name, excludeID); err != nil {
		return "", nil, nil, err
	} else if taken {
		return "", nil, nil, errors.New("分组名称已存在")
	}
	chainIDs := dedupeInt64s(input.ChainIDs)
	if err := s.st.ValidateAssignableChains(ctx, chainIDs); err != nil {
		return "", nil, nil, err
	}
	seenSubs := map[int64]bool{}
	dedupedSubs := make([]userExternalSubscriptionInput, 0, len(input.ExternalSubscriptions))
	for _, item := range input.ExternalSubscriptions {
		if seenSubs[item.SubscriptionID] {
			continue
		}
		seenSubs[item.SubscriptionID] = true
		dedupedSubs = append(dedupedSubs, item)
	}
	items, err := s.validateExternalSubscriptions(ctx, dedupedSubs)
	if err != nil {
		return "", nil, nil, err
	}
	extSubs := make([]store.LinkGroupExternalSubscription, 0, len(items))
	for _, item := range items {
		extSubs = append(extSubs, store.LinkGroupExternalSubscription{
			SubscriptionID: item.SubscriptionID, Mode: item.Mode,
		})
	}
	return name, chainIDs, extSubs, nil
}

func (s *Server) handleListLinkGroups(w http.ResponseWriter, r *http.Request) {
	groups, err := s.st.ListLinkGroups(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// 空列表编码为 []（而不是 null）：前端直接把 data 当作数组使用。
	if groups == nil {
		groups = []store.LinkGroup{}
	}
	writeJSON(w, http.StatusOK, groups)
}

func (s *Server) handleCreateLinkGroup(w http.ResponseWriter, r *http.Request) {
	var req linkGroupInput
	if err := readJSON(r, &req); err != nil {
		writeProtocolError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	name, chainIDs, extSubs, err := s.validateLinkGroup(r.Context(), req, true)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	o := s.observeStart(r, "link_group.create", "创建链路分组", linkGroupObserveStages)
	defer o.Close()
	id, err := s.st.CreateLinkGroup(r.Context(), name, chainIDs, extSubs)
	if err != nil {
		o.Fail(err)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	o.Report("db", 100, "分组已保存")
	s.audit(r, "link_group.create", nil, nil, map[string]any{"id": id, "name": name})
	writeJSON(w, http.StatusOK, map[string]any{"id": id})
}

// handleUpdateLinkGroup 整体替换链路分组。
func (s *Server) handleUpdateLinkGroup(w http.ResponseWriter, r *http.Request) {
	var req linkGroupInput
	if err := readJSON(r, &req); err != nil {
		writeProtocolError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.ID <= 0 {
		writeError(w, http.StatusBadRequest, "id 必须为正整数")
		return
	}
	name, chainIDs, extSubs, err := s.validateLinkGroup(r.Context(), req, false)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	o := s.observeStart(r, "link_group.update", "更新链路分组", linkGroupObserveStages)
	defer o.Close()
	if err := s.st.UpdateLinkGroup(r.Context(), req.ID, name, chainIDs, extSubs); err != nil {
		o.Fail(err)
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "链路分组不存在")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	o.Report("db", 100, "分组已保存")
	s.audit(r, "link_group.update", nil, nil, map[string]any{"id": req.ID, "name": name})
	writeJSON(w, http.StatusOK, map[string]any{"id": req.ID})
}

func (s *Server) handleDeleteLinkGroup(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID int64 `json:"id"`
	}
	if err := readJSON(r, &req); err != nil {
		writeProtocolError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.ID <= 0 {
		writeError(w, http.StatusBadRequest, "id 必须为正整数")
		return
	}
	before, err := s.st.LinkGroupByID(r.Context(), req.ID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "链路分组不存在")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	o := s.observeStart(r, "link_group.delete", "删除链路分组", linkGroupObserveStages)
	defer o.Close()
	if err := s.st.DeleteLinkGroup(r.Context(), req.ID); err != nil {
		o.Fail(err)
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "链路分组不存在")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	o.Report("db", 100, "分组已删除")
	s.audit(r, "link_group.delete", nil, nil, map[string]any{"id": req.ID, "name": before.Name})
	writeJSON(w, http.StatusOK, nil)
}

// dedupeInt64s 去除重复 id，保留首次出现顺序。
func dedupeInt64s(ids []int64) []int64 {
	seen := map[int64]bool{}
	var out []int64
	for _, id := range ids {
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}

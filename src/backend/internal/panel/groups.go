package panel

import (
	"context"
	"errors"
	"log"
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
	ID                    int64                          `json:"id"`
	Name                  string                         `json:"name"`
	ChainIDs              []int64                        `json:"chain_ids"`
	ExternalSubscriptions []userExternalSubscriptionInput `json:"external_subscriptions"`
}

type userGroupInput struct {
	ID           int64   `json:"id"`
	Name         string  `json:"name"`
	UserIDs      []int64 `json:"user_ids"`
	LinkGroupIDs []int64 `json:"link_group_ids"`
}

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

// validateUserGroup 校验用户分组输入：名称非空唯一、用户与链路分组存在。
// user_ids 与 link_group_ids 先去重（保留首次出现），重复 id 不再触发主键冲突 500。
func (s *Server) validateUserGroup(ctx context.Context, input userGroupInput, isCreate bool) (string, []int64, []int64, error) {
	name := strings.TrimSpace(input.Name)
	if name == "" {
		return "", nil, nil, errors.New("分组名称不能为空")
	}
	excludeID := int64(0)
	if !isCreate {
		excludeID = input.ID
	}
	if taken, err := s.st.UserGroupNameTaken(ctx, name, excludeID); err != nil {
		return "", nil, nil, err
	} else if taken {
		return "", nil, nil, errors.New("分组名称已存在")
	}
	userIDs := dedupeInt64s(input.UserIDs)
	for _, userID := range userIDs {
		if _, err := s.st.UserByID(ctx, userID); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				return "", nil, nil, errors.New("用户不存在")
			}
			return "", nil, nil, err
		}
	}
	linkGroupIDs := dedupeInt64s(input.LinkGroupIDs)
	for _, linkGroupID := range linkGroupIDs {
		if _, err := s.st.LinkGroupByID(ctx, linkGroupID); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				return "", nil, nil, errors.New("链路分组不存在")
			}
			return "", nil, nil, err
		}
	}
	return name, userIDs, linkGroupIDs, nil
}

func (s *Server) handleListLinkGroups(w http.ResponseWriter, r *http.Request) {
	groups, err := s.st.ListLinkGroups(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
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
	o := s.observeStart(r, "link_group.create", "创建链路分组",
		[]progress.Stage{
			{Key: "db", Label: "校验并写入数据库"},
			{Key: "reconcile", Label: "同步共享端点"},
			{Key: "regenerate", Label: "重新生成订阅文件"},
		})
	defer o.Close()
	id, err := s.st.CreateLinkGroup(r.Context(), name, chainIDs, extSubs)
	if err != nil {
		o.Fail(err)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	o.Report("db", 100, "分组已保存")
	o.Report("reconcile", 100, "共享端点已同步")
	o.Report("regenerate", 0, "等待订阅重生成")
	s.audit(r, "link_group.create", nil, nil, map[string]any{"id": id, "name": name})
	writeJSON(w, http.StatusOK, map[string]any{"id": id})
}

// handleUpdateLinkGroup 整体替换链路分组；变更后重发布引用它的用户并 reconcile 涉及端点。
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
	before, err := s.st.LinkGroupByID(r.Context(), req.ID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "链路分组不存在")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	name, chainIDs, extSubs, err := s.validateLinkGroup(r.Context(), req, false)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	users, err := s.st.SubscriptionUserIDsForLinkGroup(r.Context(), req.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	affectedChains := mergeInt64s(before.ChainIDs, chainIDs)
	o := s.observeStart(r, "link_group.update", "更新链路分组",
		[]progress.Stage{
			{Key: "db", Label: "校验并写入数据库"},
			{Key: "reconcile", Label: "同步共享端点"},
			{Key: "regenerate", Label: "重新生成订阅文件"},
		})
	defer o.CloseIfPending()
	if err := s.st.UpdateLinkGroup(r.Context(), req.ID, name, chainIDs, extSubs); err != nil {
		o.Fail(err)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	o.Report("db", 100, "分组已保存")
	endpointIDs := s.endpointIDsForChains(r.Context(), affectedChains)
	o.Report("reconcile", 100, "共享端点已同步")
	s.triggerGroupChange(r.Context(), users, endpointIDs)
	o.WatchUsers(users)
	o.Report("regenerate", 0, "等待订阅重生成")
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
	users, err := s.st.SubscriptionUserIDsForLinkGroup(r.Context(), req.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	o := s.observeStart(r, "link_group.delete", "删除链路分组",
		[]progress.Stage{
			{Key: "db", Label: "校验并写入数据库"},
			{Key: "reconcile", Label: "同步共享端点"},
			{Key: "regenerate", Label: "重新生成订阅文件"},
		})
	defer o.CloseIfPending()
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
	s.triggerGroupChange(r.Context(), users, s.endpointIDsForChains(r.Context(), before.ChainIDs))
	o.WatchUsers(users)
	o.Report("regenerate", 0, "等待订阅重生成")
	s.audit(r, "link_group.delete", nil, nil, map[string]any{"id": req.ID, "name": before.Name})
	writeJSON(w, http.StatusOK, nil)
}

func (s *Server) handleListUserGroups(w http.ResponseWriter, r *http.Request) {
	groups, err := s.st.ListUserGroups(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, groups)
}

func (s *Server) handleCreateUserGroup(w http.ResponseWriter, r *http.Request) {
	var req userGroupInput
	if err := readJSON(r, &req); err != nil {
		writeProtocolError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	name, userIDs, linkGroupIDs, err := s.validateUserGroup(r.Context(), req, true)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	o := s.observeStart(r, "user_group.create", "创建用户分组",
		[]progress.Stage{
			{Key: "db", Label: "校验并写入数据库"},
			{Key: "reconcile", Label: "同步共享端点"},
			{Key: "regenerate", Label: "重新生成订阅文件"},
		})
	defer o.CloseIfPending()
	id, err := s.st.CreateUserGroup(r.Context(), name, userIDs, linkGroupIDs)
	if err != nil {
		o.Fail(err)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	o.Report("db", 100, "分组已保存")
	// 新分组立即使成员进入分组模式：重发布成员 + reconcile 关联链路端点。
	s.triggerGroupChange(r.Context(), userIDs, s.endpointIDsForLinkGroups(r.Context(), linkGroupIDs))
	o.WatchUsers(userIDs)
	o.Report("regenerate", 0, "等待订阅重生成")
	s.audit(r, "user_group.create", nil, nil, map[string]any{"id": id, "name": name})
	writeJSON(w, http.StatusOK, map[string]any{"id": id})
}

// handleUpdateUserGroup 整体替换用户分组；变更后重发布新旧成员并 reconcile 新旧链路分组端点。
func (s *Server) handleUpdateUserGroup(w http.ResponseWriter, r *http.Request) {
	var req userGroupInput
	if err := readJSON(r, &req); err != nil {
		writeProtocolError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.ID <= 0 {
		writeError(w, http.StatusBadRequest, "id 必须为正整数")
		return
	}
	before, err := s.st.UserGroupByID(r.Context(), req.ID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "用户分组不存在")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	name, userIDs, linkGroupIDs, err := s.validateUserGroup(r.Context(), req, false)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	allUsers := mergeInt64s(before.UserIDs, userIDs)
	allLinkGroups := mergeInt64s(before.LinkGroupIDs, linkGroupIDs)
	o := s.observeStart(r, "user_group.update", "更新用户分组",
		[]progress.Stage{
			{Key: "db", Label: "校验并写入数据库"},
			{Key: "reconcile", Label: "同步共享端点"},
			{Key: "regenerate", Label: "重新生成订阅文件"},
		})
	defer o.CloseIfPending()
	if err := s.st.UpdateUserGroup(r.Context(), req.ID, name, userIDs, linkGroupIDs); err != nil {
		o.Fail(err)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	o.Report("db", 100, "分组已保存")
	s.triggerGroupChange(r.Context(), allUsers, s.endpointIDsForLinkGroups(r.Context(), allLinkGroups))
	o.WatchUsers(allUsers)
	o.Report("regenerate", 0, "等待订阅重生成")
	s.audit(r, "user_group.update", nil, nil, map[string]any{"id": req.ID, "name": name})
	writeJSON(w, http.StatusOK, map[string]any{"id": req.ID})
}

func (s *Server) handleDeleteUserGroup(w http.ResponseWriter, r *http.Request) {
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
	before, err := s.st.UserGroupByID(r.Context(), req.ID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "用户分组不存在")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	users, err := s.st.UsersForUserGroup(r.Context(), req.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	o := s.observeStart(r, "user_group.delete", "删除用户分组",
		[]progress.Stage{
			{Key: "db", Label: "校验并写入数据库"},
			{Key: "reconcile", Label: "同步共享端点"},
			{Key: "regenerate", Label: "重新生成订阅文件"},
		})
	defer o.CloseIfPending()
	if err := s.st.DeleteUserGroup(r.Context(), req.ID); err != nil {
		o.Fail(err)
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "用户分组不存在")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	o.Report("db", 100, "分组已删除")
	// 成员恢复直接分配，需重发布 + 端点 reconcile。
	s.triggerGroupChange(r.Context(), users, s.endpointIDsForLinkGroups(r.Context(), before.LinkGroupIDs))
	o.WatchUsers(users)
	o.Report("regenerate", 0, "等待订阅重生成")
	s.audit(r, "user_group.delete", nil, nil, map[string]any{"id": req.ID, "name": before.Name})
	writeJSON(w, http.StatusOK, nil)
}

// triggerGroupChange 分组变更后的触发动作：受影响用户异步重发布 + 受影响共享端点 reconcile。
func (s *Server) triggerGroupChange(ctx context.Context, userIDs, endpointIDs []int64) {
	if s.subscriptions != nil {
		s.subscriptions.EnqueueUsers(userIDs, "")
	}
	for _, endpointID := range endpointIDs {
		if s.disp == nil {
			continue
		}
		if err := s.disp.ReconcileSharedEndpoint(ctx, endpointID); err != nil {
			log.Printf("panel: reconcile shared endpoint %d: %v", endpointID, err)
		}
	}
}

// endpointIDsForChains 返回链路 ID 列表对应的共享入口 ID（已发布链路上查 chains.endpoint_id）。
func (s *Server) endpointIDsForChains(ctx context.Context, chainIDs []int64) []int64 {
	if len(chainIDs) == 0 {
		return nil
	}
	chains, err := s.st.ListChains(ctx)
	if err != nil {
		return nil
	}
	byID := make(map[int64]int64, len(chains))
	for _, c := range chains {
		byID[c.ID] = c.EndpointID
	}
	seen := map[int64]bool{}
	var out []int64
	for _, chainID := range chainIDs {
		if ep := byID[chainID]; ep != 0 && !seen[ep] {
			seen[ep] = true
			out = append(out, ep)
		}
	}
	return out
}

// endpointIDsForLinkGroups 返回链路分组列表引用的全部链路共享入口 ID。
func (s *Server) endpointIDsForLinkGroups(ctx context.Context, linkGroupIDs []int64) []int64 {
	var chainIDs []int64
	for _, lgID := range linkGroupIDs {
		lg, err := s.st.LinkGroupByID(ctx, lgID)
		if err != nil {
			continue
		}
		chainIDs = append(chainIDs, lg.ChainIDs...)
	}
	return s.endpointIDsForChains(ctx, chainIDs)
}

// mergeInt64s 返回 a ∪ b（去重，保持首次出现顺序）。
func mergeInt64s(a, b []int64) []int64 {
	return dedupeInt64s(append(append([]int64{}, a...), b...))
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

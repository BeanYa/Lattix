package panel

import (
	"context"
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"

	"lattix/backend/internal/progress"
	"lattix/backend/internal/store"
)

type externalSubscriptionInput struct {
	ID                  int64  `json:"id"`
	Name                string `json:"name"`
	URL                 string `json:"url"`
	UserAgent           string `json:"user_agent"`
	SkipCertVerify      bool   `json:"skip_cert_verify"`
	AutoUpdate          bool   `json:"auto_update"`
	UpdateIntervalHours int    `json:"update_interval_hours"`
}

func (s *Server) handleListExternalSubscriptions(w http.ResponseWriter, r *http.Request) {
	subs, err := s.st.ListExternalSubscriptions(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]store.ExternalSubscription, 0, len(subs))
	out = append(out, subs...)
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleCreateExternalSubscription(w http.ResponseWriter, r *http.Request) {
	var req externalSubscriptionInput
	if err := readJSON(r, &req); err != nil {
		writeProtocolError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.UpdateIntervalHours == 0 {
		req.UpdateIntervalHours = 24
	}
	o := s.observeStart(r, "external_subscription.create", "创建外部订阅",
		[]progress.Stage{
			{Key: "fetch", Label: "拉取远程订阅"},
			{Key: "parse", Label: "解析节点"},
			{Key: "db", Label: "写入数据库"},
		})
	defer o.Close()
	sub, err := s.extSubs.Create(r.Context(), req.Name, strings.TrimSpace(req.URL),
		req.UserAgent, req.SkipCertVerify, req.AutoUpdate, req.UpdateIntervalHours)
	if err != nil {
		o.Fail(err)
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	o.Report("fetch", 100, "拉取完成")
	o.Report("parse", 100, "解析完成")
	o.Report("db", 100, "已写入数据库")
	s.audit(r, "external_subscription.created", nil, nil, map[string]any{
		"id": sub.ID, "name": sub.Name, "node_count": sub.NodeCount,
	})
	writeJSON(w, http.StatusOK, sub)
}

func (s *Server) handleUpdateExternalSubscription(w http.ResponseWriter, r *http.Request) {
	var req externalSubscriptionInput
	if err := readJSON(r, &req); err != nil {
		writeProtocolError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.ID <= 0 {
		writeError(w, http.StatusBadRequest, "id 必须为正整数")
		return
	}
	if req.UpdateIntervalHours == 0 {
		req.UpdateIntervalHours = 24
	}
	o := s.observeStart(r, "external_subscription.update", "更新外部订阅",
		[]progress.Stage{
			{Key: "db", Label: "写入数据库"},
		})
	defer o.Close()
	sub, err := s.extSubs.Update(r.Context(), req.ID, req.Name, strings.TrimSpace(req.URL),
		req.UserAgent, req.SkipCertVerify, req.AutoUpdate, req.UpdateIntervalHours)
	if err != nil {
		o.Fail(err)
		status := http.StatusBadRequest
		if errors.Is(err, store.ErrNotFound) {
			status = http.StatusNotFound
		}
		writeError(w, status, err.Error())
		return
	}
	o.Report("db", 100, "订阅已更新")
	s.audit(r, "external_subscription.updated", nil, nil, map[string]any{
		"id": sub.ID, "name": sub.Name,
	})
	writeJSON(w, http.StatusOK, sub)
}

func (s *Server) handleDeleteExternalSubscription(w http.ResponseWriter, r *http.Request) {
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
	o := s.observeStart(r, "external_subscription.delete", "删除外部订阅",
		[]progress.Stage{
			{Key: "db", Label: "写入数据库"},
			{Key: "regenerate", Label: "重发布关联用户"},
		})
	defer o.CloseIfPending()
	// 删除前收集受影响用户（删除后按订阅 ID 查不到行）；收集失败记日志，
	// 不丢弃已收集到的直接分配用户。
	affected, err := s.st.UsersByExternalSubscriptionID(r.Context(), req.ID)
	if err != nil {
		log.Printf("panel: collect direct users of external subscription %d: %v", req.ID, err)
	}
	groupAffected, err := s.st.UsersByExternalSubscriptionThroughGroups(r.Context(), req.ID)
	if err != nil {
		log.Printf("panel: collect group users of external subscription %d: %v", req.ID, err)
	}
	if err := s.st.DeleteExternalSubscription(r.Context(), req.ID); err != nil {
		o.Fail(err)
		status := http.StatusBadRequest
		if errors.Is(err, store.ErrNotFound) {
			status = http.StatusNotFound
		}
		writeError(w, status, err.Error())
		return
	}
	o.Report("db", 100, "订阅已删除")
	seen := map[int64]bool{}
	var ids []int64
	for _, id := range append(affected, groupAffected...) {
		if id > 0 && !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	if s.subscriptions != nil {
		s.subscriptions.EnqueueUsers(ids, "")
		o.WatchUsers(ids)
	}
	o.Report("regenerate", 0, "等待订阅重生成")
	s.audit(r, "external_subscription.deleted", nil, nil, map[string]any{"id": req.ID})
	writeJSON(w, http.StatusOK, nil)
}

func (s *Server) handleSyncExternalSubscription(w http.ResponseWriter, r *http.Request) {
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
	o := s.observeStart(r, "external_subscription.sync", "同步外部订阅",
		[]progress.Stage{
			{Key: "fetch", Label: "拉取远程订阅"},
			{Key: "parse", Label: "解析节点"},
			{Key: "db", Label: "写入数据库"},
			{Key: "regenerate", Label: "重发布关联用户"},
		})
	defer o.CloseIfPending()
	sub, err := s.extSubs.Sync(r.Context(), req.ID)
	if err != nil {
		o.Fail(err)
		status := http.StatusBadGateway
		if errors.Is(err, store.ErrNotFound) {
			status = http.StatusNotFound
		}
		writeError(w, status, err.Error())
		return
	}
	o.Report("fetch", 100, "拉取完成")
	o.Report("parse", 100, "解析完成")
	o.Report("db", 100, "已写入数据库")
	s.audit(r, "external_subscription.synced", nil, nil, map[string]any{
		"id": sub.ID, "node_count": sub.NodeCount,
	})
	s.republishExternalSubUsers(r.Context(), []int64{sub.ID})
	o.Report("regenerate", 100, "已触发重发布")
	writeJSON(w, http.StatusOK, sub)
}

func (s *Server) handleListExternalChains(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.URL.Query().Get("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "id 必须为正整数")
		return
	}
	chains, err := s.st.ListExternalChains(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]store.ExternalChain, 0, len(chains))
	out = append(out, chains...)
	writeJSON(w, http.StatusOK, out)
}

// republishExternalSubUsers 将关联了给定外部订阅的用户加入订阅重生成队列。
// 直接分配与分组派生分别收集、合并去重；单边查询失败记日志并保留另一侧已收集的用户。
func (s *Server) republishExternalSubUsers(ctx context.Context, subscriptionIDs []int64) {
	if s.subscriptions == nil || len(subscriptionIDs) == 0 {
		return
	}
	seen := map[int64]bool{}
	var userIDs []int64
	for _, subID := range subscriptionIDs {
		direct, err := s.st.UsersByExternalSubscriptionID(ctx, subID)
		if err != nil {
			log.Printf("panel: collect direct users of external subscription %d: %v", subID, err)
		}
		groupUsers, err := s.st.UsersByExternalSubscriptionThroughGroups(ctx, subID)
		if err != nil {
			log.Printf("panel: collect group users of external subscription %d: %v", subID, err)
		}
		for _, userID := range append(append([]int64{}, direct...), groupUsers...) {
			if !seen[userID] {
				seen[userID] = true
				userIDs = append(userIDs, userID)
			}
		}
	}
	s.subscriptions.EnqueueUsers(userIDs, "")
}

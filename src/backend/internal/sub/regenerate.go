package sub

import (
	"context"
	"errors"
	"log"
	"net/http/httptest"
	"strings"
	"time"

	"lattix/backend/internal/store"
)

const regenerationDebounce = 150 * time.Millisecond

type pendingPublication struct {
	version uint64
	baseURL string
}

func (s *Server) StartRegenerator(ctx context.Context) {
	s.startOnce.Do(func() {
		if err := s.st.RecoverSubscriptionPublicationJobs(ctx); err != nil {
			log.Printf("subscription: recover publication jobs: %v", err)
		}
		s.wakeRegenerator()
		s.queueWG.Add(1)
		go func() {
			defer s.queueWG.Done()
			s.runRegenerator(ctx)
		}()
	})
}

func (s *Server) WaitRegenerator(ctx context.Context) error {
	done := make(chan struct{})
	go func() {
		s.queueWG.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Server) EnqueueUsers(userIDs []int64, baseURL string) {
	if len(userIDs) == 0 {
		return
	}
	s.rememberBaseURL(baseURL)
	s.queueMu.Lock()
	for _, userID := range userIDs {
		if userID > 0 {
			s.queueSeq++
			s.pending[userID] = pendingPublication{version: s.queueSeq, baseURL: strings.TrimRight(baseURL, "/")}
			s.queued[userID] = strings.TrimRight(baseURL, "/")
		}
	}
	s.queueMu.Unlock()
	s.wakeRegenerator()
}

func (s *Server) wakeRegenerator() {
	select {
	case s.queueWake <- struct{}{}:
	default:
	}
}

// RegenerateUser 用于管理员显式刷新：返回时全部格式已经原子发布，失败直接返回。
func (s *Server) RegenerateUser(ctx context.Context, userID int64, baseURL string) error {
	s.EnqueueUsers([]int64{userID}, baseURL)
	return s.publishPendingUser(ctx, userID)
}

func (s *Server) EnqueueUsersForNode(ctx context.Context, nodeID int64) error {
	chain, err := s.st.ChainByServiceNode(ctx, nodeID)
	if err != nil {
		return err
	}
	if chain != nil {
		return s.EnqueueCatalogUsers(ctx, "")
	}
	ids, err := s.st.SubscriptionUserIDsForNode(ctx, nodeID)
	if err != nil {
		return err
	}
	s.EnqueueUsers(ids, "")
	return nil
}

func (s *Server) EnqueueUsersForChain(ctx context.Context, _ int64) error {
	return s.EnqueueCatalogUsers(ctx, "")
}

func (s *Server) EnqueueUsersForEndpoint(ctx context.Context, _ int64) error {
	return s.EnqueueCatalogUsers(ctx, "")
}

func (s *Server) EnqueueUsersForServer(ctx context.Context, _ int64, baseURL string) error {
	return s.EnqueueCatalogUsers(ctx, baseURL)
}

func (s *Server) EnqueueCatalogUsers(ctx context.Context, baseURL string) error {
	ids, err := s.st.CatalogUserIDs(ctx)
	if err != nil {
		return err
	}
	s.EnqueueUsers(ids, baseURL)
	return nil
}

func (s *Server) runRegenerator(ctx context.Context) {
	retry := time.NewTicker(15 * time.Second)
	defer retry.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.queueWake:
		case <-retry.C:
		}
		timer := time.NewTimer(regenerationDebounce)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		for ctx.Err() == nil {
			processed := s.processQueuedSubscriptionJobs(ctx)
			for userID := range s.takeQueued() {
				publishCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
				err := s.publishPendingUser(publishCtx, userID)
				cancel()
				if err != nil {
					log.Printf("subscription: regenerate user %d: %v", userID, err)
				}
			}
			if !processed {
				break
			}
		}
	}
}

func (s *Server) processQueuedSubscriptionJobs(ctx context.Context) bool {
	users, err := s.st.QueuedSubscriptionPublicationUserIDs(ctx)
	if err != nil {
		log.Printf("subscription: list queued publication users: %v", err)
		return false
	}
	processed := false
	for _, userID := range users {
		if ctx.Err() != nil {
			break
		}
		lock := s.publishLock(userID)
		lock.Lock()
		err := s.processNextSubscriptionJobLocked(ctx, userID)
		lock.Unlock()
		switch {
		case err == nil:
			processed = true
		case !errors.Is(err, store.ErrNotFound):
			log.Printf("subscription: process queued publication for user %d: %v", userID, err)
		}
	}
	return processed
}

func (s *Server) processNextSubscriptionJobLocked(ctx context.Context, userID int64) error {
	job, err := s.st.ClaimSubscriptionPublicationJob(ctx, userID)
	if err != nil {
		return err
	}
	pending := s.pendingPublication(userID)
	baseURL := job.BaseURL
	if baseURL == "" {
		baseURL = s.currentBaseURL()
	}
	pendingBaseURL := pending.baseURL
	if pendingBaseURL == "" {
		pendingBaseURL = s.currentBaseURL()
	}
	publishCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	publishCtx, err = store.WithSubscriptionPublicationJob(publishCtx, job)
	if err == nil {
		_, err = s.publishUserLocked(publishCtx, userID, baseURL)
	}
	if err == nil {
		if pending.version != 0 && strings.TrimRight(baseURL, "/") == pendingBaseURL &&
			!s.clearPendingPublication(userID, pending.version) && s.observer != nil {
			s.observer.NotifyUserPublished(userID, nil)
		}
		return nil
	}
	if ctx.Err() != nil {
		finishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if err := s.st.RequeueSubscriptionPublicationJob(finishCtx, job); err != nil {
			log.Printf("subscription: requeue interrupted publication %s: %v", job.ID, err)
		}
		s.wakeRegenerator()
		return ctx.Err()
	}
	log.Printf("subscription: publish %s job %s for user %d: %v", job.Kind, job.ID, userID, err)
	return s.st.FailSubscriptionPublicationJob(ctx, job, "订阅生成失败，请重试")
}

func (s *Server) pendingPublication(userID int64) pendingPublication {
	s.queueMu.Lock()
	defer s.queueMu.Unlock()
	return s.pending[userID]
}

// clearPendingPublication reports whether a newer background request remains.
func (s *Server) clearPendingPublication(userID int64, version uint64) bool {
	s.queueMu.Lock()
	defer s.queueMu.Unlock()
	if s.pending[userID].version == version {
		delete(s.pending, userID)
		delete(s.queued, userID)
	}
	return s.pending[userID].version != 0
}

// publishPendingUser 使后台任务与订阅请求共用同一用户发布锁；读取待更新版本必须
// 在锁内完成，防止后台已经发布后请求重复编译。发布中再次入队则继续生成新版本。
func (s *Server) publishPendingUser(ctx context.Context, userID int64) error {
	if s.pendingPublication(userID).version == 0 {
		return nil
	}
	lock := s.publishLock(userID)
	lock.Lock()
	defer lock.Unlock()
	for {
		job := s.pendingPublication(userID)
		if job.version == 0 {
			return nil
		}
		baseURL := job.baseURL
		if baseURL == "" {
			baseURL = s.currentBaseURL()
		}
		_, err := s.publishUserLocked(ctx, userID, baseURL)
		if err != nil {
			if s.observer != nil {
				s.observer.NotifyUserPublished(userID, err)
			}
			return err
		}
		if !s.clearPendingPublication(userID, job.version) {
			if s.observer != nil {
				s.observer.NotifyUserPublished(userID, nil)
			}
			return nil
		}
	}
}

func (s *Server) takeQueued() map[int64]string {
	s.queueMu.Lock()
	defer s.queueMu.Unlock()
	queued := s.queued
	s.queued = make(map[int64]string)
	return queued
}

func (s *Server) rememberBaseURL(baseURL string) {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		return
	}
	s.baseMu.Lock()
	s.lastBase = baseURL
	s.baseMu.Unlock()
}

func (s *Server) currentBaseURL() string {
	s.baseMu.RLock()
	baseURL := s.lastBase
	s.baseMu.RUnlock()
	if baseURL != "" {
		return baseURL
	}
	return strings.TrimRight(s.base(httptest.NewRequest("GET", "http://localhost/", nil)), "/")
}

package sub

import (
	"context"
	"log"
	"net/http/httptest"
	"strings"
	"time"
)

const regenerationDebounce = 150 * time.Millisecond

type pendingPublication struct {
	version uint64
	baseURL string
}

func (s *Server) StartRegenerator(ctx context.Context) {
	s.startOnce.Do(func() {
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
	ids, err := s.st.SubscriptionUserIDsForNode(ctx, nodeID)
	if err != nil {
		return err
	}
	s.EnqueueUsers(ids, "")
	return nil
}

func (s *Server) EnqueueUsersForChain(ctx context.Context, chainID int64) error {
	ids, err := s.st.SubscriptionUserIDsForChain(ctx, chainID)
	if err != nil {
		return err
	}
	s.EnqueueUsers(ids, "")
	return nil
}

func (s *Server) EnqueueUsersForEndpoint(ctx context.Context, endpointID int64) error {
	ids, err := s.st.SubscriptionUserIDsForEndpoint(ctx, endpointID)
	if err != nil {
		return err
	}
	s.EnqueueUsers(ids, "")
	return nil
}

func (s *Server) EnqueueUsersForServer(ctx context.Context, serverID int64, baseURL string) error {
	ids, err := s.st.SubscriptionUserIDsForServer(ctx, serverID)
	if err != nil {
		return err
	}
	s.EnqueueUsers(ids, baseURL)
	return nil
}

func (s *Server) runRegenerator(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.queueWake:
		}
		timer := time.NewTimer(regenerationDebounce)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		for userID := range s.takeQueued() {
			publishCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
			err := s.publishPendingUser(publishCtx, userID)
			cancel()
			if err != nil {
				log.Printf("subscription: regenerate user %d: %v", userID, err)
			}
		}
	}
}

// publishPendingUser 使后台任务与订阅请求共用同一用户发布锁；读取待更新版本必须
// 在锁内完成，防止后台已经发布后请求重复编译。发布中再次入队则继续生成新版本。
func (s *Server) publishPendingUser(ctx context.Context, userID int64) error {
	s.queueMu.Lock()
	pending := s.pending[userID].version != 0
	s.queueMu.Unlock()
	if !pending {
		return nil
	}
	lock := s.publishLock(userID)
	lock.Lock()
	defer lock.Unlock()
	for {
		s.queueMu.Lock()
		job := s.pending[userID]
		s.queueMu.Unlock()
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
		s.queueMu.Lock()
		if s.pending[userID].version == job.version {
			delete(s.pending, userID)
			delete(s.queued, userID)
		}
		pending := s.pending[userID].version != 0
		s.queueMu.Unlock()
		if !pending {
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

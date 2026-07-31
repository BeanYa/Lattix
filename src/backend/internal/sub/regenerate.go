package sub

import (
	"context"
	"log"
	"net/http/httptest"
	"strings"
	"time"
)

const regenerationDebounce = 150 * time.Millisecond

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
			s.queued[userID] = strings.TrimRight(baseURL, "/")
		}
	}
	s.queueMu.Unlock()
	select {
	case s.queueWake <- struct{}{}:
	default:
	}
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
		for userID, baseURL := range s.takeQueued() {
			if baseURL == "" {
				baseURL = s.currentBaseURL()
			}
			publishCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
			_, err := s.PublishUser(publishCtx, userID, baseURL)
			cancel()
			if err != nil {
				log.Printf("subscription: regenerate user %d: %v", userID, err)
			}
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

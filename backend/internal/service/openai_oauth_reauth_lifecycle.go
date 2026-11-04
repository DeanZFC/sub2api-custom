package service

import (
	"context"
	"time"
)

// Start resumes durable queued Excel authorizations after a process restart and
// prepares enabled accounts whose independent grant is still missing.
func (s *OpenAIOAuthReauthService) Start() {
	if s == nil {
		return
	}
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	if s.lifecycleCancel != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.lifecycleCancel = cancel
	s.lifecycleDone = make(chan struct{})
	go func() {
		defer close(s.lifecycleDone)
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			runCtx, runCancel := context.WithTimeout(ctx, 20*time.Second)
			_ = s.QueueMissingExcelAuthorizations(runCtx)
			if repo, ok := s.repo.(interface {
				HasActiveExcelTasks(context.Context) (bool, error)
			}); ok {
				if pending, err := repo.HasActiveExcelTasks(runCtx); err == nil && pending {
					s.EnsureWorker()
				}
			}
			runCancel()
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}

func (s *OpenAIOAuthReauthService) Stop() {
	if s == nil {
		return
	}
	s.lifecycleMu.Lock()
	cancel, done := s.lifecycleCancel, s.lifecycleDone
	s.lifecycleMu.Unlock()
	if cancel != nil {
		cancel()
		<-done
	}
	s.stopWorker()
	s.mihomoLeaseMu.Lock()
	leases := s.mihomoLeases
	s.mihomoLeases = make(map[int64]func())
	s.mihomoLeaseMu.Unlock()
	for _, release := range leases {
		release()
	}
}

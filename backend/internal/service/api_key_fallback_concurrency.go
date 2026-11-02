package service

import (
	"context"
	"errors"
	"sync"
	"time"
)

var ErrFallbackGroupConcurrency = errors.New("fallback group concurrency limit reached")

type UserGroupConcurrencyMover interface {
	UserGroupConcurrencyCache
	MoveUserGroupSlot(context.Context, int64, int64, int64, int, int, string) (bool, error)
}

// Keep the global user token throughout the transfer. The Redis operation
// checks target capacity and moves only the group dimension atomically.
func (s *ConcurrencyService) bindFallbackConcurrency(ctx context.Context, result *AcquireResult, userID, groupID int64, userMax int, requestID string) *AcquireResult {
	r := fallbackRouting(ctx)
	if r == nil || result == nil || !result.Acquired {
		return result
	}
	var mu sync.Mutex
	currentGroup := groupID
	released := false
	originalRelease := result.ReleaseFunc
	moved := false
	r.concurrencyAdmission = func(moveCtx context.Context, target *Group) error {
		mu.Lock()
		defer mu.Unlock()
		if released {
			return context.Canceled
		}
		mover, ok := s.cache.(UserGroupConcurrencyMover)
		if !ok {
			return errors.New("group concurrency transfer unavailable")
		}
		acquired, err := mover.MoveUserGroupSlot(moveCtx, userID, currentGroup, target.ID, userMax, target.UserConcurrencyLimit, requestID)
		if err != nil {
			return err
		}
		if !acquired {
			return ErrFallbackGroupConcurrency
		}
		currentGroup, moved = target.ID, true
		return nil
	}
	result.ReleaseFunc = func() {
		mu.Lock()
		defer mu.Unlock()
		if released {
			return
		}
		released = true
		if !moved {
			if originalRelease != nil {
				originalRelease()
			}
			return
		}
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if cache, ok := s.cache.(UserGroupConcurrencyCache); ok {
			_ = cache.ReleaseUserGroupSlot(cleanup, userID, currentGroup, requestID)
		}
	}
	return result
}

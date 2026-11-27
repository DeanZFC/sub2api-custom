package service

import (
	"context"
	"errors"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
)

type intelligenceEligibleRepository interface {
	IntelligenceTargetEligible(context.Context, int64, int64, int64, string) (bool, error)
}
type intelligenceArtworkQueue interface {
	DueIntelligenceArtwork(context.Context, time.Time) ([]int64, error)
}

func (s *ScheduledTestRunnerService) retryDueIntelligenceArt(ctx context.Context, now time.Time) {
	if s.scheduledSvc == nil {
		return
	}
	repo, ok := s.scheduledSvc.resultRepo.(intelligenceArtworkQueue)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, scheduledTestPersistenceTimeout)
	defer cancel()
	ids, err := repo.DueIntelligenceArtwork(ctx, now)
	if err != nil {
		logger.LegacyPrintf("service.scheduled_test_runner", "list due intelligence artwork: %v", err)
		return
	}
	ctx = context.WithValue(ctx, scheduledTestAutomaticRunKey{}, true)
	for _, id := range ids {
		if ctx.Err() != nil {
			return
		}
		if _, err = s.scheduledSvc.RetryResult(ctx, id); err != nil && !errors.Is(err, ErrScheduledTestAccountRunning) && !errors.Is(err, ErrScheduledTestResultNotFailed) {
			logger.LegacyPrintf("service.scheduled_test_runner", "retry intelligence artwork %d: %v", id, err)
		}
	}
}

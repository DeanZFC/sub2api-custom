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
	DeferIntelligenceArtwork(context.Context, int64, time.Time, time.Time) error
}

var errIntelligenceTargetUnavailable = errors.New("account no longer supports the selected text model")

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
		_, err = s.scheduledSvc.RetryResult(ctx, id)
		if errors.Is(err, errIntelligenceTargetUnavailable) {
			// Eligibility can change again. Keep the original evidence and let
			// other due rows advance while this account has no matching text model.
			err = repo.DeferIntelligenceArtwork(ctx, id, now, now.Add(15*time.Minute))
		}
		if err != nil && !errors.Is(err, ErrScheduledTestAccountRunning) && !errors.Is(err, ErrScheduledTestResultNotFailed) {
			logger.LegacyPrintf("service.scheduled_test_runner", "retry intelligence artwork %d: %v", id, err)
		}
	}
}

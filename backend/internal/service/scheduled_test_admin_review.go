package service

import (
	"context"
	"time"
)

// Administrator decisions are distinct from counted public ballots.
type ScheduledTestAdminReview struct {
	Result        *ScheduledTestResult `json:"result"`
	Generation    int64                `json:"generation"`
	Verdict       string               `json:"verdict"`
	AdminVerdict  string               `json:"admin_verdict"`
	AdminUserID   *int64               `json:"admin_user_id,omitempty"`
	DecidedAt     *time.Time           `json:"decided_at,omitempty"`
	AccountPaused bool                 `json:"account_paused"`
}

type ScheduledTestAdminReviewRepository interface {
	ListAdminReviews(context.Context) ([]*ScheduledTestAdminReview, error)
	DecideTestResult(context.Context, int64, int64, int64, string) error
}

func (s *ScheduledTestService) ListAdminReviews(ctx context.Context) ([]*ScheduledTestAdminReview, error) {
	repo, ok := s.resultRepo.(ScheduledTestAdminReviewRepository)
	if !ok {
		return nil, ErrScheduledTestVoteUnavailable
	}
	rows, err := repo.ListAdminReviews(ctx)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		if row != nil {
			normalizeStoredTestResults([]*ScheduledTestResult{row.Result})
		}
	}
	return rows, nil
}

func (s *ScheduledTestService) DecideTestResult(ctx context.Context, adminID, resultID, generation int64, verdict string) error {
	if verdict != "pass" && verdict != "fail" {
		return ErrScheduledTestVoteInvalid
	}
	if adminID <= 0 || resultID <= 0 || generation <= 0 {
		return ErrScheduledTestVoteUnavailable
	}
	repo, ok := s.resultRepo.(ScheduledTestAdminReviewRepository)
	if !ok {
		return ErrScheduledTestVoteUnavailable
	}
	return repo.DecideTestResult(ctx, adminID, resultID, generation, verdict)
}

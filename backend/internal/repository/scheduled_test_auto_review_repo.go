package repository

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

var _ service.ScheduledTestAutoReviewRepository = (*scheduledTestResultRepository)(nil)

func (r *scheduledTestResultRepository) LoadReviewSettings(ctx context.Context) (*service.ScheduledTestReviewSettings, error) {
	config, err := qualityConfig(ctx, r.db, false)
	return config.PelicanReview, err
}

func (r *scheduledTestResultRepository) ReserveReview(ctx context.Context, expected service.ScheduledTestReviewSettings) (bool, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	config, err := qualityConfig(ctx, tx, true)
	if err != nil {
		return false, err
	}
	current := config.PelicanReview
	if current == nil || !current.Enabled || current.Validate() != nil || current.JudgeAccountID != expected.JudgeAccountID || strings.TrimSpace(current.JudgeModel) != strings.TrimSpace(expected.JudgeModel) || current.DailyRequests != expected.DailyRequests {
		return false, nil
	}
	var requests int64
	err = tx.QueryRowContext(ctx, `INSERT INTO scheduled_test_auto_review_days(day,requests)
 VALUES((clock_timestamp() AT TIME ZONE 'UTC')::date,1)
 ON CONFLICT(day) DO UPDATE SET requests=scheduled_test_auto_review_days.requests+1
 WHERE scheduled_test_auto_review_days.requests < $1 RETURNING requests`, current.DailyRequests).Scan(&requests)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, tx.Commit()
}

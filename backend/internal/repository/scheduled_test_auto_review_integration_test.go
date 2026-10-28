//go:build integration

package repository

import (
	"sync"
	"sync/atomic"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/Wei-Shaw/sub2api/migrations"
	"github.com/stretchr/testify/require"
)

func TestAutoReviewBudgetIntegration(t *testing.T) {
	ctx, db, operations, _ := qualityOperationsDB(t)
	raw, err := migrations.FS.ReadFile("314_scheduled_test_auto_review.sql")
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, string(raw))
	require.NoError(t, err)
	repo := &scheduledTestResultRepository{db: db}
	snapshot, err := operations.OperationsSnapshot(ctx)
	require.NoError(t, err)
	c := snapshot.Config
	settings := service.ScheduledTestReviewSettings{Enabled: true, JudgeAccountID: 63, JudgeModel: "vision", DailyRequests: 7}
	c.PelicanReview = &settings
	require.NoError(t, operations.SaveOperationsConfig(ctx, &c))
	loaded, err := repo.LoadReviewSettings(ctx)
	require.NoError(t, err)
	require.Equal(t, &settings, loaded)
	var wg sync.WaitGroup
	var admitted atomic.Int64
	for i := 0; i < 30; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ok, err := repo.ReserveReview(ctx, settings)
			require.NoError(t, err)
			if ok {
				admitted.Add(1)
			}
		}()
	}
	wg.Wait()
	require.EqualValues(t, 7, admitted.Load(), "atomic limit includes every confirmation call")
	_, err = db.ExecContext(ctx, `UPDATE scheduled_test_auto_review_days SET day=day-1`)
	require.NoError(t, err)
	ok, err := repo.ReserveReview(ctx, settings)
	require.NoError(t, err)
	require.True(t, ok, "UTC next day has its own allowance")
	c.PelicanReview = &service.ScheduledTestReviewSettings{Enabled: true, JudgeAccountID: 63, JudgeModel: "new-vision", DailyRequests: 7}
	require.NoError(t, operations.SaveOperationsConfig(ctx, &c))
	ok, err = repo.ReserveReview(ctx, settings)
	require.NoError(t, err)
	require.False(t, ok, "do not admit stale judge configuration")
	c.PelicanReview.Enabled = false
	require.NoError(t, operations.SaveOperationsConfig(ctx, &c))
	ok, err = repo.ReserveReview(ctx, *c.PelicanReview)
	require.NoError(t, err)
	require.False(t, ok)
}

func visualPassRateFixture(t *testing.T) *passRateFixture {
	f := newPassRateFixture(t, 2)
	f.exec(`CREATE TABLE quality_operations_config(id INT PRIMARY KEY,version BIGINT,config JSONB);
 INSERT INTO quality_operations_config VALUES(1,1,'{"pelican_review":{"enabled":true,"judge_account_id":63,"judge_model":"vision","daily_requests":100}}');`)
	f.exec(`UPDATE scheduled_test_definitions SET output_kind='html' WHERE id=$1`, f.definitionID)
	rule := &f.plan.Protection.Rules[0]
	rule.ExpectedAnswer = ""
	rule.AutoReview = &service.ScheduledTestAutoReviewConfig{Enabled: true}
	rule.Vote = &service.ScheduledTestVoteConfig{Enabled: true, PassAtLeast: 1}
	var err error
	f.plan, err = f.plans.Update(f.ctx, f.plan)
	require.NoError(t, err)
	return f
}
func visualResult(f *passRateFixture, verdict string) *service.ScheduledTestResult {
	row := f.begin()
	row.OutputKind = "html"
	row.OutputHTML = "<svg/>"
	settings, err := f.repo.LoadReviewSettings(f.ctx)
	require.NoError(f.t, err)
	row.ExecutionInfo = &service.ScheduledTestExecutionInfo{AutoReview: &service.ScheduledTestAutoReviewSummary{Verdict: verdict, RubricVersion: service.ScheduledTestReviewRubricVersion, JudgeAccountID: settings.JudgeAccountID, JudgeModel: settings.JudgeModel}}
	f.complete(row, "success", verdict)
	return row
}
func TestAutoReviewPassRateIntegration(t *testing.T) {
	t.Run("automatic verdict and administrator correction", func(t *testing.T) {
		f := visualPassRateFixture(t)
		visualResult(f, "pass")
		row := visualResult(f, "fail")
		require.True(t, f.summary("model-a").Blocked)
		// A successful duplicate completion cannot wash out the same-round failure.
		row.ExecutionInfo.AutoReview.Verdict = "pass"
		f.complete(row, "success", "pass")
		require.True(t, f.summary("model-a").Blocked)
		var generation int64
		require.NoError(t, f.db.QueryRowContext(f.ctx, `SELECT generation FROM scheduled_test_protection_states WHERE result_id=$1`, row.ID).Scan(&generation))
		require.NoError(t, f.repo.DecideTestResult(f.ctx, 1, row.ID, generation, "pass"))
		s := f.summary("model-a")
		require.Equal(t, 2, s.Samples)
		require.Equal(t, 2, s.Passed)
		require.False(t, s.Blocked)
		require.NoError(t, f.repo.DecideTestResult(f.ctx, 1, row.ID, generation, "fail"))
		s = f.summary("model-a")
		require.Equal(t, 1, s.Passed)
		require.True(t, s.Blocked)
	})
	t.Run("pending excluded and changed judge starts new window", func(t *testing.T) {
		f := visualPassRateFixture(t)
		visualResult(f, "pending")
		require.Zero(t, f.evidenceCount())
		visualResult(f, "pass")
		old := f.summary("model-a")
		f.exec(`UPDATE quality_operations_config SET config=jsonb_set(config,'{pelican_review,judge_model}','"new-vision"')`)
		visualResult(f, "pass")
		current := f.summary("model-a")
		require.NotEqual(t, old.RuleVersion, current.RuleVersion)
		require.Equal(t, 1, current.Samples)
	})
	t.Run("judge config changing before completion makes old result pending", func(t *testing.T) {
		f := visualPassRateFixture(t)
		row := f.begin()
		row.Status = "success"
		row.OutputKind = "html"
		row.OutputHTML = "<svg/>"
		row.ExecutionInfo = &service.ScheduledTestExecutionInfo{AutoReview: &service.ScheduledTestAutoReviewSummary{
			Verdict: "fail", JudgeAccountID: 63, JudgeModel: "vision", RubricVersion: service.ScheduledTestReviewRubricVersion,
		}}
		require.NoError(t, f.repo.Update(f.ctx, row))
		f.exec(`UPDATE quality_operations_config SET config=jsonb_set(config,'{pelican_review,judge_model}','"replacement-vision"')`)
		require.NoError(t, f.repo.CompleteProtection(f.ctx, row, "fail", "old judge failure"))
		var verdict string
		require.NoError(t, f.db.QueryRowContext(f.ctx, `SELECT verdict FROM scheduled_test_protection_states WHERE result_id=$1`, row.ID).Scan(&verdict))
		require.Equal(t, "pending", verdict)
		require.Zero(t, f.evidenceCount())
	})
}

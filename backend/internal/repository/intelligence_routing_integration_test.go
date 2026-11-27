//go:build integration

package repository

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func testIntelligenceRouting(t *testing.T, ctx context.Context, db *sql.DB, plans *scheduledTestPlanRepository, results *scheduledTestResultRepository, pelican int64) {
	p := genericRoutingPlan(pelican, 8, 9)
	p.ModelID, p.ModelIDs = "model-a", []string{"model-a", "model-b"}
	p.ReasoningEffort = "high"
	p.Protection = service.ScheduledTestProtectionConfig{Intelligence: &service.IntelligencePolicy{AutoIsolate: true, RecoveryPasses: 2, CooldownMinutes: 30}}
	plan, err := plans.Create(ctx, p)
	require.NoError(t, err)
	start := time.Now().UTC().Add(-4 * time.Hour).Truncate(time.Microsecond)
	finish := func(minutes int, model, verdict, art string, checks ...service.IntelligenceCheck) *service.ScheduledTestResult {
		t.Helper()
		inputs := genericRoundInputs(plan, []int64{62}, start.Add(time.Duration(minutes)*time.Minute))
		inputs[0].ModelID = model
		inputs[0].OutputKind = "html"
		rows, err := results.BeginRun(ctx, plan, uuid.NewString(), inputs)
		require.NoError(t, err)
		r := rows[0]
		r.Status = "success"
		if art == "default" {
			r.OutputHTML = "<svg/>"
		}
		if art == "missing" {
			r.Status = "failed"
		}
		r.ExecutionInfo = &service.ScheduledTestExecutionInfo{Intelligence: &service.IntelligenceEvidence{PolicyVersion: plan.Protection.Intelligence.Version, AutoVerdict: verdict, Artwork: art, Checks: checks}}
		require.NoError(t, results.Update(ctx, r))
		return r
	}
	check := func(status string, heldA, heldB bool) {
		t.Helper()
		var got string
		var a, b bool
		require.NoError(t, db.QueryRowContext(ctx, `SELECT status,COALESCE((extra->'active_test_model_holds')?'model-a',false),COALESCE((extra->'active_test_model_holds')?'model-b',false) FROM accounts WHERE id=62`).Scan(&got, &a, &b))
		require.Equal(t, status, got)
		require.Equal(t, heldA, a)
		require.Equal(t, heldB, b)
	}
	due := func(minutes int, models ...string) {
		t.Helper()
		targets, err := plans.DueIntelligenceTargets(ctx, plan.ID, start.Add(time.Duration(minutes)*time.Minute))
		require.NoError(t, err)
		var got []string
		for _, s := range targets {
			require.EqualValues(t, 62, s.AccountID)
			got = append(got, s.Model)
		}
		require.ElementsMatch(t, models, got)
	}
	// Under the previous rule a wrong answer with all other checks passing was
	// suspect. New rounds must not reinterpret this stored observation.
	legacyWrong := finish(0, "model-a", "suspect", "skipped",
		service.IntelligenceCheck{Key: "fingerprint", Status: "pass"},
		service.IntelligenceCheck{Key: "codex_state", Status: "pass"},
		service.IntelligenceCheck{Key: "candy", Status: "fail"},
		service.IntelligenceCheck{Key: "http_429", Status: "pass"})
	check("active", false, false)
	due(4)
	due(5, "model-a")
	finish(5, "model-a", "confirmed", "skipped")
	check("active", true, false)
	due(34)
	due(35, "model-a")
	finish(6, "model-a", "suspect", "skipped")
	due(34)
	due(35, "model-a")
	good := finish(6, "model-b", "pass", "default")
	require.NoError(t, results.ReviewIntelligenceArt(ctx, good.ID, 1, "fail", "作品不符合题意"))
	check("quality_paused", true, true)
	require.NoError(t, results.ReviewIntelligenceArt(ctx, good.ID, 1, "pass", "复核修正"))
	check("active", true, false)
	finish(34, "model-a", "pass", "default")
	due(35, "model-a")
	// A completed four-check pass already satisfies the two-check rule.
	// Confirm at the normal interval and keep both original check lists.
	twoPass := []service.IntelligenceCheck{{Key: "candy", Status: "pass"}, {Key: "http_429", Status: "pass"}}
	fourPass := []service.IntelligenceCheck{{Key: "fingerprint", Status: "pass"}, {Key: "codex_state", Status: "pass"}, twoPass[0], twoPass[1]}
	first := finish(35, "model-a", "pass", "default", fourPass...)
	require.NoError(t, results.Update(ctx, first))
	check("active", true, false)
	due(39)
	due(40, "model-a")
	missing := finish(40, "model-a", "pass", "missing", twoPass...)
	check("active", true, false)
	overview, e := results.IntelligenceOverview(ctx, plan.ID)
	require.NoError(t, e)
	require.True(t, overview.Results[0].RetryableArtwork)
	require.Equal(t, twoPass, overview.Results[0].Evidence.Checks)
	require.Equal(t, fourPass, overview.Results[1].Evidence.Checks)
	stored, err := results.GetByID(ctx, legacyWrong.ID)
	require.NoError(t, err)
	require.Equal(t, "suspect", stored.ExecutionInfo.Intelligence.AutoVerdict)
	missing.Status = "success"
	missing.OutputHTML = "<svg/>"
	missing.ExecutionInfo.Intelligence.Artwork = "default"
	require.NoError(t, results.Update(ctx, missing))
	check("active", false, false)
	finish(45, "model-a", "confirmed", "skipped")
	old := plan.Protection.Intelligence.Version
	plan.Protection.Intelligence.AutoIsolate = false
	plan, err = plans.Update(ctx, plan)
	require.NoError(t, err)
	require.NotEqual(t, old, plan.Protection.Intelligence.Version)
	require.NoError(t, results.ReviewIntelligenceArt(ctx, good.ID, 1, "fail", "历史结论"))
	finish(45, "model-b", "suspect", "skipped")
	check("active", true, false)
	due(50, "model-b")
	finish(46, "model-b", "confirmed", "skipped")
	check("active", true, false)
	finish(75, "model-a", "pass", "default")
	finish(80, "model-a", "pass", "default")
	check("active", false, false)
	plan.Protection.Intelligence.AutoIsolate = true
	plan, err = plans.Update(ctx, plan)
	require.NoError(t, err)
	finish(85, "model-a", "confirmed", "skipped")
	due(115, "model-a")
	plan.ExcludedAccountIDs = []int64{62}
	plan, err = plans.Update(ctx, plan)
	require.NoError(t, err)
	check("active", false, false)
	due(115)
	plan.ExcludedAccountIDs = nil
	plan, err = plans.Update(ctx, plan)
	require.NoError(t, err)
	check("active", false, false)
	finish(90, "model-b", "confirmed", "skipped")
	require.NoError(t, results.ReleaseIntelligenceHold(ctx, plan.ID, 62, "model-b", 1))
	check("active", false, false)
	// A crashed worker after one recovery pass leaves a pending target. A new
	// lease must fence that result and break the streak, even across restart.
	finish(100, "model-a", "confirmed", "skipped")
	finish(130, "model-a", "pass", "default")
	pending := genericRoundInputs(plan, []int64{62}, start.Add(135*time.Minute))
	pending[0].ModelID = "model-a"
	pending[0].ExecutionInfo = &service.ScheduledTestExecutionInfo{Intelligence: &service.IntelligenceEvidence{PolicyVersion: plan.Protection.Intelligence.Version, AutoVerdict: "unknown", Artwork: "skipped"}}
	_, err = results.BeginRun(ctx, plan, uuid.NewString(), pending)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `INSERT INTO scheduled_test_execution_leases(plan_id,scope,owner,expires_at) VALUES($1,'plan','lost',now()-interval '1 minute')`, plan.ID)
	require.NoError(t, err)
	claimed, err := plans.ClaimExecution(ctx, plan, "plan", "replacement", false)
	require.NoError(t, err)
	require.True(t, claimed)
	var passes int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT passes FROM intelligence_model_states WHERE plan_id=$1 AND model_key='model-a' AND account_id=62 AND blocked`, plan.ID).Scan(&passes))
	require.Zero(t, passes)
	require.NoError(t, plans.ReleaseExecution(ctx, plan.ID, "plan", "replacement"))
	var reviews, bindings int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT count(*) FROM intelligence_artwork_review_log`).Scan(&reviews))
	require.Equal(t, 3, reviews)
	require.NoError(t, db.QueryRowContext(ctx, `SELECT count(*) FROM account_groups WHERE account_id=62`).Scan(&bindings))
	require.Equal(t, 2, bindings)
}

func TestIntelligenceDefaultBudgetAndRecoveryReserve(t *testing.T) {
	ctx := context.Background()
	db := integrationDB
	var account int64
	require.NoError(t, db.QueryRowContext(ctx, `INSERT INTO accounts(name,platform,type) VALUES($1,'openai','oauth') RETURNING id`, uuid.NewString()).Scan(&account))
	t.Cleanup(func() {
		_, err := db.Exec(`DELETE FROM quality_probe_attempts WHERE account_id=$1`, account)
		require.NoError(t, err)
		_, err = db.Exec(`DELETE FROM quality_budget_days WHERE account_id=$1`, account)
		require.NoError(t, err)
		_, err = db.Exec(`DELETE FROM accounts WHERE id=$1`, account)
		require.NoError(t, err)
	})
	_, err := db.ExecContext(ctx, `INSERT INTO quality_probe_attempts(account_id,day,reserved_tokens,intelligence_probe) SELECT $1,(now() AT TIME ZONE 'UTC')::date,1,true FROM generate_series(1,180)`, account)
	require.NoError(t, err)
	repo := qualityOperationsRepository{db: db}
	var deferred *service.QualityDeferredError
	_, err = repo.ReserveQualityProbe(ctx, account, 1, true, "intelligence:round")
	require.ErrorAs(t, err, &deferred)
	_, err = repo.ReserveQualityProbe(ctx, account, 1, true, "intelligence-retest:recover")
	require.NoError(t, err)
	_, err = repo.ReserveQualityProbe(ctx, account, 1, true, "other")
	require.NoError(t, err)
}

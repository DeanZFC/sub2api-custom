//go:build integration

package repository

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func testScheduledTestTriggerEvidence(t *testing.T, ctx context.Context, db *sql.DB, plans *scheduledTestPlanRepository, repo *scheduledTestResultRepository, definitionID int64, combined bool) {
	plan := genericRoutingPlan(definitionID, 8)
	rule := service.ScheduledTestProtectionRule{TestDefinitionID: definitionID, Thresholds: []service.ScheduledTestThreshold{{Metric: "output_numeric", Operator: "lt", Value: 30}},
		OnFail: &service.ScheduledTestOutcomeAction{Scheduling: "pause"}, OnPass: &service.ScheduledTestOutcomeAction{Scheduling: "resume"}}
	plan.Protection.Rules = []service.ScheduledTestProtectionRule{rule}
	if combined {
		plan.Protection.Mode = "combined"
		plan.Protection.Combinations = []service.ScheduledTestCombinationRule{
			{ID: "fail", Name: "Below threshold", Condition: combinedTestLeaf(definitionID, "fail"), Action: service.ScheduledTestOutcomeAction{Scheduling: "pause"}},
			{ID: "pass", Name: "Recovered", Condition: combinedTestLeaf(definitionID, "pass"), Action: service.ScheduledTestOutcomeAction{Scheduling: "resume"}},
		}
	}
	plan, err := plans.Create(ctx, plan)
	require.NoError(t, err)
	started := time.Now().UTC().Truncate(time.Microsecond)
	round := func(runID string, at time.Time) *service.ScheduledTestResult {
		t.Helper()
		rows, err := repo.BeginRun(ctx, plan, runID, genericRoundInputs(plan, []int64{62}, at))
		require.NoError(t, err)
		require.NoError(t, repo.BeginProtectionRun(ctx, plan, at))
		require.NoError(t, repo.BeginProtection(ctx, rows[0], rule))
		return rows[0]
	}
	complete := func(row *service.ScheduledTestResult, verdict, response string) {
		t.Helper()
		row.Status, row.ResponseText = "success", response
		require.NoError(t, repo.Update(ctx, row))
		require.NoError(t, repo.CompleteProtection(ctx, row, verdict, "trigger reason"))
	}
	svc := service.NewScheduledTestService(nil, repo)
	trigger := func() *service.ScheduledTestProtectionTrigger {
		t.Helper()
		holds, err := svc.AccountProtectionDetails(ctx, 62)
		require.NoError(t, err)
		require.Len(t, holds, 1)
		require.NotNil(t, holds[0].TriggerSnapshot)
		return holds[0].TriggerSnapshot
	}
	first := round("evidence-first", started)
	complete(first, "fail", "29")
	event := trigger()
	require.Positive(t, event.ID)
	require.Equal(t, "automatic", event.Source)
	require.Equal(t, first.ID, event.ResultID)
	require.Equal(t, "evidence-first", event.RunID)
	require.Equal(t, "model", event.ModelID)
	require.Len(t, event.PolicyVersion, 64)
	require.Equal(t, "active", event.Before.Status)
	require.Equal(t, "quality_paused", event.After.Status)
	require.True(t, event.Before.Schedulable)
	require.Equal(t, float64(29), *event.Tests[0].Thresholds[0].Observed)
	require.EqualValues(t, 1, *event.Tests[0].Thresholds[0].Samples)
	if combined {
		require.Equal(t, "fail", event.Rules[0].ID)
	}
	_, err = db.ExecContext(ctx, `UPDATE scheduled_test_protection_events SET snapshot='{}' WHERE id=$1`, event.ID)
	require.ErrorContains(t, err, "immutable")
	second := round("evidence-second", started.Add(time.Minute))
	complete(second, "fail", "12")
	require.Equal(t, event, trigger(), "an ongoing hold retains its original measurements and policy")
	require.NoError(t, repo.Delete(ctx, first.ID))
	require.Equal(t, event, trigger(), "source retention must not destroy evidence")
	third := round("evidence-third", started.Add(2*time.Minute))
	complete(third, "pass", "50")
	holds, err := svc.AccountProtectionDetails(ctx, 62)
	require.NoError(t, err)
	require.Empty(t, holds)
	fourth := round("evidence-fourth", started.Add(3*time.Minute))
	complete(fourth, "fail", "8")
	renewed := trigger()
	require.Greater(t, renewed.ID, event.ID)
	require.Equal(t, float64(8), *renewed.Tests[0].Thresholds[0].Observed)
	require.NoError(t, plans.Delete(ctx, plan.ID))
	var historicalRun string
	require.NoError(t, db.QueryRowContext(ctx, `SELECT snapshot->>'run_id' FROM scheduled_test_protection_events WHERE id=$1`, event.ID).Scan(&historicalRun))
	require.Equal(t, "evidence-first", historicalRun)
}

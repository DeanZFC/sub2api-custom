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

func testScheduledTestLatestPublicRun(t *testing.T, ctx context.Context, db *sql.DB, plans service.ScheduledTestPlanRepository, results *scheduledTestResultRepository, candyID, htmlID int64) {
	t.Helper()
	_, err := db.ExecContext(ctx, `INSERT INTO accounts(id,name) VALUES (90,'Successful'),(91,'Failed'),(92,'Queued'),(93,'Removed');
INSERT INTO account_groups(account_id,group_id) VALUES (90,8),(91,8),(92,8),(93,8)`)
	require.NoError(t, err)
	groupID := int64(8)
	newPlan := func(name string) *service.ScheduledTestPlan {
		t.Helper()
		p, err := plans.Create(ctx, &service.ScheduledTestPlan{
			Name: name, GroupIDs: []int64{groupID}, GroupID: &groupID,
			TestDefinitionID: &candyID, TestDefinitionIDs: []int64{candyID, htmlID},
			TargetMode: "all_accounts", ModelID: name, CronExpression: "0 * * * *", MaxResults: 10,
		})
		require.NoError(t, err)
		return p
	}
	plan, independent := newPlan("Hourly latest run"), newPlan("Independent schedule")
	noon := time.Date(2026, time.September, 25, 12, 0, 0, 0, time.UTC)
	input := func(p *service.ScheduledTestPlan, accountID, definitionID int64, at time.Time) *service.ScheduledTestResult {
		return &service.ScheduledTestResult{
			PlanID: p.ID, AccountID: &accountID, GroupID: &groupID, TestDefinitionID: &definitionID,
			TargetMode: "all_accounts", ModelID: p.ModelID, Status: "pending", OutputKind: "text",
			StartedAt: at, FinishedAt: at,
		}
	}
	finish := func(row *service.ScheduledTestResult, status string) {
		t.Helper()
		row.Status = status
		require.NoError(t, results.Update(ctx, row))
	}
	var inputs []*service.ScheduledTestResult
	for _, accountID := range []int64{90, 91, 92, 93} {
		for _, definitionID := range []int64{candyID, htmlID} {
			inputs = append(inputs, input(plan, accountID, definitionID, noon))
		}
	}
	previous, err := results.BeginRun(ctx, plan, "noon", inputs)
	require.NoError(t, err)
	for _, row := range previous {
		finish(row, "success")
	}
	other, err := results.BeginRun(ctx, independent, "independent-noon", []*service.ScheduledTestResult{input(independent, 90, candyID, noon)})
	require.NoError(t, err)
	finish(other[0], "success")
	assertVisible := func(want ...int64) {
		t.Helper()
		for _, limit := range []int{1, 3} {
			rows, err := results.ListVisible(ctx, 100, limit)
			require.NoError(t, err)
			var got []int64
			for _, row := range rows {
				if row.PlanID == plan.ID || row.PlanID == independent.ID {
					got = append(got, row.ID)
					require.Contains(t, []string{"success", "passed"}, row.Status)
				}
			}
			require.ElementsMatch(t, want, got)
		}
	}
	var previousIDs []int64
	for _, row := range previous {
		previousIDs = append(previousIDs, row.ID)
	}
	assertVisible(append(previousIDs, other[0].ID)...)

	// A new snapshot replaces the entire previous round, including accounts
	// and test types that are no longer targeted. Completion times may differ.
	one := noon.Add(time.Hour)
	current, err := results.BeginRun(ctx, plan, "one-pm", []*service.ScheduledTestResult{
		input(plan, 90, candyID, one), input(plan, 90, htmlID, one.Add(time.Minute)),
		input(plan, 91, candyID, one), input(plan, 92, candyID, one),
	})
	require.NoError(t, err)
	assertVisible(other[0].ID)
	finish(current[0], "success")
	finish(current[1], "passed")
	finish(current[2], "failed")
	finish(current[3], "running")
	assertVisible(current[0].ID, current[1].ID, other[0].ID)

	// Failed and queued accounts still exist in the administrator's snapshot.
	admin, err := results.ListByPlanID(ctx, plan.ID, 1)
	require.NoError(t, err)
	require.Len(t, admin, 4)
	states := map[int64]string{}
	for _, row := range admin {
		states[row.ID] = row.Status
	}
	require.Equal(t, "failed", states[current[2].ID])
	require.Equal(t, "running", states[current[3].ID])
	for _, old := range previous {
		if *old.TestDefinitionID != candyID {
			continue
		}
		history, err := results.ListVisibleHistory(ctx, 100, old.ID, 0, 20)
		require.NoError(t, err)
		ids := make([]int64, 0, len(history))
		for _, row := range history {
			ids = append(ids, row.ID)
		}
		require.Contains(t, ids, old.ID, "old successes remain accessible through history")
	}

	// Retrying the current failed row does not reveal its previous success.
	current[2].StartedAt = one.Add(5 * time.Minute)
	finish(current[2], "running")
	assertVisible(current[0].ID, current[1].ID, other[0].ID)
	finish(current[2], "success")
	assertVisible(current[0].ID, current[1].ID, current[2].ID, other[0].ID)

	// Even a later manual update to an old run cannot replace the current run.
	previous[2].StartedAt = one.Add(10 * time.Minute)
	finish(previous[2], "success")
	assertVisible(current[0].ID, current[1].ID, current[2].ID, other[0].ID)

	// When the whole latest round fails, this plan has no public result.
	last, err := results.BeginRun(ctx, plan, "two-pm", []*service.ScheduledTestResult{input(plan, 90, candyID, noon.Add(2*time.Hour))})
	require.NoError(t, err)
	finish(last[0], "failed")
	assertVisible(other[0].ID)
	var eligible int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT count(*) FROM accounts WHERE id IN (90,91,92,93) AND status='active' AND schedulable`).Scan(&eligible))
	require.Equal(t, 4, eligible, "listing results does not modify account state or scheduling")
}

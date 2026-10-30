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

func testScheduledTestDeletedAccountResults(t *testing.T, ctx context.Context, db *sql.DB, plans service.ScheduledTestPlanRepository, results *scheduledTestResultRepository, candyID int64) {
	t.Helper()
	_, err := db.ExecContext(ctx, `INSERT INTO groups(id,name) VALUES (99201,'Deleted account results');
INSERT INTO accounts(id,name,status,schedulable) VALUES
 (99211,'Deleted result account','active',true),
 (99212,'Disabled result account','disabled',false),
 (99213,'Active result account','active',true);
INSERT INTO account_groups(account_id,group_id) VALUES (99211,99201),(99212,99201),(99213,99201)`)
	require.NoError(t, err)
	groupID, deletedID, disabledID, activeID := int64(99201), int64(99211), int64(99212), int64(99213)
	at := time.Date(2026, time.October, 30, 4, 0, 0, 0, time.UTC)
	for _, scenario := range []struct {
		name     string
		snapshot bool
	}{
		{name: "latest execution snapshot", snapshot: true},
		{name: "legacy empty latest run"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			_, err := db.ExecContext(ctx, `UPDATE accounts SET deleted_at=NULL WHERE id=$1`, deletedID)
			require.NoError(t, err)
			plan, err := plans.Create(ctx, &service.ScheduledTestPlan{
				Name: scenario.name, GroupID: &groupID, GroupIDs: []int64{groupID},
				TestDefinitionID: &candyID, TestDefinitionIDs: []int64{candyID},
				TargetMode: "all_accounts", ModelID: scenario.name, CronExpression: "0 * * * *", MaxResults: 10,
			})
			require.NoError(t, err)
			input := func(accountID *int64, status string, startedAt time.Time) *service.ScheduledTestResult {
				targetMode := "all_accounts"
				if accountID == nil {
					targetMode = "group"
				}
				return &service.ScheduledTestResult{
					PlanID: plan.ID, GroupID: &groupID, AccountID: accountID, TestDefinitionID: &candyID,
					TargetMode: targetMode, ModelID: plan.ModelID, Status: status, OutputKind: "text",
					StartedAt: startedAt, FinishedAt: startedAt,
				}
			}
			older, err := results.Create(ctx, input(&deletedID, "success", at.Add(-time.Hour)))
			require.NoError(t, err)
			inputs := []*service.ScheduledTestResult{
				input(&deletedID, "success", at), input(&disabledID, "failed", at),
				input(&activeID, "running", at), input(nil, "pending", at),
			}
			var current []*service.ScheduledTestResult
			if scenario.snapshot {
				current, err = results.BeginRun(ctx, plan, "deleted-account-results-round", inputs)
				require.NoError(t, err)
			} else {
				for _, row := range inputs {
					created, err := results.Create(ctx, row)
					require.NoError(t, err)
					current = append(current, created)
				}
				var latestRunID string
				require.NoError(t, db.QueryRowContext(ctx, `SELECT latest_run_id FROM scheduled_test_plans WHERE id=$1`, plan.ID).Scan(&latestRunID))
				require.Empty(t, latestRunID, "exercise the pre-snapshot listing path")
			}
			ids := func(rows []*service.ScheduledTestResult) []int64 {
				values := make([]int64, 0, len(rows))
				for _, row := range rows {
					values = append(values, row.ID)
				}
				return values
			}
			latest, err := results.ListByPlanID(ctx, plan.ID, 1)
			require.NoError(t, err)
			require.ElementsMatch(t, ids(current), ids(latest), "existing and disabled accounts are visible before deletion")
			_, err = db.ExecContext(ctx, `UPDATE accounts SET deleted_at=NOW() WHERE id=$1`, deletedID)
			require.NoError(t, err)
			latest, err = results.ListByPlanID(ctx, plan.ID, 1)
			require.NoError(t, err)
			require.ElementsMatch(t, ids(current[1:]), ids(latest), "deleted accounts disappear without hiding disabled accounts or account-free queued checks")
			for _, row := range latest {
				if row.ID == current[3].ID {
					require.Nil(t, row.AccountID)
					require.Equal(t, "group", row.TargetMode)
					require.Equal(t, "pending", row.Status)
				}
			}
			history, err := results.ListByPlanID(ctx, plan.ID, 10)
			require.NoError(t, err)
			require.ElementsMatch(t, append(ids(current), older.ID), ids(history), "soft deletion preserves all retained historical executions")
			for _, row := range history {
				if row.AccountID != nil && *row.AccountID == deletedID {
					require.Equal(t, "Deleted result account", row.AccountName, "historical records retain the deleted account name")
				}
			}
		})
	}
}

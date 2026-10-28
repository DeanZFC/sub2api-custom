//go:build integration

package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func testScheduledTestConfiguredModelOrder(t *testing.T, ctx context.Context, db *sql.DB, plans service.ScheduledTestPlanRepository, results *scheduledTestResultRepository, definitionID int64) {
	t.Helper()
	groupID, accountID := int64(8), int64(83)
	_, err := db.ExecContext(ctx, `INSERT INTO accounts(id,name) VALUES(83,'Configured model order'); INSERT INTO account_groups(account_id,group_id) VALUES(83,8)`)
	require.NoError(t, err)
	plan, err := plans.Create(ctx, &service.ScheduledTestPlan{
		Name: "Configured model order", GroupIDs: []int64{groupID}, GroupID: &groupID, Enabled: true,
		TestDefinitionID: &definitionID, TestDefinitionIDs: []int64{definitionID}, TargetMode: "all_accounts",
		ModelID: "z-first", ModelIDs: []string{"z-first", "a-second"}, CronExpression: "0 * * * *", MaxResults: 3,
	})
	require.NoError(t, err)
	started := time.Now().UTC().Truncate(time.Microsecond)
	for i, model := range plan.ModelIDs {
		_, err := results.Create(ctx, &service.ScheduledTestResult{
			PlanID: plan.ID, TestDefinitionID: &definitionID, GroupID: &groupID, AccountID: &accountID,
			TargetMode: plan.TargetMode, ModelID: model, Status: "success", OutputKind: "number", ResponseText: "21",
			StartedAt: started.Add(time.Duration(i) * time.Second), FinishedAt: started.Add(time.Duration(i) * time.Second),
		})
		require.NoError(t, err)
	}
	assertOrders := func(expected map[string]int) {
		t.Helper()
		public, err := results.ListVisible(ctx, 100, 3)
		require.NoError(t, err)
		allGroups, err := results.ListVisibleByGroup(ctx, 100)
		require.NoError(t, err)
		admin, err := results.ListByPlanID(ctx, plan.ID, 1)
		require.NoError(t, err)
		for _, rows := range [][]*service.ScheduledTestResult{public, allGroups, admin} {
			seen := 0
			for _, row := range rows {
				if row.PlanID != plan.ID {
					continue
				}
				seen++
				require.Equal(t, expected[row.ModelID], row.ModelOrder)
				history, err := results.ListVisibleHistory(ctx, 100, row.ID, 0, 20)
				require.NoError(t, err)
				require.Len(t, history, 1)
				require.Equal(t, row.ModelOrder, history[0].ModelOrder)
				loaded, err := results.GetByID(ctx, row.ID)
				require.NoError(t, err)
				require.Equal(t, row.ModelOrder, loaded.ModelOrder)
				if row.ModelOrder == 0 {
					encoded, err := json.Marshal(row)
					require.NoError(t, err)
					require.Contains(t, string(encoded), `"model_order":0`)
				}
			}
			require.Equal(t, 2, seen)
		}
	}
	assertOrders(map[string]int{"z-first": 0, "a-second": 1})
	_, err = db.ExecContext(ctx, `UPDATE scheduled_test_plans SET model_ids=ARRAY['a-second','z-first'] WHERE id=$1`, plan.ID)
	require.NoError(t, err)
	assertOrders(map[string]int{"a-second": 0, "z-first": 1})
	// Single-model legacy rules retain their first choice; removed models sort last.
	_, err = db.ExecContext(ctx, `UPDATE scheduled_test_plans SET model_ids='{}',model_id='z-first' WHERE id=$1`, plan.ID)
	require.NoError(t, err)
	assertOrders(map[string]int{"z-first": 0, "a-second": 2147483647})
}

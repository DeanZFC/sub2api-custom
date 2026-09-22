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

func testScheduledTestCurrentGroupOrder(t *testing.T, ctx context.Context, db *sql.DB, plans service.ScheduledTestPlanRepository, results *scheduledTestResultRepository, definitionID int64) {
	t.Helper()
	_, err := db.ExecContext(ctx, `INSERT INTO groups(id,name) VALUES(20,'Original order group'),(21,'Destination order group'),(22,'No configured rule');
INSERT INTO accounts(id,name) VALUES(80,'Order projection account');
INSERT INTO account_groups(account_id,group_id) VALUES(80,20)`)
	require.NoError(t, err)
	newPlan := func(groupID int64, order int, enabled bool, mode string) *service.ScheduledTestPlan {
		t.Helper()
		plan, err := plans.Create(ctx, &service.ScheduledTestPlan{
			Name: "Current group order", GroupID: &groupID, SortOrder: order, Enabled: enabled,
			TestDefinitionID: &definitionID, TestDefinitionIDs: []int64{definitionID}, TargetMode: mode,
			ModelID: "group-order", CronExpression: "0 * * * *", MaxResults: 3,
		})
		require.NoError(t, err)
		return plan
	}
	source := newPlan(20, 5, true, "all_accounts")
	aggregatePlan := newPlan(20, 9, true, "group")
	zeroOrder := newPlan(21, 0, false, "group")
	newPlan(21, 4, true, "group")
	started := time.Now().UTC().Truncate(time.Microsecond)
	newResult := func(plan *service.ScheduledTestPlan, accountID *int64) *service.ScheduledTestResult {
		t.Helper()
		result, err := results.Create(ctx, &service.ScheduledTestResult{
			PlanID: plan.ID, TestDefinitionID: &definitionID, GroupID: plan.GroupID, AccountID: accountID,
			TargetMode: plan.TargetMode, Status: "success", OutputKind: "number", ResponseText: "21",
			ModelID: plan.ModelID, StartedAt: started, FinishedAt: started,
		})
		require.NoError(t, err)
		return result
	}
	accountID := int64(80)
	accountResult, aggregateResult := newResult(source, &accountID), newResult(aggregatePlan, nil)
	assertProjection := func(expectedGroup int64, expectedOrder int) {
		t.Helper()
		visible, err := results.ListVisible(ctx, 100, 3)
		require.NoError(t, err)
		byID := make(map[int64]*service.ScheduledTestResult, len(visible))
		for _, row := range visible {
			byID[row.ID] = row
		}
		for _, expected := range []struct {
			result                         *service.ScheduledTestResult
			groupID                        int64
			groupOrder, producingPlanOrder int
		}{
			{accountResult, expectedGroup, expectedOrder, 5},
			{aggregateResult, 20, 5, 9},
		} {
			row := byID[expected.result.ID]
			require.NotNil(t, row)
			require.Equal(t, expected.groupID, *row.GroupID)
			require.NotNil(t, row.GroupOrder)
			require.Equal(t, expected.groupOrder, *row.GroupOrder)
			require.Equal(t, expected.producingPlanOrder, row.PlanOrder)
			history, err := results.ListVisibleHistory(ctx, 100, row.ID, 0, 20)
			require.NoError(t, err)
			require.Len(t, history, 1)
			require.Equal(t, expected.groupID, *history[0].GroupID)
			require.Equal(t, row.GroupOrder, history[0].GroupOrder)
			require.Equal(t, row.PlanOrder, history[0].PlanOrder)
			if expected.groupOrder == 0 {
				encoded, err := json.Marshal(row)
				require.NoError(t, err)
				require.Contains(t, string(encoded), `"group_order":0`, "zero is a configured display order, not an absent field")
			}
		}
	}
	assertProjection(20, 5)
	_, err = db.ExecContext(ctx, `UPDATE account_groups SET group_id=21 WHERE account_id=80`)
	require.NoError(t, err)
	assertProjection(21, 0) // MIN includes the disabled plan, even without any result from that plan.
	zeroOrder.SortOrder = 7
	_, err = plans.Update(ctx, zeroOrder)
	require.NoError(t, err)
	assertProjection(21, 4) // A configuration edit immediately changes existing cards and history.
	_, err = db.ExecContext(ctx, `UPDATE account_groups SET group_id=22 WHERE account_id=80`)
	require.NoError(t, err)
	assertProjection(22, 2147483647) // An unconfigured destination does not inherit the producing plan's order.
}

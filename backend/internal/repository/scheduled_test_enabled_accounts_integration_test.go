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

func testScheduledTestEnabledTargets(t *testing.T, ctx context.Context, db *sql.DB, plans service.ScheduledTestPlanRepository, results *scheduledTestResultRepository, candyID, htmlID int64) {
	t.Helper()
	_, err := db.ExecContext(ctx, `INSERT INTO groups(id,name) VALUES
 (99101,'Enabled targets'),(99102,'Second source'),(99103,'Outside policy');
INSERT INTO accounts(id,name,status,schedulable,deleted_at) VALUES
 (99111,'Enabled','active',true,NULL),
 (99112,'Automatic pause','quality_paused',true,NULL),
 (99113,'Inactive','inactive',true,NULL),
 (99114,'Disabled','disabled',true,NULL),
 (99115,'Manual scheduling stop','active',false,NULL),
 (99116,'Automatic pause manually stopped','quality_paused',false,NULL),
 (99117,'Error','error',true,NULL),
 (99118,'Deleted','active',true,NOW()),
 (99119,'Excluded','active',true,NULL),
 (99120,'Outside policy','active',true,NULL);
INSERT INTO account_groups(account_id,group_id) VALUES
 (99111,99101),(99111,99102),(99112,99102),(99113,99101),(99114,99101),
 (99115,99101),(99116,99102),(99117,99101),(99118,99101),(99119,99101),(99120,99103)`)
	require.NoError(t, err)
	groupID := int64(99101)
	plan, err := plans.Create(ctx, &service.ScheduledTestPlan{
		Name: "Enabled account targets", GroupIDs: []int64{groupID, 99102}, GroupID: &groupID,
		ExcludedAccountIDs: []int64{99119}, TestDefinitionID: &candyID, TestDefinitionIDs: []int64{candyID, htmlID},
		TargetMode: "all_accounts", ModelID: "enabled-targets", CronExpression: "0 * * * *", Enabled: true, MaxResults: 20,
	})
	require.NoError(t, err)
	for _, list := range []func(context.Context, *service.ScheduledTestPlan, *int64) ([]int64, error){
		results.ListPlanTargetAccountIDs, results.ListPlanDetectionAccountIDs,
	} {
		ids, err := list(ctx, plan, nil)
		require.NoError(t, err)
		require.Equal(t, []int64{99111, 99112}, ids, "only enabled accounts and automatic pauses are targets; multiple source groups do not duplicate them")
		for accountID := int64(99113); accountID <= 99120; accountID++ {
			ids, err = list(ctx, plan, &accountID)
			require.NoError(t, err)
			require.Empty(t, ids, "explicit account filtering must not bypass detection eligibility: %d", accountID)
		}
	}
	ids, err := results.ListPlanTargetAccountIDs(ctx, plan, nil)
	require.NoError(t, err)
	rows, err := results.BeginRun(ctx, plan, "enabled-target-round", genericRoundInputs(plan, ids, time.Now().UTC().Truncate(time.Microsecond)))
	require.NoError(t, err)
	require.Len(t, rows, 4, "disabled accounts never create queued test rows")
	for _, row := range rows {
		require.NotNil(t, row.AccountID)
		require.Contains(t, ids, *row.AccountID)
	}
	for _, accountID := range ids {
		eligible, err := results.IsPlanRunAccountEligible(ctx, plan.ID, "enabled-target-round", accountID)
		require.NoError(t, err)
		require.True(t, eligible)
	}
	_, err = db.ExecContext(ctx, `UPDATE accounts SET schedulable=false WHERE id IN (99111,99112)`)
	require.NoError(t, err)
	for _, accountID := range ids {
		eligible, err := results.IsPlanRunAccountEligible(ctx, plan.ID, "enabled-target-round", accountID)
		require.NoError(t, err)
		require.False(t, eligible, "disabling an already queued account prevents execution")
	}
	ids, err = results.ListPlanTargetAccountIDs(ctx, plan, nil)
	require.NoError(t, err)
	require.Empty(t, ids)
	_, err = db.ExecContext(ctx, `UPDATE accounts SET status='active',schedulable=true WHERE id IN (99111,99113)`)
	require.NoError(t, err)
	ids, err = results.ListPlanTargetAccountIDs(ctx, plan, nil)
	require.NoError(t, err)
	require.Equal(t, []int64{99111, 99113}, ids, "reenabled accounts return on the next target discovery")
}

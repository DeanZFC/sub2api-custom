//go:build integration

package repository

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

func testScheduledTestExcludedAccounts(t *testing.T, ctx context.Context, db *sql.DB, plans *scheduledTestPlanRepository, repo *scheduledTestResultRepository, definitionID int64) {
	plan := genericRoutingPlan(definitionID, 8, 10)
	plan.ExcludedAccountIDs = []int64{63}
	plan.Protection.Rules[0].OnFail.Scheduling = "pause"
	plan, err := plans.Create(ctx, plan)
	require.NoError(t, err)
	loaded, err := plans.GetByID(ctx, plan.ID)
	require.NoError(t, err)
	require.Equal(t, []int64{63}, loaded.ExcludedAccountIDs)
	_, err = db.ExecContext(ctx, `INSERT INTO account_groups(account_id,group_id) VALUES(63,10)`)
	require.NoError(t, err)
	for _, eligibleOnly := range []bool{false, true} {
		ids, err := repo.listPlanAccountIDs(ctx, plan, nil, eligibleOnly)
		require.NoError(t, err)
		require.Equal(t, []int64{62}, ids, "excluded even if present in multiple selected groups")
	}
	accountPlans, err := plans.ListByAccountID(ctx, 63)
	require.NoError(t, err)
	require.Empty(t, accountPlans)
	other := *plan
	other.ID, other.Enabled, other.Protection = 0, false, service.ScheduledTestProtectionConfig{}
	other.ExcludedAccountIDs = nil
	otherPlan, err := plans.Create(ctx, &other)
	require.NoError(t, err)
	ids, err := repo.ListPlanTargetAccountIDs(ctx, otherPlan, nil)
	require.NoError(t, err)
	require.Equal(t, []int64{62, 63}, ids, "exclusions belong only to their strategy")
	started := time.Now().UTC().Truncate(time.Microsecond)
	_, err = repo.BeginRun(ctx, plan, "excluded-injected", genericRoundInputs(plan, []int64{63}, started))
	require.Error(t, err, "snapshot must reject excluded accounts even if discovery is bypassed")
	rows, err := repo.BeginRun(ctx, plan, "before-exclusion", genericRoundInputs(plan, []int64{62}, started))
	require.NoError(t, err)
	require.NoError(t, repo.BeginProtectionRun(ctx, plan, started))
	require.NoError(t, repo.BeginProtection(ctx, rows[0], plan.Protection.Rules[0]))
	rows[0].Status = "success"
	require.NoError(t, repo.Update(ctx, rows[0]))
	require.NoError(t, repo.CompleteProtection(ctx, rows[0], "fail", "test pause"))
	var status string
	require.NoError(t, db.QueryRowContext(ctx, `SELECT status FROM accounts WHERE id=62`).Scan(&status))
	require.Equal(t, "quality_paused", status)
	stale := *plan
	plan.ExcludedAccountIDs = []int64{62, 63}
	plan, err = plans.Update(ctx, plan)
	require.NoError(t, err)
	require.Equal(t, []int64{62, 63}, plan.ExcludedAccountIDs)
	eligible, err := repo.IsPlanRunAccountEligible(ctx, plan.ID, "before-exclusion", 62)
	require.NoError(t, err)
	require.False(t, eligible)
	_, err = repo.BeginRun(ctx, &stale, "stale-exclusion", genericRoundInputs(&stale, []int64{62}, started.Add(time.Minute)))
	require.ErrorContains(t, err, "policy changed")
	require.NoError(t, repo.CompleteProtection(ctx, rows[0], "fail", "late failure"))
	var schedulable bool
	require.NoError(t, db.QueryRowContext(ctx, `SELECT status,schedulable FROM accounts WHERE id=62`).Scan(&status, &schedulable))
	require.Equal(t, "active", status, "old policy hold is removed and late results cannot pause again")
	require.True(t, schedulable)
	var groups pq.Int64Array
	require.NoError(t, db.QueryRowContext(ctx, `SELECT array_agg(group_id ORDER BY group_id) FROM account_groups WHERE account_id=62`).Scan(&groups))
	require.Equal(t, pq.Int64Array{8}, groups, "excluding does not change the account's current groups")
	ids, err = repo.ListPlanTargetAccountIDs(ctx, plan, nil)
	require.NoError(t, err)
	require.Empty(t, ids)
	plan.ExcludedAccountIDs = []int64{}
	plan, err = plans.Update(ctx, plan)
	require.NoError(t, err)
	require.Empty(t, plan.ExcludedAccountIDs)
	ids, err = repo.ListPlanTargetAccountIDs(ctx, plan, nil)
	require.NoError(t, err)
	require.Equal(t, []int64{62, 63}, ids)
}

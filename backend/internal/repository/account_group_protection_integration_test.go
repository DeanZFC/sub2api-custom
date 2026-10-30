//go:build integration

package repository

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/pkg/adminaccess"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

func testManualMembershipProtection(t *testing.T, ctx context.Context, db *sql.DB, plans *scheduledTestPlanRepository, results *scheduledTestResultRepository, definitionID int64, scenario string) {
	exec := func(query string, args ...any) {
		t.Helper()
		_, err := db.ExecContext(ctx, query, args...)
		require.NoError(t, err)
	}
	plan := genericRoutingPlan(definitionID, 8, 10)
	plan.Protection.Rules[0].OnFail.Scheduling = "pause"
	plan, err := plans.Create(ctx, plan)
	require.NoError(t, err)
	started := time.Now().UTC().Truncate(time.Microsecond)
	rows, err := results.BeginRun(ctx, plan, "membership-round", genericRoundInputs(plan, []int64{62, 63}, started))
	require.NoError(t, err)
	require.NoError(t, results.BeginProtectionRun(ctx, plan, started))
	for _, row := range rows {
		require.NoError(t, results.BeginProtection(ctx, row, plan.Protection.Rules[0]))
	}
	exec(`UPDATE scheduled_test_protection_states SET blocked=TRUE,reason='old policy hold' WHERE plan_id=$1`, plan.ID)
	exec(`INSERT INTO scheduled_test_combination_states(plan_id,account_id,blocked,reason) VALUES($1,62,TRUE,'combined hold'),($1,63,TRUE,'combined hold')`, plan.ID)
	exec(`UPDATE accounts SET status='quality_paused',extra='{"quality_protection_reason":"old policy hold","setting":"keep"}'`)
	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	accounts := newAccountRepositoryWithSQL(client, db, nil)
	bind := accounts.BindGroups
	if scenario == "scoped detach" || scenario == "scoped concurrent completion" {
		bind = func(ctx context.Context, accountID int64, groupIDs []int64) error {
			tx, err := client.Tx(ctx)
			if err != nil {
				return err
			}
			defer tx.Rollback()
			txctx := dbent.NewTxContext(adminaccess.WithPrincipal(ctx, &adminaccess.Principal{}), tx)
			if err := accounts.ScopedAdminLockAccountMembership(txctx, accountID); err != nil {
				return err
			}
			if _, err := tx.Client().ExecContext(txctx, "SELECT id FROM accounts WHERE id=$1 FOR UPDATE", accountID); err != nil {
				return err
			}
			if err := accounts.BindGroups(txctx, accountID, groupIDs); err != nil {
				return err
			}
			return tx.Commit()
		}
	}
	kept := scenario == "same policy" || scenario == "remove one source"
	if scenario == "manual disabled" {
		exec(`UPDATE accounts SET status='disabled' WHERE id=62`)
	}
	if scenario == "manual unschedulable" {
		exec(`UPDATE accounts SET schedulable=FALSE WHERE id=62`)
	}
	if scenario == "other policy" {
		other := genericRoutingPlan(definitionID, 9)
		other.Protection.Rules[0].OnPass = &service.ScheduledTestOutcomeAction{GroupMode: "keep"}
		other.Protection.Rules[0].OnFail = &service.ScheduledTestOutcomeAction{GroupMode: "keep", Scheduling: "pause"}
		other, err = plans.Create(ctx, other)
		require.NoError(t, err)
		exec(`INSERT INTO scheduled_test_combination_states(plan_id,account_id,blocked,reason) VALUES($1,62,TRUE,'other policy hold')`, other.ID)
	}
	if scenario == "late begin" {
		exec(`DELETE FROM scheduled_test_protection_states WHERE account_id=62; DELETE FROM scheduled_test_combination_states WHERE account_id=62; UPDATE accounts SET status='active' WHERE id=62`)
	}
	if scenario == "remove one source" {
		require.NoError(t, accounts.AddToGroup(ctx, 62, 10, 1))
	}
	if scenario == "outbox rollback" {
		exec(`ALTER TABLE scheduler_outbox ADD CONSTRAINT reject_membership_event CHECK (event_type<>'account_groups_changed') NOT VALID`)
		defer exec(`ALTER TABLE scheduler_outbox DROP CONSTRAINT reject_membership_event`)
	}
	switch scenario {
	case "concurrent completion", "scoped concurrent completion":
		// A result writer already owns the plan. The membership edit must wait,
		// then remove its just-committed hold under the same lock ordering.
		tx, txErr := db.BeginTx(ctx, nil)
		require.NoError(t, txErr)
		defer tx.Rollback()
		_, txErr = lockProtectionPlan(ctx, tx, plan.ID)
		require.NoError(t, txErr)
		completed := make(chan error, 1)
		go func() { completed <- bind(ctx, 62, []int64{9}) }()
		require.Eventually(t, func() bool {
			var waiting bool
			err := db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE application_name=current_setting('application_name') AND pid<>pg_backend_pid() AND wait_event_type='Lock')`).Scan(&waiting)
			return err == nil && waiting
		}, 5*time.Second, 10*time.Millisecond)
		_, txErr = tx.ExecContext(ctx, `UPDATE scheduled_test_protection_states SET blocked=TRUE,reason='concurrent completion' WHERE account_id=62 AND plan_id=$1`, plan.ID)
		require.NoError(t, txErr)
		require.NoError(t, tx.Commit())
		select {
		case err = <-completed:
		case <-ctx.Done():
			t.Fatal("membership edit did not finish after the protection writer committed")
		}
	case "same policy":
		err = accounts.BindGroups(ctx, 62, []int64{10})
	case "remove one source", "remove last source":
		err = accounts.RemoveFromGroup(ctx, 62, 8)
	case "clear all":
		err = accounts.BindGroups(ctx, 62, nil)
	default:
		err = bind(ctx, 62, []int64{9})
	}
	if scenario == "outbox rollback" {
		require.Error(t, err)
	} else {
		require.NoError(t, err)
	}
	var holds int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM scheduled_test_protection_states WHERE account_id=62 AND plan_id=$1)+(SELECT count(*) FROM scheduled_test_combination_states WHERE account_id=62 AND plan_id=$1)`, plan.ID).Scan(&holds))
	var revoked bool
	require.NoError(t, db.QueryRowContext(ctx, `SELECT protection_revoked FROM scheduled_test_results WHERE id=$1`, rows[0].ID).Scan(&revoked))
	if kept || scenario == "outbox rollback" {
		require.Equal(t, 2, holds)
		require.False(t, revoked)
		if scenario == "outbox rollback" {
			var groups pq.Int64Array
			require.NoError(t, db.QueryRowContext(ctx, `SELECT array_agg(group_id ORDER BY group_id) FROM account_groups WHERE account_id=62`).Scan(&groups))
			require.Equal(t, pq.Int64Array{8, 9}, groups, "failed outbox enqueue rolls the membership edit back")
		}
		return
	}
	require.Zero(t, holds)
	require.True(t, revoked)
	var status, setting string
	var schedulable bool
	require.NoError(t, db.QueryRowContext(ctx, `SELECT status,schedulable,extra->>'setting' FROM accounts WHERE id=62`).Scan(&status, &schedulable, &setting))
	require.Equal(t, "keep", setting)
	switch scenario {
	case "manual disabled":
		require.Equal(t, "disabled", status)
	case "manual unschedulable":
		require.False(t, schedulable)
	case "other policy":
		require.Equal(t, "quality_paused", status)
	default:
		require.Equal(t, "active", status)
	}
	var otherHolds int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT count(*) FROM scheduled_test_combination_states WHERE plan_id=$1 AND account_id=63 AND blocked`, plan.ID).Scan(&otherHolds))
	require.Equal(t, 1, otherHolds)
	eligible, err := results.IsPlanRunAccountEligible(ctx, plan.ID, rows[0].RunID, 62)
	require.NoError(t, err)
	require.False(t, eligible)
	eligible, err = results.IsPlanRunAccountEligible(ctx, plan.ID, rows[1].RunID, 63)
	require.NoError(t, err)
	require.True(t, eligible)
	// Late starts, completions and round initialization cannot restore a hold.
	rows[0].Status = "success"
	require.NoError(t, results.Update(ctx, rows[0]))
	require.NoError(t, results.BeginProtectionRun(ctx, plan, started))
	require.NoError(t, results.BeginProtection(ctx, rows[0], plan.Protection.Rules[0]))
	require.NoError(t, results.CompleteProtection(ctx, rows[0], "fail", "late failure"))
	require.NoError(t, db.QueryRowContext(ctx, `SELECT count(*) FROM scheduled_test_protection_states WHERE account_id=62 AND plan_id=$1`, plan.ID).Scan(&holds))
	require.Zero(t, holds)
	var events int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT count(*) FROM scheduler_outbox WHERE account_id=62 AND event_type='account_groups_changed'`).Scan(&events))
	require.Positive(t, events, "empty bindings must also invalidate scheduler caches")
	if scenario == "stale discovery" {
		_, err := results.BeginRun(ctx, plan, "stale-targets", genericRoundInputs(plan, []int64{62, 63}, started.Add(time.Hour)))
		require.ErrorContains(t, err, "left test policy groups")
		var latest string
		require.NoError(t, db.QueryRowContext(ctx, `SELECT latest_run_id FROM scheduled_test_plans WHERE id=$1`, plan.ID).Scan(&latest))
		require.Equal(t, rows[0].RunID, latest, "rejected snapshot must not invalidate the active round")
	}
	if scenario == "detach" {
		require.NoError(t, accounts.BindGroups(ctx, 62, []int64{8}))
		eligible, err := results.IsPlanRunAccountEligible(ctx, plan.ID, rows[0].RunID, 62)
		require.NoError(t, err)
		require.False(t, eligible, "moving back cannot resurrect the old round")
		fresh, err := results.BeginRun(ctx, plan, "new-membership-round", genericRoundInputs(plan, []int64{62, 63}, started.Add(time.Hour)))
		require.NoError(t, err)
		require.NoError(t, results.BeginProtectionRun(ctx, plan, started.Add(time.Hour)))
		require.NoError(t, results.BeginProtection(ctx, fresh[0], plan.Protection.Rules[0]))
		eligible, err = results.IsPlanRunAccountEligible(ctx, plan.ID, fresh[0].RunID, 62)
		require.NoError(t, err)
		require.True(t, eligible, "a newly frozen round can protect the account again")
	}
}

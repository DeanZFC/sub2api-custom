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
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func testMembershipRefreshesConfiguredModelProjection(t *testing.T, ctx context.Context, db *sql.DB, plans *scheduledTestPlanRepository, results *scheduledTestResultRepository, definitionID int64) {
	makePlan := func(group int64, model string) *service.ScheduledTestPlan {
		plan := genericRoutingPlan(definitionID, group)
		plan.ModelID, plan.ModelIDs = model, []string{model}
		plan.Protection.Rules[0].OnPass = &service.ScheduledTestOutcomeAction{Scheduling: "resume", GroupMode: "keep"}
		plan.Protection.Rules[0].OnFail = &service.ScheduledTestOutcomeAction{Scheduling: "pause", GroupMode: "keep"}
		created, err := plans.Create(ctx, plan)
		require.NoError(t, err)
		return created
	}
	plan := makePlan(8, "model-a")
	makePlan(10, "model-b") // This policy has not run or enrolled the account yet.
	rows := multiModelProtectionRound(t, ctx, results, plan, "membership-model-a", time.Now().UTC())
	completeMultiModelProtection(t, ctx, results, rows["model-a"][0], "fail")
	status := func(want string) {
		t.Helper()
		var got string
		require.NoError(t, db.QueryRowContext(ctx, `SELECT status FROM accounts WHERE id=62`).Scan(&got))
		require.Equal(t, want, got)
	}
	status(service.StatusQualityPaused)
	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	accounts := newAccountRepositoryWithSQL(client, db, nil)
	require.NoError(t, accounts.AddToGroup(ctx, 62, 10, 1))
	status(service.StatusActive)
	checkMultiModelProtectionStates(t, ctx, db, map[string]string{"model-a": "blocked", "model-b": "pending"})
	require.NoError(t, accounts.RemoveFromGroup(ctx, 62, 10))
	status(service.StatusQualityPaused)
	checkMultiModelProtectionStates(t, ctx, db, map[string]string{"model-a": "blocked"})
	require.NoError(t, accounts.AddToGroup(ctx, 62, 10, 1))
	require.NoError(t, accounts.RemoveFromGroup(ctx, 62, 8))
	status(service.StatusActive)
	checkMultiModelProtectionStates(t, ctx, db, map[string]string{"model-b": "pending"})
	require.NoError(t, accounts.RemoveFromGroup(ctx, 62, 10))
	checkMultiModelProtectionStates(t, ctx, db, map[string]string{})
	// Unrelated memberships must not release legacy pauses when no active
	// automation or model projection participates in the edit.
	_, err := db.ExecContext(ctx, `UPDATE accounts SET status='quality_paused',extra='{"quality_protection_reason":"legacy hold"}' WHERE id=62`)
	require.NoError(t, err)
	require.NoError(t, accounts.AddToGroup(ctx, 62, 11, 1))
	status(service.StatusQualityPaused)
	require.NoError(t, accounts.RemoveFromGroup(ctx, 62, 11))
	status(service.StatusQualityPaused)
}

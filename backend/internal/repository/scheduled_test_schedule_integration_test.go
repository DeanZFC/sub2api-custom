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

func testScheduledTestTimePeriodsPersistence(t *testing.T, ctx context.Context, db *sql.DB, plans *scheduledTestPlanRepository, definitionID int64) {
	input := genericRoutingPlan(definitionID, 8, 10)
	input.Protection = service.ScheduledTestProtectionConfig{}
	initialNext := time.Now().UTC().Add(time.Hour).Truncate(time.Microsecond)
	input.NextRunAt = &initialNext
	plan, err := plans.Create(ctx, input)
	require.NoError(t, err)
	require.Nil(t, plan.Schedule)
	plan.Schedule = &service.ScheduledTestSchedule{Timezone: "Asia/Shanghai", Periods: []service.ScheduledTestSchedulePeriod{
		{Start: "08:00", End: "20:00", IntervalMinutes: 60},
		{Start: "20:00", End: "08:00", IntervalMinutes: 180},
	}}
	plan, err = plans.Update(ctx, plan)
	require.NoError(t, err)
	loaded, err := plans.GetByID(ctx, plan.ID)
	require.NoError(t, err)
	require.Equal(t, plan.Schedule, loaded.Schedule)
	listed, err := plans.List(ctx)
	require.NoError(t, err)
	require.Len(t, listed, 1)
	require.Equal(t, plan.Schedule, listed[0].Schedule)
	listed, err = plans.ListByAccountID(ctx, 62)
	require.NoError(t, err)
	require.Equal(t, plan.Schedule, listed[0].Schedule)
	due, err := plans.ListDue(ctx, initialNext.Add(-time.Second))
	require.NoError(t, err)
	require.Empty(t, due)
	due, err = plans.ListDue(ctx, initialNext)
	require.NoError(t, err)
	require.Len(t, due, 1)
	require.Equal(t, plan.Schedule, due[0].Schedule)

	oldRun := *plan
	plan.Schedule = &service.ScheduledTestSchedule{Timezone: "UTC", Periods: []service.ScheduledTestSchedulePeriod{{Start: "00:00", End: "24:00", IntervalMinutes: 120}}}
	newNext := initialNext.Add(2 * time.Hour)
	plan.NextRunAt = &newNext
	plan, err = plans.Update(ctx, plan)
	require.NoError(t, err)
	finished := initialNext.Add(time.Minute)
	require.NoError(t, plans.UpdateAfterRun(ctx, &oldRun, finished, initialNext.Add(3*time.Minute)))
	loaded, err = plans.GetByID(ctx, plan.ID)
	require.NoError(t, err)
	require.True(t, loaded.NextRunAt.Equal(newNext), "an old round cannot overwrite the new schedule")
	require.True(t, loaded.LastRunAt.Equal(finished))
	advanced := newNext.Add(2 * time.Hour)
	require.NoError(t, plans.UpdateAfterRun(ctx, plan, finished, advanced))
	loaded, err = plans.GetByID(ctx, plan.ID)
	require.NoError(t, err)
	require.True(t, loaded.NextRunAt.Equal(advanced))

	stale := *loaded
	deferred := advanced.Add(12 * time.Hour)
	require.NoError(t, plans.UpdateNextRun(ctx, loaded, deferred))
	loaded, err = plans.GetByID(ctx, plan.ID)
	require.NoError(t, err)
	require.True(t, loaded.NextRunAt.Equal(deferred))
	require.True(t, loaded.LastRunAt.Equal(finished), "deferral does not fabricate a completed run")
	require.NoError(t, plans.UpdateNextRun(ctx, &stale, deferred.Add(time.Hour)))
	loaded, err = plans.GetByID(ctx, plan.ID)
	require.NoError(t, err)
	require.True(t, loaded.NextRunAt.Equal(deferred), "stale discovery cannot change an already advanced schedule")

	plan.Schedule = nil
	plan.CronExpression = "0 9 * * *"
	plan, err = plans.Update(ctx, plan)
	require.NoError(t, err)
	require.Nil(t, plan.Schedule)
	// Older migrated rows have SQL NULL instead of JSON null.
	_, err = db.ExecContext(ctx, `UPDATE scheduled_test_plans SET schedule_config=NULL WHERE id=$1`, plan.ID)
	require.NoError(t, err)
	require.NoError(t, plans.UpdateAfterRun(ctx, plan, finished, advanced))
	loaded, err = plans.GetByID(ctx, plan.ID)
	require.NoError(t, err)
	require.Nil(t, loaded.Schedule)
	require.True(t, loaded.NextRunAt.Equal(advanced))
}

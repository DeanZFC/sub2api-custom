package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func dailyTestSchedule() *ScheduledTestSchedule {
	return &ScheduledTestSchedule{Timezone: "Asia/Shanghai", Periods: []ScheduledTestSchedulePeriod{
		{Start: "08:00", End: "20:00", IntervalMinutes: 60},
		{Start: "20:00", End: "08:00", IntervalMinutes: 180},
	}}
}

func TestScheduledTestTimePeriodsNextRun(t *testing.T) {
	for _, tc := range []struct{ from, want string }{
		{"2026-10-14T07:59:59+08:00", "2026-10-14T08:00:00+08:00"},
		{"2026-10-14T08:00:00+08:00", "2026-10-14T09:00:00+08:00"},
		{"2026-10-14T19:00:00+08:00", "2026-10-14T20:00:00+08:00"},
		{"2026-10-14T20:00:00+08:00", "2026-10-14T23:00:00+08:00"},
		{"2026-10-14T23:00:00+08:00", "2026-10-15T02:00:00+08:00"},
		{"2026-10-15T02:00:00+08:00", "2026-10-15T05:00:00+08:00"},
		{"2026-10-15T05:00:00+08:00", "2026-10-15T08:00:00+08:00"},
		{"2026-10-14T00:00:00Z", "2026-10-14T01:00:00Z"},
	} {
		t.Run(tc.from, func(t *testing.T) {
			from, err := time.Parse(time.RFC3339, tc.from)
			require.NoError(t, err)
			want, err := time.Parse(time.RFC3339, tc.want)
			require.NoError(t, err)
			got, err := computeScheduledTestNextRun(&ScheduledTestPlan{Schedule: dailyTestSchedule()}, from)
			require.NoError(t, err)
			require.True(t, want.Equal(got), "want %s, got %s", want, got)
		})
	}
}

func TestScheduledTestTimePeriodsAnchorsGapsAndDST(t *testing.T) {
	for _, tc := range []struct {
		name, zone, start, end string
		interval               int
		from, want             string
	}{
		{"anchored minutes", "Asia/Shanghai", "08:15", "20:00", 90, "2026-10-14T08:16:00+08:00", "2026-10-14T09:45:00+08:00"},
		{"overnight anchor", "Asia/Shanghai", "23:15", "06:00", 90, "2026-10-14T23:16:00+08:00", "2026-10-15T00:45:00+08:00"},
		{"gap waits next day", "Asia/Shanghai", "08:00", "20:00", 60, "2026-10-14T19:00:00+08:00", "2026-10-15T08:00:00+08:00"},
		{"end excluded", "UTC", "08:00", "10:00", 120, "2026-10-14T08:00:00Z", "2026-10-15T08:00:00Z"},
		{"full day", "UTC", "00:00", "24:00", 180, "2026-10-14T21:00:00Z", "2026-10-15T00:00:00Z"},
		{"spring gap", "America/New_York", "02:00", "03:00", 60, "2026-03-08T01:59:00-05:00", "2026-03-09T02:00:00-04:00"},
		{"fall repeat", "America/New_York", "01:00", "02:00", 60, "2026-11-01T01:00:00-04:00", "2026-11-01T01:00:00-05:00"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			from, err := time.Parse(time.RFC3339, tc.from)
			require.NoError(t, err)
			want, err := time.Parse(time.RFC3339, tc.want)
			require.NoError(t, err)
			schedule := &ScheduledTestSchedule{Timezone: tc.zone, Periods: []ScheduledTestSchedulePeriod{{tc.start, tc.end, tc.interval}}}
			got, err := computeScheduledTestNextRun(&ScheduledTestPlan{Schedule: schedule}, from)
			require.NoError(t, err)
			require.True(t, want.Equal(got), "want %s, got %s", want, got)
		})
	}
}

func TestScheduledTestTimePeriodsValidation(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*ScheduledTestSchedule)
	}{
		{"missing timezone", func(s *ScheduledTestSchedule) { s.Timezone = "" }},
		{"server local timezone", func(s *ScheduledTestSchedule) { s.Timezone = "Local" }},
		{"invalid timezone", func(s *ScheduledTestSchedule) { s.Timezone = "Mars/Test" }},
		{"empty", func(s *ScheduledTestSchedule) { s.Periods = nil }},
		{"too many", func(s *ScheduledTestSchedule) { s.Periods = make([]ScheduledTestSchedulePeriod, 25) }},
		{"overlap", func(s *ScheduledTestSchedule) { s.Periods[1].Start = "19:59" }},
		{"overnight overlap", func(s *ScheduledTestSchedule) { s.Periods[1].End = "08:01" }},
		{"same endpoints", func(s *ScheduledTestSchedule) { s.Periods[0].End = "08:00" }},
		{"invalid minute", func(s *ScheduledTestSchedule) { s.Periods[0].Start = "08:60" }},
		{"short time", func(s *ScheduledTestSchedule) { s.Periods[0].Start = "8:00" }},
		{"signed hour", func(s *ScheduledTestSchedule) { s.Periods[0].Start = "+1:00" }},
		{"midnight start", func(s *ScheduledTestSchedule) { s.Periods[0].Start = "24:00" }},
		{"zero interval", func(s *ScheduledTestSchedule) { s.Periods[0].IntervalMinutes = 0 }},
		{"interval longer than period", func(s *ScheduledTestSchedule) { s.Periods[0].IntervalMinutes = 721 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			schedule := dailyTestSchedule()
			tc.edit(schedule)
			_, _, err := parseScheduledTestSchedule(schedule)
			require.Error(t, err)
		})
	}
}

type timePeriodDeferredPlanRepo struct {
	ScheduledTestPlanRepository
	plan *ScheduledTestPlan
	next time.Time
}

func (r *timePeriodDeferredPlanRepo) ListDue(context.Context, time.Time) ([]*ScheduledTestPlan, error) {
	return []*ScheduledTestPlan{r.plan}, nil
}

func (r *timePeriodDeferredPlanRepo) UpdateNextRun(_ context.Context, _ *ScheduledTestPlan, next time.Time) error {
	r.next = next
	return nil
}

func TestScheduledTestTimePeriodsDeferOverdueRunsOutsideWindow(t *testing.T) {
	schedule := &ScheduledTestSchedule{Timezone: "Asia/Shanghai", Periods: []ScheduledTestSchedulePeriod{{Start: "08:00", End: "20:00", IntervalMinutes: 60}}}
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC) // 20:00 Shanghai, end excluded.
	previous := now.Add(-2 * time.Hour)
	due := now.Add(-time.Hour)
	plan := &ScheduledTestPlan{ID: 1, Schedule: schedule, LastRunAt: &previous, NextRunAt: &due}
	repo := &timePeriodDeferredPlanRepo{plan: plan}
	runner := &ScheduledTestRunnerService{planRepo: repo}
	// No execution repositories are installed: discovery must only reschedule,
	// without beginning a new result or clearing the current protection round.
	runner.runDuePlansAt(context.Background(), now)
	require.Equal(t, time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC), repo.next)
	require.Equal(t, previous, *plan.LastRunAt)
	for _, tc := range []struct {
		hour int
		want bool
	}{{0, true}, {11, true}, {12, false}, {23, false}} {
		allowed, err := scheduledTestScheduleAllows(schedule, time.Date(2026, 10, 14, tc.hour, 0, 0, 0, time.UTC))
		require.NoError(t, err)
		require.Equal(t, tc.want, allowed)
	}
	allowed, err := scheduledTestScheduleAllows(nil, now)
	require.NoError(t, err)
	require.True(t, allowed, "legacy cron does not use time period restrictions")
}

func TestScheduledTestTimePeriodsServiceAndLegacyCron(t *testing.T) {
	plans := &multiDefinitionPlanRepoStub{}
	svc := NewScheduledTestService(plans, nil)
	svc.SetDefinitionRepository(multiDefinitionRepoStub{definitions: map[int64]*ScheduledTestDefinition{1: {ID: 1, Enabled: true}}})
	plan, err := svc.CreatePlan(context.Background(), &ScheduledTestPlan{GroupIDs: []int64{8}, TestDefinitionIDs: []int64{1}, ModelID: "model", Schedule: dailyTestSchedule()})
	require.NoError(t, err, "period schedules do not require cron")
	require.NotNil(t, plan.NextRunAt)
	plan.Schedule = nil
	plan.CronExpression = "CRON_TZ=Asia/Shanghai 15 * * * *"
	updated, err := svc.UpdatePlan(context.Background(), plan)
	require.NoError(t, err)
	require.Nil(t, updated.Schedule)
	from := time.Date(2026, 10, 14, 0, 16, 0, 0, time.UTC)
	next, err := computeScheduledTestNextRun(plan, from)
	require.NoError(t, err)
	require.Equal(t, from.Add(59*time.Minute), next)
}

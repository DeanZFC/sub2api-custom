package service

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type actionStatisticsResultRepo struct {
	*statisticsResultRepoStub
	ScheduledTestProtectionRepository
	mu               sync.Mutex
	selectedGroups   []int64
	completedVerdict map[int64]string
}

func (r *actionStatisticsResultRepo) ListPlanDetectionAccountIDs(_ context.Context, plan *ScheduledTestPlan, accountID *int64) ([]int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if plan.GroupID != nil {
		r.selectedGroups = append(r.selectedGroups, *plan.GroupID)
	}
	if accountID != nil {
		return []int64{*accountID}, nil
	}
	return append([]int64(nil), r.accountIDs...), nil
}

func (*actionStatisticsResultRepo) BeginProtection(context.Context, *ScheduledTestResult, ScheduledTestProtectionRule) error {
	return nil
}

func (r *actionStatisticsResultRepo) CompleteProtection(_ context.Context, result *ScheduledTestResult, verdict, _ string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.completedVerdict[result.ID] = verdict
	return nil
}

func TestScheduledTestActionStatisticsFollowMovedAccountsWithoutChangingVisibility(t *testing.T) {
	for _, tc := range []struct {
		name                 string
		mode                 string
		actionsEnabled       bool
		protectionEnabled    bool
		actionOnAnotherType  bool
		wantCrossGroupSample bool
	}{
		{name: "account follows automatic moves", mode: "account", actionsEnabled: true, protectionEnabled: true, wantCrossGroupSample: true},
		{name: "all accounts follow automatic moves", mode: "all_accounts", actionsEnabled: true, protectionEnabled: true, wantCrossGroupSample: true},
		{name: "a different detection type can move the account", mode: "account", actionsEnabled: true, protectionEnabled: true, actionOnAnotherType: true, wantCrossGroupSample: true},
		{name: "disabled protection retains the source filter", mode: "account", actionsEnabled: true},
		{name: "scheduling-only protection retains the source filter", mode: "account", protectionEnabled: true},
		{name: "group aggregate never follows individual moves", mode: "group", actionsEnabled: true, protectionEnabled: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := &actionStatisticsResultRepo{
				statisticsResultRepoStub: &statisticsResultRepoStub{
					retryResultRepoStub: &retryResultRepoStub{updated: make(chan *ScheduledTestResult, 2)},
					accountIDs:          []int64{12, 13},
				},
				completedVerdict: make(map[int64]string),
			}
			plan := statisticsTestPlan()
			plan.Enabled = true
			plan.TargetMode = tc.mode
			if tc.mode == "account" {
				plan.AccountID = scheduledTestPtrInt64(13)
			}
			plan.Protection = ScheduledTestProtectionConfig{
				Enabled: tc.protectionEnabled,
				Rules: []ScheduledTestProtectionRule{{
					TestDefinitionID: 1,
					Thresholds:       []ScheduledTestThreshold{{Metric: "cache_rate", Operator: "lt", Value: 80}},
					MinSamples:       10,
				}},
			}
			if tc.actionsEnabled {
				if tc.actionOnAnotherType {
					plan.Protection.Rules = append(plan.Protection.Rules, ScheduledTestProtectionRule{TestDefinitionID: 2, PauseOnFailure: true})
				}
				rule := &plan.Protection.Rules[len(plan.Protection.Rules)-1]
				rule.OnPass = &ScheduledTestOutcomeAction{Scheduling: "keep", GroupMode: "assign", GroupIDs: []int64{10, 11}}
				rule.OnFail = &ScheduledTestOutcomeAction{Scheduling: "keep", GroupMode: "assign", GroupIDs: []int64{8}}
			}
			repo.collect = func(_ context.Context, filter ScheduledTestStatisticsFilter) (*ScheduledTestStatistics, error) {
				if tc.wantCrossGroupSample && filter.GroupID != nil {
					// A moved account no longer receives traffic under its original
					// group. Restricting statistics to that group would strand it in
					// pending forever even after requests in its new group improve.
					return &ScheduledTestStatistics{WindowStart: filter.WindowStart, WindowEnd: filter.WindowEnd}, nil
				}
				return &ScheduledTestStatistics{
					WindowStart: filter.WindowStart, WindowEnd: filter.WindowEnd,
					TotalRequests: 20, CacheInputTokens: 100, CacheReadTokens: 90, CacheRate: protectionFloat(.9),
				}, nil
			}
			runner := NewScheduledTestRunnerService(nil, statisticsTestService(repo), nil, nil, nil, nil)
			runner.runPlanDefinition(context.Background(), plan)
			count := 1
			if tc.mode == "all_accounts" {
				count = 2
			}
			require.Equal(t, count, repo.countCreated())
			require.Len(t, repo.filters, count)
			collectedIDs := make([]int64, 0, count)
			for _, filter := range repo.filters {
				if tc.wantCrossGroupSample {
					require.Nil(t, filter.GroupID, "recovery statistics must include the account's destination groups")
				} else {
					require.Equal(t, plan.GroupID, filter.GroupID)
				}
				require.Equal(t, plan.ModelID, filter.Model)
				require.Equal(t, time.Hour, filter.WindowEnd.Sub(filter.WindowStart))
				if tc.mode == "group" {
					require.Nil(t, filter.AccountID)
				} else {
					require.NotNil(t, filter.AccountID)
					collectedIDs = append(collectedIDs, *filter.AccountID)
				}
			}
			if tc.mode == "account" {
				require.Equal(t, []int64{13}, collectedIDs)
			} else if tc.mode == "all_accounts" {
				require.ElementsMatch(t, []int64{12, 13}, collectedIDs)
			}
			for range count {
				result := <-repo.updated
				require.Equal(t, "success", result.Status)
				require.Equal(t, plan.GroupID, result.GroupID, "source group remains the result visibility boundary")
				require.Equal(t, plan.ModelID, result.ModelID)
				require.Equal(t, int64(20), result.OutputStatistics.TotalRequests)
				if tc.wantCrossGroupSample {
					require.Equal(t, "pass", repo.completedVerdict[result.ID], "destination traffic must allow the configured metric to recover")
				}
			}
			for _, started := range repo.created {
				require.Equal(t, plan.GroupID, started.GroupID, "running result visibility must not be widened either")
			}
			for _, sourceGroup := range repo.selectedGroups {
				require.Equal(t, int64(8), sourceGroup, "eligibility must still use the plan's source-group authorization")
			}
			require.Equal(t, int64(8), *plan.GroupID, "statistics filtering must not mutate the saved plan")
		})
	}
}

package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

type allGroupQualityFallbackCandidatesTestRepo struct {
	groupQualityFallbackTestRepo
	allCalls, ordinaryCalls int
}

func (r *allGroupQualityFallbackCandidatesTestRepo) ListByGroup(ctx context.Context, groupID int64) ([]Account, error) {
	r.ordinaryCalls++
	accounts, err := r.groupQualityFallbackTestRepo.ListByGroup(ctx, groupID)
	normal := make([]Account, 0, len(accounts))
	for _, account := range accounts {
		if account.Status == StatusActive {
			normal = append(normal, account)
		}
	}
	return normal, err
}

func (r *allGroupQualityFallbackCandidatesTestRepo) ListGroupQualityFallbackCandidates(ctx context.Context, groupID int64) ([]Account, error) {
	r.allCalls++
	return r.groupQualityFallbackTestRepo.ListByGroup(ctx, groupID)
}

func TestGroupQualityFallbackLoadsAutomaticHoldsWithoutAdmittingManualStops(t *testing.T) {
	for _, mode := range []string{"legacy", "legacy_nonbatch", "advanced"} {
		t.Run(mode, func(t *testing.T) {
			resetOpenAIAdvancedSchedulerSettingCacheForTest()
			defer resetOpenAIAdvancedSchedulerSettingCacheForTest()
			groupID := int64(43)
			held := groupQualityFallbackTestAccount(1, groupID, 100, true)
			manualStop := groupQualityFallbackTestAccount(2, groupID, 0, true)
			manualStop.Schedulable = false
			disabled := groupQualityFallbackTestAccount(3, groupID, -100, true)
			disabled.Status = StatusDisabled
			gate := &groupQualityFallbackTestGate{recovery: true}
			svc := groupQualityFallbackService(mode, []Account{held, manualStop, disabled}, gate)
			baseRepo, ok := svc.accountRepo.(groupQualityFallbackTestRepo)
			require.True(t, ok)
			repo := &allGroupQualityFallbackCandidatesTestRepo{groupQualityFallbackTestRepo: baseRepo}
			svc.accountRepo = repo
			ctx, req := groupQualityAffinityRequest(&groupID, "public-model", "")
			selection, err := svc.selectGroupQualityFallback(ctx, req, ErrNoAvailableAccounts)
			require.NoError(t, err)
			require.True(t, selection.Acquired)
			require.Equal(t, held.ID, selection.Account.ID)
			require.Equal(t, 1, repo.allCalls)
			require.Zero(t, repo.ordinaryCalls, "fallback must use the broad optional query, not active-only ListByGroup")
			require.Len(t, gate.calls, 1)
			require.Equal(t, held.ID, gate.calls[0].AccountID)
			selection.ReleaseFunc()
		})
	}
}

package repository

import (
	"context"
	"testing"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func assertGroupQualityFallbackCandidateList(t *testing.T, client *dbent.Client, repo *accountRepository) {
	t.Helper()
	ctx := context.Background()
	group, err := client.Group.Create().SetName("quality-fallback-candidates").SetPlatform(service.PlatformOpenAI).Save(ctx)
	require.NoError(t, err)
	other, err := client.Group.Create().SetName("quality-fallback-other").SetPlatform(service.PlatformOpenAI).Save(ctx)
	require.NoError(t, err)
	ids := make(map[string]int64)
	for _, tt := range []struct {
		name, status string
		schedulable  bool
		groupID      int64
		deleted      bool
	}{
		{"normal", service.StatusActive, true, group.ID, false},
		{"automatic hold", "quality_paused", true, group.ID, false},
		{"manual scheduling stop", "quality_paused", false, group.ID, false},
		{"manual disabled", service.StatusDisabled, true, group.ID, false},
		{"other group", "quality_paused", true, other.ID, false},
		{"deleted hold", "quality_paused", true, group.ID, true},
	} {
		create := client.Account.Create().SetName(tt.name).SetPlatform(service.PlatformOpenAI).SetType(service.AccountTypeAPIKey).
			SetStatus(tt.status).SetSchedulable(tt.schedulable).SetConcurrency(3).SetCredentials(map[string]any{}).
			SetExtra(map[string]any{"quality_protection_reason": "automatic cache quality hold"})
		if tt.deleted {
			create.SetDeletedAt(time.Now())
		}
		account, err := create.Save(ctx)
		require.NoError(t, err)
		ids[tt.name] = account.ID
		_, err = client.AccountGroup.Create().SetAccountID(account.ID).SetGroupID(tt.groupID).Save(ctx)
		require.NoError(t, err)
	}
	normal, err := repo.ListByGroup(ctx, group.ID)
	require.NoError(t, err)
	require.Len(t, normal, 1, "ordinary list keeps its active-only behavior")
	require.Equal(t, ids["normal"], normal[0].ID)
	all, err := repo.ListGroupQualityFallbackCandidates(ctx, group.ID)
	require.NoError(t, err)
	byID := make(map[int64]service.Account, len(all))
	for _, account := range all {
		byID[account.ID] = account
		require.Contains(t, account.GroupIDs, group.ID)
	}
	require.Len(t, all, 4)
	require.Equal(t, "quality_paused", byID[ids["automatic hold"]].Status)
	require.True(t, byID[ids["automatic hold"]].Schedulable)
	require.False(t, byID[ids["manual scheduling stop"]].Schedulable, "candidate enumeration never clears manual stop")
	require.Equal(t, service.StatusDisabled, byID[ids["manual disabled"]].Status)
	require.NotContains(t, byID, ids["other group"])
	require.NotContains(t, byID, ids["deleted hold"])
}

func TestListGroupQualityFallbackCandidatesIncludesAutomaticHolds(t *testing.T) {
	_, client := newUserEntRepo(t)
	assertGroupQualityFallbackCandidateList(t, client, newAccountRepositoryWithSQL(client, nil, nil))
}

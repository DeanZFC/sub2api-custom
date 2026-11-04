//go:build integration

package repository

import (
	"github.com/stretchr/testify/require"
	"testing"
)

func TestAstraOwnedPauseSurvivesTicketWritesAndTokenRefresh(t *testing.T) {
	f := newAstraGroupFixture(t)
	f.settings.SchedulingMode = "account"
	f.settings.TicketPoolScheduling = true
	f.save()
	f.apply(false, true)
	owns := func(want bool) {
		state, err := f.repo.AstraHarvestRecoveryState(t.Context(), f.id)
		require.NoError(t, err)
		require.Equal(t, want, state.OwnedPause)
	}
	owns(true)
	require.NoError(t, f.repo.UpdateExtra(t.Context(), f.id, map[string]any{"codex_turn_ticket:gpt-6-astra": map[string]any{"state": "private-test-ticket"}}))
	owns(true)
	require.NoError(t, f.repo.UpdateExtra(t.Context(), f.id, map[string]any{"codex_turn_ticket:gpt-6-astra:probe": map[string]any{"result": "success"}}))
	owns(true)
	account, err := f.repo.GetByID(t.Context(), f.id)
	require.NoError(t, err)
	account.Credentials["access_token"] = "rotated-token"
	require.NoError(t, f.repo.UpdateCredentials(t.Context(), f.id, account.Credentials))
	owns(true)
	f.apply(true, true)
	owns(false)
	f.apply(false, true)
	f.exec(`UPDATE accounts SET name='manual-change',updated_at=clock_timestamp()+interval '1 second' WHERE id=$1`, f.id)
	owns(false)
	require.NoError(t, f.repo.UpdateExtra(t.Context(), f.id, map[string]any{"codex_turn_ticket:gpt-6-astra:probe": map[string]any{"result": "success"}}))
	owns(false)
	_, err = f.repo.ApplyAstraScheduling(t.Context(), f.id, f.settings, true)
	require.ErrorContains(t, err, "astra_restore_conflict")
}

func TestAstraRemovedGroupsRemainRecoverableOnlyWhileOwned(t *testing.T) {
	f := newAstraGroupFixture(t)
	f.settings.TicketPoolScheduling = true
	f.settings.SchedulingGroupIDs = []int64{f.other}
	f.save()
	f.apply(false, true)
	state, err := f.repo.AstraHarvestRecoveryState(t.Context(), f.id)
	require.NoError(t, err)
	require.Equal(t, []int64{f.other}, state.OriginalGroupIDs)
	f.exec(`INSERT INTO account_groups(account_id,group_id,priority) VALUES($1,$2,99)`, f.id, f.other)
	state, err = f.repo.AstraHarvestRecoveryState(t.Context(), f.id)
	require.NoError(t, err)
	require.Empty(t, state.OriginalGroupIDs)
}

package service

import (
	"context"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

type recoveryScopeRepo struct {
	AccountRepository
	state AstraHarvestRecoveryState
}

func (r *recoveryScopeRepo) AstraHarvestRecoveryState(context.Context, int64) (AstraHarvestRecoveryState, error) {
	return r.state, nil
}
func TestAstraHarvestRecoveryKeepsOwnedPausesInDefaultScope(t *testing.T) {
	gateway := ticketTestService(t, config.OpenAICodexTicketConfig{Enabled: true, Models: []string{"gpt-6-astra"}}, nil)
	settings := config.AstraRoutingSettings{TicketPoolScheduling: true, SchedulingMode: "account"}
	gateway.cfg.SetAstraRoutingLoader(func(context.Context) config.AstraRoutingSettings { return settings })
	repo := &recoveryScopeRepo{state: AstraHarvestRecoveryState{OwnedPause: true}}
	gateway.accountRepo = repo
	scope, _ := NormalizeCodexTicketHarvestScope(CodexTicketHarvestScope{})
	account := ticketTestAccount(1)
	account.Schedulable = false
	require.True(t, gateway.harvestScopeAllowsAccount(t.Context(), scope, account))
	repo.state.OwnedPause = false
	require.False(t, gateway.harvestScopeAllowsAccount(t.Context(), scope, account), "manual pauses remain excluded")
	repo.state.OwnedPause = true
	until := time.Now().Add(time.Hour)
	account.RateLimitResetAt = &until
	require.False(t, gateway.harvestScopeAllowsAccount(t.Context(), scope, account), "owned pauses never bypass cooldowns")
	account.RateLimitResetAt = nil
	settings.TicketPoolScheduling = false
	require.False(t, gateway.harvestScopeAllowsAccount(t.Context(), scope, account))
	settings.TicketPoolScheduling = true
	settings.SchedulingMode = "groups"
	account.Schedulable = true
	account.GroupIDs = nil
	scope.Mode = "selected"
	scope.GroupIDs = []int64{7}
	repo.state.OriginalGroupIDs = []int64{7}
	require.True(t, gateway.harvestScopeIncludesAccount(t.Context(), scope, account), "removed legacy scope membership must be recoverable")
	repo.state.OriginalGroupIDs = nil
	require.False(t, gateway.harvestScopeIncludesAccount(t.Context(), scope, account))
}

package service

import (
	"context"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"slices"
)

type AstraHarvestRecoveryState struct {
	OwnedPause       bool
	OriginalGroupIDs []int64
}
type AstraHarvestRecoveryRepository interface {
	AstraHarvestRecoveryState(context.Context, int64) (AstraHarvestRecoveryState, error)
}

func (s *OpenAIGatewayService) harvestScopeAllowsAccount(ctx context.Context, scope CodexTicketHarvestScope, account *Account) bool {
	if scope.allowsAccount(account) {
		return true
	}
	if s == nil || s.cfg == nil || account == nil || account.Schedulable {
		return false
	}
	settings := s.cfg.AstraRouting(ctx)
	if !settings.TicketPoolScheduling || settings.EffectiveSchedulingMode() != "account" {
		return false
	}
	candidate := *account
	candidate.Schedulable = true
	if !scope.allowsAccount(&candidate) {
		return false
	}
	repo, ok := s.accountRepo.(AstraHarvestRecoveryRepository)
	if !ok {
		return false
	}
	state, err := repo.AstraHarvestRecoveryState(ctx, account.ID)
	return err == nil && state.OwnedPause
}
func (s *OpenAIGatewayService) harvestScopeIncludesAccount(ctx context.Context, scope CodexTicketHarvestScope, account *Account, known ...config.AstraRoutingSettings) bool {
	if scope.includes(account) {
		return true
	}
	if s == nil || s.cfg == nil || account == nil || scope.Mode != "selected" {
		return false
	}
	var settings config.AstraRoutingSettings
	if len(known) > 0 {
		settings = known[0]
	} else {
		settings = s.cfg.AstraRouting(ctx)
	}
	if !settings.TicketPoolScheduling || settings.EffectiveSchedulingMode() != "groups" {
		return false
	}
	repo, ok := s.accountRepo.(AstraHarvestRecoveryRepository)
	if !ok {
		return false
	}
	state, err := repo.AstraHarvestRecoveryState(ctx, account.ID)
	if err != nil {
		return false
	}
	for _, id := range state.OriginalGroupIDs {
		if slices.Contains(scope.GroupIDs, id) {
			return true
		}
	}
	return false
}

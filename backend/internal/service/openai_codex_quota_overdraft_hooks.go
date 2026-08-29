package service

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
)

func codexQuotaOverdraftBypassesSchedulingThreshold(ctx context.Context, account *Account) bool {
	return codexQuotaOverdraftSchedulingEnabled(ctx) && isCodexQuotaOverdraftAccount(account) &&
		codexQuotaOverdraftSchedulingAllowed(account, time.Now().UTC())
}

func (s *RateLimitService) notifyCodexQuotaOverdraftAwareSchedulingBlock(
	account *Account,
	until time.Time,
) {
	if !CodexQuotaOverdraftEnabled() || !isCodexQuotaOverdraftAccount(account) {
		s.notifyAccountSchedulingBlocked(account, until, "account_scheduling_threshold")
	}
}

func (s *OpenAIGatewayService) listCodexQuotaOverdraftSchedulableAccounts(
	ctx context.Context,
	groupID *int64,
	platform string,
) ([]Account, bool, error) {
	if !CodexQuotaOverdraftSchedulingEnabled(ctx) || platform != PlatformOpenAI || s.accountRepo == nil {
		return nil, false, nil
	}
	var accounts []Account
	var err error
	if s.cfg != nil && s.cfg.RunMode == config.RunModeSimple {
		accounts, err = s.accountRepo.ListSchedulableByPlatform(ctx, platform)
	} else if groupID != nil {
		accounts, err = s.accountRepo.ListSchedulableByGroupIDAndPlatform(ctx, *groupID, platform)
	} else {
		accounts, err = s.accountRepo.ListSchedulableUngroupedByPlatform(ctx, platform)
	}
	if err != nil {
		return nil, true, fmt.Errorf("query overdraft accounts failed: %w", err)
	}
	accounts = normalizeCodexQuotaOverdraftAccountsForScheduling(ctx, accounts)
	return s.filterOpenAIAccountsBySchedulingThreshold(ctx, accounts), true, nil
}

func (s *OpenAIGatewayService) handleCodexQuotaOverdraftUpstream429(
	ctx context.Context,
	account *Account,
	statusCode int,
	headers http.Header,
	responseBody []byte,
	canonicalModel []string,
) bool {
	if statusCode != http.StatusTooManyRequests || s.codexQuotaOverdraft == nil {
		return false
	}
	preferredModel := ""
	if len(canonicalModel) > 0 {
		preferredModel = canonicalModel[0]
	}
	return s.codexQuotaOverdraft.HandleQuota429(ctx, account, headers, responseBody, preferredModel)
}

func (s *OpenAIGatewayService) processCodexQuotaOverdraftUsageSnapshot(
	ctx context.Context,
	accountID int64,
	now time.Time,
	updates map[string]any,
) {
	persistSnapshot := codexQuotaOverdraftSnapshotPrearmReached(updates) || s.getCodexSnapshotThrottle().Allow(accountID, now)
	businessSuccess := codexQuotaOverdraftWasInjected(ctx, accountID)

	go func() {
		updateCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if s.accountRepo == nil {
			return
		}
		var account *Account
		current, err := s.accountRepo.GetByID(updateCtx, accountID)
		if err == nil {
			account = current
		}
		// Account-level disable skips only overdraft state/coordinator work. The
		// ordinary Codex usage snapshot must still be persisted for the UI.
		if !isCodexQuotaOverdraftAccount(account) {
			if persistSnapshot {
				if err := s.accountRepo.UpdateExtra(updateCtx, accountID, updates); err == nil {
					notifyOpenAIAutoReset(accountID)
				}
			}
			return
		}
		if !persistSnapshot && s.codexQuotaOverdraft != nil {
			if account != nil {
				state, hasState := codexQuotaOverdraftStateFromAccount(account)
				_, wasExhausted := codexQuotaOverdraftSignalFromAccount(account, state, now)
				persistSnapshot = wasExhausted || hasState && state.Status != codexQuotaOverdraftProbeRecovered
			}
		}
		if persistSnapshot {
			if err := s.accountRepo.UpdateExtra(updateCtx, accountID, updates); err != nil {
				return
			}
			notifyOpenAIAutoReset(accountID)
		}
		if s.codexQuotaOverdraft == nil {
			return
		}
		if account == nil {
			current, err := s.accountRepo.GetByID(updateCtx, accountID)
			if err != nil || current == nil {
				return
			}
			account = current
		}
		mergeAccountExtra(account, updates)
		if businessSuccess {
			s.codexQuotaOverdraft.observeBusinessSuccess(account, "")
		} else {
			s.codexQuotaOverdraft.observeAccount(account, "")
		}
	}()
}

func (s *OpenAIGatewayService) observeCodexQuotaOverdraftScheduleSuccess(
	accountID int64,
	model string,
	requestCtx []context.Context,
) {
	if len(requestCtx) > 0 && s.codexQuotaOverdraft != nil && codexQuotaOverdraftWasInjected(requestCtx[0], accountID) {
		s.codexQuotaOverdraft.ObserveBusinessSuccessByID(accountID, model)
	}
}

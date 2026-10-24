package service

import (
	"context"
	"errors"
	"sort"
	"strings"
)

// GroupQualityFallbackRequest identifies one exact automatic-quality recovery
// candidate. The repository must recheck live supply and reserve a per-request
// tracking lease; the marker alone never authorizes traffic.
type GroupQualityFallbackRequest struct {
	GroupID, AccountID int64
	Model              string
}

type groupQualityFallbackContextKey struct{}

func WithGroupQualityFallback(ctx context.Context, req GroupQualityFallbackRequest) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, groupQualityFallbackContextKey{}, req)
}

func GroupQualityFallbackFromContext(ctx context.Context) (GroupQualityFallbackRequest, bool) {
	if ctx == nil {
		return GroupQualityFallbackRequest{}, false
	}
	req, ok := ctx.Value(groupQualityFallbackContextKey{}).(GroupQualityFallbackRequest)
	return req, ok && req.GroupID > 0 && req.AccountID > 0 && req.Model != ""
}

func withoutGroupQualityFallback(ctx context.Context) context.Context {
	if _, ok := GroupQualityFallbackFromContext(ctx); !ok {
		return ctx
	}
	return WithGroupQualityFallback(ctx, GroupQualityFallbackRequest{})
}

// WithoutGroupQualityFallback retains pricing and request cancellation while
// starting an ordinary admission attempt on a previously recovering WS owner.
func WithoutGroupQualityFallback(ctx context.Context) context.Context {
	return withoutGroupQualityFallback(ctx)
}

// Only a request-local copy is relaxed. Manual schedulable=false, arbitrary
// status changes, credential cooldowns, quota and rate limits stay untouched.
// The live repository must prove automatic hold provenance before admitting it.
func groupQualityFallbackAccount(ctx context.Context, account *Account, requestedModel string) *Account {
	req, ok := GroupQualityFallbackFromContext(ctx)
	if !ok || account == nil || req.AccountID != account.ID || req.Model != passiveQualityRoutingModel(ctx, requestedModel) || !openAIStickyAccountMatchesGroup(account, &req.GroupID) {
		return account
	}
	adjusted := *account
	if adjusted.Schedulable && adjusted.Status == "quality_paused" {
		if reason, _ := adjusted.Extra["quality_protection_reason"].(string); strings.TrimSpace(reason) != "" {
			adjusted.Status = StatusActive
		}
	}
	holds, _ := account.Extra["passive_quality"].([]any)
	kept := make([]any, 0, len(holds))
	for _, raw := range holds {
		hold, ok := raw.(map[string]any)
		if !ok {
			kept = append(kept, raw)
			continue
		}
		phase, _ := hold["phase"].(string)
		model, _ := hold["model"].(string)
		blocking := phase == "paused" || phase == "probing" || phase == "trial"
		if blocking && parseExtraInt(hold["policy_id"]) > 0 && (hold["scope"] == "account" || model == req.Model) {
			continue
		}
		kept = append(kept, raw)
	}
	if len(kept) != len(holds) {
		adjusted.Extra = make(map[string]any, len(account.Extra))
		for key, value := range account.Extra {
			adjusted.Extra[key] = value
		}
		adjusted.Extra["passive_quality"] = kept
	}
	return &adjusted
}

func groupQualityAutomaticHold(ctx context.Context, account *Account, groupID *int64, requestedModel string) bool {
	if account == nil || groupID == nil || *groupID <= 0 || !account.IsOpenAI() {
		return false
	}
	model := passiveQualityRoutingModel(ctx, requestedModel)
	if account.Status != "quality_paused" && !account.PassiveQualityBlocked(model) {
		return false
	}
	probe := WithGroupQualityFallback(ctx, GroupQualityFallbackRequest{GroupID: *groupID, AccountID: account.ID, Model: model})
	adjusted := groupQualityFallbackAccount(probe, account, requestedModel)
	return adjusted.IsSchedulableForModelWithContext(probe, requestedModel)
}

func groupQualityFallbackRetryable(err error) bool {
	return errors.Is(err, ErrNoAvailableAccounts) || errors.Is(err, ErrNoAvailableCompactAccounts) || errors.Is(err, ErrAccountAdmissionDenied) || errors.Is(err, ErrGroupQualityTrafficBudget)
}

// Recovery is a second, acquired-only pass. It never substitutes a wait plan,
// changes persistent account state, or replays incremental response state on
// another credential. Ranking uses the scheduler's existing score.
func (s *OpenAIGatewayService) selectGroupQualityFallback(ctx context.Context, req OpenAIAccountScheduleRequest, originalErr error) (*AccountSelectionResult, error) {
	if s == nil || s.accountRepo == nil || s.concurrencyService == nil || !groupQualityFallbackRetryable(originalErr) || NormalizeOpenAICompatiblePlatform(req.Platform) != PlatformOpenAI || req.RequiredImageCapability != "" || req.GroupID == nil || *req.GroupID <= 0 {
		return nil, originalErr
	}
	if _, ok := s.concurrencyService.passiveQualityGate.(GroupQualityTrafficGate); !ok {
		return nil, originalErr
	}
	if _, ok := s.concurrencyService.passiveQualityGate.(GroupQualityFallbackLeaseGate); !ok {
		return nil, originalErr
	}
	traffic, ok := GroupQualityTrafficRequestFromContext(ctx)
	if !ok || traffic.GroupID != *req.GroupID {
		return nil, originalErr
	}
	ctx = withPassiveQualityRoutingModel(withoutGroupQualityFallback(ctx), req.RequestedModel)
	budgetDenied := errors.Is(originalErr, ErrGroupQualityTrafficBudget)
	ctx = s.withOpenAIProfitControlGate(ctx, req.GroupID)
	ctx = s.withOpenAIGroupPrivacyRequirement(ctx, req.GroupID)
	if s.checkChannelPricingRestriction(ctx, req.GroupID, req.RequestedModel) {
		return nil, originalErr
	}
	owner := int64(0)
	if strings.TrimSpace(req.PreviousResponseID) != "" && !req.PreviousResponseCanMove {
		store := s.getOpenAIWSStateStore()
		if store == nil {
			return nil, originalErr
		}
		owner, _ = store.GetResponseAccount(ctx, *req.GroupID, strings.TrimSpace(req.PreviousResponseID))
		if owner <= 0 {
			return nil, originalErr
		}
	}
	accounts, err := s.accountRepo.ListByGroup(ctx, *req.GroupID)
	if err != nil {
		return nil, err
	}
	scheduler, _ := s.getOpenAIAccountScheduler(ctx).(*defaultOpenAIAccountScheduler)
	if scheduler == nil {
		scheduler = &defaultOpenAIAccountScheduler{service: s, stats: newOpenAIAccountRuntimeStats()}
	}
	req.RequirePrivacySet = s.openAIGroupRequiresPrivacySet(ctx, req.GroupID)
	candidates := make([]*Account, 0, len(accounts))
	for i := range accounts {
		account := &accounts[i]
		if _, excluded := req.ExcludedIDs[account.ID]; excluded {
			continue
		}
		if !s.openAIAccountMatchesSchedulingGroup(account, req.GroupID) {
			continue
		}
		marker := GroupQualityFallbackRequest{GroupID: *req.GroupID, AccountID: account.ID, Model: passiveQualityRoutingModel(ctx, req.RequestedModel)}
		candidateCtx := WithGroupQualityFallback(ctx, marker)
		adjusted := groupQualityFallbackAccount(candidateCtx, account, req.RequestedModel)
		if !isOpenAICompatibleAccountEligibleForRequest(candidateCtx, adjusted, req.Platform, req.RequestedModel, req.RequireCompact, req.RequiredCapability) || !scheduler.isAccountRequestCompatible(candidateCtx, adjusted, req) || !scheduler.isAccountTransportCompatible(adjusted, req.RequiredTransport) || s.isOpenAIAccountBlockedBySchedulingThreshold(candidateCtx, adjusted) {
			continue
		}
		if verified, _ := groupQualityRoutingState(account, req.GroupID, marker.Model); verified && !groupQualityAutomaticHold(ctx, account, req.GroupID, req.RequestedModel) {
			return nil, originalErr
		}
		if owner > 0 && account.ID != owner {
			continue
		}
		_, managed := groupQualityRoutingState(account, req.GroupID, marker.Model)
		if !managed && !budgetDenied && !groupQualityAutomaticHold(ctx, account, req.GroupID, req.RequestedModel) {
			continue
		}
		candidates = append(candidates, passiveQualitySchedulingAccount(candidateCtx, adjusted, req.RequestedModel))
	}
	if len(candidates) == 0 {
		return nil, originalErr
	}
	loadMap, err := s.concurrencyService.GetAccountsLoadBatch(ctx, buildOpenAIAccountLoadRequest(candidates))
	if err != nil {
		loadMap = nil
	}
	plan := scheduler.buildOpenAIAccountLoadPlan(ctx, req, candidates, loadMap)
	ranked := append([]openAIAccountCandidateScore(nil), plan.candidates...)
	sort.SliceStable(ranked, func(i, j int) bool { return isOpenAIAccountCandidateBetter(ranked[i], ranked[j]) })
	budget := newOpenAISelectionProbeBudget()
	budget.enableLimit()
	if req.SessionHash != "" && s.cache != nil {
		if id, lookupErr := s.getStickySessionAccountID(ctx, req.GroupID, req.SessionHash); lookupErr == nil && id > 0 {
			req.PreserveStickyBinding = true
		}
	}
	req.PreserveStickyBinding = req.PreserveStickyBinding || owner > 0
	for _, candidate := range ranked {
		candidateCtx := WithGroupQualityFallback(ctx, GroupQualityFallbackRequest{GroupID: *req.GroupID, AccountID: candidate.account.ID, Model: passiveQualityRoutingModel(ctx, req.RequestedModel)})
		selection, _, acquireErr := scheduler.tryAcquireOpenAISelectionOrderWithBudget(candidateCtx, req, []openAIAccountCandidateScore{candidate}, budget)
		if acquireErr != nil {
			return nil, acquireErr
		}
		if selection != nil {
			return selection, nil
		}
	}
	return nil, ErrGroupQualityTrafficBudget
}

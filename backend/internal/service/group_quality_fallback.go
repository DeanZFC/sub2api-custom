package service

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"
)

const (
	groupQualityFallbackAffinityTTL  = 30 * time.Minute
	groupQualityFallbackScoreTimeout = time.Second
)

// Use a separate namespace from normal sticky ownership. A fallback is only a
// temporary route, and must not replace the conversation's original account.
// An explicit client session is only meaningful inside its authenticated key.
func groupQualityFallbackAffinityKey(apiKeyID, groupID int64, model, sessionHash string) string {
	if apiKeyID <= 0 || groupID <= 0 || model == "" || strings.TrimSpace(sessionHash) == "" {
		return ""
	}
	digest := sha256.Sum256([]byte(fmt.Sprintf("%d:%s%s", len(model), model, sessionHash)))
	return fmt.Sprintf("openai:group-quality-fallback:v1:%d:%d:%x", apiKeyID, groupID, digest)
}

// GroupQualityFallbackRequest identifies one exact automatic-quality recovery
// candidate. The repository must recheck live supply and reserve a per-request
// tracking lease; the marker alone never authorizes traffic.
type GroupQualityFallbackRequest struct {
	GroupID, AccountID int64
	Model              string
	supply             *groupQualityFallbackSupplyScope
}

type groupQualityFallbackContextKey struct{}

type groupQualityFallbackSupplyScope struct {
	check          func(context.Context, []*Account) (bool, error)
	requestedModel string
	publicModel    string
}

// GroupQualityFallbackAdmissionModel keeps capability/model gates on the
// scheduler's forwarded model while protection and leases retain public identity.
func GroupQualityFallbackAdmissionModel(ctx context.Context, model string) string {
	if ctx != nil {
		if current, _ := ctx.Value(groupQualityFallbackAdmissionModelContextKey{}).(string); current != "" {
			return current
		}
	}
	if req, ok := GroupQualityFallbackFromContext(ctx); ok && req.supply != nil && req.supply.requestedModel != "" && req.Model == req.supply.publicModel {
		return req.supply.requestedModel
	}
	return model
}

type groupQualityFallbackAdmissionModelContextKey struct{}

// WithGroupQualityFallbackAdmissionModel freezes the forwarded model for one
// admission attempt without changing the public protection or lease identity.
func WithGroupQualityFallbackAdmissionModel(ctx context.Context, model string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, groupQualityFallbackAdmissionModelContextKey{}, strings.TrimSpace(model))
}

// WithGroupQualityFallbackSupplyChecker carries request constraints to the live
// repository check. Accounts passed to the checker must come from its locked
// membership and protection query, rather than a scheduler snapshot.
func WithGroupQualityFallbackSupplyChecker(ctx context.Context, check func(context.Context, []*Account) (bool, error)) context.Context {
	req, ok := GroupQualityFallbackFromContext(ctx)
	if !ok || check == nil {
		return ctx
	}
	req.supply = &groupQualityFallbackSupplyScope{check: check}
	return WithGroupQualityFallback(ctx, req)
}

// GroupQualityFallbackHasRequestSupply checks whether live unrestricted
// accounts can serve this request now. Unscoped internal callers retain the
// conservative group-wide supply check.
func GroupQualityFallbackHasRequestSupply(ctx context.Context, accounts []*Account) (bool, error) {
	req, ok := GroupQualityFallbackFromContext(ctx)
	if ok && req.supply != nil && req.supply.check != nil {
		return req.supply.check(ctx, accounts)
	}
	return len(accounts) > 0, nil
}

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
	if groupQualityRequiresActiveRecovery(account, req.Model) {
		return account
	}
	adjusted := *account
	adjusted.Extra = GroupQualityFallbackExtra(account.Extra, req.Model)
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
		adjusted.Extra["passive_quality"] = kept
	}
	return &adjusted
}

// GroupQualityFallbackExtra copies only automatic capability projections for a
// single request. Callers at the database boundary must first prove a live hold.
// Neither the account snapshot nor any persistent protection state is changed.
func GroupQualityFallbackExtra(extra map[string]any, model string) map[string]any {
	copy := make(map[string]any, len(extra))
	for key, value := range extra {
		copy[key] = value
	}
	delete(copy, "active_test_all_models_paused")
	for _, key := range []string{"active_test_model_holds", "active_pass_rate_holds"} {
		holds, ok := extra[key].(map[string]any)
		if !ok {
			continue
		}
		kept := make(map[string]any, len(holds))
		for heldModel, value := range holds {
			if heldModel != "" && heldModel != model {
				kept[heldModel] = value
			}
		}
		copy[key] = kept
	}
	return copy
}

func groupQualityAutomaticHold(ctx context.Context, account *Account, groupID *int64, requestedModel string) bool {
	if account == nil || groupID == nil || *groupID <= 0 || !account.IsOpenAI() {
		return false
	}
	model := passiveQualityRoutingModel(ctx, requestedModel)
	// An open trial is still recovering even when it can accept a limited
	// request. It must not be mistaken for unrestricted normal supply.
	trial, _ := account.Extra["quality_protection_trial"].(bool)
	holds, _ := account.Extra["passive_quality"].([]any)
	for _, raw := range holds {
		hold, ok := raw.(map[string]any)
		if ok && hold["phase"] == "trial" && (hold["scope"] == "account" || hold["model"] == model) {
			trial = true
		}
	}
	if account.Status != "quality_paused" && !trial && !account.PassiveQualityBlocked(model) && !account.ActiveTestModelBlocked(model) {
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
// another credential. A session's previous fallback is preferred while it is
// eligible; first selection and overflow prefer independent quality scores,
// with the scheduler's existing order retained for ties and unknown scores.
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
	if gate, ok := s.concurrencyService.passiveQualityGate.(GroupQualityActiveRecoveryGate); ok {
		active, err := gate.GroupQualityActiveRecoveryEnabled(ctx, *req.GroupID, passiveQualityRoutingModel(ctx, req.RequestedModel))
		if err != nil {
			return nil, err
		}
		if active {
			return nil, originalErr
		}
	}
	var profitErr error
	ctx, profitErr = s.withGroupQualityFallbackProfitControl(ctx, req.GroupID)
	if profitErr != nil {
		return nil, profitErr
	}
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
	var accounts []Account
	var err error
	if repo, ok := s.accountRepo.(interface {
		ListGroupQualityFallbackCandidates(context.Context, int64) ([]Account, error)
	}); ok {
		accounts, err = repo.ListGroupQualityFallbackCandidates(ctx, *req.GroupID)
	} else {
		accounts, err = s.accountRepo.ListByGroup(ctx, *req.GroupID)
	}
	if err != nil {
		return nil, err
	}
	scheduler, _ := s.getOpenAIAccountScheduler(ctx).(*defaultOpenAIAccountScheduler)
	if scheduler == nil {
		scheduler = &defaultOpenAIAccountScheduler{service: s, stats: newOpenAIAccountRuntimeStats()}
	}
	req.RequirePrivacySet = s.openAIGroupRequiresPrivacySet(ctx, req.GroupID)
	candidates := make([]*Account, 0, len(accounts))
	normal := make([]*Account, 0, len(accounts))
	supplyCheck := s.groupQualityFallbackSupplyChecker(ctx, scheduler, req, owner)
	supplyScope := &groupQualityFallbackSupplyScope{check: supplyCheck, requestedModel: req.RequestedModel, publicModel: passiveQualityRoutingModel(ctx, req.RequestedModel)}
	for i := range accounts {
		account := &accounts[i]
		if _, excluded := req.ExcludedIDs[account.ID]; excluded {
			continue
		}
		if !s.openAIAccountMatchesSchedulingGroup(account, req.GroupID) {
			continue
		}
		if owner > 0 && account.ID != owner {
			continue
		}
		marker := GroupQualityFallbackRequest{GroupID: *req.GroupID, AccountID: account.ID, Model: passiveQualityRoutingModel(ctx, req.RequestedModel)}
		candidateCtx := WithGroupQualityFallback(ctx, marker)
		adjusted := groupQualityFallbackAccount(candidateCtx, account, req.RequestedModel)
		if !isOpenAICompatibleAccountEligibleForRequest(candidateCtx, adjusted, req.Platform, req.RequestedModel, req.RequireCompact, req.RequiredCapability) || !scheduler.isAccountRequestCompatible(candidateCtx, adjusted, req) || !scheduler.isAccountTransportCompatible(adjusted, req.RequiredTransport) || s.isOpenAIAccountBlockedBySchedulingThreshold(candidateCtx, adjusted) {
			continue
		}
		// Unrestricted accounts stay on normal routing. Only normal capacity that
		// can serve this exact request may prevent recovery fallback.
		if !groupQualityAutomaticHold(ctx, account, req.GroupID, req.RequestedModel) {
			normal = append(normal, account)
			continue
		}
		candidates = append(candidates, passiveQualitySchedulingAccount(candidateCtx, adjusted, req.RequestedModel))
	}
	if len(candidates) == 0 {
		return nil, originalErr
	}
	if available, checkErr := supplyCheck(ctx, normal); checkErr != nil {
		return nil, checkErr
	} else if available {
		return nil, originalErr
	}
	loadMap, err := s.concurrencyService.GetAccountsLoadBatchFresh(ctx, buildOpenAIAccountLoadRequest(candidates))
	if err != nil {
		loadMap = nil
	}
	plan := scheduler.buildOpenAIAccountLoadPlan(ctx, req, candidates, loadMap)
	ranked := append([]openAIAccountCandidateScore(nil), plan.candidates...)
	sort.SliceStable(ranked, func(i, j int) bool { return isOpenAIAccountCandidateBetter(ranked[i], ranked[j]) })
	model := passiveQualityRoutingModel(ctx, req.RequestedModel)
	qualityRanked := s.rankGroupQualityFallbackByScore(ctx, ranked, model)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	affinityKey := ""
	userAffinity := openAIUserAffinityFromContext(ctx)
	if userAffinity == nil {
		if apiKey, authenticated := APIKeyForUsageContext(ctx); authenticated {
			affinityKey = groupQualityFallbackAffinityKey(apiKey.ID, *req.GroupID, model, req.SessionHash)
		}
	}
	affinityAccountID, preferredAccountID := int64(0), int64(0)
	affinityEscapeReason := ""
	if affinityKey != "" && s.cache != nil {
		if id, lookupErr := s.cache.GetSessionAccountID(ctx, *req.GroupID, affinityKey); lookupErr == nil && id > 0 {
			affinityAccountID = id
			for i, candidate := range ranked {
				if candidate.account.ID != id {
					continue
				}
				// TTFT is a soft score in recovery and must not undo affinity on
				// every slow turn. The advanced scheduler's runtime error stats
				// still remove preference, without excluding the last supply.
				// Legacy schedulers retain their existing stateless scoring.
				escapeCfg := s.openAIStickyEscapeConfig()
				if escapeCfg.enabled && candidate.errorRate > escapeCfg.errorRate {
					affinityEscapeReason = "error_rate"
					break
				}
				preferredAccountID = id
				copy(ranked[1:i+1], ranked[:i])
				ranked[0] = candidate
				break
			}
		}
	}
	if userAffinity != nil {
		// The authenticated-user affinity is the durable owner across sessions.
		// Prefer it during emergency recovery when still eligible; per-session
		// fallback affinity must not create a second, API-key-scoped owner.
		preferredOwner := userAffinity.sessionAccountID
		if preferredOwner <= 0 {
			preferredOwner = userAffinity.userAccountID
		}
		for i, candidate := range ranked {
			if candidate.account.ID == preferredOwner {
				escapeCfg := s.openAIStickyEscapeConfig()
				if escapeCfg.enabled && candidate.errorRate > escapeCfg.errorRate {
					affinityEscapeReason = "error_rate"
					markOpenAIUserAffinityMigration(ctx, preferredOwner)
					break
				}
				preferredAccountID = preferredOwner
				copy(ranked[1:i+1], ranked[:i])
				ranked[0] = candidate
				break
			}
		}
	}
	budget := newOpenAISelectionProbeBudget()
	budget.enableLimit()
	if req.SessionHash != "" && s.cache != nil {
		if id, lookupErr := s.getStickySessionAccountID(ctx, req.GroupID, req.SessionHash); lookupErr == nil && id > 0 {
			confirmedMigration := userAffinity != nil && userAffinity.migrationFrom == id
			req.PreserveStickyBinding = req.PreserveStickyBinding || !confirmedMigration
		}
	}
	req.PreserveStickyBinding = req.PreserveStickyBinding || owner > 0
	for _, candidate := range ranked {
		candidateCtx := WithGroupQualityFallback(ctx, GroupQualityFallbackRequest{GroupID: *req.GroupID, AccountID: candidate.account.ID, Model: model, supply: supplyScope})
		selection, _, acquireErr := scheduler.tryAcquireOpenAISelectionOrderWithBudget(candidateCtx, req, []openAIAccountCandidateScore{candidate}, budget)
		if acquireErr != nil {
			return nil, acquireErr
		}
		if selection != nil {
			accountID := selection.Account.ID
			if affinityKey != "" && s.cache != nil {
				if affinityAccountID == accountID {
					// Refresh only: an overlapping request may already have moved
					// affinity after a failure; do not overwrite it on this hit.
					_ = s.cache.RefreshSessionTTL(ctx, *req.GroupID, affinityKey, groupQualityFallbackAffinityTTL)
				} else {
					// GatewayCache has no CAS. Concurrent first selections are
					// best-effort; every later hit still passes all live gates.
					_ = s.cache.SetSessionAccountID(ctx, *req.GroupID, affinityKey, accountID, groupQualityFallbackAffinityTTL)
				}
			}
			source := "score"
			rankingSource := "scheduler"
			if qualityRanked {
				rankingSource = "quality_score"
			}
			if owner > 0 {
				source = "previous_response_owner"
			} else if accountID == preferredAccountID {
				source = "affinity"
			}
			slog.Info("group_quality_fallback_selected", "group_id", *req.GroupID, "account_id", accountID, "model", model,
				"affinity_hit", accountID == preferredAccountID, "selection_source", source, "ranking_source", rankingSource, "affinity_escape_reason", affinityEscapeReason)
			return selection, nil
		}
	}
	// An unsuccessful emergency pass must not hide a real ordinary recovery
	// budget denial: API-key fallback groups may still serve that request.
	// Pure concurrency exhaustion never creates this original budget error.
	if errors.Is(originalErr, ErrGroupQualityTrafficBudget) {
		return nil, originalErr
	}
	if len(budget.trafficDenied) > 0 {
		return nil, ErrGroupQualityTrafficBudget
	}
	if len(budget.denied) > 0 {
		return nil, ErrAccountAdmissionDenied
	}
	return nil, ErrNoAvailableAccounts
}

func (s *OpenAIGatewayService) groupQualityFallbackSupplyChecker(ctx context.Context, scheduler *defaultOpenAIAccountScheduler, req OpenAIAccountScheduleRequest, owner int64) func(context.Context, []*Account) (bool, error) {
	// Read configuration before the repository locks account rows. The checker
	// must not invoke ApplyAccountSchedulingThreshold, which writes those rows.
	var thresholds map[string]int
	if s.rateLimitService != nil && s.rateLimitService.settingService != nil {
		thresholds = s.rateLimitService.settingService.GetAccountSchedulingThresholds(ctx)
	}
	initialTraffic, _ := GroupQualityTrafficRequestFromContext(ctx)
	initialPublicModel := passiveQualityRoutingModel(ctx, req.RequestedModel)
	// A caller may reuse its exclusion map for the next attempt or WS turn.
	excluded := make(map[int64]struct{}, len(req.ExcludedIDs))
	for id := range req.ExcludedIDs {
		excluded[id] = struct{}{}
	}
	req.ExcludedIDs = excluded
	return func(ctx context.Context, accounts []*Account) (bool, error) {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		currentReq, currentOwner := req, owner
		if traffic, ok := GroupQualityTrafficRequestFromContext(ctx); ok {
			if traffic.Model != "" && traffic.Model != initialPublicModel {
				currentReq.RequestedModel = traffic.Model
			}
			if traffic.Key != initialTraffic.Key {
				// Later WS turns remain bound to the established upstream owner.
				if fallback, marked := GroupQualityFallbackFromContext(ctx); marked {
					currentOwner = fallback.AccountID
				}
			}
		}
		currentReq.RequestedModel = GroupQualityFallbackAdmissionModel(ctx, currentReq.RequestedModel)
		ctx = withPassiveQualityRoutingModel(withoutGroupQualityFallback(ctx), currentReq.RequestedModel)
		eligible := make([]*Account, 0, len(accounts))
		for _, account := range accounts {
			if account == nil || (currentOwner > 0 && account.ID != currentOwner) {
				continue
			}
			if _, excluded := req.ExcludedIDs[account.ID]; excluded {
				continue
			}
			if !s.openAIAccountMatchesSchedulingGroup(account, currentReq.GroupID) ||
				!isOpenAICompatibleAccountEligibleForRequest(ctx, account, currentReq.Platform, currentReq.RequestedModel, currentReq.RequireCompact, currentReq.RequiredCapability) ||
				!scheduler.isAccountRequestCompatible(ctx, account, currentReq) || !scheduler.isAccountTransportCompatible(account, currentReq.RequiredTransport) {
				continue
			}
			if decision := EvaluateAccountSchedulingThreshold(account, thresholds, time.Now()); decision.ShouldPause && decision.Until != nil && decision.Until.After(time.Now()) {
				continue
			}
			adjusted := passiveQualitySchedulingAccount(ctx, account, currentReq.RequestedModel)
			if len(account.ProxyIDs) > 1 {
				active := 0
				now := time.Now()
				for _, id := range account.ProxyIDs {
					for _, proxy := range account.Proxies {
						if proxy != nil && proxy.ID == id && proxy.IsActive() && !proxy.IsExpired(now) {
							active++
							break
						}
					}
				}
				if active == 0 {
					continue
				}
				// Only active, unexpired egress routes contribute normal capacity.
				copy := *adjusted
				copy.qualityConcurrencyLimit = min(adjusted.TotalConcurrency(), active*account.Concurrency)
				adjusted = &copy
			}
			if account.Concurrency <= 0 {
				return true, nil
			}
			eligible = append(eligible, adjusted)
		}
		if len(eligible) == 0 {
			return false, nil
		}
		loads, err := s.concurrencyService.GetAccountsLoadBatchFresh(ctx, buildOpenAIAccountLoadRequest(eligible))
		if ctxErr := ctx.Err(); ctxErr != nil {
			return false, ctxErr
		}
		if err != nil {
			// Unknown peer capacity cannot veto an acquired emergency candidate.
			// Its own slot still goes through atomic acquisition and live gates.
			return false, nil
		}
		for _, account := range eligible {
			if load := loads[account.ID]; load != nil && load.CurrentConcurrency < account.TotalConcurrency() {
				return true, nil
			}
		}
		return false, nil
	}
}

// Ranking only changes preference. Unknown evidence remains eligible, and all
// candidates still pass the same concurrency acquisition and live admission.
// Starting with the scheduler's order preserves its tie-breaks in both sets.
func (s *OpenAIGatewayService) rankGroupQualityFallbackByScore(ctx context.Context, ranked []openAIAccountCandidateScore, model string) bool {
	provider, ok := s.concurrencyService.passiveQualityGate.(ChannelQualityScoreProvider)
	if !ok || len(ranked) == 0 {
		return false
	}
	ids := make([]int64, 0, len(ranked))
	for _, candidate := range ranked {
		ids = append(ids, candidate.account.ID)
	}
	scoreCtx, cancel := context.WithTimeout(ctx, groupQualityFallbackScoreTimeout)
	scores, err := provider.ChannelQualityScores(scoreCtx, ids, model)
	cancel()
	if err != nil {
		slog.Warn("group_quality_fallback_score_unavailable", "model", model, "error", err)
		return false
	}
	known := make([]openAIAccountCandidateScore, 0, len(ranked))
	unknown := make([]openAIAccountCandidateScore, 0, len(ranked))
	for _, candidate := range ranked {
		score := scores[candidate.account.ID]
		// Scoring is one global configuration. An explicitly disabled summary
		// means the existing scheduler owns the complete ordering again.
		if score != nil && score.Status == "disabled" {
			return false
		}
		if score != nil && score.IsRankable() {
			known = append(known, candidate)
		} else {
			unknown = append(unknown, candidate)
		}
	}
	if len(known) == 0 {
		return false
	}
	sort.SliceStable(known, func(i, j int) bool {
		return *scores[known[i].account.ID].Value > *scores[known[j].account.ID].Value
	})
	copy(ranked, known)
	copy(ranked[len(known):], unknown)
	return true
}

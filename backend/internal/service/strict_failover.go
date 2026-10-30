package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

// This error deliberately does not wrap ErrNoAvailableAccounts: a failed
// selection or an exhausted probe budget is not proof that a tier is down.
var ErrStrictFailoverUnconfirmed = errors.New("strict failover: primary tier availability is unconfirmed")

// Only this typed result can authorize external group fallback in strict mode.
var errStrictFailoverAllUnavailable = fmt.Errorf("%w: all strict failover tiers explicitly unavailable", ErrNoAvailableAccounts)

var ErrStrictFailoverOwnerBound = errors.New("strict failover: continuation belongs to an account outside the active tier; start a new request with full context")

type strictFailoverContextKey struct{}
type strictFailoverFailuresKey struct{}
type strictFailoverState struct {
	groupID  int64
	tier     int
	accounts []Account
	allowed  map[int64]struct{}
}
type strictFailoverFailures struct {
	sync.RWMutex
	reasons map[int64]string
	active  bool
}

func CloneStrictFailoverConfig(cfg StrictFailoverConfig) StrictFailoverConfig {
	out := StrictFailoverConfig{Enabled: cfg.Enabled, Tiers: make([][]int64, len(cfg.Tiers))}
	for i := range cfg.Tiers {
		out.Tiers[i] = append([]int64(nil), cfg.Tiers[i]...)
	}
	return out
}
func invalidStrictFailover(message string) error {
	return infraerrors.New(http.StatusBadRequest, "INVALID_STRICT_FAILOVER", message)
}
func validateStrictFailoverShape(cfg StrictFailoverConfig) error {
	if cfg.Enabled && len(cfg.Tiers) == 0 {
		return invalidStrictFailover("at least one non-empty tier is required")
	}
	if len(cfg.Tiers) > 10 {
		return invalidStrictFailover("at most 10 tiers are allowed")
	}
	seen := map[int64]bool{}
	for _, tier := range cfg.Tiers {
		if len(tier) == 0 {
			return invalidStrictFailover("tiers must not be empty")
		}
		for _, id := range tier {
			if id <= 0 || seen[id] {
				return invalidStrictFailover("account IDs must be positive and occur in exactly one tier")
			}
			seen[id] = true
		}
	}
	if len(seen) > 1000 {
		return invalidStrictFailover("at most 1000 accounts are allowed")
	}
	return nil
}
func (s *adminServiceImpl) validateStrictFailover(ctx context.Context, groupID int64, cfg StrictFailoverConfig) error {
	if err := validateStrictFailoverShape(cfg); err != nil {
		return err
	}
	if !cfg.Enabled {
		return nil
	}
	ids := []int64{}
	for _, tier := range cfg.Tiers {
		ids = append(ids, tier...)
	}
	if s.accountRepo == nil {
		return fmt.Errorf("validate strict failover membership: account repository unavailable")
	}
	accounts, err := s.accountRepo.GetByIDs(ctx, ids)
	if err != nil {
		return fmt.Errorf("validate strict failover membership: %w", err)
	}
	members := map[int64]bool{}
	for _, a := range accounts {
		if openAIStickyAccountMatchesGroup(a, &groupID) {
			members[a.ID] = true
		}
	}
	for _, id := range ids {
		if !members[id] {
			return invalidStrictFailover(fmt.Sprintf("account %d is not a member of this group", id))
		}
	}
	return nil
}

func withStrictFailoverFailures(ctx context.Context) context.Context {
	if ctx.Value(strictFailoverFailuresKey{}) != nil {
		return ctx
	}
	return context.WithValue(ctx, strictFailoverFailuresKey{}, &strictFailoverFailures{reasons: map[int64]string{}})
}

// StrictFailoverRequestActive lets forwarding loops exhaust the finite pool of
// untried accounts before stopping. The shared request state survives selection's
// local contexts and follows the actual scheduled group during group fallback.
func StrictFailoverRequestActive(ctx context.Context) bool {
	if ctx != nil {
		if state, ok := ctx.Value(strictFailoverFailuresKey{}).(*strictFailoverFailures); ok {
			state.RLock()
			defer state.RUnlock()
			return state.active
		}
	}
	return false
}

// RecordStrictFailoverFailure records a failed attempt only after same-account
// retries are exhausted. A real upstream response or a typed transport/stream
// failure proves this account could not serve this request, including transient
// provider capacity failures. It does not change persistent account health or
// prove that other accounts are unavailable. Generic exclusions do neither.
func RecordStrictFailoverFailure(ctx context.Context, accountID int64, failure *UpstreamFailoverError) {
	if ctx == nil || ctx.Err() != nil || failure == nil || !failure.ShouldRetryNextAccount() {
		return
	}
	reason := failure.strictFailoverReason
	// The forwarding service has already decided whether another account can
	// safely serve this error (ShouldRetryNextAccount). Preserve that decision
	// for provider-specific statuses and account-specific body limits too.
	if failure.ObservedUpstreamStatus >= 400 && failure.ObservedUpstreamStatus < 600 {
		reason = fmt.Sprintf("upstream_http_%d", failure.ObservedUpstreamStatus)
	}
	// Typed billing failures also originate from explicit upstream stream events.
	if failure.Reason == UpstreamBillingExhaustedReason && failure.Scope == GatewayFailureScopeAccount {
		reason = "upstream_billing_exhausted"
	}
	if failure.IsCredentialFailure() && failure.Scope == GatewayFailureScopeAccount {
		reason = "credential_failure"
	}
	if reason == "" {
		return
	}
	if state, ok := ctx.Value(strictFailoverFailuresKey{}).(*strictFailoverFailures); ok {
		state.Lock()
		state.reasons[accountID] = reason
		state.Unlock()
	}
}
func strictFailureReason(ctx context.Context, id int64) string {
	if state, ok := ctx.Value(strictFailoverFailuresKey{}).(*strictFailoverFailures); ok {
		state.RLock()
		defer state.RUnlock()
		return state.reasons[id]
	}
	return ""
}
func strictFailoverStateFrom(ctx context.Context) *strictFailoverState {
	state, _ := ctx.Value(strictFailoverContextKey{}).(*strictFailoverState)
	return state
}
func strictFailoverAllows(ctx context.Context, id int64) bool {
	state := strictFailoverStateFrom(ctx)
	if state == nil {
		return true
	}
	_, ok := state.allowed[id]
	return ok
}
func strictFailoverAccounts(ctx context.Context, groupID *int64) ([]Account, bool) {
	state := strictFailoverStateFrom(ctx)
	if state == nil || state.groupID != derefGroupID(groupID) {
		return nil, false
	}
	return append([]Account(nil), state.accounts...), true
}

// Pin the credential of non-replayable continuations even within the active
// tier; a later compatibility or admission failure must not switch the owner.
func pinStrictFailoverOwner(ctx context.Context, owner int64) (context.Context, error) {
	state := strictFailoverStateFrom(ctx)
	if state == nil {
		return ctx, nil
	}
	if owner <= 0 || !strictFailoverAllows(ctx, owner) {
		return ctx, ErrStrictFailoverOwnerBound
	}
	pinned := &strictFailoverState{groupID: state.groupID, tier: state.tier, allowed: map[int64]struct{}{owner: {}}}
	for _, a := range state.accounts {
		if a.ID == owner {
			pinned.accounts = append(pinned.accounts, a)
		}
	}
	return context.WithValue(ctx, strictFailoverContextKey{}, pinned), nil
}

func strictFailoverSelectionError(ctx context.Context, err error) error {
	if err == nil || strictFailoverStateFrom(ctx) == nil {
		return err
	}
	if errors.Is(err, ErrNoAvailableAccounts) || errors.Is(err, ErrNoAvailableCompactAccounts) || errors.Is(err, ErrAccountQualityHeld) {
		return fmt.Errorf("%w: %v", ErrStrictFailoverUnconfirmed, err)
	}
	return err
}

// prepareStrictFailover reads configured accounts from the DB, including inactive
// accounts omitted by scheduler snapshots. Unassigned group members form a final
// tier. Availability and the request's existing profit gate apply to every tier;
// concurrency is not a reason to advance.
func prepareStrictFailover(ctx context.Context, group *Group, repo AccountRepository, excluded map[int64]struct{}, model string, unavailable func(*Account) (string, error)) (context.Context, error) {
	if state, ok := ctx.Value(strictFailoverFailuresKey{}).(*strictFailoverFailures); ok {
		state.Lock()
		state.active = group != nil && group.StrictFailover.Enabled
		state.Unlock()
	}
	if group == nil || !group.StrictFailover.Enabled {
		return context.WithValue(ctx, strictFailoverContextKey{}, (*strictFailoverState)(nil)), nil
	}
	if err := validateStrictFailoverShape(group.StrictFailover); err != nil {
		return ctx, fmt.Errorf("%w: invalid configuration", ErrStrictFailoverUnconfirmed)
	}
	if repo == nil {
		return ctx, fmt.Errorf("%w: account repository unavailable", ErrStrictFailoverUnconfirmed)
	}
	ids := []int64{}
	assigned := map[int64]struct{}{}
	for _, tier := range group.StrictFailover.Tiers {
		ids = append(ids, tier...)
		for _, id := range tier {
			assigned[id] = struct{}{}
		}
	}
	rows, err := repo.GetByIDs(ctx, ids)
	if err != nil {
		return ctx, fmt.Errorf("%w: load accounts: %v", ErrStrictFailoverUnconfirmed, err)
	}
	if err := ctx.Err(); err != nil {
		return ctx, err
	}
	byID := make(map[int64]*Account, len(rows))
	for _, a := range rows {
		if a != nil {
			byID[a.ID] = a
		}
	}
	evidence := map[int64]string{}
	for i := 0; i <= len(group.StrictFailover.Tiers); i++ {
		var tier []int64
		if i < len(group.StrictFailover.Tiers) {
			tier = group.StrictFailover.Tiers[i]
		} else {
			// Only load the implicit tier when all explicit tiers are exhausted.
			// ListByGroup omits inactive members, which are already unavailable.
			members, loadErr := repo.ListByGroup(ctx, group.ID)
			if loadErr != nil {
				return ctx, fmt.Errorf("%w: load unassigned group accounts: %v", ErrStrictFailoverUnconfirmed, loadErr)
			}
			for j := range members {
				a := &members[j]
				if _, configured := assigned[a.ID]; configured || !openAIStickyAccountMatchesGroup(a, &group.ID) {
					continue
				}
				assigned[a.ID] = struct{}{}
				byID[a.ID] = a
				tier = append(tier, a.ID)
			}
		}
		if err := ctx.Err(); err != nil {
			return ctx, err
		}
		state := &strictFailoverState{groupID: group.ID, tier: i + 1, allowed: map[int64]struct{}{}}
		uncertain := false
		for _, id := range tier {
			a := byID[id]
			reason := ""
			// An exclusion caused by a local failure remains unknown even if its
			// legacy side effects wrote a cooldown. Only explicit evidence can
			// exhaust that account for this request.
			if _, wasExcluded := excluded[id]; wasExcluded {
				reason = strictFailureReason(ctx, id)
				// A terminal profit veto is also a deterministic request-local
				// exclusion. Re-evaluate fresh DB data instead of treating it as
				// unknown upstream health or persisting a health restriction.
				if reason == "" && a != nil && openAIStickyAccountMatchesGroup(a, &group.ID) {
					_, reason = openAIProfitControlVetoReason(ctx, a)
				}
				if reason == "" {
					uncertain = true
					continue
				}
			} else {
				switch {
				case a == nil:
					reason = "deleted"
				case !openAIStickyAccountMatchesGroup(a, &group.ID):
					reason = "removed_from_group"
				case !a.IsSchedulableForModelWithContext(ctx, model):
					reason = strictAccountUnavailableReason(ctx, a, model)
				default:
					reason, err = unavailable(a)
					if err != nil {
						return ctx, fmt.Errorf("%w: account %d: %v", ErrStrictFailoverUnconfirmed, id, err)
					}
				}
			}
			if reason == "" {
				_, reason = openAIProfitControlVetoReason(ctx, a)
			}
			if reason != "" {
				evidence[id] = reason
				continue
			}
			state.accounts = append(state.accounts, *a)
			state.allowed[id] = struct{}{}
		}
		if len(state.accounts) > 0 {
			if i > 0 {
				slog.InfoContext(ctx, "strict_failover_tier_selected", "group_id", group.ID, "model", model, "tier", i+1, "unavailable_accounts", evidence)
			}
			return context.WithValue(ctx, strictFailoverContextKey{}, state), nil
		}
		if uncertain {
			return ctx, fmt.Errorf("%w: tier %d contains exclusions without upstream evidence", ErrStrictFailoverUnconfirmed, i+1)
		}
	}
	slog.InfoContext(ctx, "strict_failover_all_tiers_unavailable", "group_id", group.ID, "model", model, "unavailable_accounts", evidence)
	return ctx, errStrictFailoverAllUnavailable
}

func strictAccountUnavailableReason(ctx context.Context, a *Account, model string) string {
	now := time.Now()
	switch {
	case !a.IsActive():
		return "account_inactive"
	case !a.Schedulable:
		return "scheduling_disabled"
	case a.AutoPauseOnExpired && a.ExpiresAt != nil && !now.Before(*a.ExpiresAt):
		return "account_expired"
	case a.OverloadUntil != nil && now.Before(*a.OverloadUntil):
		return "upstream_overload_cooldown"
	case a.RateLimitResetAt != nil && now.Before(*a.RateLimitResetAt):
		return "account_rate_limited"
	case a.TempUnschedulableUntil != nil && now.Before(*a.TempUnschedulableUntil):
		return "account_cooldown"
	case a.SupportsAccountQuota() && a.IsQuotaExceeded():
		return "account_quota_exhausted"
	case a.IsUpstreamBillingRateLimitedAt(now):
		return "upstream_billing_exhausted"
	case a.ActiveTestModelBlocked(activePassRateRoutingModel(ctx, model)):
		return "active_test_model_blocked"
	default:
		return "model_rate_limited"
	}
}

func strictGroupFromContext(ctx context.Context, groupID *int64) *Group {
	if g, ok := ctx.Value(ctxkey.Group).(*Group); ok && g != nil && g.ID == derefGroupID(groupID) {
		return g
	}
	if r, ok := ctx.Value(apiKeyGroupFallbackContextKey{}).(apiKeyGroupFallbackRouting); ok {
		for _, g := range []*Group{r.primaryGroup, r.fallbackGroup} {
			if g != nil && g.ID == derefGroupID(groupID) {
				return g
			}
		}
	}
	return nil
}

func (s *OpenAIGatewayService) prepareOpenAIStrictFailover(ctx context.Context, groupID *int64, excluded map[int64]struct{}, model, platform string, capability OpenAIEndpointCapability, imageCapability OpenAIImagesCapability, compact bool, transports ...OpenAIUpstreamTransport) (context.Context, error) {
	g := strictGroupFromContext(ctx, groupID)
	if groupID != nil && s.schedulerSnapshot != nil && s.schedulerSnapshot.groupRepo != nil {
		var err error
		g, err = s.schedulerSnapshot.GetGroupByIDLite(ctx, *groupID)
		if err != nil {
			return ctx, fmt.Errorf("%w: load group: %v", ErrStrictFailoverUnconfirmed, err)
		}
	}
	ctx = s.withOpenAIQuotaAutoPauseContext(ctx)
	parents := map[int64]*Account{}
	return prepareStrictFailover(ctx, g, s.accountRepo, excluded, model, func(a *Account) (string, error) {
		if a.Platform != NormalizeOpenAICompatiblePlatform(platform) {
			return "platform_mismatch", nil
		}
		if !supportsOpenAIRequestModel(a, model, compact) {
			return "model_not_supported", nil
		}
		if !accountSupportsOpenAICapabilities(a, capability, imageCapability) || (compact && openAICompactSupportTier(a) == 0) {
			return "capability_not_supported", nil
		}
		if len(transports) > 0 && transports[0] != "" && transports[0] != OpenAIUpstreamTransportAny && transports[0] != OpenAIUpstreamTransportHTTPSSE && !s.isOpenAIAccountTransportCompatible(a, transports[0]) {
			return "transport_not_supported", nil
		}
		if a.IsShadow() {
			id := *a.ParentAccountID
			if _, loaded := parents[id]; !loaded {
				rows, err := s.accountRepo.GetByIDs(ctx, []int64{id})
				if err != nil {
					return "", err
				}
				parents[id] = nil
				for _, parent := range rows {
					if parent != nil && parent.ID == id {
						parents[id] = parent
					}
				}
			}
			if !parentHealthyForShadow(a, func(id int64) *Account { return parents[id] }) {
				return "shadow_parent_unavailable", nil
			}
		}
		if source, ok := CompositeRouteSourceFromContext(ctx); ok && source == CompositeRouteSourceAccount {
			if publicModel, ok := RequestedPublicModelFromContext(ctx); ok && !explicitModelMappingClaims(*a, publicModel) {
				return "account_model_not_owned", nil
			}
		}
		if a.IsGrok() {
			if paused, _ := shouldAutoPauseGrokAccountByQuota(a); paused {
				return "quota_exhausted", nil
			}
		}
		if g.RequirePrivacySet && !a.IsPrivacySet() {
			return "privacy_required", nil
		}
		if s.isOpenAIAccountRequestRuntimeBlocked(a, model) {
			return "model_runtime_cooldown", nil
		}
		if paused, _ := shouldAutoPauseOpenAIAccountByQuota(ctx, a); paused {
			return "quota_exhausted", nil
		}
		return "", nil
	})
}

func (s *GatewayService) prepareGatewayStrictFailover(ctx context.Context, groupID *int64, excluded map[int64]struct{}, model, platform string, mixed bool) (context.Context, error) {
	g := strictGroupFromContext(ctx, groupID)
	if g == nil && groupID != nil && s.groupRepo != nil {
		var err error
		g, err = s.groupRepo.GetByIDLite(ctx, *groupID)
		if err != nil {
			return ctx, err
		}
	}
	return prepareStrictFailover(ctx, g, s.accountRepo, excluded, model, func(a *Account) (string, error) {
		if a.Platform != platform && (!mixed || a.Platform != PlatformAntigravity || !a.IsMixedSchedulingEnabled()) {
			return "platform_mismatch", nil
		}
		if model != "" && !s.isModelSupportedByAccountWithContext(ctx, a, model) {
			return "model_not_supported", nil
		}
		if g.RequirePrivacySet && !a.IsPrivacySet() {
			return "privacy_required", nil
		}
		if !s.isAccountSchedulableForQuota(a) {
			return "quota_exhausted", nil
		}
		return "", nil
	})
}

// ValidateStrictFailoverBoundAccount re-evaluates a long-lived connection before
// each subsequent request. Account-bound WS state cannot be moved silently;
// the caller must request reconnection with full context when the tier changes.
func (s *OpenAIGatewayService) ValidateStrictFailoverBoundAccount(ctx context.Context, groupID *int64, account *Account, model string, capability OpenAIEndpointCapability, transport OpenAIUpstreamTransport) error {
	if account == nil {
		return ErrStrictFailoverUnconfirmed
	}
	selectedCtx, err := s.prepareOpenAIStrictFailover(ctx, groupID, nil, model, account.Platform, capability, "", false, transport)
	if err != nil {
		return err
	}
	if !strictFailoverAllows(selectedCtx, account.ID) {
		return ErrStrictFailoverOwnerBound
	}
	return nil
}

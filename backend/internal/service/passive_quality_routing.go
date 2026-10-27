package service

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/google/uuid"
)

type passiveQualityAllocationSeedKey struct{}

// The seed is stable across candidate filtering and retries. Sessions remain
// assigned normally after admission; cache restrictions never clear bindings.
func withPassiveQualityAllocationSeed(ctx context.Context, session string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if _, ok := ctx.Value(passiveQualityAllocationSeedKey{}).(string); ok {
		return ctx
	}
	seed := session
	if seed == "" {
		seed = resolveUsageBillingRequestID(ctx, "")
	}
	if seed == "" {
		seed = uuid.NewString()
	}
	return context.WithValue(ctx, passiveQualityAllocationSeedKey{}, seed)
}

// PassiveQualityNewSessionPercent composes overlapping cache controls using
// their strictest percentage. It deliberately does not cap concurrency.
func (a *Account) PassiveQualityNewSessionPercent(model string) int {
	percent := 100
	if a == nil {
		return percent
	}
	holds, _ := a.Extra["passive_quality"].([]any)
	for _, raw := range holds {
		h, ok := raw.(map[string]any)
		if !ok || h["phase"] != "cache_limited" {
			continue
		}
		if h["scope"] != "account" && (model == "" || h["model"] != model) {
			continue
		}
		value := 100
		switch v := h["allocation_percent"].(type) {
		case float64:
			value = int(v)
		case int:
			value = v
		case json.Number:
			n, e := strconv.Atoi(string(v))
			if e == nil {
				value = n
			}
		}
		// A malformed snapshot must not erase traffic or strand recovery.
		if value >= 1 && value < percent {
			percent = value
		}
	}
	return percent
}

func passiveQualityAllowsNewSession(ctx context.Context, a *Account, model string) bool {
	model = passiveQualityRoutingModel(ctx, model)
	percent := a.PassiveQualityNewSessionPercent(model)
	if percent >= 100 {
		return true
	}
	seed, _ := ctx.Value(passiveQualityAllocationSeedKey{}).(string)
	if seed == "" {
		seed = resolveUsageBillingRequestID(ctx, "")
	}
	if seed == "" {
		seed = uuid.NewString()
	}
	h := sha256.Sum256([]byte(fmt.Sprintf("cache-allocation:%s:%d:%s", seed, a.ID, model)))
	return binary.BigEndian.Uint64(h[:8])%100 < uint64(percent)
}

// Call only after hard eligibility checks. Sampling can reduce new assignment,
// but must not turn an otherwise healthy pool into an outage: when every healthy
// candidate is sampled out, retain the pool. Existing session owners are exempt.
func passiveQualityNewSessionCandidates(ctx context.Context, candidates []*Account, model string, existingIDs ...int64) []*Account {
	allowed := make([]*Account, 0, len(candidates))
	for _, a := range candidates {
		existing := false
		for _, id := range existingIDs {
			if id > 0 && id == a.ID {
				existing = true
				break
			}
		}
		if existing || passiveQualityAllowsNewSession(ctx, a, model) {
			allowed = append(allowed, a)
		}
	}
	if len(allowed) == 0 {
		return candidates
	}
	return allowed
}

// Legacy priority/LRU selectors retain their normal order within admitted and
// fallback tiers; an excluded cache candidate is usable only if no peer admits.
func passiveQualityPreferNewSession(ctx context.Context, candidate, current *Account, model string) (prefer, decided bool) {
	if current == nil {
		return false, false
	}
	a, b := passiveQualityAllowsNewSession(ctx, candidate, model), passiveQualityAllowsNewSession(ctx, current, model)
	if a == b {
		return false, false
	}
	return a, true
}

// Keep the sampled-out tier until acquisition completes. These are already
// eligible candidates and still pass every normal fresh/admission gate.
func passiveQualityNewSessionTiers(ctx context.Context, candidates []*Account, model string, existingIDs ...int64) (preferred, fallback []*Account) {
	preferred = passiveQualityNewSessionCandidates(ctx, candidates, model, existingIDs...)
	if len(preferred) == len(candidates) {
		return preferred, nil
	}
	ids := make(map[int64]bool, len(preferred))
	for _, a := range preferred {
		ids[a.ID] = true
	}
	for _, a := range candidates {
		if !ids[a.ID] {
			fallback = append(fallback, a)
		}
	}
	return preferred, fallback
}

func passiveQualityOrderNewSessionCandidates(ctx context.Context, candidates []*Account, model string, existingIDs ...int64) {
	preferred, fallback := passiveQualityNewSessionTiers(ctx, candidates, model, existingIDs...)
	if len(fallback) == 0 {
		return
	}
	copy(candidates, append(preferred, fallback...))
}

func passiveQualityOrderNewSessionLoads(ctx context.Context, candidates []accountWithLoad, model string, existingIDs ...int64) {
	accounts := make([]*Account, 0, len(candidates))
	for _, candidate := range candidates {
		accounts = append(accounts, candidate.account)
	}
	preferred, fallback := passiveQualityNewSessionTiers(ctx, accounts, model, existingIDs...)
	if len(fallback) == 0 {
		return
	}
	ids := make(map[int64]bool, len(preferred))
	for _, a := range preferred {
		ids[a.ID] = true
	}
	sort.SliceStable(candidates, func(i, j int) bool { return ids[candidates[i].account.ID] && !ids[candidates[j].account.ID] })
}

func passiveQualityPreferredNewSessionLoads(ctx context.Context, candidates []accountWithLoad, model string, existingIDs ...int64) []accountWithLoad {
	accounts := make([]*Account, 0, len(candidates))
	for _, candidate := range candidates {
		accounts = append(accounts, candidate.account)
	}
	admitted, fallback := passiveQualityNewSessionTiers(ctx, accounts, model, existingIDs...)
	if len(fallback) == 0 {
		return candidates
	}
	ids := make(map[int64]bool, len(admitted))
	for _, account := range admitted {
		ids[account.ID] = true
	}
	preferred := make([]accountWithLoad, 0, len(admitted))
	for _, candidate := range candidates {
		if ids[candidate.account.ID] {
			preferred = append(preferred, candidate)
		}
	}
	return preferred
}

// Public request identity is separate from provider model mapping. Quality
// telemetry, policy routing, and live admission must all use the same identity.
func passiveQualityRoutingModel(ctx context.Context, requestedModel string) string {
	if ctx != nil {
		if model := qualityRequestModel(ctx); model != "" {
			return model
		}
	}
	return requestedModel
}

func withPassiveQualityRoutingModel(ctx context.Context, requestedModel string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if qualityRequestModel(ctx) == "" && requestedModel != "" {
		return context.WithValue(ctx, ctxkey.Model, requestedModel)
	}
	return ctx
}

func shouldClearStickySessionWithContext(ctx context.Context, account *Account, requestedModel string) bool {
	if account == nil {
		return false
	}
	if !account.IsSchedulable() || account.PassiveQualityBlocked(passiveQualityRoutingModel(ctx, requestedModel)) || account.ActivePassRateBlocked(activePassRateRoutingModel(ctx, requestedModel)) {
		return true
	}
	// Upstream cooldown is still keyed by the mapped provider model.
	return account.GetRateLimitRemainingTimeWithContext(ctx, requestedModel) > 0
}

func passiveQualitySchedulingAccount(ctx context.Context, account *Account, requestedModel string) *Account {
	if account == nil {
		return nil
	}
	adjusted := qualitySchedulingAccount(*account, passiveQualityRoutingModel(ctx, requestedModel))
	return &adjusted
}

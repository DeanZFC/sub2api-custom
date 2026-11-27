package service

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"
)

// One meter belongs to a single in-process chat request. Each forwarded account
// and each additional HTTP retry must reserve before contacting its upstream.
type GroupProbeMeter struct {
	Result           GroupProbeResult
	CompletedForward bool
	budget           QualityOperationsRepository
	pricing          *BillingService
	channels         *ChannelService
	groupID          int64
	model            string
	reservation      *QualityProbeReservation
	sent             bool
	account          *Account
	started          time.Time
	runCtx           context.Context
}

func NewGroupProbeMeter(budget QualityOperationsRepository, pricing *BillingService, channels *ChannelService, groupID int64, model string) *GroupProbeMeter {
	return &GroupProbeMeter{budget: budget, pricing: pricing, channels: channels, groupID: groupID, model: model, started: time.Now()}
}

const GroupProbeOutputTokens = 128

type groupProbeMeterKey struct{}

func (p *GroupProbeMeter) Begin(ctx context.Context, a *Account, bodyBytes int) (context.Context, bool) {
	p.account = a
	p.runCtx = ctx
	p.CompletedForward = false
	if !p.reserve(ctx, int64(bodyBytes)+GroupProbeOutputTokens) {
		return ctx, false
	}
	if p.Result.AccountID != 0 && p.Result.AccountID != a.ID {
		p.Result.Switched = true
	}
	p.Result.AccountID = a.ID
	p.sent = false
	return context.WithValue(ctx, groupProbeMeterKey{}, p), true
}

func (p *GroupProbeMeter) reserve(ctx context.Context, tokens int64) bool {
	if p.Result.Deferred != nil {
		return false
	}
	r, err := p.budget.ReserveQualityProbe(ctx, p.account.ID, tokens, true, "group_probe")
	if err != nil {
		var d *QualityDeferredError
		if errors.As(err, &d) {
			d = &QualityDeferredError{Reason: "budget_exhausted", Until: d.Until}
		} else {
			slog.Warn("group probe budget admission", "error", err)
			d = &QualityDeferredError{Reason: "budget_unavailable", Until: time.Now().Add(time.Minute)}
		}
		p.Result.Deferred = d
		return false
	}
	p.reservation = r
	return true
}

// PrepareGroupProbeTransport runs once per physical HTTP attempt. The first send
// uses Begin's reservation, which also covers adapters with a custom transport.
// A probe retains its owner deadline even when a customer adapter detaches for billing.
func PrepareGroupProbeTransport(req *http.Request, cancel context.CancelFunc) (context.CancelFunc, error) {
	p, _ := req.Context().Value(groupProbeMeterKey{}).(*GroupProbeMeter)
	if p == nil {
		return cancel, nil
	}
	if p.sent {
		p.settle(nil, "unknown")
		estimate := req.ContentLength
		if estimate < 0 {
			estimate = 0
		}
		if !p.reserve(p.runCtx, estimate+GroupProbeOutputTokens) {
			return cancel, p.Result.Deferred
		}
	}
	p.sent = true
	stop := context.AfterFunc(p.runCtx, cancel)
	return func() { stop(); cancel() }, nil
}

func (p *GroupProbeMeter) settle(tokens *int64, kind string) {
	if p.reservation == nil {
		return
	}
	if tokens == nil {
		p.Result.PartialUsage = true
	}
	ctx, done := context.WithTimeout(context.Background(), 3*time.Second)
	defer done()
	if err := p.budget.SettleQualityProbe(ctx, p.reservation, QualityProbeOutcome{At: time.Now(), Kind: kind, Tokens: tokens}); err != nil {
		slog.Warn("group probe budget settlement", "error", err)
	}
	p.reservation = nil
}

func (p *GroupProbeMeter) ObserveOpenAI(ctx context.Context, r *OpenAIForwardResult, err error) {
	if r == nil {
		p.settle(nil, "unknown")
		return
	}
	tier := ""
	if r.ServiceTier != nil {
		tier = *r.ServiceTier
	}
	p.observe(ctx, r.Usage.billingUsageTokens(), r.UpstreamModel, r.Model, tier, r.ReasoningEffort, 1, err)
}

func (p *GroupProbeMeter) Observe(ctx context.Context, r *ForwardResult, err error) {
	if r == nil {
		p.settle(nil, "unknown")
		return
	}
	p.observe(ctx, r.Usage.billingUsageTokens(), r.UpstreamModel, r.Model, r.UpstreamResponseServiceTier, r.ReasoningEffort, r.upstreamPriceFactor(), err)
}

func (p *GroupProbeMeter) observe(ctx context.Context, t UsageTokens, upstream, model, tier string, reasoning *string, factor float64, err error) {
	p.CompletedForward = err == nil
	tokens := int64(t.InputTokens) + int64(t.OutputTokens) + int64(t.CacheReadTokens) + int64(t.CacheCreationTokens)
	if tokens <= 0 {
		p.settle(nil, "unknown")
		return
	}
	kind := "healthy"
	if err != nil {
		kind = "failed"
	}
	p.settle(&tokens, kind)
	if p.Result.Tokens == nil {
		p.Result.Tokens = new(int64)
	}
	*p.Result.Tokens += tokens
	if upstream == "" {
		upstream = model
	}
	if upstream == "" {
		upstream = p.model
	}
	effort := ""
	if reasoning != nil {
		effort = *reasoning
	}
	long := accountStatsLongContextPricingEnabled(openAILongContextBillingGate(p.account))
	base := tryModelFilePricing(p.pricing, upstream, t, tier, p.started, long, effort)
	var total float64
	if base != nil {
		total = *base * factor
	}
	cost := resolveAccountStatsCost(ctx, p.channels, p.pricing, p.account.ID, p.groupID, upstream, t, 1, total, tier, p.started, long, factor, effort)
	if cost == nil && base != nil {
		cost = &total
	}
	if cost != nil {
		if p.Result.EstimatedCost == nil {
			p.Result.EstimatedCost = new(float64)
		}
		*p.Result.EstimatedCost += *cost * p.account.BillingRateMultiplier()
	} else {
		p.Result.PartialUsage = true
	}
}

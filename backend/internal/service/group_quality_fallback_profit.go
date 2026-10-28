package service

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
)

const groupQualityFallbackProfitUnavailable = "group_quality_profit_unavailable"

// Quality recovery may relax automatic quality holds, never profit admission.
// Keep the ordinary formula and request threshold; only recovery fails closed
// when the policy cannot be resolved or an enabled policy has no usable gate.
func (s *OpenAIGatewayService) withGroupQualityFallbackProfitControl(ctx context.Context, groupID *int64) (context.Context, error) {
	ctx = s.withOpenAIProfitControlGate(ctx, groupID)
	if gate, _ := ctx.Value(openAIProfitControlGateCtxKey{}).(*openAIProfitControlGate); gate != nil && groupID != nil && gate.groupID == *groupID {
		return ctx, nil
	}
	var group *Group
	if current, ok := ctx.Value(ctxkey.Group).(*Group); ok && IsGroupContextValid(current) && groupID != nil && current.ID == *groupID {
		group = current
	} else if s.schedulerSnapshot != nil && groupID != nil {
		var err error
		group, err = s.schedulerSnapshot.GetGroupByIDLite(ctx, *groupID)
		if err != nil || group == nil {
			slog.Warn("group_quality_fallback_profit_policy_unavailable", "group_id", *groupID, "error", err)
			return ctx, fmt.Errorf("%w: %s", ErrAccountAdmissionDenied, groupQualityFallbackProfitUnavailable)
		}
	}
	if group == nil || group.ProfitControlEnabled {
		return ctx, fmt.Errorf("%w: %s", ErrAccountAdmissionDenied, groupQualityFallbackProfitUnavailable)
	}
	return ctx, nil
}

// Only quality recovery uses an authoritative post-slot rate read. An account
// refresh failure cannot authorize an emergency request on a stale rate.
func (s *OpenAIGatewayService) groupQualityFallbackProfitVetoLatest(ctx context.Context, selected *Account) (*Account, bool, string) {
	if recovery, ok := GroupQualityFallbackFromContext(ctx); ok {
		var err error
		ctx, err = s.withGroupQualityFallbackProfitControl(ctx, &recovery.GroupID)
		if err != nil {
			return selected, true, groupQualityFallbackProfitUnavailable
		}
	}
	gate, _ := ctx.Value(openAIProfitControlGateCtxKey{}).(*openAIProfitControlGate)
	if gate == nil {
		return selected, false, ""
	}
	if selected == nil || s.accountRepo == nil {
		return selected, true, groupQualityFallbackProfitUnavailable
	}
	latest, err := s.accountRepo.GetByID(ctx, selected.ID)
	if err != nil || latest == nil || latest.ID != selected.ID {
		openAIProfitControlObserverInstance.recordRefreshFailure(gate.groupID, gate.platform, gate.threshold)
		return selected, true, groupQualityFallbackProfitUnavailable
	}
	if selected.SelectedProxyID > 0 {
		routed := *latest
		routed.ProxyID, routed.Proxy, routed.SelectedProxyID = selected.ProxyID, selected.Proxy, selected.SelectedProxyID
		latest = &routed
	}
	vetoed, reason := openAIProfitControlVetoReason(ctx, latest)
	return latest, vetoed, reason
}

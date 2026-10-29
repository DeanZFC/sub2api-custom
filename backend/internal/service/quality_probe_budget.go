package service

import (
	"context"
	"errors"
	"io"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
)

type qualityOperationsKey struct{}
type qualitySettlementKey struct{}

// All background quality transports enter here, including compatibility
// attempts and Antigravity's test-only transport path. Customer traffic has no
// qualityOperationsKey and never touches this budget.
func executeQualityTestHTTP(req *http.Request, account int64, send func(*http.Request) (*http.Response, error)) (*http.Response, error) {
	ctx := req.Context()
	if active, ok := ctx.Value(passiveRecoveryRequestKey{}).(*passiveRecoveryRequest); ok && active != nil {
		if active.attempts == nil || active.attempts.Add(1) > passiveRecoveryMaxRequests {
			deferred := &QualityDeferredError{Reason: "recovery_attempt_limit", Until: time.Now().Add(time.Minute)}
			if collector, ok := ctx.Value(accountTestUsageKey{}).(*accountTestUsageCollector); ok {
				collector.deferred = deferred
			}
			return nil, deferred
		}
		if err := applyPassiveRecoveryRequestIdentity(req, active.session); err != nil {
			return nil, err
		}
	}
	repo, _ := ctx.Value(qualityOperationsKey{}).(QualityOperationsRepository)
	if repo != nil {
		collector, _ := ctx.Value(accountTestUsageKey{}).(*accountTestUsageCollector)
		if collector != nil && collector.deferred != nil {
			return nil, collector.deferred
		}
		automatic, _ := ctx.Value(scheduledTestAutomaticRunKey{}).(bool)
		recovery := QualityProbePurpose(ctx) == "recovery"
		session, _ := ctx.Value(qualityRunKey{}).(string)
		estimate := qualityProbeTokenEstimate(req)
		reserveCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		res, err := repo.ReserveQualityProbe(reserveCtx, account, estimate, automatic || recovery, session)
		cancel()
		if err != nil {
			var deferred *QualityDeferredError
			if !errors.As(err, &deferred) {
				logger.LegacyPrintf("service.quality_operations", "budget admission failed: %v", err)
				deferred = &QualityDeferredError{Reason: "无法检查检测预算", Until: time.Now().Add(time.Minute)}
			}
			if collector != nil {
				collector.deferred = deferred
			}
			return nil, deferred
		}
		ctx = context.WithValue(ctx, qualitySettlementKey{}, func(a *ScheduledTestRequestAttempt) {
			outcome := QualityProbeOutcome{At: time.Now().UTC(), Kind: "unknown"}
			if a.qualityUsageFinal && a.InputTokens != nil && a.OutputTokens != nil {
				values := []*int64{a.InputTokens, a.OutputTokens}
				if a.qualityCacheExclusive {
					values = append(values, a.CacheReadTokens, a.CacheCreationTokens)
				}
				tokens := int64(0)
				valid := true
				for _, v := range values {
					if v == nil {
						continue
					}
					if *v < 0 || tokens > math.MaxInt64-*v {
						valid = false
						break
					}
					tokens += *v
				}
				if valid {
					outcome.Tokens = &tokens
				}
			}
			if a.HTTPStatus >= 200 && a.HTTPStatus < 300 && a.FailureKind == "" {
				outcome.Kind = "healthy"
			} else {
				outcome.Kind = scheduledTestFailureKind(&ScheduledTestResult{Status: "failed", ExecutionInfo: &ScheduledTestExecutionInfo{Attempts: []ScheduledTestRequestAttempt{*a}, FailureKind: a.FailureKind}})
			}
			settlement := qualityProbeSettlement{id: res.ID, finish: func(finalKind string) {
				if finalKind != "" && finalKind != "quality_deferred" {
					outcome.Kind = finalKind
				}
				persist, stop := context.WithTimeout(context.Background(), 5*time.Second)
				defer stop()
				if err := repo.SettleQualityProbe(persist, res, outcome); err != nil {
					logger.LegacyPrintf("service.quality_operations", "budget settlement failed, reservation retained: %v", err)
				}
			}}
			// Final stream validation may detect an early EOF behind HTTP 200. Wait
			// until the background test finishes so aggregation uses that verdict.
			if collector != nil {
				collector.qualitySettlements = append(collector.qualitySettlements, settlement)
			} else {
				settlement.finish("")
			}
		})
		req = req.WithContext(ctx)
	}
	started := time.Now().UTC()
	resp, err := send(req)
	if err == nil && resp == nil {
		err = errors.New("upstream returned nil response")
	}
	return observeAccountTestResponse(ctx, started, resp, err)
}

// Text probes retain the conservative one-token-per-body-byte reservation.
// Trusted visual review images use a bounded per-image allowance instead of
// counting PNG compression/base64 bytes as text. Image allowances include
// headroom for the fixed 960x720 renderer frames; they are budget estimates,
// not billing rates. Actual upstream usage settles the reservation afterwards.
func qualityProbeTokenEstimate(req *http.Request) int64 {
	estimate := req.ContentLength
	if estimate < 0 {
		estimate = 0
	}
	if vision, ok := req.Context().Value(accountTestVisionContextKey{}).(accountTestVisionInput); ok && len(vision.Images) >= 4 && len(vision.Images) <= 8 {
		imageBytes := int64(0)
		for _, image := range vision.Images {
			imageBytes = saturatingQualityTokenAdd(imageBytes, int64(len(image)))
		}
		if imageBytes <= estimate {
			estimate -= imageBytes
			model := ""
			if identity, ok := req.Context().Value(accountTestModelIdentityContextKey{}).(*accountTestModelIdentity); ok && identity != nil {
				model, _, _ = identity.snapshot()
			}
			estimate = saturatingQualityTokenAdd(estimate, int64(len(vision.Images))*qualityVisionImageTokenAllowance(model))
		}
	}
	return saturatingQualityTokenAdd(estimate, int64(accountTestOutputTokenLimit(req.Context())))
}

func qualityVisionImageTokenAllowance(model string) int64 {
	model = strings.ToLower(strings.TrimSpace(model))
	// GPT-4o mini's high-detail image units are much larger than other models.
	// Match it before GPT-4o and retain conservative admission for unknown IDs.
	switch {
	case strings.HasPrefix(model, "gpt-4o-mini"):
		return 32768
	case strings.HasPrefix(model, "gpt-5"), strings.HasPrefix(model, "gpt-6"),
		strings.HasPrefix(model, "gpt-4.1-mini"), strings.HasPrefix(model, "gpt-4.1-nano"), strings.HasPrefix(model, "o4-mini"):
		return 8192
	case strings.HasPrefix(model, "gpt-4o"), strings.HasPrefix(model, "gpt-4.1"),
		strings.HasPrefix(model, "o1"), strings.HasPrefix(model, "o3"):
		return 2048
	default:
		return 65536
	}
}

func saturatingQualityTokenAdd(a, b int64) int64 {
	if b < 0 {
		return a
	}
	if a > math.MaxInt64-b {
		return math.MaxInt64
	}
	return a + b
}

func qualityUsageReadError(a *ScheduledTestRequestAttempt, err error) {
	if err != nil && !errors.Is(err, io.EOF) && a.FailureKind == "" {
		if errors.Is(err, context.Canceled) {
			a.FailureKind = "canceled"
		} else {
			a.FailureKind = "network"
		}
	}
}

type qualityProbeSettlement struct {
	id     int64
	finish func(string)
}

func (c *accountTestUsageCollector) finishQualitySettlements(kind string) {
	var latest int64
	for _, s := range c.qualitySettlements {
		if s.id > latest {
			latest = s.id
		}
	}
	for _, s := range c.qualitySettlements {
		override := ""
		if s.id == latest {
			override = kind
		}
		s.finish(override)
	}
	c.qualitySettlements = nil
}

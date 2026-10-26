package service

import (
	"context"
	"errors"
	"io"
	"math"
	"net/http"
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
	repo, _ := ctx.Value(qualityOperationsKey{}).(QualityOperationsRepository)
	if repo != nil {
		collector, _ := ctx.Value(accountTestUsageKey{}).(*accountTestUsageCollector)
		if collector != nil && collector.deferred != nil {
			return nil, collector.deferred
		}
		automatic, _ := ctx.Value(scheduledTestAutomaticRunKey{}).(bool)
		recovery := QualityProbePurpose(ctx) == "recovery"
		session, _ := ctx.Value(qualityRunKey{}).(string)
		// Body byte length is a conservative input estimate. The configured reserve
		// also covers protocols which cannot enforce an output-token ceiling.
		estimate := req.ContentLength
		if estimate < 0 {
			estimate = 0
		}
		estimate += int64(accountTestOutputTokenLimit(ctx))
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

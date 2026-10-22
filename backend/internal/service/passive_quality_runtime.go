package service

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

var ErrPassiveQualityConflict = errors.New("passive quality configuration or state changed; refresh and retry")

const passiveQualityCaptureKey = "passive_quality_capture"
const passiveQualityStartedKey = "passive_quality_started"
const passiveQualityTurnKey = "passive_quality_turn"

type PassiveQualityService struct {
	Repo         PassiveQualityRepository
	test         *AccountTestService
	ctx          context.Context
	cancel       context.CancelFunc
	wg           sync.WaitGroup
	queue        chan PassiveQualityFailure
	gap          atomic.Int64
	enabled      atomic.Bool
	cacheEnabled atomic.Bool
	flushMu      sync.Mutex
	pending      []PassiveQualityFailure
	probeSlots   chan struct{}
}

func NewPassiveQualityService(repo PassiveQualityRepository, test *AccountTestService) *PassiveQualityService {
	ctx, cancel := context.WithCancel(context.Background())
	return &PassiveQualityService{Repo: repo, test: test, ctx: ctx, cancel: cancel, queue: make(chan PassiveQualityFailure, 8192), probeSlots: make(chan struct{}, 4)}
}
func (s *PassiveQualityService) Start() {
	// Capture by default until the first successful configuration read. A startup
	// read failure must not silently suppress failures for existing policies.
	s.enabled.Store(true)
	s.cacheEnabled.Store(true)
	s.wg.Add(2)
	go s.run()
	go s.flushLoop()
}
func (s *PassiveQualityService) Stop() { s.cancel(); s.wg.Wait() }
func (s *PassiveQualityService) ConfigurationChanged() {
	s.enabled.Store(true)
	s.cacheEnabled.Store(true)
}
func (s *PassiveQualityService) run() {
	defer s.wg.Done()
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	nextMaintenance := time.Time{}
	for {
		if s.ctx.Err() != nil {
			return
		}
		ctx, cancel := context.WithTimeout(s.ctx, 12*time.Second)
		policies, err := s.Repo.ListPolicies(ctx)
		if err == nil {
			enabled := false
			cacheEnabled := false
			for _, p := range policies {
				enabled = enabled || p.Enabled
				cacheEnabled = cacheEnabled || (p.Enabled && p.CacheRate.Enabled)
			}
			s.enabled.Store(enabled)
			s.cacheEnabled.Store(cacheEnabled)
		}
		cutoff := time.Now()
		ready := s.flush(true)
		var probes []PassiveQualityState
		if ready {
			probes, err = s.Repo.Sweep(ctx, cutoff, cap(s.probeSlots)-len(s.probeSlots))
		}
		cancel()
		if err != nil && s.ctx.Err() == nil {
			logger.LegacyPrintf("service.passive_quality", "sweep failed: %v", err)
		}
		if time.Now().After(nextMaintenance) && s.test != nil && s.test.qualityOperations != nil {
			nextMaintenance = time.Now().Add(time.Hour)
			if repo, ok := s.test.qualityOperations.(interface{ PruneQualityOperations(context.Context) error }); ok {
				maintenance, stop := context.WithTimeout(s.ctx, 3*time.Second)
				if err := repo.PruneQualityOperations(maintenance); err != nil && s.ctx.Err() == nil {
					logger.LegacyPrintf("service.quality_operations", "maintenance failed: %v", err)
				}
				stop()
			}
		}
		for _, p := range probes {
			s.probeSlots <- struct{}{}
			s.wg.Add(1)
			go func(p PassiveQualityState) { defer s.wg.Done(); defer func() { <-s.probeSlots }(); s.probe(p) }(p)
		}

		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
func (s *PassiveQualityService) probe(p PassiveQualityState) {
	if s.test == nil {
		return
	}
	ctx, cancel := context.WithTimeout(s.ctx, 60*time.Second)
	defer cancel()
	model := p.Model
	if model == "" {
		model = p.ProbeModel
	}
	result, err := s.test.RunTestBackground(context.WithValue(ctx, passiveQualityProbeKey{}, true), p.AccountID, model)
	if s.ctx.Err() != nil {
		return
	}
	success := err == nil && result != nil && result.Status == "success" && strings.TrimSpace(result.ResponseText) != ""
	persist, cancelPersist := context.WithTimeout(s.ctx, 5*time.Second)
	defer cancelPersist()
	if QualityResultDeferred(result) {
		if repo, ok := s.Repo.(interface {
			PostponePassiveProbe(context.Context, PassiveQualityState, time.Time) error
		}); ok {
			until := time.Now().Add(time.Minute)
			if result.ExecutionInfo.NextCheckAt != nil {
				until = *result.ExecutionInfo.NextCheckAt
			}
			if err = repo.PostponePassiveProbe(persist, p, until); err != nil {
				logger.LegacyPrintf("service.passive_quality", "probe deferral persistence failed: %v", err)
			}
		}
		return
	}
	if err = s.Repo.FinishProbe(persist, p, success, time.Now()); err != nil {
		logger.LegacyPrintf("service.passive_quality", "probe persistence failed: %v", err)
	}
}
func (s *PassiveQualityService) enqueue(f PassiveQualityFailure) {
	if !s.enabled.Load() {
		return
	}
	select {
	case s.queue <- f:
	default:
		s.gap.Store(time.Now().UnixNano())
	}
}
func (s *PassiveQualityService) flush(all bool) bool {
	s.flushMu.Lock()
	defer s.flushMu.Unlock()
	rounds := 1
	if all {
		rounds = 17
	}
	for i := 0; i < rounds; i++ {
		for len(s.pending) < 512 {
			select {
			case f := <-s.queue:
				s.pending = append(s.pending, f)
			default:
				goto drained
			}
		}
	drained:
		gapNS := s.gap.Swap(0)
		var gap *time.Time
		if gapNS > 0 {
			v := time.Unix(0, gapNS)
			gap = &v
		}
		if len(s.pending) == 0 && gap == nil {
			return true
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		err := s.Repo.RecordFailures(ctx, s.pending, gap)
		cancel()
		if err != nil {
			s.gap.Store(time.Now().UnixNano())
			logger.LegacyPrintf("service.passive_quality", "telemetry persistence failed: %v", err)
			return false
		}
		s.pending = nil
		if !all {
			return true
		}
	}
	return len(s.queue) == 0
}
func (s *PassiveQualityService) flushLoop() {
	defer s.wg.Done()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-s.ctx.Done():
			s.flush(true)
			return
		case <-ticker.C:
			s.flush(false)
		}
	}
}

// Capture is installed on gateway routes independently of optional ops settings.
// Attempts are copied immediately; long-lived WebSockets need not close before
// a failed turn becomes visible to the quality evaluator.
func (s *PassiveQualityService) Capture() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), passiveQualityCacheSinkKey{}, func(f PassiveQualityFailure) {
			if f.AttemptKey != "" || s.cacheEnabled.Load() {
				s.enqueue(f)
			}
		}))
		PassiveQualityBeginTurn(c)
		c.Set(passiveQualityCaptureKey, func(ev OpsUpstreamErrorEvent) {
			if !s.enabled.Load() || c.Request == nil || c.Request.Context().Err() != nil || ev.AccountID <= 0 || !PassiveQualityUpstreamFailure(ev) {
				return
			}
			if strings.Contains(c.Request.URL.Path, "count_tokens") {
				return
			}
			model, _ := c.Request.Context().Value(ctxkey.Model).(string)
			if public, ok := RequestedPublicModelFromContext(c.Request.Context()); ok && public != "" {
				model = public
			}
			if model == "" {
				return
			}
			key := resolveUsageBillingRequestID(c.Request.Context(), "")
			// Connection IDs are not request IDs. The turn marker changes before each
			// turn's first upstream send and deduplicates retry-exhausted summary events.
			if strings.EqualFold(c.GetHeader("Upgrade"), "websocket") {
				key = "ws:" + c.GetString(passiveQualityTurnKey)
			}
			if key == "" {
				key = c.GetString(passiveQualityTurnKey)
			}
			started, _ := c.Get(passiveQualityStartedKey)
			at, _ := started.(time.Time)
			if at.IsZero() {
				at = time.Now()
			}
			s.enqueue(PassiveQualityFailure{AccountID: ev.AccountID, Model: model, RequestKey: key, StartedAt: at, At: time.Now()})
		})
	}
}
func PassiveQualityBeginTurn(c *gin.Context) {
	if c != nil {
		key := uuid.NewString()
		c.Set(passiveQualityStartedKey, time.Now())
		c.Set(passiveQualityTurnKey, key)
		if c.Request != nil && strings.EqualFold(c.GetHeader("Upgrade"), "websocket") {
			c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), passiveQualityUsageTurnKey{}, "ws:"+key))
		}
	}
}
func PassiveQualityUpstreamFailure(ev OpsUpstreamErrorEvent) bool {
	// Client validation, policy refusals, cancellations and local saturation do
	// not establish bad upstream health. Explicit auth/quota/provider errors do.
	code := ev.UpstreamStatusCode
	if code == http.StatusUnauthorized || code == http.StatusPaymentRequired || code == http.StatusTooManyRequests || code >= 500 {
		return true
	}
	return code == 0 && ev.Kind == "request_error" && ev.Stage != "admission" && !strings.Contains(strings.ToLower(ev.Message), "context canceled")
}
func RecordPassiveQualityAttempt(c *gin.Context, ev OpsUpstreamErrorEvent) {
	if c == nil {
		return
	}
	if fn, ok := c.Get(passiveQualityCaptureKey); ok {
		if capture, ok := fn.(func(OpsUpstreamErrorEvent)); ok {
			capture(ev)
		}
	}
}

type passiveQualityCacheSinkKey struct{}
type passiveQualityUsageTurnKey struct{}

// Usage workers replace request deadlines with their own bounded context. Copy
// only immutable telemetry values, including the turn captured at submission.
func PassiveQualityUsageContext(parent, base context.Context) context.Context {
	if base == nil {
		base = context.Background()
	}
	if parent == nil {
		return base
	}
	for _, key := range []any{passiveQualityCacheSinkKey{}, passiveQualityUsageTurnKey{}} {
		if value := parent.Value(key); value != nil {
			base = context.WithValue(base, key, value)
		}
	}
	return base
}

// Record cache reporting presence and correlate WS usage IDs with failure turn
// IDs without changing billing identity or storing billing values.
func recordPassiveQualityCache(ctx context.Context, accountID int64, requestKey string, reported bool) {
	if ctx == nil || accountID <= 0 || requestKey == "" {
		return
	}
	attemptKey, _ := ctx.Value(passiveQualityUsageTurnKey{}).(string)
	if !reported && attemptKey == "" {
		return
	}
	if sink, ok := ctx.Value(passiveQualityCacheSinkKey{}).(func(PassiveQualityFailure)); ok {
		sink(PassiveQualityFailure{AccountID: accountID, RequestKey: requestKey, AttemptKey: attemptKey, At: time.Now(), CacheReported: reported})
	}
}

type passiveQualityProbeKey struct{}

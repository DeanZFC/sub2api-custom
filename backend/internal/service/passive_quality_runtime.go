package service

import (
	"context"
	"errors"
	"net/http"
	"net/url"
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
var ErrPassiveQualityModelRequired = errors.New("select a model for this model-isolated protection operation")

const passiveQualityCaptureKey = "passive_quality_capture"
const passiveQualityStartedKey = "passive_quality_started"
const passiveQualityTurnKey = "passive_quality_turn"
const passiveQualityAttemptGroupKey = "passive_quality_attempt_group"

// SetPassiveQualityAttemptGroup records the effective routing group without
// changing the authenticated API key (for request-local group fallbacks).
func SetPassiveQualityAttemptGroup(c *gin.Context, groupID int64) {
	if c != nil {
		c.Set(passiveQualityAttemptGroupKey, groupID)
		SetGroupQualityTrafficGroup(c, groupID)
	}
}

func passiveQualityNonTextEndpoint(endpoint string) bool {
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return false
	}
	for _, segment := range strings.Split(strings.ToLower(parsed.Path), "/") {
		if segment == "images" || segment == "videos" || segment == "audio" {
			return true
		}
	}
	return false
}

type PassiveQualityService struct {
	Repo          PassiveQualityRepository
	test          *AccountTestService
	ctx           context.Context
	cancel        context.CancelFunc
	wg            sync.WaitGroup
	queue         chan PassiveQualityFailure
	enabled       atomic.Bool
	cacheEnabled  atomic.Bool
	flushMu       sync.Mutex
	pending       []PassiveQualityFailure
	probeSlots    chan struct{}
	diagnosticsMu sync.Mutex
	diagnostics   PassiveQualityRuntimeDiagnostics
	pendingCount  atomic.Int64
	dropped       atomic.Int64
	backlogSince  atomic.Int64
	gapsMu        sync.Mutex
	gaps          map[passiveQualityGapScope]PassiveQualityTelemetryGap
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
	s.wg.Add(3)
	go s.run()
	go s.flushLoop()
	go s.maintenanceLoop()
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
	for {
		if s.ctx.Err() != nil {
			return
		}
		s.sweepCycle()
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *PassiveQualityService) sweepCycle() {
	started := time.Now()
	s.diagnosticsMu.Lock()
	s.diagnostics.LastSweepStartedAt = &started
	s.diagnosticsMu.Unlock()
	ctx, cancel := context.WithTimeout(s.ctx, 3*time.Second)
	policies, err := s.Repo.ListPolicies(ctx)
	cancel()
	policyErr := err
	// Independent scoring observes real traffic even when enforcement or scoring
	// is switched off. Keeping its bounded telemetry warm avoids a false healthy
	// window after enabling scoring, and decouples evidence from protection rules.
	_, scoreTelemetry := s.Repo.(ChannelQualityScoreProvider)
	if err == nil {
		enabled, cacheEnabled := scoreTelemetry, scoreTelemetry
		for _, p := range policies {
			enabled = enabled || p.Enabled
			cacheEnabled = cacheEnabled || (p.Enabled && p.CacheRate.Enabled)
		}
		s.enabled.Store(enabled)
		s.cacheEnabled.Store(cacheEnabled)
	} else if scoreTelemetry {
		s.enabled.Store(true)
		s.cacheEnabled.Store(true)
	}
	cutoff := time.Now()
	ready := s.flush(true)
	var probes []PassiveQualityState
	if ready {
		// Collection has its own budget; a slow flush must not consume evaluation time.
		evaluation, stop := context.WithTimeout(s.ctx, 12*time.Second)
		probes, err = s.Repo.Sweep(evaluation, cutoff, cap(s.probeSlots)-len(s.probeSlots))
		stop()
	}
	ended := time.Now()
	s.diagnosticsMu.Lock()
	s.diagnostics.LastSweepDurationMS = ended.Sub(started).Milliseconds()
	if !ready {
		s.diagnostics.SweepSkippedBacklog++
	}
	if err != nil {
		s.diagnostics.LastSweepError = err.Error()
	} else if policyErr != nil {
		s.diagnostics.LastSweepError = policyErr.Error()
	} else if ready {
		s.diagnostics.LastSweepError = ""
		s.diagnostics.LastSweepSucceededAt = &ended
	}
	s.diagnosticsMu.Unlock()
	if policyErr != nil && s.ctx.Err() == nil {
		logger.LegacyPrintf("service.passive_quality", "policy refresh failed: %v", policyErr)
	}
	if err != nil && s.ctx.Err() == nil {
		logger.LegacyPrintf("service.passive_quality", "sweep failed: %v", err)
	}
	for _, p := range probes {
		s.probeSlots <- struct{}{}
		s.wg.Add(1)
		go func(p PassiveQualityState) { defer s.wg.Done(); defer func() { <-s.probeSlots }(); s.probe(p) }(p)
	}
}

func (s *PassiveQualityService) maintenanceLoop() {
	defer s.wg.Done()
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	nextOperations := time.Time{}
	for {
		if s.ctx.Err() != nil {
			return
		}
		if repo, ok := s.Repo.(PassiveQualityTelemetryRepository); ok {
			ctx, cancel := context.WithTimeout(s.ctx, 3*time.Second)
			err := repo.PrunePassiveQuality(ctx)
			cancel()
			now := time.Now()
			s.diagnosticsMu.Lock()
			if err != nil {
				s.diagnostics.LastCleanupError = err.Error()
			} else {
				s.diagnostics.LastCleanupError = ""
				s.diagnostics.LastCleanupSucceededAt = &now
			}
			s.diagnosticsMu.Unlock()
			if err != nil && s.ctx.Err() == nil {
				logger.LegacyPrintf("service.passive_quality", "maintenance failed: %v", err)
			}
		}
		if time.Now().After(nextOperations) && s.test != nil && s.test.qualityOperations != nil {
			nextOperations = time.Now().Add(time.Hour)
			if repo, ok := s.test.qualityOperations.(interface{ PruneQualityOperations(context.Context) error }); ok {
				ctx, cancel := context.WithTimeout(s.ctx, 3*time.Second)
				err := repo.PruneQualityOperations(ctx)
				cancel()
				if err != nil && s.ctx.Err() == nil {
					logger.LegacyPrintf("service.quality_operations", "maintenance failed: %v", err)
				}
			}
		}
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
func (s *PassiveQualityService) probe(p PassiveQualityState) {
	if p.ActiveRecoveryConfig != nil && p.ActiveRecoveryConfig.IsActiveRecovery() {
		s.probeActiveRecovery(p)
		return
	}
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
	if f.At.IsZero() {
		f.At = time.Now()
	}
	s.backlogSince.CompareAndSwap(0, time.Now().UnixNano())
	select {
	case s.queue <- f:
	default:
		kind := "failure"
		if f.CacheReported || f.AttemptKey != "" || f.CacheMetadata != nil {
			kind = "cache"
		}
		s.dropped.Add(1)
		s.markGap(PassiveQualityTelemetryGap{From: f.At, To: f.At, AccountID: f.AccountID, GroupID: f.GroupID, Model: f.Model, Kind: kind, DroppedRecords: 1})
	}
}
func (s *PassiveQualityService) flush(all bool) bool {
	// One budget covers lock contention and the entire batch drain.
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	for !s.flushMu.TryLock() {
		select {
		case <-ctx.Done():
			return false
		case <-time.After(10 * time.Millisecond):
		}
	}
	defer s.flushMu.Unlock()
	rounds := 1
	if all {
		rounds = 17
	}
	for i := 0; i < rounds; i++ {
		if ctx.Err() != nil {
			return false
		}
		for len(s.pending) < 512 {
			select {
			case f := <-s.queue:
				s.pending = append(s.pending, f)
			default:
				goto drained
			}
		}
	drained:
		s.pendingCount.Store(int64(len(s.pending)))
		gaps := s.takeGaps()
		if len(s.pending) == 0 && len(gaps) == 0 {
			s.clearCollectionBacklog()
			return true
		}
		var err error
		if repo, ok := s.Repo.(PassiveQualityTelemetryRepository); ok {
			err = repo.RecordTelemetry(ctx, s.pending, gaps)
		} else {
			// Old repository implementations can only express a conservative global gap.
			var gap *time.Time
			for _, g := range gaps {
				if gap == nil || g.From.Before(*gap) {
					v := g.From
					gap = &v
				}
			}
			err = s.Repo.RecordFailures(ctx, s.pending, gap)
		}
		now := time.Now()
		s.diagnosticsMu.Lock()
		if err != nil {
			s.diagnostics.LastPersistError = err.Error()
		} else {
			s.diagnostics.LastPersistError = ""
			s.diagnostics.LastPersistedAt = &now
		}
		s.diagnosticsMu.Unlock()
		if err != nil {
			// Retain both records and loss markers until one transaction persists them.
			for _, g := range gaps {
				s.markGap(g)
			}
			// Make the failed persistence interval visible to other replicas after
			// the database recovers. The items remain queued for eventual backfill;
			// the marker errs conservatively for this window during that handoff.
			for _, f := range s.pending {
				kind := "failure"
				if f.CacheReported || f.AttemptKey != "" || f.CacheMetadata != nil {
					kind = "cache"
				}
				from := f.At
				if from.IsZero() {
					from = now
				}
				s.markGap(PassiveQualityTelemetryGap{From: from, To: now, AccountID: f.AccountID, GroupID: f.GroupID, Model: f.Model, Kind: kind})
			}
			logger.LegacyPrintf("service.passive_quality", "telemetry persistence failed: %v", err)
			return false
		}
		s.pending = nil
		s.pendingCount.Store(0)
		if !all {
			if len(s.queue) == 0 {
				s.clearCollectionBacklog()
			}
			return true
		}
	}
	ready := len(s.queue) == 0
	if ready {
		s.gapsMu.Lock()
		ready = len(s.gaps) == 0
		s.gapsMu.Unlock()
	}
	if ready {
		s.clearCollectionBacklog()
	}
	return ready
}
func (s *PassiveQualityService) clearCollectionBacklog() {
	s.backlogSince.Store(0)
	// An enqueue racing with the reset must still leave a visible backlog.
	s.gapsMu.Lock()
	hasGaps := len(s.gaps) > 0
	s.gapsMu.Unlock()
	if len(s.queue) > 0 || hasGaps {
		s.backlogSince.CompareAndSwap(0, time.Now().UnixNano())
	}
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
		c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), passiveQualityCacheCaptureEnabledKey{}, s.cacheEnabled.Load))
		c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), passiveQualityCacheSinkKey{}, func(f PassiveQualityFailure) {
			if f.AttemptKey != "" || s.cacheEnabled.Load() {
				s.enqueue(f)
			}
		}))
		PassiveQualityBeginTurn(c)
		c.Set(passiveQualityCaptureKey, func(ev OpsUpstreamErrorEvent) {
			if !s.enabled.Load() || c.Request == nil || ev.AccountID <= 0 || !PassiveQualityUpstreamFailure(ev) {
				return
			}
			// A detached upstream can return an HTTP failure after the client has
			// disconnected. Keep that independent evidence, but continue rejecting
			// cancellation-derived transport errors and logical stream statuses.
			if c.Request.Context().Err() != nil && (!ev.upstreamHTTPResponseObserved || ev.UpstreamStatusCode == 0) {
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
			// Admission, usage correlation, and retry failures share a quality turn
			// identity. Billing IDs remain independent, including upstream WS IDs.
			key, _ := c.Request.Context().Value(passiveQualityUsageTurnKey{}).(string)
			if key == "" {
				key = c.GetString(passiveQualityTurnKey)
			}
			started, _ := c.Get(passiveQualityStartedKey)
			at, _ := started.(time.Time)
			if at.IsZero() {
				at = time.Now()
			}
			groupID := int64(0)
			if override, ok := c.Get(passiveQualityAttemptGroupKey); ok {
				groupID, _ = override.(int64)
			} else if raw, ok := c.Get("api_key"); ok {
				if key, ok := raw.(*APIKey); ok && key != nil && key.GroupID != nil {
					groupID = *key.GroupID
				}
			}
			nonText := passiveQualityNonTextEndpoint(c.Request.URL.Path) || passiveQualityNonTextEndpoint(ev.UpstreamURL)
			s.enqueue(PassiveQualityFailure{AccountID: ev.AccountID, GroupID: groupID, NonText: nonText, Model: model, RequestKey: key, StartedAt: at, At: time.Now()})
		})
	}
}
func PassiveQualityBeginTurn(c *gin.Context) {
	if c != nil {
		key := uuid.NewString()
		startedAt := time.Now()
		c.Set(passiveQualityStartedKey, startedAt)
		c.Set(passiveQualityTurnKey, key)
		if c.Request != nil && strings.EqualFold(c.GetHeader("Upgrade"), "websocket") {
			key = "ws:" + key
		}
		beginGroupQualityTrafficTurn(c, key)
		if c.Request != nil {
			c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), passiveQualityCacheMetadataKey{}, (*PassiveQualityCacheMetadata)(nil)))
			c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), passiveQualityUsageTurnKey{}, key))
			c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), passiveQualityUsageStartedKey{}, startedAt))
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
type passiveQualityUsageStartedKey struct{}

// Usage workers replace request deadlines with their own bounded context. Copy
// only immutable telemetry values, including the turn captured at submission.
func PassiveQualityUsageContext(parent, base context.Context) context.Context {
	if base == nil {
		base = context.Background()
	}
	if parent == nil {
		return base
	}
	for _, key := range []any{passiveQualityCacheSinkKey{}, passiveQualityUsageTurnKey{}, passiveQualityUsageStartedKey{}, passiveQualityCacheMetadataKey{}} {
		if value := parent.Value(key); value != nil {
			base = context.WithValue(base, key, value)
		}
	}
	return freezeGroupQualityTrafficContext(parent, base)
}

// Record cache reporting presence and correlate billing IDs with quality turn
// IDs without changing billing identity or storing billing values.
func recordPassiveQualityCache(ctx context.Context, accountID int64, requestKey string, reported bool) {
	if ctx == nil || accountID <= 0 || requestKey == "" {
		return
	}
	attemptKey, _ := ctx.Value(passiveQualityUsageTurnKey{}).(string)
	startedAt, _ := ctx.Value(passiveQualityUsageStartedKey{}).(time.Time)
	metadata, _ := ctx.Value(passiveQualityCacheMetadataKey{}).(*PassiveQualityCacheMetadata)
	if metadata != nil && metadata.AccountID != accountID {
		metadata = nil
	}
	if !reported && attemptKey == "" && metadata == nil {
		return
	}
	if sink, ok := ctx.Value(passiveQualityCacheSinkKey{}).(func(PassiveQualityFailure)); ok {
		model, _ := RequestedPublicModelFromContext(ctx)
		traffic, _ := GroupQualityTrafficRequestFromContext(ctx)
		if model == "" {
			model = traffic.Model
		}
		sink(PassiveQualityFailure{AccountID: accountID, GroupID: traffic.GroupID, Model: model, RequestKey: requestKey, AttemptKey: attemptKey, StartedAt: startedAt, At: time.Now(), CacheReported: reported, CacheMetadata: metadata})
	}
}

type passiveQualityProbeKey struct{}

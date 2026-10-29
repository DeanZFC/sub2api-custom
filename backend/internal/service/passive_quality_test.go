package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func passiveTestConfig() PassiveQualityConfig {
	return PassiveQualityConfig{Name: "Quality", Enabled: true, Mode: "enforce", GroupIDs: []int64{8}, Model: "model", Scope: "model", WindowSeconds: 600, MaxSamples: 100, MinSamples: 5, Confirmations: 2, NewSamples: 2, ErrorRate: PassiveQualityMetric{Enabled: true, Pause: 20, Recover: 5}, CooldownSeconds: 30, MaxCooldownSeconds: 300, TrialSeconds: 60, TrialRequests: 10, TrialConcurrency: 1, RecoverySamples: 5}
}
func TestPassiveQualityEvidenceAndThresholds(t *testing.T) {
	now := time.Now()
	var samples []PassiveQualitySample
	for i := 0; i < 10; i++ {
		ms := float64((10 - i) * 100)
		samples = append(samples, PassiveQualitySample{At: now.Add(-time.Duration(i) * time.Second), TTFT: &ms, CacheInput: 100, CacheRead: 25})
	}
	samples[0].Failed = true
	samples[1].Failed = true
	e := PassiveQualitySummarize(samples, now.Add(-2*time.Second), true)
	require.Equal(t, 2, e.Fresh)
	require.Equal(t, 2, e.ConsecutiveFailures)
	require.InDelta(t, 20, *e.ErrorRate, .001)
	require.Equal(t, 800.0, *e.TTFT)
	require.Equal(t, 8, e.TTFTSamples)
	require.Equal(t, 25.0, *e.CacheRate)
	c := passiveTestConfig()
	verdict, _ := EvaluatePassiveQuality(c, e, false)
	require.Equal(t, "fail", verdict)
	e.ErrorRate = new(float64(5))
	verdict, _ = EvaluatePassiveQuality(c, e, true)
	require.Equal(t, "pass", verdict)
	c.TTFT = PassiveQualityMetric{Enabled: true, Pause: 1000, Recover: 800}
	e.TTFTSamples = 4
	verdict, _ = EvaluatePassiveQuality(c, e, true)
	require.Equal(t, "pending", verdict)
	e.Complete = false
	*e.ErrorRate = 100
	verdict, _ = EvaluatePassiveQuality(c, e, false)
	require.Equal(t, "pending", verdict)
	require.Nil(t, PassiveQualitySummarize(nil, time.Time{}, true).CacheRate)
	c.CacheRate = PassiveQualityMetric{Enabled: true, Pause: 20, Recover: 30}
	e.Complete = true
	e.ErrorRate = new(float64(0))
	e.TTFTSamples = 8
	verdict, _ = EvaluatePassiveQuality(c, e, true)
	require.Equal(t, "fail", verdict)
}
func TestPassiveQualityValidation(t *testing.T) {
	require.NoError(t, func() error { c := passiveTestConfig(); return c.Validate() }())
	for _, edit := range []func(*PassiveQualityConfig){func(c *PassiveQualityConfig) { c.GroupIDs = []int64{8, 8} }, func(c *PassiveQualityConfig) { c.RecoverySamples = c.TrialRequests + 1 }, func(c *PassiveQualityConfig) { c.ErrorRate.Recover = c.ErrorRate.Pause }, func(c *PassiveQualityConfig) { c.ErrorRate.Enabled = false }, func(c *PassiveQualityConfig) {
		c.CacheRate = PassiveQualityMetric{Enabled: true, Pause: 30, Recover: 20}
	}, func(c *PassiveQualityConfig) { c.Mode = "resume" }} {
		c := passiveTestConfig()
		edit(&c)
		require.Error(t, c.Validate())
	}
}

func TestPassiveQualityOptionalModel(t *testing.T) {
	for _, scope := range []string{"", "model", "account"} {
		c := passiveTestConfig()
		c.Model, c.Scope = "  ", scope
		require.NoError(t, c.Validate())
		require.Empty(t, c.Model)
		require.Equal(t, "account", c.Scope)
	}
	c := passiveTestConfig()
	c.Model = " public-model "
	require.NoError(t, c.Validate())
	require.Equal(t, "public-model", c.Model)
	require.Equal(t, "model", c.Scope)
	c.Model, c.Scope = "", "invalid"
	require.Error(t, c.Validate())
}

func TestPassiveQualityProbeModelFromEvidence(t *testing.T) {
	samples := []PassiveQualitySample{{Model: "newest-success"}, {Model: "failed-model", Failed: true}, {Model: "older-failure", Failed: true}}
	require.Equal(t, "failed-model", PassiveQualitySummarize(samples, time.Time{}, true).ProbeModel)
	require.Equal(t, "newest-success", PassiveQualitySummarize(samples[:1], time.Time{}, true).ProbeModel)
}

func TestPassiveQualityV2ModelsAreIndependentByDefault(t *testing.T) {
	for _, model := range []string{"", "model-a"} {
		for _, scope := range []string{"", "model", "account"} {
			c := passiveTestConfig()
			c.SchemaVersion, c.Model, c.Scope = 2, model, scope
			require.NoError(t, c.Validate())
			if scope == "account" {
				require.Equal(t, "account", c.Scope)
				require.False(t, c.PerModel())
			} else {
				require.Equal(t, "model", c.Scope)
				require.True(t, c.PerModel())
			}
		}
	}
}

func TestPassiveQualityAllModelTrialPrefilter(t *testing.T) {
	all := map[string]any{"scope": "account", "model": "", "phase": "trial", "used": float64(0), "max_requests": float64(5), "trial_ends_at": time.Now().Add(time.Minute).Format(time.RFC3339Nano)}
	a := &Account{Status: StatusActive, Schedulable: true, Extra: map[string]any{"passive_quality": []any{all}}}
	require.False(t, a.PassiveQualityBlocked("A"))
	require.False(t, a.PassiveQualityBlocked("B"))
	all["used"] = float64(5)
	require.True(t, a.PassiveQualityBlocked("A"))
	require.True(t, a.PassiveQualityBlocked("B"))
	all["used"] = float64(0)
	filtered := map[string]any{"scope": "account", "model": "A", "phase": "trial", "trial_ends_at": all["trial_ends_at"]}
	a.Extra["passive_quality"] = []any{all, filtered}
	require.False(t, a.PassiveQualityBlocked("A"))
	require.True(t, a.PassiveQualityBlocked("B"), "an unfiltered trial cannot bypass a filtered hold")
	all["phase"] = "paused"
	require.True(t, a.PassiveQualityBlocked("A"))
	require.True(t, a.PassiveQualityBlocked("B"))
}
func TestPassiveQualityBackoffAndModelGate(t *testing.T) {
	c := passiveTestConfig()
	s := PassiveQualityState{AccountID: 12}
	now := time.Now()
	serviceEnd := now
	for i := 0; i < 12; i++ {
		PassiveQualityPause(c, &s, now, "failed")
		require.GreaterOrEqual(t, s.NextAt.Sub(now), serviceEnd.Sub(now))
		require.LessOrEqual(t, s.NextAt.Sub(now), 300*time.Second)
		serviceEnd = s.NextAt
	}
	a := &Account{Status: StatusActive, Schedulable: true, Extra: map[string]any{}}
	set := func(scope, phase string, end time.Time) {
		raw, _ := json.Marshal([]map[string]any{{"scope": scope, "model": "model", "phase": phase, "trial_ends_at": end}})
		var holds any
		require.NoError(t, json.Unmarshal(raw, &holds))
		a.Extra["passive_quality"] = holds
	}
	set("model", "paused", now)
	require.True(t, a.IsSchedulable())
	require.False(t, a.IsSchedulableForModel("model"))
	require.True(t, a.IsSchedulableForModel("other"))
	set("account", "paused", now)
	require.False(t, a.IsSchedulable())
	set("model", "trial", now.Add(time.Minute))
	require.True(t, a.IsSchedulableForModel("model"))
	set("model", "trial", now.Add(-time.Minute))
	require.False(t, a.IsSchedulableForModel("model"))
}
func TestPassiveQualityCaptureIndependentOfOpsAndDeduplicable(t *testing.T) {
	s := NewPassiveQualityService(nil, nil)
	defer s.cancel()
	s.enabled.Store(true)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/responses", nil)
	ctx := context.WithValue(c.Request.Context(), ctxkey.Model, "model")
	ctx = context.WithValue(ctx, ctxkey.ClientRequestID, "local-uuid")
	c.Request = c.Request.WithContext(ctx)
	s.Capture()(c)
	appendOpsUpstreamError(c, OpsUpstreamErrorEvent{AccountID: 1, UpstreamStatusCode: 503, Kind: "http_error"})
	appendOpsUpstreamError(c, OpsUpstreamErrorEvent{AccountID: 1, UpstreamStatusCode: 503, Kind: "retry_exhausted"})
	a, b := <-s.queue, <-s.queue
	require.Equal(t, a.RequestKey, b.RequestKey)
	require.Equal(t, c.GetString(passiveQualityTurnKey), a.RequestKey)
	appendOpsUpstreamError(c, OpsUpstreamErrorEvent{AccountID: 2, UpstreamStatusCode: 400, Kind: "http_error"})
	require.Empty(t, s.queue)
	c.Request.Header.Set("Upgrade", "websocket")
	PassiveQualityBeginTurn(c)
	appendOpsUpstreamError(c, OpsUpstreamErrorEvent{AccountID: 1, UpstreamStatusCode: 503})
	a = <-s.queue
	PassiveQualityBeginTurn(c)
	appendOpsUpstreamError(c, OpsUpstreamErrorEvent{AccountID: 1, UpstreamStatusCode: 503})
	b = <-s.queue
	require.NotEqual(t, a.RequestKey, b.RequestKey)
}

func TestPassiveQualityCacheReportingPresence(t *testing.T) {
	for _, tc := range []struct {
		body     string
		reported bool
	}{{`{"usage":{"input_tokens":20,"output_tokens":1}}`, false}, {`{"usage":{"input_tokens":20,"input_tokens_details":{"cached_tokens":0}}}`, true}, {`{"usage":{"input_tokens":20,"input_tokens_details":{"cached_tokens":10}}}`, true}} {
		usage, ok := extractOpenAIUsageFromJSONBytes([]byte(tc.body))
		require.True(t, ok)
		require.Equal(t, tc.reported, usage.CacheUsageReported)
	}
	var usage ClaudeUsage
	parseSSEUsagePassthrough(`{"type":"message_start","message":{"usage":{"input_tokens":10}}}`, &usage)
	require.False(t, usage.CacheUsageReported)
	parseSSEUsagePassthrough(`{"type":"message_start","message":{"usage":{"input_tokens":10,"cache_read_input_tokens":0}}}`, &usage)
	require.True(t, usage.CacheUsageReported)
}

func TestPassiveQualityUsageWorkerRetainsReportingAndFrozenTurn(t *testing.T) {
	s := NewPassiveQualityService(nil, nil)
	defer s.cancel()
	s.ConfigurationChanged()
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/responses", nil)
	s.Capture()(c)
	worker := PassiveQualityUsageContext(c.Request.Context(), context.Background())
	recordPassiveQualityCache(worker, 62, "client:zero", true)
	zero := <-s.queue
	require.True(t, zero.CacheReported)
	require.Equal(t, c.GetString(passiveQualityTurnKey), zero.AttemptKey)

	c.Request.Header.Set("Upgrade", "websocket")
	PassiveQualityBeginTurn(c)
	firstKey := "ws:" + c.GetString(passiveQualityTurnKey)
	worker = PassiveQualityUsageContext(c.Request.Context(), context.Background())
	PassiveQualityBeginTurn(c)
	// A queued first-turn bill must retain that turn even after a new request.
	s.cacheEnabled.Store(false)
	recordPassiveQualityCache(worker, 62, "resp_first", false)
	first := <-s.queue
	require.Equal(t, firstKey, first.AttemptKey)
	require.Equal(t, "resp_first", first.RequestKey)
	require.False(t, first.CacheReported)
	require.NotEqual(t, "ws:"+c.GetString(passiveQualityTurnKey), first.AttemptKey)
}

func TestPassiveQualityTrafficAndOutcomesShareTurnIdentity(t *testing.T) {
	for _, websocket := range []bool{false, true} {
		t.Run(fmt.Sprint(websocket), func(t *testing.T) {
			s := NewPassiveQualityService(nil, nil)
			defer s.cancel()
			s.enabled.Store(true)
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest("POST", "/v1/responses", nil)
			if websocket {
				c.Request.Header.Set("Upgrade", "websocket")
			}
			c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), ctxkey.Model, "model"))
			s.Capture()(c)
			SetGroupQualityTrafficGroup(c, 8)
			SetGroupQualityTrafficModel(c.Request.Context(), "model")
			traffic, ok := GroupQualityTrafficRequestFromContext(c.Request.Context())
			require.True(t, ok)
			worker := PassiveQualityUsageContext(c.Request.Context(), context.Background())
			for _, account := range []int64{62, 63} {
				appendOpsUpstreamError(c, OpsUpstreamErrorEvent{AccountID: account, UpstreamStatusCode: 503})
				failure := <-s.queue
				require.Equal(t, traffic.Key, failure.RequestKey)
				require.Equal(t, account, failure.AccountID)
			}
			PassiveQualityBeginTurn(c)
			next, ok := GroupQualityTrafficRequestFromContext(c.Request.Context())
			require.True(t, ok)
			require.NotEqual(t, traffic.Key, next.Key)
			// A late HTTP bill or WS turn keeps its original admission identity,
			// including responses without cache reporting and disabled cache policy.
			recordPassiveQualityCache(worker, 63, "upstream-billing-id", false)
			usage := <-s.queue
			require.Equal(t, traffic.Key, usage.AttemptKey)
			require.Equal(t, "upstream-billing-id", usage.RequestKey)
			require.False(t, usage.CacheReported)
			frozen, ok := GroupQualityTrafficRequestFromContext(worker)
			require.True(t, ok)
			require.Equal(t, traffic.Key, frozen.Key)
		})
	}
}

type passiveAdmissionTestRepo struct {
	PassiveQualityRepository
	allow    bool
	releases []bool
	model    string
}

func (r *passiveAdmissionTestRepo) AcquirePassiveQuality(_ context.Context, _ int64, model, _ string) (bool, bool, error) {
	r.model = model
	return r.allow, r.allow, nil
}
func (r *passiveAdmissionTestRepo) ReleasePassiveQuality(_ context.Context, _ string, unused bool) error {
	r.releases = append(r.releases, unused)
	return nil
}
func TestPassiveQualityAdmissionRefundsIfActiveProtectionDenies(t *testing.T) {
	repo := &passiveAdmissionTestRepo{allow: true}
	svc := NewConcurrencyService(nil)
	svc.passiveQualityGate = repo
	svc.SetCacheRecoveryGate(cacheRecoveryGateFunc(func(context.Context, int64) (bool, error) { return false, nil }))
	released := 0
	ctx := context.WithValue(context.Background(), ctxkey.Model, "model")
	_, err := svc.admitCacheRecoveryRequest(ctx, 62, &AcquireResult{Acquired: true, ReleaseFunc: func() { released++ }})
	require.ErrorIs(t, err, ErrAccountAdmissionDenied)
	require.Equal(t, 1, released)
	require.Equal(t, []bool{true}, repo.releases)
	require.Equal(t, "model", repo.model)
	svc.SetCacheRecoveryGate(nil)
	repo.releases = nil
	result, err := svc.admitCacheRecoveryRequest(ctx, 62, &AcquireResult{Acquired: true})
	require.NoError(t, err)
	result.ReleaseFunc()
	result.ReleaseFunc()
	require.Equal(t, []bool{false}, repo.releases)
}

func TestPassiveQualityOverlappingAccountTrialPrefilter(t *testing.T) {
	a := &Account{Status: StatusActive, Schedulable: true}
	data := `[{"scope":"account","model":"A","phase":"trial","used":5,"max_requests":5,"trial_ends_at":"` + time.Now().Add(time.Minute).Format(time.RFC3339) + `"},{"scope":"account","model":"B","phase":"trial","used":0,"max_requests":5,"trial_ends_at":"` + time.Now().Add(time.Minute).Format(time.RFC3339) + `"}]`
	var holds any
	require.NoError(t, json.Unmarshal([]byte(data), &holds))
	a.Extra = map[string]any{"passive_quality": holds}
	require.True(t, a.IsSchedulable())
	require.True(t, a.PassiveQualityBlocked("A"))
	require.False(t, a.PassiveQualityBlocked("B"))
	require.True(t, a.PassiveQualityBlocked("C"))
}

func TestPassiveQualityLimitValidationIgnoresProbeBudget(t *testing.T) {
	c := passiveTestConfig()
	c.Action = "limit"
	c.ConcurrencyLimit = 2
	c.RecoverySamples = 50
	c.TrialRequests = 0
	c.CooldownSeconds = 0
	require.NoError(t, c.Validate())
	c.ConcurrencyLimit = 0
	require.Error(t, c.Validate())
	c.ConcurrencyLimit = 2
	c.PriorityPenalty = -1
	require.Error(t, c.Validate())
	c.PriorityPenalty = 0
	c.Action = "unexpected"
	require.Error(t, c.Validate())
}

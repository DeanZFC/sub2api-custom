package service

import (
	"context"
	"encoding/json"
	"math"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func independentGroupTargetConfig(t *testing.T, purpose string, threshold float64) PassiveQualityConfig {
	t.Helper()
	c := PassiveQualityConfig{Name: "group target", Enabled: true, Mode: "enforce", GroupIDs: []int64{8}, CacheRate: PassiveQualityMetric{Enabled: true, Pause: threshold}}
	// Decode the public API shape to exercise purpose dispatch independently of
	// the editor's preset builder and avoid coupling to its implementation.
	raw, err := json.Marshal(map[string]string{"purpose": purpose})
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(raw, &c))
	return c
}

func TestGroupQualityTargetIndependentMinimalConfiguration(t *testing.T) {
	for _, purpose := range []string{"group_quality_target", "cache_target"} {
		for _, target := range []float64{70, 80} {
			c := independentGroupTargetConfig(t, purpose, target)
			require.NoError(t, c.Validate())
			require.True(t, c.IsGroupQualityTarget())
			require.True(t, c.PerModel(), "an omitted model must discover separate model states")
			require.Equal(t, 2, c.SchemaVersion)
			require.Equal(t, "model", c.Scope)
			require.Equal(t, "pause", c.Action)
			require.Empty(t, c.Model)
			require.Nil(t, c.CacheProtection, "the target must not silently become soft reuse protection")
			require.False(t, c.ErrorRate.Enabled)
			require.False(t, c.TTFT.Enabled)
			require.Equal(t, target+5, c.CacheRate.Recover)
			require.GreaterOrEqual(t, c.RecoverySamples, 5)
			require.Zero(t, c.TrialRequests)
			require.Zero(t, c.TrialConcurrency)
			require.Zero(t, c.TrialSeconds)
		}
	}
}

func TestGroupQualityTargetDiscardsCustomerTrialLimits(t *testing.T) {
	c := independentGroupTargetConfig(t, "group_quality_target", 80)
	c.TrialRequests, c.TrialSeconds, c.TrialConcurrency = 1, 1, 1
	require.NoError(t, c.Validate())
	require.Zero(t, c.TrialRequests)
	require.Zero(t, c.TrialSeconds)
	require.Zero(t, c.TrialConcurrency)
	require.Equal(t, 5, c.RecoverySamples)
	require.Equal(t, 2, c.Confirmations)
}

func TestGroupQualityTargetTrafficModeDefaultsAndValidation(t *testing.T) {
	defaultMode := independentGroupTargetConfig(t, "group_quality_target", 80)
	require.NoError(t, defaultMode.Validate())
	require.Equal(t, "strict", defaultMode.TrafficMode)

	for _, mode := range []string{"availability", "bypass"} {
		c := independentGroupTargetConfig(t, "group_quality_target", 80)
		c.TrafficMode = mode
		require.ErrorContains(t, c.Validate(), "do not support customer recovery traffic")
	}

	invalid := independentGroupTargetConfig(t, "group_quality_target", 80)
	invalid.RecoveryMode = "traffic"
	require.ErrorContains(t, invalid.Validate(), "require active recovery")
}

func TestGroupQualityTargetIndependentRejectsAmbiguousOrImpossibleScope(t *testing.T) {
	cases := map[string]func(*PassiveQualityConfig){
		"no group":          func(c *PassiveQualityConfig) { c.GroupIDs = nil },
		"multiple groups":   func(c *PassiveQualityConfig) { c.GroupIDs = []int64{8, 9} },
		"account scope":     func(c *PassiveQualityConfig) { c.Scope = "account" },
		"soft action":       func(c *PassiveQualityConfig) { c.Action = "limit" },
		"legacy schema":     func(c *PassiveQualityConfig) { c.SchemaVersion = 1 },
		"no metric":         func(c *PassiveQualityConfig) { c.CacheRate.Enabled = false },
		"unachievable goal": func(c *PassiveQualityConfig) { c.CacheRate.Pause = 100 },
		"nonfinite goal":    func(c *PassiveQualityConfig) { c.CacheRate.Pause = math.NaN() },
		"reverse recovery":  func(c *PassiveQualityConfig) { c.CacheRate.Recover = 79 },
		"infinite recovery": func(c *PassiveQualityConfig) { c.CacheRate.Recover = math.Inf(1) },
		"insufficient active budget": func(c *PassiveQualityConfig) {
			c.RecoverySamples = 20
		},
		"reuse config": func(c *PassiveQualityConfig) {
			p := DefaultPassiveQualityCacheConfig()
			c.CacheProtection = &p
		},
	}
	for name, edit := range cases {
		t.Run(name, func(t *testing.T) {
			c := independentGroupTargetConfig(t, "group_quality_target", 80)
			edit(&c)
			require.Error(t, c.Validate())
		})
	}
}

func TestGroupQualityTargetIndependentThresholdBoundaries(t *testing.T) {
	for _, target := range []float64{70, 80} {
		c := independentGroupTargetConfig(t, "group_quality_target", target)
		require.NoError(t, c.Validate())
		for _, tc := range []struct {
			value    float64
			recovery bool
			want     string
		}{
			{target - 0.01, false, "fail"},
			{target, false, "pass"},
			{target + 0.01, false, "pass"},
			{target + 4.99, true, "fail"},
			{target + 5, true, "pass"},
		} {
			e := PassiveQualityEvidence{Complete: true, Samples: 20, CacheSamples: 20, CacheRate: &tc.value}
			got, _ := EvaluatePassiveQuality(c, e, tc.recovery)
			require.Equal(t, tc.want, got, "target=%v value=%v recovery=%v", target, tc.value, tc.recovery)
		}
	}
}

func TestGroupQualityTargetIndependentUnknownZeroAndWeightedEvidence(t *testing.T) {
	c := independentGroupTargetConfig(t, "group_quality_target", 80)
	require.NoError(t, c.Validate())
	now := time.Date(2026, 10, 24, 0, 0, 0, 0, time.UTC)
	samples := make([]PassiveQualitySample, 20)
	for i := range samples {
		samples[i] = PassiveQualitySample{At: now.Add(-time.Duration(i) * time.Second), Model: "sol"}
	}
	e := PassiveQualitySummarize(samples, now.Add(-time.Minute), true)
	require.Nil(t, e.CacheRate)
	verdict, reason := EvaluatePassiveQuality(c, e, false)
	require.Equal(t, "pending", verdict)
	require.Equal(t, "insufficient_metric_samples", reason)
	for i := range samples {
		samples[i].CacheReported = true
		samples[i].CacheInput = 100
	}
	e = PassiveQualitySummarize(samples, now.Add(-time.Minute), true)
	require.NotNil(t, e.CacheRate)
	require.Zero(t, *e.CacheRate, "explicit zero is evidence, not missing telemetry")
	verdict, _ = EvaluatePassiveQuality(c, e, false)
	require.Equal(t, "fail", verdict)
	// Nineteen tiny perfect requests do not compensate for one large cold request.
	for i := 0; i < 19; i++ {
		samples[i].CacheInput, samples[i].CacheRead = 1, 1
	}
	samples[19].CacheInput = 981
	e = PassiveQualitySummarize(samples, now, true)
	require.Equal(t, int64(1000), e.CacheInputTokens)
	require.Equal(t, int64(19), e.CacheReadTokens)
	require.InDelta(t, 1.9, *e.CacheRate, 0.000001)
	require.Zero(t, e.Fresh, "re-reading identical evidence cannot supply fresh confirmations")
	verdict, _ = EvaluatePassiveQuality(c, e, false)
	require.Equal(t, "fail", verdict)
	e.Complete = false
	verdict, reason = EvaluatePassiveQuality(c, e, false)
	require.Equal(t, "pending", verdict)
	require.Equal(t, "telemetry_incomplete", reason)
	verdict, _ = EvaluatePassiveQuality(c, e, true)
	require.Equal(t, "pending", verdict, "telemetry gaps cannot establish recovery")
}

func TestGroupQualityTargetIndependentEveryEnabledMetricMustRecover(t *testing.T) {
	c := independentGroupTargetConfig(t, "group_quality_target", 80)
	c.ErrorRate = PassiveQualityMetric{Enabled: true, Pause: 20, Recover: 5}
	c.TTFT = PassiveQualityMetric{Enabled: true, Pause: 1000, Recover: 800}
	require.NoError(t, c.Validate())
	cache, errorRate, latency := 90.0, 0.0, 700.0
	e := PassiveQualityEvidence{Complete: true, Samples: 20, CacheSamples: 20, TTFTSamples: 20, CacheRate: &cache, ErrorRate: &errorRate, TTFT: &latency}
	verdict, _ := EvaluatePassiveQuality(c, e, true)
	require.Equal(t, "pass", verdict)
	errorRate = 6
	verdict, _ = EvaluatePassiveQuality(c, e, true)
	require.Equal(t, "fail", verdict)
	errorRate, e.TTFT = 0, nil
	verdict, _ = EvaluatePassiveQuality(c, e, true)
	require.Equal(t, "pending", verdict, "cache recovery cannot hide missing latency evidence")
}

func TestGroupQualityTargetIndependentAvailabilityAndLatencyInclusiveBoundaries(t *testing.T) {
	for _, tc := range []struct {
		availability float64
		latency      float64
	}{{80, 10000}, {70, 20000}} {
		c := independentGroupTargetConfig(t, "group_quality_target", tc.availability)
		c.ErrorRate = PassiveQualityMetric{Enabled: true, Pause: 100 - tc.availability}
		c.TTFT = PassiveQualityMetric{Enabled: true, Pause: tc.latency}
		require.NoError(t, c.Validate())
		cache, errorRate, latency := 99.0, 100-tc.availability, tc.latency-1
		e := PassiveQualityEvidence{Complete: true, Samples: 20, CacheSamples: 20, TTFTSamples: 20, CacheRate: &cache, ErrorRate: &errorRate, TTFT: &latency}
		verdict, _ := EvaluatePassiveQuality(c, e, false)
		require.Equal(t, "pass", verdict, "availability at the target passes")
		errorRate += 0.01
		verdict, reason := EvaluatePassiveQuality(c, e, false)
		require.Equal(t, "fail", verdict)
		require.Contains(t, reason, "error_rate:")
		errorRate -= 0.02
		verdict, _ = EvaluatePassiveQuality(c, e, false)
		require.Equal(t, "pass", verdict)
		latency = tc.latency
		verdict, _ = EvaluatePassiveQuality(c, e, false)
		require.Equal(t, "pass", verdict, "TTFT at the target passes")
		latency++
		verdict, reason = EvaluatePassiveQuality(c, e, false)
		require.Equal(t, "fail", verdict)
		require.Contains(t, reason, "ttft_p90:")
	}
}

func TestGroupQualityTargetIndependentFailureCaptureKeepsGroupIdentity(t *testing.T) {
	s := NewPassiveQualityService(nil, nil)
	defer s.cancel()
	s.enabled.Store(true)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/responses", nil)
	c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), ctxkey.Model, "sol"))
	groupID := int64(8)
	c.Set("api_key", &APIKey{GroupID: &groupID})
	s.Capture()(c)
	appendOpsUpstreamError(c, OpsUpstreamErrorEvent{AccountID: 62, UpstreamStatusCode: 503, Kind: "http_error"})
	event := <-s.queue
	require.Equal(t, groupID, event.GroupID)
	require.Equal(t, int64(62), event.AccountID)
	require.Equal(t, "sol", event.Model)
	c.Set("api_key", &APIKey{})
	appendOpsUpstreamError(c, OpsUpstreamErrorEvent{AccountID: 63, UpstreamStatusCode: 503, Kind: "http_error"})
	require.Zero(t, (<-s.queue).GroupID, "missing group identity must remain unknown")
}

func TestGroupQualityTargetIndependentSnapshotHoldsComposeByModel(t *testing.T) {
	trial := map[string]any{"policy_id": float64(1), "purpose": "group_quality_target", "scope": "model", "model": "sol", "phase": "trial", "used": float64(0), "max_requests": float64(20), "trial_ends_at": time.Now().Add(time.Minute).Format(time.RFC3339Nano)}
	paused := map[string]any{"policy_id": float64(2), "scope": "model", "model": "sol", "phase": "paused"}
	a := &Account{Extra: map[string]any{"passive_quality": []any{trial, paused}}}
	require.True(t, a.PassiveQualityBlocked("sol"), "one recovered policy cannot override another policy's pause")
	require.False(t, a.PassiveQualityBlocked("astra"), "another model retains its independent eligibility")
	a.Extra["passive_quality"] = []any{trial}
	require.True(t, a.PassiveQualityBlocked("sol"), "retired group trial snapshots cannot authorize customer requests")
	trial["used"] = float64(20)
	require.True(t, a.PassiveQualityBlocked("sol"), "exhausting the real-traffic trial budget must close the snapshot gate")
	require.False(t, a.PassiveQualityBlocked("astra"))
}

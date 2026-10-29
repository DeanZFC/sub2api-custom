package service

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai_compat"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func recoveryFloat(v float64) *float64 { return &v }

func activeRecoveryFixture() PassiveQualityState {
	return PassiveQualityState{AccountID: 42, Model: "public-model", ActiveRecoveryConfig: &PassiveQualityConfig{
		Confirmations: 2, RecoverySamples: 5, ConsecutiveFailures: 3,
		ErrorRate: PassiveQualityMetric{Enabled: true, Recover: 5},
		TTFT:      PassiveQualityMetric{Enabled: true, Recover: 2000},
		TPS:       PassiveQualityMetric{Enabled: true, Recover: 40},
		CacheRate: PassiveQualityMetric{Enabled: true, Recover: 85},
	}}
}

func activeRecoverySuccess(at time.Time, cached int64) *ScheduledTestResult {
	return &ScheduledTestResult{Status: "success", ResponseText: "A complete answer", FinishedAt: at, ExecutionInfo: &ScheduledTestExecutionInfo{Attempts: []ScheduledTestRequestAttempt{{
		StartedAt: at.Add(-3 * time.Second), HTTPStatus: 200, qualityUsageFinal: true, qualityStream: true, qualityTerminal: true,
		FirstTokenMS: recoveryFloat(500), DurationMS: recoveryFloat(3000),
		ScheduledTestTokenUsage: ScheduledTestTokenUsage{InputTokens: usageInt(1000), OutputTokens: usageInt(256), CacheReadTokens: usageInt(cached)},
	}}}}
}

func TestPassiveActiveRecoveryUsesIndependentWarmBatches(t *testing.T) {
	now := time.Date(2026, 10, 29, 1, 0, 0, 0, time.UTC)
	var sessions, prompts []string
	runner := passiveActiveRecoveryRunner{now: func() time.Time { return now }, wait: func(_ context.Context, d time.Duration) error { now = now.Add(d); return nil }}
	runner.request = func(ctx context.Context, id int64, model, prompt string) (*ScheduledTestResult, error) {
		require.EqualValues(t, 42, id)
		require.Equal(t, "public-model", model)
		require.Equal(t, 2048, accountTestOutputTokenLimit(ctx))
		sessions = append(sessions, passiveRecoverySession(ctx))
		prompts = append(prompts, prompt)
		now = now.Add(3 * time.Second)
		cached := int64(900)
		if len(sessions) == 1 || len(sessions) == 7 {
			cached = 0
		}
		return activeRecoverySuccess(now, cached), nil
	}
	out := runner.run(context.Background(), activeRecoveryFixture())
	require.True(t, out.Passed)
	require.Equal(t, "pass", out.Verdict)
	require.Equal(t, 12, out.Requests)
	require.Equal(t, 2, out.WarmupRequests)
	require.Equal(t, 2, out.Batches)
	require.Equal(t, 12, out.Evidence.Samples)
	require.Equal(t, 10, out.Evidence.CacheSamples)
	require.EqualValues(t, 10000, out.Evidence.CacheInputTokens)
	require.InDelta(t, 90, *out.Evidence.CacheRate, .001)
	require.Equal(t, prompts[0], prompts[5])
	require.Equal(t, sessions[0], sessions[5])
	require.NotEqual(t, prompts[0], prompts[6])
	require.NotEqual(t, sessions[0], sessions[6])
	require.GreaterOrEqual(t, out.EvidenceBatches[1].Latest.Sub(out.EvidenceBatches[0].Latest), 120*time.Second)
	for _, batch := range out.EvidenceBatches {
		require.Equal(t, 5, batch.CacheSamples)
		require.Equal(t, 6, batch.TTFTSamples)
		require.Equal(t, 6, batch.TPSSamples)
	}
}

func TestPassiveActiveRecoveryCannotMaskLaterBadBatch(t *testing.T) {
	now := time.Now()
	calls := 0
	runner := passiveActiveRecoveryRunner{now: func() time.Time { return now }, wait: func(_ context.Context, d time.Duration) error { now = now.Add(d); return nil }, request: func(context.Context, int64, string, string) (*ScheduledTestResult, error) {
		calls++
		now = now.Add(time.Second)
		cached := int64(1000)
		if calls > 6 {
			cached = 800
		}
		return activeRecoverySuccess(now, cached), nil
	}}
	out := runner.run(context.Background(), activeRecoveryFixture())
	require.Equal(t, "fail", out.Verdict)
	require.False(t, out.Passed)
	require.Equal(t, 2, out.Batches)
	require.InDelta(t, 90, *out.Evidence.CacheRate, .001, "combined evidence would hide the second batch")
	require.InDelta(t, 80, *out.EvidenceBatches[1].CacheRate, .001)
}

func TestPassiveActiveRecoveryUnknownMetricsStayPending(t *testing.T) {
	for _, metric := range []string{"cache", "ttft", "tps_short", "usage_final"} {
		t.Run(metric, func(t *testing.T) {
			now := time.Now()
			calls := 0
			runner := passiveActiveRecoveryRunner{now: func() time.Time { return now }, wait: func(context.Context, time.Duration) error { return nil }, request: func(context.Context, int64, string, string) (*ScheduledTestResult, error) {
				calls++
				now = now.Add(time.Second)
				r := activeRecoverySuccess(now, 900)
				a := &r.ExecutionInfo.Attempts[0]
				switch metric {
				case "cache":
					a.CacheReadTokens = nil
				case "ttft":
					a.FirstTokenMS = nil
				case "tps_short":
					a.OutputTokens = usageInt(127)
				case "usage_final":
					a.qualityUsageFinal = false
				}
				return r, nil
			}}
			out := runner.run(context.Background(), activeRecoveryFixture())
			require.Equal(t, "pending", out.Verdict)
			require.Equal(t, "insufficient_metric_samples", out.Reason)
			require.Equal(t, 6, calls)
		})
	}
}

func TestPassiveActiveRecoveryFailedWarmupAndMalformedStreamCannotPass(t *testing.T) {
	for _, kind := range []string{"http", "empty", "truncated", "malformed", "retry"} {
		t.Run(kind, func(t *testing.T) {
			now := time.Now()
			calls := 0
			runner := passiveActiveRecoveryRunner{now: func() time.Time { return now }, wait: func(context.Context, time.Duration) error { return nil }, request: func(context.Context, int64, string, string) (*ScheduledTestResult, error) {
				calls++
				r := activeRecoverySuccess(now, 900)
				a := &r.ExecutionInfo.Attempts[0]
				switch kind {
				case "http":
					a.HTTPStatus = 503
					r.Status = "failed"
				case "empty":
					r.ResponseText = ""
				case "truncated":
					a.qualityTerminal = false
				case "malformed":
					a.qualityMalformed = true
				case "retry":
					r.ExecutionInfo.Attempts = append([]ScheduledTestRequestAttempt{{HTTPStatus: 503}}, *a)
				}
				return r, nil
			}}
			out := runner.run(context.Background(), activeRecoveryFixture())
			require.Equal(t, "fail", out.Verdict)
			require.Equal(t, 1, calls)
			require.Positive(t, out.Evidence.Failures)
			require.Zero(t, out.Evidence.CacheSamples)
		})
	}
}

func TestPassiveActiveRecoveryDeferredCancelledAndOversized(t *testing.T) {
	now := time.Now()
	until := now.Add(time.Hour)
	calls := 0
	runner := passiveActiveRecoveryRunner{now: func() time.Time { return now }, wait: func(context.Context, time.Duration) error { return nil }, request: func(context.Context, int64, string, string) (*ScheduledTestResult, error) {
		calls++
		return &ScheduledTestResult{ExecutionInfo: &ScheduledTestExecutionInfo{FailureKind: "quality_deferred", NextCheckAt: &until}, ErrorMessage: "budget"}, nil
	}}
	out := runner.run(context.Background(), activeRecoveryFixture())
	require.Equal(t, "deferred", out.Verdict)
	require.Equal(t, &until, out.DeferredUntil)
	require.Zero(t, out.Requests)
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	out = runner.run(cancelled, activeRecoveryFixture())
	require.Equal(t, "pending", out.Verdict)
	require.Equal(t, "recovery_interrupted", out.Reason)
	p := activeRecoveryFixture()
	p.ActiveRecoveryConfig.RecoverySamples = 20
	out = runner.run(context.Background(), p)
	require.Equal(t, "recovery_configuration_exceeds_probe_budget", out.Reason)
	require.Equal(t, 1, calls)
}

func TestPassiveRecoverySamplesNormalizeProviderCacheTokens(t *testing.T) {
	now := time.Now()
	for _, exclusive := range []bool{false, true} {
		r := activeRecoverySuccess(now, 800)
		a := &r.ExecutionInfo.Attempts[0]
		if exclusive {
			a.InputTokens = usageInt(100)
			a.CacheCreationTokens = usageInt(100)
			a.qualityCacheExclusive = true
		}
		samples, reason := passiveRecoverySamples(r, nil, "model", "session:0", false, now)
		require.Empty(t, reason)
		require.EqualValues(t, 1000, samples[0].CacheInput)
		require.EqualValues(t, 800, samples[0].CacheRead)
		require.InDelta(t, 102.4, *summarizeActiveRecovery(samples).OutputTPS, .001)
	}
	r := activeRecoverySuccess(now, 0)
	samples, reason := passiveRecoverySamples(r, nil, "model", "session:1", false, now)
	require.Empty(t, reason)
	require.Equal(t, 1, summarizeActiveRecovery(samples).CacheSamples)
	require.Zero(t, *summarizeActiveRecovery(samples).CacheRate)
}

func TestPassiveRecoveryDisabledAccountNeverCallsUpstream(t *testing.T) {
	for _, status := range []string{StatusActive, "disabled", "quality_paused"} {
		t.Run(status, func(t *testing.T) {
			account := &Account{ID: 42, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: status, Schedulable: false, Credentials: map[string]any{"api_key": "test", "base_url": "https://upstream.example.com"}, Extra: map[string]any{openai_compat.ExtraKeyResponsesSupported: true}}
			upstream := &modelIdentityHTTPUpstream{}
			svc := &AccountTestService{accountRepo: &modelIdentityAccountRepo{account: account}, httpUpstream: upstream, cfg: &config.Config{}}
			ctx := context.WithValue(context.Background(), passiveRecoveryRequestKey{}, &passiveRecoveryRequest{session: "test", attempts: &atomic.Int64{}})
			r, err := svc.RunTestBackgroundWithPrompt(ctx, 42, "gpt-test", "test")
			require.NoError(t, err)
			require.True(t, QualityResultDeferred(r))
			require.Zero(t, upstream.requests)
		})
	}
}

func TestPassiveRecoveryActualAttemptCapAndStableIdentity(t *testing.T) {
	collector := &accountTestUsageCollector{}
	active := &passiveRecoveryRequest{session: "private-session", attempts: &atomic.Int64{}}
	ctx := context.WithValue(context.Background(), passiveRecoveryRequestKey{}, active)
	ctx = context.WithValue(ctx, accountTestUsageKey{}, collector)
	sends := 0
	for n := 0; n < 25; n++ {
		req, err := http.NewRequestWithContext(ctx, "POST", "https://example.invalid/v1/responses", strings.NewReader("{\"model\":\"x\",\"input\":\"synthetic\"}"))
		require.NoError(t, err)
		resp, err := executeQualityTestHTTP(req, 42, func(wire *http.Request) (*http.Response, error) {
			sends++
			var payload map[string]any
			require.NoError(t, json.NewDecoder(wire.Body).Decode(&payload))
			require.NotContains(t, payload, "prompt_cache_key")
			require.Equal(t, "private-session", wire.Header.Get("session-id"))
			return costJSONResponse(200, "{}"), nil
		})
		if n < 20 {
			require.NoError(t, err)
			_, _ = io.ReadAll(resp.Body)
			_ = resp.Body.Close()
		} else {
			require.Error(t, err)
		}
	}
	require.Equal(t, 20, sends)
	require.Len(t, collector.snapshot().Attempts, 20)
	require.NotNil(t, collector.deferred)
}

func TestPassiveRecoveryIdentityPreservesExistingFingerprint(t *testing.T) {
	active := &passiveRecoveryRequest{session: "private-session", attempts: &atomic.Int64{}}
	ctx := context.WithValue(context.Background(), passiveRecoveryRequestKey{}, active)
	for n, userID := range []string{"first-account-identity", "random-second-identity"} {
		req, err := http.NewRequestWithContext(ctx, "POST", "https://example.invalid/v1/messages", strings.NewReader("{\"metadata\":{\"user_id\":\""+userID+"\"}}"))
		require.NoError(t, err)
		req.Header.Set("session-id", "existing-fingerprint")
		require.NoError(t, applyPassiveRecoveryRequestIdentity(req, active.session))
		require.Equal(t, "existing-fingerprint", req.Header.Get("session-id"))
		var payload map[string]any
		require.NoError(t, json.NewDecoder(req.Body).Decode(&payload))
		metadata, ok := payload["metadata"].(map[string]any)
		require.True(t, ok)
		require.Equal(t, "first-account-identity", metadata["user_id"], n)
	}
}

func TestPassiveRecoveryRealFailureOutranksIncompatibleRetry(t *testing.T) {
	r := activeRecoverySuccess(time.Now(), 900)
	r.ExecutionInfo.Attempts = append([]ScheduledTestRequestAttempt{{HTTPStatus: 503}, {HTTPStatus: 400}}, r.ExecutionInfo.Attempts...)
	_, reason := passiveRecoverySamples(r, nil, "model", "session", false, time.Now())
	require.Equal(t, "active_request_failed", reason)
}

func TestPassiveRecoveryIncompatibleProbeDoesNotSilentlyRetry(t *testing.T) {
	account := &Account{ID: 42, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Credentials: map[string]any{"api_key": "test", "base_url": "https://upstream.example.com"}, Extra: map[string]any{openai_compat.ExtraKeyResponsesSupported: false}}
	upstream := &modelIdentityHTTPUpstream{responses: []*http.Response{costJSONResponse(400, "{\"error\":{\"message\":\"Unsupported synthetic probe request\"}}")}}
	budget := &qualityOperationsStub{reserve: func(context.Context, int64, int64, bool, string) (*QualityProbeReservation, error) {
		return &QualityProbeReservation{ID: 1}, nil
	}}
	svc := &AccountTestService{accountRepo: &modelIdentityAccountRepo{account: account}, httpUpstream: upstream, cfg: &config.Config{}, qualityOperations: budget}
	ctx := context.WithValue(context.Background(), passiveRecoveryRequestKey{}, &passiveRecoveryRequest{session: "private", attempts: &atomic.Int64{}})
	r, err := svc.RunTestBackgroundWithPrompt(withAccountTestMaxOutputTokens(ctx, 2048), 42, "gpt-test", "synthetic long request")
	require.NoError(t, err)
	require.Equal(t, 1, upstream.requests)
	_, reason := passiveRecoverySamples(r, nil, "gpt-test", "private", false, time.Now())
	require.Equal(t, "active_probe_incompatible", reason)
	for _, attempt := range r.ExecutionInfo.Attempts {
		require.Nil(t, attempt.CacheReadTokens)
	}
}

func TestPassiveRecoveryPreservesOptionalCacheKey(t *testing.T) {
	for _, path := range []string{"/v1/responses", "/v1/chat/completions"} {
		for _, present := range []bool{false, true} {
			payload := map[string]any{"model": "test", "input": "same synthetic prefix"}
			if present {
				payload["prompt_cache_key"] = "builder-owned-key"
			}
			body, err := json.Marshal(payload)
			require.NoError(t, err)
			req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, "https://example.invalid"+path, strings.NewReader(string(body)))
			require.NoError(t, err)
			require.NoError(t, applyPassiveRecoveryRequestIdentity(req, "stable-test-session"))
			var output map[string]any
			require.NoError(t, json.NewDecoder(req.Body).Decode(&output))
			require.Equal(t, payload, output)
			if present {
				require.Equal(t, "builder-owned-key", output["prompt_cache_key"])
			} else {
				require.NotContains(t, output, "prompt_cache_key")
			}
		}
	}
}

func TestPassiveRecoveryKnownFailureTerminalsCannotPass(t *testing.T) {
	for _, body := range []string{
		"data: {\"type\":\"response.output_text.delta\",\"delta\":\"partial\"}\n\ndata: {\"type\":\"response.failed\",\"response\":{\"error\":{\"message\":\"upstream failed\"}}}\n",
		"data: {\"type\":\"response.output_text.delta\",\"delta\":\"partial\"}\n\ndata: {\"type\":\"response.incomplete\"}\n",
	} {
		account := &Account{ID: 42, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"api_key": "test", "base_url": "https://upstream.example.com"}, Extra: map[string]any{openai_compat.ExtraKeyResponsesSupported: true}}
		upstream := &modelIdentityHTTPUpstream{body: body}
		svc := &AccountTestService{accountRepo: &modelIdentityAccountRepo{account: account}, httpUpstream: upstream, cfg: &config.Config{}}
		r, err := svc.RunTestBackgroundWithPrompt(context.Background(), 42, "gpt-test", "test")
		require.NoError(t, err)
		require.Equal(t, "failed", r.Status)
		_, reason := passiveRecoverySamples(r, nil, "gpt-test", "private", false, time.Now())
		require.NotEmpty(t, reason)
	}
}

func TestPassiveRecoveryFingerprintUsesStableBatchSession(t *testing.T) {
	account := newTestOAuthAccount(741, map[string]any{codexFingerprintModeExtraKey: string(codexFingerprintSingleMachineMultiWindow)})
	var session, thread, installation string
	for n := 0; n < 3; n++ {
		ctx := context.WithValue(context.Background(), passiveRecoveryRequestKey{}, &passiveRecoveryRequest{session: "stable-recovery-batch", attempts: &atomic.Int64{}})
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodPost, "/test", nil).WithContext(ctx)
		ids := prepareCodexFingerprintTestIDs(c, account)
		require.NotNil(t, ids)
		if n == 0 {
			session, thread, installation = ids.sessionID, ids.threadID, ids.installationID
		} else {
			require.Equal(t, session, ids.sessionID)
			require.Equal(t, thread, ids.threadID)
			require.Equal(t, installation, ids.installationID)
		}
		releaseStagedCodexFingerprintLease(c)
	}
}

func TestPassiveRecoveryEligibilityRespectsOtherAccountLimits(t *testing.T) {
	now := time.Now()
	until := now.Add(time.Hour)
	a := &Account{Status: "quality_paused", Schedulable: true, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}
	require.Nil(t, passiveRecoveryEligibility(a, "test-model", now), "automatic hold itself must allow active recovery")
	a.RateLimitResetAt = &until
	deferred := passiveRecoveryEligibility(a, "test-model", now)
	require.NotNil(t, deferred)
	require.Equal(t, until, deferred.Until)
	a.RateLimitResetAt = nil
	a.Status = "error"
	require.NotNil(t, passiveRecoveryEligibility(a, "test-model", now))
}

func TestPassiveRecoveryEvidenceJSONNames(t *testing.T) {
	encoded, err := json.Marshal(PassiveQualityActiveRecoveryResult{Verdict: "pending", Requests: 2, WarmupRequests: 1, EvidenceBatches: []PassiveQualityEvidence{}})
	require.NoError(t, err)
	var result map[string]any
	require.NoError(t, json.Unmarshal(encoded, &result))
	require.Equal(t, "pending", result["verdict"])
	require.Equal(t, 2.0, result["requests"])
	require.Equal(t, 1.0, result["warmup_requests"])
	require.Contains(t, result, "evidence_batches")
}

// Force separate network-read observations without sleeping or making a real
// upstream call. A single fully buffered response intentionally has no TPS.
type recoveryChunkBody struct{ chunks []string }

func (b *recoveryChunkBody) Read(p []byte) (int, error) {
	if len(b.chunks) == 0 {
		return 0, io.EOF
	}
	n := copy(p, b.chunks[0])
	b.chunks[0] = b.chunks[0][n:]
	if b.chunks[0] == "" {
		b.chunks = b.chunks[1:]
	}
	return n, nil
}
func (*recoveryChunkBody) Close() error { return nil }

func TestPassiveRecoveryAdaptiveProvidersCountActualSamples(t *testing.T) {
	for _, platform := range []string{PlatformZhipu, PlatformDeepseek, PlatformKimi} {
		t.Run(platform, func(t *testing.T) {
			account := &Account{ID: 42, Platform: platform, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 1, Credentials: map[string]any{
				"api_key": "test", "api_protocol": APIProtocolAdaptive, "api_base_urls": map[string]any{
					APIProtocolChatCompletions: "https://chat.example.com/v1", APIProtocolAnthropic: "https://anthropic.example.com", APIProtocolResponses: "https://responses.example.com",
				},
			}}
			protocols := []string{APIProtocolChatCompletions, APIProtocolAnthropic}
			if account.SupportsNativeCNResponses() {
				protocols = append(protocols, APIProtocolResponses)
			}
			visited := map[string]int{}
			upstream := &modelIdentityHTTPUpstream{beforeReturn: func(req *http.Request, _ int) { visited[req.URL.Host]++ }}
			for n := 0; n < 12; n++ {
				for _, protocol := range protocols {
					upstream.responses = append(upstream.responses, recoveryProtocolResponse(protocol))
				}
			}
			reservations := 0
			budget := &qualityOperationsStub{reserve: func(context.Context, int64, int64, bool, string) (*QualityProbeReservation, error) {
				reservations++
				return &QualityProbeReservation{ID: int64(reservations)}, nil
			}}
			svc := &AccountTestService{accountRepo: &modelIdentityAccountRepo{account: account}, httpUpstream: upstream, cfg: &config.Config{}, qualityOperations: budget}
			now := time.Now()
			runner := passiveActiveRecoveryRunner{request: svc.RunTestBackgroundWithPrompt, now: func() time.Time { return now }, wait: func(_ context.Context, d time.Duration) error { now = now.Add(d); return nil }}
			probe := activeRecoveryFixture()
			if platform == PlatformDeepseek {
				probe.Model = "deepseek-v4-flash"
			}
			out := runner.run(context.Background(), probe)
			require.True(t, out.Passed, "%s: %s", out.Verdict, out.Reason)
			expected := 16
			if len(protocols) == 3 {
				expected = 18
			}
			require.Equal(t, expected, upstream.requests)
			require.Equal(t, expected, out.Requests)
			require.Equal(t, expected, reservations)
			require.Equal(t, 2, out.Batches)
			require.Equal(t, 12, out.Evidence.CacheSamples)
			require.Len(t, budget.outcomes, expected)
			require.Len(t, visited, len(protocols))
			require.Equal(t, expected/len(protocols), visited["anthropic.example.com"])
		})
	}
}

func recoveryProtocolResponse(protocol string) *http.Response {
	var chunks []string
	switch protocol {
	case APIProtocolChatCompletions:
		chunks = []string{
			"data: {\"choices\":[{\"delta\":{\"content\":\"A complete explanation\"}}]}\n\n",
			"data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":1000,\"completion_tokens\":256,\"prompt_tokens_details\":{\"cached_tokens\":900}}}\n\n",
			"data: [DONE]\n\n",
		}
	case APIProtocolAnthropic:
		chunks = []string{
			"data: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":100,\"output_tokens\":0,\"cache_read_input_tokens\":900,\"cache_creation_input_tokens\":0}}}\n\n",
			"data: {\"type\":\"content_block_delta\",\"delta\":{\"text\":\"A complete explanation\"}}\n\n",
			"data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":256}}\n\n",
			"data: {\"type\":\"message_stop\"}\n\n",
		}
	default:
		chunks = []string{
			"data: {\"type\":\"response.output_text.delta\",\"delta\":\"A complete explanation\"}\n\n",
			"data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"usage\":{\"input_tokens\":1000,\"output_tokens\":256,\"input_tokens_details\":{\"cached_tokens\":900}}}}\n\n",
		}
	}
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: &recoveryChunkBody{chunks: chunks}}
}

type recoveryErrorAccountRepo struct {
	modelIdentityAccountRepo
	errors int
}

func (r *recoveryErrorAccountRepo) SetError(_ context.Context, _ int64, _ string) error {
	r.errors++
	r.account.Status = "error"
	return nil
}

func TestPassiveRecoveryAuthenticationFailureDoesNotCreatePermanentAccountError(t *testing.T) {
	account := &Account{ID: 42, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: "quality_paused", Schedulable: true, Credentials: map[string]any{"api_key": "test", "base_url": "https://upstream.example.com"}, Extra: map[string]any{openai_compat.ExtraKeyResponsesSupported: false}}
	repo := &recoveryErrorAccountRepo{modelIdentityAccountRepo: modelIdentityAccountRepo{account: account}}
	upstream := &modelIdentityHTTPUpstream{responses: []*http.Response{costJSONResponse(401, "{}"), recoveryProtocolResponse(APIProtocolChatCompletions)}}
	svc := &AccountTestService{accountRepo: repo, httpUpstream: upstream, cfg: &config.Config{}}
	ctx := context.WithValue(context.Background(), passiveRecoveryRequestKey{}, &passiveRecoveryRequest{session: "private", attempts: &atomic.Int64{}})
	r, err := svc.RunTestBackgroundWithPrompt(ctx, 42, "test-model", "test")
	require.NoError(t, err)
	require.Equal(t, "failed", r.Status)
	require.Equal(t, "quality_paused", account.Status)
	require.Zero(t, repo.errors)
	_, reason := passiveRecoverySamples(r, nil, "test-model", "private", true, time.Now())
	require.Equal(t, "active_request_failed", reason)
	r, err = svc.RunTestBackgroundWithPrompt(ctx, 42, "test-model", "test")
	require.NoError(t, err)
	require.Equal(t, "success", r.Status)
	require.Equal(t, 2, upstream.requests)
	require.NoError(t, svc.setAccountTestError(context.Background(), 42, "ordinary auth failure"))
	require.Equal(t, 1, repo.errors)
	require.Equal(t, "error", account.Status)
	r, err = svc.RunTestBackgroundWithPrompt(ctx, 42, "test-model", "test")
	require.NoError(t, err)
	require.True(t, QualityResultDeferred(r))
	require.Equal(t, 2, upstream.requests)
}

func TestPassiveRecoveryAdaptiveFailedProtocolCannotHideBehindOthers(t *testing.T) {
	now := time.Now()
	calls := 0
	runner := passiveActiveRecoveryRunner{now: func() time.Time { return now }, wait: func(context.Context, time.Duration) error { return nil }, request: func(context.Context, int64, string, string) (*ScheduledTestResult, error) {
		calls++
		r := activeRecoverySuccess(now, 900)
		a := r.ExecutionInfo.Attempts[0]
		r.ExecutionInfo.Attempts = []ScheduledTestRequestAttempt{a, {HTTPStatus: 503}, a}
		return r, nil
	}}
	out := runner.run(context.Background(), activeRecoveryFixture())
	require.Equal(t, "fail", out.Verdict)
	require.Equal(t, 1, calls)
	require.Equal(t, 3, out.Requests)
}

func TestPassiveRecoveryKnownUnsupportedMetricsDoNotSendRequests(t *testing.T) {
	for _, account := range []*Account{
		{ID: 42, Platform: PlatformAnthropic, Type: AccountTypeBedrock, Status: StatusActive, Schedulable: true},
		{ID: 42, Platform: PlatformTypeSafe, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true},
	} {
		upstream := &modelIdentityHTTPUpstream{}
		svc := &AccountTestService{accountRepo: &modelIdentityAccountRepo{account: account}, httpUpstream: upstream, cfg: &config.Config{}}
		for _, metric := range []string{"cache", "ttft", "tps"} {
			cfg := &PassiveQualityConfig{}
			switch metric {
			case "cache":
				cfg.CacheRate.Enabled = true
			case "ttft":
				cfg.TTFT.Enabled = true
			case "tps":
				cfg.TPS.Enabled = true
			}
			ctx := context.WithValue(context.Background(), passiveRecoveryRequestKey{}, &passiveRecoveryRequest{session: "private", attempts: &atomic.Int64{}, config: cfg})
			r, err := svc.RunTestBackgroundWithPrompt(ctx, 42, "test-model", "test")
			require.NoError(t, err)
			require.True(t, QualityResultDeferred(r))
			require.Contains(t, r.ErrorMessage, "active_probe_metrics_unsupported")
			require.Zero(t, upstream.requests)
			require.WithinDuration(t, time.Now().Add(time.Hour), *r.ExecutionInfo.NextCheckAt, time.Second)
		}
		cfg := &PassiveQualityConfig{ErrorRate: PassiveQualityMetric{Enabled: true}}
		ctx := context.WithValue(context.Background(), passiveRecoveryRequestKey{}, &passiveRecoveryRequest{session: "private", config: cfg})
		require.Nil(t, passiveRecoveryUnsupportedMetrics(ctx, account, time.Now()))
	}
}

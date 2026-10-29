package service

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai_compat"
	"github.com/stretchr/testify/require"
)

type qualityOperationsStub struct {
	QualityOperationsRepository
	reserve  func(context.Context, int64, int64, bool, string) (*QualityProbeReservation, error)
	outcomes []QualityProbeOutcome
}

func (s *qualityOperationsStub) ReserveQualityProbe(ctx context.Context, a, t int64, auto bool, session string) (*QualityProbeReservation, error) {
	return s.reserve(ctx, a, t, auto, session)
}
func (s *qualityOperationsStub) SettleQualityProbe(_ context.Context, _ *QualityProbeReservation, o QualityProbeOutcome) error {
	s.outcomes = append(s.outcomes, o)
	return nil
}
func TestQualityOperationsDeniedBeforeTransportAndNeverQualityFailure(t *testing.T) {
	for _, cause := range []error{&QualityDeferredError{Reason: "budget", Until: time.Now().Add(time.Hour)}, errors.New("database unavailable")} {
		account := &Account{ID: 42, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"api_key": "test", "base_url": "https://upstream.example.com"}, Extra: map[string]any{openai_compat.ExtraKeyResponsesSupported: true}}
		upstream := &modelIdentityHTTPUpstream{}
		repo := &qualityOperationsStub{reserve: func(context.Context, int64, int64, bool, string) (*QualityProbeReservation, error) { return nil, cause }}
		svc := &AccountTestService{accountRepo: &modelIdentityAccountRepo{account: account}, httpUpstream: upstream, cfg: &config.Config{}, qualityOperations: repo}
		runner := &ScheduledTestRunnerService{automaticRetryBaseDelay: time.Millisecond}
		calls := 0
		result := runner.runWithAutomaticRetries(context.Background(), &ScheduledTestPlan{}, nil, func(ctx context.Context) *ScheduledTestResult {
			calls++
			r, err := svc.RunTestBackground(ctx, 42, "test-model")
			require.NoError(t, err)
			return r
		})
		require.True(t, QualityResultDeferred(result))
		require.Zero(t, upstream.requests)
		require.Equal(t, 1, calls)
		require.Empty(t, repo.outcomes)
		verdict, _ := evaluateScheduledTestProtection(ScheduledTestProtectionRule{PauseOnFailure: true}, result)
		require.Equal(t, "pending", verdict)
		require.NotNil(t, result.ExecutionInfo.NextCheckAt)
	}
}
func TestQualityOperationsEveryRetryIsReserved(t *testing.T) {
	account := &Account{ID: 42, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"api_key": "test", "base_url": "https://upstream.example.com"}, Extra: map[string]any{openai_compat.ExtraKeyResponsesSupported: true}}
	upstream := &modelIdentityHTTPUpstream{responses: []*http.Response{costJSONResponse(503, `{"error":{"type":"server_error"},"usage":{"input_tokens":10,"output_tokens":2}}`)}}
	admissions := 0
	repo := &qualityOperationsStub{reserve: func(_ context.Context, a, tokens int64, _ bool, session string) (*QualityProbeReservation, error) {
		admissions++
		require.NotEmpty(t, session)
		require.Positive(t, tokens)
		if admissions == 2 {
			return nil, &QualityDeferredError{Reason: "budget", Until: time.Now().Add(time.Hour)}
		}
		return &QualityProbeReservation{ID: 1, AccountID: a}, nil
	}}
	svc := &AccountTestService{accountRepo: &modelIdentityAccountRepo{account: account}, httpUpstream: upstream, cfg: &config.Config{}, qualityOperations: repo}
	runner := &ScheduledTestRunnerService{automaticRetryBaseDelay: time.Millisecond}
	result := runner.runWithAutomaticRetries(context.Background(), &ScheduledTestPlan{}, nil, func(ctx context.Context) *ScheduledTestResult {
		r, err := svc.RunTestBackground(ctx, 42, "test-model")
		require.NoError(t, err)
		return r
	})
	require.Equal(t, 2, admissions)
	require.Equal(t, 1, upstream.requests)
	require.True(t, QualityResultDeferred(result))
	require.Len(t, result.ExecutionInfo.Attempts, 1)
	require.Len(t, repo.outcomes, 1)
	require.EqualValues(t, 12, *repo.outcomes[0].Tokens)
}
func TestQualityOperationsSettlementProtocolCacheAndUnknown(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		total      *int64
	}{
		{"openai", `{"usage":{"input_tokens":20,"output_tokens":7,"input_tokens_details":{"cached_tokens":10},"output_tokens_details":{"reasoning_tokens":3}}}`, usageInt(27)},
		{"anthropic", `{"usage":{"input_tokens":20,"output_tokens":7,"cache_read_input_tokens":10,"cache_creation_input_tokens":5}}`, usageInt(42)},
		{"stream_incomplete", "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":20,\"output_tokens\":0}}}\n\n", nil},
		{"partial", `{"usage":{"input_tokens":20}}`, nil},
		{"unknown", `{"content":"ok"}`, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := &qualityOperationsStub{reserve: func(context.Context, int64, int64, bool, string) (*QualityProbeReservation, error) {
				return &QualityProbeReservation{ID: 1}, nil
			}}
			ctx := context.WithValue(context.Background(), qualityOperationsKey{}, repo)
			collector := &accountTestUsageCollector{}
			ctx = context.WithValue(ctx, accountTestUsageKey{}, collector)
			req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://example.invalid", strings.NewReader("{}"))
			require.NoError(t, err)
			response, err := executeQualityTestHTTP(req, 42, func(*http.Request) (*http.Response, error) { return costJSONResponse(200, tc.body), nil })
			require.NoError(t, err)
			_, err = io.ReadAll(response.Body)
			require.NoError(t, err)
			require.NoError(t, response.Body.Close())
			require.NoError(t, response.Body.Close())
			collector.finishQualitySettlements("")
			require.Len(t, repo.outcomes, 1)
			require.Equal(t, tc.total, repo.outcomes[0].Tokens)
		})
	}
}
func TestQualityOperationsDynamicBoundaries(t *testing.T) {
	c := DefaultQualityOperationsConfig()
	require.Zero(t, c.HealthyDelay(100))
	c.DynamicEnabled = true
	require.Zero(t, c.HealthyDelay(c.HealthyRounds-1))
	require.Equal(t, 15*time.Minute, c.HealthyDelay(c.HealthyRounds))
	require.Equal(t, 30*time.Minute, c.HealthyDelay(c.HealthyRounds+1))
	require.Equal(t, 4*time.Hour, c.HealthyDelay(100))
	c.HealthyMaxSeconds = c.HealthyBaseSeconds - 1
	require.Error(t, c.Validate())
}

func (s *qualityOperationsStub) QualityFrequencyDue(context.Context, int64, int64, string) (*time.Time, error) {
	next := time.Now().Add(time.Hour)
	return &next, nil
}
func (s *qualityOperationsStub) RecordQualityFrequency(context.Context, *ScheduledTestPlan, *ScheduledTestResult, string, bool) error {
	return nil
}
func TestQualityOperationsDynamicFrequencyPreservesManualRuns(t *testing.T) {
	base := &snapshotResultRepoStub{retryResultRepoStub: &retryResultRepoStub{}, targets: []int64{12}}
	repo := &costBackoffRepo{snapshotResultRepoStub: base, states: map[int64]*ScheduledTestBackoff{}, policy: map[int64]string{}}
	upstream := 0
	accounts := scheduledTestExecutionAccountRepo{get: func(context.Context, int64) (*Account, error) {
		upstream++
		return &Account{ID: 12, Extra: map[string]any{"synthetic_ui_test": true}}, nil
	}}
	runner := newSnapshotRunner(base, accounts)
	runner.scheduledSvc.resultRepo = repo
	runner.accountTestSvc.qualityOperations = &qualityOperationsStub{}
	defer runner.Stop()
	plan := automaticRetryTestPlan()
	plan.Protection.Enabled = false
	plan.TestDefinitionIDs = []int64{1, 2}
	// Directly use persisted queue targets so statistics precede the paid check.
	id := int64(12)
	started := time.Now()
	base.latestRun = "run"
	base.frozenMembers = map[int64]bool{12: true}
	queue := []*scheduledTestExecutionTarget{}
	for i, kind := range []string{"text", "text"} {
		copy := *plan
		def := int64(i + 1)
		copy.TestDefinitionID = &def
		queue = append(queue, &scheduledTestExecutionTarget{plan: &copy, kind: kind, accountID: &id, pending: &ScheduledTestResult{ID: int64(i + 1), AccountID: &id, RunID: "run", StartedAt: started}})
	}
	runner.runExecutionTargetQueues(context.WithValue(context.Background(), scheduledTestAutomaticRunKey{}, true), queue, started, map[int64]*scheduledTestAccountProgress{})
	require.Zero(t, upstream, "healthy frequency skips automatic generation")
	require.Zero(t, repo.writes, "partial round must not extend health or failure streak")
	runner.runExecutionTargetQueues(context.Background(), queue, started, map[int64]*scheduledTestAccountProgress{})
	require.Positive(t, upstream, "manual run bypasses adaptive frequency")
}

func TestQualityOperationsUsesFinalStreamVerdict(t *testing.T) {
	account := &Account{ID: 42, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"api_key": "test", "base_url": "https://upstream.example.com"}, Extra: map[string]any{openai_compat.ExtraKeyResponsesSupported: true}}
	resp := costJSONResponse(200, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"partial\"}\n\n")
	resp.Header.Set("Content-Type", "text/event-stream")
	upstream := &modelIdentityHTTPUpstream{responses: []*http.Response{resp}}
	repo := &qualityOperationsStub{reserve: func(context.Context, int64, int64, bool, string) (*QualityProbeReservation, error) {
		return &QualityProbeReservation{ID: 1}, nil
	}}
	svc := &AccountTestService{accountRepo: &modelIdentityAccountRepo{account: account}, httpUpstream: upstream, cfg: &config.Config{}, qualityOperations: repo}
	result, err := svc.RunTestBackground(context.Background(), 42, "test-model")
	require.NoError(t, err)
	require.Equal(t, "failed", result.Status)
	require.Len(t, repo.outcomes, 1)
	require.Equal(t, result.ExecutionInfo.FailureKind, repo.outcomes[0].Kind)
	require.NotEqual(t, "healthy", repo.outcomes[0].Kind)
	require.Nil(t, repo.outcomes[0].Tokens)
}

type qualityMaintenanceStub struct {
	QualityOperationsRepository
	calls int
	err   error
}

func (s *qualityMaintenanceStub) PruneQualityOperations(context.Context) error {
	s.calls++
	return s.err
}
func TestQualityOperationsMaintenanceRetainsHourlyCleanupWithoutPassiveService(t *testing.T) {
	repo := &qualityMaintenanceStub{}
	runner := &ScheduledTestRunnerService{accountTestSvc: &AccountTestService{qualityOperations: repo}}
	now := time.Now()
	runner.pruneQualityOperations(context.Background(), now)
	runner.pruneQualityOperations(context.Background(), now.Add(time.Minute))
	require.Equal(t, 1, repo.calls)
	runner.pruneQualityOperations(context.Background(), now.Add(time.Hour))
	require.Equal(t, 2, repo.calls)
	repo.err = errors.New("temporary cleanup failure")
	runner.pruneQualityOperations(context.Background(), now.Add(2*time.Hour))
	require.Equal(t, 3, repo.calls)
	repo.err = nil
	runner.pruneQualityOperations(context.Background(), now.Add(2*time.Hour+time.Minute))
	require.Equal(t, 4, repo.calls, "failed cleanup retries on the next scheduler sweep")
}

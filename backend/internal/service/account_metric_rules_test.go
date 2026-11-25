package service

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestMetricRulePauseEventReasons(t *testing.T) {
	start := time.Now()
	cfg := DefaultPerformanceConfig()
	cfg.Enabled = true
	cfg.TTFT.Pause.Max = metricNumber(2000)
	cfg.TTFT.Recovery.Max = metricNumber(1000)
	// Older snapshots contain the PREVIOUS pause reason. Recover the actual
	// decision from their frozen metrics where possible.
	m := AccountPerformance{Config: cfg, Reason: "duration", Paused: true, TrialStartedAt: &start}
	m.DescribeState()
	m.Metrics = OpsAccountRequestStats{AvgFirstTokenMs: metricNumber(1400), FirstTokenSamples: 3, WindowEnd: *m.TrialEndsAt}
	action, reason := m.MetricDecision(*m.TrialEndsAt)
	require.Equal(t, "pause", action)
	require.Equal(t, "trial_recovery_threshold", reason)
	for _, tc := range []struct {
		firstToken float64
		decision   string
		want       string
	}{
		{2400, "", "ttft"},
		{1400, "", "trial_timeout"},
		{1400, reason, "trial_recovery_threshold"},
	} {
		m.Metrics.AvgFirstTokenMs = &tc.firstToken
		raw, err := json.Marshal(struct {
			AccountPerformance
			Decision string `json:"decision_reason,omitempty"`
		}{m, tc.decision})
		require.NoError(t, err)
		event := PerformanceEvent{Action: "pause", Evidence: raw}
		event.ReadPauseReason()
		require.Equal(t, tc.want, event.Reason)
	}
}

func metricNumber(n float64) *float64 { return &n }
func TestMetricRulesValidation(t *testing.T) {
	c := DefaultPerformanceConfig()
	require.NoError(t, c.Validate())
	c.Enabled = true
	require.Error(t, c.Validate())
	c.SuccessRate.Pause.Min = metricNumber(95)
	require.NoError(t, c.Validate())
	c.SuccessRate.Recovery.Min = metricNumber(90)
	require.Error(t, c.Validate())
	c.SuccessRate.Recovery.Min = metricNumber(99)
	require.NoError(t, c.Validate())
	c.SuccessRate.Recovery.Max = metricNumber(98)
	require.Error(t, c.Validate())
	c = DefaultPerformanceConfig()
	c.CacheRate.Pause.Max = metricNumber(101)
	require.Error(t, c.Validate())
	c = DefaultPerformanceConfig()
	c.MinSamples = 0
	require.Error(t, c.Validate())
	c = DefaultPerformanceConfig()
	c.RecoverySamples = 101
	require.Error(t, c.Validate())
	c = DefaultPerformanceConfig()
	c.Version = 0
	require.Error(t, c.Validate())
}
func TestMetricRulesSampleGatesAndRecovery(t *testing.T) {
	c := DefaultPerformanceConfig()
	c.Enabled = true
	c.SuccessRate.Pause.Min = metricNumber(90)
	c.SuccessRate.Recovery.Min = metricNumber(95)
	c.CacheRate.Pause.Min = metricNumber(30)
	c.TTFT.Pause.Max = metricNumber(3000)
	c.Duration.Pause.Max = metricNumber(10000)
	m := AccountPerformance{Config: c}
	require.Empty(t, m.Breaches())
	require.Equal(t, "trial_insufficient_samples", m.recoveryFailure())
	m.Metrics = OpsAccountRequestStats{SuccessRate: metricNumber(0), RequestSamples: 19}
	require.Empty(t, m.Breaches())
	m.Metrics.RequestSamples = 20
	require.Equal(t, []string{"success_rate"}, m.Breaches())
	m.Metrics.SuccessRate = metricNumber(.9)
	require.Empty(t, m.Breaches(), "equality is allowed")
	now := time.Now()
	m.TrialStartedAt = &now
	m.Metrics = OpsAccountRequestStats{SuccessRate: metricNumber(1), RequestSamples: 3, CacheRate: metricNumber(.5), CacheSamples: 3, AvgFirstTokenMs: metricNumber(2000), FirstTokenSamples: 3, AvgDurationMs: metricNumber(5000), DurationSamples: 2}
	require.Equal(t, "trial_insufficient_samples", m.recoveryFailure(), "each selected metric needs enough fresh samples")
	m.Metrics.DurationSamples = 3
	require.Empty(t, m.recoveryFailure())
	m.Metrics.SuccessRate = metricNumber(.93)
	require.Empty(t, m.Breaches())
	require.Equal(t, "trial_recovery_threshold", m.recoveryFailure(), "hysteresis requires the stricter recovery threshold")
	m.Metrics.SuccessRate = nil
	require.Equal(t, "trial_insufficient_samples", m.recoveryFailure())
	m.Metrics = OpsAccountRequestStats{}
	require.Empty(t, m.Breaches())
	require.Equal(t, "trial_insufficient_samples", m.recoveryFailure(), "expired samples are not recovery")
}

type trialAdmissionStub struct {
	performanceAccountRepoStub
	trialID    int64
	admissions int
	dueAt      time.Time
}

func (r *trialAdmissionStub) PerformanceRoutingStates(_ context.Context, _ []int64) (map[int64]PerformanceRoutingState, error) {
	state := PerformanceRoutingState{Blocked: r.admissions > 0}
	if !state.Blocked && !r.dueAt.IsZero() {
		state.TrialDueAt = &r.dueAt
	}
	return map[int64]PerformanceRoutingState{r.trialID: state}, nil
}

func (r *trialAdmissionStub) AdmitPerformanceRequest(_ context.Context, id int64) (bool, error) {
	if id != r.trialID {
		return true, nil
	}
	if r.admissions > 0 {
		return false, nil
	}
	r.admissions++
	r.paused = map[int64]bool{id: true}
	return true, nil
}
func TestMetricTrialGateCoversUnlimitedAndProxyPool(t *testing.T) {
	ctx := context.Background()
	for _, account := range []*Account{{ID: 7}, proxyRoutingAccount()} {
		r := &trialAdmissionStub{trialID: account.ID}
		s := NewConcurrencyService(&routingTestCache{proxyLegacyCache: &proxyLegacyCache{}, selected: 22})
		gateway := &GatewayService{concurrencyService: s, accountRepo: r}
		result, err := s.AcquireAccountRoute(ctx, &account, account.Concurrency)
		require.NoError(t, err)
		require.True(t, result.Acquired)
		require.Zero(t, r.admissions)
		latest, vetoed, _ := gateway.AdmitAccountForForward(ctx, account)
		require.False(t, vetoed)
		require.Same(t, account, latest, "retain the reserved proxy for forwarding")
		_, vetoed, reason := gateway.AdmitAccountForForward(ctx, account)
		require.True(t, vetoed)
		require.Equal(t, "performance_paused", reason)
		result.ReleaseFunc()
		require.Equal(t, 1, r.admissions)
	}
}

func TestMetricTrialSchedulerRechecksBeforeAdmitting(t *testing.T) {
	for _, tc := range []struct {
		name      string
		advanced  string
		loadBatch bool
	}{
		{"advanced_top_k_one", "true", true},
		{"legacy_load_batch", "false", true},
		{"legacy_priority", "false", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			testMetricTrialScheduler(t, tc.advanced, tc.loadBatch)
		})
	}
}

func testMetricTrialScheduler(t *testing.T, advanced string, loadBatch bool) {
	resetOpenAIAdvancedSchedulerSettingCacheForTest()
	ctx := context.Background()
	trial := &Account{ID: 7, Priority: 99, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true, Concurrency: 4}
	backup := *trial
	backup.ID, backup.Priority = 8, 5
	rules := &trialAdmissionStub{trialID: trial.ID, dueAt: time.Now().Add(-time.Second)}
	rules.AccountRepository = schedulerTestOpenAIAccountRepo{accounts: []Account{*trial, backup}}
	cache := &openAISnapshotCacheStub{
		snapshotAccounts: []*Account{trial, &backup},
		accountsByID:     map[int64]*Account{trial.ID: trial, backup.ID: &backup},
	}
	cfg := &config.Config{}
	cfg.Gateway.OpenAIWS.LBTopK = 1
	cfg.Gateway.Scheduling.LoadBatchEnabled = loadBatch
	sticky := &schedulerTestGatewayCache{}
	s := &OpenAIGatewayService{
		accountRepo:        rules,
		schedulerSnapshot:  &SchedulerSnapshotService{cache: cache, accountRepo: rules},
		cfg:                cfg,
		cache:              sticky,
		rateLimitService:   newOpenAIAdvancedSchedulerRateLimitService(advanced),
		concurrencyService: NewConcurrencyService(schedulerTestConcurrencyCache{}),
	}
	metadataAccount, err := s.SelectAccountForTokenCount(ctx, nil, "", "gpt-5.1", "", PlatformOpenAI)
	require.NoError(t, err)
	require.Equal(t, backup.ID, metadataAccount.ID, "only measured generation traffic is promoted to trial")
	selectAccount := func(session string, excluded map[int64]struct{}) *AccountSelectionResult {
		selection, _, err := s.SelectAccountWithScheduler(ctx, nil, "", session, "gpt-5.1", excluded, OpenAIUpstreamTransportAny, false)
		require.NoError(t, err)
		require.True(t, selection.Acquired)
		t.Cleanup(selection.ReleaseFunc)
		return selection
	}

	// Two in-flight requests may both pass the post-slot snapshot refresh.
	// Only one can use this trial interval, and the loser can still use a backup.
	require.NoError(t, sticky.SetSessionAccountID(ctx, 0, "openai:bound", backup.ID, time.Hour))
	require.Equal(t, backup.ID, selectAccount("bound", nil).Account.ID)
	first, concurrent := selectAccount("new", nil), selectAccount("", nil)
	require.Equal(t, trial.ID, first.Account.ID)
	require.Equal(t, trial.ID, concurrent.Account.ID)
	require.True(t, first.PreserveStickyBinding)
	require.NotContains(t, sticky.sessionBindings, "openai:new")
	require.Zero(t, rules.admissions)
	_, vetoed, _ := s.AdmitAccountForForward(ctx, first.Account)
	require.False(t, vetoed)
	_, vetoed, reason := s.AdmitAccountForForward(ctx, concurrent.Account)
	require.True(t, vetoed)
	require.Equal(t, "performance_paused", reason)
	require.Equal(t, 1, rules.admissions)
	next := selectAccount("", map[int64]struct{}{trial.ID: {}})
	require.Equal(t, backup.ID, next.Account.ID)
	_, vetoed, _ = s.AdmitAccountForForward(ctx, next.Account)
	require.False(t, vetoed)
	require.Equal(t, 1, rules.admissions)
}

type metricUnavailableRepo struct{ AccountPerformanceRepository }

func (*metricUnavailableRepo) Read(context.Context, []int64, time.Time, bool) (map[int64]AccountPerformance, error) {
	return map[int64]AccountPerformance{1: {Config: DefaultPerformanceConfig(), Metrics: OpsAccountRequestStats{SuccessRate: metricNumber(1), RequestSamples: 20}}}, nil
}
func TestMetricRulesDisabledMonitoringCannotEvaluateOrShowFalseSuccess(t *testing.T) {
	s := NewAccountPerformanceService(&metricUnavailableRepo{})
	s.ops = &OpsService{cfg: &config.Config{}}
	s.evaluate(context.Background())
	rows, err := s.Read(context.Background(), []int64{1}, time.Now(), true)
	require.NoError(t, err)
	require.False(t, rows[1].MonitoringAvailable)
	require.Nil(t, rows[1].Metrics.SuccessRate)
	require.Zero(t, rows[1].Metrics.RequestSamples)
}

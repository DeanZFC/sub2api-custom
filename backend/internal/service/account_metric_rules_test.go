package service

import (
	"context"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

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
	require.False(t, m.ReadyForRecovery())
	m.Metrics = OpsAccountRequestStats{SuccessRate: metricNumber(0), RequestSamples: 19}
	require.Empty(t, m.Breaches())
	m.Metrics.RequestSamples = 20
	require.Equal(t, []string{"success_rate"}, m.Breaches())
	m.Metrics.SuccessRate = metricNumber(.9)
	require.Empty(t, m.Breaches(), "equality is allowed")
	now := time.Now()
	m.TrialStartedAt = &now
	m.Metrics = OpsAccountRequestStats{SuccessRate: metricNumber(1), RequestSamples: 3, CacheRate: metricNumber(.5), CacheSamples: 3, AvgFirstTokenMs: metricNumber(2000), FirstTokenSamples: 3, AvgDurationMs: metricNumber(5000), DurationSamples: 2}
	require.False(t, m.ReadyForRecovery(), "each selected metric needs enough fresh samples")
	m.Metrics.DurationSamples = 3
	require.True(t, m.ReadyForRecovery())
	m.Metrics.SuccessRate = metricNumber(.93)
	require.Empty(t, m.Breaches())
	require.False(t, m.ReadyForRecovery(), "hysteresis requires the stricter recovery threshold")
	m.Metrics.SuccessRate = nil
	require.False(t, m.ReadyForRecovery())
	m.Metrics = OpsAccountRequestStats{}
	require.Empty(t, m.Breaches())
	require.False(t, m.ReadyForRecovery(), "expired samples are not recovery")
}

type trialAdmissionStub struct {
	performanceAccountRepoStub
	allowed  bool
	refunded int
}

func (r *trialAdmissionStub) AcquirePerformanceTrial(context.Context, int64) (bool, func(), error) {
	return r.allowed, func() { r.refunded++ }, nil
}
func TestMetricTrialGateCoversUnlimitedAndProxyPool(t *testing.T) {
	r := &trialAdmissionStub{}
	s := NewConcurrencyService(nil)
	s.performancePauses = r
	for _, proxies := range [][]int64{nil, {1, 2}} {
		result, err := s.AcquireAccountSlot(context.Background(), 1, 0, proxies...)
		require.NoError(t, err)
		require.False(t, result.Acquired)
	}
	r.allowed = true
	result, err := s.AcquireAccountSlot(context.Background(), 1, 0)
	require.NoError(t, err)
	require.True(t, result.Acquired)
	result.ReleaseFunc()
	require.Zero(t, r.refunded, "completed admission consumes the trial budget")
}

type metricUnavailableRepo struct{ AccountPerformanceRepository }

func (*metricUnavailableRepo) Read(context.Context, []int64, time.Time) (map[int64]AccountPerformance, error) {
	return map[int64]AccountPerformance{1: {Config: DefaultPerformanceConfig(), Metrics: OpsAccountRequestStats{SuccessRate: metricNumber(1), RequestSamples: 20}}}, nil
}
func TestMetricRulesDisabledMonitoringCannotEvaluateOrShowFalseSuccess(t *testing.T) {
	s := NewAccountPerformanceService(&metricUnavailableRepo{})
	s.ops = &OpsService{cfg: &config.Config{}}
	s.evaluate(context.Background())
	rows, err := s.Read(context.Background(), []int64{1}, time.Now())
	require.NoError(t, err)
	require.False(t, rows[1].MonitoringAvailable)
	require.Nil(t, rows[1].Metrics.SuccessRate)
	require.Zero(t, rows[1].Metrics.RequestSamples)
}

func TestMetricTrialRefundsRejectedAdmission(t *testing.T) {
	for _, tc := range []struct {
		name     string
		acquired bool
		gate     bool
	}{
		{"redis_saturated", false, false}, {"later_admission_denied", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := &trialAdmissionStub{allowed: true}
			s := NewConcurrencyService(&proxyLegacyCache{acquireResult: tc.acquired})
			s.performancePauses = r
			if tc.gate {
				s.SetAccountAdmissionGate(accountAdmissionGateFunc(func(context.Context, int64) (bool, error) { return false, nil }))
			}
			result, _ := s.AcquireAccountSlot(context.Background(), 1, 5)
			require.False(t, result.Acquired)
			require.Equal(t, 1, r.refunded)
		})
	}
}

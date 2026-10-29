package service

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai_compat"
	"github.com/stretchr/testify/require"
)

type passiveProbeTestRepo struct {
	PassiveQualityRepository
	probe   PassiveQualityState
	success bool
}

func (r *passiveProbeTestRepo) FinishProbe(_ context.Context, probe PassiveQualityState, success bool, _ time.Time) error {
	r.probe, r.success = probe, success
	return nil
}

type passiveActiveProbeDispatchTestRepo struct {
	passiveProbeTestRepo
	activeCalls int
	activeProbe PassiveQualityState
	result      PassiveQualityActiveRecoveryResult
}

func (r *passiveActiveProbeDispatchTestRepo) FinishActiveRecovery(_ context.Context, probe PassiveQualityState, result PassiveQualityActiveRecoveryResult, _ time.Time) error {
	r.activeCalls++
	r.activeProbe, r.result = probe, result
	return nil
}

func TestPassiveQualityGroupProbeCannotUseConnectivityRecovery(t *testing.T) {
	for _, purpose := range []string{"group_quality_target", "cache_target"} {
		for _, mode := range []string{"missing", "traffic"} {
			t.Run(purpose+"/"+mode, func(t *testing.T) {
				account := &Account{ID: 62, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Concurrency: 1,
					Credentials: map[string]any{"api_key": "test", "base_url": "https://upstream.example.com"},
					Extra:       map[string]any{openai_compat.ExtraKeyResponsesSupported: false}}
				upstream := &modelIdentityHTTPUpstream{body: `{"choices":[{"message":{"content":"OK"},"finish_reason":"stop"}]}`}
				accountTest := &AccountTestService{accountRepo: &modelIdentityAccountRepo{account: account}, httpUpstream: upstream, cfg: &config.Config{}}
				repo := &passiveActiveProbeDispatchTestRepo{}
				s := NewPassiveQualityService(repo, accountTest)
				defer s.cancel()
				probe := PassiveQualityState{Purpose: purpose, AccountID: 62, PolicyID: 1, Version: 2, Generation: 3, Model: "model"}
				if mode != "missing" {
					probe.ActiveRecoveryConfig = &PassiveQualityConfig{Purpose: purpose, RecoveryMode: mode}
				}
				s.probe(probe)
				require.Equal(t, 1, repo.activeCalls)
				require.Equal(t, probe, repo.activeProbe)
				require.Equal(t, "pending", repo.result.Verdict)
				require.False(t, repo.result.Passed)
				require.Zero(t, repo.probe.PolicyID, "a connectivity callback cannot recover a group target")
				require.Empty(t, upstream.requestModel, "unavailable active recovery must not send a connectivity-only request")
			})
		}
	}
}

func TestPassiveQualityProbeUsesObservedOrConfiguredModel(t *testing.T) {
	for _, model := range []string{"", "configured-model"} {
		t.Run("filter="+model, func(t *testing.T) {
			account := &Account{ID: 62, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Concurrency: 1,
				Credentials: map[string]any{"api_key": "test", "base_url": "https://upstream.example.com", "model_mapping": map[string]any{"observed-model": "mapped-observed", "configured-model": "mapped-configured"}},
				Extra:       map[string]any{openai_compat.ExtraKeyResponsesSupported: false}}
			upstream := &modelIdentityHTTPUpstream{body: `{"choices":[{"message":{"content":"OK"},"finish_reason":"stop"}]}`}
			test := &AccountTestService{accountRepo: &modelIdentityAccountRepo{account: account}, httpUpstream: upstream, cfg: &config.Config{}}
			repo := &passiveProbeTestRepo{}
			s := NewPassiveQualityService(repo, test)
			defer s.cancel()
			probe := PassiveQualityState{AccountID: 62, PolicyID: 1, Version: 2, Generation: 3, Model: model, ProbeModel: "observed-model"}
			s.probe(probe)
			want := "mapped-observed"
			if model != "" {
				want = "mapped-configured"
			}
			require.Equal(t, want, upstream.requestModel)
			require.True(t, repo.success)
			require.Equal(t, probe, repo.probe, "the original policy and generation fence must survive model selection")
		})
	}
}

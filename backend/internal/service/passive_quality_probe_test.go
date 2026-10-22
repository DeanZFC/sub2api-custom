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

package service

import (
	"context"
	"errors"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai_compat"
	"github.com/stretchr/testify/require"
)

type recoveryChannelFailureRepo struct {
	ChannelRepository
	calls int
}

func (r *recoveryChannelFailureRepo) ListAll(context.Context) ([]Channel, error) {
	r.calls++
	return nil, errors.New("database unavailable")
}
func recoveryMappedChannels(channels []Channel, platforms map[int64]string) *ChannelService {
	s := &ChannelService{}
	s.cache.Store(populateChannelCache(channels, platforms))
	return s
}
func recoveryModelContext(group int64) context.Context {
	return context.WithValue(context.Background(), passiveRecoveryRequestKey{}, &passiveRecoveryRequest{session: "mapped-recovery", attempts: &atomic.Int64{}, config: &PassiveQualityConfig{GroupIDs: []int64{group}}})
}
func TestPassiveRecoveryGroupMappingPrecedesAccountMappingOnWire(t *testing.T) {
	for _, platform := range []string{PlatformOpenAI, PlatformAnthropic} {
		t.Run(platform, func(t *testing.T) {
			account := &Account{ID: 42, Platform: platform, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 1, Credentials: map[string]any{"api_key": "test", "base_url": "https://upstream.example.com", "model_mapping": map[string]any{"channel-a": "wire-a", "channel-b": "wire-b"}}, Extra: map[string]any{openai_compat.ExtraKeyResponsesSupported: true}}
			channels := recoveryMappedChannels([]Channel{
				{ID: 1, Status: StatusActive, GroupIDs: []int64{8}, ModelMapping: map[string]map[string]string{platform: {"public-alias": "channel-a"}}},
				{ID: 2, Status: StatusActive, GroupIDs: []int64{9}, ModelMapping: map[string]map[string]string{platform: {"public-alias": "channel-b"}}},
			}, map[int64]string{8: platform, 9: platform})
			upstream := &modelIdentityHTTPUpstream{}
			for i := 0; i < 2; i++ {
				protocol := APIProtocolResponses
				if platform == PlatformAnthropic {
					protocol = APIProtocolAnthropic
				}
				upstream.responses = append(upstream.responses, recoveryProtocolResponse(protocol))
			}
			svc := &AccountTestService{accountRepo: &modelIdentityAccountRepo{account: account}, httpUpstream: upstream, cfg: &config.Config{}, openaiGatewayService: &OpenAIGatewayService{channelService: channels}}
			for i, group := range []int64{8, 9} {
				result, err := svc.RunTestBackgroundWithPrompt(recoveryModelContext(group), 42, "public-alias", "Explain the test.")
				require.NoError(t, err)
				require.Equal(t, "success", result.Status, result.ErrorMessage)
				require.Equal(t, []string{"wire-a", "wire-b"}[i], upstream.requestModel)
				samples, reason := passiveRecoverySamples(result, nil, "public-alias", "test-session", false, time.Now())
				require.Empty(t, reason)
				require.NotEmpty(t, samples)
				for _, sample := range samples {
					require.Equal(t, "public-alias", sample.Model, "wire mapping must not change quality state ownership")
				}
			}
			require.Equal(t, 2, upstream.requests)
		})
	}
}
func TestPassiveRecoveryGroupMappingHonorsBillingRestrictionSource(t *testing.T) {
	now := time.Now()
	account := &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"model_mapping": map[string]any{"channel-model": "wire-model"}}}
	for _, tc := range []struct{ source, allowed string }{{BillingModelSourceRequested, "public-alias"}, {BillingModelSourceChannelMapped, "channel-model"}, {BillingModelSourceResponse, "channel-model"}, {BillingModelSourceUpstream, "wire-model"}} {
		t.Run(tc.source, func(t *testing.T) {
			channel := Channel{ID: 1, Status: StatusActive, GroupIDs: []int64{8}, RestrictModels: true, BillingModelSource: tc.source, ModelMapping: map[string]map[string]string{PlatformOpenAI: {"public-alias": "channel-model"}}, ModelPricing: []ChannelModelPricing{{Platform: PlatformOpenAI, Models: []string{tc.allowed}}}}
			svc := &AccountTestService{openaiGatewayService: &OpenAIGatewayService{channelService: recoveryMappedChannels([]Channel{channel}, map[int64]string{8: PlatformOpenAI})}}
			model, deferred := svc.passiveRecoveryModel(recoveryModelContext(8), account, "public-alias", now)
			require.Nil(t, deferred)
			require.Equal(t, "channel-model", model)
			channel.ModelPricing[0].Models = []string{"different-model"}
			svc.openaiGatewayService.channelService = recoveryMappedChannels([]Channel{channel}, map[int64]string{8: PlatformOpenAI})
			_, deferred = svc.passiveRecoveryModel(recoveryModelContext(8), account, "public-alias", now)
			require.NotNil(t, deferred)
			require.Equal(t, "active_recovery_channel_model_restricted", deferred.Reason)
		})
	}
}
func TestPassiveRecoveryMappingFailuresAndRestrictionsSendNoRequests(t *testing.T) {
	account := &Account{ID: 42, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Credentials: map[string]any{"api_key": "test", "base_url": "https://upstream.example.com"}}
	for _, kind := range []string{"load_failure", "restricted"} {
		t.Run(kind, func(t *testing.T) {
			repo := &recoveryChannelFailureRepo{}
			channels := &ChannelService{repo: repo}
			want := "active_recovery_channel_mapping_unavailable"
			if kind == "restricted" {
				channels = recoveryMappedChannels([]Channel{{ID: 1, Status: StatusActive, GroupIDs: []int64{8}, RestrictModels: true}}, map[int64]string{8: PlatformOpenAI})
				want = "active_recovery_channel_model_restricted"
			}
			upstream := &modelIdentityHTTPUpstream{}
			svc := &AccountTestService{accountRepo: &modelIdentityAccountRepo{account: account}, httpUpstream: upstream, cfg: &config.Config{}, openaiGatewayService: &OpenAIGatewayService{channelService: channels}}
			for i := 0; i < 2; i++ {
				result, err := svc.RunTestBackgroundWithPrompt(recoveryModelContext(8), 42, "public-alias", "Explain.")
				require.NoError(t, err)
				require.True(t, QualityResultDeferred(result))
				require.Contains(t, result.ErrorMessage, want)
			}
			require.Zero(t, upstream.requests, "configuration uncertainty cannot spend active request budget")
			if kind == "load_failure" {
				require.Equal(t, 1, repo.calls, "cached DB failure must remain distinct from an unmapped group")
			}
		})
	}
}
func TestPassiveRecoveryNoChannelRetainsPublicModel(t *testing.T) {
	for _, svc := range []*AccountTestService{{}, {openaiGatewayService: &OpenAIGatewayService{channelService: recoveryMappedChannels(nil, nil)}}} {
		model, deferred := svc.passiveRecoveryModel(recoveryModelContext(8), &Account{}, "public-model", time.Now())
		require.Nil(t, deferred)
		require.Equal(t, "public-model", model)
	}
}
func TestPassiveRecoveryRunnerKeepsPublicModelAfterChannelResolution(t *testing.T) {
	account := &Account{ID: 42, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 1, Credentials: map[string]any{"api_key": "test", "base_url": "https://upstream.example.com", "model_mapping": map[string]any{"channel-model": "wire-model"}}, Extra: map[string]any{openai_compat.ExtraKeyResponsesSupported: true}}
	channels := recoveryMappedChannels([]Channel{{ID: 1, Status: StatusActive, GroupIDs: []int64{8}, ModelMapping: map[string]map[string]string{PlatformOpenAI: {"public-alias": "channel-model"}}}}, map[int64]string{8: PlatformOpenAI})
	upstream := &modelIdentityHTTPUpstream{beforeReturn: func(_ *http.Request, _ int) {}}
	for i := 0; i < 12; i++ {
		upstream.responses = append(upstream.responses, recoveryProtocolResponse(APIProtocolResponses))
	}
	svc := &AccountTestService{accountRepo: &modelIdentityAccountRepo{account: account}, httpUpstream: upstream, cfg: &config.Config{}, openaiGatewayService: &OpenAIGatewayService{channelService: channels}}
	now := time.Now()
	runner := passiveActiveRecoveryRunner{request: svc.RunTestBackgroundWithPrompt, now: func() time.Time { return now }, wait: func(_ context.Context, d time.Duration) error { now = now.Add(d); return nil }}
	probe := activeRecoveryFixture()
	probe.Model = "public-alias"
	probe.ActiveRecoveryConfig.GroupIDs = []int64{8}
	result := runner.run(context.Background(), probe)
	require.True(t, result.Passed, "%s: %s", result.Verdict, result.Reason)
	require.Equal(t, 12, upstream.requests)
	require.Equal(t, "wire-model", upstream.requestModel)
	require.Equal(t, "public-alias", result.Evidence.ProbeModel)
	for _, batch := range result.EvidenceBatches {
		require.Equal(t, "public-alias", batch.ProbeModel)
	}
}

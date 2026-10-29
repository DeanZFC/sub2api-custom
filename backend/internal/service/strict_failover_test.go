package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/stretchr/testify/require"
)

type strictAccountRepo struct {
	schedulerTestOpenAIAccountRepo
	err error
}

func (r *strictAccountRepo) GetByIDs(_ context.Context, ids []int64) ([]*Account, error) {
	if r.err != nil {
		return nil, r.err
	}
	out := []*Account{}
	for i := range r.accounts {
		for _, id := range ids {
			if r.accounts[i].ID == id {
				a := r.accounts[i]
				out = append(out, &a)
			}
		}
	}
	return out, nil
}
func strictFixture() (*Group, *strictAccountRepo) {
	g := &Group{ID: 75, Name: "strict", Platform: PlatformOpenAI, Status: StatusActive, Hydrated: true, StrictFailover: StrictFailoverConfig{Enabled: true, Tiers: [][]int64{{1, 2}, {3}}}}
	r := &strictAccountRepo{}
	for _, id := range []int64{1, 2, 3, 4} {
		r.accounts = append(r.accounts, Account{ID: id, Name: "account", Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 1, GroupIDs: []int64{75}, AccountGroups: []AccountGroup{{AccountID: id, GroupID: 75}}, Credentials: map[string]any{"model_mapping": map[string]any{"gpt-5.1": "gpt-5.1"}}})
	}
	return g, r
}
func strictContext(g *Group) context.Context {
	return withStrictFailoverFailures(context.WithValue(context.Background(), ctxkey.Group, g))
}
func strictNoExtraGate(*Account) (string, error) { return "", nil }

func TestStrictFailoverTierState(t *testing.T) {
	tests := []struct {
		name      string
		mutate    func(*Group, *strictAccountRepo)
		excluded  map[int64]struct{}
		evidence  bool
		wantTier  int
		wantError error
	}{
		{name: "primary", wantTier: 1},
		{name: "one_primary_disabled", mutate: func(_ *Group, r *strictAccountRepo) { r.accounts[0].Schedulable = false }, wantTier: 1},
		{name: "all_primary_disabled", mutate: func(_ *Group, r *strictAccountRepo) {
			r.accounts[0].Schedulable = false
			r.accounts[1].Status = "error"
		}, wantTier: 2},
		{name: "rate_limited", mutate: func(_ *Group, r *strictAccountRepo) {
			until := time.Now().Add(time.Minute)
			r.accounts[0].RateLimitResetAt = &until
			r.accounts[1].RateLimitResetAt = &until
		}, wantTier: 2},
		{name: "unconfirmed_exclusions", excluded: map[int64]struct{}{1: {}, 2: {}}, wantError: ErrStrictFailoverUnconfirmed},
		{name: "upstream_evidence", excluded: map[int64]struct{}{1: {}, 2: {}}, evidence: true, wantTier: 2},
		{name: "database_error", mutate: func(_ *Group, r *strictAccountRepo) { r.err = errors.New("db offline") }, wantError: ErrStrictFailoverUnconfirmed},
		{name: "all_down", mutate: func(_ *Group, r *strictAccountRepo) {
			for i := range r.accounts {
				r.accounts[i].Schedulable = false
			}
		}, wantError: ErrNoAvailableAccounts},
		{name: "deleted_and_detached", mutate: func(_ *Group, r *strictAccountRepo) {
			r.accounts = r.accounts[1:]
			r.accounts[0].GroupIDs = nil
			r.accounts[0].AccountGroups = nil
		}, wantTier: 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g, r := strictFixture()
			if tt.mutate != nil {
				tt.mutate(g, r)
			}
			ctx := strictContext(g)
			if tt.evidence {
				for _, id := range []int64{1, 2} {
					RecordStrictFailoverFailure(ctx, id, &UpstreamFailoverError{StatusCode: 429, ObservedUpstreamStatus: 429})
				}
			}
			selected, err := prepareStrictFailover(ctx, g, r, tt.excluded, "gpt-5.1", strictNoExtraGate)
			if tt.wantError != nil {
				require.ErrorIs(t, err, tt.wantError)
				if tt.wantError == ErrStrictFailoverUnconfirmed {
					require.NotErrorIs(t, err, ErrNoAvailableAccounts)
				}
				return
			}
			require.NoError(t, err)
			state := strictFailoverStateFrom(selected)
			require.Equal(t, tt.wantTier, state.tier)
			require.False(t, strictFailoverAllows(selected, 4))
			if state.tier == 1 {
				require.False(t, strictFailoverAllows(selected, 3))
			}
		})
	}
}

func TestStrictFailoverFailureEvidence(t *testing.T) {
	for _, tt := range []struct {
		name    string
		err     *UpstreamFailoverError
		allowed bool
	}{
		{"upstream_429", &UpstreamFailoverError{StatusCode: 429, ObservedUpstreamStatus: 429}, true},
		{"actual_503", &UpstreamFailoverError{StatusCode: 503, ObservedUpstreamStatus: 503, ResponseHeaders: http.Header{"X-Request-Id": []string{"r1"}}}, true},
		{"synthetic_502_with_body_headers", &UpstreamFailoverError{StatusCode: 502, ResponseBody: []byte("stream truncated"), ResponseHeaders: http.Header{"X-Request-Id": []string{"r1"}}}, false},
		{"unclassified_stream", newOpenAIUpstreamFailoverError(502, nil, []byte("connection lost"), "", false), false},
		{"explicit_billing_event", newUpstreamBillingFailoverError(402, nil, []byte("insufficient quota"), false), true},
		{"synthetic_timeout", &UpstreamFailoverError{StatusCode: 504}, false},
		{"client_400", &UpstreamFailoverError{StatusCode: 400}, false},
		{"request_scoped", &UpstreamFailoverError{StatusCode: 429, RequestScopedTransient: true}, false},
		{"provider_scoped", &UpstreamFailoverError{StatusCode: 503, Scope: GatewayFailureScopeProvider}, false},
		{"canceled_request", &UpstreamFailoverError{StatusCode: 499}, false},
		{"stop", &UpstreamFailoverError{StatusCode: 401, NextAccountAction: NextAccountStop}, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ctx := withStrictFailoverFailures(context.Background())
			RecordStrictFailoverFailure(ctx, 1, tt.err)
			require.Equal(t, tt.allowed, strictFailureReason(ctx, 1) != "")
		})
	}
	ctx, cancel := context.WithCancel(withStrictFailoverFailures(context.Background()))
	cancel()
	RecordStrictFailoverFailure(ctx, 1, &UpstreamFailoverError{StatusCode: 429, ObservedUpstreamStatus: 429})
	require.Empty(t, strictFailureReason(ctx, 1))
}

func TestStrictFailoverOpenAIScheduler(t *testing.T) {
	for _, advanced := range []bool{false, true} {
		for _, busy := range []bool{false, true} {
			t.Run(map[bool]string{false: "legacy", true: "advanced"}[advanced]+map[bool]string{false: "/available", true: "/busy"}[busy], func(t *testing.T) {
				g, r := strictFixture()
				ctx := strictContext(g)
				r.accounts[2].Priority = -1000 // A better-scoring backup must never enter the pool.
				cache := &schedulerTestGatewayCache{}
				require.NoError(t, cache.SetSessionAccountID(ctx, g.ID, "strict-session", 3, time.Hour))
				cfg := &config.Config{}
				cfg.Gateway.Scheduling.LoadBatchEnabled = true
				cfg.Gateway.OpenAIWS.LBTopK = 1
				cfg.Gateway.OpenAIWS.SchedulerScoreWeights.Priority = 100
				acquired := []int64{}
				cc := schedulerTestConcurrencyCache{acquiredIDs: &acquired, acquireResults: map[int64]bool{1: !busy, 2: !busy, 3: true}}
				if busy {
					cc.loadMap = map[int64]*AccountLoadInfo{1: {AccountID: 1, CurrentConcurrency: 1, LoadRate: 100}, 2: {AccountID: 2, CurrentConcurrency: 1, LoadRate: 100}}
				}
				svc := &OpenAIGatewayService{accountRepo: r, cache: cache, cfg: cfg, concurrencyService: NewConcurrencyService(cc), rateLimitService: newOpenAIAdvancedSchedulerRateLimitService(map[bool]string{false: "false", true: "true"}[advanced], "true", "true")}
				selection, _, err := svc.SelectAccountWithScheduler(ctx, &g.ID, "", "strict-session", "gpt-5.1", nil, OpenAIUpstreamTransportAny, false)
				require.NoError(t, err)
				require.NotNil(t, selection)
				require.Contains(t, []int64{1, 2}, selection.Account.ID)
				require.NotContains(t, acquired, int64(3))
				require.Equal(t, !busy, selection.Acquired)
				if busy {
					require.NotNil(t, selection.WaitPlan)
				}
				if selection.ReleaseFunc != nil {
					selection.ReleaseFunc()
				}
			})
		}
	}
}

func TestStrictFailoverOpenAIRecoveryAndRetry(t *testing.T) {
	g, r := strictFixture()
	ctx := strictContext(g)
	svc := &OpenAIGatewayService{accountRepo: r, cache: &schedulerTestGatewayCache{}, cfg: &config.Config{}, rateLimitService: newOpenAIAdvancedSchedulerRateLimitService("true")}
	excluded := map[int64]struct{}{1: {}}
	RecordStrictFailoverFailure(ctx, 1, &UpstreamFailoverError{StatusCode: 429, ObservedUpstreamStatus: 429})
	selected, _, err := svc.SelectAccountWithScheduler(ctx, &g.ID, "", "", "gpt-5.1", excluded, OpenAIUpstreamTransportAny, false)
	require.NoError(t, err)
	require.Equal(t, int64(2), selected.Account.ID)
	excluded[2] = struct{}{}
	RecordStrictFailoverFailure(ctx, 2, &UpstreamFailoverError{StatusCode: 401, ObservedUpstreamStatus: 401})
	selected, _, err = svc.SelectAccountWithScheduler(ctx, &g.ID, "", "", "gpt-5.1", excluded, OpenAIUpstreamTransportAny, false)
	require.NoError(t, err)
	require.Equal(t, int64(3), selected.Account.ID)
	selected, _, err = svc.SelectAccountWithScheduler(strictContext(g), &g.ID, "", "", "gpt-5.1", nil, OpenAIUpstreamTransportAny, false)
	require.NoError(t, err)
	require.Contains(t, []int64{1, 2}, selected.Account.ID)
}

func TestStrictFailoverOwnerBound(t *testing.T) {
	for _, advanced := range []string{"false", "true"} {
		t.Run(advanced, func(t *testing.T) {
			g, r := strictFixture()
			ctx := strictContext(g)
			svc := &OpenAIGatewayService{accountRepo: r, cache: &schedulerTestGatewayCache{}, cfg: newSchedulerTestOpenAIWSV2Config(), rateLimitService: newOpenAIAdvancedSchedulerRateLimitService(advanced)}
			require.NoError(t, svc.getOpenAIWSStateStore().BindResponseAccount(ctx, g.ID, "resp_strict", 3, time.Hour))
			selection, _, err := svc.SelectAccountWithScheduler(ctx, &g.ID, "resp_strict", "", "gpt-5.1", nil, OpenAIUpstreamTransportAny, false)
			require.Nil(t, selection)
			require.ErrorIs(t, err, ErrStrictFailoverOwnerBound)
		})
	}
}

func TestStrictFailoverValidationAndAuthCache(t *testing.T) {
	g, r := strictFixture()
	svc := &adminServiceImpl{accountRepo: r}
	require.NoError(t, svc.validateStrictFailover(context.Background(), g.ID, g.StrictFailover))
	for _, cfg := range []StrictFailoverConfig{{Enabled: true}, {Enabled: true, Tiers: [][]int64{{}}}, {Enabled: true, Tiers: [][]int64{{1}, {1}}}, {Enabled: true, Tiers: [][]int64{{-1}}}, {Enabled: true, Tiers: [][]int64{{999}}}} {
		require.Error(t, svc.validateStrictFailover(context.Background(), g.ID, cfg))
	}
	key := &APIKey{ID: 1, UserID: 2, GroupID: &g.ID, Key: "test-strict", Status: StatusActive, User: &User{ID: 2, Status: StatusActive}, Group: g}
	api := &APIKeyService{}
	payload, err := json.Marshal(&APIKeyAuthCacheEntry{Snapshot: api.snapshotFromAPIKey(context.Background(), key)})
	require.NoError(t, err)
	var entry APIKeyAuthCacheEntry
	require.NoError(t, json.Unmarshal(payload, &entry))
	materialized, used, err := api.applyAuthCacheEntry(key.Key, &entry)
	require.NoError(t, err)
	require.True(t, used)
	require.Equal(t, g.StrictFailover, materialized.Group.StrictFailover)
	materialized.Group.StrictFailover.Tiers[0][0] = 999
	require.Equal(t, int64(1), entry.Snapshot.Group.StrictFailover.Tiers[0][0])
}

func TestStrictFailoverUncertainDoesNotUseAPIKeyFallback(t *testing.T) {
	g, r := strictFixture()
	fallback := &Group{ID: 76, Name: "backup", Platform: PlatformOpenAI, Status: StatusActive, Hydrated: true}
	ctx := WithAPIKeyGroupFallbackRouting(context.Background(), &APIKey{GroupID: &g.ID, Group: g, FallbackGroupID: &fallback.ID, FallbackGroup: fallback})
	ctx, err := prepareStrictFailover(ctx, g, r, nil, "gpt-5.1", strictNoExtraGate)
	require.NoError(t, err)
	_, _, ok := apiKeyFallbackGroupForSelection(ctx, &g.ID, strictFailoverSelectionError(ctx, ErrNoAvailableAccounts))
	require.False(t, ok)
}

func TestStrictFailoverLocalFailureCooldownCannotAuthorizeDowngrade(t *testing.T) {
	g, r := strictFixture()
	until := time.Now().Add(time.Minute)
	for i := 0; i < 2; i++ {
		r.accounts[i].TempUnschedulableUntil = &until
	}
	ctx := strictContext(g)
	for _, id := range []int64{1, 2} {
		RecordStrictFailoverFailure(ctx, id, &UpstreamFailoverError{StatusCode: 502, ResponseBody: []byte("stream truncated")})
	}
	_, err := prepareStrictFailover(ctx, g, r, map[int64]struct{}{1: {}, 2: {}}, "gpt-5.1", strictNoExtraGate)
	require.ErrorIs(t, err, ErrStrictFailoverUnconfirmed)
	require.NotErrorIs(t, err, ErrNoAvailableAccounts)
}

func TestStrictFailoverGatewayAndGemini(t *testing.T) {
	for _, platform := range []string{PlatformAnthropic, PlatformGemini} {
		for _, path := range []string{"simple", "load", "compat"} {
			if path == "compat" && platform != PlatformGemini {
				continue
			}
			t.Run(platform+"/"+path, func(t *testing.T) {
				g, r := strictFixture()
				g.Platform = platform
				for i := range r.accounts {
					r.accounts[i].Platform = platform
					r.accounts[i].Credentials = map[string]any{}
				}
				r.accounts[2].Priority = -999
				ctx := strictContext(g)
				cache := &schedulerTestGatewayCache{}
				require.NoError(t, cache.SetSessionAccountID(ctx, g.ID, "session", 3, time.Hour))
				require.NoError(t, cache.SetSessionAccountID(ctx, g.ID, "gemini:session", 3, time.Hour))
				gateway := &GatewayService{accountRepo: r, cache: cache}
				gemini := &GeminiMessagesCompatService{accountRepo: r, cache: cache}
				selectAccount := func(excluded map[int64]struct{}) (*Account, error) {
					if path == "compat" {
						return gemini.SelectAccountForModelWithExclusions(ctx, &g.ID, "session", "", excluded)
					}
					if path == "load" {
						selected, err := gateway.SelectAccountWithLoadAwareness(ctx, &g.ID, "session", "", excluded, "", 0)
						if err != nil {
							return nil, err
						}
						return selected.Account, nil
					}
					return gateway.SelectAccountForModelWithExclusions(ctx, &g.ID, "session", "", excluded)
				}
				selected, err := selectAccount(nil)
				require.NoError(t, err)
				require.Contains(t, []int64{1, 2}, selected.ID)
				_, err = selectAccount(map[int64]struct{}{1: {}, 2: {}})
				require.ErrorIs(t, err, ErrStrictFailoverUnconfirmed)
				r.accounts[0].Schedulable = false
				r.accounts[1].Schedulable = false
				selected, err = selectAccount(nil)
				require.NoError(t, err)
				require.Equal(t, int64(3), selected.ID)
			})
		}
	}
}

func TestStrictFailoverBoundConnectionRecovery(t *testing.T) {
	g, r := strictFixture()
	svc := &OpenAIGatewayService{accountRepo: r}
	ctx := strictContext(g)
	r.accounts[0].Schedulable = false
	r.accounts[1].Schedulable = false
	require.NoError(t, svc.ValidateStrictFailoverBoundAccount(ctx, &g.ID, &r.accounts[2], "gpt-5.1", OpenAIEndpointCapabilityResponses, OpenAIUpstreamTransportAny))
	r.accounts[0].Schedulable = true
	require.ErrorIs(t, svc.ValidateStrictFailoverBoundAccount(ctx, &g.ID, &r.accounts[2], "gpt-5.1", OpenAIEndpointCapabilityResponses, OpenAIUpstreamTransportAny), ErrStrictFailoverOwnerBound)
	require.NoError(t, svc.ValidateStrictFailoverBoundAccount(ctx, &g.ID, &r.accounts[0], "gpt-5.1", OpenAIEndpointCapabilityResponses, OpenAIUpstreamTransportAny))
}

func TestStrictFailoverTransportCapabilities(t *testing.T) {
	g, r := strictFixture()
	for i := range r.accounts {
		r.accounts[i].Extra = map[string]any{"openai_apikey_responses_websockets_v2_enabled": i >= 2}
	}
	svc := &OpenAIGatewayService{accountRepo: r, cfg: newSchedulerTestOpenAIWSV2Config()}
	selected, err := svc.prepareOpenAIStrictFailover(strictContext(g), &g.ID, nil, "gpt-5.1", PlatformOpenAI, OpenAIEndpointCapabilityResponses, "", false, OpenAIUpstreamTransportResponsesWebsocketV2)
	require.NoError(t, err)
	require.Equal(t, 2, strictFailoverStateFrom(selected).tier)
	selected, err = svc.prepareOpenAIStrictFailover(strictContext(g), &g.ID, nil, "gpt-5.1", PlatformOpenAI, OpenAIEndpointCapabilityResponses, "", false, OpenAIUpstreamTransportHTTPSSE)
	require.NoError(t, err)
	require.Equal(t, 1, strictFailoverStateFrom(selected).tier)
}

func TestStrictFailoverContinuationNeverSwitchesSameTier(t *testing.T) {
	for _, advanced := range []string{"true", "false"} {
		t.Run(advanced, func(t *testing.T) {
			g, r := strictFixture()
			ctx := strictContext(g)
			acquired := []int64{}
			cache := schedulerTestConcurrencyCache{acquiredIDs: &acquired}
			concurrency := NewConcurrencyService(cache)
			concurrency.SetAccountAdmissionGate(accountAdmissionGateFunc(func(_ context.Context, id int64) (bool, error) { return id != 1, nil }))
			svc := &OpenAIGatewayService{accountRepo: r, cache: &schedulerTestGatewayCache{}, concurrencyService: concurrency, cfg: newSchedulerTestOpenAIWSV2Config(), rateLimitService: newOpenAIAdvancedSchedulerRateLimitService(advanced)}
			require.NoError(t, svc.getOpenAIWSStateStore().BindResponseAccount(ctx, g.ID, "resp_owner_primary", 1, time.Hour))
			selected, _, err := svc.SelectAccountWithScheduler(ctx, &g.ID, "resp_owner_primary", "", "gpt-5.1", nil, OpenAIUpstreamTransportAny, false)
			require.Error(t, err)
			require.Nil(t, selected)
			for _, id := range acquired {
				require.Equal(t, int64(1), id)
			}
		})
	}
}

func TestStrictFailoverDirectContinuationGate(t *testing.T) {
	g, r := strictFixture()
	ctx := strictContext(g)
	svc := &OpenAIGatewayService{accountRepo: r, cache: &schedulerTestGatewayCache{}, cfg: newSchedulerTestOpenAIWSV2Config()}
	require.NoError(t, svc.getOpenAIWSStateStore().BindResponseAccount(ctx, g.ID, "resp_direct", 3, time.Hour))
	selected, err := svc.SelectAccountByPreviousResponseID(ctx, &g.ID, "resp_direct", "gpt-5.1", nil, false)
	require.Nil(t, selected)
	require.ErrorIs(t, err, ErrStrictFailoverOwnerBound)
}

func TestStrictFailoverUpstreamEventEvidence(t *testing.T) {
	svc := &OpenAIGatewayService{}
	ctx := withStrictFailoverFailures(context.Background())
	failure := svc.newOpenAIWSRateLimitFailoverError(&Account{Type: AccountTypeAPIKey}, nil, []byte("upstream rate_limit_exceeded"), "rate limit exceeded")
	require.Zero(t, failure.ObservedUpstreamStatus)
	RecordStrictFailoverFailure(ctx, 1, failure)
	require.Equal(t, "upstream_rate_limit_event", strictFailureReason(ctx, 1))
	failure.RequestScopedTransient = true
	RecordStrictFailoverFailure(ctx, 2, failure)
	require.Empty(t, strictFailureReason(ctx, 2))
}

func TestStrictFailoverExternalFallbackRequiresCompleteEvidence(t *testing.T) {
	g, r := strictFixture()
	backup := &Group{ID: 76, Platform: PlatformOpenAI, Status: StatusActive, Hydrated: true}
	ctx := WithAPIKeyGroupFallbackRouting(context.Background(), &APIKey{GroupID: &g.ID, Group: g, FallbackGroupID: &backup.ID, FallbackGroup: backup})
	for _, err := range []error{ErrNoAvailableAccounts, ErrNoAvailableCompactAccounts, ErrAccountQualityHeld, ErrAccountAdmissionDenied} {
		_, _, allowed := apiKeyFallbackGroupForSelection(ctx, &g.ID, err)
		require.False(t, allowed)
	}
	for i := range r.accounts {
		r.accounts[i].Schedulable = false
	}
	_, err := prepareStrictFailover(ctx, g, r, nil, "gpt-5.1", strictNoExtraGate)
	require.ErrorIs(t, err, ErrNoAvailableAccounts)
	selected, _, allowed := apiKeyFallbackGroupForSelection(ctx, &g.ID, err)
	require.True(t, allowed)
	require.Equal(t, backup, selected)
}

func TestStrictFailoverClientRestrictionDoesNotBypassTiers(t *testing.T) {
	g, _ := strictFixture()
	g.ClaudeCodeOnly = true
	backupID := int64(76)
	g.FallbackGroupID = &backupID
	svc := &GatewayService{}
	_, _, err := svc.resolveGatewayGroup(strictContext(g), &g.ID)
	require.ErrorIs(t, err, ErrClaudeCodeOnly)
}

func TestStrictFailoverFallbackGroupCacheRoundTrip(t *testing.T) {
	g, _ := strictFixture()
	snapshot := apiKeyAuthGroupSnapshotFromGroup(g)
	require.Equal(t, g.StrictFailover, snapshot.StrictFailover)
	g.StrictFailover.Tiers[0][0] = 999
	require.Equal(t, int64(1), snapshot.StrictFailover.Tiers[0][0])
	serialized, err := json.Marshal(snapshot)
	require.NoError(t, err)
	var restored APIKeyAuthGroupSnapshot
	require.NoError(t, json.Unmarshal(serialized, &restored))
	got := apiKeyAuthGroupFromSnapshot(&restored)
	require.True(t, got.StrictFailover.Enabled)
	require.Equal(t, [][]int64{{1, 2}, {3}}, got.StrictFailover.Tiers)
	got.StrictFailover.Tiers[0][0] = 777
	require.Equal(t, int64(1), restored.StrictFailover.Tiers[0][0])
}

func TestStrictFailoverExhaustedContinuationCannotEscapeToAnotherGroup(t *testing.T) {
	g, r := strictFixture()
	fallback := &Group{ID: 76, Platform: PlatformOpenAI, Status: StatusActive, Hydrated: true}
	ctx := WithAPIKeyGroupFallbackRouting(context.Background(), &APIKey{GroupID: &g.ID, Group: g, FallbackGroupID: &fallback.ID, FallbackGroup: fallback})
	for i := range r.accounts {
		r.accounts[i].Schedulable = false
	}
	svc := &OpenAIGatewayService{accountRepo: r, cache: &schedulerTestGatewayCache{}, cfg: newSchedulerTestOpenAIWSV2Config()}
	require.NoError(t, svc.getOpenAIWSStateStore().BindResponseAccount(ctx, g.ID, "resp_exhausted", 1, time.Hour))
	selected, _, err := svc.SelectAccountWithScheduler(ctx, &g.ID, "resp_exhausted", "", "gpt-5.1", nil, OpenAIUpstreamTransportAny, false)
	require.Nil(t, selected)
	require.ErrorIs(t, err, ErrStrictFailoverOwnerBound)
}

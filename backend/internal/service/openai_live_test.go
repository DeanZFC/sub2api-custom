package service

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	coderws "github.com/coder/websocket"
	"github.com/stretchr/testify/require"
)

type strictLiveTestStore struct{ liveTestStore }

func (*strictLiveTestStore) ClaimLiveController(context.Context, string, string, string) (bool, error) {
	return false, nil // The test covers call creation; no observer connection is needed.
}

type strictLiveTestConcurrencyCache struct {
	schedulerTestConcurrencyCache
	liveReleases []int64
}

func (*strictLiveTestConcurrencyCache) AcquireLiveLease(context.Context, int64, int, int64, int, int64, string, bool) (bool, error) {
	return true, nil
}

func (*strictLiveTestConcurrencyCache) RefreshLiveLease(context.Context, int64, int64, int64, string) (bool, error) {
	return true, nil
}

func (c *strictLiveTestConcurrencyCache) ReleaseLiveLease(_ context.Context, accountID, _ int64, _ int64, _ string) error {
	c.liveReleases = append(c.liveReleases, accountID)
	return nil
}

type strictLiveSequenceUpstream struct {
	accountIDs []int64
	succeedID  int64
	err        error
}

func (s *strictLiveSequenceUpstream) Do(request *http.Request, _ string, accountID int64, _ int) (*http.Response, error) {
	s.accountIDs = append(s.accountIDs, accountID)
	if s.err != nil {
		return nil, s.err
	}
	if accountID == s.succeedID {
		return &http.Response{StatusCode: http.StatusCreated, Header: http.Header{"Location": {"/backend-api/codex/call_strict"}}, Body: io.NopCloser(strings.NewReader("v=0\r\n"))}, nil
	}
	return &http.Response{StatusCode: http.StatusServiceUnavailable, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"error":{"message":"upstream unavailable"}}`)), Request: request}, nil
}

func (s *strictLiveSequenceUpstream) DoWithTLS(request *http.Request, proxyURL string, accountID int64, concurrency int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return s.Do(request, proxyURL, accountID, concurrency)
}

func TestCreateLiveCallStrictFailoverTraversesAllPrimaryAccounts(t *testing.T) {
	for _, strict := range []bool{false, true} {
		t.Run(map[bool]string{false: "ordinary_budget", true: "strict_tiers"}[strict], func(t *testing.T) {
			g, repo := strictFixture()
			g.StrictFailover.Enabled = strict
			g.StrictFailover.Tiers = [][]int64{{1, 2, 3, 4, 5}}
			repo.accounts = nil
			for id := int64(1); id <= 6; id++ {
				repo.accounts = append(repo.accounts, Account{ID: id, Platform: PlatformOpenAI, Type: AccountTypeOAuth,
					Status: StatusActive, Schedulable: true, Concurrency: 1, Priority: int(id), GroupIDs: []int64{g.ID},
					Credentials: map[string]any{"access_token": "test-token", "chatgpt_account_id": "test-account"}})
			}
			upstream := &strictLiveSequenceUpstream{succeedID: 6}
			if !strict {
				upstream.succeedID = 0 // Exhaust the ordinary retry budget regardless of pool order.
			}
			cache := &strictLiveTestConcurrencyCache{}
			store := &strictLiveTestStore{liveTestStore: liveTestStore{GatewayCache: &schedulerTestGatewayCache{}}}
			cfg := &config.Config{JWT: config.JWTConfig{Secret: "live-strict-test-secret"}}
			svc := &OpenAIGatewayService{accountRepo: repo, cache: store, cfg: cfg, concurrencyService: NewConcurrencyService(cache), httpUpstream: upstream,
				liveAttestation: liveAttestationStub{header: `{"v":1}`}, liveAttestationCipher: newLiveAttestationCipher(cfg)}
			ctx := strictContext(g)
			created, err := svc.CreateLiveCall(ctx, &LiveCallRequest{SDP: "v=offer\r\n", Session: json.RawMessage(`{"model":"gpt-live"}`)},
				LiveCallIdentity{GroupID: &g.ID, UserID: 1, APIKeyID: 1}, 1)
			if strict {
				require.NoError(t, err)
				require.Equal(t, int64(6), created.Account.ID)
				require.Len(t, upstream.accountIDs, 6)
				require.ElementsMatch(t, []int64{1, 2, 3, 4, 5}, upstream.accountIDs[:5])
				require.Equal(t, int64(6), upstream.accountIDs[5])
				require.Len(t, cache.liveReleases, 5, "every failed call must release its live lease")
			} else {
				require.Error(t, err)
				require.Nil(t, created)
				require.Len(t, upstream.accountIDs, 4, "ordinary Live retries retain the existing limit")
			}
		})
	}
}

func TestCreateLiveCallStrictFailoverStopsOnClientCancellation(t *testing.T) {
	g, repo := strictFixture()
	repo.accounts[0].Type = AccountTypeOAuth
	repo.accounts[0].Credentials = map[string]any{"access_token": "test-token", "chatgpt_account_id": "test-account"}
	upstream := &strictLiveSequenceUpstream{err: context.Canceled}
	store := &strictLiveTestStore{liveTestStore: liveTestStore{GatewayCache: &schedulerTestGatewayCache{}}}
	cfg := &config.Config{JWT: config.JWTConfig{Secret: "live-strict-test-secret"}}
	svc := &OpenAIGatewayService{accountRepo: repo, cache: store, cfg: cfg, concurrencyService: NewConcurrencyService(&strictLiveTestConcurrencyCache{}), httpUpstream: upstream,
		liveAttestation: liveAttestationStub{header: `{"v":1}`}, liveAttestationCipher: newLiveAttestationCipher(cfg)}
	ctx := withStrictFailoverFailures(context.WithValue(context.Background(), ctxkey.Group, g))
	_, err := svc.CreateLiveCall(ctx, &LiveCallRequest{SDP: "v=offer\r\n", Session: json.RawMessage(`{"model":"gpt-live"}`)},
		LiveCallIdentity{GroupID: &g.ID, UserID: 1, APIKeyID: 1}, 1)
	require.ErrorIs(t, err, context.Canceled)
	require.Len(t, upstream.accountIDs, 1)
	require.Empty(t, strictFailureReason(ctx, 1))
}

type liveHTTPUpstreamStub struct {
	request *http.Request
	body    []byte
}

type liveAttestationStub struct {
	header string
	err    error
}

func (s liveAttestationStub) Check(context.Context) error {
	return s.err
}

func (s liveAttestationStub) Generate(context.Context) (string, error) {
	return s.header, s.err
}

func (s *liveHTTPUpstreamStub) Do(
	request *http.Request,
	_ string,
	_ int64,
	_ int,
) (*http.Response, error) {
	s.request = request
	body, err := io.ReadAll(request.Body)
	if err != nil {
		return nil, err
	}
	s.body = body
	return &http.Response{
		StatusCode: http.StatusOK,
		Header: http.Header{
			"Location": {"/backend-api/codex/call_test"},
		},
		Body: io.NopCloser(strings.NewReader("v=0\r\n")),
	}, nil
}

func (s *liveHTTPUpstreamStub) DoWithTLS(
	request *http.Request,
	proxyURL string,
	accountID int64,
	accountConcurrency int,
	_ *tlsfingerprint.Profile,
) (*http.Response, error) {
	return s.Do(request, proxyURL, accountID, accountConcurrency)
}

func TestLiveCapabilityOnlyAllowsOpenAIOAuth(t *testing.T) {
	require.True(t, (&Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth}).SupportsOpenAIEndpointCapability(OpenAIEndpointCapabilityLive))
	require.False(t, (&Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey}).SupportsOpenAIEndpointCapability(OpenAIEndpointCapabilityLive))
	require.False(t, (&Account{Platform: PlatformGrok, Type: AccountTypeOAuth}).SupportsOpenAIEndpointCapability(OpenAIEndpointCapabilityLive))
	require.False(t, (&Account{
		Platform: PlatformOpenAI,
		Type:     AccountTypeOAuth,
		Credentials: map[string]any{
			openAIAuthModeCredentialKey: OpenAIAuthModePersonalAccessToken,
		},
	}).SupportsOpenAIEndpointCapability(OpenAIEndpointCapabilityLive))
	require.False(t, (&Account{
		Platform: PlatformOpenAI,
		Type:     AccountTypeOAuth,
		Credentials: map[string]any{
			openAIAuthModeCredentialKey: OpenAIAuthModeAgentIdentity,
		},
	}).SupportsOpenAIEndpointCapability(OpenAIEndpointCapabilityLive))
}

func TestValidateLiveCallRequestDoesNotRequireDelegation(t *testing.T) {
	request := &LiveCallRequest{
		SDP:     "v=0\r\n",
		Session: json.RawMessage(`{"model":"gpt-live-test","instructions":"hello"}`),
	}
	require.NoError(t, ValidateLiveCallRequest(request))
	require.NotContains(t, string(request.Session), "delegation")
}

func TestCreateUpstreamLiveCallPreservesSession(t *testing.T) {
	upstream := &liveHTTPUpstreamStub{}
	service := &OpenAIGatewayService{
		cfg:          &config.Config{},
		httpUpstream: upstream,
	}
	account := &Account{
		ID:          7,
		Platform:    PlatformOpenAI,
		Type:        AccountTypeOAuth,
		Concurrency: 2,
		Credentials: map[string]any{
			"access_token":       "test-access-token",
			"chatgpt_account_id": "acct_test",
		},
	}
	session := json.RawMessage(`{
		"model":"gpt-live-test",
		"delegation":{"type":"client"},
		"custom":{"keep":true}
	}`)

	created, err := service.createUpstreamLiveCall(context.Background(), account, &LiveCallRequest{
		SDP:     "v=offer\r\n",
		Session: session,
	}, `{"v":1,"s":0,"t":"v1.test"}`)
	require.NoError(t, err)
	require.Equal(t, "call_test", created.CallID)
	require.Equal(t, []byte("v=0\r\n"), created.SDP)

	var forwarded struct {
		SDP     string          `json:"sdp"`
		Session json.RawMessage `json:"session"`
	}
	require.NoError(t, json.Unmarshal(upstream.body, &forwarded))
	require.Equal(t, "v=offer\r\n", forwarded.SDP)
	require.JSONEq(t, string(session), string(forwarded.Session))
	require.Equal(t, "Bearer test-access-token", upstream.request.Header.Get("Authorization"))
	require.Equal(t, "acct_test", upstream.request.Header.Get("Chatgpt-Account-Id"))
	require.Equal(t, "quicksilver=v2", upstream.request.Header.Get("OpenAI-Alpha"))
	require.Equal(t, `{"v":1,"s":0,"t":"v1.test"}`, upstream.request.Header.Get(liveAttestationHeader))
	require.NotEmpty(t, upstream.request.Header.Get("Session-Id"))
	require.NotEmpty(t, upstream.request.Header.Get("Thread-Id"))
	require.Empty(t, upstream.request.Header.Get("OpenAI-Beta"))
	require.Equal(t, HTTPUpstreamProfileOpenAI, HTTPUpstreamProfileFromContext(upstream.request.Context()))
	require.True(t, HTTPUpstreamRedirectsDisabled(upstream.request.Context()))
}

type live429SequenceUpstream struct {
	calls int
}

func (s *live429SequenceUpstream) Do(request *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	s.calls++
	return &http.Response{
		StatusCode: http.StatusTooManyRequests,
		Header:     http.Header{"Retry-After": []string{"0"}},
		Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"rate limited"}}`)),
		Request:    request,
	}, nil
}

func (s *live429SequenceUpstream) DoWithTLS(request *http.Request, proxyURL string, accountID int64, accountConcurrency int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return s.Do(request, proxyURL, accountID, accountConcurrency)
}

func TestCreateUpstreamLiveCallReturns429WithoutRetryMarker(t *testing.T) {
	upstream := &live429SequenceUpstream{}
	svc := &OpenAIGatewayService{
		cfg:          &config.Config{},
		httpUpstream: upstream,
	}
	account := &Account{
		ID:          8,
		Platform:    PlatformOpenAI,
		Type:        AccountTypeOAuth,
		Concurrency: 1,
		Credentials: map[string]any{
			"access_token":       "test-access-token",
			"chatgpt_account_id": "acct_test",
		},
	}

	_, err := svc.createUpstreamLiveCall(context.Background(), account, &LiveCallRequest{
		SDP:     "v=offer\r\n",
		Session: json.RawMessage(`{"model":"gpt-live-test"}`),
	}, `{"v":1}`)
	var failoverErr *UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr)
	require.Equal(t, http.StatusTooManyRequests, failoverErr.StatusCode)
	require.Equal(t, 1, upstream.calls, "账号级透明 429 重试已关闭")
}

func TestLiveAttestationCipherRoundTripAndRejectsOtherInstanceKey(t *testing.T) {
	first := newLiveAttestationCipher(&config.Config{
		JWT: config.JWTConfig{Secret: "first-live-secret"},
	})
	second := newLiveAttestationCipher(&config.Config{
		JWT: config.JWTConfig{Secret: "second-live-secret"},
	})
	require.NotNil(t, first)
	require.NotNil(t, second)

	ciphertext, err := first.Encrypt(`{"v":1,"s":0,"t":"v1.opaque"}`)
	require.NoError(t, err)
	require.NotContains(t, ciphertext, "opaque")

	plaintext, err := first.Decrypt(ciphertext)
	require.NoError(t, err)
	require.Equal(t, `{"v":1,"s":0,"t":"v1.opaque"}`, plaintext)

	_, err = second.Decrypt(ciphertext)
	require.Error(t, err)
}

func TestPrepareLiveAttestationEncryptsHeaderAndReturnsExplicitProviderError(t *testing.T) {
	cipher := newLiveAttestationCipher(&config.Config{
		JWT: config.JWTConfig{Secret: "live-attestation-test-secret"},
	})
	service := &OpenAIGatewayService{
		liveAttestation:       liveAttestationStub{header: `{"v":1,"s":0,"t":"v1.test"}`},
		liveAttestationCipher: cipher,
	}
	header, ciphertext, err := service.prepareLiveAttestation(context.Background())
	require.NoError(t, err)
	require.Equal(t, `{"v":1,"s":0,"t":"v1.test"}`, header)
	require.NotContains(t, ciphertext, "v1.test")
	decrypted, err := cipher.Decrypt(ciphertext)
	require.NoError(t, err)
	require.Equal(t, header, decrypted)

	service.liveAttestation = liveAttestationStub{err: errors.New("macOS app missing")}
	_, _, err = service.prepareLiveAttestation(context.Background())
	var unavailable *LiveAttestationUnavailableError
	require.ErrorAs(t, err, &unavailable)
	require.Contains(t, unavailable.Error(), "macOS app missing")
}

func TestLiveMaxSessionDurationDefaultsAndOverrides(t *testing.T) {
	require.Equal(t, defaultLiveMaxSessionDuration, (&OpenAIGatewayService{}).liveMaxSessionDuration())
	require.Equal(
		t,
		90*time.Second,
		(&OpenAIGatewayService{cfg: &config.Config{
			Gateway: config.GatewayConfig{
				Live: config.GatewayLiveConfig{MaxSessionDurationSeconds: 90},
			},
		}}).liveMaxSessionDuration(),
	)
}

func TestLiveSidebandNormalCloseEndsCall(t *testing.T) {
	normalClose := coderws.CloseError{Code: coderws.StatusNormalClosure}
	require.ErrorIs(t, liveSidebandReadError(normalClose), ErrLiveCallNotFound)

	abnormalClose := coderws.CloseError{Code: coderws.StatusInternalError}
	require.Equal(t, abnormalClose, liveSidebandReadError(abnormalClose))
}

func TestLiveCreateFailoverUsesExistingOpenAIPolicy(t *testing.T) {
	service := &OpenAIGatewayService{}
	account := newOpenAIUpstreamErrorTestAccount()
	require.False(t, service.shouldFailoverLiveCreateError(account, &UpstreamFailoverError{
		StatusCode:   http.StatusBadRequest,
		ResponseBody: []byte(`{"error":{"message":"invalid session"}}`),
	}))
	require.True(t, service.shouldFailoverLiveCreateError(account, &UpstreamFailoverError{
		StatusCode: http.StatusForbidden,
	}))
	require.True(t, service.shouldFailoverLiveCreateError(account, &UpstreamFailoverError{
		StatusCode: http.StatusBadGateway,
	}))
	require.True(t, service.shouldFailoverLiveCreateError(account, errors.New("transport failed")))
}

func TestLiveCallIDFromLocation(t *testing.T) {
	callID, err := liveCallIDFromLocation("https://chatgpt.com/backend-api/codex/call_123?intent=quicksilver")
	require.NoError(t, err)
	require.Equal(t, "call_123", callID)

	callID, err = liveCallIDFromLocation("/backend-api/codex/call_456")
	require.NoError(t, err)
	require.Equal(t, "call_456", callID)
}

func TestRequestTypeLive(t *testing.T) {
	require.True(t, RequestTypeLive.IsValid())
	require.Equal(t, "live", RequestTypeLive.String())
	parsed, err := ParseUsageRequestType("live")
	require.NoError(t, err)
	require.Equal(t, RequestTypeLive, parsed)
}

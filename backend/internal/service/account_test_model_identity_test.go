package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai_compat"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type modelIdentityAccountRepo struct {
	AccountRepository
	account *Account
}

func (r *modelIdentityAccountRepo) GetByID(context.Context, int64) (*Account, error) {
	return r.account, nil
}

func (r *modelIdentityAccountRepo) UpdateExtra(context.Context, int64, map[string]any) error {
	return nil
}

type modelIdentityHTTPUpstream struct {
	body         string
	requestModel string
	responses    []*http.Response
	requests     int
	beforeReturn func(*http.Request, int)
}

func (u *modelIdentityHTTPUpstream) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	var payload struct {
		Model string `json:"model"`
	}
	if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
		return nil, err
	}
	u.requestModel = payload.Model
	u.requests++
	if u.beforeReturn != nil {
		u.beforeReturn(req, u.requests)
	}
	if len(u.responses) > 0 {
		response := u.responses[0]
		u.responses = u.responses[1:]
		return response, nil
	}
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(u.body))}, nil
}

func (u *modelIdentityHTTPUpstream) DoWithTLS(req *http.Request, proxy string, id int64, concurrency int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return u.Do(req, proxy, id, concurrency)
}

func TestAccountTestModelIdentity_BackgroundCapturesWireModels(t *testing.T) {
	for _, tt := range []struct {
		name       string
		responses  bool
		body       string
		wantModels []string
	}{
		{
			name:      "responses_sse",
			responses: true,
			body: "data: {\"type\":\"response.created\",\"response\":{\"model\":\"returned-model\"}}\n\n" +
				"data: {\"type\":\"response.output_text.delta\",\"delta\":\"I am a different model\"}\n\n" +
				"data: {\"type\":\"response.completed\",\"response\":{\"model\":\"returned-model\",\"status\":\"completed\"}}",
			wantModels: []string{"returned-model"},
		},
		{
			name:       "responses_json",
			responses:  true,
			body:       `{"model":"returned-model","status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"OK"}]}]}`,
			wantModels: []string{"returned-model"},
		},
		{
			name: "chat_sse_conflicting_models",
			body: "data: {\"model\":\"returned-model\",\"choices\":[{\"delta\":{\"content\":\"OK\"},\"finish_reason\":null}]}\n\n" +
				"data: {\"model\":\"another-model\",\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n",
			wantModels: []string{"returned-model", "another-model"},
		},
		{
			name:       "chat_json",
			body:       `{"model":"returned-model","choices":[{"message":{"content":"OK"},"finish_reason":"stop"}]}`,
			wantModels: []string{"returned-model"},
		},
		{
			name:      "no_metadata_does_not_use_content_or_request",
			responses: true,
			body: "data: {\"type\":\"response.output_text.delta\",\"delta\":\"{\\\"model\\\":\\\"mapped-model\\\"}\"}\n\n" +
				"data: {\"type\":\"response.completed\"}\n\n",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			account := &Account{
				ID: 4, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Concurrency: 1,
				Credentials: map[string]any{"api_key": "test", "base_url": "https://upstream.example.com", "model_mapping": map[string]any{"requested-model": "mapped-model"}},
				Extra:       map[string]any{openai_compat.ExtraKeyResponsesSupported: tt.responses},
			}
			upstream := &modelIdentityHTTPUpstream{body: tt.body}
			svc := &AccountTestService{accountRepo: &modelIdentityAccountRepo{account: account}, httpUpstream: upstream, cfg: &config.Config{}}
			result, err := svc.RunTestBackgroundWithPromptAndReasoning(context.Background(), account.ID, "requested-model", "Reply OK.", "")
			require.NoError(t, err)
			require.Equal(t, "success", result.Status, result.ErrorMessage)
			require.Equal(t, "mapped-model", upstream.requestModel)
			require.Equal(t, "mapped-model", result.UpstreamModel)
			require.Equal(t, tt.wantModels, result.ReturnedModels)
			require.False(t, result.ModelEvidenceInvalid)
		})
	}
}

func modelIdentityContext(t *testing.T) (*gin.Context, *accountTestModelIdentity) {
	t.Helper()
	ctx, identity := newAccountTestModelIdentity(context.Background())
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/test", nil).WithContext(ctx)
	return c, identity
}

func TestAccountTestModelIdentity_OAuthCapturesNormalizedWireModel(t *testing.T) {
	account := &Account{
		ID: 5, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive, Concurrency: 1,
		Credentials: map[string]any{"access_token": "test-token"},
	}
	upstream := &modelIdentityHTTPUpstream{body: "data: {\"type\":\"response.completed\",\"response\":{\"model\":\"gpt-5.6-sol\"}}\n\n"}
	svc := &AccountTestService{accountRepo: &modelIdentityAccountRepo{account: account}, httpUpstream: upstream}
	result, err := svc.RunTestBackgroundWithPrompt(context.Background(), account.ID, "gpt-5.6", "Reply OK.")
	require.NoError(t, err)
	require.Equal(t, "success", result.Status, result.ErrorMessage)
	require.Equal(t, "gpt-5.6-sol", upstream.requestModel)
	require.Equal(t, upstream.requestModel, result.UpstreamModel)
	require.Equal(t, []string{"gpt-5.6-sol"}, result.ReturnedModels)
	require.False(t, result.ModelEvidenceInvalid)
}

func TestAccountTestModelIdentity_RecoveryClearsPriorAttemptEvidence(t *testing.T) {
	c, identity := modelIdentityContext(t)
	c.Request = c.Request.WithContext(markAgentIdentityTaskRecoveryTried(c.Request.Context()))
	captureAccountTestUpstreamModel(c, "previous-model")
	captureAccountTestReturnedModels(c, map[string]any{"model": "previous-model", "response": map[string]any{"model": 42}})
	account := &Account{
		ID: 5, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive, Concurrency: 1,
		Credentials: map[string]any{"access_token": "test-token"},
	}
	upstream := &modelIdentityHTTPUpstream{body: "data: {\"type\":\"response.completed\",\"response\":{\"model\":\"gpt-5.6-sol\"}}\n\n"}
	svc := &AccountTestService{accountRepo: &modelIdentityAccountRepo{account: account}, httpUpstream: upstream}
	require.NoError(t, svc.testOpenAIAccountConnection(c, account, "gpt-5.6", "Reply OK.", ""))
	model, returned, invalid := identity.snapshot()
	require.Equal(t, "gpt-5.6-sol", model)
	require.Equal(t, []string{"gpt-5.6-sol"}, returned)
	require.False(t, invalid)
	// A duplicated presentation event must not clear the active attempt's evidence.
	svc.sendEvent(c, TestEvent{Type: "test_start", Model: "gpt-5.6"})
	model, returned, invalid = identity.snapshot()
	require.Equal(t, "gpt-5.6-sol", model)
	require.Equal(t, []string{"gpt-5.6-sol"}, returned)
	require.False(t, invalid)
}

func TestAccountTestModelIdentity_CompatibilityRetryClearsPriorAttemptEvidence(t *testing.T) {
	c, identity := modelIdentityContext(t)
	account := &Account{
		ID: 5, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Concurrency: 1,
		Credentials: map[string]any{"api_key": "test", "base_url": "https://upstream.example.com"},
		Extra:       map[string]any{openai_compat.ExtraKeyResponsesSupported: true},
	}
	upstream := &modelIdentityHTTPUpstream{
		responses: []*http.Response{
			{StatusCode: http.StatusBadRequest, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"error":{"message":"Unsupported parameter: max_output_tokens"}}`))},
			{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("data: {\"type\":\"response.completed\",\"response\":{\"model\":\"actual-model\"}}\n\n"))},
		},
		beforeReturn: func(_ *http.Request, attempt int) {
			if attempt == 1 {
				captureAccountTestReturnedModels(c, map[string]any{"model": "previous-model", "response": map[string]any{"model": 42}})
			}
		},
	}
	svc := &AccountTestService{accountRepo: &modelIdentityAccountRepo{account: account}, httpUpstream: upstream, cfg: &config.Config{}}
	require.NoError(t, svc.testOpenAIAccountConnection(c, account, "actual-model", "Reply OK.", ""))
	require.Equal(t, 2, upstream.requests)
	model, returned, invalid := identity.snapshot()
	require.Equal(t, "actual-model", model)
	require.Equal(t, []string{"actual-model"}, returned)
	require.False(t, invalid)
}

func TestAccountTestModelIdentity_MalformedUTF8CannotBecomeEvidence(t *testing.T) {
	svc := &AccountTestService{}
	for _, body := range []string{
		"{\"model\":\"bad-\xff-model\",\"status\":\"completed\"}",
		`{"model":"bad-\ud800-model","status":"completed"}`,
		"data: {\"type\":\"response.completed\",\"response\":{\"model\":\"bad-\xff-model\"}}\n\n",
	} {
		c, identity := modelIdentityContext(t)
		require.NoError(t, svc.processOpenAIStream(c, strings.NewReader(body)))
		_, returned, invalid := identity.snapshot()
		require.Empty(t, returned)
		require.True(t, invalid)
	}
}

func TestAccountTestModelIdentity_ProtocolMetadata(t *testing.T) {
	svc := &AccountTestService{}
	for _, tt := range []struct {
		name  string
		body  string
		parse func(*gin.Context, io.Reader) error
	}{
		{"anthropic_sse", "data: {\"type\":\"message_start\",\"message\":{\"model\":\"actual-model\"}}\n\ndata: {\"type\":\"message_stop\"}\n\n", svc.processClaudeStream},
		{"anthropic_json", `{"model":"actual-model","stop_reason":"end_turn","content":[{"type":"text","text":"OK"}]}`, svc.processClaudeStream},
		{"adaptive_anthropic", "data: {\"type\":\"message_start\",\"message\":{\"model\":\"actual-model\"}}\n\ndata: {\"type\":\"message_stop\"}\n\n", svc.processCNProviderAdaptiveAnthropicStream},
		{"gemini", "data: {\"modelVersion\":\"actual-model\",\"candidates\":[{\"finishReason\":\"STOP\"}]}\n\n", svc.processGeminiStream},
		{"gemini_cli", "data: {\"response\":{\"modelVersion\":\"actual-model\",\"candidates\":[{\"finishReason\":\"STOP\"}]}}\n\n", svc.processGeminiStream},
	} {
		t.Run(tt.name, func(t *testing.T) {
			c, identity := modelIdentityContext(t)
			require.NoError(t, tt.parse(c, strings.NewReader(tt.body)))
			_, models, invalid := identity.snapshot()
			require.Equal(t, []string{"actual-model"}, models)
			require.False(t, invalid)
		})
	}
}

func TestAccountTestModelIdentity_RejectsUntrustedAndUnboundedEvidence(t *testing.T) {
	c, identity := modelIdentityContext(t)
	captureAccountTestReturnedModels(c, map[string]any{
		"model":   "actual-model",
		"content": map[string]any{"model": "content-model"},
		"choices": []any{map[string]any{"message": map[string]any{"model": "spoofed-model"}}},
	})
	captureAccountTestReturnedModels(c, map[string]any{"response": map[string]any{"model": "actual-model"}})
	_, models, invalid := identity.snapshot()
	require.Equal(t, []string{"actual-model"}, models)
	require.False(t, invalid)
	for i := 0; i < 16; i++ {
		captureAccountTestReturnedModels(c, map[string]any{"model": fmt.Sprintf("model-%d", i)})
	}
	_, models, invalid = identity.snapshot()
	require.Len(t, models, 16)
	require.True(t, invalid)
	for _, value := range []any{nil, 42, "", strings.Repeat("x", 257), "model\nname", "model-\xff"} {
		c, identity = modelIdentityContext(t)
		captureAccountTestReturnedModels(c, map[string]any{"model": value})
		_, models, invalid = identity.snapshot()
		require.Empty(t, models)
		require.True(t, invalid)
	}
}

func TestAccountTestModelIdentity_NoPrivateEventsInInteractiveOutput(t *testing.T) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/test", nil)
	svc := &AccountTestService{}
	svc.sendEvent(c, TestEvent{Type: "test_start", Model: "requested-model"})
	require.NoError(t, svc.processOpenAIStream(c, strings.NewReader("data: {\"type\":\"response.completed\",\"response\":{\"model\":\"returned-model\"}}\n\n")))
	require.Nil(t, accountTestIdentity(c))
	require.NotContains(t, recorder.Body.String(), "returned-model")
	require.NotContains(t, recorder.Body.String(), "upstream_model")
}

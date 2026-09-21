package handler

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type incompleteResponsesStreamUpstream struct {
	service.HTTPUpstream
	accountIDs []int64
}

func (u *incompleteResponsesStreamUpstream) Do(_ *http.Request, _ string, accountID int64, _ int) (*http.Response, error) {
	u.accountIDs = append(u.accountIDs, accountID)
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body: io.NopCloser(strings.NewReader(
			"event: response.output_text.delta\ndata: " +
				`{"type":"response.output_text.delta","response_id":"resp_partial","delta":"partial answer","sequence_number":1}` + "\n\n")),
	}, nil
}

// The service deliberately returns partial output plus an error on EOF. Check
// the real handler boundary supplies one failure terminal without replaying an
// already visible answer against the next available account.
func TestOpenAIResponses_PartialOutputEOFReturnsFailedWithoutReplay(t *testing.T) {
	gin.SetMode(gin.TestMode)
	groupID := int64(4205)
	accounts := []service.Account{}
	for i := 0; i < 2; i++ {
		accounts = append(accounts, service.Account{
			ID: int64(9920 + i), Name: "stream-account", Platform: service.PlatformOpenAI,
			Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true, Priority: i + 1,
			Credentials: map[string]any{"api_key": "test-key", "base_url": "https://api.example.test"},
			Extra:       map[string]any{"openai_passthrough": true},
		})
	}
	cfg := &config.Config{RunMode: config.RunModeSimple}
	cfg.Default.RateMultiplier = 1
	cfg.Gateway.MaxAccountSwitches = 1
	upstream := &incompleteResponsesStreamUpstream{}
	billing := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(billing.Stop)
	gateway := service.NewOpenAIGatewayService(
		&openAIWSFailoverHandlerAccountRepoStub{accounts: accounts}, nil, nil, nil, nil, nil, nil,
		cfg, nil, nil, service.NewBillingService(cfg, nil), nil, billing, upstream,
		&service.DeferredService{}, nil, nil, nil, nil, nil, nil, nil,
	)
	h := NewOpenAIGatewayHandler(gateway, service.NewConcurrencyService(nil), billing,
		service.NewAPIKeyService(nil, nil, nil, nil, nil, nil, cfg), nil, nil, nil, nil, cfg)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"gpt-5.6-terra","input":"hello","stream":true}`))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{
		ID: 1805, GroupID: &groupID, User: &service.User{ID: 1705, Status: service.StatusActive},
		Group: &service.Group{ID: groupID, Platform: service.PlatformOpenAI, Status: service.StatusActive},
	})
	c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 1705})

	h.Responses(c)

	require.Equal(t, []int64{9920}, upstream.accountIDs, "visible output must not be replayed")
	require.Equal(t, http.StatusOK, recorder.Code, "stream headers are already committed")
	body := recorder.Body.String()
	require.Contains(t, body, "partial answer")
	require.Equal(t, 1, strings.Count(body, "event: response.failed\n"))
	require.NotContains(t, body, "response.completed")
	terminal := body[strings.Index(body, "event: response.failed\n"):]
	response, failure := parseResponsesFailedSSE(t, terminal)
	require.NotEmpty(t, failure["message"])
	require.Equal(t, "failed", response["status"])
	payload := strings.TrimSpace(strings.SplitN(terminal, "data: ", 2)[1])
	require.True(t, gjson.Get(payload, "sequence_number").Exists())
	require.Positive(t, gjson.Get(payload, "response.created_at").Int())
}

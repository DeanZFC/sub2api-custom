package service

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestOpenAIGatewayServiceForwardRejectsAmbiguousModelFieldsBeforeNormalization(t *testing.T) {
	gin.SetMode(gin.TestMode)
	fields := []struct {
		name string
		json string
	}{
		{"astra_then_sol", `"model":"gpt-6-astra","model":"gpt-6.1-sol"`},
		{"sol_then_astra", `"model":"gpt-6.1-sol","model":"gpt-6-astra"`},
		{"identical_values", `"model":"gpt-6-astra","model":"gpt-6-astra"`},
		{"case_variant", `"model":"gpt-6-astra","Model":"gpt-6.1-sol"`},
		{"case_variant_first", `"MODEL":"gpt-6-astra","model":"gpt-6.1-sol"`},
		{"escaped_key", `"model":"gpt-6-astra","m\u006fdel":"gpt-6.1-sol"`},
		{"null_last", `"model":"gpt-6-astra","model":null`},
		{"null_first", `"model":null,"model":"gpt-6-astra"`},
	}
	shapes := []struct {
		name string
		json string
	}{
		{"input", `"input":"hello"`},
		{"messages", `"messages":[{"role":"user","content":"hello"}]`},
	}
	for _, shape := range shapes {
		for _, field := range fields {
			t.Run(shape.name+"/"+field.name, func(t *testing.T) {
				upstream := &httpUpstreamRecorder{}
				usageRepo := &openAIRecordUsageLogRepoStub{}
				svc := &OpenAIGatewayService{httpUpstream: upstream, usageLogRepo: usageRepo}
				rec := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(rec)
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
				body := []byte(`{` + field.json + `,"stream":true,` + shape.json + `}`)

				// A nil account makes any account lookup before validation fail too.
				result, err := svc.Forward(context.Background(), c, nil, body)
				require.EqualError(t, err, "duplicate or case-conflicting model fields are not allowed")
				require.Nil(t, result, "rejected requests must not return billable usage")
				require.Equal(t, http.StatusBadRequest, rec.Code)
				require.JSONEq(t, `{"error":{"type":"invalid_request_error","message":"duplicate or case-conflicting model fields are not allowed","param":"model"}}`, rec.Body.String())
				require.Empty(t, upstream.requests)
				require.Zero(t, usageRepo.calls)
			})
		}
	}
}

func TestOpenAIGatewayServiceForwardSingleModelStillForwards(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, fields := range []string{
		`"input":"hello"`,
		`"messages":[{"role":"user","content":"hello"}]`,
		`"input":"hello","metadata":{"model":"nested-a","Model":"nested-b"}`,
	} {
		t.Run(fields, func(t *testing.T) {
			upstream := &httpUpstreamRecorder{resp: &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body: io.NopCloser(strings.NewReader(
					`{"id":"resp_model_guard","model":"gpt-6-astra","usage":{"input_tokens":1,"output_tokens":2}}`,
				)),
			}}
			cfg := &config.Config{}
			svc := &OpenAIGatewayService{cfg: cfg, httpUpstream: upstream}
			account := &Account{
				ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Concurrency: 1,
				Credentials: map[string]any{"api_key": "sk-test", "base_url": "https://example.com"},
				Extra:       map[string]any{"openai_responses_supported": true},
			}
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
			SetOpenAIClientTransport(c, OpenAIClientTransportHTTP)

			result, err := svc.Forward(context.Background(), c, account, []byte(`{"model":"gpt-6-astra","stream":false,`+fields+`}`))
			require.NoError(t, err)
			require.NotNil(t, result)
			require.Len(t, upstream.requests, 1)
			require.Equal(t, "gpt-6-astra", result.Model)
			require.Equal(t, "gpt-6-astra", gjson.GetBytes(upstream.lastBody, "model").String())
			require.True(t, gjson.GetBytes(upstream.lastBody, "input").Exists())
		})
	}
}

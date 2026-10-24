//go:build unit

package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// Gateways sometimes keep a provider's cache counter name inside otherwise
// OpenAI-compatible usage. Presence without the actual count must never turn
// a reported cache hit into a zero-hit protection sample or uncached billing.
func TestOpenAICacheUsageAliasesAcrossWireReaders(t *testing.T) {
	for _, field := range []string{"cache_read_input_tokens", "cache_read_tokens", "cached_tokens", "prompt_cache_hit_tokens", "cachedContentTokenCount"} {
		for _, read := range []int{0, 800} {
			t.Run(fmt.Sprintf("%s/%d", field, read), func(t *testing.T) {
				payload := fmt.Sprintf(`{"usage":{"input_tokens":1000,"output_tokens":10,%q:%d}}`, field, read)
				jsonUsage, ok := extractOpenAIUsageFromJSONBytes([]byte(payload))
				require.True(t, ok)
				var streamUsage, wsUsage OpenAIUsage
				terminal := `{"type":"response.completed","response":` + payload + `}`
				(&OpenAIGatewayService{}).parseSSEUsage(terminal, &streamUsage)
				parseOpenAIWSResponseUsageFromCompletedEvent([]byte(terminal), &wsUsage)
				ccUsage := extractCCStreamUsage(payload)
				require.NotNil(t, ccUsage)
				for _, usage := range []OpenAIUsage{jsonUsage, streamUsage, wsUsage, *ccUsage} {
					require.True(t, usage.CacheUsageReported)
					require.Equal(t, read, usage.CacheReadInputTokens)
					require.Equal(t, 1000, usage.InputTokens)
					require.Equal(t, 1000-read, usage.InputTokens-usage.CacheReadInputTokens-usage.CacheCreationInputTokens)
				}
			})
		}
	}
}

func TestOpenAICacheUsageAliasesPreserveExplicitCanonicalZero(t *testing.T) {
	for _, detail := range []string{"input_tokens_details", "prompt_tokens_details"} {
		payload := fmt.Sprintf(`{"usage":{"input_tokens":1000,"output_tokens":10,%q:{"cached_tokens":0},"prompt_cache_hit_tokens":800,"cachedContentTokenCount":900}}`, detail)
		usage, ok := extractOpenAIUsageFromJSONBytes([]byte(payload))
		require.True(t, ok)
		require.True(t, usage.CacheUsageReported)
		require.Zero(t, usage.CacheReadInputTokens)
	}
}

func TestOpenAICompatibilityBridgesPreserveCacheAliases(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, field := range []string{"cache_read_input_tokens", "prompt_cache_hit_tokens", "cachedContentTokenCount"} {
		for _, messages := range []bool{false, true} {
			for _, stream := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/messages=%t/stream=%t", field, messages, stream), func(t *testing.T) {
					body := []byte(fmt.Sprintf(`{"model":"gpt-6.1-sol","max_tokens":1000,"messages":[{"role":"user","content":"hello"}],"stream":%t}`, stream))
					response := fmt.Sprintf(`data: {"type":"response.completed","response":{"id":"resp_cached","model":"gpt-6.1-sol","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}],"usage":{"input_tokens":1000,"output_tokens":10,%q:800}}}`, field) + "\n\n"
					upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(response))}}
					svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream}
					account := &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Concurrency: 1, Credentials: map[string]any{"api_key": "fixture-key", "base_url": "https://api.openai.com"}}
					recorder := httptest.NewRecorder()
					c, _ := gin.CreateTestContext(recorder)
					c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
					var result *OpenAIForwardResult
					var err error
					if messages {
						result, err = svc.ForwardAsAnthropic(context.Background(), c, account, body, "", "")
					} else {
						result, err = svc.ForwardAsChatCompletions(context.Background(), c, account, body, "", "")
					}
					require.NoError(t, err)
					require.NotNil(t, result)
					require.Equal(t, 800, result.Usage.CacheReadInputTokens)
					require.True(t, result.Usage.CacheUsageReported)
					require.Equal(t, 1000, result.Usage.InputTokens)
					if messages {
						require.Contains(t, recorder.Body.String(), `"cache_read_input_tokens":800`)
					} else {
						require.Contains(t, recorder.Body.String(), `"cached_tokens":800`)
					}
				})
			}
		}
	}
}

func TestOpenAICompatibilityUsageMissingCacheCounterRemainsUnknown(t *testing.T) {
	for _, details := range []string{`{}`, `{"image_tokens":10}`, `{"cache_write_tokens":10}`} {
		var usage apicompat.ResponsesUsage
		require.NoError(t, json.Unmarshal([]byte(`{"input_tokens":1000,"output_tokens":10,"input_tokens_details":`+details+`}`), &usage))
		require.False(t, copyOpenAIUsageFromResponsesUsage(&usage).CacheUsageReported)
	}
	for _, details := range []string{"input_tokens_details", "prompt_tokens_details"} {
		var usage apicompat.ResponsesUsage
		require.NoError(t, json.Unmarshal([]byte(fmt.Sprintf(`{"input_tokens":1000,"output_tokens":10,%q:{"cached_tokens":0},"prompt_cache_hit_tokens":800}`, details)), &usage))
		got := copyOpenAIUsageFromResponsesUsage(&usage)
		require.True(t, got.CacheUsageReported)
		require.Zero(t, got.CacheReadInputTokens)
	}
}

package service

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestOpenAIVMForwardPreservesContinuationAndUsage(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(strconv.FormatBool(stream), func(t *testing.T) {
			body := `{"model":"gpt-5.4","previous_response_id":"resp_previous","input":[{"type":"function_call_output","call_id":"call_original","output":"keep me"}],"max_output_tokens":128,"store":true,"stream":` + strconv.FormatBool(stream) + `,"tools":[{"type":"namespace","name":"browser","tools":[{"type":"function","name":"open"}]}],"client_metadata":{"custom":"unchanged"}}`
			vm, repo, _ := vmTestService(t, func(w http.ResponseWriter, r *http.Request) {
				require.Equal(t, "/v1/accounts/7/responses", r.URL.Path)
				require.Equal(t, "Bearer "+vmTestSecret, r.Header.Get("Authorization"))
				require.Equal(t, "account-at", r.Header.Get("X-VM2API-Access-Token"))
				require.Equal(t, "chatgpt-owner", r.Header.Get("X-VM2API-ChatGPT-Account-ID"))
				require.Equal(t, "lite-test", r.Header.Get("X-OpenAI-Internal-Codex-Responses-Lite"))
				require.Equal(t, "original-session", r.Header.Get("Session_id"))
				require.Empty(t, r.Header.Get("Cookie"))
				require.Empty(t, r.Header.Get("Proxy-Authorization"))
				got, err := io.ReadAll(r.Body)
				require.NoError(t, err)
				require.JSONEq(t, body, string(got))
				response := `{"id":"resp_vm","object":"response","status":"completed","model":"gpt-5.4","output":[],"usage":{"input_tokens":11,"output_tokens":7,"input_tokens_details":{"cached_tokens":3}}}`
				w.Header().Set("X-Request-Id", "request_vm")
				if stream {
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = fmt.Fprintf(w, "event: response.completed%cdata: %s%c%cdata: [DONE]%c%c", 10, `{"type":"response.completed","response":`+response+"}", 10, 10, 10, 10)
				} else {
					w.Header().Set("Content-Type", "application/json")
					_, _ = io.WriteString(w, response)
				}
			})
			repo.b = &OpenAIVMBinding{AccountID: 7, Status: "ready"}
			direct := &httpUpstreamRecorder{}
			svc := &OpenAIGatewayService{cfg: &config.Config{}, openaiVM: vm, accountRepo: vm.accounts, cache: &stubGatewayCache{}, httpUpstream: direct}
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(body))
			c.Request.Header.Set("Authorization", "Bearer client-secret")
			c.Request.Header.Set("X-VM2API-Access-Token", "spoofed")
			c.Request.Header.Set("Cookie", "private=cookie")
			c.Request.Header.Set("Proxy-Authorization", "secret")
			c.Request.Header.Set("X-OpenAI-Internal-Codex-Responses-Lite", "lite-test")
			c.Request.Header.Set("Session_id", "original-session")
			result, err := svc.Forward(context.Background(), c, repo.account, []byte(body))
			require.NoError(t, err)
			require.Equal(t, 200, recorder.Code)
			require.Equal(t, "resp_vm", result.ResponseID)
			require.EqualValues(t, 11, result.Usage.InputTokens)
			require.EqualValues(t, 7, result.Usage.OutputTokens)
			require.EqualValues(t, 3, result.Usage.CacheReadInputTokens)
			require.Empty(t, direct.requests)
		})
	}
}

func TestOpenAIVMForwardKeepsFastAndImagePolicies(t *testing.T) {
	vm, repo, _ := vmTestService(t, func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		require.False(t, gjson.GetBytes(body, "service_tier").Exists())
		require.Equal(t, "resp_previous", gjson.GetBytes(body, "previous_response_id").String())
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"resp_policy","usage":{"input_tokens":1,"output_tokens":2}}`)
	})
	repo.b = &OpenAIVMBinding{AccountID: 7, Status: "ready"}
	svc := newOpenAIGatewayServiceWithSettings(t, openAIFastFilterPriorityPolicy())
	svc.cfg, svc.openaiVM, svc.accountRepo = &config.Config{}, vm, vm.accounts
	ctx := withOpenAIFastPolicyContext(context.Background(), openAIFastFilterPriorityPolicy())
	body := `{"model":"gpt-5.4","stream":false,"service_tier":"priority","previous_response_id":"resp_previous","input":[]}`
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(body))
	result, err := svc.Forward(ctx, c, repo.account, []byte(body))
	require.NoError(t, err)
	require.Nil(t, result.ServiceTier)
	recorder := httptest.NewRecorder()
	c, _ = gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	c.Set("api_key", &APIKey{Group: &Group{AllowImageGeneration: false}})
	_, err = svc.Forward(ctx, c, repo.account, []byte(`{"model":"gpt-5.4","tools":[{"type":"image_generation"}],"input":[]}`))
	require.Error(t, err)
	require.Equal(t, http.StatusForbidden, recorder.Code)
}

func TestOpenAIVMPublicInferenceRoutesDoNotBypassVM(t *testing.T) {
	for _, endpoint := range []string{"responses/compact", "images/generations", "images/edits", "alpha/search"} {
		t.Run(endpoint, func(t *testing.T) {
			vm, repo, _ := vmTestService(t, func(w http.ResponseWriter, r *http.Request) {
				require.Equal(t, "/v1/accounts/7/"+endpoint, r.URL.Path)
				w.WriteHeader(http.StatusNotFound)
			})
			repo.b = &OpenAIVMBinding{AccountID: 7, Status: "ready"}
			req, err := http.NewRequest(http.MethodPost, "https://chatgpt.com/backend-api/codex/"+endpoint, strings.NewReader(`{}`))
			require.NoError(t, err)
			req.Header.Set("Authorization", "Bearer token")
			resp, handled, err := vm.RoundTrip(req, repo.account)
			require.NoError(t, err)
			require.True(t, handled)
			defer func() { _ = resp.Body.Close() }()
			require.Equal(t, http.StatusNotFound, resp.StatusCode)
		})
	}
}

func TestOpenAIVMForwardCancellationStillDrainsUsage(t *testing.T) {
	requestCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := make(chan struct{})
	release := make(chan struct{})
	vm, repo, _ := vmTestService(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprintf(w, "data: %s%c%c", `{"type":"response.created","response":{"id":"resp_cancel"}}`, 10, 10)
		flusher, ok := w.(http.Flusher)
		require.True(t, ok)
		flusher.Flush()
		close(started)
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		_, _ = fmt.Fprintf(w, "data: %s%c%cdata: [DONE]%c%c", `{"type":"response.completed","response":{"id":"resp_cancel","model":"gpt-5.4","usage":{"input_tokens":17,"output_tokens":5}}}`, 10, 10, 10, 10)
	})
	repo.b = &OpenAIVMBinding{AccountID: 7, Status: "ready"}
	svc := &OpenAIGatewayService{cfg: &config.Config{}, openaiVM: vm, accountRepo: vm.accounts, cache: &stubGatewayCache{}}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil).WithContext(requestCtx)
	type outcome struct {
		result *OpenAIForwardResult
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		result, err := svc.Forward(requestCtx, c, repo.account, []byte(`{"model":"gpt-5.4","stream":true,"input":"hello"}`))
		done <- outcome{result, err}
	}()
	<-started
	cancel()
	close(release)
	select {
	case outcome := <-done:
		require.NoError(t, outcome.err)
		require.EqualValues(t, 17, outcome.result.Usage.InputTokens)
		require.EqualValues(t, 5, outcome.result.Usage.OutputTokens)
	case <-time.After(3 * time.Second):
		t.Fatal("cancelled downstream did not drain terminal usage")
	}
}

func TestOpenAIVMStripsForeignAccountTurnState(t *testing.T) {
	var received string
	vm, repo, _ := vmTestService(t, func(w http.ResponseWriter, r *http.Request) {
		received = r.Header.Get("X-Codex-Turn-State")
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, "{\"id\":\"resp_review\",\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}")
	})
	repo.b = &OpenAIVMBinding{AccountID: 7, Status: "ready"}
	svc := &OpenAIGatewayService{cfg: &config.Config{}, openaiVM: vm, accountRepo: vm.accounts, cache: &stubGatewayCache{}}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	c.Request.Header.Set("Session-Id", "session-review")
	c.Request.Header.Set("X-Codex-Turn-State", "foreign-account-blob")
	svc.noteOpenAICodexTurnStateProvenance(c, &Account{ID: 99})
	_, err := svc.Forward(context.Background(), c, repo.account, []byte("{\"model\":\"gpt-5.4\",\"stream\":false,\"input\":[]}"))
	if err != nil {
		t.Fatal(err)
	}
	if received != "" {
		t.Fatal("VM forwarding included turn-state known to come from different account")
	}
}

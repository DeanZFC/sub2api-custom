package service

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestUpstreamErrorRetryWebSocketPreservesCurrentTurn(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, eventType := range []string{"error", "response.failed"} {
		for _, turns := range []int{1, 2} {
			t.Run(fmt.Sprintf("%s_turn_%d", eventType, turns), func(t *testing.T) {
				cfg := newOpenAIWSV2TestConfig()
				cfg.Security.URLAllowlist.Enabled = false
				cfg.Security.URLAllowlist.AllowInsecureHTTP = true
				payload := `{"type":"error","error":{"code":"custom_busy","message":"Upstream is CURRENTLY BUSY, try again"}}`
				if eventType == "response.failed" {
					payload = `{"type":"response.failed","response":{"error":{"code":"custom_busy","message":"Upstream is CURRENTLY BUSY, try again"}}}`
				}
				events := [][]byte{[]byte(payload)}
				if turns == 2 {
					events = append([][]byte{[]byte(`{"type":"response.completed","response":{"id":"resp_first","status":"completed","output":[],"usage":{"input_tokens":1,"output_tokens":1}}}`)}, events...)
				}
				pool := newOpenAIWSConnPool(cfg)
				pool.setClientDialerForTest(&openAIWSCaptureDialer{conn: &openAIWSCaptureConn{events: events}})
				account := &Account{ID: 89, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 1,
					Credentials: map[string]any{"api_key": "sk-test"}, Extra: map[string]any{"responses_websockets_v2_enabled": true}}
				svc := &OpenAIGatewayService{cfg: cfg, cache: &stubGatewayCache{}, httpUpstream: &httpUpstreamRecorder{},
					openaiWSResolver: NewOpenAIWSProtocolResolver(cfg), toolCorrector: NewCodexToolCorrector(), openaiWSPool: pool}
				defer svc.CloseOpenAIWSPool()
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				retryCtx := upstreamRetryTestContext(t, ctx, "currently busy", 2)
				errorsCh := make(chan error, 1)
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					conn, err := coderws.Accept(w, r, nil)
					if err != nil {
						errorsCh <- err
						return
					}
					defer conn.CloseNow()
					c, _ := gin.CreateTestContext(httptest.NewRecorder())
					c.Request = r.WithContext(retryCtx)
					_, first, err := conn.Read(ctx)
					if err != nil {
						errorsCh <- err
						return
					}
					errorsCh <- svc.ProxyResponsesWebSocketFromClient(retryCtx, c, conn, account, "sk-test", first, nil)
				}))
				defer server.Close()
				client, _, err := coderws.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), nil)
				require.NoError(t, err)
				defer client.CloseNow()
				require.NoError(t, client.Write(ctx, coderws.MessageText, []byte(`{"type":"response.create","model":"gpt-5.1","input":[{"role":"user","content":"first question"}]}`)))
				if turns == 2 {
					_, completed, err := client.Read(ctx)
					require.NoError(t, err)
					require.Contains(t, string(completed), "response.completed")
					require.NoError(t, client.Write(ctx, coderws.MessageText, []byte(`{"type":"response.create","model":"gpt-5.1","input":[{"role":"user","content":"second question"}]}`)))
				}
				select {
				case err := <-errorsCh:
					var failure *UpstreamFailoverError
					require.ErrorAs(t, err, &failure)
					require.True(t, failure.ConfiguredRetry)
					if turns == 2 {
						body, currentTurn := OpenAIWSCurrentTurnRetryPayload(err)
						require.True(t, currentTurn)
						require.Contains(t, string(body), "second question", "must not replay only the first request")
					}
					claimed, err := TryConfiguredUpstreamErrorRetry(retryCtx, failure)
					require.True(t, claimed)
					require.NoError(t, err)
				case <-ctx.Done():
					t.Fatal("websocket retry did not return")
				}
			})
		}
	}
}

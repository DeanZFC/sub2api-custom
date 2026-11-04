package service

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestExcelPrimaryShadowNeverDispatchesNativeRequests(t *testing.T) {
	parentID := int64(100)
	parent := &Account{ID: parentID, Platform: PlatformOpenAI, Type: AccountTypeOAuth,
		Credentials: map[string]any{"access_token": "excel-token", "client_id": "app_fnr0pYvVwwFDocDumLG3H2Bp", "chatgpt_account_id": "workspace"}}
	shadow := &Account{ID: 200, ParentAccountID: &parentID, Platform: PlatformOpenAI, Type: AccountTypeOAuth}
	repo := newStubCredRepo(parent)
	for _, path := range []string{"token", "responses", "images", "account_test", "models"} {
		t.Run(path, func(t *testing.T) {
			upstream := &httpUpstreamRecorder{}
			gateway := &OpenAIGatewayService{accountRepo: repo, httpUpstream: upstream}
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
			var err error
			switch path {
			case "token":
				var token string
				token, _, err = gateway.GetAccessToken(t.Context(), shadow)
				require.Empty(t, token)
			case "responses":
				_, err = gateway.Forward(t.Context(), c, shadow, []byte(`{"model":"gpt-5.4","input":"hello"}`))
			case "images":
				_, err = gateway.ForwardImages(t.Context(), c, shadow, nil, &OpenAIImagesRequest{Model: "gpt-image-1", Endpoint: "/v1/images/generations"}, "")
			case "account_test":
				tester := &AccountTestService{accountRepo: repo, httpUpstream: upstream}
				err = tester.testOpenAIAccountConnection(c, shadow, "gpt-5.4", "hello", AccountTestModeDefault)
			case "models":
				_, err = gateway.FetchOpenAIModelsList(t.Context(), shadow)
			}
			require.ErrorContains(t, err, "native Codex credentials are required")
			require.Nil(t, upstream.lastReq, "legacy Excel shadows must fail before outbound I/O")
		})
	}
}

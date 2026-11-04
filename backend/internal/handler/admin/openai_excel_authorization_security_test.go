package admin

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestExcelAuthorizationWorkerRejectsWrongTokenWithoutEchoingSecrets(t *testing.T) {
	t.Setenv("OPENAI_REAUTH_WORKER_TOKEN", strings.Repeat("configured-worker-token-", 3))
	h := NewOpenAIOAuthReauthHandler(nil)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/internal/openai-reauth/claim", strings.NewReader(`{"worker_id":"fixture"}`))
	c.Request.Header.Set("X-OpenAI-Reauth-Worker-Token", "wrong-private-token")
	h.Claim(c)
	require.Equal(t, http.StatusUnauthorized, w.Code)
	require.Equal(t, "no-store", w.Header().Get("Cache-Control"))
	require.NotContains(t, w.Body.String(), "wrong-private-token")
	require.NotContains(t, w.Body.String(), "configured-worker-token")
}

func TestExcelAuthorizationAdminErrorsAreNotCacheable(t *testing.T) {
	h := NewOpenAIOAuthReauthHandler(nil)
	for _, handler := range []func(*gin.Context){h.GetStatus, h.CreateTask, h.SaveConfig, h.CredentialEncryption, h.WorkerStatus} {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodGet, "/invalid", nil)
		c.Params = gin.Params{{Key: "id", Value: "invalid"}}
		handler(c)
		require.Equal(t, "no-store", w.Header().Get("Cache-Control"))
	}
}

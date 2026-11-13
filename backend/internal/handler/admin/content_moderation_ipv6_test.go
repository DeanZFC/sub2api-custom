package admin

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/adminaccess"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestContentModerationIPv6HandlerValidationAndPermissions(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		name, body string
		scoped     bool
		want       int
	}{
		{"scoped-admin", `{}`, true, 403},
		{"missing", `{}`, false, 400},
		{"invalid-engine", `{"engine":"other","key_hash":"` + strings.Repeat("a", 64) + `"}`, false, 400},
		{"invalid-hash", `{"engine":"openai","key_hash":"` + strings.Repeat("g", 64) + `"}`, false, 400},
		{"short-hash", `{"engine":"openai","key_hash":"abcd"}`, false, 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(http.MethodPost, "/allocate-ipv6", strings.NewReader(tc.body))
			c.Request.Header.Set("Content-Type", "application/json")
			if tc.scoped {
				c.Request = c.Request.WithContext(adminaccess.WithPrincipal(c.Request.Context(), &adminaccess.Principal{KeyID: "scoped"}))
			}
			// A nil service ensures rejected requests cannot provision any address.
			h := NewContentModerationHandler(nil)
			h.AllocateAPIKeyIPv6(c)
			require.Equal(t, tc.want, w.Code)
			if tc.scoped {
				w = httptest.NewRecorder()
				request := c.Request
				c, _ = gin.CreateTestContext(w)
				c.Request = request
				h.UpdateConfig(c)
				require.Equal(t, 403, w.Code)
			}
		})
	}
}

func TestContentModerationIPv6HandlerSavedKeyAndSafeResponse(t *testing.T) {
	gin.SetMode(gin.TestMode)
	key := "sk-moderation-private-test-value"
	sum := sha256.Sum256([]byte(key))
	hash := hex.EncodeToString(sum[:])
	settings := &moderationEngineSettings{value: fmt.Sprintf(`{"api_keys":[%q],"key_ipv6":{%q:{"proxy_id":42,"address":"2001:db8::42"}}}`, key, hash)}
	svc := service.NewContentModerationService(settings, nil, nil, nil, nil, nil, nil, nil)
	h := NewContentModerationHandler(svc)
	router := gin.New()
	router.POST("/allocate-ipv6", h.AllocateAPIKeyIPv6)
	router.PUT("/config", h.UpdateConfig)
	for _, tc := range []struct {
		method, path, body string
		want               int
	}{
		{http.MethodPost, "/allocate-ipv6", fmt.Sprintf(`{"engine":"openai","key_hash":%q}`, hash), 200},
		{http.MethodPost, "/allocate-ipv6", fmt.Sprintf(`{"engine":"typesafe","key_hash":%q}`, hash), 400},
		{http.MethodPut, "/config", `{"auto_assign_ipv6":true}`, 200},
	} {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
		req.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(w, req)
		require.Equal(t, tc.want, w.Code, w.Body.String())
		require.NotContains(t, w.Body.String(), key)
		if tc.method == http.MethodPost {
			require.Equal(t, "no-store", w.Header().Get("Cache-Control"))
		}
		if tc.want == 200 {
			require.Contains(t, w.Body.String(), `"managed_ipv6":"2001:db8::42"`)
		}
		if tc.method == http.MethodPut {
			require.Contains(t, w.Body.String(), `"auto_assign_ipv6":true`)
		}
	}
}

func TestContentModerationIPv6HandlerFullAccessCompatibility(t *testing.T) {
	gin.SetMode(gin.TestMode)
	key := "sk-full-access-moderation-test"
	sum := sha256.Sum256([]byte(key))
	hash := hex.EncodeToString(sum[:])
	settings := &moderationEngineSettings{value: fmt.Sprintf(`{"api_keys":[%q],"key_ipv6":{%q:{"proxy_id":42,"address":"2001:db8::42"}}}`, key, hash)}
	svc := service.NewContentModerationService(settings, nil, nil, nil, nil, nil, nil, nil)
	h := NewContentModerationHandler(svc)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		principal := &adminaccess.Principal{KeyID: "full-access", Policy: adminaccess.Policy{FullAccess: true}}
		c.Request = c.Request.WithContext(adminaccess.WithPrincipal(c.Request.Context(), principal))
		c.Next()
	})
	router.POST("/allocate-ipv6", h.AllocateAPIKeyIPv6)
	router.PUT("/config", h.UpdateConfig)
	for _, tc := range []struct{ method, path, body string }{
		{http.MethodPost, "/allocate-ipv6", fmt.Sprintf(`{"engine":"openai","key_hash":%q}`, hash)},
		{http.MethodPut, "/config", `{"auto_assign_ipv6":true}`},
	} {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
		request.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(recorder, request)
		require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
		require.NotContains(t, recorder.Body.String(), key)
		require.Contains(t, recorder.Body.String(), `"managed_ipv6":"2001:db8::42"`)
	}
}

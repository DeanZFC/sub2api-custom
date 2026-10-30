package routes

import (
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestGatewayRoutesRequestModelFieldsRejectBeforeRouting(t *testing.T) {
	const message = "duplicate or case-conflicting model fields are not allowed"
	paths := []string{
		"/v1/responses", "/v1/responses/compact", "/v1/responses/input_tokens",
		"/responses", "/responses/compact", "/responses/input_tokens",
		"/backend-api/codex/responses", "/backend-api/codex/responses/compact",
		"/backend-api/codex/responses/input_tokens",
		"/v1/chat/completions", "/chat/completions",
		"/v1/messages", "/v1/messages/count_tokens", "/messages/count_tokens",
		"/antigravity/v1/messages", "/antigravity/v1/messages/count_tokens",
	}
	bodies := []struct{ name, body string }{
		{"native input", `{"model":"gpt-6-astra","model":"gpt-6.1-sol","stream":true,"input":"hi"}`},
		{"legacy messages", `{"model":"gpt-6-astra","model":"gpt-6.1-sol","stream":true,"messages":[{"role":"user","content":"hi"}]}`},
		{"case conflicting", `{"model":"gpt-6-astra","MODEL":"gpt-6.1-sol","messages":false}`},
		{"identical", `{"model":"gpt-6-astra","model":"gpt-6-astra"}`},
	}
	groups := []struct {
		name  string
		group *service.Group
	}{
		{"allowlist disabled", allowlistGroup(service.PlatformOpenAI, false)},
		{"both allowed", allowlistGroup(service.PlatformOpenAI, true, "gpt-6-astra", "gpt-6.1-sol")},
		{"one denied", allowlistGroup(service.PlatformOpenAI, true, "gpt-6-astra")},
		{"composite", allowlistGroup(service.PlatformComposite, false)},
		{"anthropic", allowlistGroup(service.PlatformAnthropic, false)},
	}
	for _, group := range groups {
		t.Run(group.name, func(t *testing.T) {
			// Handlers deliberately have no dependencies or auth subject. Returning
			// this validation error proves rejection precedes their parsing/setup
			// and the composite resolver, irrespective of the allowlist setting.
			router := newGatewayRoutesTestRouterWithGroup(group.group)
			for _, path := range paths {
				for _, body := range bodies {
					t.Run(path+"/"+body.name, func(t *testing.T) {
						req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body.body))
						req.Header.Set("Content-Type", "application/json")
						w := httptest.NewRecorder()
						router.ServeHTTP(w, req)
						require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
						require.Contains(t, w.Body.String(), message)
						require.Contains(t, w.Body.String(), "invalid_request_error")
					})
				}
			}
		})
	}
}

func TestGatewayRoutesRequestModelFieldsMountedBeforeAllowlist(t *testing.T) {
	source, err := os.ReadFile("gateway.go")
	require.NoError(t, err)
	for _, chain := range []string{"gateway", "gemini", "antigravityV1", "antigravityV1Beta"} {
		pattern := regexp.QuoteMeta(chain+".Use(requestModelFields)") + `\s+` + regexp.QuoteMeta(chain+".Use(groupModelAllowlist)")
		require.Regexp(t, regexp.MustCompile(pattern), string(source), chain)
	}
	require.Contains(t, string(source), "gin.HandlerFunc(apiKeyAuth), requestModelFields, groupModelAllowlist, compositeTarget")
}

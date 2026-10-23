package service

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func passiveCacheTestAccount() *Account {
	return &Account{ID: 7, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"api_key": "private-provider-key"}}
}

func TestPassiveCacheMetadataMatchesPrefixBeforeAnyHit(t *testing.T) {
	a := passiveCacheTestAccount()
	now := time.Now()
	first := buildPassiveQualityCacheMetadata(a, []byte(`{"model":"gpt-5","instructions":"same","input":[{"role":"user","content":"private prompt"}]}`), "conversation", "gpt-5", 12, now)
	next := buildPassiveQualityCacheMetadata(a, []byte(`{"input":[{"content":"private prompt","role":"user"},{"role":"assistant","content":"response"},{"role":"user","content":"next"}],"model":"gpt-5","instructions":"same"}`), "conversation", "gpt-5", 12, now.Add(time.Second))
	require.Empty(t, first.UnsupportedReason)
	require.Empty(t, next.UnsupportedReason)
	require.Contains(t, next.PrefixHashes, first.PromptHash)
	require.Equal(t, first.SessionHash, next.SessionHash)
	require.Equal(t, int64(1024), first.MinimumTokens)
	raw, err := json.Marshal(first)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "private prompt")
	require.NotContains(t, string(raw), "private-provider-key")
	require.NotContains(t, string(raw), "conversation")
	changed := buildPassiveQualityCacheMetadata(a, []byte(`{"model":"gpt-5","instructions":"changed","input":[{"role":"user","content":"private prompt"}]}`), "conversation", "gpt-5", 12, now)
	require.NotContains(t, changed.PrefixHashes, first.PromptHash)
	otherTenant := buildPassiveQualityCacheMetadata(a, []byte(`{"model":"gpt-5","input":"hello"}`), "conversation", "gpt-5", 13, now)
	require.NotEqual(t, first.SessionHash, otherTenant.SessionHash)
	a.Credentials["api_key"] = "replacement-key"
	rekeyed := buildPassiveQualityCacheMetadata(a, []byte(`{"model":"gpt-5","input":"hello"}`), "conversation", "gpt-5", 12, now)
	require.NotEqual(t, first.Scope, rekeyed.Scope)
}

func TestPassiveCacheMetadataUnknownScopeAndPromptAreNotZeroHits(t *testing.T) {
	cases := []struct {
		name, body, session string
		mutate              func(*Account)
		reason              string
	}{
		{name: "proxy", body: `{"model":"gpt-5","input":"hello"}`, session: "s", mutate: func(a *Account) { a.Credentials["base_url"] = "https://proxy.example/v1" }, reason: "unknown_cache_scope"},
		{name: "no session", body: `{"model":"gpt-5","input":"hello"}`, reason: "unknown_session"},
		{name: "server history", body: `{"model":"gpt-5","input":"hello","previous_response_id":"resp_1"}`, session: "s", reason: "server_side_history"},
		{name: "image", body: `{"model":"gpt-5","input":[{"type":"input_image","image_url":"https://example.com/a.png"}]}`, session: "s", reason: "external_or_multimodal_content"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := passiveCacheTestAccount()
			if tc.mutate != nil {
				tc.mutate(a)
			}
			m := buildPassiveQualityCacheMetadata(a, []byte(tc.body), tc.session, "gpt-5", 1, time.Now())
			require.Equal(t, tc.reason, m.UnsupportedReason)
			require.Empty(t, m.PromptHash)
		})
	}
}

func TestPassiveCacheAnthropicMovedBreakpointRetainsContentPrefix(t *testing.T) {
	a := passiveCacheTestAccount()
	a.Platform = PlatformAnthropic
	partial := buildPassiveQualityCacheMetadata(a, []byte(`{"model":"claude-sonnet-4-5","system":[{"type":"text","text":"s","cache_control":{"type":"ephemeral"}}],"messages":[{"role":"user","content":"hello"}]}`), "s", "claude-sonnet-4-5", 1, time.Now())
	require.Equal(t, "partial_or_unknown_cache_boundary", partial.UnsupportedReason)
	first := buildPassiveQualityCacheMetadata(a, []byte(`{"model":"claude-sonnet-4-5","messages":[{"role":"user","content":[{"type":"text","text":"hello","cache_control":{"type":"ephemeral"}}]}]}`), "s", "claude-sonnet-4-5", 1, time.Now())
	next := buildPassiveQualityCacheMetadata(a, []byte(`{"model":"claude-sonnet-4-5","messages":[{"role":"user","content":[{"type":"text","text":"hello"}]},{"role":"assistant","content":"reply"},{"role":"user","content":[{"type":"text","text":"next","cache_control":{"type":"ephemeral"}}]}]}`), "s", "claude-sonnet-4-5", 1, time.Now())
	require.Empty(t, first.UnsupportedReason)
	require.Empty(t, next.UnsupportedReason)
	require.Contains(t, next.PrefixHashes, first.PromptHash)
	require.Equal(t, int64(4096), first.MinimumTokens)
}

func TestPassiveCacheCaptureFreezesAsyncTurnAndAccountSwitch(t *testing.T) {
	s := NewPassiveQualityService(nil, nil)
	defer s.cancel()
	s.ConfigurationChanged()
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/responses", nil)
	c.Request.Header.Set("Upgrade", "websocket")
	c.Set("api_key", &APIKey{ID: 12})
	s.Capture()(c)
	a := passiveCacheTestAccount()
	first := CapturePassiveQualityCacheRequest(c.Request.Context(), c, a, []byte(`{"model":"gpt-5","input":"first"}`), "s", "gpt-5")
	worker := PassiveQualityUsageContext(first, context.Background())
	PassiveQualityBeginTurn(c)
	second := CapturePassiveQualityCacheRequest(first, c, a, []byte(`{"model":"gpt-5","input":"second"}`), "s", "gpt-5")
	recordPassiveQualityCache(worker, a.ID, "first", true)
	f := <-s.queue
	require.NotNil(t, f.CacheMetadata)
	require.NotEqual(t, f.CacheMetadata.PromptHash, passiveCacheMetadataForTest(t, second).PromptHash)
	switched := *a
	switched.ID = 8
	ctx := CapturePassiveQualityCacheRequest(second, c, &switched, []byte(`{"model":"gpt-5","input":"second"}`), "s", "gpt-5")
	require.Equal(t, int64(8), passiveCacheMetadataForTest(t, ctx).AccountID)
	// Explicit unknown reporting still persists request-side evidence.
	recordPassiveQualityCache(ctx, 8, "unknown", false)
	f = <-s.queue
	require.NotNil(t, f.CacheMetadata)
	require.False(t, f.CacheReported)
}

func TestPassiveCacheWireCaptureUsesFinalBodyAndRetryIdentity(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/responses", nil)
	c.Set("api_key", &APIKey{ID: 12})
	s := NewPassiveQualityService(nil, nil)
	defer s.cancel()
	s.ConfigurationChanged()
	s.Capture()(c)
	a := passiveCacheTestAccount()
	CapturePassiveQualityCacheRequest(c.Request.Context(), c, a, []byte(`{"model":"gpt-5","input":"client"}`), "client-session", "public-model")
	send := func(body, key string) *PassiveQualityCacheMetadata {
		req, err := http.NewRequest("POST", "https://api.openai.com/v1/responses", bytes.NewBufferString(body))
		require.NoError(t, err)
		req.Header.Set("Authorization", "Bearer "+key)
		capturePassiveQualityCacheHTTPRequest(c, a, req)
		require.NotNil(t, req.Body)
		return passiveCacheMetadataForTest(t, c.Request.Context())
	}
	first := send(`{"model":"gpt-5","instructions":"injected","input":"wire-1"}`, "key1")
	worker := PassiveQualityUsageContext(c.Request.Context(), context.Background())
	retry := send(`{"model":"gpt-5","instructions":"injected","input":"wire-2"}`, "key1")
	require.True(t, first.WireVerified)
	require.Equal(t, "public-model", first.Model)
	require.Equal(t, first.SessionHash, retry.SessionHash)
	require.NotEqual(t, first.PromptHash, retry.PromptHash)
	require.Same(t, first, worker.Value(passiveQualityCacheMetadataKey{}))
	override := send(`{"model":"gpt-5","instructions":"injected","input":"wire-2"}`, "key2")
	require.NotEqual(t, retry.Scope, override.Scope)
	unreadable, err := http.NewRequest("POST", "https://api.openai.com/v1/responses", nil)
	require.NoError(t, err)
	capturePassiveQualityCacheHTTPRequest(c, a, unreadable)
	unknown := passiveCacheMetadataForTest(t, c.Request.Context())
	require.False(t, unknown.WireVerified, "an unreadable retry cannot reuse prior verified metadata")
	require.Empty(t, unknown.PromptHash)
	require.True(t, override.WireVerified, "already captured usage snapshots remain immutable")
	require.True(t, first.WireVerified)
}

func TestPassiveCacheCaptureToggleOnOpenConnection(t *testing.T) {
	s := NewPassiveQualityService(nil, nil)
	defer s.cancel()
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/responses", nil)
	c.Set("api_key", &APIKey{ID: 12})
	s.Capture()(c)
	a := passiveCacheTestAccount()
	body := []byte(`{"model":"gpt-5","input":"text"}`)
	CapturePassiveQualityCacheRequest(c.Request.Context(), c, a, body, "session", "gpt-5")
	require.Nil(t, passiveCacheMetadataForTest(t, c.Request.Context()))
	s.ConfigurationChanged()
	PassiveQualityBeginTurn(c)
	CapturePassiveQualityCacheRequest(c.Request.Context(), c, a, body, "session", "gpt-5")
	require.NotNil(t, passiveCacheMetadataForTest(t, c.Request.Context()))
}

func passiveCacheMetadataForTest(t *testing.T, ctx context.Context) *PassiveQualityCacheMetadata {
	t.Helper()
	metadata, ok := ctx.Value(passiveQualityCacheMetadataKey{}).(*PassiveQualityCacheMetadata)
	require.True(t, ok)
	return metadata
}

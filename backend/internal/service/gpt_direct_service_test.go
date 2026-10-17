package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

type gptDirectSettingsRepo struct {
	SettingRepository
	values map[string]string
}

func (r *gptDirectSettingsRepo) GetValue(_ context.Context, key string) (string, error) {
	if value, ok := r.values[key]; ok {
		return value, nil
	}
	return "", ErrSettingNotFound
}
func (r *gptDirectSettingsRepo) Set(_ context.Context, key, value string) error {
	r.values[key] = value
	return nil
}
func (r *gptDirectSettingsRepo) GetMultiple(_ context.Context, keys []string) (map[string]string, error) {
	result := map[string]string{}
	for _, key := range keys {
		result[key] = r.values[key]
	}
	return result, nil
}

func newGPTDirectTestService(t *testing.T, server *httptest.Server) (*GPTDirectService, *SettingService) {
	t.Helper()
	settings := NewSettingService(&gptDirectSettingsRepo{values: map[string]string{}}, nil)
	_, err := settings.SaveGPTDirectConfig(context.Background(), GPTDirectConfig{
		Enabled: true, BaseURL: server.URL, TenantSlug: "example-agent", PurchaseURL: "https://shop.example.com/cdk",
	})
	require.NoError(t, err)
	return NewGPTDirectService(settings), settings
}

func TestGPTDirectSettingsValidationAndPublicProjection(t *testing.T) {
	settings := NewSettingService(&gptDirectSettingsRepo{values: map[string]string{}}, nil)
	initial, err := settings.GetGPTDirectConfig(context.Background())
	require.NoError(t, err)
	require.False(t, initial.Enabled)
	require.Equal(t, "ctai", initial.TenantSlug)
	require.Equal(t, "https://ctai.chengtingkj.org", initial.BaseURL)
	require.Equal(t, "https://wzyp.cn/shop/ctai/mgitov", initial.PurchaseURL)
	valid := GPTDirectConfig{Enabled: true, BaseURL: "https://service.example.com/", TenantSlug: "CTAI", PurchaseURL: "https://shop.example.com/item?id=1"}
	updates := 0
	settings.SetOnUpdateCallback(func() { updates++ })
	saved, err := settings.SaveGPTDirectConfig(context.Background(), valid)
	require.NoError(t, err)
	require.Equal(t, 1, updates)
	require.Equal(t, "https://service.example.com", saved.BaseURL)
	require.Equal(t, "ctai", saved.TenantSlug)
	raw, err := json.Marshal(saved.Public())
	require.NoError(t, err)
	require.NotContains(t, string(raw), "service.example.com")
	require.NotContains(t, string(raw), "tenant_slug")
	require.NotEmpty(t, saved.Public().ReceiptScope)
	public, err := settings.GetPublicSettings(context.Background())
	require.NoError(t, err)
	require.True(t, public.GPTDirectEnabled)
	injection, err := settings.GetPublicSettingsForInjection(context.Background())
	require.NoError(t, err)
	injectedSettings, ok := injection.(*PublicSettingsInjectionPayload)
	require.True(t, ok)
	require.True(t, injectedSettings.GPTDirectEnabled)
	for _, address := range []string{"javascript:alert(1)", "https://user:pass@service.example.com", "https://service.example.com/api/v1", "https://service.example.com?token=x", "https://service.example.com#x"} {
		invalid := valid
		invalid.BaseURL = address
		_, err := settings.SaveGPTDirectConfig(context.Background(), invalid)
		require.Error(t, err, address)
	}
	for _, change := range []func(*GPTDirectConfig){
		func(v *GPTDirectConfig) { v.PurchaseURL = "" },
		func(v *GPTDirectConfig) { v.PurchaseURL = "javascript:alert(1)" },
		func(v *GPTDirectConfig) { v.TenantSlug = "../../admin" },
	} {
		invalid := valid
		change(&invalid)
		_, err := settings.SaveGPTDirectConfig(context.Background(), invalid)
		require.Error(t, err)
	}
	require.Equal(t, 1, updates, "failed validation must not save or invalidate")
	loaded, err := settings.GetGPTDirectConfig(context.Background())
	require.NoError(t, err)
	require.Equal(t, saved, loaded, "saved custom addresses must override initial defaults")
	_, err = settings.SaveGPTDirectConfig(context.Background(), GPTDirectConfig{})
	require.NoError(t, err)
	cleared, err := settings.GetGPTDirectConfig(context.Background())
	require.NoError(t, err)
	require.Empty(t, cleared.BaseURL, "an explicitly cleared address must stay empty")
	require.Empty(t, cleared.PurchaseURL)
}

func TestGPTDirectRedemptionScopesAndSanitizes(t *testing.T) {
	var keys []string
	var bodies []map[string]string
	var headers []http.Header
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/public/redeem", r.URL.Path)
		require.Equal(t, http.MethodPost, r.Method)
		var body map[string]string
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		bodies = append(bodies, body)
		headers = append(headers, r.Header.Clone())
		keys = append(keys, r.Header.Get("Idempotency-Key"))
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"task":{"id":"task_1","status":"queued","product":"gpt","plan":"plus","session":"upstream-secret","events":[{"message":"private"}]},"token":%q,"debug":"private"}`, strings.Repeat("a", 43))
	}))
	defer server.Close()
	client, _ := newGPTDirectTestService(t, server)
	for _, userID := range []int64{7, 7, 8} {
		result, failure := client.Call(context.Background(), "redeem", GPTDirectInput{Code: " code-123 ", Session: "test-session"}, userID, "203.0.113.9", "same-request-id")
		require.Nil(t, failure)
		encoded, err := json.Marshal(result)
		require.NoError(t, err)
		require.NotContains(t, string(encoded), "private")
		require.NotContains(t, string(encoded), "secret")
		require.NotContains(t, string(encoded), "events")
	}
	require.Len(t, keys, 3)
	require.Equal(t, keys[0], keys[1], "retries must retain the upstream key")
	require.NotEqual(t, keys[0], keys[2], "different users must not share a key")
	require.Regexp(t, gptDirectKeyPattern, keys[0])
	for i, body := range bodies {
		require.Equal(t, map[string]string{"product": "gpt", "tenantSlug": "example-agent", "code": "CODE-123", "session": "test-session"}, body)
		require.Empty(t, headers[i].Get("Authorization"))
		require.Empty(t, headers[i].Get("Cookie"))
		require.Empty(t, headers[i].Get("Origin"))
		require.Equal(t, "203.0.113.9", headers[i].Get("X-Forwarded-For"))
	}
}

func TestGPTDirectNoWriteRetryOrRedirectAndSafeErrors(t *testing.T) {
	var calls atomic.Int32
	var status atomic.Int32
	status.Store(503)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Location", "/must-not-follow")
		w.Header().Set("Retry-After", "123")
		w.WriteHeader(int(status.Load()))
		fmt.Fprint(w, `{"error":{"code":"CODE_USED","message":"secret-provider-diagnostic"}}`)
	}))
	defer server.Close()
	client, _ := newGPTDirectTestService(t, server)
	for _, tc := range []struct {
		upstream, want int
		reason         string
	}{
		{503, 502, "UPSTREAM_UNAVAILABLE"}, {302, 502, "UPSTREAM_UNAVAILABLE"}, {409, 409, "CODE_USED"}, {429, 429, "RATE_LIMIT"},
	} {
		status.Store(int32(tc.upstream))
		before := calls.Load()
		_, failure := client.Call(context.Background(), "redeem", GPTDirectInput{Code: "CODE-123", Session: "test-session"}, 7, "203.0.113.9", "same-request-id")
		require.NotNil(t, failure)
		require.Equal(t, tc.want, failure.Status)
		require.Equal(t, tc.reason, failure.Reason)
		require.Equal(t, int32(1), calls.Load()-before)
		require.NotContains(t, failure.Error(), "secret")
		if tc.upstream == 429 {
			require.Equal(t, 123, failure.RetryAfter)
		}
	}
}

func TestGPTDirectRejectsInvalidRequestsAndDisabledFeature(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(500) }))
	defer server.Close()
	client, settings := newGPTDirectTestService(t, server)
	for _, tc := range []struct {
		op    string
		input GPTDirectInput
		key   string
	}{
		{"redeem", GPTDirectInput{Code: "CODE-123", Session: "session"}, ""},
		{"redeem", GPTDirectInput{Code: "CODE-123"}, "request-123"},
		{"verify", GPTDirectInput{Code: "x"}, ""},
		{"task", GPTDirectInput{ID: "../admin", Token: strings.Repeat("a", 43)}, ""},
		{"task", GPTDirectInput{ID: "task_1"}, ""},
		{"../admin", GPTDirectInput{}, ""},
	} {
		_, failure := client.Call(context.Background(), tc.op, tc.input, 1, "", tc.key)
		require.NotNil(t, failure)
	}
	_, err := settings.SaveGPTDirectConfig(context.Background(), GPTDirectConfig{})
	require.NoError(t, err)
	_, failure := client.Call(context.Background(), "verify", GPTDirectInput{Code: "CODE-123"}, 1, "", "")
	require.Equal(t, "DISABLED", failure.Reason)
	require.Zero(t, calls.Load())
}

func TestGPTDirectTaskLookupRequiresMatchingProductAndIdentity(t *testing.T) {
	product, id := "gpt", "task_1"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodGet, r.Method)
		require.Equal(t, "/api/v1/public/tasks/task_1", r.URL.Path)
		require.Equal(t, strings.Repeat("a", 43), r.URL.Query().Get("token"))
		fmt.Fprintf(w, `{"id":%q,"status":"success","product":%q,"progress":200,"message":"private diagnostic"}`, id, product)
	}))
	defer server.Close()
	client, _ := newGPTDirectTestService(t, server)
	input := GPTDirectInput{ID: "task_1", Token: strings.Repeat("a", 43)}
	result, failure := client.Call(context.Background(), "task", input, 1, "", "")
	require.Nil(t, failure)
	task, ok := result.(GPTDirectTask)
	require.True(t, ok)
	require.Equal(t, float64(100), task.Progress)
	product = "claude"
	_, failure = client.Call(context.Background(), "task", input, 1, "", "")
	require.Equal(t, "INVALID_RESPONSE", failure.Reason)
	product, id = "gpt", "task_other"
	_, failure = client.Call(context.Background(), "task", input, 1, "", "")
	require.Equal(t, "INVALID_RESPONSE", failure.Reason)
}

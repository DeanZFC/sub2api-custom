package service

import (
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestClassifyUpstreamBillingError_HTTP402IsBillingSignal(t *testing.T) {
	got := ClassifyUpstreamBillingError(http.StatusPaymentRequired, nil)
	require.True(t, got.Matched)
	require.Equal(t, "payment_required", got.Code)
}

func TestShouldFailoverOpenAIPassthroughResponse_BillingBadRequest(t *testing.T) {
	body := []byte(`{"error":{"code":"insufficient_quota","message":"Provider account has no credits remaining"}}`)

	require.True(t, shouldFailoverOpenAIPassthroughResponse(
		&Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth},
		http.StatusBadRequest,
		body,
	))
}

func TestClassifyUpstreamBillingError_StructuredCodes(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "openai error code", body: `{"error":{"code":"insufficient_quota","message":"quota exhausted"}}`},
		{name: "responses error code", body: `{"response":{"error":{"code":"billing_hard_limit_reached"}}}`},
		{name: "detail code", body: `{"detail":{"code":"payment-required"}}`},
		{name: "vendor-prefixed code", body: `{"code":"provider_insufficient_balance"}`},
		{name: "credit code", body: `{"error":{"code":"out-of-credits"}}`},
		{name: "error type", body: `{"error":{"type":"insufficient_quota"}}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ClassifyUpstreamBillingError(http.StatusBadRequest, []byte(tt.body))
			require.True(t, got.Matched)
			require.NotEmpty(t, got.Code)
		})
	}
}

func TestClassifyUpstreamBillingError_StructuredMessages(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "error message", body: `{"error":{"message":"Your account has insufficient balance"}}`},
		{name: "responses message", body: `{"response":{"error":{"message":"Payment is required to continue"}}}`},
		{name: "detail message", body: `{"detail":{"message":"payment required for this account"}}`},
		{name: "top-level message", body: `{"message":"Your account has insufficient credits"}`},
		{name: "chinese balance", body: `{"error":{"message":"上游账号余额不足"}}`},
		{name: "credit balance low", body: `{"error":{"message":"Your credit balance is too low for this request"}}`},
		{name: "anthropic credit balance", body: `{"type":"error","error":{"type":"invalid_request_error","message":"Your credit balance is too low to access the Anthropic API. Please go to Plans & Billing to upgrade or purchase credits."}}`},
		{name: "billing limit reached", body: `{"error":{"message":"Billing limit reached for this account"}}`},
		{name: "openai billing quota message", body: `{"error":{"message":"You exceeded your current quota, please check your plan and billing details."}}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ClassifyUpstreamBillingError(http.StatusBadGateway, []byte(tt.body))
			require.True(t, got.Matched)
			require.NotEmpty(t, got.Message)
		})
	}
}

func TestClassifyUpstreamBillingError_BoundedPlainTextMessage(t *testing.T) {
	got := ClassifyUpstreamBillingError(http.StatusBadRequest, []byte("provider rejected request: insufficient balance"))
	require.True(t, got.Matched)
	require.Contains(t, got.Message, "insufficient balance")
}

func TestClassifyUpstreamBillingError_DoesNotScanUntrustedContent(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
	}{
		{name: "plain text unrelated body", status: http.StatusBadGateway, body: `upstream says the request failed`},
		{name: "echoed prompt field", status: http.StatusBadRequest, body: `{"error":{"type":"invalid_request_error","message":"invalid prompt"},"input":"insufficient balance"}`},
		{name: "generic quota validation", status: http.StatusBadRequest, body: `{"error":{"code":"invalid_request_error","message":"quota field must be an integer"}}`},
		{name: "code-like text without billing code", status: http.StatusBadRequest, body: `{"error":{"message":"parameter insufficient_quota is not supported"}}`},
		{name: "rate limit code", status: http.StatusTooManyRequests, body: `{"error":{"code":"rate_limit_exceeded","message":"try again later"}}`},
		{name: "string error envelope", status: http.StatusBadGateway, body: `{"error":"insufficient balance"}`},
		{name: "cyber policy message", status: http.StatusForbidden, body: `{"error":{"code":"cyber_policy","message":"prompt contains insufficient balance"}}`},
		{name: "validation code with quoted billing phrase", status: http.StatusBadRequest, body: `{"error":{"code":"invalid_request_error","message":"unsupported parameter: insufficient balance"}}`},
		{name: "safety type with unrelated code", status: http.StatusBadRequest, body: `{"error":{"code":"request_rejected","type":"content_policy_violation","message":"prompt contains insufficient balance"}}`},
		{name: "quoted billing message in safety rejection", status: http.StatusForbidden, body: `{"error":{"code":"content_policy_violation","message":"Blocked input: check your plan and billing details"}}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.False(t, IsUpstreamBillingError(tt.status, []byte(tt.body)))
		})
	}
}

func TestClassifyUpstreamBillingError_PreservesStructuredEvidence(t *testing.T) {
	got := ClassifyUpstreamBillingError(http.StatusBadRequest, []byte(`{"error":{"code":"insufficient_quota","message":"Your quota is exhausted"}}`))
	require.True(t, got.Matched)
	require.Equal(t, "insufficient_quota", got.Code)
	require.Equal(t, "Your quota is exhausted", got.Message)
}

func TestClassifyUpstreamBillingError_PreservesTemporaryLimitsAndSuccessfulContent(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
	}{
		{"gemini quota hint", 429, `{"error":{"code":429,"status":"RESOURCE_EXHAUSTED","message":"Resource has been exhausted (e.g. check quota)."}}`},
		{"minute quota", 429, `{"error":{"code":"quota_exceeded","message":"requests per minute exceeded"}}`},
		{"daily quota", 429, `{"error":{"code":"quota_exhausted","message":"daily quota exhausted; resets tomorrow"}}`},
		{"generic quota", 429, `{"error":{"message":"Quota limit reached for this account"}}`},
		{"successful text", 200, "insufficient balance"},
		{"successful message", 200, `{"message":"insufficient balance"}`},
		{"successful billing discussion", 200, `{"message":"You exceeded your current quota, please check your plan and billing details."}`},
		{"safety with quota status", 403, `{"error":{"code":"content_policy_violation","status":"RESOURCE_EXHAUSTED","message":"prompt contains insufficient balance"}}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.False(t, IsUpstreamBillingError(tt.status, []byte(tt.body)))
		})
	}
}

func TestClassifyUpstreamBillingError_ExplicitSemanticBilling(t *testing.T) {
	for _, body := range []string{
		`{"error":{"code":429,"status":"RESOURCE_EXHAUSTED","message":"Your credit balance is too low"}}`,
		`{"error":{"status":"PAYMENT_REQUIRED"}}`,
		`{"error":{"code":"insufficient_balance","type":"invalid_request_error"}}`,
		`{"response":{"error":{"code":"billing_not_active"}}}`,
	} {
		require.True(t, IsUpstreamBillingError(http.StatusOK, []byte(body)), body)
	}
}

func TestBuildOpenAIResponseFailedSSE_RedactsBillingEvidence(t *testing.T) {
	sse := buildOpenAIResponseFailedSSE(
		"resp_test",
		"gpt-5",
		[]byte(`{"type":"error","error":{"type":"invalid_request_error","code":"insufficient_balance","message":"Provider account has no credits remaining"}}`),
		"Provider account has no credits remaining",
	)

	require.Contains(t, sse, UpstreamBillingExhaustedClientMessage)
	require.Contains(t, sse, `"code":"upstream_account_unavailable"`)
	require.NotContains(t, strings.ToLower(sse), "no credits remaining")
	require.NotContains(t, sse, "insufficient_balance")
}

func TestSanitizeOpenAIBillingEvent_RemovesVendorDetails(t *testing.T) {
	for _, eventType := range []string{"error", "response.failed"} {
		t.Run(eventType, func(t *testing.T) {
			source := []byte(`{"type":"` + eventType + `","details":{"balance":0},"message":"provider balance","error":{"code":"insufficient_balance","message":"provider balance","details":{"balance":0},"param":"billing"},"response":{"id":"resp_billing","error":{"code":"insufficient_quota","message":"provider balance","status":"PAYMENT_REQUIRED"},"instructions":"private instructions","metadata":{"billing":"private"},"billing":"private","output":[],"usage":{"input_tokens":10}}}`)
			original := string(source)
			safe, changed := sanitizeOpenAIResponseFailedEventForClient(source, eventType, true)
			require.True(t, changed)
			require.Equal(t, original, string(source), "operator evidence must remain unmodified")
			require.Contains(t, string(safe), UpstreamBillingExhaustedClientMessage)
			for _, secret := range []string{"provider balance", "insufficient_balance", "insufficient_quota", "PAYMENT_REQUIRED", `"details"`, `"param"`} {
				require.NotContains(t, string(safe), secret)
			}
			if eventType == "response.failed" {
				require.NotContains(t, string(safe), "private")
			}
		})
	}
}

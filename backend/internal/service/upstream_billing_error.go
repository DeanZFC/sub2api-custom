package service

import (
	"net/http"
	"strings"

	"github.com/tidwall/gjson"
)

const (
	// UpstreamBillingExhaustedReason marks a provider account whose payment,
	// credit, or hard quota limit rejected the request. It is deliberately
	// distinct from the local user's billing errors.
	UpstreamBillingExhaustedReason = GatewayFailureReason("upstream_billing_exhausted")
	// UpstreamBillingExhaustedClientMessage is the only message exposed after
	// an upstream account billing failure. The provider's balance and account
	// state stay in Ops logs instead.
	UpstreamBillingExhaustedClientMessage = "Upstream service temporarily unavailable, please retry later"
)

// UpstreamBillingErrorClassification is the result of classifying a response
// returned by an upstream provider. Code and Message are copied only from the
// provider's structured error fields and are intended for operator logging;
// callers must not use them as a client-facing response without sanitizing.
type UpstreamBillingErrorClassification struct {
	Matched bool
	Code    string
	Message string
}

// ClassifyUpstreamBillingError identifies account-level billing, balance, and
// hard-quota failures in an upstream response.
//
// This function is deliberately scoped to upstream responses. Local user
// balance, subscription, quota, and risk-control errors never pass through it
// and therefore retain their existing client-facing behavior.
//
// For JSON responses, only explicit code/message fields are inspected. We do
// not scan arbitrary JSON: request prompts and metadata can contain words
// such as "balance" or "quota" and must not make an otherwise unrelated
// response look like an account billing failure. A bounded non-JSON body has
// no nested request fields, so it is checked only for the specific billing
// phrases below. HTTP 402 is an explicit billing signal even when the provider
// omits a response body.
func ClassifyUpstreamBillingError(statusCode int, body []byte) UpstreamBillingErrorClassification {
	if statusCode == http.StatusPaymentRequired {
		return UpstreamBillingErrorClassification{
			Matched: true,
			Code:    "payment_required",
		}
	}

	if len(body) == 0 || !gjson.ValidBytes(body) {
		if statusCode >= http.StatusBadRequest && len(body) > 0 && len(body) <= 4096 && isUpstreamBillingErrorMessage(string(body)) {
			return UpstreamBillingErrorClassification{Matched: true, Message: strings.TrimSpace(string(body))}
		}
		return UpstreamBillingErrorClassification{}
	}

	// Successful payloads require an explicit error envelope. Ordinary
	// output and echoed messages are not account-health evidence.
	if statusCode < http.StatusBadRequest &&
		!gjson.GetBytes(body, "error").IsObject() &&
		!gjson.GetBytes(body, "response.error").IsObject() {
		return UpstreamBillingErrorClassification{}
	}

	for _, path := range upstreamBillingErrorCodePaths {
		value := gjson.GetBytes(body, path)
		if value.Type != gjson.String {
			continue
		}
		code := strings.TrimSpace(value.String())
		if isUpstreamBillingErrorCode(code) {
			return UpstreamBillingErrorClassification{
				Matched: true,
				Code:    code,
				Message: firstStructuredUpstreamBillingMessage(body),
			}
		}
	}

	// A few providers put the stable classification in error.type instead of
	// error.code. It is treated as a code only when it is one of the explicit
	// billing values above; generic types such as invalid_request_error do not
	// match.
	for _, path := range upstreamBillingErrorTypePaths {
		value := gjson.GetBytes(body, path)
		if value.Type != gjson.String {
			continue
		}
		code := strings.TrimSpace(value.String())
		if isUpstreamBillingErrorCode(code) {
			return UpstreamBillingErrorClassification{
				Matched: true,
				Code:    code,
				Message: firstStructuredUpstreamBillingMessage(body),
			}
		}
	}

	// RESOURCE_EXHAUSTED and generic quota statuses can be temporary.
	// Only explicit billing statuses are sufficient without message evidence.
	for _, path := range upstreamBillingErrorStatusPaths {
		value := gjson.GetBytes(body, path)
		if value.Type == gjson.String && isUpstreamBillingErrorCode(value.String()) {
			return UpstreamBillingErrorClassification{
				Matched: true,
				Code:    value.String(),
				Message: firstStructuredUpstreamBillingMessage(body),
			}
		}
	}

	for _, path := range upstreamBillingErrorMessagePaths {
		// A provider may wrap a request-scoped rejection in a message that
		// repeats user supplied words. Known request/safety codes take
		// precedence over their message and must never be treated as account
		// billing state.
		if hasNonBillingUpstreamErrorCode(body) {
			break
		}
		value := gjson.GetBytes(body, path)
		if value.Type != gjson.String {
			continue
		}
		message := strings.TrimSpace(value.String())
		if isUpstreamBillingErrorMessage(message) {
			return UpstreamBillingErrorClassification{
				Matched: true,
				Code:    firstStructuredUpstreamBillingCode(body),
				Message: message,
			}
		}
	}

	return UpstreamBillingErrorClassification{}
}

func hasNonBillingUpstreamErrorCode(body []byte) bool {
	for _, path := range upstreamBillingErrorCodePaths {
		if isNonBillingUpstreamErrorCode(gjson.GetBytes(body, path).String()) {
			return true
		}
	}
	for _, path := range upstreamBillingErrorTypePaths {
		value := normalizeUpstreamBillingToken(gjson.GetBytes(body, path).String())
		// Anthropic also uses this generic type for exhausted credit. A specific
		// request/safety code above still wins, but the type alone cannot rule
		// out billing when the message explicitly reports insufficient funds.
		if value != "invalid_request_error" && isNonBillingUpstreamErrorCode(value) {
			return true
		}
	}
	return false
}

func isNonBillingUpstreamErrorCode(value string) bool {
	switch normalizeUpstreamBillingToken(value) {
	case "cyber_policy", "content_policy", "content_policy_violation", "safety_violation", "invalid_request", "invalid_request_error", "bad_request", "validation_error":
		return true
	default:
		return false
	}
}

// IsUpstreamBillingError is the boolean form for hot-path callers that only
// need to decide whether an account should fail over or be cooled down.
func IsUpstreamBillingError(statusCode int, body []byte) bool {
	return ClassifyUpstreamBillingError(statusCode, body).Matched
}

// upstreamBillingStatusCode returns a useful HTTP status for a billing
// classification. Semantic Gemini errors can arrive inside an HTTP 200
// stream, so callers must not pass that transport status to account-health or
// failover handling.
func upstreamBillingStatusCode(statusCode int, body []byte) int {
	if !IsUpstreamBillingError(statusCode, body) {
		return 0
	}
	if statusCode >= http.StatusBadRequest && statusCode <= 599 {
		return statusCode
	}
	for _, path := range upstreamBillingErrorCodePaths {
		value := gjson.GetBytes(body, path)
		if value.Type == gjson.Number {
			code := int(value.Int())
			if code >= 400 && code <= 599 {
				return code
			}
		}
	}
	for _, path := range upstreamBillingErrorStatusPaths {
		status := normalizeUpstreamBillingToken(gjson.GetBytes(body, path).String())
		switch status {
		case "resource_exhausted", "quota_exceeded", "quota_exhausted", "quota_depleted":
			return http.StatusTooManyRequests
		case "payment_required", "billing_required":
			return http.StatusPaymentRequired
		}
	}
	return http.StatusPaymentRequired
}

func newUpstreamBillingFailoverError(statusCode int, headers http.Header, body []byte, retryableOnSameAccount bool) *UpstreamFailoverError {
	return &UpstreamFailoverError{
		StatusCode:             statusCode,
		ResponseHeaders:        headers.Clone(),
		ResponseBody:           body,
		RetryableOnSameAccount: retryableOnSameAccount,
		Scope:                  GatewayFailureScopeAccount,
		Reason:                 UpstreamBillingExhaustedReason,
		NextAccountAction:      NextAccountRetry,
		ClientStatusCode:       http.StatusBadGateway,
		ClientMessage:          UpstreamBillingExhaustedClientMessage,
	}
}

// IsUpstreamBillingExhausted reports whether a failover error came from an
// upstream account billing/credit condition. Handlers use this to bypass
// administrator error-passthrough rules, which must never reveal provider
// account balances to end users.
func (e *UpstreamFailoverError) IsUpstreamBillingExhausted() bool {
	// Older forwarding adapters may still construct a plain failover error.
	// Keep the final client boundary safe using its upstream evidence as well.
	return e != nil && (e.Reason == UpstreamBillingExhaustedReason || IsUpstreamBillingError(e.StatusCode, e.ResponseBody))
}

var upstreamBillingErrorCodePaths = []string{
	"error.code",
	"response.error.code",
	"detail.code",
	"code",
}

var upstreamBillingErrorMessagePaths = []string{
	"error.message",
	"response.error.message",
	"detail.message",
	"message",
}

var upstreamBillingErrorTypePaths = []string{
	"error.type",
	"response.error.type",
	"detail.type",
	"type",
}

var upstreamBillingErrorStatusPaths = []string{
	"error.status",
	"response.error.status",
	"detail.status",
	"status",
}

func firstStructuredUpstreamBillingCode(body []byte) string {
	for _, path := range upstreamBillingErrorCodePaths {
		value := gjson.GetBytes(body, path)
		if value.Type == gjson.String {
			if code := strings.TrimSpace(value.String()); code != "" {
				return code
			}
		}
	}
	for _, path := range upstreamBillingErrorTypePaths {
		value := gjson.GetBytes(body, path)
		if value.Type == gjson.String {
			if code := strings.TrimSpace(value.String()); code != "" {
				return code
			}
		}
	}
	return ""
}

func firstStructuredUpstreamBillingMessage(body []byte) string {
	for _, path := range upstreamBillingErrorMessagePaths {
		value := gjson.GetBytes(body, path)
		if value.Type == gjson.String {
			if message := strings.TrimSpace(value.String()); message != "" {
				return message
			}
		}
	}
	return ""
}

func isUpstreamBillingErrorCode(value string) bool {
	normalized := normalizeUpstreamBillingToken(value)
	if normalized == "" {
		return false
	}

	// Keep this list focused on account payment/credit state. Generic
	// rate_limit/usage_limit codes are intentionally excluded because they are
	// request pressure and already have a separate policy.
	for _, code := range []string{
		"insufficient_quota",
		"insufficient_balance",
		"balance_insufficient",
		"insufficient_credits",
		"credit_exhausted",
		"credits_exhausted",
		"credit_depleted",
		"credits_depleted",
		"out_of_credits",
		"no_credits",
		"billing_hard_limit_reached",
		"billing_limit_reached",
		"spending_limit_reached",
		"payment_required",
		"billing_required",
		"billing_not_active",
		"payment_failed",
		"billing_failed",
	} {
		if normalized == code {
			return true
		}
	}

	// Providers commonly qualify the same condition with a vendor or account
	// prefix (for example openai_insufficient_quota). Accept only a known
	// billing suffix rather than arbitrary substring matches.
	for _, suffix := range []string{
		"_insufficient_quota",
		"_insufficient_balance",
		"_insufficient_credits",
		"_credit_exhausted",
		"_credits_exhausted",
		"_payment_required",
		"_billing_hard_limit_reached",
		"_spending_limit_reached",
	} {
		if strings.HasSuffix(normalized, suffix) {
			return true
		}
	}
	return false
}

func isUpstreamBillingErrorMessage(value string) bool {
	message := strings.ToLower(strings.TrimSpace(value))
	if message == "" {
		return false
	}

	// These phrases are intentionally specific. Do not match a bare "quota",
	// "balance", or "payment" because providers may include those words in a
	// validation message or echoed request content.
	for _, phrase := range []string{
		"insufficient_balance",
		"insufficient balance",
		"balance is insufficient",
		"balance was insufficient",
		"not enough balance",
		"no remaining balance",
		"balance is too low",
		"insufficient funds",
		"余额不足",
		"余额不够",
		"余额已用尽",
		"欠费",
		"payment required",
		"payment is required",
		"billing required",
		"billing hard limit",
		"billing limit reached",
		"check your plan and billing details",
		"spending limit reached",
		"spending limit exceeded",
		"credit balance is insufficient",
		"insufficient credits",
		"not enough credits",
		"requires more credits",
		"credit exhausted",
		"credits exhausted",
		"credit balance is too low",
		"credit balance too low",
		"out of credits",
		"no credits remaining",
		"payment failed",
		"billing failed",
	} {
		if strings.Contains(message, phrase) {
			return true
		}
	}

	return false
}

func normalizeUpstreamBillingToken(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, "-", "_")
	value = strings.ReplaceAll(value, " ", "_")
	return value
}

package service

import (
	"net/http"
	"strings"

	"github.com/tidwall/gjson"
)

// OpenAIUpstreamFailureClientMessage hides provider implementation details while
// leaving the original error available for routing, retries and Ops diagnostics.
const OpenAIUpstreamFailureClientMessage = "本次请求未能完成，请稍后重试。"

// OpenAIUpstreamErrorClientMessage is for the final client-facing copy only.
// Never use it to classify an upstream failure or replace its diagnostic body.
// Actionable request errors retain their explanation; unclassified/server errors
// are not assumed to be suitable for display merely because they have a message.
func OpenAIUpstreamErrorClientMessage(payload []byte, message string) string {
	message = sanitizeUpstreamErrorMessage(strings.TrimSpace(message))
	if message == "" {
		message = extractOpenAISSEErrorMessage(payload)
	}
	if message == UpstreamBillingExhaustedClientMessage || IsUpstreamBillingError(http.StatusBadGateway, payload) {
		return UpstreamBillingExhaustedClientMessage
	}
	if message == "" || openAIInternalFailureMessage(message) {
		return OpenAIUpstreamFailureClientMessage
	}
	if isOpenAIContextWindowError(message, payload) {
		return message
	}
	if hit, _, _ := detectOpenAICyberPolicy(payload); hit {
		return message
	}
	for _, path := range []string{"response.error.type", "error.type", "response.error.code", "error.code", "code"} {
		switch strings.ToLower(strings.TrimSpace(gjson.GetBytes(payload, path).String())) {
		case "invalid_request_error", "invalid_request", "invalid_argument", "validation_error",
			"context_length_exceeded", "model_not_found", "invalid_parameter", "missing_required_parameter",
			"content_policy_violation", "content_policy", "safety", "safety_error", "cyber_policy":
			return message
		}
	}
	// Some compatible providers omit both type and code for policy rejections.
	lower := strings.ToLower(message)
	if strings.Contains(lower, "content policy") || strings.Contains(lower, "blocked by policy") ||
		strings.Contains(lower, "safety policy") || strings.Contains(lower, "flagged by") {
		return message
	}
	return OpenAIUpstreamFailureClientMessage
}

func openAIInternalFailureMessage(message string) bool {
	lower := strings.ToLower(message)
	for _, marker := range []string{
		"stream disconnected before completion", "did not deliver validated output", "excel bps",
		"internal server error", "internal error", "stack trace", "traceback (most recent call last)",
		"context deadline exceeded", "unexpected eof", "connection reset", "stream error: stream id",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

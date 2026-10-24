package service

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/tidwall/gjson"
)

type upstreamErrorMessageContextKey struct{}

// WithUpstreamErrorMessage takes a per-request snapshot. Display policy must not
// affect the original errors used for retries, accounting or administrator logs.
func WithUpstreamErrorMessage(ctx context.Context, message string) context.Context {
	return context.WithValue(ctx, upstreamErrorMessageContextKey{}, strings.TrimSpace(message))
}

func ConfiguredUpstreamErrorMessage(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	message, _ := ctx.Value(upstreamErrorMessageContextKey{}).(string)
	return message
}

func UpstreamErrorClientMessage(ctx context.Context, fallback string) string {
	if message := ConfiguredUpstreamErrorMessage(ctx); message != "" {
		return message
	}
	return fallback
}

// IsUpstreamErrorPayload only inspects protocol error fields. Assistant text,
// tool results, and metadata may discuss errors and must pass through unchanged.
func IsUpstreamErrorPayload(payload []byte, eventType string) bool {
	if eventType == "error" || eventType == "response.failed" {
		return true
	}
	if !gjson.ValidBytes(payload) {
		return false
	}
	root := gjson.ParseBytes(payload)
	typ := root.Get("type").String()
	return typ == "error" || typ == "response.failed" ||
		(root.Get("error").Exists() && root.Get("error").Type != gjson.Null) ||
		root.Get("status").String() == "failed" ||
		(root.Get("response.status").String() == "failed" && root.Get("response.error").Exists())
}

// WrapUpstreamErrorPayload builds a client-only protocol envelope, discarding
// provider extensions that can repeat diagnostics (details, stack, metadata).
// It never modifies payload or performs classification/recording side effects.
func WrapUpstreamErrorPayload(ctx context.Context, payload []byte, eventType string) []byte {
	message := ConfiguredUpstreamErrorMessage(ctx)
	if message == "" || !IsUpstreamErrorPayload(payload, eventType) {
		return payload
	}
	root := gjson.ParseBytes(payload)
	if eventType == "" {
		eventType = root.Get("type").String()
	}
	safeError := map[string]any{"message": message}
	// Stable machine-readable codes retain client retry/protocol semantics.
	// Free-form provider fields (including param) are intentionally omitted.
	for _, field := range []string{"type", "code", "status"} {
		for _, prefix := range []string{"response.error.", "error.", ""} {
			if prefix == "" && field == "type" {
				continue
			}
			v := root.Get(prefix + field)
			if v.Type == gjson.Number || (v.Type == gjson.String && safeUpstreamErrorToken(v.String())) {
				safeError[field] = v.Value()
				break
			}
		}
	}
	if _, ok := safeError["type"]; !ok && root.Get("error.code").Type != gjson.Number {
		safeError["type"] = "upstream_error"
	}
	if code, _ := safeError["code"].(string); code == "server_is_overloaded" || code == "slow_down" {
		safeError["code"] = openAICapacityShedRetryableClientCode
	}
	safe := map[string]any{"error": safeError}
	if root.Get("status").String() == "failed" {
		// Task polling and non-streaming Responses use a failed object rather
		// than a standalone error envelope. Keep the identity/status contract.
		safe["status"] = "failed"
		for _, field := range []string{"id", "request_id", "object", "model", "created_at"} {
			if v := root.Get(field); v.Type == gjson.String || v.Type == gjson.Number {
				safe[field] = v.Value()
			}
		}
		if root.Get("object").String() == "response" {
			safe["output"] = []any{}
		}
		if root.Get("message").Exists() {
			safe["message"] = message
		}
	}
	if eventType == "error" || eventType == "response.failed" {
		safe["type"] = eventType
	}
	if eventType == "response.failed" || root.Get("response.status").String() == "failed" {
		response := map[string]any{
			"id":     "resp_" + strings.ReplaceAll(uuid.NewString(), "-", ""),
			"object": "response", "created_at": time.Now().Unix(),
			"status": "failed", "output": []any{}, "error": safeError,
		}
		for _, field := range []string{"id", "model", "created_at"} {
			if v := root.Get("response." + field); v.Exists() {
				response[field] = v.Value()
			}
		}
		if usage := root.Get("response.usage"); usage.IsObject() {
			response["usage"] = upstreamErrorUsageForClient(usage)
		}
		safe["response"] = response
		safe["type"] = "response.failed"
		safe["sequence_number"] = root.Get("sequence_number").Int()
	} else if eventType == "error" && (root.Get("message").Exists() || root.Get("sequence_number").Exists()) {
		safe["message"], safe["param"] = message, nil
		if code, ok := safeError["code"]; ok {
			safe["code"] = code
		}
	}
	for _, field := range []string{"sequence_number", "status", "status_code", "usage"} {
		v := root.Get(field)
		if field == "usage" && v.IsObject() {
			safe[field] = upstreamErrorUsageForClient(v)
		} else if v.Type == gjson.Number {
			safe[field] = v.Value()
		}
	}
	result, err := json.Marshal(safe)
	if err != nil {
		return payload
	}
	return result
}

func upstreamErrorUsageForClient(usage gjson.Result) map[string]any {
	result := make(map[string]any)
	usage.ForEach(func(key, value gjson.Result) bool {
		if !safeUpstreamErrorToken(key.String()) {
			return true
		}
		if value.Type == gjson.Number {
			result[key.String()] = value.Value()
		} else if value.IsObject() {
			result[key.String()] = upstreamErrorUsageForClient(value)
		}
		return true
	})
	return result
}

func safeUpstreamErrorToken(value string) bool {
	if len(value) == 0 || len(value) > 100 {
		return false
	}
	for _, r := range value {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-' || r == '.') {
			return false
		}
	}
	return true
}

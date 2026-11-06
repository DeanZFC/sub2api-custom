package service

import (
	"context"
	"github.com/gin-gonic/gin"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/tidwall/gjson"
)

// SetGroupAntiDistillationService installs the shared group policy at startup.
func (s *OpenAIGatewayService) SetGroupAntiDistillationService(protection *GroupAntiDistillationService) {
	s.groupAntiDistillation = protection
}

// ObserveGroupAntiDistillationUsage runs independently of asynchronous billing:
// a saturated usage worker pool must not silently skip protection evidence.
// Call once after a successful HTTP request or websocket turn, never on retry.
func (s *OpenAIGatewayService) ObserveGroupAntiDistillationUsage(ctx context.Context, key *APIKey, account *Account, result *OpenAIForwardResult, startedAt time.Time, requestBody []byte) {
	if s == nil || s.groupAntiDistillation == nil || key == nil || account == nil || account.Platform != PlatformOpenAI || result == nil {
		return
	}
	if !openAIGroupAntiDistillationUsageEligible(result) || !gjson.ValidBytes(requestBody) ||
		openAIGroupAntiDistillationRequestHasOpaqueHistory(requestBody) || openAIGroupAntiDistillationRequestHasMedia(requestBody) {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if startedAt.IsZero() {
		startedAt, _ = ctx.Value(groupAntiDistillationRequestStartKey{}).(time.Time)
	}
	// Internal callers without ingress timing use the measured request duration.
	if startedAt.IsZero() {
		startedAt = time.Now().Add(-result.Duration)
	}
	sampleCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer cancel()
	sampleCtx = WithGroupAntiDistillationRequestStart(sampleCtx, startedAt)
	// Never trust caller-controlled request IDs for sample deduplication.
	if result.antiDistillationRequestID == "" {
		result.antiDistillationRequestID = uuid.NewString()
	}
	s.groupAntiDistillation.Observe(sampleCtx, key, result.antiDistillationRequestID, result.Usage.CacheReadInputTokens)
}

// A reference can hide prior media even when the current turn only sends text.
// Do not fetch or persist conversation contents to classify protection samples.
// Session/cache identity fields alone do not imply an upstream history reference.
func openAIGroupAntiDistillationRequestHasOpaqueHistory(body []byte) bool {
	view := openAIRequestPayloadView(body)
	for _, field := range []string{"previous_response_id", "conversation", "conversation_id", "thread_id"} {
		reference := view.Get(field)
		if reference.Exists() && reference.Type != gjson.Null &&
			(reference.Type != gjson.String || strings.TrimSpace(reference.String()) != "") {
			return true
		}
	}
	return false
}

// Upstreams may count image/audio input only in the total input tokens. Inspect
// the request too: an omitted media breakdown is not proof of a text request.
// Keep this evidence local; no request content is stored in protection state.
func openAIGroupAntiDistillationRequestHasMedia(body []byte) bool {
	view := openAIRequestPayloadView(body)
	if audio := view.Get("audio"); audio.Exists() && audio.Type != gjson.Null {
		return true
	}
	for _, field := range []string{"modalities", "response_modalities"} {
		for _, modality := range view.Get(field).Array() {
			switch strings.ToLower(strings.TrimSpace(modality.String())) {
			case "audio", "image", "video":
				return true
			}
		}
	}
	for _, field := range []string{"input", "messages"} {
		content := view.Get(field)
		if openAIJSONValueMayContainImageInput(content) || openAIGroupAntiDistillationContentHasMedia(content) {
			return true
		}
	}
	return false
}

func openAIGroupAntiDistillationContentHasMedia(value gjson.Result) bool {
	if value.IsArray() {
		for _, part := range value.Array() {
			if openAIGroupAntiDistillationContentHasMedia(part) {
				return true
			}
		}
	} else if value.IsObject() {
		if value.Get("image_url").Exists() {
			return true
		}
		switch strings.ToLower(strings.TrimSpace(value.Get("type").String())) {
		case "image", "image_url", "input_image", "output_image", "input_audio", "audio", "output_audio", "input_video", "video", "video_url":
			return true
		}
		for _, field := range []string{"content", "output"} {
			if openAIGroupAntiDistillationContentHasMedia(value.Get(field)) {
				return true
			}
		}
	}
	return false
}

func openAIGroupAntiDistillationUsageEligible(result *OpenAIForwardResult) bool {
	return result != nil && result.Usage.CacheReadKnown && !result.Usage.cacheReadMediaObserved && !result.ClientDisconnect &&
		result.SucceededForScheduling() && result.Usage.InputTokens > 0 && result.Usage.CacheReadInputTokens >= 0 &&
		!result.ImageEndpoint && result.ImageCount == 0 && result.VideoCount == 0 && result.AudioUsage == nil &&
		result.Usage.ImageInputTokens == 0 && result.Usage.ImageOutputTokens == 0
}

func checkOpenAIGroupProtectionBeforeForward(ctx context.Context, c *gin.Context) error {
	if err := CheckGroupAntiDistillationAdmission(ctx); err != nil {
		if c != nil {
			MarkOpsClientBusinessLimited(c, OpsClientBusinessLimitedReasonLocalPolicyDenied)
			c.JSON(http.StatusTooManyRequests, gin.H{"error": gin.H{"type": "rate_limit_error", "code": "group_anti_distillation_paused", "message": err.Error()}})
		}
		return err
	}
	return nil
}

// A transport terminator alone does not prove successful generation. Keep
// truncated/filtered/unknown Chat Completions outcomes out of protection.
func openAIProtectionCCFinishState(payload []byte) (finished, incomplete bool) {
	view := gjson.ParseBytes(payload)
	if e := view.Get("error"); e.Exists() && e.Type != gjson.Null {
		incomplete = true
	}
	for _, choice := range view.Get("choices").Array() {
		reason := strings.TrimSpace(choice.Get("finish_reason").String())
		switch reason {
		case "":
		case "stop", "tool_calls", "function_call":
			finished = true
		default:
			incomplete = true
		}
	}
	return
}

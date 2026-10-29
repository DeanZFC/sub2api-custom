package service

import "time"

func (b *accountTestUsageBody) observationTime() time.Time {
	if !b.observedAt.IsZero() {
		return b.observedAt
	}
	return time.Now()
}

func (b *accountTestUsageBody) finishTiming(now time.Time) {
	if b.attempt.DurationMS != nil || b.attempt.StartedAt.IsZero() {
		return
	}
	elapsed := float64(now.Sub(b.attempt.StartedAt)) / float64(time.Millisecond)
	if elapsed >= 0 {
		b.attempt.DurationMS = &elapsed
	}
}

// Only a streamed text delta establishes TTFT. Headers, heartbeats, initial
// usage and a completed non-streaming body are not first-token observations.
// Text presence is checked in memory and never retained in usage telemetry.
func (b *accountTestUsageBody) captureTiming(data map[string]any, now time.Time) {
	if !b.sse {
		return
	}
	if response, ok := data["response"].(map[string]any); ok {
		// Gemini CLI wraps the same stream event in a response envelope.
		if _, hasCandidates := response["candidates"]; hasCandidates {
			b.captureTiming(response, now)
		}
	}
	kind, _ := data["type"].(string)
	if kind == "" {
		kind = b.eventKind
	}
	content, final := false, false
	textPresent := func(v any) bool { s, ok := v.(string); return ok && s != "" }
	switch kind {
	case "response.output_text.delta":
		content = textPresent(data["delta"])
	case "content_block_delta":
		if delta, ok := data["delta"].(map[string]any); ok {
			content = textPresent(delta["text"])
		}
	case "response.completed", "response.done", "response.failed", "response.incomplete", "message_stop":
		final = true
	}
	if kind == "response.failed" && b.attempt.FailureKind == "" {
		b.attempt.FailureKind = "server"
	}
	if kind == "response.incomplete" && b.attempt.FailureKind == "" {
		b.attempt.FailureKind = "output_limit"
	}
	if choices, ok := data["choices"].([]any); ok {
		for _, raw := range choices {
			choice, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			if delta, ok := choice["delta"].(map[string]any); ok {
				content = content || textPresent(delta["content"])
			}
			final = final || textPresent(choice["finish_reason"])
			if reason, ok := choice["finish_reason"].(string); ok && isAccountTestTruncationFinishReason(reason) && b.attempt.FailureKind == "" {
				b.attempt.FailureKind = "output_limit"
			}
		}
	}
	if candidates, ok := data["candidates"].([]any); ok {
		for _, raw := range candidates {
			candidate, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			if body, ok := candidate["content"].(map[string]any); ok {
				if parts, ok := body["parts"].([]any); ok {
					for _, rawPart := range parts {
						if part, ok := rawPart.(map[string]any); ok {
							content = content || textPresent(part["text"])
						}
					}
				}
			}
			final = final || textPresent(candidate["finishReason"])
			if reason, ok := candidate["finishReason"].(string); ok && isAccountTestTruncationFinishReason(reason) && b.attempt.FailureKind == "" {
				b.attempt.FailureKind = "output_limit"
			}
		}
	}
	if content && b.attempt.FirstTokenMS == nil && !b.attempt.StartedAt.IsZero() {
		elapsed := float64(now.Sub(b.attempt.StartedAt)) / float64(time.Millisecond)
		if elapsed >= 0 {
			b.attempt.FirstTokenMS = &elapsed
		}
	}
	if final {
		b.attempt.qualityTerminal = true
		b.finishTiming(now)
	}
}

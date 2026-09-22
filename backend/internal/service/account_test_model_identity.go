package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
)

type accountTestModelIdentityContextKey struct{}

// Metadata is collected separately from the UI SSE events so model-generated
// text cannot impersonate the upstream's reported model identity.
type accountTestModelIdentity struct {
	mu       sync.Mutex
	upstream string
	returned []string
	invalid  bool
	started  bool
}

func newAccountTestModelIdentity(ctx context.Context) (context.Context, *accountTestModelIdentity) {
	identity := &accountTestModelIdentity{}
	return context.WithValue(ctx, accountTestModelIdentityContextKey{}, identity), identity
}

func accountTestIdentity(c *gin.Context) *accountTestModelIdentity {
	if c == nil || c.Request == nil {
		return nil
	}
	identity, _ := c.Request.Context().Value(accountTestModelIdentityContextKey{}).(*accountTestModelIdentity)
	return identity
}

func captureAccountTestUpstreamModel(c *gin.Context, model string) {
	if identity := accountTestIdentity(c); identity != nil {
		identity.mu.Lock()
		defer identity.mu.Unlock()
		if identity.started {
			return
		}
		model = strings.TrimSpace(model)
		if !validAccountTestModelIdentity(model) {
			identity.invalid = true
			return
		}
		identity.upstream = model
	}
}

func beginAccountTestModelAttempt(c *gin.Context, model string) {
	if identity := accountTestIdentity(c); identity != nil {
		identity.mu.Lock()
		defer identity.mu.Unlock()
		identity.upstream = ""
		identity.returned = nil
		identity.invalid = false
		identity.started = true
		model = strings.TrimSpace(model)
		if !validAccountTestModelIdentity(model) {
			identity.invalid = true
			return
		}
		identity.upstream = model
	}
}

func validAccountTestModelIdentity(model string) bool {
	// encoding/json replaces malformed UTF-8 and surrogate escapes with RuneError.
	// Reject that rune as well so corrupted wire metadata cannot become evidence.
	return model != "" && len(model) <= 256 && utf8.ValidString(model) &&
		!strings.ContainsRune(model, utf8.RuneError) && strings.IndexFunc(model, unicode.IsControl) < 0
}

func captureAccountTestReturnedModels(c *gin.Context, data map[string]any) {
	identity := accountTestIdentity(c)
	if identity == nil {
		return
	}
	identity.mu.Lock()
	defer identity.mu.Unlock()
	appendModel := func(value any) {
		model, ok := value.(string)
		if !ok || !validAccountTestModelIdentity(strings.TrimSpace(model)) {
			identity.invalid = true
			return
		}
		model = strings.TrimSpace(model)
		for _, existing := range identity.returned {
			if existing == model {
				return
			}
		}
		if len(identity.returned) >= 16 {
			identity.invalid = true
			return
		}
		identity.returned = append(identity.returned, model)
	}
	// Only protocol metadata locations are eligible. Never traverse content,
	// choices, candidates, tool payloads, or any other model-generated values.
	containers := []map[string]any{data}
	for _, key := range []string{"response", "message"} {
		if nested, ok := data[key].(map[string]any); ok {
			containers = append(containers, nested)
		}
	}
	for _, container := range containers {
		for _, key := range []string{"model", "modelVersion"} {
			if value, exists := container[key]; exists {
				appendModel(value)
			}
		}
	}
}

func (identity *accountTestModelIdentity) snapshot() (string, []string, bool) {
	identity.mu.Lock()
	defer identity.mu.Unlock()
	return identity.upstream, append([]string(nil), identity.returned...), identity.invalid
}

func (s *AccountTestService) processOpenAIJSONTestResponse(c *gin.Context, body []byte, chat bool) error {
	var data map[string]any
	if err := json.Unmarshal(body, &data); err != nil || data == nil {
		return s.sendErrorAndEnd(c, "Invalid OpenAI response: expected SSE or JSON response")
	}
	captureAccountTestReturnedModels(c, data)
	if upstreamError, ok := data["error"].(map[string]any); ok {
		message, _ := upstreamError["message"].(string)
		if message == "" {
			message = "OpenAI API returned an error"
		}
		return s.sendErrorAndEnd(c, message)
	}
	if chat {
		choices, _ := data["choices"].([]any)
		if len(choices) == 0 {
			return s.sendErrorAndEnd(c, "Chat Completions response has no choices")
		}
		for _, raw := range choices {
			choice, _ := raw.(map[string]any)
			reason, _ := choice["finish_reason"].(string)
			if strings.TrimSpace(reason) == "" || isAccountTestTruncationFinishReason(reason) {
				return s.sendErrorAndEnd(c, fmt.Sprintf("Chat Completions response incomplete (finish_reason=%s)", reason))
			}
			if message, ok := choice["message"].(map[string]any); ok {
				if content, ok := message["content"].(string); ok && content != "" {
					s.sendEvent(c, TestEvent{Type: "content", Text: content})
				}
			}
		}
	} else {
		status, _ := data["status"].(string)
		if status != "completed" {
			return s.sendErrorAndEnd(c, fmt.Sprintf("OpenAI response incomplete (status=%s)", status))
		}
		output, _ := data["output"].([]any)
		for _, raw := range output {
			item, _ := raw.(map[string]any)
			content, _ := item["content"].([]any)
			for _, blockRaw := range content {
				block, _ := blockRaw.(map[string]any)
				if block["type"] == "output_text" {
					if text, ok := block["text"].(string); ok && text != "" {
						s.sendEvent(c, TestEvent{Type: "content", Text: text})
					}
				}
			}
		}
	}
	s.sendEvent(c, TestEvent{Type: "test_complete", Success: true})
	return nil
}

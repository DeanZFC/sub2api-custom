package service

import (
	"context"
	"encoding/json"
	"strings"
)

type scheduledTestOutputKindContextKey struct{}

const scheduledTestHTMLInstructions = `You generate the final HTML/SVG content for an automated content test.
Return the complete source directly in your response: a self-contained HTML document with inline SVG, styles and scripts, or a complete standalone SVG when requested. Include all required closing tags and the full implementation without placeholders or omitted sections.
This is a single-response content generation task. There is no workspace, filesystem, terminal, browser or tool executor available. Do not inspect directories, read or write files, run commands or tests, call tools, or emit tool-call markup.
Output only the finished HTML/SVG source. Do not include a plan, progress updates, explanations, Markdown fences, file paths, or claims that a file was created. Implement the user's requested content directly in the document.`

// Keep the output contract local to this execution, including retries and
// concurrent definitions. Ordinary connectivity and numeric probes retain
// their existing instructions.
func withScheduledTestOutputKind(ctx context.Context, kind string) context.Context {
	return context.WithValue(ctx, scheduledTestOutputKindContextKey{}, strings.ToLower(strings.TrimSpace(kind)))
}

func scheduledTestContentInstructions(ctx context.Context) string {
	if ctx != nil {
		if kind, _ := ctx.Value(scheduledTestOutputKindContextKey{}).(string); kind == "html" {
			return scheduledTestHTMLInstructions
		}
	}
	return ""
}

func applyScheduledTestContentInstructions(ctx context.Context, payload map[string]any, protocol string) {
	instructions := scheduledTestContentInstructions(ctx)
	if instructions == "" {
		return
	}
	switch protocol {
	case APIProtocolResponses:
		payload["instructions"] = instructions
	case APIProtocolAnthropic:
		payload["system"] = []map[string]any{{"type": "text", "text": instructions}}
	case APIProtocolChatCompletions:
		messages, ok := payload["messages"].([]map[string]any)
		if !ok {
			return
		}
		payload["messages"] = append([]map[string]any{{"role": "system", "content": instructions}}, messages...)
	case "gemini":
		payload["systemInstruction"] = map[string]any{"parts": []map[string]any{{"text": instructions}}}
	}
}

// Gemini and Antigravity builders serialize before transport wrapping. Replace
// the system instruction in the inner request without changing its user prompt
// or generation configuration.
func applyScheduledTestGeminiContentInstructions(ctx context.Context, body []byte) ([]byte, error) {
	if scheduledTestContentInstructions(ctx) == "" {
		return body, nil
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, err
	}
	inner := payload
	if request, ok := payload["request"].(map[string]any); ok {
		inner = request
	}
	applyScheduledTestContentInstructions(ctx, inner, "gemini")
	return json.Marshal(payload)
}

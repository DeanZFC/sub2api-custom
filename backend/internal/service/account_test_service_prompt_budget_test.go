//go:build unit

package service

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAccountTestOutputBudgetKeepsLegacyProbeSmall(t *testing.T) {
	require.Equal(t, 1024, accountTestMaxOutputTokens("", 1024))
	require.Equal(t, 256, accountTestMaxOutputTokens("   ", 256))
	require.Equal(t, scheduledTestCustomPromptMaxOutputTokens, accountTestMaxOutputTokens("draw a pelican", 1024))
}

func TestCreateClaudeTestPayloadUsesLargeBudgetOnlyForConfiguredPrompt(t *testing.T) {
	legacy, err := createTestPayload("claude-sonnet-4-6")
	require.NoError(t, err)
	require.Equal(t, 1024, legacy["max_tokens"])

	configured, err := createTestPayload("claude-sonnet-4-6", "create a complete HTML document")
	require.NoError(t, err)
	require.Equal(t, scheduledTestCustomPromptMaxOutputTokens, configured["max_tokens"])
}

func TestCreateOpenAIResponsesTestPayloadUsesLargeBudgetOnlyForConfiguredPrompt(t *testing.T) {
	legacy := createOpenAITestPayload("gpt-5.4", false)
	_, present := legacy["max_output_tokens"]
	require.False(t, present)

	configured := createOpenAITestPayload("gpt-5.4", false, "create a complete HTML document")
	require.Equal(t, scheduledTestCustomPromptMaxOutputTokens, configured["max_output_tokens"])
}

func TestAccountTestReasoningMapsUltraToResponsesMax(t *testing.T) {
	payload := createOpenAITestPayload("gpt-5.4", true, "reply")
	applyAccountTestReasoningEffort(payload, "ultra")
	reasoning, ok := payload["reasoning"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "max", reasoning["effort"])
}

func TestAccountTestParseSSEOutputKeepsOnlyModelContent(t *testing.T) {
	body := strings.Join([]string{
		`data: {"type":"status","text":"connecting"}`,
		`data: {"type":"content","text":"<html>"}`,
		`data: {"type":"test_complete","success":true}`,
		`data: {"type":"content","text":"</html>"}`,
		`data: {"type":"error","error":"ignored for this parser assertion"}`,
		"",
	}, "\n")
	text, errText := parseTestSSEOutput(body)
	require.Equal(t, "<html></html>", text)
	require.Equal(t, "ignored for this parser assertion", errText)
}

func TestProcessClaudeStreamRejectsTruncatedSSE(t *testing.T) {
	svc := &AccountTestService{}
	c, recorder := newTestContext()
	body := strings.NewReader("data: {\"type\":\"content_block_delta\",\"delta\":{\"text\":\"<html>partial\"}}\n\n" +
		"data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"max_tokens\"}}\n\n" +
		"data: {\"type\":\"message_stop\"}\n\n")

	err := svc.processClaudeStream(c, body)
	require.Error(t, err)
	require.Contains(t, err.Error(), "truncated")
	require.NotContains(t, recorder.Body.String(), `"success":true`)
}

func TestProcessClaudeStreamRejectsTruncatedNonSSEResponse(t *testing.T) {
	svc := &AccountTestService{}
	c, _ := newTestContext()
	err := svc.processClaudeStream(c, strings.NewReader(`{"type":"message","content":[{"type":"text","text":"partial"}],"stop_reason":"max_tokens"}`))
	require.Error(t, err)
	require.Contains(t, err.Error(), "truncated")
}

func TestProcessClaudeStreamAcceptsCompleteNonSSEResponse(t *testing.T) {
	svc := &AccountTestService{}
	c, recorder := newTestContext()
	err := svc.processClaudeStream(c, strings.NewReader(`{"type":"message","content":[{"type":"text","text":"complete"}],"stop_reason":"end_turn"}`))
	require.NoError(t, err)
	require.Contains(t, recorder.Body.String(), `"text":"complete"`)
	require.Contains(t, recorder.Body.String(), `"success":true`)
}

func TestProcessOpenAIChatCompletionsRejectsLengthFinishReason(t *testing.T) {
	svc := &AccountTestService{}
	c, recorder := newTestContext()
	body := strings.NewReader("data: {\"choices\":[{\"delta\":{\"content\":\"partial\"},\"finish_reason\":\"length\"}]}\n\ndata: [DONE]\n\n")
	err := svc.processOpenAIChatCompletionsStream(c, body)
	require.Error(t, err)
	require.Contains(t, err.Error(), "truncated")
	require.NotContains(t, recorder.Body.String(), `"success":true`)
}

func TestProcessOpenAIResponsesRejectsIncompleteTerminal(t *testing.T) {
	svc := &AccountTestService{}
	c, _ := newTestContext()
	body := strings.NewReader("data: {\"type\":\"response.completed\",\"response\":{\"status\":\"incomplete\"}}\n\n")
	err := svc.processOpenAIStream(c, body)
	require.Error(t, err)
	require.Contains(t, err.Error(), "incomplete")
}

func TestExtractAntigravityTestResponseRejectsTruncation(t *testing.T) {
	body := []byte(`data: {"response":{"candidates":[{"content":{"parts":[{"text":"partial"}]},"finishReason":"MAX_TOKENS"}]}}`)
	_, err := extractAntigravityTestResponse(body)
	require.Error(t, err)
	require.Contains(t, err.Error(), "truncated")
}

func TestExtractAntigravityTestResponseAcceptsNonSSECompletion(t *testing.T) {
	body := []byte(`{"response":{"candidates":[{"content":{"parts":[{"text":"complete"}]},"finishReason":"STOP"}]}}`)
	text, err := extractAntigravityTestResponse(body)
	require.NoError(t, err)
	require.Equal(t, "complete", text)
}

func TestBedrockPromptBudgetIsEncoded(t *testing.T) {
	// Keep this assertion independent from an upstream call: the payload shape
	// is shared with testBedrockAccountConnection and is intentionally checked
	// through the same JSON contract used by the signer.
	payload := map[string]any{
		"max_tokens": accountTestMaxOutputTokens("create a complete HTML document", 256),
	}
	encoded, err := json.Marshal(payload)
	require.NoError(t, err)
	require.Contains(t, string(encoded), `"max_tokens":8192`)
}

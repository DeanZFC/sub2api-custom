package service

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// The gateway owns BPS model routing, proxy leases and VM precedence. Reuse that
// path instead of probing the native Codex endpoint for a BPS-selected model.
func (s *AccountTestService) testExcelBPSAccountConnection(c *gin.Context, account *Account, modelID, prompt string) error {
	if s.openaiGatewayService == nil {
		return s.sendErrorAndEnd(c, "Excel BPS account test gateway is unavailable")
	}
	model := strings.TrimSpace(modelID)
	if model == "" {
		model = openai.DefaultTestModel
	}
	body, err := buildExcelBPSAccountTestBody(model, prompt)
	if err != nil {
		return s.sendErrorAndEnd(c, "Failed to create Excel BPS test payload")
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return s.sendErrorAndEnd(c, "Failed to create Excel BPS test payload")
	}
	ctx := c.Request.Context()
	applyAccountTestOutputTokenLimit(ctx, payload, "max_output_tokens")
	applyAccountTestReasoningEffort(payload, accountTestReasoningEffort(ctx))
	applyScheduledTestContentInstructions(ctx, payload, APIProtocolResponses)
	applyAccountTestVisionInput(ctx, payload, false)
	body, err = json.Marshal(payload)
	if err != nil {
		return s.sendErrorAndEnd(c, "Failed to create Excel BPS test payload")
	}

	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.Header().Set("X-Accel-Buffering", "no")
	c.Writer.Flush()
	upstreamModel := account.GetMappedModel(model)
	s.sendEvent(c, TestEvent{Type: "test_start", Model: upstreamModel})
	beginAccountTestModelAttempt(c, upstreamModel)

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "/v1/responses", bytes.NewReader(body))
	if err != nil {
		return s.sendErrorAndEnd(c, "Failed to create Excel BPS test request")
	}
	request.Header = c.Request.Header.Clone()
	if request.Header == nil {
		request.Header = make(http.Header)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "text/event-stream")
	// Admin browser headers are not a client identity for an internal probe.
	// Match the native account tester before the gateway's codex_cli_only gate.
	ApplyCodexCanonicalAuthIdentity(request.Header)
	applyOpenAICodexProbeHeaders(request.Header)
	probe := httptest.NewRecorder()
	probeCtx, _ := gin.CreateTestContext(probe)
	probeCtx.Request = request
	// A manual or scheduled one-shot test has no conversation by default. Give
	// only its cloned request an identity for session-scoped BPS proxy leases.
	if scope, _ := resolveOpenAIWSExecutionScope(probeCtx, body, 0); scope == "" {
		request.Header.Set("Session-Id", "account-test-"+uuid.NewString())
	}
	var forwarded *OpenAIForwardResult
	resp, err := executeQualityTestHTTP(request, account.ID, func(req *http.Request) (*http.Response, error) {
		probeCtx.Request = req
		result, forwardErr := s.openaiGatewayService.Forward(req.Context(), probeCtx, account, body)
		forwarded = result
		response := &http.Response{
			StatusCode: probe.Code,
			Header:     probe.Header().Clone(),
			Body:       io.NopCloser(bytes.NewReader(probe.Body.Bytes())),
		}
		if forwardErr != nil {
			return response, forwardErr
		}
		if result == nil || result.ClientDisconnect {
			return response, fmt.Errorf("excel BPS test response was interrupted")
		}
		return response, nil
	})
	if resp != nil && resp.Body != nil {
		defer func() { _ = resp.Body.Close() }()
	}
	if err != nil {
		return s.sendErrorAndEnd(c, err.Error())
	}
	if resp.StatusCode != http.StatusOK {
		return s.sendErrorAndEnd(c, fmt.Sprintf("Excel BPS account test returned HTTP %d", resp.StatusCode))
	}
	// Parsing the buffered wire still captures usage and returned-model evidence,
	// but its read time is not the upstream's first-token time.
	if usageBody, ok := resp.Body.(*accountTestUsageBody); ok && forwarded.FirstTokenMs != nil {
		firstTokenMS := float64(*forwarded.FirstTokenMs)
		usageBody.attempt.FirstTokenMS = &firstTokenMS
	}
	return s.processOpenAIStream(c, resp.Body)
}

func buildExcelBPSAccountTestBody(model, prompt string) ([]byte, error) {
	text := strings.TrimSpace(prompt)
	if text == "" {
		text = "hi"
	}
	return json.Marshal(map[string]any{
		"model": model, "stream": true, "store": false,
		"input": []any{map[string]any{"type": "message", "role": "user", "content": []any{
			map[string]any{"type": "input_text", "text": text},
		}}},
		"reasoning":         map[string]any{"effort": "medium"},
		"max_output_tokens": accountTestMaxOutputTokens(prompt, 1024),
	})
}

package service

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// Interactive and scheduled tests use the same protocol, account proxy, and
// credential handling as gateway requests. No Codex fallback is possible.
func (s *AccountTestService) testPrismAccountConnection(c *gin.Context, account *Account, model, prompt, mode string, opts AccountTestOptions) error {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "", "default", "text":
	default:
		return s.sendErrorAndEnd(c, "Prism currently supports text tests only")
	}
	if opts.ImageDataURL != "" || opts.AudioDataURL != "" {
		return s.sendErrorAndEnd(c, "Prism currently supports text tests only")
	}
	model = strings.TrimSpace(model)
	if model == "" {
		model = PrismDefaultModel
	}
	if strings.TrimSpace(prompt) == "" {
		prompt = "Hi"
	}
	payload := map[string]any{"model": model, "input": prompt}
	if effort := accountTestReasoningEffort(c.Request.Context()); effort != "" {
		payload["reasoning"] = map[string]string{"effort": effort}
	}
	raw, _ := json.Marshal(payload)
	request, err := parsePrismIncoming(raw, account, false, "")
	if err != nil {
		return s.sendErrorAndEnd(c, err.Error())
	}
	var tokenProvider *OpenAITokenProvider
	if s.openaiGatewayService != nil {
		tokenProvider = s.openaiGatewayService.openAITokenProvider
	}
	client, err := newPrismAccountClient(s.httpUpstream, s.cfg, account, tokenProvider)
	if err != nil {
		return s.sendErrorAndEnd(c, "Prism account configuration is invalid or incomplete")
	}
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("X-Accel-Buffering", "no")
	c.Status(http.StatusOK)
	s.sendEvent(c, TestEvent{Type: "test_start", Model: model})
	s.sendEvent(c, TestEvent{Type: "status", Text: "Prism is preparing an isolated conversation and generating a response..."})
	result, err := generatePrismWithHeartbeat(c.Request.Context(), client, request.request, func() error { _, e := fmt.Fprint(c.Writer, ": waiting for Prism\n\n"); c.Writer.Flush(); return e })
	if err != nil {
		if c.Request.Context().Err() != nil {
			return c.Request.Context().Err()
		}
		return s.sendErrorAndEnd(c, prismClientError(err)["message"].(string))
	}
	s.sendEvent(c, TestEvent{Type: "content", Text: result.Text})
	s.sendEvent(c, TestEvent{Type: "test_complete", Success: true})
	return nil
}

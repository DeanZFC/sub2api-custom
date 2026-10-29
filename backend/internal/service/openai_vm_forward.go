package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

// openAIVMPublicEndpoint is deliberately closed: only documented public
// inference routes go through vm2api. The Codex manifest and WHAM quota keep
// their native schemas through the account's authenticated CONNECT exit.
func openAIVMPublicEndpoint(method, path string) string {
	if method != http.MethodPost {
		return ""
	}
	for _, prefix := range []string{"/backend-api/codex/", "/v1/"} {
		if !strings.HasPrefix(path, prefix) {
			continue
		}
		endpoint := strings.TrimPrefix(path, prefix)
		switch endpoint {
		case "responses", "responses/compact", "images/generations", "images/edits", "alpha/search":
			return endpoint
		}
	}
	return ""
}

// The vm2api gateway owns Codex protocol adaptation and account identity. Only
// application authorization and explicit billing/model policy run here; native
// direct-upstream normalization must not erase previous_response_id, tool
// outputs, namespaces, or feature headers before that gateway sees them.
func (s *OpenAIGatewayService) forwardOpenAIVMResponses(ctx context.Context, c *gin.Context, account *Account, body []byte) (*OpenAIForwardResult, error) {
	started := time.Now()
	clearOpenAIResponsesClientToolMapping(c)
	clearOpenAIResponsesNamespaceNames(c)
	setCodexToolNameReverse(c, nil)
	stageCodexFingerprintIDs(c, nil)
	SetActualOpenAIUpstreamEndpoint(c, openAIResponsesUpstreamEndpoint)
	restriction := s.detectCodexClientRestriction(c, account, body)
	if restriction.Enabled && !restriction.Matched {
		MarkOpsClientBusinessLimited(c, OpsClientBusinessLimitedReasonLocalPolicyDenied)
		c.JSON(http.StatusForbidden, gin.H{"error": gin.H{"type": "forbidden_error", "message": CodexClientRestrictionMessage(restriction)}})
		return nil, errors.New("codex_cli_only restriction: only codex official clients are allowed")
	}
	requestedModel := strings.TrimSpace(gjson.GetBytes(body, "model").String())
	upstreamModel := strings.TrimSpace(account.GetMappedModel(requestedModel))
	if upstreamModel == "" {
		upstreamModel = requestedModel
	}
	if upstreamModel != requestedModel {
		body = s.ReplaceModelInBody(body, upstreamModel)
	}
	var err error
	body, err = s.applyOpenAIFastPolicyToBody(ctx, account, upstreamModel, body)
	if err != nil {
		var blocked *OpenAIFastBlockedError
		if errors.As(err, &blocked) {
			writeOpenAIFastPolicyBlockedResponse(c, blocked)
		}
		return nil, err
	}
	imageIntent := IsImageGenerationIntent(openAIResponsesEndpoint, upstreamModel, body)
	var imageConfig OpenAIResponsesImageBillingConfig
	if imageIntent {
		if !GroupAllowsImageGeneration(apiKeyGroup(getAPIKeyFromContext(c))) {
			MarkOpsClientBusinessLimited(c, OpsClientBusinessLimitedReasonLocalFeatureGate)
			c.JSON(http.StatusForbidden, gin.H{"error": gin.H{"type": "permission_error", "message": ImageGenerationPermissionMessage()}})
			return nil, errors.New("image generation disabled for group")
		}
		imageConfig, err = resolveOpenAIResponsesImageBillingConfigDetailedFromBody(body, upstreamModel)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"type": "invalid_request_error", "message": err.Error(), "param": "size"}})
			return nil, err
		}
	}
	token, _, err := s.GetAccessToken(ctx, account)
	if err != nil {
		return nil, err
	}
	target := appendOpenAIResponsesRequestPathSuffix(chatgptCodexURL, openAIResponsesRequestPathSuffix(c))
	upstreamCtx, releaseUpstreamCtx := detachUpstreamContext(ctx)
	defer releaseUpstreamCtx()
	req, err := http.NewRequestWithContext(upstreamCtx, http.MethodPost, target, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header = openAIVMRequestHeaders(c.Request.Header)
	s.guardOpenAICodexTurnStateEcho(c, account, req.Header)
	req.Header.Set("Authorization", "Bearer "+token)
	if err = resolveAndSetOpenAIChatGPTAccountHeaders(ctx, s.accountRepo, req.Header, account); err != nil {
		return nil, fmt.Errorf("resolve VM account identity: %w", err)
	}
	if req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", "application/json")
	}
	stream := gjson.GetBytes(body, "stream").Bool()
	if stream {
		req.Header.Set("Accept", "text/event-stream")
	} else {
		req.Header.Set("Accept", "application/json")
	}
	SetOpsUpstreamModel(c, upstreamModel)
	c.Set("openai_passthrough", true)
	c.Set("openai_ws_transport_reason", "account_vm_bridge")
	// vm2api rewrites the final kernel request; this intermediate body cannot
	// establish any additional OpenAI cache-prefix or routing identity.
	upstreamStarted := time.Now()
	resp, handled, err := s.openaiVM.RoundTrip(req, account)
	SetOpsLatencyMs(c, OpsUpstreamLatencyMsKey, time.Since(upstreamStarted).Milliseconds())
	if err != nil {
		return nil, s.handleOpenAIUpstreamTransportError(ctx, c, account, err, true)
	}
	if !handled || resp == nil {
		return nil, errors.New("OpenAI VM binding changed during forwarding; direct fallback is disabled")
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= http.StatusBadRequest {
		errorBody := s.readUpstreamErrorBody(resp)
		if shouldFailoverOpenAIPassthroughResponse(account, resp.StatusCode, errorBody) {
			return nil, s.handleFailoverErrorResponsePassthrough(ctx, resp, c, account, body, errorBody)
		}
		return nil, s.handleErrorResponsePassthrough(ctx, resp, c, account, body, errorBody)
	}
	if extractOpenAICodexTurnState(resp.Header) != "" {
		s.noteOpenAICodexTurnStateProvenance(c, account)
	}
	result := &OpenAIForwardResult{RequestID: resp.Header.Get("x-request-id"), UpstreamHeaders: resp.Header, Model: requestedModel, BillingModel: upstreamModel, UpstreamModel: upstreamModel, Stream: stream}
	if stream {
		parsed, parseErr := s.handleStreamingResponsePassthrough(ctx, resp, c, account, started, requestedModel, upstreamModel)
		if parseErr != nil {
			return nil, parseErr
		}
		if parsed.usage != nil {
			result.Usage = *parsed.usage
		}
		result.FirstTokenMs, result.ResponseID = parsed.firstTokenMs, parsed.responseID
		result.ImageCount, result.ImageOutputSizes = parsed.imageCount, parsed.imageOutputSizes
	} else {
		parsed, parseErr := s.handleNonStreamingResponsePassthrough(ctx, resp, c, account, requestedModel, upstreamModel)
		if parseErr != nil {
			return nil, parseErr
		}
		if parsed.usage != nil {
			result.Usage = *parsed.usage
		}
		result.ResponseID, result.ImageCount, result.ImageOutputSizes = parsed.responseID, parsed.imageCount, parsed.imageOutputSizes
	}
	result.Duration = time.Since(started)
	result.ServiceTier = resolvedOpenAIUpstreamServiceTier(c, extractOpenAIServiceTierFromBody(body))
	result.ReasoningEffort = extractOpenAIReasoningEffortFromBody(body, upstreamModel, requestedModel)
	result.UpstreamResponseModel = observedUpstreamResponseModel(c)
	result.UpstreamResponseModelConflict = observedUpstreamResponseModelConflict(c)
	result.UpstreamResponseServiceTier = observedUpstreamResponseServiceTier(c)
	if result.ImageCount > 0 {
		result.BillingModel, result.ImageSize, result.ImageInputSize = imageConfig.Model, imageConfig.SizeTier, imageConfig.InputSize
	}
	s.bindHTTPResponseAccount(ctx, c, account, result.ResponseID)
	if !account.IsShadow() {
		if snapshot := ParseCodexRateLimitHeaders(resp.Header); snapshot != nil {
			s.updateCodexUsageSnapshot(ctx, account.ID, snapshot)
		}
	}
	stampOpenAIResponsesUpstreamEndpoint(c, result)
	return result, nil
}

// Authentication, proxy, arbitrary routing and hop-by-hop headers are never
// accepted from the caller. vm2api receives its documented client features.
func openAIVMRequestHeaders(in http.Header) http.Header {
	out := make(http.Header)
	if values := in.Values("Version"); len(values) > 0 {
		out["Version"] = append([]string(nil), values...)
	}
	for _, name := range []string{"Accept", "Content-Type", "User-Agent", "Originator", "OpenAI-Beta", "Session-Id", "Session_id", "Conversation_id", "X-Session-Id", "X-Conversation-Id", "Thread-Id", "X-Client-Request-Id", "X-Request-Id", "X-Codex-Beta-Features", "X-Codex-Installation-Id", "X-Codex-Parent-Thread-Id", "X-Codex-Turn-Metadata", "X-Codex-Turn-State", "X-Codex-Window-Id", "X-Codex-Image-Turn-Id", "X-OpenAI-Internal-Codex-Responses-Lite", "X-OpenAI-Memgen-Request", "X-OpenAI-Subagent"} {
		if values := in.Values(name); len(values) > 0 {
			out[http.CanonicalHeaderKey(name)] = append([]string(nil), values...)
		}
	}
	return out
}

package service

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/util/responseheaders"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"go.uber.org/zap"
)

// 本文件收敛三个 CC（Chat Completions）forwarder 之间重复的 HTTP 管线与 SSE
// 循环骨架（PR #3802 遗留项）：
//
//   - forwardAsRawChatCompletions          （原生 CC 直转）
//   - forwardResponsesViaRawChatCompletions（/v1/responses → CC 回退）
//   - forwardAnthropicViaRawChatCompletions（/v1/messages → CC 回退）
//
// 以及 messages / chat_completions 两条 Responses 主路径中逐字相同的错误处理块。
// CC 回退路径在此共用流完整性校验；各路径的模型/effort 归一化、fast policy、
// Grok 分支与客户端协议编码仍留在调用方。

// newUpstreamSSEScanner 构造读取上游 SSE 流的行扫描器，按配置放大单行上限。
func (s *OpenAIGatewayService) newUpstreamSSEScanner(r io.Reader) *bufio.Scanner {
	scanner := bufio.NewScanner(r)
	maxLineSize := defaultMaxLineSize
	if s.cfg != nil && s.cfg.Gateway.MaxLineSize > 0 {
		maxLineSize = s.cfg.Gateway.MaxLineSize
	}
	scanner.Buffer(make([]byte, 0, 64*1024), maxLineSize)
	scanner.Split(newBoundedSSESplit(s.cfg, maxLineSize, nil))
	return scanner
}

// newStreamHeaderWriter 返回幂等的 SSE 响应头写入闭包：首次调用时透传过滤后的
// 上游响应头并写入标准 SSE 头 + 200 状态码，后续调用为 no-op。延迟到首个事件
// 写出前才提交响应头，使上游早期失败仍可改走 failover 或非流式错误响应。
func (s *OpenAIGatewayService) newStreamHeaderWriter(c *gin.Context, upstream http.Header) func() {
	headersWritten := false
	return func() {
		if headersWritten {
			return
		}
		headersWritten = true
		if s.responseHeaderFilter != nil {
			responseheaders.WriteFilteredHeaders(c.Writer.Header(), upstream, s.responseHeaderFilter)
		}
		c.Writer.Header().Set("Content-Type", "text/event-stream")
		c.Writer.Header().Set("Cache-Control", "no-cache")
		c.Writer.Header().Set("Connection", "keep-alive")
		c.Writer.Header().Set("X-Accel-Buffering", "no")
		c.Writer.WriteHeader(http.StatusOK)
	}
}

// readOpenAIUpstreamError 读取上游错误体并把 resp.Body 回卷为可重读的副本
// （下游 handleXxxErrorResponse 需要再次读取），返回原始错误体与脱敏后的
// 上游错误消息。
func (s *OpenAIGatewayService) readOpenAIUpstreamError(resp *http.Response) ([]byte, string) {
	respBody, upstreamMsg, _ := s.readOpenAIUpstreamErrorWithCompleteness(resp)
	return respBody, upstreamMsg
}

func (s *OpenAIGatewayService) readOpenAIUpstreamErrorWithCompleteness(resp *http.Response) ([]byte, string, bool) {
	limit := openAIUpstreamErrorBodyReadLimitForConfig(nil)
	if s != nil {
		limit = openAIUpstreamErrorBodyReadLimitForConfig(s.cfg)
	}
	respBody, readErr := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	complete := readErr == nil && int64(len(respBody)) <= limit
	if int64(len(respBody)) > limit {
		respBody = respBody[:limit]
	}
	_ = resp.Body.Close()
	resp.Body = io.NopCloser(bytes.NewReader(respBody))

	upstreamMsg := strings.TrimSpace(extractUpstreamErrorMessage(respBody))
	upstreamMsg = sanitizeUpstreamErrorMessage(upstreamMsg)
	return respBody, upstreamMsg, complete
}

// failoverOpenAIUpstreamHTTPError 对 >=400 的上游响应做 failover 判定：命中时
// 记录 ops 事件、执行账号级错误处置并返回 *UpstreamFailoverError；未命中返回
// nil，调用方继续走各自端点格式的非 failover 错误处理链。
func (s *OpenAIGatewayService) failoverOpenAIUpstreamHTTPError(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	resp *http.Response,
	respBody []byte,
	upstreamMsg string,
	upstreamModel string,
) *UpstreamFailoverError {
	shouldFailover := s.shouldFailoverOpenAIUpstreamResponse(account, resp.StatusCode, upstreamMsg, respBody)
	tempUnscheduled := false
	if c != nil && account != nil && account.Platform != PlatformGrok && !shouldFailover && !IsResponseCommitted(c) && s.rateLimitService != nil {
		tempUnscheduled = s.rateLimitService.CheckErrorPolicy(ctx, account, resp.StatusCode, respBody, upstreamModel) == ErrorPolicyTempUnscheduled
		shouldFailover = tempUnscheduled
	}
	if account != nil && account.Platform == PlatformGrok {
		shouldFailover = s.shouldFailoverGrokUpstreamError(resp.StatusCode, respBody)
	}
	if account != nil && account.Platform == PlatformGrok {
		s.handleGrokAccountUpstreamError(ctx, account, resp.StatusCode, resp.Header, respBody)
	}
	if !shouldFailover {
		return nil
	}
	upstreamDetail := ""
	if s.cfg != nil && s.cfg.Gateway.LogUpstreamErrorBody {
		maxBytes := s.cfg.Gateway.LogUpstreamErrorBodyMaxBytes
		if maxBytes <= 0 {
			maxBytes = 2048
		}
		upstreamDetail = truncateString(string(respBody), maxBytes)
	}
	appendOpsUpstreamError(c, OpsUpstreamErrorEvent{

		ProxyID:            opsUpstreamProxyID(account),
		ProxyName:          opsUpstreamProxyName(account),
		Platform:           account.Platform,
		AccountID:          account.ID,
		AccountName:        account.Name,
		UpstreamStatusCode: resp.StatusCode,
		UpstreamRequestID:  resp.Header.Get("x-request-id"),
		Kind:               "failover",
		Message:            upstreamMsg,
		Detail:             upstreamDetail,
	})
	shouldDisable := tempUnscheduled
	if account.Platform != PlatformGrok && !tempUnscheduled {
		shouldDisable = s.handleOpenAIAccountUpstreamError(ctx, account, resp.StatusCode, resp.Header, respBody, upstreamModel)
	}
	return s.newOpenAIAccountFailoverError(
		account,
		resp.StatusCode,
		resp.Header,
		respBody,
		upstreamMsg,
		shouldDisable,
		!shouldDisable && account.IsPoolMode() && (account.IsPoolModeRetryableStatus(resp.StatusCode) || isOpenAITransientProcessingError(resp.StatusCode, upstreamMsg, respBody)),
		resp.StatusCode,
	)
}

// openAIChatCompletionsTargetURL 解析账号的（非 Grok）Chat Completions 上游端点。
func (s *OpenAIGatewayService) openAIChatCompletionsTargetURL(account *Account) (string, error) {
	baseURL := account.GetOpenAIBaseURL()
	if baseURL == "" {
		baseURL = "https://api.openai.com"
	}
	validatedURL, err := s.validateUpstreamBaseURL(baseURL)
	if err != nil {
		return "", fmt.Errorf("invalid base_url: %w", err)
	}
	return buildOpenAIChatCompletionsURL(validatedURL), nil
}

// resolveCCFallbackTarget 解析两条 CC 回退路径共用的账号凭证与上游端点
// （回退路径仅面向 APIKey 账号，凭证恒为 openai api_key）。
func (s *OpenAIGatewayService) resolveCCFallbackTarget(account *Account) (apiKey string, targetURL string, err error) {
	apiKey = strings.TrimSpace(account.GetOpenAIProtocolAPIKey())
	if apiKey == "" {
		return "", "", fmt.Errorf("account %d missing api_key", account.ID)
	}
	targetURL, err = s.openAIChatCompletionsTargetURL(account)
	if err != nil {
		return "", "", err
	}
	return apiKey, targetURL, nil
}

// sendCCUpstreamRequest 构建并发送 CC 上游请求：分离的上游 context、OpenAI HTTP
// profile、标准头（含流式 Accept 切换）、客户端 header 白名单透传、自定义 UA 与
// 账号级 header 覆写，最后经代理发出。传输层失败（DNS/TCP/TLS，无 HTTP 响应）
// 统一由 handleOpenAIUpstreamTransportError 归一为 failover。
//
// userAgent 为空时保留默认 UA；Grok 的默认 UA 兜底由调用方解析后传入。
func (s *OpenAIGatewayService) sendCCUpstreamRequest(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	targetURL string,
	body []byte,
	stream bool,
	bearerToken string,
	userAgent string,
	grokCacheIdentity string,
) (*http.Response, error) {
	// DeepSeek thinking mode 要求历史 assistant 回传 reasoning_content。
	// Responses→CC 回退在加密-only / 缺 reasoning item 且缓存未命中时会漏掉该
	// 字段，上游 400 "The `reasoning_content` in the thinking mode must be
	// passed back to the API"。在共用出站点补空格占位，真实明文不覆盖。
	body = ensureDeepSeekChatReasoningPlaceholders(account, body)
	upstreamCtx, releaseUpstreamCtx := detachUpstreamContext(ctx)
	upstreamReq, err := http.NewRequestWithContext(upstreamCtx, http.MethodPost, targetURL, bytes.NewReader(body))
	releaseUpstreamCtx()
	if err != nil {
		return nil, fmt.Errorf("build upstream request: %w", err)
	}
	// 记录本次实际选择的协议端点，供错误日志和用量日志在没有
	// OpenAIForwardResult（例如 503/传输失败）时使用。每次发送都覆盖，
	// 避免 Gin context 在账号 failover 尝试之间残留旧端点。
	SetActualOpenAIUpstreamEndpoint(c, "/v1/chat/completions")
	upstreamReq = upstreamReq.WithContext(WithHTTPUpstreamProfile(upstreamReq.Context(), HTTPUpstreamProfileOpenAI))
	upstreamReq.Header.Set("Content-Type", "application/json")
	upstreamReq.Header.Set("Authorization", "Bearer "+bearerToken)
	if stream {
		upstreamReq.Header.Set("Accept", "text/event-stream")
	} else {
		upstreamReq.Header.Set("Accept", "application/json")
	}

	// 透传白名单中的客户端 header。详见 openaiCCRawAllowedHeaders 的设计说明。
	for key, values := range c.Request.Header {
		lowerKey := strings.ToLower(key)
		if openaiCCRawAllowedHeaders[lowerKey] {
			for _, v := range values {
				upstreamReq.Header.Add(key, v)
			}
		}
	}
	if userAgent != "" {
		upstreamReq.Header.Set("user-agent", userAgent)
	}

	if account.Platform == PlatformGrok {
		if account.IsGrokOAuth() {
			applyGrokCLIHeaders(upstreamReq.Header)
		}
		applyGrokCacheHeaders(upstreamReq.Header, grokCacheIdentity)
	}
	// 官方 OpenCode / Command Code 上游收敛为规范客户端 UA：客户端透传的编程库
	// UA 会命中其前置 Cloudflare bot 拦截（CF 1010/403），并被计入账号 403 strike。
	applyOpenCodeUpstreamUserAgent(account, targetURL, upstreamReq.Header)

	// 账号级请求头覆写：放在所有内置默认头（含 Grok CLI 身份头）之后应用，
	// 使配置值获得除共享传输层强制头之外的最高优先级。
	account.ApplyHeaderOverrides(upstreamReq.Header)
	applyOpenCodeSessionHeader(c, account, targetURL, upstreamReq.Header, body)

	proxyURL := ""
	if account.ProxyID != nil && account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}
	resp, err := s.doOpenAIUpstream(upstreamReq, proxyURL, account)
	if err != nil {
		return nil, s.handleOpenAIUpstreamTransportError(ctx, c, account, err, false)
	}
	return resp, nil
}

// ccStreamScanState 是 scanCCStream 返回的读取状态快照。
type ccStreamScanState struct {
	// Usage 为 include_usage chunk 中最近一次出现的用量（上游可能重复发送，
	// 总是保留最新值）；终态事件中的用量由调用方在 finalize 阶段自行覆盖。
	Usage OpenAIUsage
	// FirstTokenMs 为首个实际输出 chunk（排除 usage-only chunk）的到达时延。
	FirstTokenMs *int
	// SawDone 表示上游发出了 [DONE] 哨兵。
	SawDone         bool
	SawFinishReason bool
	SawOutput       bool
	// ErrorPayload is retained only for the existing sanitized Ops error recorder.
	ErrorPayload []byte
	// Err includes read failures, malformed/error events and missing output or
	// completion. Callers must skip successful finalization when it is non-nil.
	Err error
}

// scanCCStream 驱动两条 CC 回退路径共享的 SSE 读循环：提取 data 行、在 [DONE]
// 哨兵处停止、保留最新 usage、记录首 token 时延，并把每个解析成功的 chunk 交给
// emit 回调做各自的协议转换与写出。由调用方在排除客户端断开后记录失败。
func (s *OpenAIGatewayService) scanCCStream(
	c *gin.Context,
	resp *http.Response,
	startTime time.Time,
	emit func(*apicompat.ChatCompletionsChunk),
) ccStreamScanState {
	var st ccStreamScanState
	var protectionFinished, protectionIncomplete bool
	var explicitIncomplete bool
	var eventType string

	scanner := s.newUpstreamSSEScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			eventType = ""
		}
		if strings.HasPrefix(line, "event:") {
			eventType = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			continue
		}
		payload, ok := extractOpenAISSEDataLine(line)
		if !ok {
			continue
		}
		payload = strings.TrimSpace(payload)
		if payload == "" {
			continue
		}
		if payload == "[DONE]" {
			st.SawDone = true
			break
		}
		// 观察上游 CC chunk 回显的 model / service_tier；档位仅用于诊断，计费使用出站请求档位。
		// CC chunk 无 type 字段，按 untyped payload 观察（上游约束：只有终止
		// 事件与无类型 body 报告实际处理档位）。
		if observer := upstreamResponseModelObserverFromContext(c); observer != nil {
			observer.ObserveOpenAI([]byte(payload), "")
		}

		finished, incomplete := openAICCFinishState([]byte(payload))
		protectionFinished = protectionFinished || finished
		protectionIncomplete = protectionIncomplete || incomplete
		if u := extractCCStreamUsage(payload); u != nil {
			mergeOpenAICacheUsageEvidence(st.Usage, u)
			st.Usage = *u
		}
		// Error envelopes are valid JSON but not ChatCompletionsChunk values:
		// unmarshalling alone would silently discard their error fields.
		if isRawChatCompletionsErrorPayload([]byte(payload)) ||
			gjson.Get(payload, "type").String() == "response.failed" ||
			eventType == "error" || eventType == "response.failed" {
			st.ErrorPayload = []byte(payload)
			st.Err = errOpenAIUpstreamStreamError
			break
		}

		var chunk apicompat.ChatCompletionsChunk
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			st.Err = fmt.Errorf("%w: %w", errOpenAIUpstreamInvalidStream, err)
			break
		}
		for _, choice := range chunk.Choices {
			if choice.FinishReason != nil && strings.TrimSpace(*choice.FinishReason) != "" {
				st.SawFinishReason = true
				// Explicit generation limits/filtering keep their existing protocol
				// semantics, including when no content preceded the finish reason.
				explicitIncomplete = explicitIncomplete || *choice.FinishReason == "length" || *choice.FinishReason == "content_filter"
			}
		}
		if chatChunkStartsResponsesOutput(&chunk) {
			st.SawOutput = true
			if st.FirstTokenMs == nil {
				ms := int(time.Since(startTime).Milliseconds())
				st.FirstTokenMs = &ms
			}
		}
		emit(&chunk)
	}

	if st.Err == nil {
		st.Err = scanner.Err()
	}
	if st.Err == nil {
		switch {
		case !st.SawDone && !st.SawFinishReason:
			// Some providers attach cumulative usage to ordinary chunks; usage
			// alone must not turn a clean EOF into a successful completion.
			st.Err = ErrOpenAIUpstreamStreamTruncated
		case !st.SawOutput && !explicitIncomplete:
			st.Err = errOpenAIUpstreamEmptyResponse
		}
	}
	if !st.SawDone || st.Err != nil || !protectionFinished || protectionIncomplete {
		st.Usage.CacheReadKnown = false
	}
	return st
}

// ccStreamFailure preserves partial usage and gives both protocol handlers a
// typed failure they can terminate explicitly. It does not replay a request
// whose upstream may already have generated or billed tokens.
func (s *OpenAIGatewayService) ccStreamFailure(
	c *gin.Context,
	account *Account,
	resp *http.Response,
	st *ccStreamScanState,
	clientDisconnected bool,
	logPrefix, upstreamModel string,
) (error, bool) {
	var clientErr error
	if c != nil && c.Request != nil {
		clientErr = c.Request.Context().Err()
	}
	if clientDisconnected || clientErr != nil {
		st.Usage.CacheReadKnown = false
		if clientErr == nil {
			clientErr = context.Canceled
		}
		return fmt.Errorf("stream usage incomplete: %w", clientErr), true
	}
	if st.Err == nil {
		return nil, false
	}
	// A read timeout/cancellation while the downstream request is still active
	// is an upstream failure. Only actual client departure above suppresses it.
	requestID := resp.Header.Get("x-request-id")
	message := st.Err.Error()
	if len(st.ErrorPayload) > 0 {
		message = extractOpenAISSEErrorMessage(st.ErrorPayload)
	}
	s.recordOpenAIStreamUpstreamError(c, account, false, requestID, "http_error", st.ErrorPayload, message)
	fields := []zap.Field{
		zap.Error(st.Err),
		zap.String("upstream_request_id", requestID),
		zap.String("upstream_model", upstreamModel),
		zap.Bool("saw_done", st.SawDone),
		zap.Bool("saw_finish_reason", st.SawFinishReason),
		zap.Bool("saw_output", st.SawOutput),
	}
	if account != nil {
		fields = append(fields, zap.Int64("account_id", account.ID))
	}
	log := logger.L()
	if c != nil && c.Request != nil {
		log = logger.FromContext(c.Request.Context())
	}
	log.Warn(logPrefix+": upstream stream failed", fields...)
	return newOpenAIUpstreamStreamReadError(st.Err), false
}

// logCCStreamMissingDoneSentinel 记录"上游未发 [DONE] 哨兵即结束"的 debug 日志。
func logCCStreamMissingDoneSentinel(logPrefix, requestID string) {
	logger.L().Debug(logPrefix+": upstream stream ended without done sentinel",
		zap.String("request_id", requestID),
	)
}

// readCCUpstreamJSONResponse 读取并解析 CC 非流式 JSON 响应，失败时以调用方
// 端点格式回写错误；成功时顺带提取 usage。
func (s *OpenAIGatewayService) readCCUpstreamJSONResponse(
	c *gin.Context,
	resp *http.Response,
	writeError compatErrorWriter,
) (*apicompat.ChatCompletionsResponse, OpenAIUsage, error) {
	respBody, err := ReadUpstreamResponseBody(resp.Body, s.cfg, c, openAITooLargeError)
	if err != nil {
		if !errors.Is(err, ErrUpstreamResponseBodyTooLarge) {
			writeError(c, http.StatusBadGateway, "api_error", "Failed to read upstream response")
		}
		return nil, OpenAIUsage{}, fmt.Errorf("read upstream body: %w", err)
	}

	var ccResp apicompat.ChatCompletionsResponse
	if err := json.Unmarshal(respBody, &ccResp); err != nil {
		writeError(c, http.StatusBadGateway, "api_error", "Failed to parse upstream response")
		return nil, OpenAIUsage{}, fmt.Errorf("parse chat completions response: %w", err)
	}
	// 观察上游 CC JSON 回显的 model / service_tier；档位仅用于诊断，计费使用出站请求档位。
	// CC JSON 无 type 字段，按 untyped payload 观察（上游约束）。
	if observer := upstreamResponseModelObserverFromContext(c); observer != nil {
		observer.ObserveOpenAI(respBody, "")
	}

	usage := OpenAIUsage{}
	if parsed, ok := extractOpenAIUsageFromJSONBytes(respBody); ok {
		usage = parsed
	}
	finished, incomplete := openAICCFinishState(respBody)
	if !finished || incomplete {
		usage.CacheReadKnown = false
	}
	return &ccResp, usage, nil
}

// writeOpenAIResponsesFallbackError 以 /v1/responses 回退路径的既有错误格式回写
// （裸 error 对象；不调用 MarkResponseCommitted，与原内联写法保持一致）。
func writeOpenAIResponsesFallbackError(c *gin.Context, statusCode int, errType, message string) {
	c.JSON(statusCode, gin.H{
		"error": gin.H{
			"type":    errType,
			"message": message,
		},
	})
}

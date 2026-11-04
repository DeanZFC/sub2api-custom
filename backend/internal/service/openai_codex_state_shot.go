package service

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	OpenAICodexStateProbeDefaultModel  = "gpt-6-astra"
	openAICodexStateProbeShotTimeout   = 45 * time.Second
	openAICodexStateProbeMaxBody       = 1 << 20
	openAICodexStateProbeMaxDetailByte = 300
)

type openAICodexStateShot struct {
	answer  string
	status  int
	state   string
	cookies []string
	model   string
	// streamErr 非空表示 200 但回复流没有完整跑完（上游过载、额度用尽等流内失败）。
	streamErr error
	detail    string
}

type openAICodexStateProbeContextKey struct{}

func IsOpenAICodexStateProbeRequest(ctx context.Context) bool {
	value, _ := ctx.Value(openAICodexStateProbeContextKey{}).(bool)
	return value
}

func fireOpenAICodexStateShotRequest(ctx context.Context, headers http.Header, model, turnState, cookie string, send func(*http.Request) (*http.Response, error)) (openAICodexStateShot, error) {
	return fireOpenAICodexPromptShotRequest(ctx, headers, model, turnState, cookie, "Reply with OK.", "Reply with OK.", send)
}

func fireOpenAICodexPromptShotRequest(ctx context.Context, headers http.Header, model, turnState, cookie, instructions, prompt string, send func(*http.Request) (*http.Response, error)) (openAICodexStateShot, error) {
	var out openAICodexStateShot
	ctx = context.WithValue(ctx, openAICodexStateProbeContextKey{}, true)
	shotCtx, cancel := context.WithTimeout(ctx, openAICodexStateProbeShotTimeout)
	defer cancel()
	body := []byte(`{"model":` + jsonString(model) + `,"instructions":` + jsonString(instructions) + `,"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":` + jsonString(prompt) + `}]}],"stream":true,"store":false,"parallel_tool_calls":true,"include":["reasoning.encrypted_content"]}`)
	req, err := http.NewRequestWithContext(shotCtx, http.MethodPost, chatgptCodexURL, bytes.NewReader(body))
	if err != nil {
		return out, err
	}
	req = req.WithContext(WithHTTPUpstreamProfile(req.Context(), HTTPUpstreamProfileOpenAIHarvest))
	req.Close = true
	req.Host = "chatgpt.com"
	req.Header = headers.Clone()
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("OpenAI-Beta", "responses=experimental")
	req.Header.Set("session_id", uuid.NewString())
	// Never carry a business turn, request size, or compressed/lite body into a probe.
	for _, name := range []string{responsesLiteHeaderKey, openAICodexTurnStateHeader, "Cookie", "Content-Length", "Content-Encoding", "conversation_id", "x-codex-turn-metadata"} {
		req.Header.Del(name)
	}
	if turnState = strings.TrimSpace(turnState); turnState != "" {
		req.Header.Set(openAICodexTurnStateHeader, turnState)
	}
	if cookie = strings.TrimSpace(cookie); cookie != "" {
		req.Header.Set("Cookie", cookie)
	}

	resp, err := send(req)
	if err != nil {
		if resp != nil && resp.Body != nil {
			_ = resp.Body.Close()
		}
		return out, err
	}
	if resp == nil {
		return out, errors.New("nil upstream response")
	}
	out.status = resp.StatusCode
	out.state = strings.TrimSpace(extractOpenAICodexTurnState(resp.Header))
	out.cookies = openAICodexStateRouteCookies(resp)
	if resp.Body == nil {
		out.streamErr = errors.New("probe response body missing")
		return out, nil
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, openAICodexStateProbeMaxBody+1))
	if err != nil {
		if shotCtx.Err() != nil {
			return out, shotCtx.Err()
		}
		out.streamErr = errors.New("probe response incomplete")
		return out, nil
	}
	if len(data) > openAICodexStateProbeMaxBody {
		out.streamErr = errors.New("probe response too large")
		return out, nil
	}
	if out.status != http.StatusOK {
		out.detail = openAICodexStateDetail(data)
		return out, nil
	}
	out.answer = astraCandyStreamAnswer(data)
	out.model = openAICodexStateStreamModel(data)
	if out.streamErr = validateCodexProbeResponse(data); out.streamErr != nil {
		out.detail = openAICodexStateDetail(openAICodexStateStreamErrorPayload(data))
	}
	return out, nil
}

// openAICodexStateRouteCookies 只留两枚负载均衡路由 Cookie；门票要搭配它们续接才有意义。
func openAICodexStateRouteCookies(resp *http.Response) []string {
	var out []string
	seen := map[string]bool{}
	for _, cookie := range resp.Cookies() {
		if cookie.Name != "__cflb" && cookie.Name != "__oailb" {
			continue
		}
		if seen[cookie.Name] || cookie.Value == "" || cookie.MaxAge < 0 {
			continue
		}
		seen[cookie.Name] = true
		out = append(out, cookie.Name+"="+cookie.Value)
	}
	return out
}

func openAICodexStateDetail(body []byte) string {
	msg := strings.TrimSpace(extractUpstreamErrorMessage(body))
	if msg == "" {
		msg = strings.TrimSpace(string(body))
	}
	return SanitizeUpstreamErrorMessage(truncateString(msg, openAICodexStateProbeMaxDetailByte))
}

// openAICodexStateStreamEvents 按 SSE 事件切出每个 data 负载（跳过 [DONE]）。
func openAICodexStateStreamEvents(data []byte) [][]byte {
	var events [][]byte
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 4096), openAICodexStateProbeMaxBody)
	var lines []string
	flush := func() {
		if len(lines) == 0 {
			return
		}
		payload := strings.TrimSpace(strings.Join(lines, "\n"))
		lines = nil
		if payload != "" && payload != "[DONE]" {
			events = append(events, []byte(payload))
		}
	}
	for scanner.Scan() {
		line := strings.TrimSuffix(scanner.Text(), "\r")
		if line == "" {
			flush()
			continue
		}
		if strings.HasPrefix(line, "data:") {
			lines = append(lines, strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
		}
	}
	flush()
	return events
}

// openAICodexStateStreamModel 取上游声明的响应模型：response.created 优先，其次 response.completed。
// 只用于展示，不作判据。
func openAICodexStateStreamModel(data []byte) string {
	completed := ""
	for _, payload := range openAICodexStateStreamEvents(data) {
		var event struct {
			Type     string `json:"type"`
			Response struct {
				Model string `json:"model"`
			} `json:"response"`
		}
		if json.Unmarshal(payload, &event) != nil {
			continue
		}
		model := strings.TrimSpace(event.Response.Model)
		if model == "" {
			continue
		}
		switch event.Type {
		case "response.created":
			return model
		case "response.completed":
			completed = model
		}
	}
	return completed
}

// openAICodexStateStreamErrorPayload 取流里第一个失败事件，供提取上游报错原文。
func openAICodexStateStreamErrorPayload(data []byte) []byte {
	for _, payload := range openAICodexStateStreamEvents(data) {
		var event struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(payload, &event) != nil {
			continue
		}
		switch event.Type {
		case "error", "response.failed", "response.incomplete":
			var wrapped struct {
				Response struct {
					Error json.RawMessage `json:"error"`
				} `json:"response"`
			}
			if json.Unmarshal(payload, &wrapped) == nil && len(wrapped.Response.Error) > 0 && string(wrapped.Response.Error) != "null" {
				return []byte(`{"error":` + string(wrapped.Response.Error) + `}`)
			}
			return payload
		}
	}
	return nil
}

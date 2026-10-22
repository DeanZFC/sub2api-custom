package qqbot

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func imageToolTestResponse(protocol, args string) map[string]any {
	if protocol == "responses" {
		return map[string]any{"status": "completed", "output": []any{map[string]any{"type": "function_call", "name": chatImageToolName, "arguments": args, "call_id": "call-1"}}, "usage": map[string]int{"input_tokens": 11, "output_tokens": 7}}
	}
	return map[string]any{"choices": []any{map[string]any{"finish_reason": "tool_calls", "message": map[string]any{"content": nil, "tool_calls": []any{map[string]any{"id": "call-1", "type": "function", "function": map[string]any{"name": chatImageToolName, "arguments": args}}}}}}, "usage": map[string]int{"prompt_tokens": 11, "completion_tokens": 7}}
}

func imageToolTestJSON(t *testing.T, protocol, args string) []byte {
	t.Helper()
	raw, err := json.Marshal(imageToolTestResponse(protocol, args))
	require.NoError(t, err)
	return raw
}

func imageToolTestStream(t *testing.T, protocol, args string) []byte {
	t.Helper()
	if protocol == "responses" {
		return []byte("data: {\"type\":\"response.output_item.added\",\"item\":{\"type\":\"function_call\"}}\n\ndata: {\"type\":\"response.completed\",\"response\":" + string(imageToolTestJSON(t, protocol, args)) + "}\n\n")
	}
	var b strings.Builder
	runes := []rune(args)
	for i, part := range []string{string(runes[:len(runes)/2]), string(runes[len(runes)/2:])} {
		fn := map[string]string{"arguments": part}
		call := map[string]any{"index": 0, "function": fn}
		if i == 0 {
			call["id"], call["type"], fn["name"] = "call-1", "function", chatImageToolName
		}
		event, err := json.Marshal(map[string]any{"choices": []any{map[string]any{"index": 0, "delta": map[string]any{"tool_calls": []any{call}}}}})
		require.NoError(t, err)
		fmt.Fprintf(&b, "data: %s\n\n", event)
	}
	_, _ = b.WriteString("data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"tool_calls\"}]}\n\ndata: {\"choices\":[],\"usage\":{\"prompt_tokens\":11,\"completion_tokens\":7}}\n\ndata: [DONE]\n\n")
	return []byte(b.String())
}

func TestChatTurnModelChoosesImageAcrossProtocols(t *testing.T) {
	for _, protocol := range []string{"chat_completions", "responses"} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%t", protocol, stream), func(t *testing.T) {
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					require.Equal(t, "Bearer chat-key", r.Header.Get("Authorization"))
					var body map[string]any
					require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
					require.Equal(t, "auto", body["tool_choice"])
					require.Equal(t, false, body["parallel_tool_calls"])
					require.Equal(t, false, body["stream"])
					tools, ok := body["tools"].([]any)
					require.True(t, ok)
					require.Len(t, tools, 2)
					tool, ok := tools[0].(map[string]any)
					require.True(t, ok)
					require.Equal(t, "function", tool["type"])
					key := "messages"
					if protocol == "responses" {
						key = "input"
						require.Equal(t, "/responses", r.URL.Path)
					} else {
						tool, ok = tool["function"].(map[string]any)
						require.True(t, ok)
						require.Equal(t, "/chat/completions", r.URL.Path)
					}
					require.Equal(t, chatImageToolName, tool["name"])
					require.Equal(t, true, tool["strict"])
					messages, err := json.Marshal(body[key])
					require.NoError(t, err)
					for _, want := range []string{"赛博朋克猫咪海报", "就这个吧", "data:image/png;base64,dGVzdA=="} {
						require.Contains(t, string(messages), want)
					}
					const args = `{"prompt":"赛博朋克猫咪海报，蓝紫色霓虹"}`
					if stream {
						w.Header().Set("Content-Type", "text/event-stream")
						_, _ = w.Write(imageToolTestStream(t, protocol, args))
					} else {
						_, _ = w.Write(imageToolTestJSON(t, protocol, args))
					}
				}))
				defer server.Close()
				s := New(nil, nil, false, nil)
				c := DefaultConfig()
				c.BaseURL, c.APIKey, c.ChatProtocol, c.ImageEnabled = server.URL, "chat-key", protocol, true
				got, err := s.chatTurn(context.Background(), c, []Message{{Role: "assistant", Content: "赛博朋克猫咪海报"}, {Role: "user", Content: "就这个吧", Images: []string{"data:image/png;base64,dGVzdA=="}}})
				require.NoError(t, err)
				require.Equal(t, chatTurnResult{ImagePrompt: "赛博朋克猫咪海报，蓝紫色霓虹", InputTokens: 11, OutputTokens: 7}, got)
			})
		}
	}
}

func TestChatImageToolRejectsInvalidAndPartialCalls(t *testing.T) {
	for _, protocol := range []string{"chat_completions", "responses"} {
		for _, args := range []string{`{`, `{}`, `{"prompt":null}`, `{"prompt":123}`, `{"prompt":" "}`, `{"prompt":"cat","url":"https://example.invalid"}`, `{"prompt":"cat"}{}`, `{"prompt":"` + strings.Repeat("字", 6001) + `"}`} {
			got, err := parseChatTurn(imageToolTestJSON(t, protocol, args), "application/json")
			require.Error(t, err)
			require.Empty(t, got.ImagePrompt)
			require.EqualValues(t, 11, got.InputTokens)
		}
		raw := string(imageToolTestJSON(t, protocol, `{"prompt":"cat"}`))
		got, err := parseChatTurn([]byte(strings.ReplaceAll(raw, chatImageToolName, "delete_account")), "application/json")
		require.Error(t, err)
		require.Empty(t, got.ImagePrompt)
	}
	for _, raw := range []string{
		`{"choices":[{"message":{"tool_calls":[{"type":"function","function":{"name":"generate_image","arguments":"{\"prompt\":\"cat\"}"}},{"type":"function","function":{"name":"generate_image","arguments":"{\"prompt\":\"dog\"}"}}]}}]}`,
		`{"status":"incomplete","output":[{"type":"function_call","name":"generate_image","arguments":"{\"prompt\":\"cat\"}"}]}`,
		`{"choices":[{"finish_reason":"length","message":{"tool_calls":[{"type":"function","function":{"name":"generate_image","arguments":"{\"prompt\":\"cat\"}"}}]}}]}`,
	} {
		got, err := parseChatTurn([]byte(raw), "application/json")
		require.Error(t, err)
		require.Empty(t, got.ImagePrompt)
	}
	stream := string(imageToolTestStream(t, "chat_completions", `{"prompt":"cat"}`))
	for _, raw := range []string{
		strings.ReplaceAll(stream, "data: [DONE]", ""),
		strings.ReplaceAll(stream, `"finish_reason":"tool_calls"`, `"finish_reason":"length"`),
		strings.ReplaceAll(stream, `"index":0`, `"index":1`),
		stream + "data: {\"error\":{\"message\":\"upstream failed\"}}\n\n",
		"data: {\"type\":\"response.function_call_arguments.delta\",\"delta\":\"partial\"}\n\ndata: [DONE]\n\n",
	} {
		got, err := parseChatTurn([]byte(raw), "text/event-stream")
		require.Error(t, err)
		require.Empty(t, got.ImagePrompt)
	}
}

func TestChatTurnTextIsNeverExecutedAsTool(t *testing.T) {
	text := `{"name":"generate_image","arguments":{"prompt":"example only"}}`
	raw, err := json.Marshal(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": text}}}})
	require.NoError(t, err)
	got, err := parseChatTurn(raw, "application/json")
	require.NoError(t, err)
	require.Equal(t, text, got.Text)
	require.Empty(t, got.ImagePrompt)
}

func TestChatTurnDisabledAndAutonomousHaveNoTools(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		require.NotContains(t, body, "tools")
		require.NotContains(t, body, "tool_choice")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"text only"}}]}`))
	}))
	defer server.Close()
	s := New(nil, nil, false, nil)
	c := DefaultConfig()
	c.BaseURL = server.URL
	got, err := s.chatTurn(context.Background(), c, []Message{{Role: "user", Content: "来张图"}})
	require.NoError(t, err)
	require.Equal(t, "text only", got.Text)
	c.ImageEnabled = true
	_, _, _, err = s.chat(context.Background(), c, []Message{{Role: "user", Content: "来张图"}})
	require.NoError(t, err)
}

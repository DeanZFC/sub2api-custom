package prism

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
)

func completedTestTurn() map[string]any {
	return map[string]any{
		"status": "completed", "request_id": "synthetic-request-id",
		"response": map[string]any{
			"status": "success",
			"payload": map[string]any{
				"id": "resp_synchronous", "conversationId": testConversation,
				"output": []any{map[string]any{
					"id": "msg_synchronous", "type": "message", "role": "assistant",
					"content": []any{map[string]any{"type": "output_text", "text": "Synchronous answer"}},
				}},
			},
		},
	}
}

func fixturePollCount(f *fixture) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	count := 0
	for _, path := range f.paths {
		if path == "/api/llm/response_with_tools_status" {
			count++
		}
	}
	return count
}

func TestGenerateCompletedStartWithoutPollingState(t *testing.T) {
	for _, outerConversation := range []string{"", testConversation} {
		t.Run("outer_conversation="+outerConversation, func(t *testing.T) {
			c, f := newFixture(t, func(w http.ResponseWriter, r *http.Request) bool {
				if r.URL.Path != "/api/llm/response_with_tools_start" {
					return false
				}
				turn := completedTestTurn()
				if outerConversation != "" {
					turn["conversation_id"] = outerConversation
				}
				if err := json.NewEncoder(w).Encode(turn); err != nil {
					t.Fatal(err)
				}
				return true
			})
			result, err := generate(c, context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if result.Text != "Synchronous answer" || result.ID != "resp_synchronous" || result.ConversationID != testConversation {
				t.Fatalf("unexpected immediate result: %#v", result)
			}
			if f.startCount != 1 || fixturePollCount(f) != 0 {
				t.Fatal("a completed start must not be polled or submitted again")
			}
		})
	}
}

func TestGenerateCompletedErrorIsTerminalAndSanitized(t *testing.T) {
	for _, stage := range []string{"start", "poll"} {
		for _, tc := range []struct {
			name, reason, code string
			httpStatus         any
			wantHTTPStatus     int
		}{
			{"sandbox", "sandbox_reconnecting", "sandbox_reconnecting", 503, 503},
			{"context", "conversation_too_large", "conversation_too_large", 400, 400},
			{"access", "project_edit_access_required", "project_edit_access_required", 403, 403},
			{"unknown", "unknown", "upstream_rejected", 400, 400},
			{"private_reason", "synthetic-private-token", "upstream_rejected", "synthetic-private-token", 0},
			{"out_of_range_status", "unknown", "upstream_rejected", 999, 0},
		} {
			t.Run(stage+"/"+tc.name, func(t *testing.T) {
				path := "/api/llm/response_with_tools_" + stage
				if stage == "poll" {
					path = "/api/llm/response_with_tools_status"
				}
				c, f := newFixture(t, func(w http.ResponseWriter, r *http.Request) bool {
					if r.URL.Path != path {
						return false
					}
					turn := completedTestTurn()
					turn["response"] = map[string]any{
						"status": "error", "payload": map[string]any{
							"reason": tc.reason, "httpStatus": tc.httpStatus,
							"message": "synthetic-private-token", "rootCause": "synthetic-private-prompt",
							"codexRequestDebug": map[string]string{"token": "synthetic-private-token"},
						},
					}
					if err := json.NewEncoder(w).Encode(turn); err != nil {
						t.Fatal(err)
					}
					return true
				})
				_, err := generate(c, context.Background())
				var pe *Error
				if !errors.As(err, &pe) || !pe.Submitted || !pe.Terminal || pe.Code != tc.code || pe.StatusCode != tc.wantHTTPStatus {
					t.Fatalf("unexpected terminal error: %#v", err)
				}
				if strings.Contains(err.Error(), "synthetic-private") || errors.Unwrap(err) != nil {
					t.Fatal("upstream private diagnostics escaped")
				}
				wantPolls := 0
				if stage == "poll" {
					wantPolls = 1
				}
				if f.startCount != 1 || fixturePollCount(f) != wantPolls {
					t.Fatal("terminal errors must not be polled or submitted again")
				}
			})
		}
	}
}

func TestGenerateCompletedStartRejectsUnmatchedEnvelope(t *testing.T) {
	for _, tc := range []struct {
		name     string
		mutate   func(map[string]any)
		terminal bool
	}{
		{"missing_request_id", func(turn map[string]any) { delete(turn, "request_id") }, false},
		{"blank_request_id", func(turn map[string]any) { turn["request_id"] = " \t" }, false},
		{"outer_conversation_mismatch", func(turn map[string]any) { turn["conversation_id"] = "another-conversation" }, false},
		{"payload_conversation_mismatch", func(turn map[string]any) {
			turn["response"].(map[string]any)["payload"].(map[string]any)["conversationId"] = "another-conversation"
		}, true},
		{"missing_result", func(turn map[string]any) { delete(turn, "response") }, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, f := newFixture(t, func(w http.ResponseWriter, r *http.Request) bool {
				if r.URL.Path != "/api/llm/response_with_tools_start" {
					return false
				}
				turn := completedTestTurn()
				tc.mutate(turn)
				if err := json.NewEncoder(w).Encode(turn); err != nil {
					t.Fatal(err)
				}
				return true
			})
			_, err := generate(c, context.Background())
			var pe *Error
			if !errors.As(err, &pe) || !pe.Submitted || pe.Terminal != tc.terminal || pe.Code != "invalid_response" {
				t.Fatalf("unexpected invalid envelope error: %#v", err)
			}
			if f.startCount != 1 || fixturePollCount(f) != 0 {
				t.Fatal("invalid completed start must not be polled or submitted again")
			}
		})
	}
}

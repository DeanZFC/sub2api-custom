package service

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestAccountTestTimingMeasuresContentAndTerminalEvents(t *testing.T) {
	start := time.Date(2026, 10, 29, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name                     string
		metadata, content, final map[string]any
	}{
		{"responses", map[string]any{"type": "response.created"}, map[string]any{"type": "response.output_text.delta", "delta": "answer"}, map[string]any{"type": "response.completed"}},
		{"anthropic", map[string]any{"type": "message_start"}, map[string]any{"type": "content_block_delta", "delta": map[string]any{"text": "answer"}}, map[string]any{"type": "message_stop"}},
		{"chat", map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"role": "assistant"}}}}, map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"content": "answer"}}}}, map[string]any{"choices": []any{map[string]any{"finish_reason": "stop"}}}},
		{"gemini_wrapped", map[string]any{"response": map[string]any{}}, map[string]any{"response": map[string]any{"candidates": []any{map[string]any{"content": map[string]any{"parts": []any{map[string]any{"text": "answer"}}}}}}}, map[string]any{"response": map[string]any{"candidates": []any{map[string]any{"finishReason": "STOP"}}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := &ScheduledTestRequestAttempt{StartedAt: start}
			body := &accountTestUsageBody{attempt: a, sse: true}
			body.captureTiming(tc.metadata, start.Add(100*time.Millisecond))
			require.Nil(t, a.FirstTokenMS)
			body.captureTiming(tc.content, start.Add(500*time.Millisecond))
			require.Equal(t, 500.0, *a.FirstTokenMS)
			body.captureTiming(tc.final, start.Add(3*time.Second))
			require.Equal(t, 3000.0, *a.DurationMS)
			require.True(t, a.qualityTerminal)
			body.finishTiming(start.Add(10 * time.Second))
			require.Equal(t, 3000.0, *a.DurationMS, "close must not count cleanup time")
		})
	}
}

func TestAccountTestTimingNonStreamUnknownAndMalformedStream(t *testing.T) {
	for _, tc := range []struct {
		name, body                              string
		wantStream, wantMalformed, wantTerminal bool
	}{
		{"json", "{\"choices\":[{\"message\":{\"content\":\"answer\"}}]}", false, false, false},
		{"malformed", "data: {not-json}\n\ndata: [DONE]\n", true, true, true},
		{"partial", "data: {\"type\":\"response.output_text.delta\",\"delta\":\"answer\"}\n", true, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			collector := &accountTestUsageCollector{}
			ctx := context.WithValue(context.Background(), accountTestUsageKey{}, collector)
			resp, err := observeAccountTestResponse(ctx, time.Now().Add(-time.Second), costJSONResponse(200, tc.body), nil)
			require.NoError(t, err)
			_, err = io.Copy(io.Discard, resp.Body)
			require.NoError(t, err)
			require.NoError(t, resp.Body.Close())
			a := collector.snapshot().Attempts[0]
			require.Equal(t, tc.wantStream, a.qualityStream)
			require.Equal(t, tc.wantMalformed, a.qualityMalformed)
			require.Equal(t, tc.wantTerminal, a.qualityTerminal)
			require.NotNil(t, a.DurationMS)
			if !strings.Contains(tc.body, "response.output_text.delta") {
				require.Nil(t, a.FirstTokenMS)
			}
		})
	}
}

func TestAccountTestTimingBufferedSSEDoesNotInventGenerationDuration(t *testing.T) {
	collector := &accountTestUsageCollector{}
	ctx := context.WithValue(context.Background(), accountTestUsageKey{}, collector)
	body := "data: {\"type\":\"response.output_text.delta\",\"delta\":\"answer\"}\n\ndata: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":1000,\"output_tokens\":200,\"input_tokens_details\":{\"cached_tokens\":900}}}}\n"
	resp, err := observeAccountTestResponse(ctx, time.Now().Add(-time.Second), costJSONResponse(200, body), nil)
	require.NoError(t, err)
	_, err = io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	a := collector.snapshot().Attempts[0]
	require.NotNil(t, a.FirstTokenMS)
	require.Equal(t, *a.FirstTokenMS, *a.DurationMS)
	r := &ScheduledTestResult{Status: "success", ResponseText: "answer", ExecutionInfo: collector.snapshot()}
	samples, reason := passiveRecoverySamples(r, nil, "model", "private", false, time.Now())
	require.Empty(t, reason)
	require.Nil(t, summarizeActiveRecovery(samples).OutputTPS)
}

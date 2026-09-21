package service

import (
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// Check the required fields independently of the gateway's permissive event
// structs: strict Responses clients cannot decode a failed terminal without
// sequence_number, created_at, or a response identity.
func requireDecodableOpenAIFailedTerminal(t *testing.T, stream string) {
	t.Helper()
	count := 0
	forEachOpenAISSEFrame(stream, func(eventType string, data []byte) {
		if eventType != "response.failed" {
			return
		}
		count++
		var event struct {
			SequenceNumber *int64 `json:"sequence_number"`
			Response       struct {
				ID        string            `json:"id"`
				Object    string            `json:"object"`
				CreatedAt *int64            `json:"created_at"`
				Status    string            `json:"status"`
				Output    []json.RawMessage `json:"output"`
				Error     struct {
					Code    string `json:"code"`
					Message string `json:"message"`
				} `json:"error"`
			} `json:"response"`
		}
		require.NoError(t, json.Unmarshal(data, &event))
		require.NotNil(t, event.SequenceNumber)
		require.NotEmpty(t, event.Response.ID)
		require.Equal(t, "response", event.Response.Object)
		require.NotNil(t, event.Response.CreatedAt)
		require.Equal(t, "failed", event.Response.Status)
		require.NotNil(t, event.Response.Output)
		require.NotEmpty(t, event.Response.Error.Code)
		require.NotEmpty(t, event.Response.Error.Message)
	})
	require.Equal(t, 1, count, "exactly one decodable failure must reach the client")
}

func TestOpenAIStreamFailureEnvelopePreservesIdentityAndOrdering(t *testing.T) {
	source := []byte(`{"type":"response.failed","sequence_number":27,"response":{"id":"resp_original","object":"response","created_at":1789656300,"model":"gpt-5.6-terra","output":[],"error":{"code":"insufficient_quota","message":"provider balance is empty"}}}`)
	for _, stream := range []string{
		buildOpenAIResponseFailedSSE("resp_original", "gpt-5.6-terra", source, ""),
		func() string {
			payload, changed := sanitizeOpenAIResponseFailedEventForClient(source, "response.failed", true)
			require.True(t, changed)
			return "event: response.failed\ndata: " + string(payload) + "\n\n"
		}(),
	} {
		requireDecodableOpenAIFailedTerminal(t, stream)
		forEachOpenAISSEFrame(stream, func(_ string, data []byte) {
			require.Equal(t, int64(27), gjson.GetBytes(data, "sequence_number").Int())
			require.Equal(t, int64(1789656300), gjson.GetBytes(data, "response.created_at").Int())
			require.Equal(t, "resp_original", gjson.GetBytes(data, "response.id").String())
			require.Equal(t, "gpt-5.6-terra", gjson.GetBytes(data, "response.model").String())
		})
		require.NotContains(t, stream, "provider balance is empty")
	}
}

func TestOpenAIStreamFailureEnvelope(t *testing.T) {
	for _, upstream := range []struct {
		name string
		body string
	}{
		{
			name: "bare rate limit after partial output",
			body: "data: {\"type\":\"response.output_text.delta\",\"delta\":\"partial\"}\n\n" +
				"data: {\"type\":\"error\",\"error\":{\"code\":\"rate_limit_exceeded\",\"message\":\"retry later\"}}\n\n",
		},
		{
			name: "redacted provider billing after partial output",
			body: "data: {\"type\":\"response.output_text.delta\",\"delta\":\"partial\"}\n\n" +
				"data: {\"type\":\"response.failed\",\"response\":{\"error\":{\"code\":\"insufficient_balance\",\"message\":\"provider balance is empty\"},\"usage\":{\"input_tokens\":7}}}\n\n",
		},
	} {
		for _, mode := range []string{"native", "passthrough"} {
			t.Run(upstream.name+"/"+mode, func(t *testing.T) {
				var stream string
				body := io.NopCloser(strings.NewReader(upstream.body))
				if mode == "native" {
					recorder := newOpenAIResponseFlushRecorder()
					_, err := runOpenAIResponseFlushTest(recorder, body, config.GatewayConfig{})
					require.Error(t, err)
					stream, _ = recorder.snapshot()
				} else {
					_, recorder, _, err := runPassthroughFlushTest(t, body, -1)
					require.Error(t, err)
					stream = recorder.Body.String()
				}
				requireDecodableOpenAIFailedTerminal(t, stream)
				require.NotContains(t, stream, "provider balance is empty")
			})
		}
	}
}

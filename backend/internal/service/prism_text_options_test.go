//go:build unit

package service

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPrismGatewayAcceptsPlainTextOptions(t *testing.T) {
	for _, stream := range []bool{false, true} {
		for _, tc := range []struct {
			name, text, style string
		}{
			{"defaults", `{}`, ""},
			{"null", `null`, ""},
			{"plain", `{"format":{"type":"text"}}`, ""},
			{"medium", `{"format":{"type":"text"},"verbosity":"medium"}`, ""},
			{"null_verbosity", `{"verbosity":null}`, ""},
			{"low", `{"verbosity":"low"}`, "concise"},
			{"high", `{"format":{"type":"text"},"verbosity":"high"}`, "detailed"},
		} {
			t.Run(fmt.Sprintf("%s/stream=%t", tc.name, stream), func(t *testing.T) {
				svc, account, upstream := newPrismGatewayFixture(t)
				body := fmt.Sprintf(`{"model":"client-model","instructions":"Follow the requested language.","reasoning":{"effort":"high"},"input":[{"role":"user","content":"Earlier question"},{"role":"assistant","content":"Earlier answer"},{"role":"user","content":"Synthetic question"}],"text":%s,"stream":%t}`, tc.text, stream)
				upstream.wantRoles = []string{"system", "user", "assistant", "user"}
				upstream.wantTexts = []string{"Follow the requested language.", "Earlier question", "Earlier answer", "Synthetic question"}
				if tc.style != "" {
					// Validate the preference separately, then assert it and all of
					// the caller's unchanged history reach the actual start body.
					incoming, err := parsePrismIncoming([]byte(body), account, false, "")
					require.NoError(t, err)
					var messages []struct{ Role, Content string }
					require.NoError(t, json.Unmarshal(incoming.request.Input, &messages))
					require.Len(t, messages, 5)
					require.Equal(t, "developer", messages[1].Role)
					require.Contains(t, messages[1].Content, tc.style)
					upstream.wantRoles = []string{"system", "developer", "user", "assistant", "user"}
					upstream.wantTexts = []string{"Follow the requested language.", messages[1].Content, "Earlier question", "Earlier answer", "Synthetic question"}
				}
				ctx, c, rec := prismGatewayContext(body, "/v1/responses")
				result, err := svc.Forward(ctx, c, account, []byte(body))
				require.NoError(t, err)
				require.NotNil(t, result)
				require.Equal(t, http.StatusOK, rec.Code)
				require.Equal(t, 1, upstream.starts)
				require.Contains(t, rec.Body.String(), prismGatewayAnswer)
				require.NotContains(t, rec.Body.String(), "response.failed")
			})
		}
	}
}

func TestPrismGatewayRejectsUnsupportedTextOptionsBeforeNetwork(t *testing.T) {
	for _, tc := range []struct{ name, text, param string }{
		{"invalid_verbosity", `{"verbosity":"extreme"}`, "text.verbosity"},
		{"invalid_verbosity_type", `{"verbosity":5}`, "text.verbosity"},
		{"invalid_text", `[]`, "text"},
		{"invalid_format", `{"format":"text"}`, "text.format"},
		{"json_schema", `{"verbosity":"medium","format":{"type":"json_schema","name":"result","schema":{"type":"object"}}}`, "text.format.type"},
		{"json_object", `{"verbosity":"low","format":{"type":"json_object"}}`, "text.format.type"},
		{"unknown_text_field", `{"synthetic-private-field":true}`, "text"},
		{"unknown_format_field", `{"format":{"type":"text","synthetic-private-field":true}}`, "text.format"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, account, upstream := newPrismGatewayFixture(t)
			body := fmt.Sprintf(`{"model":"client-model","input":"Synthetic question","text":%s,"stream":true}`, tc.text)
			ctx, c, rec := prismGatewayContext(body, "/v1/responses")
			result, err := svc.Forward(ctx, c, account, []byte(body))
			require.Error(t, err)
			require.Nil(t, result)
			require.Equal(t, http.StatusBadRequest, rec.Code)
			require.Zero(t, upstream.calls)
			var response struct {
				Error struct{ Code, Param string }
			}
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &response))
			require.Equal(t, "prism_unsupported_request", response.Error.Code)
			require.Equal(t, tc.param, response.Error.Param)
			require.NotContains(t, rec.Body.String(), "synthetic-private")
		})
	}
}

func TestPrismGatewayChatPlainTextFormatStillWorks(t *testing.T) {
	svc, account, upstream := newPrismGatewayFixture(t)
	body := `{"model":"client-model","reasoning_effort":"high","messages":[{"role":"user","content":"Synthetic question"}],"response_format":{"type":"text"}}`
	ctx, c, rec := prismGatewayContext(body, "/v1/chat/completions")
	result, err := svc.ForwardAsChatCompletions(ctx, c, account, []byte(body), "", "")
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, 1, upstream.starts)
	require.Contains(t, rec.Body.String(), prismGatewayAnswer)
}

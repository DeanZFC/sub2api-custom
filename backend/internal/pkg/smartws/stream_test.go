package smartws

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestReusedConnectionKeepaliveAndFailureTail(t *testing.T) {
	const stale = `{"type":"keepalive","response_id":"r1","usage":{"input_tokens":999}}`
	const keepalive = `{"type":"keepalive"}`
	const created = `{"type":"response.created","response":{"id":"r2"}}`
	const firstError = `{"type":"error","error":{"message":"original failure"}}`
	const failed = `{"type":"response.failed","response":{"id":"r2","status":"failed","usage":{"input_tokens":17}}}`
	for _, tc := range []struct {
		name                      string
		frames                    []string
		wantTail, wantStreamError bool
	}{
		{"owned", []string{stale, keepalive, created, keepalive, firstError, stale, keepalive, failed}, true, false},
		{"wrong_response", []string{created, firstError, strings.Replace(failed, "r2", "other", 1)}, false, false},
		{"unknown_owner", []string{firstError, failed}, false, false},
		{"unbound_error_with_id", []string{`{"type":"error","response_id":"r2","message":"original failure"}`, failed}, false, false},
		{"unbound_error_with_usage", []string{`{"type":"error","response_id":"r2","usage":{"input_tokens":999}}`, failed}, false, true},
		{"unexpected_completion", []string{created, firstError, `{"type":"response.completed","response":{"id":"r2","status":"completed"}}`, failed}, false, false},
		{"unattributed_control", []string{`{"type":"keepalive","usage":{"input_tokens":999}}`}, false, true},
		{"conflicting_ids", []string{created, `{"type":"keepalive","response_id":"r1","response":{"id":"r2"}}`}, false, true},
		{"bounded_stale_frames", strings.Fields(strings.Repeat(stale+" ", maxIgnoredFrames+1)), false, true},
		{"bounded_tail", append(append([]string{created, firstError}, strings.Fields(strings.Repeat(keepalive+" ", maxIgnoredFrames))...), failed), false, false},
		{"timeout", []string{created, firstError}, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				ws, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
				if err != nil {
					return
				}
				defer ws.Close()
				for _, frames := range [][]string{
					{`{"type":"response.created","response":{"id":"r1"}}`, `{"type":"response.completed","response":{"id":"r1","status":"completed"}}`}, tc.frames,
				} {
					if _, _, err := ws.ReadMessage(); err != nil {
						return
					}
					for _, frame := range frames {
						if err := ws.WriteMessage(websocket.TextMessage, []byte(frame)); err != nil {
							return
						}
					}
				}
				_, _, _ = ws.ReadMessage()
			}))
			defer server.Close()
			pool := testPool(t)
			for turn := 1; turn <= 2; turn++ {
				req := testRequest(t, server.URL, "one-session")
				resp, handled, err := pool.Try(req, "", "scope")
				require.NoError(t, err)
				require.True(t, handled)
				data, err := io.ReadAll(resp.Body)
				require.NoError(t, resp.Body.Close())
				if turn == 1 {
					require.NoError(t, err)
					require.Contains(t, string(data), "r1")
					continue
				}
				require.NotContains(t, string(data), "999")
				require.NotContains(t, string(data), "response.completed")
				if tc.wantStreamError {
					var sent *SentError
					require.ErrorAs(t, err, &sent)
					return
				}
				require.NoError(t, err)
				require.Contains(t, string(data), "original failure")
				if tc.wantTail {
					require.Contains(t, string(data), "event: keepalive")
					require.Equal(t, 1, strings.Count(string(data), "event: response.failed"))
					for _, line := range strings.Split(string(data), "\n") {
						if v, ok := strings.CutPrefix(line, "data: "); ok && gjson.Get(v, "type").Str == "error" {
							require.EqualValues(t, 17, gjson.Get(v, "usage.input_tokens").Int())
						}
					}
				} else {
					require.NotContains(t, string(data), "response.failed")
					require.NotContains(t, string(data), "input_tokens")
				}
			}
		})
	}
}

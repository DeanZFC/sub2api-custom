package routes

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestGroupProbeRequiresTextAndNormalCompletion(t *testing.T) {
	content := "data: {\"choices\":[{\"delta\":{\"content\":\"OK\"}}]}\r\n\r\n"
	stop := "data: {\"choices\":[{\"finish_reason\":\"stop\"}]}\n\n"
	done := "data: [DONE]\n\n"
	for _, tc := range []struct {
		name, stream string
		forwarded    bool
		detail       string
	}{
		{"complete", content + stop + done, true, ""},
		{"empty", stop + done, true, "empty_response"},
		{"truncated", content + stop, true, "incomplete_stream"},
		{"error after text", content + "data: {\"error\":{}}\n" + stop + done, true, "incomplete_stream"},
		{"missing upstream completion", content + stop + done, false, "incomplete_stream"},
		{"token limit", content + "data: {\"choices\":[{\"finish_reason\":\"length\"}]}\n" + done, true, "incomplete_stream"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := &groupProbeWriter{start: time.Now(), status: 200}
			for _, b := range []byte(tc.stream) {
				_, err := w.Write([]byte{b})
				require.NoError(t, err)
			}
			require.Equal(t, tc.detail, w.failure(context.Background(), tc.forwarded))
		})
	}
	w := &groupProbeWriter{status: 429}
	fmt.Fprint(w, "secret provider details")
	require.Equal(t, "HTTP 429 Too Many Requests", w.failure(context.Background(), false))
}

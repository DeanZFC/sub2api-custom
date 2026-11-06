package service

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestCodexImages26GIFRoutingAndMIME(t *testing.T) {
	for _, model := range []string{"gpt-image-2.6", "gpt-image-2.6-flare", "gpt-image-2.6-sunburst"} {
		t.Run(model, func(t *testing.T) {
			body := []byte(fmt.Sprintf(`{"model":%q,"prompt":"waving cat","output_format":"gif","response_format":"url"}`, model))
			c, rec := newOpenAIImagesTestContext(t, body)
			upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"data":[{"b64_json":"R0lGODlh"}],"usage":{"output_tokens":20}}`))}}
			svc := newOpenAIImagesTestService(upstream)
			parsed, err := svc.ParseOpenAIImagesRequest(c, body)
			require.NoError(t, err)
			_, err = svc.ForwardImages(context.Background(), c, directImagesTestAccount(), body, parsed, "")
			require.NoError(t, err)
			require.Equal(t, "/backend-api/codex/images/generations", upstream.lastReq.URL.Path)
			require.Equal(t, "gif", gjson.GetBytes(upstream.lastBody, "output_format").String())
			require.Equal(t, "data:image/gif;base64,R0lGODlh", gjson.GetBytes(rec.Body.Bytes(), "data.0.url").String())
			// The existing fallback must preserve the requested format too.
			fallback, err := buildOpenAIImagesResponsesRequest(parsed, model)
			require.NoError(t, err)
			require.Equal(t, "gif", gjson.GetBytes(fallback, "tools.0.output_format").String())
		})
	}
}

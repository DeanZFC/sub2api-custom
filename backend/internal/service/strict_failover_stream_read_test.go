package service

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestStrictFailoverStreamReadFailuresExhaustOnlyCurrentTier(t *testing.T) {
	for _, protocol := range []string{"responses", "passthrough", "messages"} {
		for _, async := range []bool{false, true} {
			for _, cause := range []error{io.EOF, io.ErrUnexpectedEOF} {
				name := protocol + "/" + cause.Error()
				if async {
					name += "/async"
				}
				t.Run(name, func(t *testing.T) {
					g, repo := strictFixture()
					ctx := strictContext(g)
					excluded := map[int64]struct{}{}
					_, err := prepareStrictFailover(ctx, g, repo, excluded, "gpt-5.1", strictNoExtraGate)
					require.NoError(t, err)
					for i, id := range []int64{1, 2} {
						failure, c := runStrictStreamReadTest(t, ctx, protocol, async, &repo.accounts[i], errReadCloser{err: cause})
						var failoverErr *UpstreamFailoverError
						require.ErrorAs(t, failure, &failoverErr)
						require.False(t, c.Writer.Written(), "a replayable failure must not commit client output")
						RecordStrictFailoverFailure(ctx, id, failoverErr)
						require.Equal(t, "upstream_stream_read_failure", strictFailureReason(ctx, id))
						excluded[id] = struct{}{}
						selected, err := prepareStrictFailover(ctx, g, repo, excluded, "gpt-5.1", strictNoExtraGate)
						require.NoError(t, err)
						if i == 0 {
							require.Equal(t, 1, strictFailoverStateFrom(selected).tier)
							require.True(t, strictFailoverAllows(selected, 2))
						} else {
							require.Equal(t, 2, strictFailoverStateFrom(selected).tier)
							require.True(t, strictFailoverAllows(selected, 3))
						}
					}
				})
			}
		}
	}
}

func TestStrictFailoverStreamReadFailuresPreserveNonReplayableBoundaries(t *testing.T) {
	for _, protocol := range []string{"responses", "passthrough", "messages"} {
		for _, async := range []bool{false, true} {
			for _, tc := range []struct {
				name string
				body io.ReadCloser
			}{
				{"canceled", errReadCloser{err: context.Canceled}},
				{"scanner_limit", errReadCloser{err: bufio.ErrTooLong}},
				{"after_output", &openAIStreamReadThenErrorCloser{
					reader: strings.NewReader("data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_partial\"}}\n\n" +
						"data: {\"type\":\"response.output_text.delta\",\"delta\":\"partial\",\"output_index\":0,\"content_index\":0}\n\n"),
					err: io.ErrUnexpectedEOF,
				}},
			} {
				name := protocol + "/" + tc.name
				if async {
					name += "/async"
				}
				t.Run(name, func(t *testing.T) {
					g, repo := strictFixture()
					ctx := strictContext(g)
					_, err := prepareStrictFailover(ctx, g, repo, nil, "gpt-5.1", strictNoExtraGate)
					require.NoError(t, err)
					failure, _ := runStrictStreamReadTest(t, ctx, protocol, async, &Account{ID: 1, Platform: PlatformOpenAI}, tc.body)
					require.Error(t, failure)
					var failoverErr *UpstreamFailoverError
					if tc.name == "scanner_limit" {
						_ = errors.As(failure, &failoverErr) // Existing local retry behavior may remain, without tier evidence.
					} else {
						require.False(t, errors.As(failure, &failoverErr), "cancellation and committed output cannot authorize replay")
					}
					RecordStrictFailoverFailure(ctx, 1, failoverErr)
					require.Empty(t, strictFailureReason(ctx, 1))
				})
			}
		}
	}
}

func TestOpenAIMessagesOrdinaryReadErrorKeepsExistingBehavior(t *testing.T) {
	failure, _ := runStrictStreamReadTest(t, context.Background(), "messages", false,
		&Account{ID: 1, Platform: PlatformOpenAI}, errReadCloser{err: io.ErrUnexpectedEOF})
	require.ErrorIs(t, failure, io.ErrUnexpectedEOF)
	var failoverErr *UpstreamFailoverError
	require.False(t, errors.As(failure, &failoverErr))
}

func runStrictStreamReadTest(t *testing.T, ctx context.Context, protocol string, async bool, account *Account, body io.ReadCloser) (error, *gin.Context) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil).WithContext(ctx)
	cfg := &config.Config{Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize}}
	if async {
		cfg.Gateway.StreamKeepaliveInterval = 1
	}
	svc := &OpenAIGatewayService{cfg: cfg}
	resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: body}
	var err error
	switch protocol {
	case "responses":
		_, err = svc.handleStreamingResponse(ctx, resp, c, account, time.Now(), "gpt-5.1", "gpt-5.1")
	case "passthrough":
		_, err = svc.handleStreamingResponsePassthrough(ctx, resp, c, account, time.Now(), "gpt-5.1", "gpt-5.1")
	case "messages":
		_, err = svc.handleAnthropicStreamingResponse(resp, c, account, "gpt-5.1", "gpt-5.1", "gpt-5.1", time.Now())
	default:
		t.Fatalf("unsupported test protocol %s", protocol)
	}
	return err, c
}

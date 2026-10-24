package service

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestGroupQualityFallbackUpstreamContextPreservesCancellation(t *testing.T) {
	for _, helper := range []struct {
		name   string
		detach func(context.Context) (context.Context, context.CancelFunc)
	}{
		{"all upstream", detachUpstreamContext},
		{"stream upstream", func(ctx context.Context) (context.Context, context.CancelFunc) {
			return detachStreamUpstreamContext(ctx, true)
		}},
	} {
		t.Run(helper.name, func(t *testing.T) {
			for _, active := range []bool{false, true} {
				name := "ordinary budget request"
				if active {
					name = "reserved emergency fallback"
				}
				t.Run(name, func(t *testing.T) {
					type metadataKey struct{}
					base := context.WithValue(context.Background(), metadataKey{}, "request-value")
					ctx := GroupQualityTrafficContext(base, GroupQualityTrafficRequest{Key: "request", GroupID: 8})
					bindGroupQualityTrafficLease(ctx, nil, 7, "public", "lease")
					if active {
						bindGroupQualityFallbackStop(ctx, "lease", func() {})
					}
					upstream, release := helper.detach(ctx)
					release()
					require.NoError(t, upstream.Err(), "request-builder cleanup must not terminate the upstream")
					require.Equal(t, "request-value", upstream.Value(metadataKey{}))
					cancelGroupQualityTraffic(ctx, ErrGroupQualityFallbackLeaseLost)
					if active {
						require.ErrorIs(t, upstream.Err(), context.Canceled)
						require.ErrorIs(t, context.Cause(upstream), ErrGroupQualityFallbackLeaseLost)
					} else {
						require.NoError(t, upstream.Err(), "ordinary traffic still drains for usage billing")
						require.NoError(t, context.Cause(upstream))
					}
				})
			}
		})
	}
}

func TestGroupQualityFallbackHTTPLeaseLossIsNotUpstreamFailure(t *testing.T) {
	ctx := GroupQualityTrafficContext(context.Background(), GroupQualityTrafficRequest{Key: "request", GroupID: 8})
	defer cancelGroupQualityTraffic(ctx, context.Canceled)
	bindGroupQualityTrafficLease(ctx, nil, 7, "public", "lease")
	bindGroupQualityFallbackStop(ctx, "lease", func() {})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cancelGroupQualityTraffic(ctx, ErrGroupQualityFallbackLeaseLost)
		<-r.Context().Done()
	}))
	defer upstream.Close()
	upstreamCtx, release := detachUpstreamContext(ctx)
	release()
	req, err := http.NewRequestWithContext(upstreamCtx, http.MethodGet, upstream.URL, nil)
	require.NoError(t, err)
	client := upstream.Client()
	client.Timeout = time.Second
	resp, transportErr := client.Do(req)
	if resp != nil {
		_ = resp.Body.Close()
	}
	require.ErrorIs(t, transportErr, ErrGroupQualityFallbackLeaseLost, "real HTTP transport exposes the local cancellation cause")
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = req
	err = (&OpenAIGatewayService{}).handleOpenAIUpstreamTransportError(ctx, c, &Account{ID: 7, Platform: PlatformOpenAI}, transportErr, false)
	require.ErrorIs(t, err, context.Canceled)
	require.ErrorIs(t, err, ErrGroupQualityFallbackLeaseLost)
	var failover *UpstreamFailoverError
	require.False(t, errors.As(err, &failover))
	require.Empty(t, c.GetString(OpsUpstreamErrorMessageKey))
	_, hasEvents := c.Get(OpsUpstreamErrorsKey)
	require.False(t, hasEvents)
}

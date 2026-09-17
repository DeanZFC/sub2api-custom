//go:build unit

package service

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPrismCompletedStartThroughGateway(t *testing.T) {
	for _, chat := range []bool{false, true} {
		for _, stream := range []bool{false, true} {
			for _, rejected := range []bool{false, true} {
				t.Run(fmt.Sprintf("chat=%t/stream=%t/rejected=%t", chat, stream, rejected), func(t *testing.T) {
					svc, account, upstream := newPrismGatewayFixture(t)
					upstream.completedStart = true
					upstream.completedStartError = rejected
					body := fmt.Sprintf(`{"model":"client-model","input":"Synthetic question","reasoning":{"effort":"high"},"stream":%t}`, stream)
					if chat {
						body = fmt.Sprintf(`{"model":"client-model","messages":[{"role":"user","content":"Synthetic question"}],"reasoning_effort":"high","stream":%t}`, stream)
					}
					ctx, c, rec := prismGatewayContext(body, "/v1/responses")
					var err error
					if chat {
						_, err = svc.ForwardAsChatCompletions(ctx, c, account, []byte(body), "", "")
					} else {
						_, err = svc.Forward(ctx, c, account, []byte(body))
					}
					if rejected {
						require.Error(t, err)
						var failover *UpstreamFailoverError
						require.False(t, errors.As(err, &failover))
						require.Contains(t, rec.Body.String(), "prism_upstream_rejected")
						require.Contains(t, rec.Body.String(), "HTTP 400")
						require.NotContains(t, rec.Body.String(), "generation may have started")
						require.NotContains(t, rec.Body.String(), "invalid_response")
					} else {
						require.NoError(t, err)
						require.Contains(t, rec.Body.String(), prismGatewayAnswer)
					}
					prismGatewayAssertPrivateAbsent(t, rec.Body.String())
					require.Equal(t, 1, upstream.starts)
					require.Zero(t, upstream.polls)
				})
			}
		}
	}
}

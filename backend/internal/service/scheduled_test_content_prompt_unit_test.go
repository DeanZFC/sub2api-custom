//go:build unit

package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestScheduledContentInstructionsOnAdaptiveCNEndpoints(t *testing.T) {
	for _, platform := range []string{PlatformDeepseek, PlatformKimi} {
		t.Run(platform, func(t *testing.T) {
			account := adaptiveCNAccountTestAccount(302, platform)
			svc, upstream := adaptiveCNAccountTestService(account, adaptiveCNChatTestResponse(), adaptiveCNAnthropicTestResponse(), adaptiveCNResponsesTestResponse())
			c, _ := newTestContext()
			c.Request = c.Request.WithContext(withScheduledTestOutputKind(context.Background(), "html"))
			err := svc.TestAccountConnection(c, account.ID, "model", "pelican", AccountTestModeDefault)
			require.NoError(t, err)
			require.Len(t, upstream.bodies, 3)
			for i, path := range []string{"messages.0.content", "system.0.text", "instructions"} {
				require.Equal(t, scheduledTestHTMLInstructions, gjson.GetBytes(upstream.bodies[i], path).String())
			}
			for i, path := range []string{"messages.1.content", "messages.0.content.0.text", "input.0.content.0.text"} {
				require.Equal(t, "pelican", gjson.GetBytes(upstream.bodies[i], path).String())
			}
			require.False(t, gjson.GetBytes(upstream.bodies[2], "store").Bool())
		})
	}
}

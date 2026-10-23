package repository

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/require"
)

func TestSanitizeChannelMonitorV2ErrorDetail(t *testing.T) {
	message := sanitizeChannelMonitorV2ErrorDetail("Worker failed: Bearer first-secret; BEARER second-secret sk-first-key sk-second-key api_key=third-secret access_token=fourth-secret")
	for _, secret := range []string{"first-secret", "second-secret", "first-key", "second-key", "third-secret", "fourth-secret"} {
		require.NotContains(t, message, secret)
	}
	require.Contains(t, message, "Worker failed")
	message = sanitizeChannelMonitorV2ErrorDetail(strings.Repeat("错误信息", 100))
	require.True(t, utf8.ValidString(message))
	require.Equal(t, strings.Repeat("错误信息", 60)+"…", message)
	require.Empty(t, sanitizeChannelMonitorV2ErrorDetail(" \n\t"))
}

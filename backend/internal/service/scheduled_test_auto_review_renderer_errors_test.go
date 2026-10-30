package service

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestScheduledTestReviewRendererHTTPFailures(t *testing.T) {
	for _, tc := range []struct {
		name      string
		status    int
		body      string
		code      string
		retryable bool
	}{
		{"legacy busy", 429, "{\"error\":\"renderer is busy\"}", "renderer_busy", true},
		{"legacy unavailable", 503, "{\"error\":\"private path /secret\"}", "renderer_unavailable", true},
		{"browser startup failure", 503, "{\"code\":\"renderer_browser_unavailable\",\"error\":\"private diagnostic\"}", "renderer_browser_unavailable", false},
		{"content failure", 503, "{\"code\":\"renderer_render_failed\",\"error\":\"<script>secret</script>\"}", "renderer_render_failed", false},
		{"timeout", 504, "{}", "renderer_timeout", true},
		{"input rejected", 413, "{}", "renderer_invalid_input", false},
		{"unknown code", 503, "{\"code\":\"<script>secret</script>\"}", "renderer_unavailable", true},
		{"unknown status", 404, "{}", "renderer_invalid_response", false},
		{"oversized body", 503, strings.Repeat("x", 5000), "renderer_unavailable", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := scheduledTestRenderHTTPFailure(tc.status, strings.NewReader(tc.body))
			require.Equal(t, tc.code, err.code)
			require.Equal(t, tc.status, err.httpStatus)
			require.Equal(t, tc.retryable, err.retryable())
			require.NotContains(t, err.Error(), "secret")
			require.NotContains(t, err.Error(), "private")
		})
	}
}

func TestScheduledTestReviewRendererTransportFailures(t *testing.T) {
	for _, tc := range []struct {
		err              error
		code, diagnostic string
		retryable        bool
	}{
		{context.Canceled, "renderer_cancelled", "cancelled", false},
		{context.DeadlineExceeded, "renderer_timeout", "timeout", true},
		{&os.PathError{Op: "dial", Path: "/private/socket", Err: os.ErrNotExist}, "renderer_unavailable", "socket_not_found", true},
		{&os.PathError{Op: "dial", Path: "/private/socket", Err: os.ErrPermission}, "renderer_unavailable", "socket_permission_denied", false},
		{&net.OpError{Op: "dial", Net: "unix", Err: syscall.ECONNREFUSED}, "renderer_unavailable", "connection_refused", true},
		{errors.New("token=private /secret"), "renderer_render_failed", "unknown", false},
	} {
		failure := scheduledTestRenderTransportFailure(tc.err)
		require.Equal(t, tc.code, failure.code)
		require.Equal(t, tc.diagnostic, failure.diagnostic)
		require.Equal(t, tc.retryable, failure.retryable())
		require.ErrorIs(t, failure, tc.err)
		require.NotContains(t, failure.Error(), "private")
	}
}

func TestScheduledTestReviewRendererMissingSocketAndInvalidInput(t *testing.T) {
	socket := filepath.Join(os.TempDir(), fmt.Sprintf("renderer-missing-%d-%d.sock", os.Getpid(), time.Now().UnixNano()))
	t.Setenv("SCHEDULED_TEST_RENDERER_SOCKET", socket)
	renderer := NewScheduledTestReviewRenderer()
	_, err := renderer.Render(context.Background(), "<svg/>")
	require.Error(t, err)
	require.Equal(t, "socket_not_found", scheduledTestRenderFailure(err).diagnostic)
	for _, html := range []string{" \t", strings.Repeat("a", scheduledTestReviewMaxHTMLBytes+1)} {
		_, err := renderer.Render(context.Background(), html)
		require.Equal(t, "renderer_invalid_input", scheduledTestRenderFailure(err).code)
	}
}

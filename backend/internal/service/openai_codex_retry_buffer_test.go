//go:build unit

package service

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestCodexRetrySSEBufferRetainsWireBytes(t *testing.T) {
	settings := DefaultCodexPreOutputRetrySettings()
	settings.Enabled = true
	for _, limit := range []int{16, codexRetryHTTPBufferLimit} {
		t.Run(time.Duration(limit).String(), func(t *testing.T) {
			body, failure, err := bufferCodexRetrySSE(context.Background(), io.NopCloser(strings.NewReader(codexRetryTestSuccess)), settings, limit, time.Second)
			require.NoError(t, err)
			require.Nil(t, failure)
			require.NotNil(t, body)
			defer func() { require.NoError(t, body.Close()) }()
			payload, err := io.ReadAll(body)
			require.NoError(t, err)
			require.Equal(t, codexRetryTestSuccess, string(payload))
		})
	}
}

func TestCodexRetrySSEBufferMatchesEventFraming(t *testing.T) {
	settings := DefaultCodexPreOutputRetrySettings()
	settings.Enabled = true
	for _, frame := range []string{
		"data: " + codexRetryTestFailure + "\n\n",
		"event: response.failed\r\ndata: " + codexRetryTestFailure + "\r\n\r\n",
		"data: " + codexRetryTestFailure,
	} {
		body, failure, err := bufferCodexRetrySSE(context.Background(), io.NopCloser(strings.NewReader(frame)), settings, codexRetryHTTPBufferLimit, time.Second)
		require.NoError(t, err)
		require.Nil(t, body)
		require.NotNil(t, failure)
		require.Equal(t, codexRetryTestFailure, failure.Data)
		require.Equal(t, "response.failed", failure.EventType)
	}
}

func TestCodexRetrySSEBufferDeadlineHandsOffPendingRead(t *testing.T) {
	reader, writer := io.Pipe()
	defer func() { _ = writer.Close() }()
	settings := DefaultCodexPreOutputRetrySettings()
	settings.Enabled = true
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	body, failure, err := bufferCodexRetrySSE(ctx, reader, settings, codexRetryHTTPBufferLimit, 10*time.Millisecond)
	require.NoError(t, err)
	require.Nil(t, failure)
	require.NotNil(t, body)
	defer func() { require.NoError(t, body.Close()) }()
	done := make(chan error, 1)
	go func() {
		_, err := io.WriteString(writer, codexRetryTestSuccess)
		_ = writer.Close()
		done <- err
	}()
	payload, err := io.ReadAll(body)
	require.NoError(t, err)
	require.NoError(t, <-done)
	require.Equal(t, codexRetryTestSuccess, string(payload))
}

func TestCodexRetrySSEBufferCancellationClosesUpstream(t *testing.T) {
	reader, writer := io.Pipe()
	defer func() { _ = writer.Close() }()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	body, failure, err := bufferCodexRetrySSE(ctx, reader, DefaultCodexPreOutputRetrySettings(), codexRetryHTTPBufferLimit, time.Second)
	require.ErrorIs(t, err, context.Canceled)
	require.Nil(t, body)
	require.Nil(t, failure)
	_, err = writer.Write([]byte("closed"))
	require.ErrorIs(t, err, io.ErrClosedPipe)
}

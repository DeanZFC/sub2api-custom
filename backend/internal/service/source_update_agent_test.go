package service

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func newUnixAgentTestServer(t *testing.T, handler http.Handler) string {
	t.Helper()
	// macOS limits Unix socket paths to roughly 104 bytes; t.TempDir paths can
	// exceed that once the test name is included.
	dir, err := os.MkdirTemp("/tmp", "su-")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socketPath := filepath.Join(dir, "u.sock")
	listener, err := net.Listen("unix", socketPath)
	require.NoError(t, err)
	server := &http.Server{Handler: handler}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() {
		_ = server.Shutdown(context.Background())
		_ = os.Remove(socketPath)
	})
	return socketPath
}

func newUnixAgentClient(socketPath, token string) *sourceUpdateAgentClient {
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", socketPath)
		},
	}
	return &sourceUpdateAgentClient{
		socketPath: socketPath,
		token:      token,
		client:     &http.Client{Transport: transport, Timeout: time.Second},
	}
}

func TestSourceUpdateAgentClientUsesUnixSocketAndToken(t *testing.T) {
	const token = "test-updater-token"
	socketPath := newUnixAgentTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, token, r.Header.Get("X-Sub2API-Updater-Token"))
		if r.URL.Path == "/health" {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"status":"ok"}`))
			return
		}
		if r.URL.Path == "/v1/update" {
			var body struct {
				RequestID       string `json:"request_id"`
				ExpectedVersion string `json:"expected_version"`
			}
			require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
			require.Equal(t, "request-1", body.RequestID)
			require.Equal(t, "0.2.0-custom.2", body.ExpectedVersion)
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"job_id":"upd-1","status":"queued"}`))
			return
		}
		if r.URL.Path == "/v1/update/upd-1" {
			_, _ = w.Write([]byte(`{"job_id":"upd-1","status":"succeeded"}`))
			return
		}
		http.NotFound(w, r)
	}))
	client := newUnixAgentClient(socketPath, token)

	require.NoError(t, client.Health(context.Background()))
	job, err := client.Start(context.Background(), "request-1", "0.2.0-custom.2")
	require.NoError(t, err)
	require.Equal(t, "upd-1", job.JobID)
	status, err := client.Status(context.Background(), "upd-1")
	require.NoError(t, err)
	require.Equal(t, SourceUpdateStatusSucceeded, status.Status)
}

func TestSourceUpdateAgentClientRejectsUnauthorizedAndMalformedJobs(t *testing.T) {
	socketPath := newUnixAgentTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Sub2API-Updater-Token") != "good-token" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"unauthorized"}`))
			return
		}
		_, _ = w.Write([]byte(`{"status":"queued"}`))
	}))
	client := newUnixAgentClient(socketPath, "bad-token")
	_, err := client.Start(context.Background(), "request-1", "0.2.0-custom.2")
	require.Error(t, err)

	client = newUnixAgentClient(socketPath, "good-token")
	_, err = client.Start(context.Background(), "request-1", "0.2.0-custom.2")
	require.Error(t, err)
	require.Contains(t, strings.ToLower(err.Error()), "invalid")

	_, err = client.Status(context.Background(), "bad/job")
	require.Error(t, err)
}

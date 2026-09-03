package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const (
	DefaultSourceUpdaterSocketPath = "/run/sub2api-updater/sub2api-custom.sock"
	SourceUpdateStatusQueued       = "queued"
	SourceUpdateStatusRunning      = "running"
	SourceUpdateStatusSucceeded    = "succeeded"
	SourceUpdateStatusFailed       = "failed"
)

var (
	ErrSourceUpdaterUnavailable = infraerrors.ServiceUnavailable(
		"SOURCE_UPDATER_UNAVAILABLE",
		"source update agent is not configured or unavailable",
	)
	ErrSourceUpdateInvalidResponse = infraerrors.ServiceUnavailable(
		"SOURCE_UPDATER_INVALID_RESPONSE",
		"source update agent returned an invalid response",
	)
)

// SourceUpdateJob is the persisted job state returned by the host updater.
// The updater continues after the application container is recreated, so the
// browser can poll this state after the original HTTP request is gone.
type SourceUpdateJob struct {
	JobID          string `json:"job_id"`
	RequestID      string `json:"request_id,omitempty"`
	Status         string `json:"status"`
	Stage          string `json:"stage,omitempty"`
	Message        string `json:"message,omitempty"`
	Error          string `json:"error,omitempty"`
	CurrentVersion string `json:"current_version,omitempty"`
	TargetVersion  string `json:"target_version,omitempty"`
	StartedAt      string `json:"started_at,omitempty"`
	FinishedAt     string `json:"finished_at,omitempty"`
}

// SourceUpdateAgent is intentionally narrower than an arbitrary command
// runner. Implementations may only start and inspect the current deployment's
// preconfigured source update workflow.
type SourceUpdateAgent interface {
	Health(ctx context.Context) error
	Start(ctx context.Context, requestID, expectedVersion string) (SourceUpdateJob, error)
	Status(ctx context.Context, jobID string) (SourceUpdateJob, error)
}

func (c *sourceUpdateAgentClient) Health(ctx context.Context) error {
	if c == nil || c.client == nil || c.socketPath == "" || c.token == "" {
		return ErrSourceUpdaterUnavailable
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://sub2api-updater/health", nil)
	if err != nil {
		return ErrSourceUpdaterUnavailable.WithCause(err)
	}
	req.Header.Set("X-Sub2API-Updater-Token", c.token)
	resp, err := c.client.Do(req)
	if err != nil {
		return ErrSourceUpdaterUnavailable.WithCause(err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	if resp.StatusCode != http.StatusOK {
		return ErrSourceUpdaterUnavailable.WithCause(fmt.Errorf("updater health returned %s", resp.Status))
	}
	return nil
}

type sourceUpdateAgentClient struct {
	socketPath string
	token      string
	client     *http.Client
}

type sourceUpdateStartRequest struct {
	RequestID       string `json:"request_id"`
	ExpectedVersion string `json:"expected_version,omitempty"`
}

// NewSourceUpdateAgentFromEnv creates the host updater client used by source
// builds. An empty token deliberately disables the integration; this keeps an
// old deployment safe until its updater service is explicitly installed.
func NewSourceUpdateAgentFromEnv() SourceUpdateAgent {
	socketPath := strings.TrimSpace(os.Getenv("SUB2API_UPDATER_SOCKET"))
	token := strings.TrimSpace(os.Getenv("SUB2API_UPDATER_TOKEN"))
	if socketPath == "" {
		socketPath = DefaultSourceUpdaterSocketPath
	}
	if token == "" {
		return nil
	}
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", socketPath)
		},
	}
	return &sourceUpdateAgentClient{
		socketPath: socketPath,
		token:      token,
		client:     &http.Client{Transport: transport, Timeout: 10 * time.Second},
	}
}

func (c *sourceUpdateAgentClient) Start(ctx context.Context, requestID, expectedVersion string) (SourceUpdateJob, error) {
	payload, err := json.Marshal(sourceUpdateStartRequest{
		RequestID:       strings.TrimSpace(requestID),
		ExpectedVersion: strings.TrimSpace(expectedVersion),
	})
	if err != nil {
		return SourceUpdateJob{}, err
	}
	return c.do(ctx, http.MethodPost, "http://sub2api-updater/v1/update", payload)
}

func (c *sourceUpdateAgentClient) Status(ctx context.Context, jobID string) (SourceUpdateJob, error) {
	jobID = strings.TrimSpace(jobID)
	if jobID == "" || strings.ContainsAny(jobID, "/?#") {
		return SourceUpdateJob{}, infraerrors.BadRequest("SOURCE_UPDATE_JOB_ID_INVALID", "source update job id is invalid")
	}
	return c.do(ctx, http.MethodGet, "http://sub2api-updater/v1/update/"+jobID, nil)
}

func (c *sourceUpdateAgentClient) do(ctx context.Context, method, target string, payload []byte) (SourceUpdateJob, error) {
	if c == nil || c.client == nil || c.socketPath == "" || c.token == "" {
		return SourceUpdateJob{}, ErrSourceUpdaterUnavailable
	}
	var body io.Reader
	if payload != nil {
		body = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, method, target, body)
	if err != nil {
		return SourceUpdateJob{}, ErrSourceUpdaterUnavailable.WithCause(err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Sub2API-Updater-Token", c.token)
	resp, err := c.client.Do(req)
	if err != nil {
		return SourceUpdateJob{}, ErrSourceUpdaterUnavailable.WithCause(err)
	}
	defer resp.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return SourceUpdateJob{}, ErrSourceUpdaterUnavailable.WithCause(err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		var remote struct {
			Error   string `json:"error"`
			Message string `json:"message"`
		}
		_ = json.Unmarshal(responseBody, &remote)
		message := strings.TrimSpace(remote.Message)
		if message == "" {
			message = strings.TrimSpace(remote.Error)
		}
		if message == "" {
			message = resp.Status
		}
		if resp.StatusCode == http.StatusConflict {
			return SourceUpdateJob{}, infraerrors.Conflict("SOURCE_UPDATE_BUSY", message)
		}
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			return SourceUpdateJob{}, ErrSourceUpdaterUnavailable.WithCause(errors.New(message))
		}
		return SourceUpdateJob{}, ErrSourceUpdaterUnavailable.WithCause(fmt.Errorf("updater returned %s: %s", resp.Status, message))
	}
	var job SourceUpdateJob
	if err := json.Unmarshal(responseBody, &job); err != nil {
		return SourceUpdateJob{}, ErrSourceUpdateInvalidResponse.WithCause(err)
	}
	if strings.TrimSpace(job.JobID) == "" {
		return SourceUpdateJob{}, ErrSourceUpdateInvalidResponse.WithCause(errors.New("updater job id is empty"))
	}
	return job, nil
}

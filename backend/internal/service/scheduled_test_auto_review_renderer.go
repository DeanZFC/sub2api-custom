package service

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"image/png"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"syscall"
	"time"
)

const scheduledTestReviewMaxHTMLBytes = 4 << 20

// Only fixed diagnostic codes may be persisted or logged. Browser responses and
// transport errors can contain generated HTML, local paths or credentials.
type scheduledTestRenderError struct {
	code       string
	diagnostic string
	httpStatus int
	cause      error
}

func (e *scheduledTestRenderError) Error() string { return e.code }
func (e *scheduledTestRenderError) Unwrap() error { return e.cause }
func (e *scheduledTestRenderError) retryable() bool {
	return e.diagnostic != "socket_permission_denied" && (e.code == "renderer_busy" || e.code == "renderer_unavailable" || e.code == "renderer_timeout")
}

func scheduledTestRenderFailure(err error) *scheduledTestRenderError {
	var failure *scheduledTestRenderError
	if errors.As(err, &failure) {
		return failure
	}
	return scheduledTestRenderTransportFailure(err)
}

func scheduledTestRenderTransportFailure(err error) *scheduledTestRenderError {
	failure := &scheduledTestRenderError{code: "renderer_render_failed", diagnostic: "unknown", cause: err}
	var netErr net.Error
	switch {
	case errors.Is(err, context.Canceled):
		failure.code, failure.diagnostic = "renderer_cancelled", "cancelled"
	case errors.Is(err, context.DeadlineExceeded), errors.As(err, &netErr) && netErr.Timeout():
		failure.code, failure.diagnostic = "renderer_timeout", "timeout"
	case errors.Is(err, os.ErrNotExist):
		failure.code, failure.diagnostic = "renderer_unavailable", "socket_not_found"
	case errors.Is(err, os.ErrPermission):
		failure.code, failure.diagnostic = "renderer_unavailable", "socket_permission_denied"
	case errors.Is(err, syscall.ECONNREFUSED):
		failure.code, failure.diagnostic = "renderer_unavailable", "connection_refused"
	case errors.Is(err, syscall.ECONNRESET), errors.Is(err, syscall.EPIPE), errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF):
		failure.code, failure.diagnostic = "renderer_unavailable", "connection_interrupted"
	case errors.As(err, &netErr):
		failure.code, failure.diagnostic = "renderer_unavailable", "transport_error"
	}
	return failure
}

func scheduledTestRenderHTTPFailure(status int, body io.Reader) *scheduledTestRenderError {
	failure := &scheduledTestRenderError{code: "renderer_invalid_response", diagnostic: "http_status", httpStatus: status}
	switch status {
	case http.StatusTooManyRequests:
		failure.code = "renderer_busy"
	case http.StatusRequestTimeout, http.StatusGatewayTimeout:
		failure.code = "renderer_timeout"
	case http.StatusBadGateway, http.StatusServiceUnavailable:
		failure.code = "renderer_unavailable"
	case http.StatusBadRequest, http.StatusRequestEntityTooLarge, http.StatusUnprocessableEntity:
		failure.code = "renderer_invalid_input"
	}
	// Rolling upgrades may still return the old message-only response. Never
	// display or log its free-form text, and do not trust unknown error codes.
	var response struct {
		Code string `json:"code"`
	}
	if json.NewDecoder(io.LimitReader(body, 4096)).Decode(&response) == nil {
		switch response.Code {
		case "renderer_busy", "renderer_unavailable", "renderer_timeout", "renderer_browser_unavailable", "renderer_render_failed", "renderer_invalid_input", "renderer_invalid_response", "renderer_cancelled":
			failure.code = response.Code
		}
	}
	return failure
}

type ScheduledTestReviewFrame struct {
	AtMS      int    `json:"at_ms"`
	PNGBase64 string `json:"png_base64"`
}

type ScheduledTestReviewRenderer interface {
	Render(context.Context, string) ([]ScheduledTestReviewFrame, error)
}

type scheduledTestReviewRenderer struct {
	client *http.Client
	sem    chan struct{}
}

// NewScheduledTestReviewRenderer connects only to the separate renderer's UNIX
// socket. Generated HTML never runs in the API process or its network namespace.
func NewScheduledTestReviewRenderer() ScheduledTestReviewRenderer {
	socket := strings.TrimSpace(os.Getenv("SCHEDULED_TEST_RENDERER_SOCKET"))
	if socket == "" {
		socket = "/app/review-renderer/renderer.sock"
	}
	transport := &http.Transport{
		Proxy: nil,
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{Timeout: 3 * time.Second}).DialContext(ctx, "unix", socket)
		},
		MaxConnsPerHost:     1,
		MaxIdleConnsPerHost: 1,
		// Chromium startup (10s) and rendering (20s) have separate deadlines.
		ResponseHeaderTimeout: 35 * time.Second,
		IdleConnTimeout:       30 * time.Second,
	}
	return &scheduledTestReviewRenderer{client: &http.Client{Transport: transport, Timeout: 40 * time.Second}, sem: make(chan struct{}, 1)}
}

func (r *scheduledTestReviewRenderer) Render(ctx context.Context, html string) ([]ScheduledTestReviewFrame, error) {
	if strings.TrimSpace(html) == "" || len(html) > scheduledTestReviewMaxHTMLBytes {
		return nil, &scheduledTestRenderError{code: "renderer_invalid_input", diagnostic: "html_size"}
	}
	if err := ctx.Err(); err != nil {
		return nil, scheduledTestRenderTransportFailure(err)
	}
	select {
	case r.sem <- struct{}{}:
		defer func() { <-r.sem }()
	case <-ctx.Done():
		return nil, scheduledTestRenderTransportFailure(ctx.Err())
	}
	payload, err := json.Marshal(struct {
		HTML string `json:"html"`
	}{html})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://renderer/render", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	response, err := r.client.Do(req)
	if err != nil {
		return nil, scheduledTestRenderTransportFailure(err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return nil, scheduledTestRenderHTTPFailure(response.StatusCode, response.Body)
	}
	const maxResponseBytes = 12 << 20
	raw, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil {
		return nil, scheduledTestRenderTransportFailure(err)
	}
	if len(raw) > maxResponseBytes {
		return nil, &scheduledTestRenderError{code: "renderer_invalid_response", diagnostic: "response_size"}
	}
	var output struct {
		Frames []ScheduledTestReviewFrame `json:"frames"`
	}
	if err = json.Unmarshal(raw, &output); err != nil || len(output.Frames) != 4 {
		return nil, &scheduledTestRenderError{code: "renderer_invalid_response", diagnostic: "frame_count"}
	}
	frameTimes := [...]int{0, 700, 1700, 2900}
	for i, frame := range output.Frames {
		if frame.AtMS != frameTimes[i] || len(frame.PNGBase64) > 3<<20 {
			return nil, &scheduledTestRenderError{code: "renderer_invalid_response", diagnostic: "frame_timestamp_or_size"}
		}
		data, err := base64.StdEncoding.DecodeString(frame.PNGBase64)
		if err != nil || len(data) > 2<<20 {
			return nil, &scheduledTestRenderError{code: "renderer_invalid_response", diagnostic: "png_encoding_or_size"}
		}
		config, err := png.DecodeConfig(bytes.NewReader(data))
		if err != nil || config.Width != 960 || config.Height != 720 {
			return nil, &scheduledTestRenderError{code: "renderer_invalid_response", diagnostic: "png_dimensions"}
		}
	}
	return output.Frames, nil
}

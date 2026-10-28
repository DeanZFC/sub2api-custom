package service

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image/png"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"time"
)

const scheduledTestReviewMaxHTMLBytes = 4 << 20

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
		MaxConnsPerHost:       1,
		MaxIdleConnsPerHost:   1,
		ResponseHeaderTimeout: 25 * time.Second,
		IdleConnTimeout:       30 * time.Second,
	}
	return &scheduledTestReviewRenderer{client: &http.Client{Transport: transport, Timeout: 30 * time.Second}, sem: make(chan struct{}, 1)}
}

func (r *scheduledTestReviewRenderer) Render(ctx context.Context, html string) ([]ScheduledTestReviewFrame, error) {
	if strings.TrimSpace(html) == "" || len(html) > scheduledTestReviewMaxHTMLBytes {
		return nil, fmt.Errorf("HTML renderer requires 1..4 MiB of content")
	}
	select {
	case r.sem <- struct{}{}:
		defer func() { <-r.sem }()
	case <-ctx.Done():
		return nil, ctx.Err()
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
		return nil, fmt.Errorf("isolated renderer unavailable")
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("isolated renderer returned HTTP %d", response.StatusCode)
	}
	const maxResponseBytes = 12 << 20
	raw, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil || len(raw) > maxResponseBytes {
		return nil, fmt.Errorf("invalid isolated renderer response size")
	}
	var output struct {
		Frames []ScheduledTestReviewFrame `json:"frames"`
	}
	if err = json.Unmarshal(raw, &output); err != nil || len(output.Frames) != 4 {
		return nil, fmt.Errorf("isolated renderer must return four frames")
	}
	frameTimes := [...]int{0, 700, 1700, 2900}
	for i, frame := range output.Frames {
		if frame.AtMS != frameTimes[i] || len(frame.PNGBase64) > 3<<20 {
			return nil, fmt.Errorf("invalid isolated renderer frame")
		}
		data, err := base64.StdEncoding.DecodeString(frame.PNGBase64)
		if err != nil || len(data) > 2<<20 {
			return nil, fmt.Errorf("invalid isolated renderer PNG")
		}
		config, err := png.DecodeConfig(bytes.NewReader(data))
		if err != nil || config.Width != 960 || config.Height != 720 {
			return nil, fmt.Errorf("invalid isolated renderer image dimensions")
		}
	}
	return output.Frames, nil
}

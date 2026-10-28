package service

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
)

func TestScheduledTestReviewRendererUnixProtocol(t *testing.T) {
	var pngData bytes.Buffer
	if err := png.Encode(&pngData, image.NewRGBA(image.Rect(0, 0, 960, 720))); err != nil {
		t.Fatal(err)
	}
	encoded := base64.StdEncoding.EncodeToString(pngData.Bytes())
	// macOS limits UNIX socket paths to a short sockaddr_un.sun_path.
	socket := filepath.Join(os.TempDir(), fmt.Sprintf("renderer-%d.sock", os.Getpid()))
	_ = os.Remove(socket)
	t.Cleanup(func() { _ = os.Remove(socket) })
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	var corrupt atomic.Bool
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/render" || r.Method != http.MethodPost {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		var input struct {
			HTML string `json:"html"`
		}
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil || input.HTML != "<svg/>" {
			t.Errorf("unexpected input: %+v, %v", input, err)
		}
		frames := make([]ScheduledTestReviewFrame, 4)
		frameTimes := [...]int{0, 700, 1700, 2900}
		for i := range frames {
			frames[i] = ScheduledTestReviewFrame{AtMS: frameTimes[i], PNGBase64: encoded}
		}
		if corrupt.Load() {
			frames[1].AtMS = 0
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"frames": frames})
	})}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Close() })
	t.Setenv("SCHEDULED_TEST_RENDERER_SOCKET", socket)
	renderer := NewScheduledTestReviewRenderer()
	frames, err := renderer.Render(context.Background(), "<svg/>")
	if err != nil || len(frames) != 4 {
		t.Fatalf("frames=%d err=%v", len(frames), err)
	}
	corrupt.Store(true)
	if _, err = renderer.Render(context.Background(), "<svg/>"); err == nil {
		t.Fatal("accepted repeated frame timestamp")
	}
}

func TestScheduledTestReviewRendererCancellation(t *testing.T) {
	renderer := &scheduledTestReviewRenderer{sem: make(chan struct{}, 1)}
	renderer.sem <- struct{}{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := renderer.Render(ctx, "<svg/>"); err == nil {
		t.Fatal("cancelled queued render should not run")
	}
}

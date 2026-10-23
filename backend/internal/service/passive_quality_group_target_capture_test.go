package service

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestGroupQualityTargetFallbackAttemptUsesEffectiveGroup(t *testing.T) {
	s := NewPassiveQualityService(nil, nil)
	defer s.cancel()
	s.enabled.Store(true)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/messages", nil)
	c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), ctxkey.Model, "model"))
	groupID := int64(8)
	key := &APIKey{GroupID: &groupID}
	c.Set("api_key", key)
	s.Capture()(c)
	SetPassiveQualityAttemptGroup(c, 9)
	appendOpsUpstreamError(c, OpsUpstreamErrorEvent{AccountID: 62, UpstreamStatusCode: 503, Kind: "http_error"})
	require.Equal(t, int64(9), (<-s.queue).GroupID)
	require.Equal(t, int64(8), *key.GroupID, "routing telemetry must not mutate authenticated billing identity")
}

func TestGroupQualityTargetFailureMediaAttribution(t *testing.T) {
	for _, tc := range []struct {
		name, inbound, upstream string
		nonText                 bool
	}{
		{"text", "/v1/responses", "https://images.example.com/v1/responses", false},
		{"image", "/v1/images/generations", "", true},
		{"video", "/v1/videos", "", true},
		{"audio", "/v1/audio/speech", "", true},
		{"upstream_media", "/v1/responses", "https://api.example.com/v1/images/edits", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := NewPassiveQualityService(nil, nil)
			defer s.cancel()
			s.enabled.Store(true)
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest("POST", tc.inbound, nil)
			c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), ctxkey.Model, "model"))
			s.Capture()(c)
			appendOpsUpstreamError(c, OpsUpstreamErrorEvent{AccountID: 62, UpstreamStatusCode: 503, Kind: "http_error", UpstreamURL: tc.upstream})
			require.Equal(t, tc.nonText, (<-s.queue).NonText)
		})
	}
}

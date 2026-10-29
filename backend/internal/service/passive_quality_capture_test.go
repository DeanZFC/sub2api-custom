package service

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type passiveQualityCaptureRepo struct {
	PassiveQualityRepository
	policies []PassiveQualityPolicy
	err      error
}

func (r *passiveQualityCaptureRepo) ListPolicies(context.Context) ([]PassiveQualityPolicy, error) {
	return r.policies, r.err
}
func (r *passiveQualityCaptureRepo) Sweep(context.Context, time.Time, int) ([]PassiveQualityState, error) {
	return nil, nil
}
func (r *passiveQualityCaptureRepo) RecordTelemetry(context.Context, []PassiveQualityFailure, []PassiveQualityTelemetryGap) error {
	return nil
}
func (r *passiveQualityCaptureRepo) PrunePassiveQuality(context.Context) error {
	return nil
}

func TestPassiveQualityTelemetryCapturesWithoutProtection(t *testing.T) {
	for _, tc := range []struct {
		name     string
		policies []PassiveQualityPolicy
		err      error
	}{
		{name: "no policies"},
		{name: "disabled policy", policies: []PassiveQualityPolicy{{PassiveQualityConfig: PassiveQualityConfig{Enabled: false}}}},
		{name: "configuration unavailable", err: errors.New("database unavailable")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := &passiveQualityCaptureRepo{policies: tc.policies, err: tc.err}
			s := NewPassiveQualityService(repo, nil)
			defer s.cancel()
			s.sweepCycle()
			require.True(t, s.enabled.Load())
			require.True(t, s.cacheEnabled.Load())
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest("POST", "/v1/responses", nil)
			c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), ctxkey.Model, "public-model"))
			s.Capture()(c)
			appendOpsUpstreamError(c, OpsUpstreamErrorEvent{AccountID: 62, UpstreamStatusCode: 503, Kind: "http_error"})
			require.Len(t, s.queue, 1)
			failure := <-s.queue
			require.EqualValues(t, 62, failure.AccountID)
			recordPassiveQualityCache(c.Request.Context(), 62, "usage-id", true)
			require.Len(t, s.queue, 1)
			cache := <-s.queue
			require.True(t, cache.CacheReported)
			require.NotEmpty(t, cache.AttemptKey)
			require.Equal(t, failure.StartedAt, cache.StartedAt)
		})
	}
}

func TestPassiveQualityStartSurvivesAsyncUsageAcrossTurns(t *testing.T) {
	for _, websocket := range []bool{false, true} {
		name := "http"
		if websocket {
			name = "websocket"
		}
		t.Run(name, func(t *testing.T) {
			s := NewPassiveQualityService(nil, nil)
			defer s.cancel()
			s.ConfigurationChanged()
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest("POST", "/v1/responses", nil)
			if websocket {
				c.Request.Header.Set("Upgrade", "websocket")
			}
			s.Capture()(c)
			firstStart, ok := c.Request.Context().Value(passiveQualityUsageStartedKey{}).(time.Time)
			require.True(t, ok)
			require.False(t, firstStart.IsZero())
			firstKey := c.Request.Context().Value(passiveQualityUsageTurnKey{})
			worker := PassiveQualityUsageContext(c.Request.Context(), context.Background())

			// A queued billing worker may finish after the next WS turn starts.
			// It must retain its original start even without cache metadata.
			PassiveQualityBeginTurn(c)
			require.NotEqual(t, firstKey, c.Request.Context().Value(passiveQualityUsageTurnKey{}))
			recordPassiveQualityCache(worker, 62, "first-usage", false)
			require.Len(t, s.queue, 1)
			first := <-s.queue
			require.Equal(t, firstKey, first.AttemptKey)
			require.Equal(t, firstStart, first.StartedAt)
			require.Nil(t, first.CacheMetadata)
			require.False(t, first.CacheReported)
			require.False(t, first.At.Before(first.StartedAt))

			secondStart, ok := c.Request.Context().Value(passiveQualityUsageStartedKey{}).(time.Time)
			require.True(t, ok)
			recordPassiveQualityCache(c.Request.Context(), 62, "second-usage", false)
			second := <-s.queue
			require.Equal(t, secondStart, second.StartedAt)
			require.NotEqual(t, first.AttemptKey, second.AttemptKey)
		})
	}
}

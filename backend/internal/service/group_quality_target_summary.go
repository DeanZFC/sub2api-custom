package service

import (
	"context"
	"time"
)

// GroupQualityTargetSummary is a live rolling-window measurement. It must not
// reuse the frozen evidence retained by a paused account's protection state.
type GroupQualityTargetSummary struct {
	SnapshotReady         bool                         `json:"snapshot_ready"`
	Stale                 bool                         `json:"stale"`
	WindowStartedAt       time.Time                    `json:"window_started_at"`
	Contributors          []GroupQualityContributor    `json:"contributors"`
	ContributorTotal      int                          `json:"contributor_total"`
	ContributorsTruncated bool                         `json:"contributors_truncated"`
	TotalInputTokens      int64                        `json:"total_input_tokens"`
	Fallback              *GroupQualityFallbackSummary `json:"fallback,omitempty"`
	TrafficControl        *GroupQualityTrafficSummary  `json:"traffic_control,omitempty"`
	PolicyID              int64                        `json:"policy_id"`
	GroupID               int64                        `json:"group_id"`
	WindowSeconds         int                          `json:"window_seconds"`
	ActualCacheRate       *float64                     `json:"actual_cache_rate"`
	CacheSamples          int64                        `json:"cache_samples"`
	CacheReadTokens       int64                        `json:"cache_read_tokens"`
	CacheInputTokens      int64                        `json:"cache_input_tokens"`
	CacheUnknownSamples   int64                        `json:"cache_unknown_samples"`
	ActualAvailability    *float64                     `json:"actual_availability"`
	SuccessSamples        int64                        `json:"success_samples"`
	FailureSamples        int64                        `json:"failure_samples"`
	FailureUnknownSamples int64                        `json:"failure_unknown_samples"`
	TTFTP90MS             *float64                     `json:"ttft_p90_ms"`
	TTFTSamples           int64                        `json:"ttft_samples"`
	TelemetryComplete     bool                         `json:"telemetry_complete"`
	GeneratedAt           time.Time                    `json:"generated_at"`
}

// The emergency lane remains independent of cache-budget configuration.
// Historical counters cover the rolling hour and exclude positively unused leases.
type GroupQualityFallbackSummary struct {
	Enabled             bool       `json:"enabled"`
	InFlight            int        `json:"in_flight"`
	Requests            int64      `json:"requests"`
	InputTokens         int64      `json:"input_tokens"`
	ReservedInputTokens int64      `json:"reserved_input_tokens"`
	LeaseSeconds        int        `json:"lease_seconds"`
	PacingSeconds       int        `json:"pacing_seconds"` // Compatibility field; zero means no fallback pacing.
	LastUsedAt          *time.Time `json:"last_used_at,omitempty"`
	LastAccountID       *int64     `json:"last_account_id,omitempty"`
}

// Kept separate from the protection interface so existing collectors and
// admission implementations need no reporting-only methods.
type GroupQualityTargetSummaryRepository interface {
	GroupQualityTargetSummaries(context.Context, time.Time) ([]GroupQualityTargetSummary, error)
}

// An exact admitted attempt is required to attribute the routing category.
type GroupQualityContributor struct {
	AccountID           int64    `json:"account_id"`
	AccountName         string   `json:"account_name"`
	Model               string   `json:"model"`
	TrafficKind         string   `json:"traffic_kind"`
	SuccessSamples      int64    `json:"success_samples"`
	FailureSamples      int64    `json:"failure_samples"`
	CacheSamples        int64    `json:"cache_samples"`
	CacheUnknownSamples int64    `json:"cache_unknown_samples"`
	CacheInputTokens    int64    `json:"cache_input_tokens"`
	CacheReadTokens     int64    `json:"cache_read_tokens"`
	UncachedTokens      int64    `json:"uncached_tokens"`
	TTFTSamples         int64    `json:"ttft_samples"`
	SlowRequests        int64    `json:"slow_requests"`
	TTFTP90MS           *float64 `json:"ttft_p90_ms"`
}

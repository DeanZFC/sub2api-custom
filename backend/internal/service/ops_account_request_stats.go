package service

import (
	"context"
	"strconv"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

// OpsAccountRequestStats covers all recorded successful requests in one rolling
// hour. Nullable metrics distinguish unavailable telemetry from measured zero.
type OpsAccountRequestStats struct {
	WindowStart       time.Time `json:"window_start"`
	WindowEnd         time.Time `json:"window_end"`
	CacheRate         *float64  `json:"cache_rate"`
	AvgFirstTokenMs   *float64  `json:"avg_first_token_ms"`
	AvgDurationMs     *float64  `json:"avg_duration_ms"`
	CacheSamples      int64     `json:"cache_samples"`
	FirstTokenSamples int64     `json:"first_token_samples"`
	DurationSamples   int64     `json:"duration_samples"`
}

type OpsAccountRequestStatsBatch struct {
	Stats map[string]*OpsAccountRequestStats `json:"stats"`
}

func (s *OpsService) GetAccountRequestStatsBatch(ctx context.Context, accountIDs []int64) (*OpsAccountRequestStatsBatch, error) {
	if err := s.RequireMonitoringEnabled(ctx); err != nil {
		return nil, err
	}
	if len(accountIDs) == 0 || len(accountIDs) > 100 {
		return nil, infraerrors.BadRequest("OPS_ACCOUNT_IDS_INVALID", "account_ids must contain between 1 and 100 positive IDs")
	}
	ids := make([]int64, 0, len(accountIDs))
	seen := make(map[int64]struct{}, len(accountIDs))
	for _, id := range accountIDs {
		if id <= 0 {
			return nil, infraerrors.BadRequest("OPS_ACCOUNT_IDS_INVALID", "account_ids must contain positive IDs")
		}
		if _, exists := seen[id]; !exists {
			seen[id] = struct{}{}
			ids = append(ids, id)
		}
	}
	if s.opsRepo == nil {
		return nil, infraerrors.ServiceUnavailable("OPS_REPO_UNAVAILABLE", "Ops repository not available")
	}

	// Capture one server timestamp for every account in this response.
	end := time.Now().UTC()
	start := end.Add(-time.Hour)
	stats, err := s.opsRepo.GetAccountRequestStats(ctx, ids, start, end)
	if err != nil {
		return nil, err
	}
	result := &OpsAccountRequestStatsBatch{Stats: make(map[string]*OpsAccountRequestStats, len(ids))}
	for _, id := range ids {
		key := strconv.FormatInt(id, 10)
		entry := stats[key]
		if entry == nil {
			entry = &OpsAccountRequestStats{}
		}
		entry.WindowStart, entry.WindowEnd = start, end
		result.Stats[key] = entry
	}
	return result, nil
}

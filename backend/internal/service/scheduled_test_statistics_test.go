package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

type statisticsReadResultsStub struct{ scheduledTestReadResultsStub }

func (r statisticsReadResultsStub) ListVisibleHistory(context.Context, int64, int64, int64, int) ([]*ScheduledTestResult, error) {
	return r.rows, nil
}

func TestScheduledTestStatisticsReadExposesOnlyTypedSnapshot(t *testing.T) {
	for _, view := range []string{"admin", "user", "history"} {
		t.Run(view, func(t *testing.T) {
			oldNumeric := 99.0
			rows := []*ScheduledTestResult{
				{ID: 11, OutputKind: " statistics ", Status: "success", ReasoningEffort: "high", OutputHTML: "secret-html", OutputNumeric: &oldNumeric,
					ResponseText: `{"window_start":"2026-09-21T10:00:00Z","window_end":"2026-09-21T11:00:00Z","total_requests":10,"success_requests":8,"failed_requests":2,"success_rate":0.8,"cache_rate":0,"avg_first_token_ms":120,"first_token_samples":8,"output_tps":42.5,"tps_samples":6,"cache_read_tokens":0,"cache_input_tokens":100,"credentials":"secret-credential","account_name":"secret-name","recent_requests":[{"success":false,"created_at":"2026-09-21T10:59:00Z","credentials":"secret-recent-credential","request_id":"secret-request","user_email":"secret-user"},{"success":true,"created_at":"2026-09-21T10:58:00Z","user_id":12345}]}`},
				{ID: 10, OutputKind: "statistics", Status: "success", ResponseText: `not valid JSON secret-invalid`, OutputStatistics: &ScheduledTestStatistics{TotalRequests: 99}},
				{ID: 9, OutputKind: "statistics", Status: "success", ResponseText: `{"window_start":"2026-09-21T12:00:00Z","window_end":"2026-09-21T11:00:00Z","private":"secret-bad-window"}`},
				{ID: 8, OutputKind: "text", Status: "success", ResponseText: "ordinary output"},
				{ID: 7, OutputKind: "statistics", Status: "success", ResponseText: `{"window_start":"2026-09-21T10:00:00Z","window_end":"2026-09-21T11:00:00Z"}`},
			}
			svc := NewScheduledTestService(nil, statisticsReadResultsStub{scheduledTestReadResultsStub{rows: rows}})
			var results []*ScheduledTestResult
			var err error
			switch view {
			case "admin":
				results, err = svc.ListResults(context.Background(), 7, 20)
			case "user":
				results, err = svc.ListVisibleResults(context.Background(), 1, 20)
			case "history":
				var history *ScheduledTestResultHistory
				history, err = svc.ListVisibleResultHistory(context.Background(), 1, 11, 0, 20)
				if history != nil {
					results = history.Items
				}
			}
			require.NoError(t, err)
			require.Len(t, results, 5)
			snapshot := results[0].OutputStatistics
			require.NotNil(t, snapshot)
			require.Equal(t, int64(10), snapshot.TotalRequests)
			require.Equal(t, 0.8, *snapshot.SuccessRate)
			require.NotNil(t, snapshot.CacheRate, "zero cache hits must remain a measured zero")
			require.Zero(t, *snapshot.CacheRate)
			require.Equal(t, 120.0, *snapshot.AvgFirstTokenMs)
			require.Equal(t, 42.5, *snapshot.OutputTPS)
			require.EqualValues(t, 6, snapshot.TPSSamples)
			require.Len(t, snapshot.RecentRequests, 2)
			require.False(t, snapshot.RecentRequests[0].Success)
			require.True(t, snapshot.RecentRequests[1].Success)
			require.True(t, snapshot.RecentRequests[0].CreatedAt.After(snapshot.RecentRequests[1].CreatedAt))
			for _, result := range results[:3] {
				require.Empty(t, result.ResponseText)
				require.Empty(t, result.OutputHTML)
				require.Nil(t, result.OutputNumeric)
				require.Empty(t, result.ReasoningEffort)
			}
			require.Nil(t, results[1].OutputStatistics)
			require.Nil(t, results[2].OutputStatistics)
			require.Equal(t, "ordinary output", results[3].ResponseText)
			require.NotNil(t, results[4].OutputStatistics)
			require.NotNil(t, results[4].OutputStatistics.RecentRequests, "old snapshots without recent requests must return an array")
			require.Empty(t, results[4].OutputStatistics.RecentRequests)
			encoded, marshalErr := json.Marshal(results)
			require.NoError(t, marshalErr)
			require.NotContains(t, string(encoded), "secret-")
			require.NotContains(t, string(encoded), "credentials")
			require.NotContains(t, string(encoded), "request_id")
			require.NotContains(t, string(encoded), "user_email")
			require.NotContains(t, string(encoded), "user_id")
			var publicRows []struct {
				OutputStatistics struct {
					RecentRequests []map[string]json.RawMessage `json:"recent_requests"`
				} `json:"output_statistics"`
			}
			require.NoError(t, json.Unmarshal(encoded, &publicRows))
			for _, recent := range publicRows[0].OutputStatistics.RecentRequests {
				require.Len(t, recent, 2, "recent request entries must only expose the public allowlist")
				require.Contains(t, recent, "success")
				require.Contains(t, recent, "created_at")
			}
			require.NotNil(t, publicRows[4].OutputStatistics.RecentRequests, "legacy snapshots must serialize recent_requests as [], not null")
		})
	}
}

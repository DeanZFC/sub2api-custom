package repository

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestChannelMonitorV2RecentRequestsScopeAndModelMapping(t *testing.T) {
	groupID := int64(7)
	start := time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC)
	filter := service.ChannelMonitorV2Filter{Start: start, End: start.Add(time.Hour), RestrictGroups: true, AllowedGroupIDs: []int64{7}, GroupIDs: []int64{7, 9}, Models: []string{"actual-unlisted"}}
	cfg := service.ChannelMonitorV2Config{Platforms: []service.ChannelMonitorV2PlatformConfig{{Platform: "openai", Enabled: true, Models: []string{"gpt-named"}}}}
	query, args, err := channelMonitorV2RecentQuery(filter, cfg, []service.ChannelMonitorV2MatrixRow{{Platform: "openai", GroupID: &groupID}})
	require.NoError(t, err)
	// The same server-derived scope applies to BOTH sources, including platform views.
	require.Contains(t, query, "ul.group_id = ANY($5)")
	require.Contains(t, query, "current_error.group_id = ANY($5)")
	require.Contains(t, query, "ul.actual_cost > 0")
	require.Contains(t, query, "NOT current_error.is_count_tokens")
	require.Equal(t, 3, strings.Count(query, "LIMIT 20"))
	require.Contains(t, query, "succeeded.request_id = current_error.request_id")
	require.NotContains(t, query, "__other__")
	require.Contains(t, query, "ANY($6)")
	require.Contains(t, query, channelMonitorV2ModelSQL)
	require.NotContains(t, query, "gpt-named") // model names must be bound, never interpolated
	require.Equal(t, start, args[0])
	var dimensions []channelMonitorV2RecentDimension
	require.NoError(t, json.Unmarshal([]byte(args[3].(string)), &dimensions))
	require.Equal(t, &groupID, dimensions[0].GroupID)
	require.Nil(t, dimensions[0].Model)
}

func TestChannelMonitorV2RecentRequestsClassifiesWithoutExposingPrivateFields(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	now := time.Now().UTC()
	rows := sqlmock.NewRows([]string{"idx", "created_at", "model", "is_error", "ttft", "type", "owner", "source", "status", "upstream_status", "message"}).
		AddRow(0, now, "gpt-test", false, 1000, "", "", "", 0, 0, "").
		AddRow(0, now.Add(time.Second), "gpt-test", false, 15000, "", "", "", 0, 0, "").
		AddRow(0, now.Add(2*time.Second), "gpt-test", true, nil, "upstream", "provider", "", 503, 503, "private message body").
		AddRow(1, now, "grok-test", true, nil, "authentication", "client", "", 401, 0, "private API key text")
	mock.ExpectQuery("SELECT d.idx, recent.created_at").WillReturnRows(rows)
	cfg := service.ChannelMonitorV2Config{HealthThresholds: service.ChannelMonitorV2HealthThresholds{WarningTTFTMs: 10000}, IgnoredErrorCategories: []string{"authentication"}}
	items := []service.ChannelMonitorV2MatrixRow{{Platform: "openai"}, {Platform: "grok"}}
	repo := &channelMonitorV2Repository{db: db}
	require.NoError(t, repo.loadChannelMonitorV2RecentRequests(context.Background(), service.ChannelMonitorV2Filter{}, cfg, items))
	require.Len(t, items[0].RecentRequests, 3)
	require.Equal(t, "success", items[0].RecentRequests[0].Status)
	require.Equal(t, "slow", items[0].RecentRequests[1].Status)
	require.Equal(t, "error", items[0].RecentRequests[2].Status)
	require.Equal(t, "upstream_5xx", items[0].RecentRequests[2].ErrorCategory)
	require.Equal(t, "ignored", items[1].RecentRequests[0].Status)
	require.Nil(t, items[0].RecentRequests[2].TTFTMs)
	encoded, err := json.Marshal(items)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "private")
	require.NotContains(t, string(encoded), "request_id")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestChannelMonitorV2RecentRequestsEmptyViewerDoesNotReadLogs(t *testing.T) {
	repo := &channelMonitorV2Repository{}
	require.NoError(t, repo.loadChannelMonitorV2RecentRequests(context.Background(), service.ChannelMonitorV2Filter{RestrictGroups: true}, service.ChannelMonitorV2Config{}, []service.ChannelMonitorV2MatrixRow{{Platform: "openai"}}))
}

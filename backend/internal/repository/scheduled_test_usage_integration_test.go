//go:build integration

package repository

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

func TestScheduledTestUsageIntegration(t *testing.T) {
	ctx := context.Background()
	suffix := time.Now().UnixNano()
	// The recorder commits its own transactions and the monitor updates shared
	// rollups. Run both against an isolated database within the test container.
	name := fmt.Sprintf("scheduled_usage_%d", suffix)
	_, err := integrationDB.ExecContext(ctx, "CREATE DATABASE "+pq.QuoteIdentifier(name))
	require.NoError(t, err)
	t.Cleanup(func() {
		_, err := integrationDB.ExecContext(ctx, "DROP DATABASE "+pq.QuoteIdentifier(name))
		require.NoError(t, err)
	})
	dsn, err := url.Parse(integrationDatabaseDSN)
	require.NoError(t, err)
	dsn.Path = "/" + name
	db, err := sql.Open("postgres", dsn.String())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	require.NoError(t, ApplyMigrations(ctx, db))
	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	account := mustCreateAccount(t, client, &service.Account{Name: fmt.Sprintf("quality-usage-%d", suffix), Platform: service.PlatformOpenAI, Status: service.StatusActive})
	first := mustCreateGroup(t, client, &service.Group{Name: fmt.Sprintf("quality-first-%d", suffix), Platform: service.PlatformOpenAI})
	second := mustCreateGroup(t, client, &service.Group{Name: fmt.Sprintf("quality-second-%d", suffix), Platform: service.PlatformOpenAI})
	repo := &qualityOperationsRepository{db: db}
	model := fmt.Sprintf("quality-model-%d", suffix)
	now := time.Now().UTC().Truncate(time.Second)
	input, output, cached := int64(100), int64(10), int64(40)
	for _, status := range []int{200, 502} {
		reservation, err := repo.ReserveQualityProbe(ctx, account.ID, 100, false, "")
		require.NoError(t, err)
		usage := service.ScheduledTestUsage{ReservationID: reservation.ID, AccountID: account.ID, Platform: service.PlatformOpenAI,
			GroupIDs: []int64{first.ID, second.ID}, Model: model, Stream: true, InputTokens: 60,
			Attempt: service.ScheduledTestRequestAttempt{StartedAt: now, HTTPStatus: status, ScheduledTestTokenUsage: service.ScheduledTestTokenUsage{InputTokens: &input, OutputTokens: &output, CacheReadTokens: &cached}}}
		require.NoError(t, repo.RecordScheduledTestUsage(ctx, usage))
		require.NoError(t, repo.RecordScheduledTestUsage(ctx, usage))
	}
	var count, failures int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT count(*) FROM usage_logs WHERE account_id=$1 AND model=$2 AND user_id IS NULL AND api_key_id IS NULL AND actual_cost=0`, account.ID, model).Scan(&count))
	require.Equal(t, 2, count)
	require.NoError(t, db.QueryRowContext(ctx, `SELECT count(*) FROM ops_error_logs WHERE account_id=$1 AND model=$2`, account.ID, model).Scan(&failures))
	require.Equal(t, 1, failures)
	var id int64
	require.NoError(t, db.QueryRowContext(ctx, `SELECT id FROM usage_logs WHERE account_id=$1 AND model=$2 ORDER BY id LIMIT 1`, account.ID, model).Scan(&id))
	log, err := newUsageLogRepositoryWithSQL(client, db).GetByID(ctx, id)
	require.NoError(t, err)
	require.NotNil(t, log.QualityUsage)
	require.Equal(t, input, *log.QualityUsage.InputTokens)
	require.Nil(t, log.QualityUsage.CacheCreationTokens)

	monitor := &channelMonitorV2Repository{db: db}
	start := now.Add(-time.Minute).Truncate(time.Minute)
	end := now.Add(2 * time.Minute).Truncate(time.Minute)
	require.NoError(t, monitor.RecomputeRange(ctx, start, end))
	cfg := service.ChannelMonitorV2Config{Platforms: []service.ChannelMonitorV2PlatformConfig{{Platform: "openai", Enabled: true}}}
	filter := service.ChannelMonitorV2Filter{Start: start, End: end, Bucket: time.Minute, RestrictGroups: true, AllowedGroupIDs: []int64{first.ID, second.ID}, Models: []string{model}, IncludeRecentRequests: true}
	matrix, err := monitor.GetMatrix(ctx, filter, cfg, service.ChannelMonitorV2GroupByPlatformGroupModel, true)
	require.NoError(t, err)
	require.Len(t, matrix.Items, 2)
	for _, item := range matrix.Items {
		require.EqualValues(t, 1, item.Metrics.SuccessRequests)
		require.EqualValues(t, 1, item.Metrics.ErrorRequests)
		require.Len(t, item.RecentRequests, 2)
	}
	platform, err := monitor.GetMatrix(ctx, filter, cfg, service.ChannelMonitorV2GroupByPlatform, true)
	require.NoError(t, err)
	require.Len(t, platform.Items, 1)
	require.EqualValues(t, 1, platform.Items[0].Metrics.SuccessRequests)
	require.EqualValues(t, 1, platform.Items[0].Metrics.ErrorRequests)
	require.Len(t, platform.Items[0].RecentRequests, 2)
	filter.AllowedGroupIDs = []int64{second.ID}
	matrix, err = monitor.GetMatrix(ctx, filter, cfg, service.ChannelMonitorV2GroupByPlatformGroupModel, true)
	require.NoError(t, err)
	require.Len(t, matrix.Items, 1)
	require.EqualValues(t, 1, matrix.Items[0].Metrics.SuccessRequests)
	require.EqualValues(t, 1, matrix.Items[0].Metrics.ErrorRequests)
}

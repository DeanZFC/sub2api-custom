//go:build integration

package repository

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestOpsRequestDetails_UsageAndIdentity(t *testing.T) {
	ctx := context.Background()
	repo := NewOpsRepository(integrationDB).(*opsRepository)
	user := &service.User{Email: uuid.NewString() + "@example.com"}
	group := &service.Group{Name: "recent-" + uuid.NewString()}
	account := &service.Account{Name: "recent-account-" + uuid.NewString()}
	key := &service.APIKey{Name: "Recent requests key"}
	require.NoError(t, integrationDB.QueryRowContext(ctx, `INSERT INTO users (email, password_hash) VALUES ($1, 'test-only') RETURNING id`, user.Email).Scan(&user.ID))
	require.NoError(t, integrationDB.QueryRowContext(ctx, `INSERT INTO groups (name) VALUES ($1) RETURNING id`, group.Name).Scan(&group.ID))
	require.NoError(t, integrationDB.QueryRowContext(ctx, `INSERT INTO accounts (name, platform, type) VALUES ($1, 'openai', 'oauth') RETURNING id`, account.Name).Scan(&account.ID))
	require.NoError(t, integrationDB.QueryRowContext(ctx, `INSERT INTO api_keys (user_id, key, name) VALUES ($1, $2, $3) RETURNING id`, user.ID, "sk-"+uuid.NewString(), key.Name).Scan(&key.ID))
	start := time.Now().UTC().Add(-time.Minute)
	end := start.Add(time.Hour)
	firstToken, duration := 3060, 38660
	multiplier, statsCost := 1.5, 0.2
	usage := &service.UsageLog{
		UserID: user.ID, APIKeyID: key.ID, AccountID: account.ID, GroupID: &group.ID,
		RequestID: uuid.NewString(), Model: "gpt-test", Stream: true, OpenAIWSMode: true,
		InputTokens: 100, OutputTokens: 10, CacheReadTokens: 800, CacheCreationTokens: 100,
		FirstTokenMs: &firstToken, DurationMs: &duration,
		TotalCost: 0.1, ActualCost: 0, AccountRateMultiplier: &multiplier, AccountStatsCost: &statsCost,
		CreatedAt: start.Add(time.Second),
	}
	_, err := integrationDB.ExecContext(ctx, `
		INSERT INTO usage_logs (user_id, api_key_id, account_id, group_id, request_id, model, stream, openai_ws_mode,
		input_tokens, output_tokens, cache_read_tokens, cache_creation_tokens, first_token_ms, duration_ms,
		total_cost, actual_cost, account_rate_multiplier, account_stats_cost, created_at, request_type)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,0)`,
		usage.UserID, usage.APIKeyID, usage.AccountID, usage.GroupID, usage.RequestID, usage.Model, usage.Stream, usage.OpenAIWSMode,
		usage.InputTokens, usage.OutputTokens, usage.CacheReadTokens, usage.CacheCreationTokens, usage.FirstTokenMs, usage.DurationMs,
		usage.TotalCost, usage.ActualCost, usage.AccountRateMultiplier, usage.AccountStatsCost, usage.CreatedAt)
	require.NoError(t, err)
	// Same request ID: an error attempt must never inherit successful usage.
	_, err = repo.InsertErrorLog(ctx, &service.OpsInsertErrorLogInput{
		RequestID: usage.RequestID, UserID: &user.ID, GroupID: &group.ID, AccountID: &account.ID,
		APIKeyID: &key.ID, Stream: true, Model: "gpt-test", ErrorPhase: "upstream",
		ErrorType: "upstream_error", Severity: "error", StatusCode: 502, ErrorMessage: "upstream failed",
		CreatedAt: start.Add(2 * time.Second),
	})
	require.NoError(t, err)
	filter := &service.OpsRequestDetailFilter{StartTime: &start, EndTime: &end, AccountID: &account.ID, PageSize: 10}
	items, total, err := repo.ListRequestDetails(ctx, filter)
	require.NoError(t, err)
	require.EqualValues(t, 2, total)
	require.Len(t, items, 2)
	failed, success := items[0], items[1]
	for _, item := range items {
		require.Equal(t, user.Email, item.UserEmail)
		require.Equal(t, group.Name, item.GroupName)
		require.Equal(t, account.Name, item.AccountName)
		require.Equal(t, key.Name, item.APIKeyName)
	}
	require.Equal(t, service.OpsRequestKindError, failed.Kind)
	require.Equal(t, "stream", failed.RequestType)
	require.Nil(t, failed.InputTokens)
	require.Nil(t, failed.CacheReadTokens)
	require.Nil(t, failed.ActualCost)
	require.Nil(t, failed.AccountCost)
	require.Equal(t, service.OpsRequestKindSuccess, success.Kind)
	require.Equal(t, "ws_v2", success.RequestType)
	require.Equal(t, 100, *success.InputTokens)
	require.Equal(t, 10, *success.OutputTokens)
	require.Equal(t, 800, *success.CacheReadTokens)
	require.Equal(t, 100, *success.CacheCreationTokens)
	require.Equal(t, 3060, *success.FirstTokenMs)
	require.Equal(t, 38660, *success.DurationMs)
	require.NotNil(t, success.ActualCost)
	require.Zero(t, *success.ActualCost)
	require.InDelta(t, 0.3, *success.AccountCost, 1e-9)

	filter.Sort = "duration_desc"
	filter.PageSize = 1
	items, total, err = repo.ListRequestDetails(ctx, filter)
	require.NoError(t, err)
	require.EqualValues(t, 2, total)
	require.Len(t, items, 1)
	require.Equal(t, service.OpsRequestKindSuccess, items[0].Kind)
	filter.Page = 2
	items, _, err = repo.ListRequestDetails(ctx, filter)
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.Equal(t, service.OpsRequestKindError, items[0].Kind)
}

func TestOpsRequestDetails_MissingIdentity(t *testing.T) {
	ctx := context.Background()
	repo := NewOpsRepository(integrationDB).(*opsRepository)
	requestID := uuid.NewString()
	_, err := repo.InsertErrorLog(ctx, &service.OpsInsertErrorLogInput{
		RequestID: requestID, ErrorPhase: "auth", ErrorType: "auth_error", Severity: "error",
		StatusCode: 401, CreatedAt: time.Now().UTC(),
	})
	require.NoError(t, err)
	items, total, err := repo.ListRequestDetails(ctx, &service.OpsRequestDetailFilter{RequestID: requestID})
	require.NoError(t, err)
	require.EqualValues(t, 1, total)
	require.Len(t, items, 1)
	require.Empty(t, items[0].UserEmail)
	require.Empty(t, items[0].GroupName)
	require.Nil(t, items[0].UserID)
	require.Nil(t, items[0].InputTokens)
}

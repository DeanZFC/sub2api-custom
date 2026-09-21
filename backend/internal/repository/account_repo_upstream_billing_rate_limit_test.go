package repository

import (
	"context"
	"encoding/json"
	"regexp"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/DATA-DOG/go-sqlmock"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestLockAndMergeAccountProbeExtraProtectsRateLimit(t *testing.T) {
	for _, tt := range []struct {
		name        string
		current     any
		input       any
		explicit    bool
		want        any
		wantEnabled bool
	}{
		{name: "unrelated stale write keeps new limit", current: 0.5, input: 9.0, want: 0.5, wantEnabled: true},
		{name: "unrelated missing value keeps new limit", current: 0.5, want: 0.5, wantEnabled: true},
		{name: "stale value cannot resurrect removed limit", input: 9.0},
		{name: "explicit edit can lower limit", current: 0.5, input: 0.25, explicit: true, want: 0.25, wantEnabled: true},
		{name: "zero requires probing", input: 0.0, explicit: true, want: 0.0, wantEnabled: true},
		{name: "explicit null clears limit", current: 0.5, explicit: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			t.Cleanup(func() { _ = db.Close() })
			client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
			t.Cleanup(func() { _ = client.Close() })
			current, err := json.Marshal(map[string]any{service.UpstreamBillingRateLimitExtraKey: tt.current})
			require.NoError(t, err)
			mock.ExpectQuery(`(?s)SELECT.*FOR NO KEY UPDATE`).
				WithArgs(int64(41), service.PlatformOpenAI, service.AccountTypeAPIKey, `{"api_key":"test"}`, nil).
				WillReturnRows(sqlmock.NewRows([]string{"identity", "ollama_identity", "proxy_identity", "probe", "sync", "snapshot", "session", "auto", "ollama_snapshot", "extra", "status", "schedulable", "error_message"}).
					AddRow(true, false, true, []byte(`true`), []byte(`true`), []byte(`{"status":"ok"}`), nil, nil, nil, current, service.StatusActive, true, ""))
			account := &service.Account{
				ID: 41, Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey,
				Credentials:                     map[string]any{"api_key": "test"},
				Extra:                           map[string]any{service.UpstreamBillingRateLimitExtraKey: tt.input},
				UpstreamBillingRateLimitChanged: tt.explicit,
			}
			// The attempted disable must never bypass an active limit.
			extra, err := lockAndMergeAccountProbeExtra(context.Background(), client, account, probeBoolPtr(false), nil)
			require.NoError(t, err)
			require.Equal(t, tt.want, extra[service.UpstreamBillingRateLimitExtraKey])
			require.Equal(t, tt.wantEnabled, extra[service.UpstreamBillingProbeEnabledExtraKey])
			require.Equal(t, tt.wantEnabled, extra[service.UpstreamBillingRateSyncEnabledExtraKey])
			if tt.wantEnabled {
				require.Equal(t, map[string]any{"status": "ok"}, extra[service.UpstreamBillingProbeExtraKey])
			} else {
				require.NotContains(t, extra, service.UpstreamBillingProbeExtraKey)
			}
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestUpstreamBillingRateLimitChangesRequireSchedulerOutbox(t *testing.T) {
	require.False(t, isSchedulerNeutralExtraKey(service.UpstreamBillingRateLimitExtraKey))
	for _, value := range []any{0.5, nil} {
		require.True(t, shouldEnqueueSchedulerOutboxForExtraUpdates(map[string]any{service.UpstreamBillingRateLimitExtraKey: value}))
	}
}

func TestListDueUpstreamBillingRateLimitedProbeAccountsIgnoresOptionalProbeSwitch(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	now := time.Now().UTC()
	var query string
	mock.ExpectQuery("WITH candidates AS").WithArgs(now, 10).WillReturnRows(sqlmock.NewRows([]string{"id"}))
	repo := newAccountRepositoryWithSQL(nil, captureQuerySQL{db: db, captured: &query}, nil)
	accounts, err := repo.ListDueUpstreamBillingRateLimitedProbeAccounts(context.Background(), now, 10)
	require.NoError(t, err)
	require.Empty(t, accounts)
	require.Contains(t, query, "upstream_billing_rate_limit")
	require.Contains(t, query, "jsonb_typeof")
	require.NotContains(t, query, "upstream_billing_probe_enabled")
	require.Contains(t, query, "LIMIT $2")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUpdateExtraRateLimitPublishesDurableSchedulerChange(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	t.Cleanup(func() { _ = client.Close() })
	mock.ExpectBegin()
	mock.ExpectExec(`(?s)UPDATE accounts SET extra = CASE WHEN.*upstream_billing_rate_limit.*jsonb_set.*upstream_billing_probe_enabled`).
		WithArgs(`{"upstream_billing_rate_limit":0.5}`, int64(41)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO scheduler_outbox")).WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()
	repo := newAccountRepositoryWithSQL(client, db, nil)
	require.NoError(t, repo.UpdateExtra(context.Background(), 41, map[string]any{service.UpstreamBillingRateLimitExtraKey: 0.5}))
	require.NoError(t, mock.ExpectationsWereMet())
}

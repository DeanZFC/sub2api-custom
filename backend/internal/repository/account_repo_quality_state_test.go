package repository

import (
	"context"
	"encoding/json"
	"testing"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/DATA-DOG/go-sqlmock"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestLockAndMergeAccountPreservesQualityAndManualSchedulingState(t *testing.T) {
	for _, tt := range []struct {
		name            string
		currentStatus   string
		incomingStatus  string
		currentReason   string
		incomingReason  string
		currentError    string
		incomingError   string
		explicitStatus  bool
		currentPaused   bool
		incomingPaused  bool
		wantStatus      string
		wantReason      string
		wantError       string
		wantSchedulable bool
	}{
		{name: "stale active snapshot cannot undo quality pause", currentStatus: "quality_paused", incomingStatus: "active", currentReason: "new failure", wantStatus: "quality_paused", wantReason: "new failure", wantSchedulable: true},
		{name: "stale reason cannot overwrite current failure", currentStatus: "quality_paused", incomingStatus: "quality_paused", currentReason: "new failure", incomingReason: "old failure", wantStatus: "quality_paused", wantReason: "new failure", wantSchedulable: true},
		{name: "stale paused snapshot cannot undo successful recovery", currentStatus: "active", incomingStatus: "quality_paused", incomingReason: "old failure", wantStatus: "active", wantSchedulable: true},
		{name: "stale reason alone cannot undo manual disable", currentStatus: "disabled", incomingStatus: "active", incomingReason: "old failure", wantStatus: "disabled", wantSchedulable: true},
		{name: "quality snapshot preserves later credential error", currentStatus: "error", currentError: "refresh token rejected", incomingStatus: "quality_paused", incomingReason: "old failure", wantStatus: "error", wantError: "refresh token rejected", wantSchedulable: true},
		{name: "current marker preserves error state", currentStatus: "error", currentReason: "quality failure", currentError: "refresh token rejected", incomingStatus: "active", wantStatus: "error", wantReason: "quality failure", wantError: "refresh token rejected", wantSchedulable: true},
		{name: "explicit active releases quality pause", currentStatus: "quality_paused", currentReason: "quality failure", incomingStatus: "active", explicitStatus: true, wantStatus: "active", wantSchedulable: true},
		{name: "explicit disable remains available", currentStatus: "quality_paused", currentReason: "quality failure", incomingStatus: "disabled", explicitStatus: true, wantStatus: "disabled", wantSchedulable: true},
		{name: "explicit active cannot enable manual scheduling pause", currentStatus: "quality_paused", currentReason: "quality failure", incomingStatus: "active", explicitStatus: true, currentPaused: true, wantStatus: "active"},
		{name: "unrelated update cannot enable manual scheduling pause", currentStatus: "active", incomingStatus: "active", currentPaused: true, wantStatus: "active"},
		{name: "quality pause retains manual scheduling pause", currentStatus: "quality_paused", currentReason: "quality failure", incomingStatus: "active", currentPaused: true, wantStatus: "quality_paused", wantReason: "quality failure"},
		{name: "explicit scheduling stop is preserved", currentStatus: "active", incomingStatus: "active", incomingPaused: true, wantStatus: "active"},
		{name: "ordinary active to error transition is unchanged", currentStatus: "active", incomingStatus: "error", incomingError: "refresh failure", wantStatus: "error", wantError: "refresh failure", wantSchedulable: true},
		{name: "ordinary error to active transition is unchanged", currentStatus: "error", currentError: "refresh failure", incomingStatus: "active", wantStatus: "active", wantSchedulable: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			t.Cleanup(func() { _ = db.Close() })
			client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
			t.Cleanup(func() { _ = client.Close() })
			currentExtra := map[string]any{}
			if tt.currentReason != "" {
				currentExtra["quality_protection_reason"] = tt.currentReason
			}
			extraJSON, err := json.Marshal(currentExtra)
			require.NoError(t, err)
			mock.ExpectQuery(`(?s)SELECT.*status,.*schedulable,.*FOR NO KEY UPDATE`).
				WithArgs(int64(41), service.PlatformOpenAI, service.AccountTypeOAuth, `{"access_token":"new-token"}`, nil).
				WillReturnRows(sqlmock.NewRows([]string{"identity", "ollama_identity", "proxy_identity", "probe", "sync", "snapshot", "session", "auto", "ollama_snapshot", "extra", "status", "schedulable", "error_message"}).
					AddRow(false, false, true, nil, nil, nil, nil, nil, nil, extraJSON, tt.currentStatus, !tt.currentPaused, tt.currentError))
			account := &service.Account{
				ID: 41, Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth,
				Credentials: map[string]any{"access_token": "new-token"}, Extra: map[string]any{"setting": "edited"},
				Status: tt.incomingStatus, StatusChanged: tt.explicitStatus,
				Schedulable: !tt.incomingPaused, ErrorMessage: tt.incomingError,
			}
			if tt.incomingReason != "" {
				account.Extra["quality_protection_reason"] = tt.incomingReason
			}
			extra, err := lockAndMergeAccountProbeExtra(context.Background(), client, account, nil, nil)
			require.NoError(t, err)
			require.Equal(t, tt.wantStatus, account.Status)
			require.Equal(t, tt.wantSchedulable, account.Schedulable)
			require.Equal(t, tt.wantError, account.ErrorMessage)
			if tt.wantReason == "" {
				require.NotContains(t, extra, "quality_protection_reason")
			} else {
				require.Equal(t, tt.wantReason, extra["quality_protection_reason"])
			}
			require.Equal(t, "edited", extra["setting"])
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

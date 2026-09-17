package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"
	"testing/synctest"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

func TestDefaultAccountHealthSettingsAreDisabled(t *testing.T) {
	settings := DefaultAccountHealthSettings()
	if settings.Enabled {
		t.Fatal("account health must be opt-in")
	}
	if got := normalizeAccountHealthSettings(AccountHealthSettings{}); got.Enabled {
		t.Fatal("normalization must not enable account health")
	}
}

type accountHealthStoreStub struct {
	isolateIDs         []int64
	recoverIDs         []int64
	automaticIsolation []bool
	automaticRecovery  []bool
	changed            bool
}

func (r *accountHealthStoreStub) TrySetAccountHealthIsolation(_ context.Context, id int64, _ time.Time, _ string, automatic bool) (bool, error) {
	r.isolateIDs = append(r.isolateIDs, id)
	r.automaticIsolation = append(r.automaticIsolation, automatic)
	return r.changed, nil
}

func (r *accountHealthStoreStub) ClearAccountHealthIsolation(_ context.Context, id int64, automatic bool) (bool, error) {
	r.recoverIDs = append(r.recoverIDs, id)
	r.automaticRecovery = append(r.automaticRecovery, automatic)
	return r.changed, nil
}

func healthStatsRows() *sqlmock.Rows {
	return sqlmock.NewRows([]string{"id", "name", "platform", "ok", "avg_ms", "err", "until", "reason", "can_isolate"})
}

func healthTestService(t *testing.T, enabled bool) (*AccountHealthService, *accountHealthStoreStub, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	cfg := DefaultAccountHealthSettings()
	cfg.Enabled = enabled
	raw, err := json.Marshal(cfg)
	require.NoError(t, err)
	settings := &upstreamBillingProbeSettingRepo{values: map[string]string{SettingKeyAccountHealthSettings: string(raw)}}
	repo := &accountHealthStoreStub{changed: true}
	return NewAccountHealthService(db, repo, settings), repo, mock
}

func TestAccountHealthDisabledDoesNotWriteSchedulingState(t *testing.T) {
	svc, repo, _ := healthTestService(t, false)
	svc.runOnce()
	require.Empty(t, repo.isolateIDs)
	require.Empty(t, repo.recoverIDs)
	require.Empty(t, svc.Snapshot())
}

func TestAccountHealthAutoRecoveryLeavesManualIsolation(t *testing.T) {
	svc, repo, mock := healthTestService(t, true)
	until := time.Now().Add(time.Hour)
	mock.ExpectQuery(`SELECT a.id`).WithArgs(10).WillReturnRows(healthStatsRows().
		AddRow(1, "auto", "openai", 20, nil, 0, until, "health:auto err_rate=60%", true).
		AddRow(2, "manual", "openai", 20, nil, 0, until, "health:manual", true))
	svc.runOnce()
	require.Equal(t, []int64{1}, repo.recoverIDs)
	require.Equal(t, []bool{true}, repo.automaticRecovery)
	require.Empty(t, repo.isolateIDs)
	require.False(t, svc.snapshot[1].Isolated)
	require.Nil(t, svc.snapshot[1].IsolatedUntil)
	require.Empty(t, svc.snapshot[1].IsolateReason)
	require.True(t, svc.snapshot[2].Isolated)
	require.Equal(t, "isolated", svc.snapshot[2].State)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestAccountHealthConditionalIsolationLossIsNotReportedAsSuccess(t *testing.T) {
	svc, repo, mock := healthTestService(t, true)
	repo.changed = false // Newer cooldown/policy won after the statistics query.
	mock.ExpectQuery(`SELECT a.id`).WithArgs(10).WillReturnRows(healthStatsRows().
		AddRow(1, "stale", "openai", 0, nil, 20, nil, nil, true).
		AddRow(2, "observe", "openai", 0, nil, 20, nil, nil, false).
		AddRow(3, "official", "openai", 0, nil, 20, time.Now().Add(time.Hour), "upstream cooldown", true))
	svc.runOnce()
	require.Equal(t, []int64{1}, repo.isolateIDs)
	require.Equal(t, []bool{true}, repo.automaticIsolation)
	for _, id := range []int64{1, 2, 3} {
		require.False(t, svc.snapshot[id].Isolated)
		require.Equal(t, "degraded", svc.snapshot[id].State)
	}
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestAccountHealthManualIsolationReportsCooldownConflict(t *testing.T) {
	svc, repo, _ := healthTestService(t, false)
	repo.changed = false
	require.ErrorContains(t, svc.Isolate(context.Background(), 1), "ACCOUNT_HEALTH_ISOLATION_CONFLICT")
	require.Equal(t, []bool{false}, repo.automaticIsolation)
	require.NoError(t, svc.Resume(context.Background(), 1))
	require.Equal(t, []bool{false}, repo.automaticRecovery)
}

func TestAccountHealthSettingsWakeWorkerAndResetInterval(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		svc, _, mock := healthTestService(t, false)
		svc.Start()
		defer svc.Stop()
		synctest.Wait()

		mock.ExpectQuery(`SELECT a.id`).WithArgs(10).WillReturnRows(healthStatsRows())
		cfg := DefaultAccountHealthSettings()
		cfg.Enabled, cfg.IntervalSeconds = true, 10
		_, err := svc.UpdateSettings(context.Background(), cfg)
		require.NoError(t, err)
		synctest.Wait()
		require.NoError(t, mock.ExpectationsWereMet(), "settings change should trigger immediate evaluation")

		mock.ExpectQuery(`SELECT a.id`).WithArgs(10).WillReturnRows(healthStatsRows())
		time.Sleep(10 * time.Second)
		synctest.Wait()
		require.NoError(t, mock.ExpectationsWereMet(), "new 10-second interval should replace the original 60-second timer")
	})
}

func TestDecideAccountHealthThresholds(t *testing.T) {
	cfg := DefaultAccountHealthSettings()
	now := time.Now()
	if got := decideAccountHealth(accountHealthRow{id: 1, ok: 8, err: 2}, cfg, now); got.State != "healthy" {
		t.Fatalf("below sample threshold state=%s", got.State)
	}
	if got := decideAccountHealth(accountHealthRow{id: 2, ok: 5, err: 5}, cfg, now); got.State != "isolated" || got.Score != 50 {
		t.Fatalf("isolated snapshot=%+v", got)
	}
	if got := decideAccountHealth(accountHealthRow{id: 3, ok: 8, err: 2}, cfg, now); got.ErrRate != 0.2 {
		t.Fatalf("err rate=%v", got.ErrRate)
	}
}

func TestAccountHealthRowOnlyRecognizesHealthReason(t *testing.T) {
	now := time.Now()
	until := now.Add(time.Minute)
	row := accountHealthRow{until: sql.NullTime{Time: until, Valid: true}, reason: sql.NullString{String: "429", Valid: true}}
	if row.healthIsolated(now) {
		t.Fatal("429 cooldown must not be treated as health isolation")
	}
	row.reason = sql.NullString{String: "health:auto", Valid: true}
	if !row.healthIsolated(now) {
		t.Fatal("health reason should be recognized")
	}
}

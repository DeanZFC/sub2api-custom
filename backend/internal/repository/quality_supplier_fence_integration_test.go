//go:build integration

package repository

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestQualityOperationsSupplierEditFencesWaitingTransactionIntegration(t *testing.T) {
	ctx, db, repo, _ := qualityOperationsDB(t)
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	supplier := service.QualitySupplier{Name: "vendor", Enabled: true, AccountIDs: []int64{62, 63}, WindowSeconds: 600, MinAccounts: 2, FailurePercent: 60, ProbeIntervalSeconds: 60}
	require.NoError(t, repo.SaveSupplier(ctx, &supplier))

	// Begin the edit transaction before admitting an old-config probe. Holding
	// the membership lock ensures the edit cannot take effect until afterward.
	blocker, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer func() { _ = blocker.Rollback() }()
	_, err = blocker.ExecContext(ctx, `SELECT pg_advisory_xact_lock(1937006961,9)`)
	require.NoError(t, err)
	var blockerPID int
	require.NoError(t, blocker.QueryRowContext(ctx, `SELECT pg_backend_pid()`).Scan(&blockerPID))
	editDone := make(chan error, 1)
	go func() { editDone <- repo.SaveSupplier(ctx, &supplier) }()
	waitForQualityBlockedBackend(t, ctx, db, blockerPID)

	old, err := repo.ReserveQualityProbe(ctx, 62, 100, false, "")
	require.NoError(t, err)
	require.NoError(t, blocker.Commit())
	require.NoError(t, <-editDone)
	require.NoError(t, repo.SettleQualityProbe(ctx, old, service.QualityProbeOutcome{Kind: "server"}))
	snapshot, err := repo.OperationsSnapshot(ctx)
	require.NoError(t, err)
	require.Zero(t, snapshot.Suppliers[0].Observed, "an edit fences all probes admitted before it takes effect, even when its transaction began earlier")

	fresh, err := repo.ReserveQualityProbe(ctx, 62, 100, false, "")
	require.NoError(t, err)
	require.NoError(t, repo.SettleQualityProbe(ctx, fresh, service.QualityProbeOutcome{Kind: "server"}))
	snapshot, err = repo.OperationsSnapshot(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, snapshot.Suppliers[0].Observed, "new-config probes still contribute supplier evidence")
	require.Equal(t, 1, snapshot.Suppliers[0].Affected)
}

func TestQualityOperationsSupplierEditFencesWaitingProbeIntegration(t *testing.T) {
	ctx, db, repo, _ := qualityOperationsDB(t)
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	supplier := service.QualitySupplier{Name: "vendor", Enabled: true, AccountIDs: []int64{62, 63}, WindowSeconds: 600, MinAccounts: 2, FailurePercent: 60, ProbeIntervalSeconds: 60}
	require.NoError(t, repo.SaveSupplier(ctx, &supplier))
	_, err := repo.ReserveQualityProbe(ctx, 62, 100, false, "")
	require.NoError(t, err)

	// Let a real reservation hold the supplier row while waiting for the
	// budget row. The edit must fence the probe after both waits complete.
	blocker, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer func() { _ = blocker.Rollback() }()
	var blockerPID int
	require.NoError(t, blocker.QueryRowContext(ctx, `SELECT pg_backend_pid() FROM quality_budget_days WHERE account_id=0 FOR UPDATE`).Scan(&blockerPID))
	type admissionResult struct {
		reservation *service.QualityProbeReservation
		err         error
	}
	admissionDone := make(chan admissionResult, 1)
	go func() {
		res, err := repo.ReserveQualityProbe(ctx, 62, 100, false, "")
		admissionDone <- admissionResult{reservation: res, err: err}
	}()
	reservationPID := waitForQualityBlockedBackend(t, ctx, db, blockerPID)
	editDone := make(chan error, 1)
	go func() { editDone <- repo.SaveSupplier(ctx, &supplier) }()
	waitForQualityBlockedBackend(t, ctx, db, reservationPID)
	require.NoError(t, blocker.Commit())
	old := <-admissionDone
	require.NoError(t, old.err)
	require.NoError(t, <-editDone)
	require.NoError(t, repo.SettleQualityProbe(ctx, old.reservation, service.QualityProbeOutcome{Kind: "server"}))
	snapshot, err := repo.OperationsSnapshot(ctx)
	require.NoError(t, err)
	require.Zero(t, snapshot.Suppliers[0].Observed, "the fence must be timestamped after acquiring the supplier row lock")
}

func waitForQualityBlockedBackend(t *testing.T, ctx context.Context, db *sql.DB, blockerPID int) int {
	t.Helper()
	var waitingPID int
	require.Eventually(t, func() bool {
		err := db.QueryRowContext(ctx, `SELECT COALESCE((SELECT pid FROM pg_stat_activity WHERE $1=ANY(pg_blocking_pids(pid)) LIMIT 1),0)`, blockerPID).Scan(&waitingPID)
		return err == nil && waitingPID != 0
	}, 5*time.Second, 5*time.Millisecond, "the transaction must be waiting for its blocker")
	return waitingPID
}

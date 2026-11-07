package repository

import (
	"context"
	"io/fs"
	"sort"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/Wei-Shaw/sub2api/migrations"
	"github.com/stretchr/testify/require"
)

func TestAccountPerformanceReleaseRequiresExactOwnershipAndDisablesEvaluation(t *testing.T) {
	for _, n := range []int64{0, 1} {
		t.Run(map[int64]string{0: "stale", 1: "current"}[n], func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer db.Close()
			repo := &accountPerformanceRepository{db: db}
			mock.ExpectBegin()
			mock.ExpectQuery("SELECT id FROM accounts.*FOR SHARE").WithArgs(int64(7)).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(7))
			mock.ExpectExec("UPDATE account_performance_monitors SET paused=false.*config=jsonb_set.*revision=revision\\+1.*WHERE account_id=\\$1 AND revision=\\$2 AND pause_generation=\\$3 AND paused").WithArgs(int64(7), int64(2), int64(3)).WillReturnResult(sqlmock.NewResult(0, n))
			if n == 1 {
				mock.ExpectExec("INSERT INTO account_performance_events").WithArgs(int64(7), "release", int64(9), int64(3), int64(3), sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(1, 1))
				mock.ExpectCommit()
			} else {
				mock.ExpectRollback()
			}
			err = repo.Release(context.Background(), 7, 2, 3, 9)
			if n == 0 {
				require.ErrorIs(t, err, service.ErrPerformanceConflict)
			} else {
				require.NoError(t, err)
			}
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}
func TestAccountPerformanceMigrationSequenceAndSchema(t *testing.T) {
	files, err := fs.Glob(migrations.FS, "*.sql")
	require.NoError(t, err)
	sort.Strings(files)
	target := "339_account_performance_monitor.sql"
	i := sort.SearchStrings(files, target)
	require.Less(t, i, len(files))
	require.Equal(t, target, files[i])
	require.True(t, strings.HasPrefix(files[i-1], "338_"))
	data, err := fs.ReadFile(migrations.FS, target)
	require.NoError(t, err)
	notx, err := validateMigrationExecutionMode(target, string(data))
	require.NoError(t, err)
	require.False(t, notx)
	require.NotContains(t, strings.ToUpper(string(data)), "UPDATE ACCOUNTS")
	require.Contains(t, string(data), "account_performance_monitors")
	require.Contains(t, string(data), "account_performance_events")
}

package repository

import (
	"context"
	"errors"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
)

func TestBulkUpdateModelSyncSavesMappingAndMetadataAtomically(t *testing.T) {
	for _, outboxFails := range []bool{false, true} {
		t.Run(map[bool]string{false: "commit", true: "outbox failure rolls back"}[outboxFails], func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			t.Cleanup(func() { _ = db.Close() })
			client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
			t.Cleanup(func() { _ = client.Close() })
			repo := newAccountRepositoryWithSQL(client, db, nil)

			mock.ExpectBegin()
			// Top-level JSONB merge replaces the entire nested model_mapping while
			// preserving other credential keys, including concurrent token updates.
			mock.ExpectExec(regexp.QuoteMeta("UPDATE accounts SET credentials = COALESCE(credentials, '{}'::jsonb) || $1::jsonb, extra = COALESCE(extra, '{}'::jsonb) || $2::jsonb, updated_at = NOW() WHERE id = ANY($3) AND deleted_at IS NULL")).
				WithArgs([]byte(`{"model_mapping":{"new-model":"new-model"}}`),
					[]byte(`{"upstream_model_metadata":{"models":{"new-model":{"id":"new-model"}}}}`), "{91}").
				WillReturnResult(sqlmock.NewResult(0, 1))
			outbox := mock.ExpectExec(regexp.QuoteMeta("INSERT INTO scheduler_outbox"))
			if outboxFails {
				outbox.WillReturnError(errors.New("outbox unavailable"))
				mock.ExpectRollback()
			} else {
				outbox.WillReturnResult(sqlmock.NewResult(1, 1))
				mock.ExpectCommit()
			}
			rows, err := repo.BulkUpdate(context.Background(), []int64{91}, service.AccountBulkUpdate{
				Credentials: map[string]any{"model_mapping": map[string]any{"new-model": "new-model"}},
				Extra: map[string]any{service.UpstreamModelMetadataExtraKey: map[string]any{
					"models": map[string]any{"new-model": map[string]any{"id": "new-model"}},
				}},
			})
			if outboxFails {
				require.EqualError(t, err, "outbox unavailable")
				require.Zero(t, rows)
			} else {
				require.NoError(t, err)
				require.Equal(t, int64(1), rows)
			}
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

package repository

import (
	"context"
	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/DATA-DOG/go-sqlmock"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestSharedAccountsExcludedFromOrdinaryCandidateQueries(t *testing.T) {
	var captured string
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(captureEntQueryMatcher{actual: &captured}))
	require.NoError(t, err)
	defer db.Close()
	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	defer client.Close()
	r := newAccountRepositoryWithSQL(client, db, nil)
	ctx := context.Background()
	for name, query := range map[string]func() ([]service.Account, error){
		"global":    func() ([]service.Account, error) { return r.ListSchedulable(ctx) },
		"platform":  func() ([]service.Account, error) { return r.ListSchedulableByPlatform(ctx, "openai") },
		"platforms": func() ([]service.Account, error) { return r.ListSchedulableByPlatforms(ctx, []string{"openai"}) },
		"ungrouped": func() ([]service.Account, error) { return r.ListSchedulableUngroupedByPlatform(ctx, "openai") },
		"ungrouped platforms": func() ([]service.Account, error) {
			return r.ListSchedulableUngroupedByPlatforms(ctx, []string{"openai"})
		},
		"models": func() ([]service.Account, error) {
			return r.ListModelAvailabilityCandidates(ctx, nil, []string{"openai"}, true)
		},
	} {
		t.Run(name, func(t *testing.T) {
			mock.ExpectQuery("candidate query").WillReturnRows(sqlmock.NewRows([]string{"id"}))
			_, err := query()
			require.NoError(t, err)
			require.Regexp(t, `"accounts"\."account_scope" = \$[0-9]+`, captured)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

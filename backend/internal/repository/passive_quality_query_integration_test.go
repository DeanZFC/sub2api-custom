//go:build integration

package repository

import (
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestPassiveQualityFilteredQueryIntegration(t *testing.T) {
	ctx, db, repo := passiveDB(t)
	p := newPassivePolicy()
	require.NoError(t, repo.SavePolicy(ctx, &p))
	other := newPassivePolicy()
	other.Name = "other"
	require.NoError(t, repo.SavePolicy(ctx, &other))
	now := time.Now().UTC().Truncate(time.Second)
	_, err := db.Exec(`INSERT INTO passive_quality_events(policy_id,account_id,policy_name,kind,detail,created_at) SELECT $1,62,'chosen','paused',jsonb_build_object('model','chosen-model'),$2::timestamptz + i * interval '1 second' FROM generate_series(1,3) i`, p.ID, now)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO passive_quality_events(policy_id,account_id,policy_name,kind,detail,created_at) SELECT $1,63,'noise','observed','{}'::jsonb,$2 FROM generate_series(1,150)`, other.ID, now)
	require.NoError(t, err)
	until := now.Add(2 * time.Second)
	q := service.PassiveQualityQuery{PolicyID: p.ID, AccountID: 62, Model: "chosen-model", Kind: "paused", Since: &now, Until: &until, Limit: 1}
	page, err := repo.QueryEvents(ctx, q)
	require.NoError(t, err)
	require.Len(t, page.Items, 1)
	require.Equal(t, p.ID, page.Items[0].PolicyID)
	require.True(t, page.HasMore)
	q.Cursor = page.NextCursor
	next, err := repo.QueryEvents(ctx, q)
	require.NoError(t, err)
	require.Len(t, next.Items, 1)
	require.Less(t, next.Items[0].ID, page.Items[0].ID)
	require.False(t, next.HasMore)
	q.Cursor = "bad"
	_, err = repo.QueryEvents(ctx, q)
	require.ErrorIs(t, err, service.ErrPassiveQualityCursor)

	_, err = db.Exec(`INSERT INTO passive_quality_states(policy_id,account_id,model_key,data) SELECT $1,62,'model-' || lpad(i::text,5,'0'),jsonb_build_object('policy_id',$1::bigint,'account_id',62,'model','model-' || lpad(i::text,5,'0'),'phase','paused','generation',1) FROM generate_series(1,10001) i`, p.ID)
	require.NoError(t, err)
	states, err := repo.QueryStates(ctx, service.PassiveQualityQuery{PolicyID: p.ID, Limit: 2})
	require.NoError(t, err)
	require.Len(t, states.Items, 2)
	require.EqualValues(t, 10001, states.Total)
	require.True(t, states.HasMore)
	more, err := repo.QueryStates(ctx, service.PassiveQualityQuery{PolicyID: p.ID, Limit: 2, Cursor: states.NextCursor})
	require.NoError(t, err)
	require.Equal(t, "model-00003", more.Items[0].Model)
	filtered, err := repo.QueryStates(ctx, service.PassiveQualityQuery{PolicyID: p.ID, AccountID: 62, Model: "model-10001", Phase: "paused"})
	require.NoError(t, err)
	require.EqualValues(t, 1, filtered.Total)
	require.Len(t, filtered.Items, 1)
	summary, err := repo.StateSummary(ctx)
	require.NoError(t, err)
	require.EqualValues(t, 10001, summary.Total)
	require.EqualValues(t, 1, summary.PausedAccounts)
	require.EqualValues(t, 10001, summary.ByPolicy[0].Phases["paused"])
	_, err = repo.QueryStates(ctx, service.PassiveQualityQuery{Cursor: "invalid"})
	require.ErrorIs(t, err, service.ErrPassiveQualityCursor)
}

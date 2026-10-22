//go:build integration

package repository

import (
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestChannelQualityOverviewIndependentOwnersAndPagination(t *testing.T) {
	ctx, db, passive := passiveDB(t)
	_, err := db.Exec(`ALTER TABLE accounts ADD COLUMN platform TEXT DEFAULT 'openai',ADD COLUMN overload_until TIMESTAMPTZ;
 CREATE TABLE scheduled_test_plans(id BIGINT PRIMARY KEY,name TEXT,model_id TEXT,enabled BOOLEAN,group_ids BIGINT[],excluded_account_ids BIGINT[] DEFAULT '{}',next_run_at TIMESTAMPTZ);
 CREATE TABLE scheduled_test_protection_states(plan_id BIGINT,account_id BIGINT,reason TEXT,updated_at TIMESTAMPTZ DEFAULT now(),blocked BOOLEAN,completed BOOLEAN,result_id BIGINT,result_started_at TIMESTAMPTZ DEFAULT now(),round_started_at TIMESTAMPTZ DEFAULT now(),verdict TEXT);
 CREATE TABLE scheduled_test_combination_states(plan_id BIGINT,account_id BIGINT,reason TEXT,updated_at TIMESTAMPTZ DEFAULT now(),blocked BOOLEAN);
 INSERT INTO scheduled_test_plans VALUES(1,'Capability','model',true,ARRAY[8]::bigint[],'{}',now());
 INSERT INTO scheduled_test_protection_states(plan_id,account_id,reason,blocked,completed,result_id,verdict) VALUES(1,62,'Passed',false,true,5,'pass');
 UPDATE accounts SET status='quality_paused',schedulable=false WHERE id=63;`)
	require.NoError(t, err)
	r := NewChannelQualityRepository(db)
	page, err := r.ListQualityAccounts(ctx, service.ChannelQualityFilter{Limit: 1})
	require.NoError(t, err)
	require.Len(t, page.Items, 1)
	require.EqualValues(t, 62, page.NextAfter)
	a := page.Items[0]
	require.Equal(t, 1, a.ActivePlans)
	require.Len(t, a.Controls, 1)
	require.Equal(t, "healthy", a.Controls[0].Phase)
	require.False(t, a.Controls[0].Blocking)
	page, err = r.ListQualityAccounts(ctx, service.ChannelQualityFilter{After: 62, Limit: 1})
	require.NoError(t, err)
	require.Zero(t, page.NextAfter)
	require.Len(t, page.Items, 1)
	reasons := []string{}
	for _, c := range page.Items[0].Controls {
		reasons = append(reasons, c.Reason)
	}
	require.ElementsMatch(t, []string{"manual_stop", "quality_paused"}, reasons)
	page, err = r.ListQualityAccounts(ctx, service.ChannelQualityFilter{ProblemsOnly: true})
	require.NoError(t, err)
	require.Len(t, page.Items, 1)
	require.EqualValues(t, 63, page.Items[0].ID)
	_, err = db.Exec(`UPDATE scheduled_test_protection_states SET verdict='fail',reason='Wrong answer' WHERE account_id=62`)
	require.NoError(t, err)
	page, err = r.ListQualityAccounts(ctx, service.ChannelQualityFilter{ProblemsOnly: true, Model: "model", Search: "A"})
	require.NoError(t, err)
	require.Len(t, page.Items, 1)
	require.Equal(t, "observed", page.Items[0].Controls[0].Phase)
	p := newPassivePolicy()
	p.Model = ""
	require.NoError(t, passive.SavePolicy(ctx, &p))
	_, err = passive.advance(ctx, p.ID, 62, time.Now(), false)
	require.NoError(t, err)
	page, err = r.ListQualityAccounts(ctx, service.ChannelQualityFilter{Model: "another-model"})
	require.NoError(t, err)
	require.Len(t, page.Items, 1, "all-model policies remain visible with a model filter")
	require.EqualValues(t, 62, page.Items[0].ID)
}

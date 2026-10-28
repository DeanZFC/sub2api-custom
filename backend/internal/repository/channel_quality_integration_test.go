//go:build integration

package repository

import (
	"database/sql"
	"encoding/json"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func prepareChannelQualitySchema(t *testing.T, db *sql.DB) {
	t.Helper()
	_, err := db.Exec(`ALTER TABLE accounts ADD COLUMN platform TEXT DEFAULT 'openai',ADD COLUMN overload_until TIMESTAMPTZ;
 CREATE TABLE scheduled_test_plans(id BIGINT PRIMARY KEY,name TEXT,model_id TEXT,enabled BOOLEAN,group_ids BIGINT[],excluded_account_ids BIGINT[] DEFAULT '{}',next_run_at TIMESTAMPTZ,model_ids TEXT[] DEFAULT '{}');
 CREATE TABLE scheduled_test_definitions(id BIGINT PRIMARY KEY,output_kind TEXT);
 ALTER TABLE scheduled_test_protection_states ADD COLUMN plan_id BIGINT,ADD COLUMN reason TEXT,ADD COLUMN updated_at TIMESTAMPTZ DEFAULT now(),ADD COLUMN completed BOOLEAN,ADD COLUMN result_id BIGINT,ADD COLUMN result_started_at TIMESTAMPTZ DEFAULT now(),ADD COLUMN round_started_at TIMESTAMPTZ DEFAULT now(),ADD COLUMN verdict TEXT,ADD COLUMN model_key TEXT NOT NULL DEFAULT '',ADD COLUMN test_definition_id BIGINT;
 ALTER TABLE scheduled_test_combination_states ADD COLUMN plan_id BIGINT,ADD COLUMN reason TEXT,ADD COLUMN updated_at TIMESTAMPTZ DEFAULT now(),ADD COLUMN model_key TEXT NOT NULL DEFAULT '',ADD COLUMN account_wide BOOLEAN NOT NULL DEFAULT false;`)
	require.NoError(t, err)
}

func TestChannelQualityOverviewIndependentOwnersAndPagination(t *testing.T) {
	ctx, db, passive := passiveDB(t)
	prepareChannelQualitySchema(t, db)
	_, err := db.Exec(`INSERT INTO scheduled_test_plans(id,name,model_id,enabled,group_ids,excluded_account_ids,next_run_at) VALUES(1,'Capability','model',true,ARRAY[8]::bigint[],'{}',now());
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
	// Healthy availability cannot hide cache restrictions from problems-only.
	_, err = db.Exec("UPDATE scheduled_test_protection_states SET verdict='pass' WHERE account_id=62")
	require.NoError(t, err)
	state := service.PassiveQualityState{PolicyID: p.ID, PolicyName: p.Name, AccountID: 62, Scope: "account", Mode: "enforce", Phase: "healthy", Cache: map[string]service.PassiveQualityCacheState{"model": {Phase: "reduced", AllocationPercent: 25}}}
	raw, err := json.Marshal(state)
	require.NoError(t, err)
	_, err = db.Exec("UPDATE passive_quality_states SET data=$1 WHERE policy_id=$2 AND account_id=62", string(raw), p.ID)
	require.NoError(t, err)
	page, err = r.ListQualityAccounts(ctx, service.ChannelQualityFilter{ProblemsOnly: true, AccountID: 62, Model: "model"})
	require.NoError(t, err)
	require.Len(t, page.Items, 1)
	require.Equal(t, "cache_limited", page.Items[0].Controls[len(page.Items[0].Controls)-1].Phase)
	page, err = r.ListQualityAccounts(ctx, service.ChannelQualityFilter{ProblemsOnly: true, AccountID: 62, Model: "another-model"})
	require.NoError(t, err)
	require.Empty(t, page.Items)
	state.Phase = "exempt"
	raw, err = json.Marshal(state)
	require.NoError(t, err)
	_, err = db.Exec("UPDATE passive_quality_states SET data=$1 WHERE policy_id=$2 AND account_id=62", string(raw), p.ID)
	require.NoError(t, err)
	page, err = r.ListQualityAccounts(ctx, service.ChannelQualityFilter{ProblemsOnly: true, AccountID: 62})
	require.NoError(t, err)
	require.Empty(t, page.Items)
	// Explicit account scope impacts every model even when its evidence filter
	// is a single model; model scope must stay absent from other-model pages.
	state.Phase, state.Model, state.Scope = "paused", "A", "account"
	raw, err = json.Marshal(state)
	require.NoError(t, err)
	_, err = db.Exec("UPDATE passive_quality_states SET data=$1 WHERE policy_id=$2 AND account_id=62", string(raw), p.ID)
	require.NoError(t, err)
	page, err = r.ListQualityAccounts(ctx, service.ChannelQualityFilter{ProblemsOnly: true, AccountID: 62, Model: "B"})
	require.NoError(t, err)
	require.Len(t, page.Items, 1)
	state.Scope = "model"
	raw, err = json.Marshal(state)
	require.NoError(t, err)
	_, err = db.Exec("UPDATE passive_quality_states SET data=$1 WHERE policy_id=$2 AND account_id=62", string(raw), p.ID)
	require.NoError(t, err)
	page, err = r.ListQualityAccounts(ctx, service.ChannelQualityFilter{ProblemsOnly: true, AccountID: 62, Model: "B"})
	require.NoError(t, err)
	require.Empty(t, page.Items)
}

func TestChannelQualityOverviewIncludesIndependentRealTrafficScore(t *testing.T) {
	ctx, db, _ := passiveDB(t)
	prepareChannelQualitySchema(t, db)
	_, err := db.Exec(`INSERT INTO usage_logs(account_id,request_id,created_at,model,requested_model,first_token_ms,duration_ms)
 SELECT 62,'score-'||i,now()-i*INTERVAL '1 second','upstream-model','public-model',2000,10000 FROM generate_series(1,20) i`)
	require.NoError(t, err)
	seedScoreFixtureReports(t, db, time.Now().Add(-time.Minute))
	// No active plan or passive policy exists; real public-model traffic still
	// makes this account discoverable and independently scoreable.
	page, err := NewChannelQualityRepository(db).ListQualityAccounts(ctx, service.ChannelQualityFilter{AccountID: 62, Model: "public-model"})
	require.NoError(t, err)
	require.Len(t, page.Items, 1)
	score := page.Items[0].QualityScore
	require.NotNil(t, score)
	require.NotNil(t, score.Value)
	require.Equal(t, 100., *score.Value)
	require.Equal(t, "partial", score.Status)
	require.Equal(t, 20, score.Samples)
	page, err = NewChannelQualityRepository(db).ListQualityAccounts(ctx, service.ChannelQualityFilter{AccountID: 62, Model: "upstream-model"})
	require.NoError(t, err)
	require.Empty(t, page.Items)
}

func TestChannelQualityOverviewPassRateModelScopeAndStaleHolds(t *testing.T) {
	ctx, db, _ := passiveDB(t)
	prepareChannelQualitySchema(t, db)
	// A disabled plan may retain an independent capability hold. Changing the
	// plan's current model must not make its old-model hold disappear.
	_, err := db.Exec(`INSERT INTO scheduled_test_plans(id,name,model_id,enabled,group_ids,excluded_account_ids,next_run_at) VALUES(1,'Capability','new-model',false,ARRAY[8]::bigint[],'{}',now())`)
	require.NoError(t, err)
	config := service.ScheduledTestPassRateConfig{Enabled: true, WindowSize: 2, WindowSeconds: 3600, PauseBelow: 80, RecoverAtLeast: 90, RecoverConsecutive: 2}
	now := time.Now()
	summary := service.SummarizeScheduledTestPassRate(config, []service.ScheduledTestPassRateObservation{{Verdict: "fail", StartedAt: now.Add(-2 * time.Hour)}, {Verdict: "pass", StartedAt: now.Add(-3 * time.Hour)}}, true, now.Add(-2*time.Hour))
	summary.RuleVersion, summary.Model = "v1", "old-model"
	raw, err := json.Marshal(summary)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO scheduled_test_pass_rate_states(plan_id,account_id,test_definition_id,model_key,rule_version,blocked,summary) VALUES(1,62,7,'old-model','v1',true,$1)`, string(raw))
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO scheduled_test_pass_rate_evidence(plan_id,account_id,test_definition_id,model_key,rule_version,verdict,started_at)
 VALUES(1,62,7,'old-model','v1','fail',now()-interval '2 hours'),(1,62,7,'old-model','v1','pass',now()-interval '3 hours')`)
	require.NoError(t, err)
	db.SetMaxOpenConns(1)
	r := NewChannelQualityRepository(db)
	page, err := r.ListQualityAccounts(ctx, service.ChannelQualityFilter{AccountID: 62, Model: "old-model", ProblemsOnly: true})
	require.NoError(t, err)
	require.Len(t, page.Items, 1)
	require.Len(t, page.Items[0].Controls, 1)
	c := page.Items[0].Controls[0]
	require.Equal(t, "pass_rate", c.Metric)
	require.Equal(t, "model", c.Scope)
	require.Equal(t, "old-model", c.Model)
	require.Equal(t, "paused", c.Phase)
	require.True(t, c.Blocking)
	require.True(t, c.PassRate.Blocked)
	require.True(t, c.PassRate.Stale)
	require.Zero(t, c.PassRate.Samples)
	require.Nil(t, c.PassRate.PassRate)
	var blocked bool
	require.NoError(t, db.QueryRow(`SELECT blocked FROM scheduled_test_pass_rate_states WHERE account_id=62`).Scan(&blocked))
	require.True(t, blocked, "reading expired evidence must never release a live hold")
	page, err = r.ListQualityAccounts(ctx, service.ChannelQualityFilter{AccountID: 62, Model: "other-model", ProblemsOnly: true})
	require.NoError(t, err)
	require.Empty(t, page.Items)

	_, err = db.Exec(`UPDATE scheduled_test_plans SET enabled=true; UPDATE scheduled_test_pass_rate_states SET blocked=false`)
	require.NoError(t, err)
	page, err = r.ListQualityAccounts(ctx, service.ChannelQualityFilter{AccountID: 62, Model: "old-model"})
	require.NoError(t, err)
	require.Equal(t, "insufficient", page.Items[0].Controls[0].Phase)
	require.False(t, page.Items[0].Controls[0].Blocking)
}

func TestChannelQualityOverviewActiveModelsRemainIndependent(t *testing.T) {
	ctx, db, _ := passiveDB(t)
	prepareChannelQualitySchema(t, db)
	_, err := db.Exec(`INSERT INTO scheduled_test_plans(id,name,model_id,model_ids,enabled,group_ids,next_run_at)
 VALUES(1,'Capability','A',ARRAY['A','B'],true,ARRAY[8]::bigint[],now());
 INSERT INTO scheduled_test_definitions VALUES(7,'number');`)
	require.NoError(t, err)
	r := NewChannelQualityRepository(db)
	page, err := r.ListQualityAccounts(ctx, service.ChannelQualityFilter{AccountID: 62, Model: "B"})
	require.NoError(t, err)
	require.Len(t, page.Items, 1, "a selected non-primary model is discoverable before its first result")
	require.Empty(t, page.Items[0].Controls)
	_, err = db.Exec(`INSERT INTO scheduled_test_protection_states(plan_id,account_id,test_definition_id,model_key,reason,blocked,completed,result_id,verdict)
 VALUES(1,62,7,'A','Passed',false,true,5,'pass'),(1,62,7,'B','Wrong answer',true,true,6,'fail')`)
	require.NoError(t, err)
	page, err = r.ListQualityAccounts(ctx, service.ChannelQualityFilter{AccountID: 62, Model: "B", ProblemsOnly: true})
	require.NoError(t, err)
	require.Len(t, page.Items, 1)
	require.Len(t, page.Items[0].Controls, 1)
	c := page.Items[0].Controls[0]
	require.Equal(t, "B", c.Model)
	require.Equal(t, "model", c.Scope)
	require.Equal(t, "paused", c.Phase)
	require.True(t, c.Blocking)
	page, err = r.ListQualityAccounts(ctx, service.ChannelQualityFilter{AccountID: 62, Model: "A", ProblemsOnly: true})
	require.NoError(t, err)
	require.Empty(t, page.Items, "model B's hold must not mark model A as a problem")
	page, err = r.ListQualityAccounts(ctx, service.ChannelQualityFilter{AccountID: 62, Model: "A"})
	require.NoError(t, err)
	require.Len(t, page.Items, 1)
	require.Len(t, page.Items[0].Controls, 1)
	require.Equal(t, "A", page.Items[0].Controls[0].Model)
	require.Equal(t, "healthy", page.Items[0].Controls[0].Phase)
	// Unenforced failures still belong only to their own model.
	_, err = db.Exec(`UPDATE scheduled_test_protection_states SET blocked=false WHERE model_key='B'`)
	require.NoError(t, err)
	page, err = r.ListQualityAccounts(ctx, service.ChannelQualityFilter{AccountID: 62, Model: "B", ProblemsOnly: true})
	require.NoError(t, err)
	require.Len(t, page.Items, 1)
	require.Equal(t, "observed", page.Items[0].Controls[0].Phase)
	page, err = r.ListQualityAccounts(ctx, service.ChannelQualityFilter{AccountID: 62, Model: "A", ProblemsOnly: true})
	require.NoError(t, err)
	require.Empty(t, page.Items)
	// Retained holds remain visible under the result's original model after a
	// plan is disabled or its current selection changes.
	_, err = db.Exec(`UPDATE scheduled_test_plans SET enabled=false,model_id='C',model_ids=ARRAY['C'];
 UPDATE scheduled_test_protection_states SET blocked=true WHERE model_key='B'`)
	require.NoError(t, err)
	page, err = r.ListQualityAccounts(ctx, service.ChannelQualityFilter{AccountID: 62, Model: "B", ProblemsOnly: true})
	require.NoError(t, err)
	require.Len(t, page.Items, 1)
	require.Len(t, page.Items[0].Controls, 1)
	require.Equal(t, "B", page.Items[0].Controls[0].Model)
	page, err = r.ListQualityAccounts(ctx, service.ChannelQualityFilter{AccountID: 62, Model: "C", ProblemsOnly: true})
	require.NoError(t, err)
	require.Empty(t, page.Items)
}

func TestChannelQualityOverviewCombinationAndStatisticsScopes(t *testing.T) {
	ctx, db, _ := passiveDB(t)
	prepareChannelQualitySchema(t, db)
	_, err := db.Exec(`INSERT INTO scheduled_test_plans(id,name,model_id,model_ids,enabled,group_ids,next_run_at)
 VALUES(1,'Combined','A',ARRAY['A','B'],true,ARRAY[8]::bigint[],now());
 INSERT INTO scheduled_test_definitions VALUES(7,'statistics');
 INSERT INTO scheduled_test_combination_states(plan_id,account_id,model_key,reason,blocked)
 VALUES(1,62,'B','Combined failure',true)`)
	require.NoError(t, err)
	r := NewChannelQualityRepository(db)
	page, err := r.ListQualityAccounts(ctx, service.ChannelQualityFilter{AccountID: 62, Model: "B", ProblemsOnly: true})
	require.NoError(t, err)
	require.Len(t, page.Items, 1)
	require.Len(t, page.Items[0].Controls, 1)
	c := page.Items[0].Controls[0]
	require.Equal(t, "B", c.Model)
	require.Equal(t, "model", c.Scope)
	require.True(t, c.Blocking)
	page, err = r.ListQualityAccounts(ctx, service.ChannelQualityFilter{AccountID: 62, Model: "A", ProblemsOnly: true})
	require.NoError(t, err)
	require.Empty(t, page.Items)
	// Statistics-driven combinations protect the whole account, including a
	// model outside the plan's selected evidence models.
	_, err = db.Exec(`UPDATE scheduled_test_combination_states SET account_wide=true;
 UPDATE scheduled_test_plans SET enabled=false`)
	require.NoError(t, err)
	page, err = r.ListQualityAccounts(ctx, service.ChannelQualityFilter{AccountID: 62, Model: "C", ProblemsOnly: true})
	require.NoError(t, err)
	require.Len(t, page.Items, 1)
	require.Len(t, page.Items[0].Controls, 1)
	c = page.Items[0].Controls[0]
	require.Equal(t, "B", c.Model)
	require.Equal(t, "account", c.Scope)
	require.True(t, c.Blocking)
	_, err = db.Exec(`DELETE FROM scheduled_test_combination_states;
 INSERT INTO scheduled_test_protection_states(plan_id,account_id,test_definition_id,model_key,reason,blocked,completed,result_id,verdict)
 VALUES(1,62,7,'B','Low cache rate',true,true,8,'fail')`)
	require.NoError(t, err)
	page, err = r.ListQualityAccounts(ctx, service.ChannelQualityFilter{AccountID: 62, Model: "C", ProblemsOnly: true})
	require.NoError(t, err)
	require.Len(t, page.Items, 1)
	require.Len(t, page.Items[0].Controls, 1)
	c = page.Items[0].Controls[0]
	require.Equal(t, "B", c.Model)
	require.Equal(t, "account", c.Scope)
	require.True(t, c.Blocking)
}

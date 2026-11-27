//go:build integration

package repository

import (
	"context"
	"encoding/json"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestIntelligenceIndependent429AndArtworkQueue(t *testing.T) {
	ctx := context.Background()
	r := &scheduledTestResultRepository{db: integrationDB}
	id := func(query string, args ...any) int64 {
		t.Helper()
		var n int64
		require.NoError(t, integrationDB.QueryRowContext(ctx, query, args...).Scan(&n))
		return n
	}
	exec := func(query string, args ...any) {
		t.Helper()
		_, err := integrationDB.ExecContext(ctx, query, args...)
		require.NoError(t, err)
	}
	g := id(`INSERT INTO groups(name,platform) VALUES($1,'openai') RETURNING id`, uuid.NewString())
	a := id(`INSERT INTO accounts(name,platform,type) VALUES('test','openai','oauth') RETURNING id`)
	p := id(`INSERT INTO scheduled_test_plans(group_id,group_ids,model_ids,target_mode,protection) VALUES($1,ARRAY[$1]::bigint[],ARRAY['m','n'],'all_accounts','{"intelligence":{"version":"v"}}') RETURNING id`, g)
	t.Cleanup(func() {
		exec(`DELETE FROM scheduled_test_plans WHERE id=$1`, p)
		exec(`DELETE FROM accounts WHERE id=$1`, a)
		exec(`DELETE FROM groups WHERE id=$1`, g)
	})
	exec(`INSERT INTO account_groups(account_id,group_id) VALUES($1,$2)`, a, g)
	row := func(model, run, version string, status int) int64 {
		ev, _ := json.Marshal(service.ScheduledTestExecutionInfo{Intelligence: &service.IntelligenceEvidence{PolicyVersion: version, CandyHTTPStatus: status}})
		return id(`INSERT INTO scheduled_test_results(plan_id,account_id,model_id,reasoning_effort,run_id,execution_info,status) VALUES($1,$2,$3,'high',$4,$5,'failed') RETURNING id`, p, a, model, run, ev)
	}
	row("m", "first", "v", 429)
	row("n", "other", "v", 200)
	row("m", "second", "v", 429)
	third := row("m", "third", "v", 429)
	prev := func(target int64, want int) {
		n, err := r.PreviousIntelligence429(ctx, &service.ScheduledTestResult{ID: target, PlanID: p, AccountID: &a, ModelID: "m", ReasoningEffort: "high", RunID: "current"}, "v")
		require.NoError(t, err)
		require.Equal(t, want, n)
	}
	prev(third, 2)
	row("m", "unobserved", "v", 0)
	next := row("m", "next", "v", 429)
	prev(next, 0)
	row("m", "old", "old", 429)
	last := row("m", "current", "v", 200)
	prev(last, 0)
	now := time.Now().UTC()
	past := now.Add(-time.Minute)
	ev, _ := json.Marshal(service.ScheduledTestExecutionInfo{Intelligence: &service.IntelligenceEvidence{PolicyVersion: "v", AutoVerdict: "pass", Artwork: "missing", ArtworkRetryAt: &past}})
	exec(`UPDATE scheduled_test_results SET execution_info=$2 WHERE id=$1`, last, ev)
	due, err := r.DueIntelligenceArtwork(ctx, now)
	require.NoError(t, err)
	require.Contains(t, due, last)
	eligible := func(version string, want bool) {
		actual, err := r.IntelligenceTargetEligible(ctx, last, p, a, version)
		require.NoError(t, err)
		require.Equal(t, want, actual)
	}
	eligible("v", true)
	eligible("old", false)
	exec(`DELETE FROM account_groups WHERE account_id=$1 AND group_id=$2`, a, g)
	eligible("v", false)
	due, err = r.DueIntelligenceArtwork(ctx, now)
	require.NoError(t, err)
	require.NotContains(t, due, last)
}

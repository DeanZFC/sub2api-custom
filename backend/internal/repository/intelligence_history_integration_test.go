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
	row := func(model, run, version string, status int, checks ...service.IntelligenceCheck) int64 {
		ev, _ := json.Marshal(service.ScheduledTestExecutionInfo{Intelligence: &service.IntelligenceEvidence{PolicyVersion: version, CandyHTTPStatus: status, Checks: checks}})
		return id(`INSERT INTO scheduled_test_results(plan_id,account_id,model_id,reasoning_effort,run_id,execution_info,status) VALUES($1,$2,$3,'high',$4,$5,'failed') RETURNING id`, p, a, model, run, ev)
	}
	// The same candy transport evidence across the four-check and two-check
	// protocols still represents independently rate-limited rounds.
	currentChecks := []service.IntelligenceCheck{{Key: "candy", Status: "unknown"}, {Key: "http_429", Status: "unknown"}}
	legacyChecks := []service.IntelligenceCheck{{Key: "fingerprint", Status: "pass"}, {Key: "codex_state", Status: "unknown"}, currentChecks[0], currentChecks[1]}
	row("m", "first", "v", 429, legacyChecks...)
	row("n", "other", "v", 200)
	row("m", "second", "v", 429, currentChecks...)
	third := row("m", "third", "v", 429, currentChecks...)
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
	// A page of older retries whose account lost text capability must leave
	// room for the next eligible result without changing its original evidence.
	older := past.Add(-time.Hour)
	olderEvidence, err := json.Marshal(service.ScheduledTestExecutionInfo{Intelligence: &service.IntelligenceEvidence{
		PolicyVersion: "v", AutoVerdict: "pass", Artwork: "missing", ArtworkRetryAt: &older, ArtworkAttempts: 1,
		Checks: []service.IntelligenceCheck{{Key: "candy", Status: "pass", Detail: "21"}},
	}})
	require.NoError(t, err)
	var parked []int64
	for i := 0; i < 20; i++ {
		rid := row("n", uuid.NewString(), "v", 200)
		exec(`UPDATE scheduled_test_results SET execution_info=$2 WHERE id=$1`, rid, olderEvidence)
		parked = append(parked, rid)
	}
	due, err = r.DueIntelligenceArtwork(ctx, now)
	require.NoError(t, err)
	require.ElementsMatch(t, parked, due)
	nextTry := now.Add(15 * time.Minute)
	for _, rid := range parked {
		require.NoError(t, r.DeferIntelligenceArtwork(ctx, rid, now, nextTry))
	}
	require.NoError(t, r.DeferIntelligenceArtwork(ctx, parked[0], now, now.Add(time.Hour)))
	var sameEvidence bool
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT (execution_info #- '{intelligence,artwork_retry_at}') = ($2::jsonb #- '{intelligence,artwork_retry_at}')
 AND (execution_info->'intelligence'->>'artwork_retry_at')::timestamptz = $3
 FROM scheduled_test_results WHERE id=$1`, parked[0], olderEvidence, nextTry).Scan(&sameEvidence))
	require.True(t, sameEvidence)
	due, err = r.DueIntelligenceArtwork(ctx, now)
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

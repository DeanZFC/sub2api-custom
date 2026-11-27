//go:build integration

package repository

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

// Exercise dispatch, frozen group authorization, latest-completed selection,
// independent retests, removal, review, retention, and revocation together.
func TestPublicIntelligenceFrozenRounds(t *testing.T) {
	ctx := context.Background()
	db := integrationDB
	r := &scheduledTestResultRepository{db: db}
	plans := &scheduledTestPlanRepository{db: db}
	id := func(query string, args ...any) int64 {
		t.Helper()
		var n int64
		require.NoError(t, db.QueryRowContext(ctx, query, args...).Scan(&n))
		return n
	}
	exec := func(query string, args ...any) {
		t.Helper()
		_, err := db.ExecContext(ctx, query, args...)
		require.NoError(t, err)
	}
	groupName := uuid.NewString()
	g1 := id(`INSERT INTO groups(name,platform,is_exclusive) VALUES($1,'openai',true) RETURNING id`, groupName)
	g2 := id(`INSERT INTO groups(name,platform,is_exclusive) VALUES($1,'openai',true) RETURNING id`, uuid.NewString())
	a1 := id(`INSERT INTO accounts(name,platform,type) VALUES('private-one','openai','oauth') RETURNING id`)
	a2 := id(`INSERT INTO accounts(name,platform,type) VALUES('private-two','openai','oauth') RETURNING id`)
	u := id(`INSERT INTO users(email,password_hash) VALUES($1,'test') RETURNING id`, uuid.NewString()+"@test.invalid")
	t.Cleanup(func() {
		exec(`DELETE FROM users WHERE id=$1`, u)
		exec(`DELETE FROM accounts WHERE id=ANY($1)`, pq.Array([]int64{a1, a2}))
		exec(`DELETE FROM groups WHERE id=ANY($1)`, pq.Array([]int64{g1, g2}))
	})
	exec(`INSERT INTO account_groups(account_id,group_id) VALUES($1,$3),($1,$4),($2,$3)`, a1, a2, g1, g2)
	exec(`INSERT INTO user_allowed_groups(user_id,group_id) VALUES($1,$2)`, u, g1)
	def := id(`SELECT id FROM scheduled_test_definitions WHERE key='pelican'`)
	plan, err := plans.Create(ctx, &service.ScheduledTestPlan{Name: "Private standard", GroupIDs: []int64{g1, g2}, GroupID: &g1, TestDefinitionIDs: []int64{def}, TestDefinitionID: &def, TargetMode: "all_accounts", ModelID: "test-model", ModelIDs: []string{"test-model", "other-model"}, Enabled: true, MaxResults: 20, ReasoningEffort: "high", CronExpression: "0 * * * *", Protection: service.ScheduledTestProtectionConfig{Intelligence: &service.IntelligencePolicy{Version: "fixture"}}})
	require.NoError(t, err)
	t.Cleanup(func() { exec(`DELETE FROM scheduled_test_plans WHERE id=$1`, plan.ID) })
	selectedModel := "test-model"
	targetFor := func(account int64, model, effort string) *service.ScheduledTestResult {
		now := time.Now().UTC()
		return &service.ScheduledTestResult{PlanID: plan.ID, AccountID: &account, GroupID: &g1, TestDefinitionID: &def, OutputKind: "html", TargetMode: "all_accounts", StartedAt: now, FinishedAt: now, Status: "pending", ModelID: model, ReasoningEffort: effort}
	}
	launch := func(retest bool, accounts ...int64) (string, []*service.ScheduledTestResult) {
		t.Helper()
		plan.IntelligenceRetest = retest
		var targets []*service.ScheduledTestResult
		run := uuid.NewString()
		for _, a := range accounts {
			targets = append(targets, targetFor(a, selectedModel, "high"))
		}
		rows, err := r.BeginRun(ctx, plan, run, targets)
		require.NoError(t, err)
		return run, rows
	}
	finish := func(row *service.ScheduledTestResult) {
		t.Helper()
		exec(`UPDATE scheduled_test_results SET status='success',finished_at=now(),output_html='<svg/>',execution_info='{"intelligence":{"auto_verdict":"pass","artwork":"default","checks":[{"key":"candy","status":"pass","detail":"21"},{"key":"http_429","status":"pass"}]}}' WHERE id=$1`, row.ID)
	}
	recent := func(g int64) []service.PublicIntelligenceRound {
		t.Helper()
		v, err := r.PublicIntelligenceRounds(ctx, u, g, "")
		require.NoError(t, err)
		return v
	}
	run1, rows := launch(false, a1, a2)
	finish(rows[0])
	require.Empty(t, recent(g1))
	finish(rows[1])
	settings := service.NewSettingService(NewSettingRepository(integrationEntClient), nil)
	setVisibility := func(visible bool) {
		t.Helper()
		require.NoError(t, settings.SetIntelligenceRoundsVisible(ctx, visible))
	}
	t.Cleanup(func() { exec(`DELETE FROM settings WHERE key=$1`, service.SettingKeyIntelligenceRoundsVisible) })
	checkHidden := func() {
		t.Helper()
		directory, err := r.PublicIntelligenceDirectory(ctx, u)
		require.NoError(t, err)
		require.False(t, directory.RoundsVisible)
		for _, g := range directory.Groups {
			require.Empty(t, g.Models)
			require.True(t, g.HasLegacyResults)
		}
		require.Empty(t, recent(g1))
		_, err = r.PublicIntelligenceRoundDetails(ctx, u, g1, "", run1, 1)
		require.ErrorIs(t, err, sql.ErrNoRows)
		summary, err := r.PublicIntelligenceSummary(ctx, u, g1, "", 1)
		require.NoError(t, err)
		require.Zero(t, summary.Rounds)
		require.Empty(t, summary.Items)
		_, err = r.PublicIntelligenceArtwork(ctx, u, g1, rows[0].ID)
		require.ErrorIs(t, err, sql.ErrNoRows)
	}
	checkHidden()
	setVisibility(true)
	rr := recent(g1)
	require.Len(t, rr, 1)
	require.Equal(t, "pass", rr[0].Verdict)
	require.Equal(t, 2, rr[0].Accounts)
	require.Empty(t, recent(g2))
	_, err = r.PublicIntelligenceArtwork(ctx, u, g2, rows[0].ID)
	require.ErrorIs(t, err, sql.ErrNoRows)
	_, err = r.PublicIntelligenceArtwork(ctx, u, g1, rows[0].ID)
	require.NoError(t, err)
	directory, err := r.PublicIntelligenceDirectory(ctx, u)
	require.NoError(t, err)
	require.True(t, directory.RoundsVisible)
	require.Contains(t, directory.Groups, service.PublicIntelligenceGroup{ID: g1, Name: groupName, Models: []string{"other-model", "test-model"}})
	d, err := r.PublicIntelligenceRoundDetails(ctx, u, g1, "", run1, 1)
	require.NoError(t, err)
	require.Len(t, d.Accounts, 2)
	require.Equal(t, []service.PublicIntelligenceCheck{{Key: "candy", Status: "pass", Detail: "21"}, {Key: "rate_limit", Status: "pass"}}, d.Accounts[0].Models[0].Checks)
	setVisibility(false)
	checkHidden()
	setVisibility(true)
	// Earlier rounds keep their recorded fingerprint and Codex checks.
	exec(`UPDATE scheduled_test_results SET execution_info=jsonb_set(execution_info,'{intelligence,checks}',
	 '[{"key":"fingerprint","status":"unknown","detail":"insufficient"},{"key":"codex_state","status":"pass"}]'::jsonb
	 || (execution_info->'intelligence'->'checks')) WHERE id=$1`, rows[0].ID)
	historical, err := r.PublicIntelligenceRoundDetails(ctx, u, g1, "", run1, 1)
	require.NoError(t, err)
	var checkCounts []int
	for _, account := range historical.Accounts {
		checks := account.Models[0].Checks
		checkCounts = append(checkCounts, len(checks))
		if len(checks) == 4 {
			require.Equal(t, service.PublicIntelligenceCheck{Key: "fingerprint", Status: "unknown", Detail: "insufficient"}, checks[0])
			require.Equal(t, service.PublicIntelligenceCheck{Key: "codex_state", Status: "pass"}, checks[1])
		}
	}
	require.ElementsMatch(t, []int{4, 2}, checkCounts)
	summary, err := r.PublicIntelligenceSummary(ctx, u, g1, "", 1)
	require.NoError(t, err)
	require.Equal(t, 2, summary.Participations)
	require.Equal(t, 2, summary.Counts.Pass)
	_, later := launch(false, a1, a2)
	require.Equal(t, run1, recent(g1)[0].ID)
	var anchor string
	require.NoError(t, db.QueryRow(`SELECT latest_run_id FROM scheduled_test_plans WHERE id=$1`, plan.ID).Scan(&anchor))
	_, target := launch(true, a1)
	finish(target[0])
	var after string
	require.NoError(t, db.QueryRow(`SELECT latest_run_id FROM scheduled_test_plans WHERE id=$1`, plan.ID).Scan(&after))
	require.Equal(t, anchor, after)
	require.Equal(t, run1, recent(g1)[0].ID)
	require.NoError(t, r.PruneOldResults(ctx, plan.ID, 0))
	// Historical attribution survives the account moving. No projection follows
	// the moved account to an unauthorized pool through the legacy result API.
	exec(`DELETE FROM account_groups WHERE account_id=$1 AND group_id=$2`, a1, g1)
	_, err = r.PublicIntelligenceArtwork(ctx, u, g1, rows[0].ID)
	require.NoError(t, err)
	legacy, err := r.ListVisibleByGroup(ctx, u)
	require.NoError(t, err)
	for _, v := range legacy {
		require.NotEqual(t, plan.ID, v.PlanID)
	}
	exec(`INSERT INTO intelligence_observations(result_id,plan_id,account_id,model_key,policy_version,run_id,started_at,finished_at,verdict,auto_isolate,recovery_passes,cooldown_minutes) VALUES($1,$2,$3,'test-model','fixture',$4,NOW(),NOW(),'pass',false,2,30)`, rows[0].ID, plan.ID, a1, run1)
	exec(`INSERT INTO intelligence_artwork_reviews(result_id,verdict,admin_id) VALUES($1,'fail',$2)`, rows[0].ID, u)
	require.Equal(t, "degraded", recent(g1)[0].Verdict)
	summary, err = r.PublicIntelligenceSummary(ctx, u, g1, "test-model", 1)
	require.NoError(t, err)
	require.Equal(t, 1, summary.Counts.Degraded)
	require.Equal(t, []service.PublicIntelligenceModelCounts{{Model: "test-model", Pass: 1, Degraded: 1}}, recent(g1)[0].ModelCounts)
	markup, err := r.PublicIntelligenceArtwork(ctx, u, g1, rows[0].ID)
	require.NoError(t, err)
	require.Equal(t, "<svg/>", markup)
	finish(later[0])
	exec(`DELETE FROM scheduled_test_results WHERE id=$1`, later[1].ID)
	last := recent(g1)
	require.Len(t, last, 2)
	require.Equal(t, "unknown", last[1].Verdict)
	require.Equal(t, 2, last[1].Accounts)
	// A missing-artwork retry runs on the original row after publication.
	// It neither hides that round in flight nor moves it ahead of newer rounds.
	exec(`UPDATE scheduled_test_results SET output_html='',execution_info=jsonb_set(execution_info,'{intelligence,artwork}','"missing"') WHERE id=$1`, rows[1].ID)
	exec(`UPDATE scheduled_test_results SET status='running' WHERE id=$1`, rows[1].ID)
	inFlight := recent(g1)
	require.Len(t, inFlight, 2)
	require.Equal(t, run1, inFlight[0].ID)
	require.Equal(t, rr[0].FinishedAt, inFlight[0].FinishedAt)
	finish(rows[1])
	afterArtwork := recent(g1)
	require.Equal(t, run1, afterArtwork[0].ID)
	require.Equal(t, rr[0].FinishedAt, afterArtwork[0].FinishedAt)
	selectedModel = "other-model"
	for i := 0; i < 21; i++ {
		_, newer := launch(false, a2)
		finish(newer[0])
	}
	require.Len(t, recent(g1), 20)
	filtered, err := r.PublicIntelligenceRounds(ctx, u, g1, "test-model")
	require.NoError(t, err)
	require.Len(t, filtered, 2)
	require.Equal(t, run1, filtered[0].ID)
	require.NoError(t, r.PruneOldResults(ctx, plan.ID, 0))
	_, err = r.PublicIntelligenceArtwork(ctx, u, g1, rows[0].ID)
	require.NoError(t, err)
	// One account can participate in multiple models and at multiple efforts.
	// Each model counts that account once and keeps failures out of its denominator.
	exec(`INSERT INTO account_groups(account_id,group_id) VALUES($1,$2)`, a1, g1)
	mixedRun := uuid.NewString()
	mixed, err := r.BeginRun(ctx, plan, mixedRun, []*service.ScheduledTestResult{
		targetFor(a1, "test-model", "high"), targetFor(a1, "test-model", "low"),
		targetFor(a2, "test-model", "high"), targetFor(a1, "other-model", "high"),
		targetFor(a2, "other-model", "high"),
	})
	require.NoError(t, err)
	finish(mixed[0])
	finish(mixed[1])
	finish(mixed[2])
	exec(`UPDATE scheduled_test_results SET status='success', execution_info='{"intelligence":{"auto_verdict":"confirmed"}}' WHERE id=$1`, mixed[3].ID)
	exec(`UPDATE scheduled_test_results SET status='failed', execution_info='{"intelligence":{"auto_verdict":"confirmed"}}' WHERE id=$1`, mixed[4].ID)
	wantModels := []service.PublicIntelligenceModelCounts{{Model: "other-model", Degraded: 1}, {Model: "test-model", Pass: 2}}
	rounds := recent(g1)
	require.Equal(t, wantModels, rounds[len(rounds)-1].ModelCounts)
	mixedDetail, err := r.PublicIntelligenceRoundDetails(ctx, u, g1, "", mixedRun, 1)
	require.NoError(t, err)
	require.Equal(t, wantModels, mixedDetail.Round.ModelCounts)
	single, err := r.PublicIntelligenceRounds(ctx, u, g1, "other-model")
	require.NoError(t, err)
	require.Equal(t, wantModels[:1], single[len(single)-1].ModelCounts)
	exec(`UPDATE scheduled_test_results SET status='failed' WHERE id=$1`, mixed[3].ID)
	single, err = r.PublicIntelligenceRounds(ctx, u, g1, "other-model")
	require.NoError(t, err)
	require.Equal(t, []service.PublicIntelligenceModelCounts{{Model: "other-model"}}, single[len(single)-1].ModelCounts)
	exec(`DELETE FROM user_allowed_groups WHERE user_id=$1 AND group_id=$2`, u, g1)
	require.Empty(t, recent(g1))
	_, err = r.PublicIntelligenceArtwork(ctx, u, g1, rows[0].ID)
	require.ErrorIs(t, err, sql.ErrNoRows)
}

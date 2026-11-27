//go:build integration

package repository

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestGroupProbeActivityAndLeases(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	group := createEntGroup(t, ctx, client, "group-probe-activity")
	user := createEntUser(t, ctx, client, "group-probe-activity@example.org")
	acct, err := client.Account.Create().SetName("probe-account").SetType("apikey").SetPlatform("openai").Save(ctx)
	require.NoError(t, err)
	key := &service.APIKey{UserID: user.ID, GroupID: &group.ID, Name: "probe-activity", Key: "probe-activity-key", Status: service.StatusActive}
	require.NoError(t, NewAPIKeyRepository(client, integrationDB).Create(ctx, key))
	repo := NewGroupProbeRepository(integrationDB)
	const model = "model-a"
	exec := func(q string, args ...any) {
		t.Helper()
		_, err := integrationDB.ExecContext(ctx, q, args...)
		require.NoError(t, err)
	}
	poll := func() { exec("UPDATE group_probes SET next_poll_at=now() WHERE group_id=$1", group.ID) }
	claim := func(expect bool) *service.GroupProbeJob {
		t.Helper()
		job, _, err := repo.Claim(ctx)
		require.NoError(t, err)
		if expect {
			require.NotNil(t, job)
			require.Equal(t, group.ID, job.GroupID)
		} else {
			require.Nil(t, job)
		}
		return job
	}
	require.NoError(t, repo.Configure(ctx, group.ID, model, true, service.GroupProbeRuleUpdate{}))
	t.Cleanup(func() { _, _ = integrationDB.Exec("DELETE FROM group_probes WHERE group_id=$1", group.ID) })
	// An active quality sample is not a customer's request.
	exec(`INSERT INTO usage_logs(group_id,account_id,model,inbound_endpoint,http_status,request_id)
        VALUES($1,$2,$3,'/internal/channel-quality',200,'group-probe-quality')`, group.ID, acct.ID, model)
	first := claim(true)
	require.ErrorIs(t, repo.Request(ctx, group.ID), service.ErrGroupProbeBusy)
	require.ErrorIs(t, repo.Configure(ctx, group.ID, "new-model", true, service.GroupProbeRuleUpdate{}), service.ErrGroupProbeBusy)
	claim(false)
	require.NoError(t, repo.Finish(ctx, first, service.GroupProbeResult{Success: true}))
	var end, next time.Time
	readSchedule := func() {
		t.Helper()
		require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT round_until,next_poll_at FROM group_probes WHERE group_id=$1", group.ID).Scan(&end, &next))
	}
	readSchedule()
	require.Equal(t, 10*time.Minute-30*time.Second, end.Sub(next))
	job, wait, err := repo.Claim(ctx)
	require.NoError(t, err)
	require.Nil(t, job)
	require.Positive(t, wait)
	require.LessOrEqual(t, wait, 30*time.Second)

	// Traffic recovers during the round. Five free calls will suppress a new
	// round, but the running round retains its original deadline and cadence.
	exec(`INSERT INTO usage_logs(group_id,user_id,api_key_id,account_id,model,requested_model,http_status,request_id,output_tokens)
        SELECT $1,$2,$3,$4,'mapped',$5,200,'group-probe-user-'||i,1 FROM generate_series(1,5) i`, group.ID, user.ID, key.ID, acct.ID, model)
	exec("UPDATE group_probes SET round_until=now()+interval '8 minutes 45 seconds',next_poll_at=now() WHERE group_id=$1", group.ID)
	readSchedule()
	deadline := end
	// A restarted worker resumes the same round after a missed tick.
	resumed, _, err := NewGroupProbeRepository(integrationDB).Claim(ctx)
	require.NoError(t, err)
	require.NotNil(t, resumed)
	claim(false)
	require.NoError(t, repo.Finish(ctx, resumed, service.GroupProbeResult{Success: true}))
	readSchedule()
	require.True(t, deadline.Equal(end))
	require.Equal(t, 10*time.Minute-90*time.Second, end.Sub(next))
	require.NoError(t, repo.Configure(ctx, group.ID, model, true, service.GroupProbeRuleUpdate{}))
	readSchedule()
	require.True(t, deadline.Equal(end), "repeating a save must not restart the round")
	require.NoError(t, repo.Request(ctx, group.ID))
	manual := claim(true)
	require.NoError(t, repo.Finish(ctx, manual, service.GroupProbeResult{Success: true}))
	readSchedule()
	require.True(t, deadline.Equal(end), "a manual probe must not extend the round")
	// Even when a slow probe finishes near the deadline, only ticks strictly
	// before the deadline may start another probe.
	exec("UPDATE group_probes SET round_until=now()+interval '10 seconds',next_poll_at=now() WHERE group_id=$1", group.ID)
	finalTick := claim(true)
	require.NoError(t, repo.Finish(ctx, finalTick, service.GroupProbeResult{Success: true}))
	readSchedule()
	require.True(t, end.Equal(next))
	exec("UPDATE group_probes SET round_until=now()-interval '6 minutes', last_probe_at=now()-interval '12 minutes' WHERE group_id=$1", group.ID)
	poll()
	claim(false)
	rows, err := repo.List(ctx)
	require.NoError(t, err)
	row := func() service.GroupProbe {
		t.Helper()
		for _, r := range rows {
			if r.GroupID == group.ID {
				return r
			}
		}
		t.Fatal("group missing")
		return service.GroupProbe{}
	}
	require.NotNil(t, row().LastUserAt)
	require.EqualValues(t, 5, row().IdleWindowMinutes)
	require.EqualValues(t, 5, row().MinRequests)
	require.EqualValues(t, 10, row().DurationMinutes)
	require.EqualValues(t, 30, row().IntervalSeconds)
	require.Nil(t, row().RoundUntil)
	// Two server failures after the last success trigger an early probe.
	exec(`INSERT INTO ops_error_logs(group_id,api_key_id,request_id,model,status_code,error_phase,error_type)
         SELECT $1,$2,'group-probe-fail-'||i,$3,503,'upstream','error' FROM generate_series(1,2) i`, group.ID, key.ID, model)
	poll()
	second := claim(true)
	require.NoError(t, repo.Finish(ctx, second, service.GroupProbeResult{Detail: "incomplete_stream"}))
	rows, err = repo.List(ctx)
	require.NoError(t, err)
	require.Equal(t, "confirming", row().Status)
	poll()
	claim(false)
	exec("UPDATE group_probes SET last_probe_at=now()-interval '61 seconds' WHERE group_id=$1", group.ID)
	poll()
	third := claim(true)
	require.NoError(t, repo.Finish(ctx, third, service.GroupProbeResult{Detail: "incomplete_stream"}))
	rows, err = repo.List(ctx)
	require.NoError(t, err)
	require.Equal(t, "failed", row().Status)
	require.NoError(t, repo.Request(ctx, group.ID))
	fourth := claim(true)
	require.NoError(t, repo.Finish(ctx, fourth, service.GroupProbeResult{Success: true}))
	rows, err = repo.List(ctx)
	require.NoError(t, err)
	require.Equal(t, "recovered", row().Status)
	require.NoError(t, repo.Request(ctx, group.ID))
	fifth := claim(true)
	require.NoError(t, repo.Finish(ctx, fifth, service.GroupProbeResult{Deferred: &service.QualityDeferredError{Reason: "budget_exhausted", Until: time.Now().Add(time.Hour)}}))
	rows, err = repo.List(ctx)
	require.NoError(t, err)
	require.Equal(t, "stale", row().Status)
	require.True(t, row().Result.Success)
	require.NoError(t, repo.Configure(ctx, group.ID, "model-b", false, service.GroupProbeRuleUpdate{}))
	require.ErrorIs(t, repo.Request(ctx, group.ID), service.ErrGroupProbeDisabled)
	require.NoError(t, repo.Finish(ctx, fourth, service.GroupProbeResult{Detail: "late result"}))
	rows, err = repo.List(ctx)
	require.NoError(t, err)
	require.Nil(t, row().Result)
	require.Nil(t, row().LastUserAt)
}

func TestGroupProbeTrafficThreshold(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	group := createEntGroup(t, ctx, client, "group-probe-rules")
	other := createEntGroup(t, ctx, client, "group-probe-rules-other")
	user := createEntUser(t, ctx, client, "group-probe-rules@example.org")
	account, err := client.Account.Create().SetName("probe-rules-account").SetType("apikey").SetPlatform("openai").Save(ctx)
	require.NoError(t, err)
	key := &service.APIKey{UserID: user.ID, GroupID: &group.ID, Name: "probe-rules", Key: "probe-rules-key", Status: service.StatusActive}
	require.NoError(t, NewAPIKeyRepository(client, integrationDB).Create(ctx, key))
	exec := func(q string, args ...any) {
		t.Helper()
		_, err := integrationDB.ExecContext(ctx, q, args...)
		require.NoError(t, err)
	}
	t.Cleanup(func() {
		exec("DELETE FROM ops_error_logs WHERE group_id=$1", group.ID)
		exec("DELETE FROM usage_logs WHERE group_id IN ($1,$2)", group.ID, other.ID)
		exec("DELETE FROM api_keys WHERE id=$1", key.ID)
		exec("DELETE FROM groups WHERE id IN ($1,$2)", group.ID, other.ID)
		exec("DELETE FROM users WHERE id=$1", user.ID)
		exec("DELETE FROM accounts WHERE id=$1", account.ID)
	})
	repo := NewGroupProbeRepository(integrationDB)
	const model = "rules-model"
	rule := service.GroupProbeRuleUpdate{IdleWindowMinutes: new(int32(5)), MinRequests: new(int32(3)), DurationMinutes: new(int32(12)), IntervalSeconds: new(int32(17))}
	require.NoError(t, repo.Configure(ctx, group.ID, model, true, rule))
	require.NoError(t, repo.Configure(ctx, group.ID, model, false, service.GroupProbeRuleUpdate{}))
	require.NoError(t, repo.Configure(ctx, group.ID, model, true, service.GroupProbeRuleUpdate{}))

	// One completed call and one failed call with partial usage and duplicate error logs.
	exec(`INSERT INTO usage_logs(group_id,user_id,api_key_id,account_id,requested_model,model,http_status,request_id,created_at) VALUES
		($1,$2,$3,$4,$5,'mapped',200,'completed',now()),
		($1,$2,$3,$4,$5,'mapped',200,'client:partial',now()),
		($1,$2,$3,$4,$5,'mapped',200,'older',now()-interval '8 minutes'),
		($1,$2,$3,$4,'another-model','mapped',200,'unrelated-model',now()),
		($6,$2,$3,$4,$5,'mapped',200,'unrelated-group',now())`, group.ID, user.ID, key.ID, account.ID, model, other.ID)
	exec(`INSERT INTO usage_logs(group_id,account_id,model,http_status,request_id,inbound_endpoint)
		VALUES($1,$2,$3,200,'internal-probe','/internal/channel-quality')`, group.ID, account.ID, model)
	exec(`INSERT INTO ops_error_logs(group_id,api_key_id,model,request_id,client_request_id,status_code,error_phase,error_type,is_count_tokens) VALUES
		($1,$2,$3,'local-id','partial',400,'request','error',false),
		($1,$2,$3,'local-id','partial',400,'request','error',false),
		($1,$2,$3,'count-tokens',NULL,400,'request','error',true)`, group.ID, key.ID, model)
	// Move the end of the previous round to exercise the observation period.
	claimAfter := func(minutes int) *service.GroupProbeJob {
		t.Helper()
		exec("UPDATE group_probes SET round_until=now()-make_interval(mins => $2),last_probe_at=now()-make_interval(mins => $2),next_poll_at=now() WHERE group_id=$1", group.ID, minutes)
		job, wait, err := repo.Claim(ctx)
		require.NoError(t, err)
		require.Zero(t, wait)
		return job
	}
	require.Nil(t, claimAfter(4), "the five-minute cooldown still applies")
	job := claimAfter(6)
	require.NotNil(t, job, "two requests in five minutes is below three")
	require.NoError(t, repo.Finish(ctx, job, service.GroupProbeResult{Success: true}))
	var seconds float64
	require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT EXTRACT(epoch FROM round_until-next_poll_at) FROM group_probes WHERE group_id=$1", group.ID).Scan(&seconds))
	require.Equal(t, (12*time.Minute - 17*time.Second).Seconds(), seconds)

	// Widening just the window admits the older third call and leaves the threshold intact.
	require.NoError(t, repo.Configure(ctx, group.ID, model, true, service.GroupProbeRuleUpdate{IdleWindowMinutes: new(int32(15))}))
	require.Nil(t, claimAfter(16), "exactly three requests meets the threshold")
	rows, err := repo.List(ctx)
	require.NoError(t, err)
	var saved *service.GroupProbe
	for i := range rows {
		if rows[i].GroupID == group.ID {
			saved = &rows[i]
			break
		}
	}
	require.NotNil(t, saved)
	require.EqualValues(t, 15, saved.IdleWindowMinutes)
	require.EqualValues(t, 3, saved.MinRequests)
	require.EqualValues(t, 12, saved.DurationMinutes)
	require.EqualValues(t, 17, saved.IntervalSeconds)
	require.Equal(t, "healthy", saved.Status)
	require.NoError(t, repo.Configure(ctx, group.ID, model, true, service.GroupProbeRuleUpdate{MinRequests: new(int32(4))}))
	require.Nil(t, claimAfter(12), "the edited fifteen-minute cooldown still applies")
	job = claimAfter(16)
	require.NotNil(t, job, "three requests is below the updated threshold of four")
	require.NoError(t, repo.Finish(ctx, job, service.GroupProbeResult{Success: true}))
}

func TestGroupProbeWorkerWakesForShortIntervals(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	group := createEntGroup(t, ctx, client, "group-probe-worker-cadence")
	t.Cleanup(func() {
		_, err := integrationDB.ExecContext(ctx, "DELETE FROM groups WHERE id=$1", group.ID)
		require.NoError(t, err)
	})
	repo := NewGroupProbeRepository(integrationDB)
	starts := make(chan time.Time, 2)
	release := make(chan struct{})
	execute := func(ctx context.Context, _ *service.Group, _ string) service.GroupProbeResult {
		select {
		case starts <- time.Now():
		case <-ctx.Done():
		}
		select {
		case <-release:
		case <-ctx.Done():
		}
		return service.GroupProbeResult{Success: true}
	}
	worker := service.NewGroupProbeService(repo, NewGroupRepository(client, integrationDB), nil, execute)
	t.Cleanup(worker.Stop)
	require.NoError(t, worker.Configure(ctx, group.ID, "test-model", true, service.GroupProbeRuleUpdate{IntervalSeconds: new(int32(2))}))
	awaitStart := func() time.Time {
		t.Helper()
		select {
		case at := <-starts:
			return at
		case <-time.After(8 * time.Second):
			t.Fatal("worker missed the configured probe interval")
			return time.Time{}
		}
	}
	first := awaitStart()
	release <- struct{}{}
	second := awaitStart()
	require.Greater(t, second.Sub(first), time.Second)
	// Pause during an active request. The lease still fences the in-flight
	// completion and the disabled group cannot be claimed again.
	require.NoError(t, worker.Configure(ctx, group.ID, "test-model", false, service.GroupProbeRuleUpdate{}))
	require.ErrorIs(t, worker.Configure(ctx, group.ID, "test-model", true, service.GroupProbeRuleUpdate{}), service.ErrGroupProbeBusy)
	close(release)
	require.Eventually(t, func() bool {
		var stopped bool
		err := integrationDB.QueryRowContext(ctx, "SELECT NOT enabled AND round_until IS NULL AND lease_owner IS NULL AND last_probe_at IS NOT NULL FROM group_probes WHERE group_id=$1", group.ID).Scan(&stopped)
		return err == nil && stopped
	}, 5*time.Second, 20*time.Millisecond)
	job, _, err := repo.Claim(ctx)
	require.NoError(t, err)
	require.Nil(t, job)
}

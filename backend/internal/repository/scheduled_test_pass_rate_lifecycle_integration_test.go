//go:build integration

package repository

import (
	"encoding/json"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func passRateLifecycleHeldFixture(t *testing.T) *passRateFixture {
	t.Helper()
	f := newPassRateFixture(t, 2)
	f.result("fail")
	f.result("fail")
	require.True(t, f.summary("model-a").Blocked)
	return f
}

func requirePassRateProjection(t *testing.T, f *passRateFixture, blocked bool) {
	t.Helper()
	var raw []byte
	require.NoError(t, f.db.QueryRowContext(f.ctx, `SELECT extra FROM accounts WHERE id=62`).Scan(&raw))
	var extra map[string]any
	require.NoError(t, json.Unmarshal(raw, &extra))
	require.Equal(t, blocked, (&service.Account{Extra: extra}).ActivePassRateBlocked("model-a"))
}

func TestScheduledTestPassRateLifecycleIntegration(t *testing.T) {
	t.Run("disabling schedule retains evidence and enforcement", func(t *testing.T) {
		f := passRateLifecycleHeldFixture(t)
		before := f.summary("model-a")
		f.plan.Enabled = false
		_, err := f.plans.Update(f.ctx, f.plan)
		require.NoError(t, err)
		after := f.summary("model-a")
		require.True(t, after.Blocked)
		require.Equal(t, before.RuleVersion, after.RuleVersion)
		require.Equal(t, before.Samples, after.Samples)
		requirePassRateProjection(t, f, true)
	})

	t.Run("definition semantic edits immediately replace the evidence window and preserve hold", func(t *testing.T) {
		f := passRateLifecycleHeldFixture(t)
		before := f.summary("model-a")
		definitions := &scheduledTestDefinitionRepository{db: f.db}
		definition, err := definitions.GetByID(f.ctx, f.definitionID)
		require.NoError(t, err)
		definition.Prompt += " Revised ability question."
		_, err = definitions.Update(f.ctx, definition)
		require.NoError(t, err)
		after := f.summary("model-a")
		require.NotEqual(t, before.RuleVersion, after.RuleVersion)
		require.Zero(t, after.Samples)
		require.Nil(t, after.PassRate)
		require.True(t, after.Blocked)
		require.Equal(t, 2, f.evidenceCount(), "historical observations remain available under their original version")
		requirePassRateProjection(t, f, true)
		f.result("pass")
		require.True(t, f.summary("model-a").Blocked)
		f.result("pass")
		require.False(t, f.summary("model-a").Blocked)
	})

	t.Run("definition edit waits for first observation before refreshing its version", func(t *testing.T) {
		f := newPassRateFixture(t, 2)
		row := f.begin()
		row.Status, row.ResponseText = "success", "20"
		require.NoError(t, f.repo.Update(f.ctx, row))
		definitions := &scheduledTestDefinitionRepository{db: f.db}
		definition, err := definitions.GetByID(f.ctx, f.definitionID)
		require.NoError(t, err)
		definition.Prompt += " Changed while the first response completed."
		tx, err := f.db.BeginTx(f.ctx, nil)
		require.NoError(t, err)
		defer tx.Rollback()
		_, err = lockProtectionPlan(f.ctx, tx, f.plan.ID)
		require.NoError(t, err)
		_, err = lockProtectionAccount(f.ctx, tx, 62)
		require.NoError(t, err)
		var blockerPID int
		require.NoError(t, tx.QueryRowContext(f.ctx, `SELECT pg_backend_pid()`).Scan(&blockerPID))
		updated := make(chan error, 1)
		go func() {
			_, err := definitions.Update(f.ctx, definition)
			updated <- err
		}()
		require.Eventually(t, func() bool {
			var waiting bool
			err := f.db.QueryRowContext(f.ctx, `SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE $1=ANY(pg_blocking_pids(pid)))`, blockerPID).Scan(&waiting)
			return err == nil && waiting
		}, 5*time.Second, 10*time.Millisecond, "definition edit must lock referenced plans even before they have a pass-rate state")
		state, err := loadProtectionState(f.ctx, tx, row.ID)
		require.NoError(t, err)
		state.completed, state.automated = true, "fail"
		require.NoError(t, recordScheduledTestPassRate(f.ctx, tx, state))
		require.NoError(t, tx.Commit())
		require.NoError(t, <-updated)
		after := f.summary("model-a")
		require.Zero(t, after.Samples, "first old-definition evidence must not remain in the edited definition window")
		require.Equal(t, 1, f.evidenceCount())
		var originalVersion string
		require.NoError(t, f.db.QueryRowContext(f.ctx, `SELECT rule_version FROM scheduled_test_pass_rate_evidence WHERE result_id=$1`, row.ID).Scan(&originalVersion))
		require.NotEqual(t, originalVersion, after.RuleVersion)
	})

	t.Run("rule edits refresh immediately without waiting for a test", func(t *testing.T) {
		f := passRateLifecycleHeldFixture(t)
		before := f.summary("model-a")
		f.plan.Protection.Rules[0].ExpectedAnswer = "42"
		f.plan.Protection.Rules[0].PassRate.WindowSize = 3
		_, err := f.plans.Update(f.ctx, f.plan)
		require.NoError(t, err)
		after := f.summary("model-a")
		require.NotEqual(t, before.RuleVersion, after.RuleVersion)
		require.Equal(t, 3, after.WindowSize)
		require.Zero(t, after.Samples)
		require.True(t, after.Blocked)
		requirePassRateProjection(t, f, true)
	})

	t.Run("explicit ownership removal clears only its state", func(t *testing.T) {
		for _, action := range []string{"exclude", "remove_rule", "remove_definition", "disable_protection", "change_detection_group"} {
			t.Run(action, func(t *testing.T) {
				f := passRateLifecycleHeldFixture(t)
				f.exec(`UPDATE accounts SET extra=extra||'{"unrelated":"retained"}'::jsonb WHERE id=62`)
				switch action {
				case "exclude":
					f.plan.ExcludedAccountIDs = []int64{62}
				case "remove_rule":
					f.plan.Protection.Rules = nil
				case "remove_definition":
					var anotherDefinition int64
					require.NoError(t, f.db.QueryRowContext(f.ctx, `SELECT id FROM scheduled_test_definitions WHERE key='pelican'`).Scan(&anotherDefinition))
					f.plan.TestDefinitionID = &anotherDefinition
					f.plan.TestDefinitionIDs = []int64{anotherDefinition}
				case "disable_protection":
					f.plan.Protection.Enabled = false
				case "change_detection_group":
					f.exec(`INSERT INTO groups(id,name) VALUES(9,'Other detection group')`)
					group := int64(9)
					f.plan.GroupID = &group
					f.plan.GroupIDs = []int64{group}
				}
				_, err := f.plans.Update(f.ctx, f.plan)
				require.NoError(t, err)
				var count int
				require.NoError(t, f.db.QueryRowContext(f.ctx, `SELECT COUNT(*) FROM scheduled_test_pass_rate_states WHERE plan_id=$1`, f.plan.ID).Scan(&count))
				require.Zero(t, count)
				require.Equal(t, 2, f.evidenceCount())
				requirePassRateProjection(t, f, false)
				var unrelated string
				require.NoError(t, f.db.QueryRowContext(f.ctx, `SELECT extra->>'unrelated' FROM accounts WHERE id=62`).Scan(&unrelated))
				require.Equal(t, "retained", unrelated)
			})
		}
	})

	t.Run("foreign key cascade removes projection and publishes scheduler invalidation", func(t *testing.T) {
		f := passRateLifecycleHeldFixture(t)
		f.exec(`DELETE FROM scheduler_outbox`)
		f.exec(`DELETE FROM groups WHERE id=8`)
		requirePassRateProjection(t, f, false)
		var count int
		require.NoError(t, f.db.QueryRowContext(f.ctx, `SELECT COUNT(*) FROM scheduler_outbox WHERE account_id=62 AND event_type='account_changed'`).Scan(&count))
		require.Equal(t, 1, count)
	})

	t.Run("manual account detach revokes late completion and clears projection", func(t *testing.T) {
		f := passRateLifecycleHeldFixture(t)
		tx, err := f.db.BeginTx(f.ctx, nil)
		require.NoError(t, err)
		defer tx.Rollback()
		plans, err := lockAccountMembershipProtection(f.ctx, tx, 62)
		require.NoError(t, err)
		require.Contains(t, plans, f.plan.ID)
		_, err = tx.ExecContext(f.ctx, `DELETE FROM account_groups WHERE account_id=62`)
		require.NoError(t, err)
		require.NoError(t, releaseDetachedAccountProtection(f.ctx, tx, 62, plans))
		require.NoError(t, tx.Commit())
		requirePassRateProjection(t, f, false)
		var revoked bool
		require.NoError(t, f.db.QueryRowContext(f.ctx, `SELECT bool_and(protection_revoked) FROM scheduled_test_results WHERE plan_id=$1 AND run_id=(SELECT latest_run_id FROM scheduled_test_plans WHERE id=$1)`, f.plan.ID).Scan(&revoked))
		require.True(t, revoked)
	})

	t.Run("administrator group removal clears only detached members", func(t *testing.T) {
		for _, action := range []string{"soft_delete", "cascade_delete", "clear_members"} {
			t.Run(action, func(t *testing.T) {
				f := passRateLifecycleHeldFixture(t)
				f.exec(`ALTER TABLE groups ADD COLUMN updated_at TIMESTAMPTZ DEFAULT NOW();
 CREATE TABLE settings(key TEXT PRIMARY KEY,value TEXT,updated_at TIMESTAMPTZ);
 CREATE TABLE composite_model_routes(group_id BIGINT,deleted_at TIMESTAMPTZ);
 INSERT INTO groups(id,name) VALUES(9,'Second detection group');
 INSERT INTO account_groups VALUES(63,9);`)
				f.plan.GroupIDs = []int64{8, 9}
				var err error
				f.plan, err = f.plans.Update(f.ctx, f.plan)
				require.NoError(t, err)
				f.exec(`INSERT INTO scheduled_test_pass_rate_states SELECT plan_id,63,test_definition_id,model_key,rule_version,blocked,summary,updated_at FROM scheduled_test_pass_rate_states WHERE account_id=62`)
				tx, err := f.db.BeginTx(f.ctx, nil)
				require.NoError(t, err)
				require.NoError(t, syncActivePassRateProjection(f.ctx, tx, 63))
				require.NoError(t, tx.Commit())
				client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, f.db)))
				repo := newGroupRepositoryWithSQL(client, f.db)
				switch action {
				case "soft_delete":
					err = repo.Delete(f.ctx, 8)
				case "cascade_delete":
					_, err = repo.DeleteCascade(f.ctx, 8)
				case "clear_members":
					_, err = repo.DeleteAccountGroupsByGroupID(f.ctx, 8)
				}
				require.NoError(t, err)
				requirePassRateProjection(t, f, false)
				var otherHeld bool
				require.NoError(t, f.db.QueryRowContext(f.ctx, `SELECT extra->'active_pass_rate_holds' ? 'model-a' FROM accounts WHERE id=63`).Scan(&otherHeld))
				require.True(t, otherHeld, "an account still in another detection group retains its hold")
			})
		}
	})

	t.Run("group removal fences the first unfinished ability round", func(t *testing.T) {
		f := newPassRateFixture(t, 2)
		row := f.begin()
		f.exec(`ALTER TABLE groups ADD COLUMN updated_at TIMESTAMPTZ DEFAULT NOW()`)
		client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, f.db)))
		repo := newGroupRepositoryWithSQL(client, f.db)
		require.NoError(t, repo.Delete(f.ctx, 8))
		f.complete(row, "success", "fail")
		var count int
		require.NoError(t, f.db.QueryRowContext(f.ctx, `SELECT COUNT(*) FROM scheduled_test_pass_rate_states WHERE plan_id=$1`, f.plan.ID).Scan(&count))
		require.Zero(t, count)
		require.Zero(t, f.evidenceCount())
		requirePassRateProjection(t, f, false)
	})
}

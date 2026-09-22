//go:build integration

package repository

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/Wei-Shaw/sub2api/migrations"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

func TestScheduledTestGroupWorkflowConflictsIntegration(t *testing.T) {
	dsn := os.Getenv("MIGRATION_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("MIGRATION_TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	admin, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	defer admin.Close()
	schema := fmt.Sprintf("quality_workflow_conflicts_%d", time.Now().UnixNano())
	_, err = admin.ExecContext(ctx, "CREATE SCHEMA "+pq.QuoteIdentifier(schema))
	require.NoError(t, err)
	defer func() {
		_, err := admin.ExecContext(context.Background(), "DROP SCHEMA "+pq.QuoteIdentifier(schema)+" CASCADE")
		require.NoError(t, err)
	}()
	parsed, err := url.Parse(dsn)
	require.NoError(t, err)
	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()
	db, err := sql.Open("postgres", parsed.String())
	require.NoError(t, err)
	defer db.Close()
	db.SetMaxOpenConns(8)
	exec := func(t *testing.T, statement string, args ...any) {
		t.Helper()
		_, err := db.ExecContext(ctx, statement, args...)
		require.NoError(t, err)
	}
	exec(t, `CREATE TABLE users(id BIGINT PRIMARY KEY);
CREATE TABLE accounts(id BIGINT PRIMARY KEY,platform TEXT NOT NULL DEFAULT 'openai',status TEXT NOT NULL DEFAULT 'active',schedulable BOOLEAN NOT NULL DEFAULT TRUE,extra JSONB NOT NULL DEFAULT '{}',updated_at TIMESTAMPTZ DEFAULT NOW(),deleted_at TIMESTAMPTZ);
CREATE TABLE groups(id BIGINT PRIMARY KEY,platform TEXT NOT NULL DEFAULT 'openai',status TEXT NOT NULL DEFAULT 'active',deleted_at TIMESTAMPTZ);
CREATE TABLE account_groups(account_id BIGINT NOT NULL REFERENCES accounts(id),group_id BIGINT NOT NULL REFERENCES groups(id),PRIMARY KEY(account_id,group_id));
CREATE TABLE scheduler_outbox(id BIGSERIAL PRIMARY KEY,event_type TEXT,account_id BIGINT,group_id BIGINT,payload JSONB,dedup_key TEXT,created_at TIMESTAMPTZ DEFAULT NOW());
CREATE UNIQUE INDEX idx_workflow_conflicts_outbox_dedup ON scheduler_outbox(dedup_key) WHERE dedup_key IS NOT NULL;
INSERT INTO accounts(id) VALUES(101),(102);
INSERT INTO groups(id) VALUES(2),(43),(90),(91),(92);
INSERT INTO account_groups VALUES(101,2),(101,90),(102,91);`)
	for _, name := range []string{
		"066_add_scheduled_test_tables.sql", "070_add_scheduled_test_auto_recover.sql",
		"247_generalized_scheduled_tests.sql", "248_scheduled_test_reasoning_effort.sql", "249_scheduled_test_result_reasoning_effort.sql",
		"250_allow_group_account_scheduled_test_targets.sql", "251_scheduled_test_target_modes.sql", "252_scheduled_test_definition_sort_order.sql",
		"253_scheduled_test_plan_sort_order.sql", "256_scheduled_test_multiple_definitions.sql", "257_scheduled_test_hourly_statistics.sql",
		"258_scheduled_test_protection.sql", "259_scheduled_test_outcome_actions.sql", "260_scheduled_test_model_check.sql",
		"261_scheduled_test_admin_review.sql", "262_scheduled_test_execution_snapshot.sql", "264_scheduled_test_cache_recovery.sql",
	} {
		raw, err := migrations.FS.ReadFile(name)
		require.NoError(t, err)
		exec(t, string(raw))
	}
	var candyID, pelicanID int64
	require.NoError(t, db.QueryRowContext(ctx, `SELECT id FROM scheduled_test_definitions WHERE key='candy'`).Scan(&candyID))
	require.NoError(t, db.QueryRowContext(ctx, `SELECT id FROM scheduled_test_definitions WHERE key='pelican'`).Scan(&pelicanID))
	repo := &scheduledTestPlanRepository{db: db}
	reset := func(t *testing.T) {
		exec(t, `TRUNCATE scheduled_test_plans RESTART IDENTITY CASCADE`)
	}
	ordinary := func(name string, source, target int64) *service.ScheduledTestPlan {
		return &service.ScheduledTestPlan{
			Name: name, GroupID: &source, TargetMode: "all_accounts", TestDefinitionID: &candyID, TestDefinitionIDs: []int64{candyID},
			ModelID: "model", CronExpression: "0 * * * *", Enabled: true, MaxResults: 20,
			Protection: service.ScheduledTestProtectionConfig{Enabled: true, Rules: []service.ScheduledTestProtectionRule{{
				TestDefinitionID: candyID, OnPass: &service.ScheduledTestOutcomeAction{Scheduling: "keep", GroupMode: "assign", GroupIDs: []int64{target}},
			}}},
		}
	}
	workflow := func() *service.ScheduledTestPlan {
		p := ordinary("糖果与鹈鹕分组", 43, 43)
		p.Protection.GroupWorkflow = &service.ScheduledTestGroupWorkflow{AutomaticTestID: candyID, ReviewTestID: pelicanID, PassGroupID: 43, FailGroupID: 2}
		p.Protection.Rules = p.Protection.GroupWorkflow.Rules()
		p.TestDefinitionIDs = []int64{candyID, pelicanID}
		return p
	}
	create := func(t *testing.T, plan *service.ScheduledTestPlan) *service.ScheduledTestPlan {
		t.Helper()
		created, err := repo.Create(ctx, plan)
		require.NoError(t, err)
		return created
	}
	assertConflict := func(t *testing.T, err error, name string) {
		t.Helper()
		require.ErrorContains(t, err, name)
		require.ErrorContains(t, err, "先停用")
	}

	t.Run("ordinary plans remain compatible and must be disabled before workflow", func(t *testing.T) {
		reset(t)
		first := create(t, ordinary("旧Pro计划", 2, 43))
		second := create(t, ordinary("旧不降智计划", 43, 2))
		_, err := repo.Create(ctx, workflow())
		assertConflict(t, err, first.Name)
		for _, plan := range []*service.ScheduledTestPlan{first, second} {
			plan.Enabled = false
			_, err = repo.Update(ctx, plan)
			require.NoError(t, err)
		}
		active := create(t, workflow())
		active.Name = "新版工作流"
		_, err = repo.Update(ctx, active)
		require.NoError(t, err, "the updated plan must not conflict with itself")
		first.Enabled = true
		_, err = repo.Update(ctx, first)
		assertConflict(t, err, active.Name)
		stored, err := repo.GetByID(ctx, first.ID)
		require.NoError(t, err)
		require.False(t, stored.Enabled)
		require.NoError(t, repo.Delete(ctx, active.ID))
		_, err = repo.Update(ctx, first)
		require.NoError(t, err)
	})

	t.Run("disabled plans and independent quality holds remain allowed", func(t *testing.T) {
		reset(t)
		active := create(t, workflow())
		disabled := ordinary("停用计划", 2, 43)
		disabled.Enabled = false
		create(t, disabled)
		noProtection := ordinary("未开启自动化", 2, 43)
		noProtection.Protection.Enabled = false
		create(t, noProtection)
		hold := ordinary("缓存率保护", 2, 43)
		hold.Protection.Rules[0].OnPass = nil
		create(t, hold)
		_, err := repo.Create(ctx, ordinary("新Pro计划", 2, 43))
		assertConflict(t, err, active.Name)
	})

	t.Run("outside source with intersecting destination conflicts", func(t *testing.T) {
		reset(t)
		outside := create(t, ordinary("外组升档", 92, 43))
		_, err := repo.Create(ctx, workflow())
		assertConflict(t, err, outside.Name)
	})

	t.Run("account only plan uses actual membership", func(t *testing.T) {
		reset(t)
		single := ordinary("指定账号计划", 90, 92)
		single.GroupID, single.TargetMode = nil, "account"
		accountID := int64(101)
		single.AccountID = &accountID
		create(t, single)
		_, err := repo.Create(ctx, workflow())
		assertConflict(t, err, single.Name)
	})

	t.Run("different source groups with shared accounts conflict", func(t *testing.T) {
		reset(t)
		create(t, workflow())
		_, err := repo.Create(ctx, ordinary("共有账号计划", 90, 92))
		assertConflict(t, err, "糖果与鹈鹕分组")
		create(t, ordinary("独立分组计划", 91, 92))
	})

	t.Run("retained accounts outside original group still conflict", func(t *testing.T) {
		reset(t)
		retained := create(t, ordinary("已迁出账号计划", 91, 92))
		exec(t, `INSERT INTO scheduled_test_managed_accounts(plan_id,account_id,source_group_id) VALUES($1,101,91)`, retained.ID)
		_, err := repo.Create(ctx, workflow())
		assertConflict(t, err, retained.Name)
	})

	t.Run("workflow retained accounts cannot be claimed by another plan", func(t *testing.T) {
		reset(t)
		active := create(t, workflow())
		exec(t, `INSERT INTO scheduled_test_managed_accounts(plan_id,account_id,source_group_id) VALUES($1,102,43)`, active.ID)
		_, err := repo.Create(ctx, ordinary("迁出后冲突", 91, 92))
		assertConflict(t, err, active.Name)
	})

	for _, operation := range []string{"create", "enable"} {
		t.Run("concurrent "+operation+" cannot introduce conflicts", func(t *testing.T) {
			reset(t)
			inputs := []*service.ScheduledTestPlan{workflow(), ordinary("并发Pro计划", 2, 43)}
			if operation == "enable" {
				for i, p := range inputs {
					p.Enabled = false
					inputs[i] = create(t, p)
					inputs[i].Enabled = true
				}
			}
			start := make(chan struct{})
			results := make(chan error, len(inputs))
			var wg sync.WaitGroup
			for _, input := range inputs {
				wg.Add(1)
				go func(p *service.ScheduledTestPlan) {
					defer wg.Done()
					<-start
					var err error
					if operation == "create" {
						_, err = repo.Create(ctx, p)
					} else {
						_, err = repo.Update(ctx, p)
					}
					results <- err
				}(input)
			}
			close(start)
			wg.Wait()
			close(results)
			succeeded := 0
			for err := range results {
				if err == nil {
					succeeded++
				} else {
					require.ErrorContains(t, err, "先停用")
				}
			}
			require.Equal(t, 1, succeeded)
			var enabled int
			require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM scheduled_test_plans WHERE enabled`).Scan(&enabled))
			require.Equal(t, 1, enabled)
		})
	}
}

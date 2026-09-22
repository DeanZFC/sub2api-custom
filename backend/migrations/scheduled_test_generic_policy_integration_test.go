//go:build integration

package migrations

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

func genericPolicyMigrationDB(t *testing.T) (context.Context, *sql.Conn) {
	t.Helper()
	dsn := os.Getenv("MIGRATION_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("MIGRATION_TEST_DATABASE_URL is not set")
	}
	db, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	conn, err := db.Conn(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	schema := "generic_policy_" + uuid.New().String()[:8]
	_, err = conn.ExecContext(ctx, "CREATE SCHEMA "+schema)
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = conn.ExecContext(context.Background(), "DROP SCHEMA "+schema+" CASCADE") })
	_, err = conn.ExecContext(ctx, "SET search_path TO "+schema)
	require.NoError(t, err)
	_, err = conn.ExecContext(ctx, `
CREATE TABLE groups (id BIGINT PRIMARY KEY,platform TEXT NOT NULL DEFAULT 'openai',status TEXT NOT NULL DEFAULT 'active',deleted_at TIMESTAMPTZ);
CREATE TABLE accounts (id BIGINT PRIMARY KEY,platform TEXT NOT NULL DEFAULT 'openai',status TEXT NOT NULL DEFAULT 'active',schedulable BOOLEAN NOT NULL DEFAULT TRUE,deleted_at TIMESTAMPTZ,extra JSONB NOT NULL DEFAULT '{}',updated_at TIMESTAMPTZ DEFAULT '2026-01-01');
CREATE TABLE account_groups (account_id BIGINT REFERENCES accounts(id),group_id BIGINT REFERENCES groups(id),PRIMARY KEY(account_id,group_id));
CREATE TABLE scheduled_test_plans (id BIGINT PRIMARY KEY,name TEXT NOT NULL DEFAULT '',group_id BIGINT REFERENCES groups(id),account_id BIGINT REFERENCES accounts(id),target_mode TEXT NOT NULL DEFAULT 'all_accounts',enabled BOOLEAN NOT NULL DEFAULT TRUE,protection JSONB NOT NULL DEFAULT '{}',latest_run_id TEXT NOT NULL DEFAULT 'legacy-run',updated_at TIMESTAMPTZ DEFAULT '2026-01-01',
 CONSTRAINT scheduled_test_plans_at_least_one_target CHECK(account_id IS NOT NULL OR group_id IS NOT NULL),
 CONSTRAINT scheduled_test_plans_target_mode_check CHECK((target_mode='account' AND account_id IS NOT NULL) OR(target_mode IN('group','all_accounts') AND group_id IS NOT NULL AND account_id IS NULL)));
CREATE TABLE scheduled_test_results (id BIGINT PRIMARY KEY,plan_id BIGINT NOT NULL REFERENCES scheduled_test_plans(id),response_text TEXT NOT NULL DEFAULT 'historical result',run_id TEXT NOT NULL DEFAULT 'legacy-run');
CREATE TABLE scheduled_test_protection_states (plan_id BIGINT REFERENCES scheduled_test_plans(id),account_id BIGINT REFERENCES accounts(id),test_definition_id BIGINT NOT NULL DEFAULT 1,result_id BIGINT REFERENCES scheduled_test_results(id),blocked BOOLEAN NOT NULL DEFAULT TRUE,PRIMARY KEY(plan_id,account_id,test_definition_id));
CREATE TABLE scheduled_test_votes (result_id BIGINT REFERENCES scheduled_test_results(id),user_id BIGINT NOT NULL DEFAULT 1,generation BIGINT NOT NULL DEFAULT 1,vote TEXT NOT NULL DEFAULT 'fail');
CREATE TABLE scheduled_test_managed_accounts (plan_id BIGINT REFERENCES scheduled_test_plans(id),account_id BIGINT REFERENCES accounts(id),source_group_id BIGINT REFERENCES groups(id),PRIMARY KEY(plan_id,account_id));
CREATE TABLE scheduler_outbox (event_type TEXT NOT NULL,account_id BIGINT);
`)
	require.NoError(t, err)
	return ctx, conn
}

func applyGenericPolicyMigration(t *testing.T, ctx context.Context, conn *sql.Conn) {
	t.Helper()
	raw, err := FS.ReadFile("265_scheduled_test_generic_policy.sql")
	require.NoError(t, err)
	_, err = conn.ExecContext(ctx, string(raw))
	require.NoError(t, err)
}

func genericPolicyWorkflow(pass, fail int64) string {
	return fmt.Sprintf(`{"enabled":true,"group_workflow":{"automatic_test_id":1,"review_test_id":2,"pass_group_id":%d,"fail_group_id":%d},"rules":[
 {"test_definition_id":1,"expected_answer":"21","answer_match":"numeric","on_pass":{"scheduling":"keep","group_mode":"assign","group_ids":[%d]},"on_fail":{"scheduling":"keep","group_mode":"assign","group_ids":[%d]}},
 {"test_definition_id":2,"vote":{"enabled":true,"public_enabled":true,"reject_above":0,"pass_at_least":2},"on_pass":{"scheduling":"keep","group_mode":"assign","group_ids":[%d]},"on_fail":{"scheduling":"keep","group_mode":"assign","group_ids":[%d]}}]}`, pass, fail, pass, fail, pass, fail)
}

// These are fixed test fixtures. Use quoted SQL literals because lib/pq cannot
// bind parameters in a multi-statement setup batch.
func genericPolicyFixture(ctx context.Context, conn *sql.Conn, query string, values ...string) (sql.Result, error) {
	for i := len(values) - 1; i >= 0; i-- {
		query = strings.ReplaceAll(query, fmt.Sprintf("$%d", i+1), pq.QuoteLiteral(values[i]))
	}
	return conn.ExecContext(ctx, query)
}

func TestScheduledTestGenericPolicyMigration(t *testing.T) {
	t.Run("workflow preserves configured values history and group order", func(t *testing.T) {
		ctx, conn := genericPolicyMigrationDB(t)
		_, err := genericPolicyFixture(ctx, conn, `
INSERT INTO groups(id) VALUES(2),(43),(88);
INSERT INTO accounts(id,status,schedulable,extra) VALUES
 (101,'quality_paused',TRUE,'{"quality_protection_reason":"old hold","untouched":true}'),
 (102,'quality_paused',FALSE,'{"quality_protection_reason":"manual stop"}'),
 (103,'error',TRUE,'{"quality_protection_reason":"unrelated error"}'),(104,'active',TRUE,'{}');
INSERT INTO account_groups VALUES(101,43),(102,2),(103,43),(104,88);
INSERT INTO scheduled_test_plans(id,group_id,protection) VALUES(1,2,$1::jsonb);
INSERT INTO scheduled_test_results(id,plan_id) VALUES(201,1),(202,1),(203,1);
INSERT INTO scheduled_test_protection_states(plan_id,account_id,result_id) VALUES(1,101,201),(1,102,202),(1,103,203);
INSERT INTO scheduled_test_votes(result_id) VALUES(201),(202);
INSERT INTO scheduled_test_managed_accounts VALUES(1,104,2);`, genericPolicyWorkflow(43, 2))
		require.NoError(t, err)
		applyGenericPolicyMigration(t, ctx, conn)
		var ids pq.Int64Array
		var anchor int64
		var accountID sql.NullInt64
		var enabled bool
		var target, note, protection string
		require.NoError(t, conn.QueryRowContext(ctx, `SELECT group_ids,group_id,account_id,enabled,target_mode,migration_note,protection::text FROM scheduled_test_plans WHERE id=1`).Scan(&ids, &anchor, &accountID, &enabled, &target, &note, &protection))
		require.Equal(t, pq.Int64Array{43, 2}, ids, "the configured passing tier keeps its first display position")
		require.EqualValues(t, 43, anchor)
		require.False(t, accountID.Valid)
		require.True(t, enabled)
		require.Equal(t, "all_accounts", target)
		require.Empty(t, note)
		var currentRows int
		require.NoError(t, conn.QueryRowContext(ctx, `SELECT count(*) FROM scheduled_test_results r JOIN scheduled_test_plans p ON p.id=r.plan_id WHERE r.run_id<>'' AND r.run_id=p.latest_run_id`).Scan(&currentRows))
		require.Zero(t, currentRows, "historical executions cannot rebuild votes or protection before a fresh generic-policy run")
		var config map[string]json.RawMessage
		require.NoError(t, json.Unmarshal([]byte(protection), &config))
		require.NotContains(t, config, "group_workflow")
		var rules []map[string]any
		require.NoError(t, json.Unmarshal(config["rules"], &rules))
		require.Equal(t, "21", rules[0]["expected_answer"])
		require.Equal(t, float64(0), rules[0]["priority"])
		require.Equal(t, float64(100), rules[1]["priority"])
		require.Equal(t, false, rules[0]["required_pass"])
		require.Equal(t, false, rules[1]["required_pass"])
		require.Equal(t, true, rules[1]["vote"].(map[string]any)["public_enabled"])
		require.Equal(t, float64(2), rules[1]["vote"].(map[string]any)["pass_at_least"])
		for _, table := range []string{"scheduled_test_votes", "scheduled_test_protection_states", "scheduled_test_managed_accounts"} {
			var count int
			require.NoError(t, conn.QueryRowContext(ctx, "SELECT count(*) FROM "+table).Scan(&count))
			require.Zero(t, count, table)
		}
		var snapshot string
		require.NoError(t, conn.QueryRowContext(ctx, `SELECT jsonb_build_object(
 'history',(SELECT count(*) FROM scheduled_test_results),'memberships',(SELECT jsonb_agg(to_jsonb(ag) ORDER BY account_id) FROM account_groups ag),
 'resumed',(SELECT status FROM accounts WHERE id=101),'manual',(SELECT status FROM accounts WHERE id=102),'error',(SELECT status FROM accounts WHERE id=103),
 'extra',(SELECT extra FROM accounts WHERE id=101),'events',(SELECT count(*) FROM scheduler_outbox))::text`).Scan(&snapshot))
		require.JSONEq(t, `{"history":3,"memberships":[{"account_id":101,"group_id":43},{"account_id":102,"group_id":2},{"account_id":103,"group_id":43},{"account_id":104,"group_id":88}],"resumed":"active","manual":"quality_paused","error":"error","extra":{"untouched":true},"events":1}`, snapshot)

		// A replay must keep judgments and holds from a new generic-policy round.
		_, err = conn.ExecContext(ctx, `INSERT INTO scheduled_test_protection_states(plan_id,account_id,result_id) VALUES(1,101,201);
INSERT INTO scheduled_test_votes(result_id) VALUES(201);
INSERT INTO scheduled_test_managed_accounts VALUES(1,104,43);
UPDATE accounts SET status='quality_paused',extra='{"quality_protection_reason":"new round"}' WHERE id=101;`)
		require.NoError(t, err)
		_, err = conn.ExecContext(ctx, `UPDATE scheduled_test_plans SET latest_run_id='generic-round' WHERE id=1; UPDATE scheduled_test_results SET run_id='generic-round' WHERE id=201`)
		require.NoError(t, err)
		const allRows = `SELECT jsonb_build_object('plans',(SELECT jsonb_agg(to_jsonb(p) ORDER BY id) FROM scheduled_test_plans p),
 'accounts',(SELECT jsonb_agg(to_jsonb(a) ORDER BY id) FROM accounts a),'states',(SELECT jsonb_agg(to_jsonb(s)) FROM scheduled_test_protection_states s),
 'votes',(SELECT jsonb_agg(to_jsonb(v)) FROM scheduled_test_votes v),'managed',(SELECT jsonb_agg(to_jsonb(ma)) FROM scheduled_test_managed_accounts ma),
 'history',(SELECT jsonb_agg(to_jsonb(r) ORDER BY id) FROM scheduled_test_results r),'events',(SELECT count(*) FROM scheduler_outbox))::text`
		var before, after string
		require.NoError(t, conn.QueryRowContext(ctx, allRows).Scan(&before))
		applyGenericPolicyMigration(t, ctx, conn)
		require.NoError(t, conn.QueryRowContext(ctx, allRows).Scan(&after))
		require.JSONEq(t, before, after)
	})

	t.Run("changed or invalid targets are disabled with actionable reasons", func(t *testing.T) {
		ctx, conn := genericPolicyMigrationDB(t)
		_, err := genericPolicyFixture(ctx, conn, `
INSERT INTO groups(id,platform) VALUES(2,'openai'),(43,'openai'),(8,'openai'),(10,'openai'),(11,'anthropic');
INSERT INTO accounts(id) VALUES(101),(102);
INSERT INTO account_groups VALUES(101,2),(101,43);
INSERT INTO scheduled_test_plans(id,group_id,account_id,target_mode,protection) VALUES
 (1,8,NULL,'all_accounts','{"enabled":true,"rules":[{"test_definition_id":1,"expected_answer":"42","on_pass":{"group_mode":"assign","group_ids":[10]},"on_fail":{"group_mode":"assign","group_ids":[8]}}]}'),
 (2,NULL,101,'account','{}'),(3,8,NULL,'group','{}'),(4,NULL,102,'account','{}'),
 (5,8,NULL,'all_accounts',$1::jsonb),
 (6,8,NULL,'all_accounts','{"enabled":true,"group_workflow":{"pass_group_id":"broken","fail_group_id":8},"rules":[]}'),
 (7,8,NULL,'all_accounts','{"enabled":true,"rules":[{"test_definition_id":1,"on_pass":{"group_mode":"assign","group_ids":[]}}]}');`, genericPolicyWorkflow(8, 11))
		require.NoError(t, err)
		applyGenericPolicyMigration(t, ctx, conn)
		rows, err := conn.QueryContext(ctx, `SELECT id,enabled,group_ids,migration_note FROM scheduled_test_plans ORDER BY id`)
		require.NoError(t, err)
		defer rows.Close()
		expected := map[int64]struct {
			ids  pq.Int64Array
			note string
		}{1: {pq.Int64Array{8, 10}, "动作目标分组"}, 2: {pq.Int64Array{2, 43}, "指定账号或分组汇总"}, 3: {pq.Int64Array{8}, "指定账号或分组汇总"}, 4: {pq.Int64Array{}, "没有可用分组"}, 5: {pq.Int64Array{8, 11}, "不同平台"}, 6: {pq.Int64Array{8}, "无法自动转换"}, 7: {pq.Int64Array{8}, "没有目标分组"}}
		for rows.Next() {
			var id int64
			var enabled bool
			var ids pq.Int64Array
			var note string
			require.NoError(t, rows.Scan(&id, &enabled, &ids, &note))
			require.False(t, enabled, "plan %d", id)
			require.Equal(t, expected[id].ids, ids, "plan %d", id)
			require.Contains(t, note, expected[id].note, "plan %d", id)
		}
		require.NoError(t, rows.Err())
	})

	t.Run("missing legacy workflow groups cannot abort the upgrade", func(t *testing.T) {
		ctx, conn := genericPolicyMigrationDB(t)
		_, err := genericPolicyFixture(ctx, conn, `INSERT INTO groups(id) VALUES(8);
INSERT INTO scheduled_test_plans(id,group_id,protection) VALUES(1,8,$1::jsonb),(2,8,$2::jsonb);`, genericPolicyWorkflow(999, 8), genericPolicyWorkflow(998, 999))
		require.NoError(t, err)
		applyGenericPolicyMigration(t, ctx, conn)
		for _, id := range []int64{1, 2} {
			var enabled bool
			var anchor sql.NullInt64
			var ids pq.Int64Array
			var note string
			require.NoError(t, conn.QueryRowContext(ctx, `SELECT enabled,group_id,group_ids,migration_note FROM scheduled_test_plans WHERE id=$1`, id).Scan(&enabled, &anchor, &ids, &note))
			require.False(t, enabled)
			require.Contains(t, note, "分组已失效")
			if id == 1 {
				require.Equal(t, pq.Int64Array{8}, ids)
				require.Equal(t, sql.NullInt64{Int64: 8, Valid: true}, anchor)
			} else {
				require.Empty(t, ids)
				require.False(t, anchor.Valid)
			}
		}
		// Preserve the obsolete action IDs as editable evidence for an admin.
		var destination int64
		require.NoError(t, conn.QueryRowContext(ctx, `SELECT (protection->'rules'->0->'on_pass'->'group_ids'->>0)::bigint FROM scheduled_test_plans WHERE id=1`).Scan(&destination))
		require.EqualValues(t, 999, destination)
		applyGenericPolicyMigration(t, ctx, conn)
	})

	t.Run("overlapping routing plans stop on group account and retained enrollment conflicts", func(t *testing.T) {
		ctx, conn := genericPolicyMigrationDB(t)
		_, err := genericPolicyFixture(ctx, conn, `INSERT INTO groups(id) VALUES(2),(43),(61),(62),(71),(72),(81),(82),(91),(92),(101),(102);
INSERT INTO accounts(id) VALUES(501),(502);
INSERT INTO account_groups VALUES(501,61),(501,71);
INSERT INTO scheduled_test_plans(id,group_id,protection) VALUES(1,2,$1::jsonb),(2,43,$1::jsonb),
 (3,61,$2::jsonb),(4,71,$3::jsonb),(5,81,$4::jsonb),
 (6,43,'{"enabled":true,"rules":[{"test_definition_id":1,"expected_answer":"21"}]}'),
 (7,91,$5::jsonb),(8,101,$6::jsonb);
INSERT INTO scheduled_test_managed_accounts VALUES(7,502,91),(8,502,101);`, genericPolicyWorkflow(43, 2), genericPolicyWorkflow(61, 62), genericPolicyWorkflow(71, 72), genericPolicyWorkflow(81, 82), genericPolicyWorkflow(91, 92), genericPolicyWorkflow(101, 102))
		require.NoError(t, err)
		applyGenericPolicyMigration(t, ctx, conn)
		for _, id := range []int64{1, 2, 3, 4, 7, 8} {
			var enabled bool
			var note string
			require.NoError(t, conn.QueryRowContext(ctx, `SELECT enabled,migration_note FROM scheduled_test_plans WHERE id=$1`, id).Scan(&enabled, &note))
			require.False(t, enabled, "plan %d", id)
			require.Contains(t, note, "范围重叠")
		}
		var enabledIDs pq.Int64Array
		require.NoError(t, conn.QueryRowContext(ctx, `SELECT array_agg(id ORDER BY id) FROM scheduled_test_plans WHERE enabled`).Scan(&enabledIDs))
		require.Equal(t, pq.Int64Array{5, 6}, enabledIDs, "disjoint routing and read-only checks remain enabled")
		var defaultActions string
		require.NoError(t, conn.QueryRowContext(ctx, `SELECT (protection->'rules'->0)::text FROM scheduled_test_plans WHERE id=6`).Scan(&defaultActions))
		require.JSONEq(t, `{"test_definition_id":1,"expected_answer":"21","priority":0,"required_pass":false,"on_pass":{"scheduling":"resume","group_mode":"keep"},"on_fail":{"scheduling":"pause","group_mode":"keep"}}`, defaultActions)
	})
}

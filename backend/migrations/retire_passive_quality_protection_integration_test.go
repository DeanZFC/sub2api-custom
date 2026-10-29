//go:build integration

package migrations

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

func TestRetirePassiveQualityProtectionMigration(t *testing.T) {
	dsn := os.Getenv("MIGRATION_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("MIGRATION_TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	admin, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = admin.Close() })
	name := "retire_passive_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	_, err = admin.ExecContext(ctx, "CREATE DATABASE "+name)
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = admin.ExecContext(context.Background(), "DROP DATABASE "+name+" WITH (FORCE)") })
	parsed, err := url.Parse(dsn)
	require.NoError(t, err)
	parsed.Path = "/" + name
	db, err := sql.Open("postgres", parsed.String())
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	_, err = db.ExecContext(ctx, "CREATE TABLE schema_migrations(filename TEXT PRIMARY KEY,checksum TEXT,applied_at TIMESTAMPTZ DEFAULT NOW())")
	require.NoError(t, err)
	files, err := FS.ReadDir(".")
	require.NoError(t, err)
	names := []string{}
	for _, f := range files {
		if strings.HasSuffix(f.Name(), ".sql") && f.Name() < "324_" {
			names = append(names, f.Name())
		}
	}
	sort.Strings(names)
	for _, filename := range names {
		raw, err := FS.ReadFile(filename)
		require.NoError(t, err)
		if strings.HasSuffix(filename, "_notx.sql") {
			// The real runner splits concurrent-index migrations at SQL statement
			// boundaries. psql is unnecessary for the selected one-statement files.
			for _, statement := range strings.Split(string(raw), ";") {
				if strings.TrimSpace(statement) == "" {
					continue
				}
				_, err = db.ExecContext(ctx, statement)
				require.NoError(t, err, filename)
			}
		} else {
			tx, err := db.BeginTx(ctx, nil)
			require.NoError(t, err)
			_, err = tx.ExecContext(ctx, string(raw))
			if err != nil {
				_ = tx.Rollback()
			}
			require.NoError(t, err, filename)
			require.NoError(t, tx.Commit(), filename)
		}
		_, err = db.ExecContext(ctx, "INSERT INTO schema_migrations(filename,checksum) VALUES($1,'test')", filename)
		require.NoError(t, err)
	}
	_, err = db.ExecContext(ctx, `
INSERT INTO groups(id,name,platform) VALUES(901,'retirement-test','openai');
INSERT INTO accounts(id,name,platform,type,status,schedulable,extra)
SELECT n,'account-'||n,'openai','apikey',CASE WHEN n=902 THEN 'disabled' WHEN n>=903 THEN 'quality_paused' ELSE 'active' END,
 n<>902,'{"passive_quality":[{"policy_id":901}],"group_quality_targets":[901],"manual_setting":"keep"}'::jsonb
FROM generate_series(901,915) n;
INSERT INTO account_groups(account_id,group_id) SELECT n,901 FROM generate_series(901,915) n;
INSERT INTO scheduled_test_definitions(id,key,name,prompt,output_kind) VALUES
(9001,'retirement_active','active','answer','number'),
(9002,'retirement_stats','statistics','','statistics'),
(9003,'retirement_stats_two','statistics two','','statistics'),
(9004,'retirement_manual','manual','draw','html');
INSERT INTO scheduled_test_plans(id,name,model_id,model_ids,group_ids,group_id,target_mode,test_definition_id,test_definition_ids,protection) VALUES
(901,'stats only','model-a',ARRAY['model-a'],ARRAY[901],901,'all_accounts',9002,ARRAY[9002,9003],
 '{"enabled":true,"rules":[{"test_definition_id":9002,"recovery":{"enabled":true}},{"test_definition_id":9003}]}'),
(902,'active only','model-a',ARRAY['model-a'],ARRAY[901],901,'all_accounts',9001,ARRAY[9001],
 '{"enabled":true,"rules":[{"test_definition_id":9001}]}'),
(903,'mixed all-pass','model-a',ARRAY['model-a'],ARRAY[901],901,'all_accounts',9002,ARRAY[9002,9001,9003],
 '{"enabled":true,"mode":"all_pass","rules":[{"test_definition_id":9002},{"test_definition_id":9001},{"test_definition_id":9003}]}'),
(904,'mixed combined','model-a',ARRAY['model-a'],ARRAY[901],901,'all_accounts',9002,ARRAY[9002,9001],
 '{"enabled":true,"mode":"combined","rules":[{"test_definition_id":9002},{"test_definition_id":9001}],"combinations":[{"id":"mixed","condition":{"op":"and","conditions":[{"test_definition_id":9001},{"test_definition_id":9002}]}},{"id":"active","condition":{"test_definition_id":9001}}]}');
INSERT INTO scheduled_test_plans(id,name,model_id,model_ids,group_ids,group_id,target_mode,test_definition_id,test_definition_ids,protection) VALUES
(905,'manual after retirement','model-a',ARRAY['model-a'],ARRAY[901],901,'all_accounts',9002,ARRAY[9002,9004],
 '{"enabled":true,"mode":"all_pass","rules":[{"test_definition_id":9002},{"test_definition_id":9004,"vote":{"enabled":true}}]}');
INSERT INTO scheduled_test_results(id,plan_id,account_id,test_definition_id,output_kind,model_id,status,response_text)
VALUES(9001,902,904,9001,'number','model-a','success','active-history'),(9002,901,903,9002,'statistics','model-a','success','statistics-history');
INSERT INTO scheduled_test_protection_states(plan_id,account_id,test_definition_id,model_key,result_started_at,blocked,verdict,rule_config)
VALUES(901,903,9002,'model-a',NOW(),TRUE,'fail','{"recovery":{"enabled":true}}'),
(901,903,9003,'model-a',NOW(),TRUE,'fail','{}'),
(902,904,9001,'model-a',NOW(),TRUE,'fail','{"test_definition_id":9001}'),
(903,905,9001,'model-a',NOW(),FALSE,'fail','{"test_definition_id":9001}'),
(903,905,9002,'model-a',NOW(),TRUE,'fail','{}'),
(903,906,9001,'model-a',NOW(),FALSE,'pending','{"test_definition_id":9001}'),
(903,906,9002,'model-a',NOW(),TRUE,'fail','{}'),
(903,909,9001,'model-a',NOW(),FALSE,'pass','{"test_definition_id":9001}'),
(903,909,9002,'model-a',NOW(),TRUE,'fail','{}'),
(901,910,9002,'model-a',NOW(),TRUE,'fail','{}'),
(903,911,9001,'model-a',NOW(),FALSE,'pending','{"test_definition_id":9001}'),
(903,911,9002,'model-a',NOW(),TRUE,'fail','{}'),
(905,913,9004,'model-a',NOW(),FALSE,'fail','{"test_definition_id":9004,"vote":{"enabled":true}}'),
(905,913,9002,'model-a',NOW(),TRUE,'fail','{}'),
(903,914,9001,'model-a',NOW(),FALSE,'pass','{"test_definition_id":9001}'),
(903,915,9001,'model-a',NOW(),FALSE,'pass','{"test_definition_id":9001}');
UPDATE scheduled_test_protection_states SET completed=TRUE WHERE account_id IN(909,914,915) AND test_definition_id=9001;
UPDATE scheduled_test_plans SET latest_run_id='current-active-round' WHERE id=903;
INSERT INTO scheduled_test_results(id,plan_id,account_id,test_definition_id,output_kind,model_id,status,response_text,run_id,started_at)
SELECT 9100+account_id,903,account_id,9001,'number','model-a','success','valid active pass',
CASE WHEN account_id=909 THEN 'current-active-round' ELSE 'old-active-round' END,result_started_at
FROM scheduled_test_protection_states WHERE account_id IN(909,915) AND test_definition_id=9001;
UPDATE scheduled_test_protection_states SET result_id=9100+account_id WHERE account_id IN(909,915) AND test_definition_id=9001;
INSERT INTO scheduled_test_protection_events(id,plan_id,account_id,test_definition_id,result_id,snapshot)
VALUES(9001,903,906,0,0,'{"tests":[{"test_definition_id":9001,"verdict":"fail"},{"test_definition_id":9002,"verdict":"fail"}]}');
INSERT INTO scheduled_test_combination_states(plan_id,account_id,model_key,blocked,account_wide,rule_ids,trigger_event_id)
VALUES(903,905,'model-a',TRUE,TRUE,'[]',NULL),(903,906,'model-a',TRUE,TRUE,'[]',9001),
(904,907,'model-a',TRUE,TRUE,'["mixed","active"]',NULL),
(904,908,'model-a',TRUE,TRUE,'["mixed"]',NULL),
(903,909,'model-a',TRUE,TRUE,'[]',NULL),
(903,911,'model-a',TRUE,TRUE,'[]',9002),
(903,912,'model-a',TRUE,TRUE,'[]',NULL),
(905,913,'model-a',TRUE,TRUE,'[]',NULL),
(903,914,'model-a',TRUE,TRUE,'[]',NULL),
(903,915,'model-a',TRUE,TRUE,'[]',NULL);
INSERT INTO scheduled_test_protection_events(id,plan_id,account_id,test_definition_id,result_id,snapshot) VALUES
(9002,903,911,0,0,'{"tests":[{"test_definition_id":9002,"verdict":"fail"}]}'),
(9003,903,911,0,0,'{"tests":[{"test_definition_id":9001,"verdict":"fail"}]}');
INSERT INTO scheduled_test_pass_rate_states(plan_id,account_id,test_definition_id,model_key,rule_version,blocked,summary)
VALUES(902,910,9001,'model-a','keep-version',TRUE,'{"reason":"active-failure"}');
INSERT INTO passive_quality_policies(id,config) VALUES(901,'{"purpose":"group_quality_target","enabled":true,"group_ids":[901]}');
INSERT INTO passive_quality_states(policy_id,account_id,model_key,data) VALUES(901,901,'model-a','{"phase":"paused"}');
INSERT INTO quality_suppliers(id,config) VALUES(901,'{"name":"retired supplier"}');
INSERT INTO quality_supplier_accounts(account_id,supplier_id) VALUES(901,901);
UPDATE quality_operations_config SET config=config||'{"recovery_reserve_percent":25}'::jsonb;
INSERT INTO quality_budget_days(day,account_id,requests,tokens,recovery_requests,recovery_tokens) VALUES(CURRENT_DATE,0,30,12345,10,3000);
INSERT INTO quality_probe_attempts(id,account_id,day,reserved_tokens,actual_tokens,settled,purpose) VALUES(9001,901,CURRENT_DATE,200,100,TRUE,'recovery');
`)
	require.NoError(t, err)
	const retained = `SELECT jsonb_build_object(
 'history',(SELECT jsonb_agg(to_jsonb(x) ORDER BY id) FROM scheduled_test_results x),
 'active',(SELECT to_jsonb(x) FROM scheduled_test_protection_states x WHERE plan_id=902 AND account_id=904),
 'pass_rate',(SELECT jsonb_agg(to_jsonb(x) ORDER BY account_id) FROM scheduled_test_pass_rate_states x),
 'events',(SELECT jsonb_agg(to_jsonb(x) ORDER BY id) FROM scheduled_test_protection_events x),
 'budgets',(SELECT jsonb_agg(to_jsonb(x) ORDER BY account_id) FROM quality_budget_days x),
 'attempts',(SELECT jsonb_agg(to_jsonb(x) ORDER BY id) FROM quality_probe_attempts x),
 'monitor',(SELECT jsonb_agg(to_jsonb(x) ORDER BY key) FROM settings x WHERE key LIKE 'channel_monitor%'))::text`
	var before string
	require.NoError(t, db.QueryRowContext(ctx, retained).Scan(&before))
	raw, err := FS.ReadFile("324_retire_passive_quality_protection.sql")
	require.NoError(t, err)
	var first string
	for n := 0; n < 2; n++ {
		tx, err := db.BeginTx(ctx, nil)
		require.NoError(t, err)
		_, err = tx.ExecContext(ctx, string(raw))
		if err != nil {
			_ = tx.Rollback()
		}
		require.NoError(t, err, "retirement run %d", n+1)
		require.NoError(t, tx.Commit())
		var after string
		require.NoError(t, db.QueryRowContext(ctx, retained).Scan(&after))
		require.JSONEq(t, before, after, "active evidence, spending and channel status must survive")
		for _, table := range []string{"passive_quality_policies", "passive_quality_states", "passive_quality_cache_reports", "group_quality_traffic_leases", "quality_suppliers"} {
			var gone bool
			require.NoError(t, db.QueryRowContext(ctx, "SELECT to_regclass($1) IS NULL", table).Scan(&gone))
			require.True(t, gone, table)
		}
		for id, want := range map[int]string{901: "active", 902: "disabled", 903: "active", 904: "quality_paused", 905: "quality_paused", 906: "quality_paused", 907: "quality_paused", 908: "active", 909: "active", 910: "quality_paused", 911: "quality_paused", 912: "quality_paused", 914: "quality_paused", 915: "quality_paused"} {
			var status string
			require.NoError(t, db.QueryRowContext(ctx, "SELECT status FROM accounts WHERE id=$1", id).Scan(&status))
			require.Equal(t, want, status, fmt.Sprintf("account %d", id))
		}
		var count int
		require.NoError(t, db.QueryRowContext(ctx, "SELECT COUNT(*) FROM accounts WHERE extra ?| ARRAY['passive_quality','group_quality_targets']").Scan(&count))
		require.Zero(t, count)
		require.NoError(t, db.QueryRowContext(ctx, "SELECT COUNT(*) FROM scheduled_test_protection_states s JOIN scheduled_test_definitions d ON d.id=s.test_definition_id WHERE d.output_kind='statistics'").Scan(&count))
		require.Zero(t, count)
		var plan string
		require.NoError(t, db.QueryRowContext(ctx, "SELECT jsonb_build_object('ids',test_definition_ids,'enabled',enabled,'rules',protection->'rules')::text FROM scheduled_test_plans WHERE id=901").Scan(&plan))
		require.JSONEq(t, `{"ids":[],"enabled":false,"rules":[]}`, plan)
		var retiredNote string
		require.NoError(t, db.QueryRowContext(ctx, "SELECT migration_note FROM scheduled_test_plans WHERE id=901").Scan(&retiredNote))
		require.NotEmpty(t, retiredNote)
		require.NoError(t, db.QueryRowContext(ctx, "SELECT rule_ids::text FROM scheduled_test_combination_states WHERE plan_id=904 AND account_id=907").Scan(&plan))
		require.JSONEq(t, `["active"]`, plan)
		var disabled, explained, retainedHold bool
		require.NoError(t, db.QueryRowContext(ctx, "SELECT protection->>'enabled'='false',migration_note<>'' FROM scheduled_test_plans WHERE id=905").Scan(&disabled, &explained))
		require.True(t, disabled)
		require.True(t, explained)
		require.NoError(t, db.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM scheduled_test_combination_states WHERE plan_id=905 AND account_id=913 AND blocked)").Scan(&retainedHold))
		require.True(t, retainedHold)

		const snapshot = `SELECT jsonb_build_object('accounts',(SELECT jsonb_agg(to_jsonb(x) ORDER BY id) FROM accounts x),'plans',(SELECT jsonb_agg(to_jsonb(x) ORDER BY id) FROM scheduled_test_plans x),'archives',(SELECT jsonb_agg(to_jsonb(x) ORDER BY kind,source_id) FROM quality_management_config_archive x),'outbox',(SELECT jsonb_agg(to_jsonb(x) ORDER BY id) FROM scheduler_outbox x))::text`
		var current string
		require.NoError(t, db.QueryRowContext(ctx, snapshot).Scan(&current))
		if n == 0 {
			first = current
		} else {
			require.JSONEq(t, first, current, "reapplying must be a no-op")
		}
	}
}

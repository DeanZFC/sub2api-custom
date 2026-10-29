//go:build integration

package migrations

import (
	"context"
	"database/sql"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

func TestRetireChannelQualityScoreMigration(t *testing.T) {
	for _, existing := range []bool{false, true} {
		name := "archives final configuration"
		if existing {
			name = "preserves existing retirement archive"
		}
		t.Run(name, func(t *testing.T) {
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
			schema := "retire_score_" + uuid.New().String()[:8]
			_, err = conn.ExecContext(ctx, "CREATE SCHEMA "+schema)
			require.NoError(t, err)
			t.Cleanup(func() { _, _ = conn.ExecContext(context.Background(), "DROP SCHEMA "+schema+" CASCADE") })
			_, err = conn.ExecContext(ctx, "SET search_path TO "+schema)
			require.NoError(t, err)
			_, err = conn.ExecContext(ctx, `
CREATE TABLE accounts (id BIGINT PRIMARY KEY, status TEXT, schedulable BOOLEAN, extra JSONB);
CREATE TABLE usage_logs (id BIGINT PRIMARY KEY, account_id BIGINT REFERENCES accounts(id), model TEXT,
 input_tokens BIGINT, cache_read_tokens BIGINT, output_tokens BIGINT, first_token_ms INTEGER, duration_ms INTEGER, created_at TIMESTAMPTZ);
CREATE TABLE passive_quality_cache_reports (account_id BIGINT REFERENCES accounts(id), request_key TEXT,
 attempt_key TEXT, cache_reported BOOLEAN, created_at TIMESTAMPTZ, PRIMARY KEY(account_id,request_key));
CREATE TABLE passive_quality_failures (id BIGINT PRIMARY KEY, account_id BIGINT REFERENCES accounts(id),
 model TEXT, request_key TEXT, started_at TIMESTAMPTZ, created_at TIMESTAMPTZ);
CREATE TABLE passive_quality_policies (id BIGINT PRIMARY KEY, config JSONB, version BIGINT);
CREATE TABLE passive_quality_states (policy_id BIGINT REFERENCES passive_quality_policies(id),
 account_id BIGINT REFERENCES accounts(id), model TEXT, data JSONB, PRIMARY KEY(policy_id,account_id,model));
CREATE TABLE scheduled_test_plans (id BIGINT PRIMARY KEY, protection JSONB);
CREATE TABLE scheduled_test_results (id BIGINT PRIMARY KEY, plan_id BIGINT REFERENCES scheduled_test_plans(id),
 account_id BIGINT REFERENCES accounts(id), model_id TEXT, status TEXT, response_text TEXT, started_at TIMESTAMPTZ);
CREATE TABLE scheduled_test_protection_states (plan_id BIGINT REFERENCES scheduled_test_plans(id),
 account_id BIGINT REFERENCES accounts(id), result_id BIGINT REFERENCES scheduled_test_results(id), blocked BOOLEAN, rule_config JSONB);
CREATE TABLE quality_management_config_archive (kind TEXT, source_id BIGINT, config JSONB,
 archived_at TIMESTAMPTZ DEFAULT now(), PRIMARY KEY(kind,source_id));
`)
			require.NoError(t, err)
			// Install the original schema so started_at is the shared column that
			// shipped with scoring, rather than a replacement test-only column.
			original, err := FS.ReadFile("313_channel_quality_score.sql")
			require.NoError(t, err)
			_, err = conn.ExecContext(ctx, string(original))
			require.NoError(t, err)
			const finalConfig = `{"enabled":false,"window_seconds":1800,"max_requests":250,"min_samples":30,
"error_rate":{"weight":65,"good":1,"bad":25},"cache_rate":{"weight":15,"good":90,"bad":5},
"ttft":{"weight":10,"good":1500,"bad":12000},"duration":{"weight":0,"good":10000,"bad":90000},
"tps":{"weight":10,"good":60,"bad":8}}`
			_, err = conn.ExecContext(ctx, `UPDATE channel_quality_score_config SET config=$1::jsonb,version=9,
collect_since='2026-10-28T01:00:00Z',updated_at='2026-10-29T02:00:00Z' WHERE id=1`, finalConfig)
			require.NoError(t, err)
			_, err = conn.ExecContext(ctx, `
INSERT INTO quality_management_config_archive(kind,source_id,config,archived_at) VALUES
 ('score',1,'{"error_rate":{"weight":40},"original":true}','2026-10-20T00:00:00Z'),
 ('active',11,'{"mode":"all_pass","original":true}','2026-10-20T00:00:00Z'),
 ('passive',21,'{"enabled":true,"original":true}','2026-10-20T00:00:00Z');
INSERT INTO accounts VALUES (62,'active',true,'{"passive_quality":[{"policy_id":21,"model":"public-model","phase":"paused"}],"active_quality":{"blocked":true}}');
INSERT INTO usage_logs VALUES (101,62,'public-model',1000,800,100,1500,4000,'2026-10-29T02:01:04Z');
INSERT INTO passive_quality_cache_reports VALUES (62,'usage-101','turn-101',true,'2026-10-29T02:01:04Z','2026-10-29T02:01:00Z');
INSERT INTO passive_quality_failures VALUES (102,62,'public-model','failed-turn','2026-10-29T02:02:00Z','2026-10-29T02:02:02Z');
INSERT INTO passive_quality_policies VALUES (21,'{"enabled":true,"purpose":"group_quality_target","group_ids":[8]}',4);
INSERT INTO passive_quality_states VALUES (21,62,'public-model','{"phase":"paused","generation":7,"availability":{"phase":"paused"}}');
INSERT INTO scheduled_test_plans VALUES (11,'{"enabled":true,"mode":"all_pass"}');
INSERT INTO scheduled_test_results VALUES (201,11,62,'public-model','success','historical intelligence result','2026-10-29T02:03:00Z');
INSERT INTO scheduled_test_protection_states VALUES (11,62,201,true,'{"expected_answer":"21"}');
`)
			require.NoError(t, err)
			if existing {
				_, err = conn.ExecContext(ctx, `INSERT INTO quality_management_config_archive(kind,source_id,config,archived_at)
VALUES ('retired_score',1,'{"original_retirement_archive":true,"version":6}','2026-10-27T00:00:00Z')`)
				require.NoError(t, err)
			}
			const historicalArchives = `SELECT jsonb_agg(to_jsonb(a) ORDER BY kind,source_id)::text FROM quality_management_config_archive a WHERE kind<>'retired_score'`
			const sharedEvidence = `SELECT jsonb_build_object(
 'accounts',(SELECT jsonb_agg(to_jsonb(a) ORDER BY id) FROM accounts a),
 'usage',(SELECT jsonb_agg(to_jsonb(u) ORDER BY id) FROM usage_logs u),
 'cache',(SELECT jsonb_agg(to_jsonb(c) ORDER BY request_key) FROM passive_quality_cache_reports c),
 'failures',(SELECT jsonb_agg(to_jsonb(f) ORDER BY id) FROM passive_quality_failures f),
 'policies',(SELECT jsonb_agg(to_jsonb(p) ORDER BY id) FROM passive_quality_policies p),
 'passive_holds',(SELECT jsonb_agg(to_jsonb(s) ORDER BY policy_id,account_id,model) FROM passive_quality_states s),
 'plans',(SELECT jsonb_agg(to_jsonb(p) ORDER BY id) FROM scheduled_test_plans p),
 'results',(SELECT jsonb_agg(to_jsonb(r) ORDER BY id) FROM scheduled_test_results r),
 'active_holds',(SELECT jsonb_agg(to_jsonb(s) ORDER BY plan_id,account_id) FROM scheduled_test_protection_states s))::text`
			var archivesBefore, evidenceBefore string
			require.NoError(t, conn.QueryRowContext(ctx, historicalArchives).Scan(&archivesBefore))
			require.NoError(t, conn.QueryRowContext(ctx, sharedEvidence).Scan(&evidenceBefore))
			migration, err := FS.ReadFile("322_retire_channel_quality_score.sql")
			require.NoError(t, err)
			var retirementBefore string
			for run := 0; run < 2; run++ {
				_, err = conn.ExecContext(ctx, string(migration))
				require.NoError(t, err, "migration run %d", run+1)
				var retired bool
				require.NoError(t, conn.QueryRowContext(ctx, `SELECT to_regclass('channel_quality_score_config') IS NULL`).Scan(&retired))
				require.True(t, retired, "dedicated scoring storage must be retired")
				var archivesAfter, evidenceAfter string
				require.NoError(t, conn.QueryRowContext(ctx, historicalArchives).Scan(&archivesAfter))
				require.JSONEq(t, archivesBefore, archivesAfter, "older score, active and passive archives must remain intact")
				require.NoError(t, conn.QueryRowContext(ctx, sharedEvidence).Scan(&evidenceAfter))
				require.JSONEq(t, evidenceBefore, evidenceAfter, "retiring scoring must retain traffic starts, metrics, holds and test history")
				var retirementAfter string
				require.NoError(t, conn.QueryRowContext(ctx, `SELECT to_jsonb(a)::text FROM quality_management_config_archive a WHERE kind='retired_score' AND source_id=1`).Scan(&retirementAfter))
				if run == 1 {
					require.JSONEq(t, retirementBefore, retirementAfter, "reapplying must not duplicate or rewrite the final archive")
				}
				retirementBefore = retirementAfter
			}
			var archivedConfig string
			if existing {
				require.NoError(t, conn.QueryRowContext(ctx, `SELECT config::text FROM quality_management_config_archive WHERE kind='retired_score' AND source_id=1`).Scan(&archivedConfig))
				require.JSONEq(t, `{"original_retirement_archive":true,"version":6}`, archivedConfig)
				var archivedAt time.Time
				require.NoError(t, conn.QueryRowContext(ctx, `SELECT archived_at FROM quality_management_config_archive WHERE kind='retired_score' AND source_id=1`).Scan(&archivedAt))
				require.Equal(t, time.Date(2026, 10, 27, 0, 0, 0, 0, time.UTC), archivedAt.UTC())
			} else {
				var version int64
				var collectSince, updatedAt time.Time
				require.NoError(t, conn.QueryRowContext(ctx, `SELECT (config->'config')::text,(config->>'version')::bigint,
(config->>'collect_since')::timestamptz,(config->>'updated_at')::timestamptz
FROM quality_management_config_archive WHERE kind='retired_score' AND source_id=1`).Scan(&archivedConfig, &version, &collectSince, &updatedAt))
				require.JSONEq(t, finalConfig, archivedConfig, "archive the last customized configuration, not historical defaults")
				require.EqualValues(t, 9, version)
				require.Equal(t, time.Date(2026, 10, 28, 1, 0, 0, 0, time.UTC), collectSince.UTC())
				require.Equal(t, time.Date(2026, 10, 29, 2, 0, 0, 0, time.UTC), updatedAt.UTC())
			}
		})
	}
}

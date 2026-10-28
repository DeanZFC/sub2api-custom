//go:build integration

package repository

import (
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/Wei-Shaw/sub2api/migrations"
	"github.com/stretchr/testify/require"
)

func TestScheduledTestPublicDisplayEvidenceIntegration(t *testing.T) {
	f := newPassRateFixture(t, 10)
	f.exec(`CREATE TABLE usage_logs(id BIGSERIAL PRIMARY KEY,account_id BIGINT,request_id TEXT,created_at TIMESTAMPTZ,model TEXT DEFAULT 'model',requested_model TEXT,duration_ms BIGINT DEFAULT 0,request_type SMALLINT DEFAULT 2,stream BOOLEAN DEFAULT true,openai_ws_mode BOOLEAN DEFAULT false,first_token_ms BIGINT,input_tokens BIGINT DEFAULT 100,output_tokens BIGINT DEFAULT 1,cache_creation_tokens BIGINT DEFAULT 0,cache_read_tokens BIGINT DEFAULT 0,image_input_tokens BIGINT DEFAULT 0,image_output_tokens BIGINT DEFAULT 0,image_count INT DEFAULT 0,video_count INT DEFAULT 0,actual_cost NUMERIC DEFAULT 0,total_cost NUMERIC DEFAULT 0,input_cost NUMERIC DEFAULT 0,cache_creation_cost NUMERIC DEFAULT 0,cache_read_cost NUMERIC DEFAULT 0,native_compaction_v2 BOOLEAN DEFAULT false,inbound_endpoint TEXT,upstream_endpoint TEXT)`)
	for _, name := range []string{"291_passive_quality.sql", "301_passive_quality_reuse_evidence.sql", "310_passive_quality_telemetry_scope.sql", "313_channel_quality_score.sql"} {
		raw, err := migrations.FS.ReadFile(name)
		require.NoError(t, err)
		f.exec(string(raw))
	}
	f.exec(`UPDATE channel_quality_score_config SET collect_since=now()-INTERVAL '2 days';
INSERT INTO accounts(id,name) VALUES(64,'Unauthorized account');
INSERT INTO groups(id,name,is_exclusive) VALUES(9,'Private group',true);
INSERT INTO account_groups VALUES(64,9)`)
	config, err := getChannelQualityScoreConfig(f.ctx, f.db)
	require.NoError(t, err)
	config.MinSamples, config.MaxRequests = 5, 10
	config.ErrorRate.Weight, config.CacheRate.Weight, config.TTFT.Weight, config.Duration.Weight = 100, 0, 0, 0
	require.NoError(t, saveChannelQualityScoreConfig(f.ctx, f.db, &config))
	at := time.Now().Add(-time.Minute)
	f.exec(`INSERT INTO usage_logs(account_id,request_id,created_at,model,requested_model,first_token_ms,duration_ms)
 SELECT 62,'good-'||i,$1::timestamptz+i*INTERVAL '1 second','upstream-private','model-a',2000,10000 FROM generate_series(1,5) i`, at)
	f.exec(`INSERT INTO passive_quality_failures(account_id,model,request_key,started_at,created_at)
 SELECT 62,'model-b','bad-'||i,$1::timestamptz,$1::timestamptz+i*INTERVAL '1 second' FROM generate_series(1,5) i`, at)
	seedScoreFixtureReports(t, f.db, at.Add(-time.Second))
	var latest *service.ScheduledTestResult
	for _, verdict := range []string{"fail", "fail", "pass", "pass", "pass", "pass", "pass", "pass", "pass", "pass"} {
		latest = f.result(verdict)
	}
	create := func(accountID *int64, groupID int64, model string) {
		t.Helper()
		target := "all_accounts"
		if accountID == nil {
			target = "group"
		}
		_, err := f.repo.Create(f.ctx, &service.ScheduledTestResult{PlanID: f.plan.ID, AccountID: accountID, GroupID: &groupID, TestDefinitionID: &f.definitionID, TargetMode: target, ModelID: model, RunID: latest.RunID, Status: "success", OutputKind: "number", ResponseText: "21", StartedAt: at, FinishedAt: at})
		require.NoError(t, err)
	}
	account62, account63, account64 := int64(62), int64(63), int64(64)
	create(&account62, 8, "model-b")
	create(&account63, 8, "model-a")
	create(&account64, 9, "model-a")
	create(nil, 8, "model-a")
	// Releasing the visible query's connection before enrichment also works
	// with a one-connection pool and avoids nested-reader deadlocks.
	f.db.SetMaxOpenConns(1)
	visible, err := f.repo.ListVisible(f.ctx, 100, 3)
	require.NoError(t, err)
	require.Len(t, visible, 4)
	for _, row := range visible {
		if row.AccountID == nil {
			require.Nil(t, row.QualityScore)
			require.Nil(t, row.PassRate)
			continue
		}
		require.NotEqual(t, account64, *row.AccountID, "unauthorized group results and their scores stay hidden")
		require.NotNil(t, row.QualityScore)
		require.Equal(t, row.ModelID, row.QualityScore.Model, "score follows the public requested model")
		switch {
		case *row.AccountID == 62 && row.ModelID == "model-a":
			require.NotNil(t, row.PassRate)
			require.Equal(t, 10, row.PassRate.Samples)
			require.Equal(t, 8, row.PassRate.Passed)
			require.Equal(t, 80., *row.PassRate.PassRate)
			require.False(t, row.PassRate.Blocked, "the configured strict pause threshold is unchanged by reads")
			require.Equal(t, 100., *row.QualityScore.Value)
		case *row.AccountID == 62 && row.ModelID == "model-b":
			require.Nil(t, row.PassRate, "a different model cannot inherit capability observations")
			require.Equal(t, 0., *row.QualityScore.Value)
		default:
			require.Nil(t, row.PassRate, "a different account cannot inherit capability observations")
			require.Equal(t, "insufficient", row.QualityScore.Status)
			require.Nil(t, row.QualityScore.Value, "no traffic means unknown, never a fabricated zero")
		}
	}
	require.Equal(t, 10, f.evidenceCount(), "reading display evidence cannot create governance samples")
}

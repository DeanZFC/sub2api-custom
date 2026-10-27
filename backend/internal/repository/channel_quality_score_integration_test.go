//go:build integration

package repository

import (
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

// This helper supplies explicit request-start evidence for synthetic score
// fixtures. Production never derives or backfills a turn start from usage rows.
func seedScoreFixtureReports(t *testing.T, db *sql.DB, started time.Time) {
	t.Helper()
	_, err := db.Exec(`INSERT INTO passive_quality_cache_reports(account_id,request_key,created_at,started_at)
 SELECT account_id,request_id,min(created_at),$1 FROM usage_logs GROUP BY account_id,request_id
 ON CONFLICT(account_id,request_key) DO UPDATE SET started_at=COALESCE(passive_quality_cache_reports.started_at,excluded.started_at)`, started)
	require.NoError(t, err)
}

func TestChannelQualityScoreConfigurationAndCacheIsolationIntegration(t *testing.T) {
	ctx, db, r := passiveDB(t)
	c, err := r.GetQualityScoreConfig(ctx)
	require.NoError(t, err)
	require.Equal(t, service.DefaultChannelQualityScoreConfig().ErrorRate, c.ErrorRate)
	require.EqualValues(t, 1, c.Version)
	_, err = db.Exec(`INSERT INTO usage_logs(account_id,request_id,created_at,first_token_ms,duration_ms)
 SELECT 62,'s-'||i,now()-i*INTERVAL '1 second',5000,10000 FROM generate_series(1,20) i`)
	require.NoError(t, err)
	seedScoreFixtureReports(t, db, time.Now().Add(-5*time.Minute))
	a, err := r.ChannelQualityScores(ctx, []int64{62, 63, 62}, "model")
	require.NoError(t, err)
	require.Len(t, a, 2)
	require.True(t, a[62].IsRankable())
	require.Equal(t, "insufficient", a[63].Status)
	original := *a[62].Value
	*a[62].Value = 0
	a[62].Dimensions[0].Key = "mutated"
	*a[62].Dimensions[0].Value = 88
	seedScoreFixtureReports(t, db, time.Now().Add(-5*time.Minute))
	b, err := r.ChannelQualityScores(ctx, []int64{62}, "model")
	require.NoError(t, err)
	require.Equal(t, original, *b[62].Value)
	require.Equal(t, "error_rate", b[62].Dimensions[0].Key)
	require.Equal(t, 0., *b[62].Dimensions[0].Value, "callers must not mutate shared cached evidence")
	stale := c
	c.TTFT.Good = 6000
	require.NoError(t, r.SaveQualityScoreConfig(ctx, &c))
	require.EqualValues(t, 2, c.Version)
	require.ErrorIs(t, r.SaveQualityScoreConfig(ctx, &stale), service.ErrChannelQualityScoreConflict)
	seedScoreFixtureReports(t, db, time.Now().Add(-5*time.Minute))
	b, err = r.ChannelQualityScores(ctx, []int64{62}, "model")
	require.NoError(t, err)
	require.Equal(t, 100., *b[62].Value, "config edits bypass cached old standard immediately")
	// A computation retains its chosen standard even if a different standard was
	// persisted before its evidence query begins.
	old, err := queryChannelQualityScoresUncached(ctx, db, []int64{62}, "model", stale)
	require.NoError(t, err)
	require.Equal(t, original, *old[62].Value)
	c.Enabled = false
	require.NoError(t, r.SaveQualityScoreConfig(ctx, &c))
	seedScoreFixtureReports(t, db, time.Now().Add(-5*time.Minute))
	b, err = r.ChannelQualityScores(ctx, []int64{62}, "model")
	require.NoError(t, err)
	require.Equal(t, "disabled", b[62].Status)
	require.Nil(t, b[62].Value)
}
func TestChannelQualityScoreRealAttemptsAndPublicModelIntegration(t *testing.T) {
	ctx, db, r := passiveDB(t)
	c, err := r.GetQualityScoreConfig(ctx)
	require.NoError(t, err)
	c.MinSamples = 5
	c.MaxRequests = 10
	require.NoError(t, r.SaveQualityScoreConfig(ctx, &c))
	at := time.Now().Add(-time.Minute)
	_, err = db.Exec(`INSERT INTO usage_logs(account_id,request_id,created_at,model,requested_model,first_token_ms,duration_ms)
 SELECT 62,'valid-'||i,$1::timestamptz+i*INTERVAL '1 second','upstream','public',2000,10000 FROM generate_series(1,9) i`, at)
	require.NoError(t, err)
	// A WS usage ID corresponds to an already failed attempt. It contributes one
	// failed outcome, not both a success and a failure, regardless of later usage.
	for _, query := range []string{
		`INSERT INTO passive_quality_failures(account_id,model,request_key,started_at,created_at) VALUES(62,'public','attempt', $1,$1)`,
		`INSERT INTO passive_quality_cache_reports(account_id,request_key,attempt_key,created_at) VALUES(62,'ws-usage','attempt',$1)`,
		`INSERT INTO usage_logs(account_id,request_id,created_at,model,requested_model,first_token_ms,duration_ms) VALUES(62,'ws-usage',$1,'upstream','public',2000,10000)`,
	} {
		_, err = db.Exec(query, at.Add(10*time.Second))
		require.NoError(t, err)
	}
	_, err = db.Exec(`INSERT INTO usage_logs(account_id,request_id,created_at,model,requested_model,request_type,input_tokens,output_tokens)
 VALUES(62,'test',$1,'upstream','public',4,100,1),(62,'quality-probe',$1,'upstream','public',6,100,1),
 (62,'empty',$1,'upstream','public',2,0,0),(62,'other',$1,'public','other-public',2,100,1),
 (62,'expired',$1-INTERVAL '1 hour','upstream','public',2,100,1)`, time.Now().Add(-time.Second))
	require.NoError(t, err)
	seedScoreFixtureReports(t, db, time.Now().Add(-5*time.Minute))
	scores, err := r.ChannelQualityScores(ctx, []int64{62}, "public")
	require.NoError(t, err)
	s := scores[62]
	require.Equal(t, 10, s.Samples)
	require.Equal(t, 10., *s.Dimensions[0].Value)
	require.Equal(t, 9, s.Dimensions[2].Samples)
	require.Equal(t, 10000., *s.Dimensions[3].Value)
	require.Equal(t, "partial", s.Status)
	require.InDelta(t, 73.33, *s.Value, .01)
	seedScoreFixtureReports(t, db, time.Now().Add(-5*time.Minute))
	scores, err = r.ChannelQualityScores(ctx, []int64{62}, "upstream")
	require.NoError(t, err)
	require.Equal(t, 0, scores[62].Samples)
	require.Nil(t, scores[62].Value)
	// Scoped telemetry gaps invalidate only matching account/model evidence.
	_, err = db.Exec(`INSERT INTO passive_quality_telemetry_gap_scopes(created_at,ended_at,account_id,model,kind) VALUES($1,$1,63,'public','failure')`, at)
	require.NoError(t, err)
	invalidateChannelQualityScoreCache(db)
	seedScoreFixtureReports(t, db, time.Now().Add(-5*time.Minute))
	scores, err = r.ChannelQualityScores(ctx, []int64{62}, "public")
	require.NoError(t, err)
	require.True(t, scores[62].IsRankable())
	_, err = db.Exec(`UPDATE passive_quality_telemetry_gap_scopes SET account_id=62`)
	require.NoError(t, err)
	invalidateChannelQualityScoreCache(db)
	seedScoreFixtureReports(t, db, time.Now().Add(-5*time.Minute))
	scores, err = r.ChannelQualityScores(ctx, []int64{62}, "public")
	require.NoError(t, err)
	require.Equal(t, "incomplete", scores[62].Status)
	require.Nil(t, scores[62].Value)
}
func TestChannelQualityScoreCacheRequiresProvenReuseIntegration(t *testing.T) {
	ctx, db, r := passiveDB(t)
	c, err := r.GetQualityScoreConfig(ctx)
	require.NoError(t, err)
	c.MinSamples = 5
	require.NoError(t, r.SaveQualityScoreConfig(ctx, &c))
	at := time.Now().Add(-2 * time.Minute)
	recordCacheRequest(t, ctx, db, r, 62, "warm", "session", "prefix", at, 0)
	for i := 1; i <= 5; i++ {
		recordCacheRequest(t, ctx, db, r, 62, fmt.Sprintf("reuse-%d", i), "session", "prefix", at.Add(time.Duration(i)*time.Second), 0)
	}
	for i := 1; i <= 5; i++ {
		recordCacheRequest(t, ctx, db, r, 62, fmt.Sprintf("cold-%d", i), fmt.Sprintf("new-session-%d", i), "prefix", at.Add(time.Duration(i+10)*time.Second), 0)
	}
	_, err = db.Exec(`INSERT INTO usage_logs(account_id,request_id,created_at,first_token_ms,duration_ms,cache_read_tokens) VALUES(62,'no-scope',$1,50,100,2048)`, at.Add(20*time.Second))
	require.NoError(t, err)
	seedScoreFixtureReports(t, db, time.Now().Add(-5*time.Minute))
	scores, err := r.ChannelQualityScores(ctx, []int64{62}, "model")
	require.NoError(t, err)
	s := scores[62]
	require.Equal(t, 12, s.Samples)
	require.Equal(t, "ready", s.Status)
	require.Equal(t, 5, s.Dimensions[1].Samples, "cold and unknown opportunities are not zero-hit failures")
	require.Equal(t, 0., *s.Dimensions[1].Value)
	require.Equal(t, 75., *s.Value)
	// Omitted hit telemetry is unknown even with digest proof, never a zero hit.
	_, err = db.Exec(`UPDATE passive_quality_cache_reports SET cache_reported=false WHERE request_key LIKE 'reuse-%'`)
	require.NoError(t, err)
	invalidateChannelQualityScoreCache(db)
	seedScoreFixtureReports(t, db, time.Now().Add(-5*time.Minute))
	scores, err = r.ChannelQualityScores(ctx, []int64{62}, "model")
	require.NoError(t, err)
	require.Equal(t, "partial", scores[62].Status)
	require.Nil(t, scores[62].Dimensions[1].Value)
	require.Equal(t, 100., *scores[62].Value)
}
func TestChannelQualityScoreDedupAndBoundedScanIntegration(t *testing.T) {
	ctx, db, r := passiveDB(t)
	c, err := r.GetQualityScoreConfig(ctx)
	require.NoError(t, err)
	c.MinSamples = 5
	c.MaxRequests = 5
	require.NoError(t, r.SaveQualityScoreConfig(ctx, &c))
	_, err = db.Exec(`INSERT INTO usage_logs(account_id,request_id,created_at,first_token_ms,duration_ms)
 SELECT 62,'duplicate',now()-i*INTERVAL '1 millisecond',2000,10000 FROM generate_series(1,30) i`)
	require.NoError(t, err)
	seedScoreFixtureReports(t, db, time.Now().Add(-5*time.Minute))
	scores, err := r.ChannelQualityScores(ctx, []int64{62}, "model")
	require.NoError(t, err)
	require.Equal(t, 1, scores[62].Samples)
	require.Equal(t, "incomplete", scores[62].Status)
	require.Nil(t, scores[62].Value, "a truncated pre-dedup scan must not invent a complete window")
	_, err = db.Exec(`INSERT INTO usage_logs(account_id,request_id,created_at,first_token_ms,duration_ms)
 SELECT 62,'new-'||i,now(),2000,0 FROM generate_series(1,5) i`)
	require.NoError(t, err)
	invalidateChannelQualityScoreCache(db)
	seedScoreFixtureReports(t, db, time.Now().Add(-5*time.Minute))
	scores, err = r.ChannelQualityScores(ctx, []int64{62}, "model")
	require.NoError(t, err)
	require.Equal(t, 5, scores[62].Samples)
	require.True(t, scores[62].IsRankable(), "a full newest deduplicated cap is a complete score window")
}
func TestChannelQualityScoreTruncatedSuccessesCannotBeReplacedWithOlderFailuresIntegration(t *testing.T) {
	ctx, db, r := passiveDB(t)
	c, err := r.GetQualityScoreConfig(ctx)
	require.NoError(t, err)
	c.MinSamples = 5
	c.MaxRequests = 5
	require.NoError(t, r.SaveQualityScoreConfig(ctx, &c))
	at := time.Now().Add(-time.Minute)
	// The scan budget sees only duplicates. Four actual newer successes sit just
	// beyond it, while four much older failures could otherwise fill the cap.
	_, err = db.Exec(`INSERT INTO usage_logs(account_id,request_id,created_at,first_token_ms,duration_ms)
 SELECT 62,'duplicate',$1::timestamptz-i*INTERVAL '1 millisecond',2000,10000 FROM generate_series(1,21) i`, at)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO usage_logs(account_id,request_id,created_at,first_token_ms,duration_ms)
 SELECT 62,'older-success-'||i,$1::timestamptz-i*INTERVAL '1 second',2000,10000 FROM generate_series(1,4) i`, at)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO passive_quality_failures(account_id,model,request_key,started_at,created_at)
 SELECT 62,'model','older-failure-'||i,$1::timestamptz-INTERVAL '1 minute',$1::timestamptz-INTERVAL '1 minute' FROM generate_series(1,4) i`, at)
	require.NoError(t, err)
	seedScoreFixtureReports(t, db, time.Now().Add(-5*time.Minute))
	scores, err := r.ChannelQualityScores(ctx, []int64{62}, "model")
	require.NoError(t, err)
	require.Equal(t, 5, scores[62].Samples)
	require.Equal(t, "incomplete", scores[62].Status)
	require.Nil(t, scores[62].Value, "older failures cannot establish a complete cap while newer successes are outside the scan budget")
}
func TestChannelQualityScoreCollectSinceExcludesUntrustedHistoryIntegration(t *testing.T) {
	ctx, db, r := passiveDB(t)
	_, err := db.Exec(`UPDATE channel_quality_score_config SET collect_since=now()`)
	require.NoError(t, err)
	c, err := r.GetQualityScoreConfig(ctx)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO usage_logs(account_id,request_id,created_at,first_token_ms,duration_ms)
 SELECT 62,'old-'||i,$1::timestamptz-i*INTERVAL '1 second',2000,10000 FROM generate_series(1,20) i`, c.EvidenceSince)
	require.NoError(t, err)
	scores, err := r.ChannelQualityScores(ctx, []int64{62}, "model")
	require.NoError(t, err)
	require.Zero(t, scores[62].Samples)
	require.Nil(t, scores[62].Value)
	// Saving a new standard must not reset or accept a client-supplied collection boundary.
	c.MinSamples = 5
	original := c.EvidenceSince
	c.EvidenceSince = time.Now().Add(-time.Hour)
	require.NoError(t, r.SaveQualityScoreConfig(ctx, &c))
	saved, err := r.GetQualityScoreConfig(ctx)
	require.NoError(t, err)
	require.True(t, saved.EvidenceSince.Equal(original))
	_, err = db.Exec(`INSERT INTO usage_logs(account_id,request_id,created_at,first_token_ms,duration_ms)
 SELECT 62,'new-'||i,now(),2000,0 FROM generate_series(1,5) i`)
	require.NoError(t, err)
	seedScoreFixtureReports(t, db, original)
	invalidateChannelQualityScoreCache(db)
	scores, err = r.ChannelQualityScores(ctx, []int64{62}, "model")
	require.NoError(t, err)
	require.Equal(t, 5, scores[62].Samples)
	require.True(t, scores[62].IsRankable())
}

func TestChannelQualityScoreCacheSwitchDetectionIsModelScopedIntegration(t *testing.T) {
	ctx, db, r := passiveDB(t)
	c, err := r.GetQualityScoreConfig(ctx)
	require.NoError(t, err)
	c.MinSamples = 5
	require.NoError(t, r.SaveQualityScoreConfig(ctx, &c))
	at := time.Now().Add(-time.Minute)
	recordCacheRequest(t, ctx, db, r, 62, "warm", "same-session", "prefix", at, 0)
	for i := 1; i <= 5; i++ {
		other := fmt.Sprintf("other-model-%d", i)
		recordCacheRequest(t, ctx, db, r, 63, other, "same-session", "prefix", at.Add(time.Duration(2*i)*time.Second), 0)
		_, err = db.Exec(`UPDATE passive_quality_cache_reports SET cache_metadata=jsonb_set(cache_metadata,'{model}','"other-model"') WHERE account_id=63 AND request_key=$1`, other)
		require.NoError(t, err)
		recordCacheRequest(t, ctx, db, r, 62, fmt.Sprintf("reuse-%d", i), "same-session", "prefix", at.Add(time.Duration(2*i+1)*time.Second), 0)
	}
	seedScoreFixtureReports(t, db, time.Now().Add(-5*time.Minute))
	scores, err := r.ChannelQualityScores(ctx, []int64{62}, "model")
	require.NoError(t, err)
	require.Equal(t, 5, scores[62].Dimensions[1].Samples, "other-model traffic cannot turn this model's existing cache scope into a cold switch")
	require.Equal(t, "ready", scores[62].Status)
}
func TestChannelQualityScoreCollectSinceRequiresRequestStartIntegration(t *testing.T) {
	ctx, db, r := passiveDB(t)
	boundary := time.Now().Add(-time.Minute)
	_, err := db.Exec(`UPDATE channel_quality_score_config SET collect_since=$1`, boundary)
	require.NoError(t, err)
	c, err := r.GetQualityScoreConfig(ctx)
	require.NoError(t, err)
	c.MinSamples = 5
	require.NoError(t, r.SaveQualityScoreConfig(ctx, &c))
	// Delayed worker persistence can make created_at-duration appear newer
	// than capture even when the real request start predates it. Only the
	// frozen turn start is authoritative; a missing/invalid start is unknown.
	_, err = db.Exec(`INSERT INTO usage_logs(account_id,request_id,created_at,first_token_ms,duration_ms)
 VALUES(62,'cross-upgrade',$1::timestamptz+INTERVAL '10 seconds',2000,100),
 (62,'unknown-start',$1::timestamptz+INTERVAL '10 seconds',2000,100),
 (62,'invalid-start',$1::timestamptz+INTERVAL '10 seconds',2000,100)`, boundary)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO passive_quality_failures(account_id,model,request_key,started_at,created_at)
 VALUES(62,'model','cross-upgrade-failure',$1::timestamptz-INTERVAL '1 second',$1::timestamptz+INTERVAL '10 seconds')`, boundary)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO usage_logs(account_id,request_id,created_at,first_token_ms,duration_ms)
 SELECT 62,'valid-'||i,$1::timestamptz+INTERVAL '20 seconds',2000,10000 FROM generate_series(1,4) i`, boundary)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO passive_quality_cache_reports(account_id,request_key,created_at,started_at)
 SELECT account_id,request_id,created_at,CASE WHEN request_id='cross-upgrade' THEN $1::timestamptz-INTERVAL '1 second'
 WHEN request_id='invalid-start' THEN created_at+INTERVAL '1 second' ELSE $1::timestamptz+INTERVAL '1 second' END
 FROM usage_logs WHERE request_id<>'unknown-start'`, boundary)
	require.NoError(t, err)
	scores, err := r.ChannelQualityScores(ctx, []int64{62}, "model")
	require.NoError(t, err)
	require.Equal(t, 4, scores[62].Samples)
	require.Equal(t, "incomplete", scores[62].Status)
	require.Nil(t, scores[62].Value, "unknown starts must not bias the error rate by silently discarding successes")
	_, err = db.Exec(`DELETE FROM usage_logs WHERE request_id IN ('unknown-start','invalid-start')`)
	require.NoError(t, err)
	invalidateChannelQualityScoreCache(db)
	scores, err = r.ChannelQualityScores(ctx, []int64{62}, "model")
	require.NoError(t, err)
	require.Equal(t, 4, scores[62].Samples)
	require.Equal(t, "insufficient", scores[62].Status)
	require.Nil(t, scores[62].Value, "known pre-upgrade starts are excluded and do not fill the sample floor")
	_, err = db.Exec(`INSERT INTO passive_quality_failures(account_id,model,request_key,started_at,created_at)
 VALUES(62,'model','valid-failure',$1::timestamptz+INTERVAL '1 second',$1::timestamptz+INTERVAL '10 seconds')`, boundary)
	require.NoError(t, err)
	invalidateChannelQualityScoreCache(db)
	scores, err = r.ChannelQualityScores(ctx, []int64{62}, "model")
	require.NoError(t, err)
	require.Equal(t, 5, scores[62].Samples)
	require.Equal(t, 20., *scores[62].Dimensions[0].Value)
	require.True(t, scores[62].IsRankable())
}

func TestChannelQualityScoreUnknownStartWithoutEligibleOutcomesIntegration(t *testing.T) {
	ctx, db, r := passiveDB(t)
	_, err := db.Exec(`INSERT INTO usage_logs(account_id,request_id,created_at,duration_ms) VALUES(62,'unknown-start',now(),NULL)`)
	require.NoError(t, err)
	scores, err := r.ChannelQualityScores(ctx, []int64{62}, "model")
	require.NoError(t, err)
	require.Zero(t, scores[62].Samples)
	require.Equal(t, "incomplete", scores[62].Status)
	require.Nil(t, scores[62].Value)
}

func TestChannelQualityScoreTrustsCapturedStartWithoutCacheOrDurationIntegration(t *testing.T) {
	ctx, db, r := passiveDB(t)
	c, err := r.GetQualityScoreConfig(ctx)
	require.NoError(t, err)
	c.MinSamples = 5
	require.NoError(t, r.SaveQualityScoreConfig(ctx, &c))
	at := time.Now().Add(-time.Minute)
	_, err = db.Exec(`INSERT INTO usage_logs(account_id,request_id,created_at,model,duration_ms,first_token_ms)
 SELECT 62,'plain-'||i,$1,'non-openai',NULL,2000 FROM generate_series(1,5) i`, at)
	require.NoError(t, err)
	for i := 1; i <= 5; i++ {
		require.NoError(t, r.RecordTelemetry(ctx, []service.PassiveQualityFailure{{AccountID: 62, RequestKey: fmt.Sprintf("plain-%d", i), AttemptKey: fmt.Sprintf("turn-%d", i), StartedAt: at.Add(-time.Second), At: at}}, nil))
	}
	scores, err := r.ChannelQualityScores(ctx, []int64{62}, "non-openai")
	require.NoError(t, err)
	require.Equal(t, 5, scores[62].Samples)
	require.Equal(t, "partial", scores[62].Status)
	require.NotNil(t, scores[62].Value)
	require.Equal(t, 100., *scores[62].Value)
	require.Nil(t, scores[62].Dimensions[3].Value, "missing duration excludes only the duration dimension when start evidence is trusted")
	// Repeated reports never replace an earlier turn start with a later retry.
	require.NoError(t, r.RecordTelemetry(ctx, []service.PassiveQualityFailure{{AccountID: 62, RequestKey: "plain-1", AttemptKey: "turn-1", StartedAt: at, At: at}}, nil))
	var started time.Time
	require.NoError(t, db.QueryRow(`SELECT started_at FROM passive_quality_cache_reports WHERE request_key='plain-1'`).Scan(&started))
	require.WithinDuration(t, at.Add(-time.Second), started, time.Microsecond)
}

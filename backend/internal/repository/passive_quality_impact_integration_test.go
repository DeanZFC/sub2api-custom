//go:build integration

package repository

import (
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestPassiveQualityCacheImpactRecordsFeesAndLongRequestExpiryIntegration(t *testing.T) {
	ctx, db, r := passiveDB(t)
	p := v2CachePolicy()
	p.CacheProtection.Quick.WindowSeconds = 900
	p.CacheProtection.ActionWindow.WindowSeconds = 900
	require.NoError(t, r.SavePolicy(ctx, &p))
	base := p.UpdatedAt.Add(time.Second)
	recordCacheRequest(t, ctx, db, r, 62, "long", "s", "p", base.Add(400*time.Second), 0)
	_, err := db.Exec(`UPDATE passive_quality_cache_reports SET cache_metadata=jsonb_set(cache_metadata,'{started_at}',to_jsonb($1::timestamptz)) WHERE request_key='long'`, base)
	require.NoError(t, err)
	_, err = db.Exec(`UPDATE usage_logs SET duration_ms=400000,input_cost=.1,cache_creation_cost=.2,cache_read_cost=.3,first_token_ms=70 WHERE request_id='long'`)
	require.NoError(t, err)
	recordCacheRequest(t, ctx, db, r, 62, "after-long", "s", "p", base.Add(401*time.Second), 0)
	_, err = db.Exec(`UPDATE usage_logs SET input_cost=.2,cache_creation_cost=.3,cache_read_cost=.4,first_token_ms=90 WHERE request_id='after-long'`)
	require.NoError(t, err)
	recordCacheRequest(t, ctx, db, r, 62, "nonstream", "other", "p", base.Add(401500*time.Millisecond), 0)
	_, err = db.Exec(`UPDATE usage_logs SET stream=false,input_cost=NULL,first_token_ms=90000 WHERE request_id='nonstream'`)
	require.NoError(t, err)
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer tx.Rollback()
	now := base.Add(402 * time.Second)
	samples, complete, err := passiveCacheSamples(ctx, tx, p, service.PassiveQualityState{AccountID: 62}, now)
	require.NoError(t, err)
	require.True(t, complete)
	require.Len(t, samples, 3)
	require.Equal(t, "after-long", samples[1].Key)
	require.Equal(t, "cold", samples[1].CacheOpportunity, "a long response cannot restart prefix retention at completion")
	require.Equal(t, "reuse_expired", samples[1].CacheReason)
	e := service.PassiveQualityCacheWindows(p.PassiveQualityConfig, samples, nil, now, complete)["model"].Action
	require.Equal(t, 3, e.TotalSamples)
	require.Equal(t, 2, e.InputCostSamples)
	require.InDelta(t, 1.5, *e.InputCost, 1e-12)
	require.Equal(t, 2, e.TTFTSamples)
	require.Equal(t, 90.0, *e.TTFTP90)
}

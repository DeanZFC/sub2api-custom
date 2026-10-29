//go:build integration

package repository

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestPassiveQualityTPSPopulationIntegration(t *testing.T) {
	ctx, db, _ := passiveDB(t)
	now := time.Now()
	for _, sample := range []struct {
		key              string
		output, duration int
		first            any
		extra            string
	}{
		{"fast", 100, 1100, 100, ""}, {"slow", 100, 9100, 100, ""},
		{"missing", 9000, 1000, nil, ""}, {"invalid", 9000, 100, 100, ""},
		{"nonstream", 9000, 1100, 100, "stream=false"},
		{"image", 9000, 1100, 100, "image_count=1"},
		{"audio", 9000, 1100, 100, "inbound_endpoint='/v1/audio/speech'"},
		{"compact", 9000, 1100, 100, "native_compaction_v2=true"},
		{"probe", 9000, 1100, 100, "request_type=4"},
	} {
		_, err := db.Exec(`INSERT INTO usage_logs(account_id,request_id,created_at,output_tokens,duration_ms,first_token_ms) VALUES(62,$1,$2,$3,$4,$5)`, sample.key, now, sample.output, sample.duration, sample.first)
		require.NoError(t, err)
		if sample.extra != "" {
			_, err = db.Exec(`UPDATE usage_logs SET `+sample.extra+` WHERE request_id=$1`, sample.key)
			require.NoError(t, err)
		}
	}
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer tx.Rollback()
	p := newPassivePolicy()
	e, err := passiveSamples(ctx, tx, p, service.PassiveQualityState{AccountID: 62}, now.Add(time.Second))
	require.NoError(t, err)
	require.Equal(t, 2, e.TPSSamples)
	require.NotNil(t, e.OutputTPS)
	require.Equal(t, 20.0, *e.OutputTPS)
	require.NoError(t, tx.Rollback())
}

func TestPassiveQualityTPSLimitAndRecoveryIntegration(t *testing.T) {
	ctx, db, r := passiveDB(t)
	p := newPassivePolicy()
	p.SchemaVersion = 2
	p.ErrorRate.Enabled = false
	p.TPS = service.PassiveQualityMetric{Enabled: true, Pause: 10, Recover: 15}
	p.TPSConcurrencyLimit = 2
	p.TPSPriorityPenalty = 20
	require.NoError(t, r.SavePolicy(ctx, &p))
	base := p.EvidenceSince().Add(20 * time.Second)
	seed := func(prefix string, at time.Time, output int) {
		for i := 0; i < 5; i++ {
			_, err := db.Exec(`INSERT INTO usage_logs(account_id,request_id,created_at,output_tokens,first_token_ms,duration_ms) VALUES(62,$1,$2,$3,50,1050)`, fmt.Sprintf("%s-%d", prefix, i), at.Add(-time.Duration(i)*time.Millisecond), output)
			require.NoError(t, err)
		}
	}
	seed("bad1", base, 5)
	_, err := r.advance(ctx, p.ID, 62, base, false)
	require.NoError(t, err)
	seed("bad2", base.Add(2*time.Second), 5)
	_, err = r.advance(ctx, p.ID, 62, base.Add(2*time.Second), false)
	require.NoError(t, err)
	states, err := r.ListStates(ctx, 62)
	require.NoError(t, err)
	require.Len(t, states, 1)
	require.NotNil(t, states[0].Speed)
	require.Equal(t, "limited", states[0].Speed.Phase)
	limit, err := r.PassiveQualityConcurrencyLimit(ctx, 62, "model")
	require.NoError(t, err)
	require.Equal(t, 2, limit)
	other, err := r.PassiveQualityConcurrencyLimit(ctx, 62, "other")
	require.NoError(t, err)
	require.Zero(t, other)
	var hold bool
	require.NoError(t, db.QueryRow(`SELECT EXISTS(SELECT 1 FROM jsonb_array_elements(extra->'passive_quality') h WHERE h->>'metric'='tps') FROM accounts WHERE id=62`).Scan(&hold))
	require.True(t, hold)
	for i := 0; i < 2; i++ {
		at := base.Add(time.Duration(40+i*2) * time.Second)
		seed(fmt.Sprintf("good%d", i), at, 20)
		_, err = r.advance(ctx, p.ID, 62, at, false)
		require.NoError(t, err)
	}
	states, err = r.ListStates(ctx, 62)
	require.NoError(t, err)
	replay := service.PassiveQualityState{AccountID: 62, Phase: "insufficient", ObserveAfter: p.EvidenceSince()}
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer tx.Rollback()
	for _, offset := range []int{0, 2, 40, 42} {
		_, _, _, err := replayPassiveMetrics(ctx, tx, p, &replay, base.Add(time.Duration(offset)*time.Second), base.Add(2*time.Minute))
		require.NoError(t, err)
	}
	liveJSON, err := json.Marshal(states[0].Speed)
	require.NoError(t, err)
	replayJSON, err := json.Marshal(replay.Speed)
	require.NoError(t, err)
	require.JSONEq(t, string(liveJSON), string(replayJSON), "TPS replay must match the live state and evidence boundaries")
	require.NoError(t, tx.Rollback())
	require.Equal(t, "healthy", states[0].Speed.Phase)
	limit, err = r.PassiveQualityConcurrencyLimit(ctx, 62, "model")
	require.NoError(t, err)
	require.Zero(t, limit)
}

//go:build integration

package repository

import (
	"fmt"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestGroupQualityContributorsCorrelateQualityTurnsIntegration(t *testing.T) {
	ctx, db, r := passiveDB(t)
	p := service.PassiveQualityPolicy{PassiveQualityConfig: service.PassiveQualityConfig{
		Purpose: "group_quality_target", GroupIDs: []int64{8}, Enabled: true, Mode: "enforce",
		CacheRate: service.PassiveQualityMetric{Enabled: true, Pause: 80},
	}}
	require.NoError(t, r.SavePolicy(ctx, &p))
	now := time.Now()
	at := now.Add(-time.Minute)
	lease := func(key, kind string, account int64, model string) {
		t.Helper()
		_, err := db.Exec(`INSERT INTO group_quality_traffic_leases(lease_id,policy_id,policy_version,account_id,group_id,model,request_key,validation,fallback,fallback_expires_at,reserved_input,created_at,released_at)
 VALUES($1,$2,$3,$4,8,$5,$6,$7,$8,$9,4096,$10,$11)`, fmt.Sprintf("lease-%d-%s-%s", account, model, key), p.ID, p.DecisionVersion(), account, model, key, kind != "normal", kind == "fallback", now.Add(time.Minute), at.Add(-time.Second), at)
		require.NoError(t, err)
	}
	usage := func(billing, turn string, account int64, reported bool) {
		t.Helper()
		_, err := db.Exec(`INSERT INTO usage_logs(account_id,group_id,request_id,model,created_at,input_tokens,cache_read_tokens,first_token_ms) VALUES($1,8,$2,'model',$3,100,0,1000)`, account, billing, at)
		require.NoError(t, err)
		if turn != "" {
			require.NoError(t, r.RecordTelemetry(ctx, []service.PassiveQualityFailure{{AccountID: account, GroupID: 8, Model: "model", RequestKey: billing, AttemptKey: turn, At: at, CacheReported: reported}}, nil))
		}
	}
	for _, kind := range []string{"normal", "validation", "fallback"} {
		turn := "turn-" + kind
		if kind == "fallback" {
			turn = "ws:" + turn
		}
		lease(turn, kind, 62, "model")
		usage("upstream-"+kind, turn, 62, kind != "validation")
	}
	// Failures use the quality turn directly. A partially billed retry of that
	// turn must count only as a failure, while another account may succeed.
	lease("ws:retry-turn", "fallback", 62, "model")
	usage("partial-upstream", "ws:retry-turn", 62, true)
	require.NoError(t, r.RecordTelemetry(ctx, []service.PassiveQualityFailure{{AccountID: 62, GroupID: 8, Model: "model", RequestKey: "ws:retry-turn", StartedAt: at.Add(-time.Second), At: at}}, nil))
	lease("ws:retry-turn", "normal", 63, "model")
	usage("retry-success-upstream", "ws:retry-turn", 63, true)
	// An unrelated concurrent request cannot inherit a nearby lease by time,
	// account or model. Historical usage without a bridge remains unattributed.
	usage("unrelated-upstream", "unrelated-turn", 62, true)
	usage("historical-billing", "", 62, false)
	s, err := r.groupQualityTargetSummary(ctx, p, now)
	require.NoError(t, err)
	require.EqualValues(t, 6, s.SuccessSamples)
	require.EqualValues(t, 1, s.FailureSamples)
	byKind := make(map[string]service.GroupQualityContributor)
	for _, c := range s.Contributors {
		byKind[fmt.Sprintf("%d/%s", c.AccountID, c.TrafficKind)] = c
	}
	require.Len(t, byKind, 5)
	require.EqualValues(t, 1, byKind["62/normal"].SuccessSamples)
	require.EqualValues(t, 1, byKind["62/validation"].SuccessSamples)
	require.EqualValues(t, 1, byKind["62/validation"].CacheUnknownSamples)
	require.EqualValues(t, 1, byKind["62/fallback"].SuccessSamples)
	require.EqualValues(t, 1, byKind["62/fallback"].FailureSamples)
	require.EqualValues(t, 1, byKind["63/normal"].SuccessSamples)
	require.EqualValues(t, 2, byKind["62/unattributed"].SuccessSamples)
}

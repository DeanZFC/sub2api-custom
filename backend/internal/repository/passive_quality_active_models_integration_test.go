//go:build integration

package repository

import (
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestActiveTargetDiscoversRareModelsAcrossWholeHourIntegration(t *testing.T) {
	ctx, db, r := passiveDB(t)
	p := service.PassiveQualityPolicy{PassiveQualityConfig: service.PassiveQualityConfig{Purpose: "group_quality_target", Name: "whole hour", Enabled: true, Mode: "enforce", GroupIDs: []int64{8}, CacheRate: service.PassiveQualityMetric{Enabled: true, Pause: 80, Recover: 85}}}
	require.NoError(t, r.SavePolicy(ctx, &p))
	now := time.Now().UTC()
	p.UpdatedAt = now.Add(-time.Hour)
	_, err := db.Exec(`UPDATE passive_quality_policies SET updated_at=$2 WHERE id=$1`, p.ID, p.UpdatedAt)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO usage_logs(account_id,group_id,request_id,model,requested_model,created_at) SELECT 62,8,'popular-'||i,'provider','popular',$1::timestamptz-i*interval '1 millisecond' FROM generate_series(1,10002) i`, now)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO usage_logs(account_id,group_id,request_id,model,requested_model,created_at) VALUES
	 (62,8,'rare','provider-rare','rare',$1::timestamptz-interval '30 minutes'),
	 (62,8,'expired','expired','expired',$1::timestamptz-interval '61 minutes'),
	 (62,9,'other-group','other','other-group',$1)`, now)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO passive_quality_failures(account_id,group_id,request_key,model,created_at,started_at) SELECT 62,8,'failure-'||i,'popular-failure',$1::timestamptz-i*interval '1 millisecond',$1::timestamptz-i*interval '1 millisecond' FROM generate_series(1,10002) i`, now)
	require.NoError(t, err)
	require.NoError(t, r.RecordFailures(ctx, []service.PassiveQualityFailure{{AccountID: 62, GroupID: 8, RequestKey: "rare-failure", Model: "rare-failure", At: now.Add(-30 * time.Minute), StartedAt: now.Add(-30 * time.Minute)}}, nil))
	targets, err := r.modelTargets(ctx, p, now)
	require.NoError(t, err)
	models := make([]string, 0, len(targets))
	for _, target := range targets {
		require.EqualValues(t, 62, target.account, "no guessed model for an unused account")
		models = append(models, target.model)
	}
	require.ElementsMatch(t, []string{"popular", "rare", "popular-failure", "rare-failure"}, models)
}

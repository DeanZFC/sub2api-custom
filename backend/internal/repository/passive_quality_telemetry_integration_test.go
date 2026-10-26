//go:build integration

package repository

import (
	"fmt"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestPassiveQualityRenamePreservesStateAndDecisionIdentityIntegration(t *testing.T) {
	for _, phase := range []string{"paused", "probing", "trial"} {
		t.Run(phase, func(t *testing.T) {
			ctx, db, r := passiveDB(t)
			p := newPassivePolicy()
			require.NoError(t, r.SavePolicy(ctx, &p))
			old := p
			now := time.Now()
			state := service.PassiveQualityState{PolicyID: p.ID, PolicyName: p.Name, AccountID: 62, Model: p.Model, Scope: p.Scope, Mode: p.Mode, Version: p.Version, Generation: 9, Phase: phase, Hits: 1, LastEvidenceAt: now.Add(-time.Second), ObserveAfter: p.EvidenceSince(), NextAt: now.Add(time.Minute), TrialStartedAt: now.Add(-time.Second), TrialEndsAt: now.Add(time.Minute), UpdatedAt: now}
			tx, err := db.BeginTx(ctx, nil)
			require.NoError(t, err)
			require.NoError(t, savePassiveState(ctx, tx, state))
			require.NoError(t, reconcilePassiveAccount(ctx, tx, 62))
			require.NoError(t, tx.Commit())
			if phase == "trial" {
				ok, err := acquirePassiveForTest(r, ctx, 62, p.Model, "preserved-lease")
				require.NoError(t, err)
				require.True(t, ok)
			}
			before, err := r.ListStates(ctx, 62)
			require.NoError(t, err)
			require.Len(t, before, 1)
			p.Name = "renamed"
			require.NoError(t, r.SavePolicy(ctx, &p))
			require.Equal(t, old.Version+1, p.Version)
			require.Equal(t, old.DecisionVersion(), p.DecisionVersion())
			require.True(t, old.EvidenceSince().Equal(p.EvidenceSince()))
			after, err := r.ListStates(ctx, 62)
			require.NoError(t, err)
			require.Len(t, after, 1)
			expected := before[0]
			expected.PolicyName = p.Name
			require.Equal(t, expected, after[0])
			version, updated := p.Version, p.UpdatedAt
			p.Version = old.Version
			require.NoError(t, r.SavePolicy(ctx, &p))
			require.Equal(t, version, p.Version)
			require.Equal(t, updated, p.UpdatedAt)
			if phase == "probing" {
				require.NoError(t, r.FinishProbe(ctx, state, true, now))
				after, err = r.ListStates(ctx, 62)
				require.NoError(t, err)
				require.Equal(t, "trial", after[0].Phase)
			}
			if phase == "trial" {
				require.NoError(t, r.ReleasePassiveQuality(ctx, "preserved-lease", false))
				after, err = r.ListStates(ctx, 62)
				require.NoError(t, err)
				require.Zero(t, after[0].Inflight)
				require.Equal(t, 1, after[0].Used)
			}
			p.ErrorRate.Pause = 25
			require.NoError(t, r.SavePolicy(ctx, &p))
			require.Equal(t, p.Version, p.DecisionVersion())
			require.Nil(t, p.EvidenceStartedAt)
			after, err = r.ListStates(ctx, 62)
			require.NoError(t, err)
			require.Empty(t, after)
		})
	}
}

func TestPassiveQualityScopedTelemetryGapsIntegration(t *testing.T) {
	ctx, db, r := passiveDB(t)
	now := time.Now()
	from := now.Add(-time.Minute)
	require.NoError(t, r.RecordTelemetry(ctx, nil, []service.PassiveQualityTelemetryGap{{From: from, To: now, AccountID: 62, GroupID: 8, Model: "a", Kind: "cache", DroppedRecords: 3}}))
	for _, tc := range []struct {
		account, group int64
		model, kind    string
		complete       bool
	}{
		{62, 8, "a", "all", false}, {62, 8, "a", "cache", false}, {62, 8, "a", "failure", true},
		{63, 8, "a", "all", true}, {62, 9, "a", "all", true}, {62, 8, "b", "all", true}, {0, 8, "a", "all", false},
	} {
		complete, err := passiveTelemetryComplete(ctx, db, from.Add(time.Second), now.Add(time.Second), tc.account, tc.model, tc.group, tc.kind)
		require.NoError(t, err)
		require.Equal(t, tc.complete, complete, fmt.Sprintf("%+v", tc))
	}
	complete, err := passiveTelemetryComplete(ctx, db, now.Add(time.Second), now.Add(time.Minute), 62, "a", 8, "all")
	require.NoError(t, err)
	require.True(t, complete)
	// Legacy binaries keep writing global markers while rolling out the migration.
	_, err = db.Exec(`INSERT INTO passive_quality_telemetry_gaps VALUES($1) ON CONFLICT DO NOTHING`, now)
	require.NoError(t, err)
	complete, err = passiveTelemetryComplete(ctx, db, from, now.Add(time.Second), 63, "b", 9, "cache")
	require.NoError(t, err)
	require.False(t, complete)
}

func TestPassiveQualityBatchDeduplicatesCacheReportsIntegration(t *testing.T) {
	ctx, db, r := passiveDB(t)
	now := time.Now()
	items := []service.PassiveQualityFailure{
		{AccountID: 62, RequestKey: "cache", At: now, AttemptKey: "attempt"},
		{AccountID: 62, RequestKey: "cache", At: now, CacheReported: true},
		{AccountID: 62, RequestKey: "failed", At: now, StartedAt: now, GroupID: 8, Model: "model"},
		{AccountID: 62, RequestKey: "failed", At: now, StartedAt: now, GroupID: 8, Model: "model"},
		{AccountID: 999, RequestKey: "deleted-account", At: now, StartedAt: now},
	}
	require.NoError(t, r.RecordTelemetry(ctx, items, nil))
	require.NoError(t, r.RecordTelemetry(ctx, items, nil))
	var count int
	var reported bool
	var key string
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM passive_quality_failures`).Scan(&count))
	require.Equal(t, 1, count)
	require.NoError(t, db.QueryRow(`SELECT cache_reported,attempt_key FROM passive_quality_cache_reports WHERE account_id=62 AND request_key='cache'`).Scan(&reported, &key))
	require.True(t, reported)
	require.Equal(t, "attempt", key)
	require.NoError(t, r.PrunePassiveQuality(ctx))
}

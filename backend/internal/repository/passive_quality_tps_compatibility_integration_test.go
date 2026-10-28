//go:build integration

package repository

import (
	"fmt"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestPassiveQualityTPSOldPolicySavePreservesProtectionIntegration(t *testing.T) {
	ctx, db, repo := passiveDB(t)
	p := newPassivePolicy()
	p.SchemaVersion = 2
	require.NoError(t, repo.SavePolicy(ctx, &p))
	// Persist exactly the pre-upgrade shape: none of the new TPS fields exist.
	_, err := db.Exec(`UPDATE passive_quality_policies SET config=config-'tps'-'tps_action'-'tps_concurrency_limit'-'tps_priority_penalty' WHERE id=$1`, p.ID)
	require.NoError(t, err)
	policies, err := repo.ListPolicies(ctx)
	require.NoError(t, err)
	require.Len(t, policies, 1)
	p = policies[0]
	require.Empty(t, p.TPSAction)
	base := p.EvidenceSince().Add(time.Second)
	for round, count := range []int{5, 2} {
		at := base.Add(time.Duration(round) * time.Second)
		var failures []service.PassiveQualityFailure
		for i := 0; i < count; i++ {
			failures = append(failures, service.PassiveQualityFailure{AccountID: 62, Model: "model", RequestKey: fmt.Sprintf("old-%d-%d", round, i), StartedAt: at, At: at})
		}
		require.NoError(t, repo.RecordFailures(ctx, failures, nil))
		_, err = repo.advance(ctx, p.ID, 62, at, false)
		require.NoError(t, err)
	}
	before, err := repo.ListStates(ctx, 62)
	require.NoError(t, err)
	require.Len(t, before, 1)
	require.Equal(t, "paused", before[0].Phase)
	var holdBefore string
	require.NoError(t, db.QueryRow(`SELECT extra->'passive_quality' FROM accounts WHERE id=62`).Scan(&holdBefore))
	originalVersion, evidenceSince := p.Version, p.EvidenceSince()
	for _, rename := range []bool{false, true} {
		// A current editor supplies disabled TPS defaults even though the old
		// policy did not contain them. They must not reset decision identity.
		p.TPS = service.PassiveQualityMetric{Pause: 10, Recover: 15}
		if rename {
			p.Name = "renamed old policy"
		}
		require.NoError(t, repo.SavePolicy(ctx, &p))
		if rename {
			require.Equal(t, originalVersion+1, p.Version)
		} else {
			require.Equal(t, originalVersion, p.Version)
		}
		require.Equal(t, evidenceSince, p.EvidenceSince())
		after, err := repo.ListStates(ctx, 62)
		require.NoError(t, err)
		require.Len(t, after, 1)
		require.Equal(t, "paused", after[0].Phase)
		require.Equal(t, before[0].Generation, after[0].Generation)
		require.Equal(t, before[0].ObserveAfter, after[0].ObserveAfter)
		require.Equal(t, before[0].LastEvidenceAt, after[0].LastEvidenceAt)
		require.Equal(t, originalVersion, p.DecisionVersion())
		allowed, err := acquirePassiveForTest(repo, ctx, 62, "model", fmt.Sprintf("after-save-%t", rename))
		require.NoError(t, err)
		require.False(t, allowed)
		var changes int
		require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM passive_quality_events WHERE policy_id=$1 AND kind='policy_changed'`, p.ID).Scan(&changes))
		require.Zero(t, changes)
		var holdAfter string
		require.NoError(t, db.QueryRow(`SELECT extra->'passive_quality' FROM accounts WHERE id=62`).Scan(&holdAfter))
		if !rename {
			require.JSONEq(t, holdBefore, holdAfter)
		} else {
			require.Contains(t, holdAfter, "renamed old policy")
		}
	}
}

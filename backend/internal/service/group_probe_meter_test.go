package service

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type groupProbeTestBudget struct {
	QualityOperationsRepository
	admissions int
	settled    []QualityProbeOutcome
}

func (b *groupProbeTestBudget) ReserveQualityProbe(context.Context, int64, int64, bool, string) (*QualityProbeReservation, error) {
	b.admissions++
	if b.admissions == 3 {
		return nil, &QualityDeferredError{Reason: "daily budget", Until: time.Now().Add(time.Hour)}
	}
	return &QualityProbeReservation{ID: int64(b.admissions)}, nil
}
func (b *groupProbeTestBudget) SettleQualityProbe(_ context.Context, _ *QualityProbeReservation, outcome QualityProbeOutcome) error {
	b.settled = append(b.settled, outcome)
	return nil
}

func TestGroupProbeBudgetFencesRetriesAndPreservesOwnerCancellation(t *testing.T) {
	b := &groupProbeTestBudget{}
	meter := NewGroupProbeMeter(b, nil, nil, 9, "text-model")
	owner, stop := context.WithCancel(context.Background())
	ctx, allowed := meter.Begin(owner, &Account{ID: 7}, 250)
	require.True(t, allowed)
	req, err := http.NewRequestWithContext(context.WithoutCancel(ctx), http.MethodPost, "https://upstream.example", nil)
	require.NoError(t, err)
	for range 2 {
		release, err := PrepareGroupProbeTransport(req, func() {})
		require.NoError(t, err)
		release()
	}
	release, err := PrepareGroupProbeTransport(req, func() {})
	require.Error(t, err)
	release()
	require.Equal(t, 3, b.admissions)
	require.Len(t, b.settled, 2)
	require.Nil(t, b.settled[0].Tokens)
	require.Equal(t, "budget_exhausted", meter.Result.Deferred.Reason)
	_, allowed = meter.Begin(owner, &Account{ID: 8}, 250)
	require.False(t, allowed)
	require.Equal(t, 3, b.admissions)

	b = &groupProbeTestBudget{}
	meter = NewGroupProbeMeter(b, nil, nil, 10, "other")
	ctx, _ = meter.Begin(owner, &Account{ID: 8}, 1)
	detached, cancel := context.WithCancel(context.WithoutCancel(ctx))
	release, err = PrepareGroupProbeTransport(req.WithContext(detached), cancel)
	require.NoError(t, err)
	defer release()
	stop()
	select {
	case <-detached.Done():
	case <-time.After(time.Second):
		t.Fatal("detached probe transport outlived owner")
	}
}

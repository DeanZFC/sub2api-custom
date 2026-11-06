package service

import (
	"context"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestGroupAntiDistillationAdmissionRechecksWebSocketTurns(t *testing.T) {
	paused, checks := false, 0
	ctx := WithGroupAntiDistillationAdmission(context.Background(), func(context.Context) (time.Duration, error) {
		checks++
		if paused {
			return time.Minute, nil
		}
		return 0, nil
	})
	require.NoError(t, CheckGroupAntiDistillationAdmission(ctx))
	paused = true
	err := CheckGroupAntiDistillationAdmission(ctx)
	require.Equal(t, 429, infraerrors.Code(err))
	require.Equal(t, 2, checks)
}
func TestGroupAntiDistillationRevalidationFailsOpen(t *testing.T) {
	ctx := WithGroupAntiDistillationAdmission(context.Background(), func(context.Context) (time.Duration, error) { return 0, context.DeadlineExceeded })
	require.NoError(t, CheckGroupAntiDistillationAdmission(ctx))
	require.NoError(t, CheckGroupAntiDistillationAdmission(context.Background()))
}

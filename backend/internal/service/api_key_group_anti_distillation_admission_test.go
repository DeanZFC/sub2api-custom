package service

import (
	"context"
	"errors"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestGroupAntiDistillationRechecksAfterAdmissionWait(t *testing.T) {
	paused := false
	calls := 0
	ctx := WithGroupAntiDistillationAdmission(context.Background(), func(context.Context) (time.Duration, error) {
		calls++
		if paused {
			return time.Minute, nil
		}
		return 0, nil
	})
	require.NoError(t, CheckGroupAntiDistillationAdmission(ctx))
	paused = true
	require.ErrorContains(t, CheckGroupAntiDistillationAdmission(ctx), "零缓存保护")
	require.Equal(t, 2, calls)
	require.NoError(t, CheckGroupAntiDistillationAdmission(WithGroupAntiDistillationAdmission(context.Background(), func(context.Context) (time.Duration, error) { return 0, errors.New("redis down") })))
}

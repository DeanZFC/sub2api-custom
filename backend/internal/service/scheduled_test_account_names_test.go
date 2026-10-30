package service

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

type scheduledTestAdminNamesStub struct {
	ScheduledTestResultRepository
	ids   []int64
	calls int
	err   error
}

func (r *scheduledTestAdminNamesStub) GetAdminResultAccountNames(_ context.Context, ids []int64) (map[int64]string, error) {
	r.ids = append([]int64(nil), ids...)
	r.calls++
	return map[int64]string{7: "Current name", 9: "Hidden name"}, r.err
}

func TestScheduledTestAdminAccountNamesUseOneDistinctCurrentAccountLookup(t *testing.T) {
	a, hidden, missing, invalid := int64(7), int64(9), int64(88), int64(0)
	rows := []*ScheduledTestResult{
		nil, {AccountID: &a, AccountName: "old name", TargetMode: "all_accounts"},
		{AccountID: &a, TargetMode: "account"}, {AccountID: &hidden, TargetMode: "group", AccountName: "old hidden name"},
		{AccountID: &missing}, {AccountID: &invalid, AccountName: "invalid name"}, {AccountName: "no account"},
	}
	repo := &scheduledTestAdminNamesStub{}
	require.NoError(t, NewScheduledTestService(nil, repo).PopulateAdminResultAccountNames(context.Background(), rows))
	require.Equal(t, []int64{7, 88}, repo.ids)
	require.Equal(t, 1, repo.calls)
	require.Equal(t, "Current name", rows[1].AccountName)
	require.Equal(t, "Current name", rows[2].AccountName)
	for _, row := range rows[3:] {
		require.Empty(t, row.AccountName)
	}
}

func TestScheduledTestAdminAccountNamesSkipGroupOnlyLookup(t *testing.T) {
	repo := &scheduledTestAdminNamesStub{}
	accountID := int64(9)
	require.NoError(t, NewScheduledTestService(nil, repo).PopulateAdminResultAccountNames(context.Background(), []*ScheduledTestResult{nil, {AccountID: &accountID, TargetMode: "group"}, {}}))
	require.Zero(t, repo.calls)
}

func TestScheduledTestAdminAccountNamesLookupFailures(t *testing.T) {
	accountID := int64(7)
	rows := []*ScheduledTestResult{{AccountID: &accountID}}
	require.Error(t, NewScheduledTestService(nil, scheduledTestReadResultsStub{}).PopulateAdminResultAccountNames(context.Background(), rows))
	expected := errors.New("lookup failed")
	repo := &scheduledTestAdminNamesStub{err: expected}
	require.ErrorIs(t, NewScheduledTestService(nil, repo).PopulateAdminResultAccountNames(context.Background(), rows), expected)
}

package qqbot

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestQQReportFilterCalendarAndLimits(t *testing.T) {
	// The UTC day is still the 17th while the report's day is the 18th.
	now := time.Date(2026, 11, 17, 16, 0, 0, 0, time.UTC)
	f, err := (ReportFilter{}).Normalize(now)
	require.NoError(t, err)
	require.Equal(t, "2026-11-12", f.StartDate)
	require.Equal(t, "2026-11-18", f.EndDate)
	require.Equal(t, 1, f.Page)
	require.Equal(t, 20, f.PageSize)
	f, err = (ReportFilter{EndDate: "2024-03-01"}).Normalize(now)
	require.NoError(t, err)
	require.Equal(t, "2024-02-24", f.StartDate)
	_, err = (ReportFilter{StartDate: "2024-01-01", EndDate: "2024-12-31"}).Normalize(now)
	require.NoError(t, err, "366 inclusive calendar days are allowed")
	for _, f := range []ReportFilter{
		{StartDate: "2024-01-01", EndDate: "2025-01-01"},
		{StartDate: "2026-11-19", EndDate: "2026-11-18"},
		{StartDate: "2026-02-29"}, {EndDate: "2026-11-18T00:00:00Z"},
		{StartDate: "0000-01-01", EndDate: "0000-01-01"},
		{Kind: "any"}, {RewardType: "money"}, {Page: -1}, {Page: 1000001}, {PageSize: 101},
	} {
		_, err := f.Normalize(now)
		require.ErrorIs(t, err, ErrReportFilter, "%+v", f)
	}
}

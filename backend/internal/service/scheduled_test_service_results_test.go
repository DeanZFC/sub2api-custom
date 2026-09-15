package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

type scheduledTestReadResultsStub struct {
	ScheduledTestResultRepository
	rows []*ScheduledTestResult
}

func (r scheduledTestReadResultsStub) ListByPlanID(context.Context, int64, int) ([]*ScheduledTestResult, error) {
	return r.rows, nil
}

func (r scheduledTestReadResultsStub) ListVisible(context.Context, int64, int) ([]*ScheduledTestResult, error) {
	return r.rows, nil
}

func TestScheduledTestResultJSONPreservesZeroSortOrder(t *testing.T) {
	data, err := json.Marshal(ScheduledTestResult{TestOrder: 0})
	require.NoError(t, err)
	var fields map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(data, &fields))
	require.Contains(t, fields, "test_order", "zero is the first display position, not an absent order")
	require.JSONEq(t, "0", string(fields["test_order"]))
}

func TestScheduledTestListRepairsLegacyNumericDisplayFromResponse(t *testing.T) {
	for _, view := range []string{"admin", "user"} {
		t.Run(view, func(t *testing.T) {
			oldValue, storedOnly := 1.0, 42.0
			body := "最少取出 **29个**。\n\n1. 第一种情况最多取出 7 个。\n2. 第二种情况最多取出 9 个。\n保证至少 1 个符合要求。"
			rows := []*ScheduledTestResult{
				{ID: 1, Status: "success", OutputKind: "number", OutputNumeric: &oldValue, ResponseText: body},
				{ID: 2, Status: "success", OutputKind: "number", OutputNumeric: &oldValue, ResponseText: "1. Inspect the input.\n2. Compare the values."},
				{ID: 3, Status: "success", OutputKind: "number", OutputNumeric: &storedOnly},
				{ID: 4, Status: "success", OutputKind: "text", ResponseText: body},
			}
			svc := NewScheduledTestService(nil, scheduledTestReadResultsStub{rows: rows})
			list := svc.ListResults
			if view == "user" {
				list = svc.ListVisibleResults
			}
			got, err := list(context.Background(), 1, 10)
			require.NoError(t, err)
			require.Len(t, got, 4)
			require.NotNil(t, got[0].OutputNumeric)
			require.Equal(t, 29.0, *got[0].OutputNumeric)
			require.Equal(t, body, got[0].ResponseText)
			require.Equal(t, "success", got[0].Status)
			require.Nil(t, got[1].OutputNumeric, "ambiguous text should be displayed without a guessed number")
			require.Equal(t, 42.0, *got[2].OutputNumeric, "preserve legacy values without source text")
			require.Nil(t, got[3].OutputNumeric, "text results must not be converted to numeric results")
		})
	}
}

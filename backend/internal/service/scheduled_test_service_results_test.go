package service

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
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

func (r scheduledTestReadResultsStub) ListVisibleWithRecentResults(context.Context, int64, int) ([]*ScheduledTestResult, error) {
	return r.rows, nil
}

func (r scheduledTestReadResultsStub) ListVisibleHistory(context.Context, int64, int64, int64, int) ([]*ScheduledTestResult, error) {
	return r.rows, nil
}

func TestScheduledTestPublicResultsHideUpstreamErrors(t *testing.T) {
	for _, view := range []string{"list", "history", "recent"} {
		t.Run(view, func(t *testing.T) {
			row := &ScheduledTestResult{ID: 1, Status: "failed", OutputKind: "text", ResponseText: "partial output", AccountName: "private account name", ErrorMessage: "private upstream diagnostic"}
			svc := NewScheduledTestService(nil, scheduledTestReadResultsStub{rows: []*ScheduledTestResult{row, nil}})
			var rows []*ScheduledTestResult
			if view != "history" {
				var err error
				list := svc.ListVisibleResults
				if view == "recent" {
					list = svc.ListVisibleResultsWithRecentResults
				}
				rows, err = list(context.Background(), 1, 4)
				require.NoError(t, err)
			} else {
				page, err := svc.ListVisibleResultHistory(context.Background(), 1, 1, 0, 20)
				require.NoError(t, err)
				rows = page.Items
			}
			require.Len(t, rows, 2)
			require.Equal(t, "failed", rows[0].Status)
			require.Equal(t, "partial output", rows[0].ResponseText)
			require.Empty(t, rows[0].ErrorMessage)
			require.Empty(t, rows[0].AccountName, "ordinary result views must strip names even if a repository supplies them")
			require.Nil(t, rows[1])
		})
	}
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
	for _, view := range []string{"admin", "user", "recent"} {
		t.Run(view, func(t *testing.T) {
			oldValue, storedOnly := 1.0, 42.0
			body := "最少取出 **29个**。\n\n1. 第一种情况最多取出 7 个。\n2. 第二种情况最多取出 9 个。\n保证至少 1 个符合要求。"
			rows := []*ScheduledTestResult{
				{ID: 1, Status: "success", OutputKind: "number", OutputNumeric: &oldValue, ResponseText: body},
				{ID: 2, Status: "success", OutputKind: "number", OutputNumeric: &oldValue, ResponseText: "1. Inspect the input.\n2. Compare the values."},
				{ID: 3, Status: "success", OutputKind: "number", OutputNumeric: &storedOnly},
				{ID: 4, Status: "success", OutputKind: "text", ResponseText: body},
				{ID: 5, Status: "success", OutputKind: "number", OutputNumeric: protectionFloat(8), ResponseText: scheduledTestCandyScreenshotResponse},
			}
			svc := NewScheduledTestService(nil, scheduledTestReadResultsStub{rows: rows})
			list := svc.ListResults
			switch view {
			case "user":
				list = svc.ListVisibleResults
			case "recent":
				list = svc.ListVisibleResultsWithRecentResults
			}
			got, err := list(context.Background(), 1, 10)
			require.NoError(t, err)
			require.Len(t, got, 5)
			require.NotNil(t, got[0].OutputNumeric)
			require.Equal(t, 29.0, *got[0].OutputNumeric)
			require.Equal(t, body, got[0].ResponseText)
			require.Equal(t, "success", got[0].Status)
			require.Nil(t, got[1].OutputNumeric, "ambiguous text should be displayed without a guessed number")
			require.Equal(t, 42.0, *got[2].OutputNumeric, "preserve legacy values without source text")
			require.Nil(t, got[3].OutputNumeric, "text results must not be converted to numeric results")
			require.Equal(t, 21.0, *got[4].OutputNumeric, "repair the screenshot's stored 8 from its original conclusion")
			require.Equal(t, scheduledTestCandyScreenshotResponse, got[4].ResponseText)
		})
	}
}

func TestScheduledTestListRepairsLegacyHTMLDisplay(t *testing.T) {
	want := "<!DOCTYPE html>\n<html lang=\"zh-CN\">\n<body>\n<h1>鹈鹕动画</h1>\n</body>\n</html>"
	var encoded bytes.Buffer
	encoder := json.NewEncoder(&encoded)
	encoder.SetEscapeHTML(false)
	err := encoder.Encode(want)
	require.NoError(t, err)
	escaped := strings.TrimSuffix(strings.TrimPrefix(encoded.String(), `"`), "\"\n")
	require.True(t, strings.HasPrefix(escaped, `<!DOCTYPE html>\n`))
	var command bytes.Buffer
	commandEncoder := json.NewEncoder(&command)
	commandEncoder.SetEscapeHTML(false)
	err = commandEncoder.Encode(map[string]string{"cmd": "cat > animation.html <<'EOF'\n" + want + "\nEOF"})
	require.NoError(t, err)
	transcript := "Creating the animation. to=terminal.exec code:\n" + command.String() + "The file is ready."

	for _, view := range []string{"admin", "user", "recent"} {
		t.Run(view, func(t *testing.T) {
			rows := []*ScheduledTestResult{
				{ID: 1, Status: "success", OutputKind: "html", ResponseText: transcript, OutputHTML: escaped},
				{ID: 2, Status: "success", OutputKind: "html", ResponseText: "Original output unavailable.", OutputHTML: escaped},
				{ID: 3, Status: "success", OutputKind: "html", OutputHTML: escaped},
				{ID: 4, Status: "failed", OutputKind: " HTML ", ResponseText: transcript, OutputHTML: "<p>old output</p>", ErrorMessage: "original failure"},
				{ID: 5, Status: "success", OutputKind: "html", ResponseText: "not HTML", OutputHTML: "legacy partial output"},
				{ID: 6, Status: "success", OutputKind: "text", ResponseText: transcript, OutputHTML: escaped},
				{ID: 7, Status: "success", OutputKind: "html", ResponseText: want, OutputHTML: "<p>stale output</p>"},
				nil,
			}
			svc := NewScheduledTestService(nil, scheduledTestReadResultsStub{rows: rows})
			list := svc.ListResults
			switch view {
			case "user":
				list = svc.ListVisibleResults
			case "recent":
				list = svc.ListVisibleResultsWithRecentResults
			}
			got, err := list(context.Background(), 1, 10)
			require.NoError(t, err)
			require.Len(t, got, len(rows))
			for _, i := range []int{0, 1, 2, 3, 6} {
				require.Equal(t, want, got[i].OutputHTML, "row %d should display normalized HTML", i)
			}
			require.Equal(t, transcript, got[0].ResponseText, "retain the original transcript for diagnosis")
			require.Equal(t, "success", got[0].Status)
			require.Equal(t, "Original output unavailable.", got[1].ResponseText)
			require.Empty(t, got[2].ResponseText)
			require.Equal(t, "failed", got[3].Status, "read-time normalization must not change failure visibility")
			if view == "admin" {
				require.Equal(t, "original failure", got[3].ErrorMessage)
			} else {
				require.Empty(t, got[3].ErrorMessage)
			}
			require.Equal(t, transcript, got[3].ResponseText)
			require.Equal(t, "legacy partial output", got[4].OutputHTML, "preserve old output when neither source can be parsed")
			require.Equal(t, escaped, got[5].OutputHTML, "text results must not be interpreted as HTML")
			require.Equal(t, transcript, got[5].ResponseText)
			require.Equal(t, want, got[6].ResponseText)
			require.Nil(t, got[7])
		})
	}
}

func TestScheduledTestPublicQualityVerdictIsNotTransportStatus(t *testing.T) {
	rows := []*ScheduledTestResult{
		{Status: "success", QualityVerdict: "pass", ProtectionDecision: &ScheduledTestProtectionDecision{Reason: "private"}},
		{Status: "success", QualityVerdict: "fail"},
		{Status: "success"},
		{Status: "failed", QualityVerdict: "pass"},
		{Status: "success", QualityVerdict: "private-value"},
	}
	normalizePublicTestResults(rows)
	require.Equal(t, "pass", rows[0].QualityVerdict)
	require.Nil(t, rows[0].ProtectionDecision)
	require.Equal(t, "fail", rows[1].QualityVerdict)
	require.Empty(t, rows[2].QualityVerdict)
	require.Equal(t, "fail", rows[3].QualityVerdict)
	require.Empty(t, rows[4].QualityVerdict)
}

func TestScheduledTestPublicQualityVerdictWaitsForCompletedDecision(t *testing.T) {
	row := &ScheduledTestResult{Status: "success", QualityVerdict: "pass", ProtectionDecision: &ScheduledTestProtectionDecision{Status: "error", Verdict: "pass", Reason: "private database diagnostic"}}
	normalizePublicTestResults([]*ScheduledTestResult{row})
	require.Equal(t, "pending", row.QualityVerdict)
	require.Nil(t, row.ProtectionDecision)
}

func TestScheduledTestPublicSkippedMarkerPreservesPrivacy(t *testing.T) {
	for _, row := range []*ScheduledTestResult{
		{Status: "failed", ProtectionDecision: &ScheduledTestProtectionDecision{Status: "skipped", Reason: "private skip diagnostic"}},
		{Status: "failed", ExecutionInfo: &ScheduledTestExecutionInfo{FailureKind: "quality_deferred"}, ErrorMessage: "private skip diagnostic"},
		{Status: "failed", ErrorMessage: "检测跳过：private skip diagnostic"},
	} {
		normalizePublicTestResults([]*ScheduledTestResult{row})
		require.True(t, row.ExecutionSkipped)
		require.Empty(t, row.ErrorMessage)
		require.Nil(t, row.ProtectionDecision)
		encoded, err := json.Marshal(row)
		require.NoError(t, err)
		require.Contains(t, string(encoded), `"execution_skipped":true`)
		require.NotContains(t, string(encoded), "private skip diagnostic")
	}
}

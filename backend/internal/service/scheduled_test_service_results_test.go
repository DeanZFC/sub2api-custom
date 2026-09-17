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

	for _, view := range []string{"admin", "user"} {
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
			if view == "user" {
				list = svc.ListVisibleResults
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
			require.Equal(t, "original failure", got[3].ErrorMessage)
			require.Equal(t, transcript, got[3].ResponseText)
			require.Equal(t, "legacy partial output", got[4].OutputHTML, "preserve old output when neither source can be parsed")
			require.Equal(t, escaped, got[5].OutputHTML, "text results must not be interpreted as HTML")
			require.Equal(t, transcript, got[5].ResponseText)
			require.Equal(t, want, got[6].ResponseText)
			require.Nil(t, got[7])
		})
	}
}

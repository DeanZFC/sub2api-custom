package service

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestScheduledTestUnsupportedOutputDoesNotExposeStoredPayload(t *testing.T) {
	value := 99.0
	rows := []*ScheduledTestResult{
		{OutputKind: " statistics ", ResponseText: "secret-payload", OutputHTML: "secret-html", OutputNumeric: &value, ReasoningEffort: "high"},
		{OutputKind: "text", ResponseText: "active-output"},
	}
	normalizeStoredTestResults(rows)
	raw, err := json.Marshal(rows)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "secret-")
	require.NotContains(t, string(raw), "output_statistics")
	require.Empty(t, rows[0].ReasoningEffort)
	require.Nil(t, rows[0].OutputNumeric)
	require.Equal(t, "active-output", rows[1].ResponseText)
}

package modelfingerprint

import (
	"os"
	"slices"
	"strings"
	"testing"
)

func TestModelFingerprintBankAndUnknown(t *testing.T) {
	// MIT ModelTrace pinned enrollment row gpt-6.2-astra__query-25__high.
	raw, err := os.ReadFile("testdata/astra-high.txt")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ model, effort, answer, want string }{
		{"gpt-6.2-astra", "high", string(raw), "pass"},
		{"gpt-6.2-sol", "high", string(raw), "fail"},
		{"unrecognized", "high", string(raw), "unknown"},
		{"gpt-6.2-astra", "ultra", string(raw), "unknown"},
		{"gpt-6.2-astra", "high", "I am GPT-6.2-astra", "unknown"},
		{"gpt-6.2-astra", "high", strings.Repeat("42 ", 310), "unknown"},
	} {
		if got, _ := Evaluate(tc.model, tc.effort, tc.answer); got != tc.want {
			t.Errorf("%s/%s = %s, want %s", tc.model, tc.effort, got, tc.want)
		}
	}
	if models := Models("high"); !slices.IsSorted(models) || !slices.Contains(models, "gpt-6.2-astra") {
		t.Fatalf("unexpected high model list: %v", models)
	}
}

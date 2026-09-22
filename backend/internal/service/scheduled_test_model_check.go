package service

import (
	"encoding/json"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const scheduledTestModelCheckPrompt = "Please reply with OK."

// This checks the upstream's declared model, not the identity of its weights.
// Evidence comes exclusively from the response envelope, never generated text.
type ScheduledTestModelCheck struct {
	RequestedModel string   `json:"requested_model"`
	UpstreamModel  string   `json:"upstream_model"`
	ReturnedModels []string `json:"returned_models"`
	MatchMode      string   `json:"match_mode"`
	Verdict        string   `json:"verdict"`
	Reason         string   `json:"reason"`
}

func validTestModelIdentifier(value string) bool {
	return len(value) <= 256 && utf8.ValidString(value) && !strings.ContainsRune(value, utf8.RuneError) && !strings.ContainsFunc(value, unicode.IsControl)
}

func scheduledTestModelsMatch(expected, returned, mode string) bool {
	if expected == returned {
		return true
	}
	if mode != "snapshot" || !strings.HasPrefix(returned, expected+"-") {
		return false
	}
	date := strings.TrimPrefix(returned, expected+"-")
	parsed, err := time.Parse("2006-01-02", date)
	return err == nil && parsed.Format("2006-01-02") == date
}

func buildScheduledTestModelCheck(requested, upstream string, returned []string, mode string, invalid, failed bool) *ScheduledTestModelCheck {
	if mode == "" {
		mode = "exact"
	}
	snapshot := &ScheduledTestModelCheck{
		RequestedModel: strings.TrimSpace(requested), UpstreamModel: strings.TrimSpace(upstream),
		ReturnedModels: []string{}, MatchMode: mode, Verdict: "unknown", Reason: "missing_model",
	}
	if !validTestModelIdentifier(snapshot.RequestedModel) {
		snapshot.RequestedModel, invalid = "", true
	}
	if !validTestModelIdentifier(snapshot.UpstreamModel) {
		snapshot.UpstreamModel, invalid = "", true
	}
	seen := map[string]bool{}
	for _, value := range returned {
		value = strings.TrimSpace(value)
		if !validTestModelIdentifier(value) {
			invalid = true
			continue
		}
		if value == "" || seen[value] {
			continue
		}
		if len(snapshot.ReturnedModels) == 16 {
			invalid = true
			continue
		}
		seen[value] = true
		snapshot.ReturnedModels = append(snapshot.ReturnedModels, value)
	}
	if failed {
		snapshot.Reason = "upstream_error"
		return snapshot
	}
	if mode != "exact" && mode != "snapshot" {
		snapshot.MatchMode, snapshot.Reason = "exact", "invalid_evidence"
		return snapshot
	}
	if invalid {
		snapshot.Reason = "invalid_evidence"
		return snapshot
	}
	if snapshot.UpstreamModel == "" {
		snapshot.Reason = "missing_upstream_model"
		return snapshot
	}
	if len(snapshot.ReturnedModels) == 0 {
		return snapshot
	}
	for _, value := range snapshot.ReturnedModels {
		if !scheduledTestModelsMatch(snapshot.UpstreamModel, value, mode) {
			snapshot.Verdict, snapshot.Reason = "fail", "mismatch"
			return snapshot
		}
	}
	snapshot.Verdict, snapshot.Reason = "pass", "match"
	return snapshot
}

func applyScheduledTestModelCheck(result *ScheduledTestResult, plan *ScheduledTestPlan) {
	mode := "exact"
	// Comparison settings still apply to a manual run with automation disabled.
	// Only the action executor uses ProtectionRule's enablement checks.
	if plan.TestDefinitionID != nil {
		for _, rule := range plan.Protection.Rules {
			if rule.TestDefinitionID == *plan.TestDefinitionID && rule.ModelMatch != "" {
				mode = rule.ModelMatch
				break
			}
		}
	}
	result.OutputModelCheck = buildScheduledTestModelCheck(plan.ModelID, result.UpstreamModel, result.ReturnedModels, mode,
		result.ModelEvidenceInvalid, result.Status != "success" && result.Status != "passed")
	// Keep the snapshot in the existing result payload so history, retries and
	// pruning all use the same immutable evidence. Reads expose a typed object.
	raw, _ := json.Marshal(result.OutputModelCheck)
	result.ResponseText = string(raw)
	result.OutputHTML, result.OutputNumeric = "", nil
}

func normalizeScheduledTestModelCheck(result *ScheduledTestResult) {
	var stored ScheduledTestModelCheck
	invalid := false
	if result.ResponseText != "" {
		invalid = json.Unmarshal([]byte(result.ResponseText), &stored) != nil
	} else if result.OutputModelCheck != nil {
		stored = *result.OutputModelCheck
	} else {
		invalid = true
	}
	result.OutputModelCheck = nil
	if result.Status != "running" && result.Status != "pending" {
		invalid = invalid || stored.Reason == "invalid_evidence"
		result.OutputModelCheck = buildScheduledTestModelCheck(result.ModelID, stored.UpstreamModel, stored.ReturnedModels,
			stored.MatchMode, invalid, result.Status != "success" && result.Status != "passed")
	}
	result.ResponseText, result.OutputHTML, result.OutputNumeric = "", "", nil
}

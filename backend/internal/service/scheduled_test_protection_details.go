package service

import (
	"context"
	"fmt"
	"slices"
	"time"
)

// This read-only projection intentionally separates the persisted hold from
// current evidence: protection states are replaced at the start of each round.
type ScheduledTestProtectionHold struct {
	ModelID         string                            `json:"model_id,omitempty"`
	PlanID          int64                             `json:"plan_id"`
	PlanName        string                            `json:"plan_name"`
	Mode            string                            `json:"mode"`
	Reason          string                            `json:"reason"`
	UpdatedAt       time.Time                         `json:"updated_at"`
	PriorRound      bool                              `json:"prior_round"`
	Rules           []ScheduledTestProtectionHit      `json:"rules"`
	Tests           []ScheduledTestProtectionEvidence `json:"tests"`
	TriggerSnapshot *ScheduledTestProtectionTrigger   `json:"trigger_snapshot,omitempty"`
}

// Trigger evidence is an immutable projection, independent of mutable results.
type ScheduledTestProtectionTrigger struct {
	ID               int64                               `json:"id"`
	OccurredAt       time.Time                           `json:"occurred_at"`
	PlanID           int64                               `json:"plan_id"`
	PlanName         string                              `json:"plan_name"`
	TestDefinitionID int64                               `json:"test_definition_id"`
	ResultID         int64                               `json:"result_id"`
	RunID            string                              `json:"run_id"`
	ModelID          string                              `json:"model_id"`
	PolicyVersion    string                              `json:"policy_version"`
	PolicyUpdatedAt  time.Time                           `json:"policy_updated_at"`
	Policy           ScheduledTestProtectionConfig       `json:"policy"`
	Source           string                              `json:"source"`
	ActorUserID      *int64                              `json:"actor_user_id,omitempty"`
	Reason           string                              `json:"reason"`
	Decision         ScheduledTestProtectionDecision     `json:"decision"`
	Before           ScheduledTestProtectionAccountState `json:"before"`
	After            ScheduledTestProtectionAccountState `json:"after"`
	Rules            []ScheduledTestProtectionHit        `json:"rules"`
	Tests            []ScheduledTestProtectionEvidence   `json:"tests"`
}

type ScheduledTestProtectionAccountState struct {
	Status      string  `json:"status"`
	Schedulable bool    `json:"schedulable"`
	GroupIDs    []int64 `json:"group_ids"`
}

type ScheduledTestProtectionHit struct {
	ID           string                             `json:"id"`
	Name         string                             `json:"name"`
	Condition    *ScheduledTestCombinationCondition `json:"condition,omitempty"`
	CurrentMatch string                             `json:"current_match"`
}

type ScheduledTestProtectionEvidence struct {
	TestDefinitionID int64                          `json:"test_definition_id"`
	TestName         string                         `json:"test_name"`
	ResultID         int64                          `json:"result_id,omitempty"`
	RunID            string                         `json:"run_id,omitempty"`
	MinSamples       int64                          `json:"min_samples"`
	WindowStart      *time.Time                     `json:"window_start,omitempty"`
	WindowEnd        *time.Time                     `json:"window_end,omitempty"`
	StartedAt        *time.Time                     `json:"started_at,omitempty"`
	Current          bool                           `json:"current"`
	Verdict          string                         `json:"verdict"`
	AutomatedVerdict string                         `json:"automated_verdict"`
	AdminVerdict     string                         `json:"admin_verdict"`
	Reason           string                         `json:"reason"`
	AutomaticReason  string                         `json:"automatic_reason"`
	Thresholds       []ScheduledTestThresholdDetail `json:"thresholds"`
	Vote             *ScheduledTestVoteConfig       `json:"vote,omitempty"`
	PassCount        int64                          `json:"pass_count"`
	FailCount        int64                          `json:"fail_count"`
}

type ScheduledTestThresholdDetail struct {
	ScheduledTestThreshold
	Observed *float64 `json:"observed"`
	Samples  *int64   `json:"samples,omitempty"`
}

// Source fields are private to the admin projection and never serialized to
// clients. In particular, full model output is only used to evaluate metrics.
type ScheduledTestProtectionHoldSource struct {
	ModelID         string
	PlanID          int64
	PlanName        string
	DefinitionID    int64
	Reason          string
	UpdatedAt       time.Time
	RuleIDs         []string
	Config          ScheduledTestProtectionConfig
	States          []ScheduledTestProtectionStateSource
	TriggerSnapshot *ScheduledTestProtectionTrigger
}

type ScheduledTestProtectionStateSource struct {
	DefinitionID int64                       `json:"test_definition_id"`
	TestName     string                      `json:"test_name"`
	Current      bool                        `json:"current"`
	RoundStarted time.Time                   `json:"round_started_at"`
	Verdict      string                      `json:"verdict"`
	Automated    string                      `json:"automated_verdict"`
	AdminVerdict string                      `json:"admin_verdict"`
	Reason       string                      `json:"reason"`
	Rule         ScheduledTestProtectionRule `json:"rule_config"`
	Result       *ScheduledTestResult        `json:"result"`
	PassCount    int64                       `json:"pass_count"`
	FailCount    int64                       `json:"fail_count"`
	RunID        string                      `json:"run_id"`
}

type ScheduledTestProtectionDetailsRepository interface {
	ListProtectionHolds(context.Context, int64) ([]ScheduledTestProtectionHoldSource, error)
}

func (s *ScheduledTestService) AccountProtectionDetails(ctx context.Context, accountID int64) ([]ScheduledTestProtectionHold, error) {
	repo, ok := s.resultRepo.(ScheduledTestProtectionDetailsRepository)
	if !ok {
		return nil, fmt.Errorf("quality protection details unavailable")
	}
	sources, err := repo.ListProtectionHolds(ctx, accountID)
	if err != nil {
		return nil, err
	}
	holds := make([]ScheduledTestProtectionHold, 0, len(sources))
	for _, source := range sources {
		holds = append(holds, buildScheduledTestProtectionHold(source))
	}
	return holds, nil
}

func buildScheduledTestProtectionHold(source ScheduledTestProtectionHoldSource) ScheduledTestProtectionHold {
	return BuildScheduledTestProtectionHold(source)
}

// BuildScheduledTestProtectionHold is shared by current details and persisted
// trigger evidence, so both use exactly the same metric validity checks.
func BuildScheduledTestProtectionHold(source ScheduledTestProtectionHoldSource) ScheduledTestProtectionHold {
	source.States = slices.Clone(source.States)
	for i := range source.States {
		if source.States[i].Result != nil {
			copy := *source.States[i].Result
			if copy.OutputStatistics == nil {
				normalizeStoredTestResults([]*ScheduledTestResult{&copy})
			}
			source.States[i].Result = &copy
		}
	}
	hold := ScheduledTestProtectionHold{PlanID: source.PlanID, PlanName: source.PlanName,
		Reason: source.Reason, UpdatedAt: source.UpdatedAt, Mode: "per_test", ModelID: source.ModelID,
		Rules: []ScheduledTestProtectionHit{}, Tests: []ScheduledTestProtectionEvidence{}, TriggerSnapshot: source.TriggerSnapshot}
	verdicts := map[int64]string{}
	for _, state := range source.States {
		if state.Current {
			verdicts[state.DefinitionID] = state.Verdict
		}
	}
	referenced := map[int64]bool{}
	if source.DefinitionID != 0 {
		referenced[source.DefinitionID] = true
	} else if source.Config.UsesAllPass() {
		hold.Mode = "all_pass"
		for _, rule := range source.Config.Rules {
			referenced[rule.TestDefinitionID] = true
		}
	} else {
		hold.Mode = "combined"
		for _, id := range source.RuleIDs {
			hit := ScheduledTestProtectionHit{ID: id, Name: id, CurrentMatch: "pending"}
			for _, rule := range source.Config.Combinations {
				if rule.ID == id {
					hit.Name, hit.Condition = rule.Name, &rule.Condition
					hit.CurrentMatch = EvaluateScheduledTestCombinationCondition(rule.Condition, verdicts)
					collectProtectionConditionTests(rule.Condition, referenced)
					break
				}
			}
			hold.Rules = append(hold.Rules, hit)
		}
	}
	for _, state := range source.States {
		if !referenced[state.DefinitionID] {
			continue
		}
		if state.RoundStarted.After(source.UpdatedAt) {
			hold.PriorRound = true
		}
		evidence := ScheduledTestProtectionEvidence{TestDefinitionID: state.DefinitionID,
			TestName: state.TestName, Current: state.Current, Verdict: "pending", AutomatedVerdict: "pending",
			Vote: state.Rule.Vote, Thresholds: []ScheduledTestThresholdDetail{}, RunID: state.RunID, MinSamples: state.Rule.MinSamples}
		if evidence.MinSamples < 1 {
			evidence.MinSamples = 1
		}
		if state.Current && state.Result != nil && state.Result.OutputStatistics != nil {
			evidence.WindowStart, evidence.WindowEnd = &state.Result.OutputStatistics.WindowStart, &state.Result.OutputStatistics.WindowEnd
		}
		if state.Result != nil {
			evidence.ResultID, evidence.StartedAt = state.Result.ID, &state.Result.StartedAt
		}
		if state.Current {
			evidence.Verdict, evidence.AutomatedVerdict = state.Verdict, state.Automated
			evidence.AdminVerdict, evidence.Reason = state.AdminVerdict, state.Reason
			evidence.PassCount, evidence.FailCount = state.PassCount, state.FailCount
			_, evidence.AutomaticReason = evaluateScheduledTestProtection(state.Rule, state.Result)
		}
		for _, threshold := range state.Rule.Thresholds {
			detail := ScheduledTestThresholdDetail{ScheduledTestThreshold: threshold}
			if state.Current && state.Result != nil {
				detail.Samples = scheduledTestMetricSamples(state.Result, threshold.Metric)
			}
			if state.Current && state.Result != nil && (state.Result.Status == "success" || state.Result.Status == "passed") {
				if value, ok := scheduledTestMetric(state.Result, threshold.Metric, state.Rule.MinSamples); ok {
					detail.Observed = &value
				}
			}
			evidence.Thresholds = append(evidence.Thresholds, detail)
		}
		hold.Tests = append(hold.Tests, evidence)
	}
	return hold
}

func scheduledTestMetricSamples(result *ScheduledTestResult, metric string) *int64 {
	if stats := result.OutputStatistics; stats != nil {
		switch metric {
		case "success_rate":
			return &stats.TotalRequests
		case "cache_rate":
			return &stats.CacheSamples
		case "avg_first_token_ms":
			return &stats.FirstTokenSamples
		case "output_tps":
			return &stats.TPSSamples
		}
	}
	if metric == "latency_ms" || metric == "output_numeric" {
		samples := int64(1)
		return &samples
	}
	return nil
}

func collectProtectionConditionTests(condition ScheduledTestCombinationCondition, ids map[int64]bool) {
	if condition.Operator == "test" {
		ids[condition.TestDefinitionID] = true
		return
	}
	for _, child := range condition.Conditions {
		collectProtectionConditionTests(child, ids)
	}
}

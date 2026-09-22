package service

import (
	"context"
	"errors"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
)

var (
	ErrScheduledTestVoteUnavailable = errors.New("this voting round is unavailable")
	ErrScheduledTestVoteInvalid     = errors.New("vote must be pass or fail")
)

type ScheduledTestProtectionConfig struct {
	Enabled      bool                           `json:"enabled"`
	Rules        []ScheduledTestProtectionRule  `json:"rules"`
	Mode         string                         `json:"mode,omitempty"`
	Combinations []ScheduledTestCombinationRule `json:"combinations,omitempty"`
}

type ScheduledTestProtectionRule struct {
	Priority         int                               `json:"priority"`
	RequiredPass     bool                              `json:"required_pass"`
	TestDefinitionID int64                             `json:"test_definition_id"`
	Thresholds       []ScheduledTestThreshold          `json:"thresholds,omitempty"`
	MinSamples       int64                             `json:"min_samples,omitempty"`
	PauseOnFailure   bool                              `json:"pause_on_failure,omitempty"`
	ExpectedAnswer   string                            `json:"expected_answer,omitempty"`
	AnswerMatch      string                            `json:"answer_match,omitempty"`
	ModelMatch       string                            `json:"model_match,omitempty"`
	Vote             *ScheduledTestVoteConfig          `json:"vote,omitempty"`
	OnPass           *ScheduledTestOutcomeAction       `json:"on_pass,omitempty"`
	OnFail           *ScheduledTestOutcomeAction       `json:"on_fail,omitempty"`
	Recovery         *ScheduledTestCacheRecoveryConfig `json:"recovery,omitempty"`
}

type ScheduledTestThreshold struct {
	Metric   string  `json:"metric"`
	Operator string  `json:"operator"`
	Value    float64 `json:"value"`
}

type ScheduledTestVoteConfig struct {
	Enabled       bool `json:"enabled"`
	PublicEnabled bool `json:"public_enabled,omitempty"`
	RejectAbove   int  `json:"reject_above"`
	PassAtLeast   int  `json:"pass_at_least"`
}

type ScheduledTestVotingSummary struct {
	Enabled         bool   `json:"enabled"`
	Open            bool   `json:"open"`
	PassCount       int    `json:"pass_count"`
	FailCount       int    `json:"fail_count"`
	MyVote          string `json:"my_vote,omitempty"`
	RejectAbove     int    `json:"reject_above"`
	PassAtLeast     int    `json:"pass_at_least"`
	ReferenceAnswer string `json:"reference_answer,omitempty"`
	AccountPaused   bool   `json:"account_paused"`
}

type ScheduledTestVoteResult struct {
	Result *ScheduledTestResult       `json:"result"`
	Voting ScheduledTestVotingSummary `json:"voting"`
}

// Optional repository capability keeps legacy execution adapters compatible.
// State transitions and ballot authorization must be checked transactionally.
type ScheduledTestProtectionRepository interface {
	ListDetectionAccountIDs(context.Context, *int64, *int64) ([]int64, error)
	BeginProtection(context.Context, *ScheduledTestResult, ScheduledTestProtectionRule) error
	CompleteProtection(context.Context, *ScheduledTestResult, string, string) error
	ListVotingResults(context.Context, int64) ([]*ScheduledTestVoteResult, error)
	CastTestVote(context.Context, int64, int64, string) (*ScheduledTestVoteResult, error)
	ClearPlanProtection(context.Context, int64) error
}

func (p *ScheduledTestPlan) ProtectionRule(definitionID *int64) *ScheduledTestProtectionRule {
	if p == nil || !p.Enabled || !p.Protection.Enabled || definitionID == nil {
		return nil
	}
	for i := range p.Protection.Rules {
		if p.Protection.Rules[i].TestDefinitionID == *definitionID {
			return &p.Protection.Rules[i]
		}
	}
	return nil
}

func validateScheduledTestProtection(plan *ScheduledTestPlan) error {
	config := &plan.Protection
	if config.Mode != "" && config.Mode != "per_test" && config.Mode != "combined" {
		return fmt.Errorf("protection mode must be per_test or combined")
	}
	if !config.Enabled {
		return nil
	}
	if len(config.Rules) == 0 || len(config.Rules) > 32 {
		return fmt.Errorf("automatic protection requires between 1 and 32 rules")
	}
	selected := make(map[int64]bool, len(plan.TestDefinitionIDs))
	for _, id := range plan.TestDefinitionIDs {
		selected[id] = true
	}
	seen := map[int64]bool{}
	for i := range config.Rules {
		rule := &config.Rules[i]
		if !selected[rule.TestDefinitionID] || seen[rule.TestDefinitionID] {
			return fmt.Errorf("protection rules must refer to distinct selected test definitions")
		}
		seen[rule.TestDefinitionID] = true
		if !config.UsesCombinations() && (rule.Priority < 0 || rule.Priority > 1000) {
			return fmt.Errorf("priority must be between 0 and 1000")
		}
		if !config.UsesCombinations() && rule.RequiredPass && rule.Vote != nil && rule.Vote.Enabled {
			return fmt.Errorf("required_pass only supports automatic conditions")
		}
		if !config.UsesCombinations() || (rule.Recovery != nil && rule.Recovery.Enabled) {
			if err := validateScheduledTestActions(rule); err != nil {
				return err
			}
		}
		for _, groupID := range rule.ManagedGroupIDs() {
			if !config.UsesCombinations() && !slices.Contains(plan.GroupIDs, groupID) {
				return fmt.Errorf("action group %d must be selected in group_ids", groupID)
			}
		}
		if rule.MinSamples < 0 || rule.MinSamples > 1000000000 {
			return fmt.Errorf("min_samples must be between 0 and 1000000000")
		}
		rule.ExpectedAnswer = strings.TrimSpace(rule.ExpectedAnswer)
		if len([]rune(rule.ExpectedAnswer)) > 10000 {
			return fmt.Errorf("expected_answer must be at most 10000 characters")
		}
		if rule.AnswerMatch == "" {
			rule.AnswerMatch = "exact"
		}
		switch rule.AnswerMatch {
		case "exact", "contains", "numeric":
		default:
			return fmt.Errorf("answer_match must be exact, contains, or numeric")
		}
		if rule.ModelMatch != "" && rule.ModelMatch != "exact" && rule.ModelMatch != "snapshot" {
			return fmt.Errorf("model_match must be exact or snapshot")
		}
		voting := rule.Vote != nil && rule.Vote.Enabled
		if rule.Vote != nil && rule.Vote.PublicEnabled && !voting {
			return fmt.Errorf("public voting requires review to be enabled")
		}
		if voting && (rule.Vote.RejectAbove < 0 || rule.Vote.PassAtLeast < 1 || rule.Vote.RejectAbove > 1000000 || rule.Vote.PassAtLeast > 1000000) {
			return fmt.Errorf("reject_above must be non-negative and pass_at_least positive (maximum 1000000)")
		}
		if !voting && rule.ExpectedAnswer != "" && rule.AnswerMatch == "numeric" {
			v, err := strconv.ParseFloat(rule.ExpectedAnswer, 64)
			if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
				return fmt.Errorf("numeric expected_answer must be finite")
			}
		}
		if len(rule.Thresholds) > 20 {
			return fmt.Errorf("at most 20 thresholds per test are supported")
		}
		for _, threshold := range rule.Thresholds {
			if threshold.Operator != "lt" && threshold.Operator != "gt" {
				return fmt.Errorf("threshold operator must be lt or gt")
			}
			if math.IsNaN(threshold.Value) || math.IsInf(threshold.Value, 0) {
				return fmt.Errorf("threshold must be finite")
			}
			switch threshold.Metric {
			case "success_rate", "cache_rate":
				if threshold.Value < 0 || threshold.Value > 100 {
					return fmt.Errorf("percentage threshold must be between 0 and 100")
				}
			case "latency_ms", "avg_first_token_ms":
				if threshold.Value < 0 {
					return fmt.Errorf("latency threshold cannot be negative")
				}
			case "output_numeric":
			default:
				return fmt.Errorf("unsupported protection metric %q", threshold.Metric)
			}
		}
		if err := validateScheduledTestCacheRecovery(rule); err != nil {
			return err
		}
	}
	return validateScheduledTestCombinations(plan)
}

func validateProtectionOutputKind(rule *ScheduledTestProtectionRule, kind string) error {
	if rule == nil {
		return nil
	}
	if rule.Recovery != nil && rule.Recovery.Enabled && kind != "statistics" {
		return fmt.Errorf("cache recovery requires a statistics test")
	}
	if kind == "model_check" {
		if (rule.Vote != nil && rule.Vote.Enabled) || rule.ExpectedAnswer != "" {
			return fmt.Errorf("model consistency uses response metadata and cannot use answer comparison or voting")
		}
		if rule.ModelMatch == "" {
			rule.ModelMatch = "exact"
		}
	} else {
		if rule.ModelMatch != "" {
			return fmt.Errorf("model_match requires a model_check test")
		}
		if (rule.Vote == nil || !rule.Vote.Enabled) && !rule.PauseOnFailure && rule.ExpectedAnswer == "" && len(rule.Thresholds) == 0 && rule.OnFail == nil {
			return fmt.Errorf("each protection rule needs a threshold, answer, voting, or failure check")
		}
	}
	if kind == "statistics" && ((rule.Vote != nil && rule.Vote.Enabled) || rule.ExpectedAnswer != "") {
		return fmt.Errorf("statistics cannot use answer comparison or voting")
	}
	for _, threshold := range rule.Thresholds {
		switch threshold.Metric {
		case "success_rate", "cache_rate", "avg_first_token_ms":
			if kind != "statistics" {
				return fmt.Errorf("metric %s requires a statistics test", threshold.Metric)
			}
		case "output_numeric":
			if kind != "number" {
				return fmt.Errorf("output_numeric requires a numeric test")
			}
		case "latency_ms":
			if kind == "statistics" {
				return fmt.Errorf("statistics latency uses avg_first_token_ms")
			}
		}
	}
	return nil
}

// Missing observations are inconclusive, never a synthetic zero or a pass that
// could release an existing suspension. Any observed violation wins.
func evaluateScheduledTestProtection(rule ScheduledTestProtectionRule, result *ScheduledTestResult) (string, string) {
	if result == nil || result.Status == "pending" || result.Status == "running" {
		return "pending", "检测未完成，等待下一轮"
	}
	if result.Status != "success" && result.Status != "passed" {
		// A configured failure action is an explicit request to resolve an
		// unsuccessful execution as a failed quality check.  This lets rules
		// such as Candy immediately run their on_fail group/scheduling action
		// after the final retry, while preserving the legacy pending behavior
		// for rules that have neither an action nor pause_on_failure enabled.
		if rule.PauseOnFailure || rule.OnFail != nil {
			return "fail", "检测执行失败"
		}
		return "pending", "检测未完成，等待下一轮"
	}
	missing := false
	if result.OutputKind == "model_check" {
		check := result.OutputModelCheck
		if check != nil && check.Verdict == "fail" {
			return "fail", "上游返回模型与实际请求模型不一致"
		}
		missing = check == nil || check.Verdict != "pass"
	}
	for _, threshold := range rule.Thresholds {
		value, ok := scheduledTestMetric(result, threshold.Metric, rule.MinSamples)
		if !ok {
			missing = true
			continue
		}
		comparisonValue, comparisonThreshold := value, threshold.Value
		// Compare ratios directly. Multiplication by 100 can turn an equal
		// boundary (e.g. 0.29 and 29%) into a false strict-less violation.
		if threshold.Metric == "success_rate" || threshold.Metric == "cache_rate" {
			comparisonThreshold /= 100
			if threshold.Metric == "success_rate" {
				comparisonValue = *result.OutputStatistics.SuccessRate
			} else {
				comparisonValue = *result.OutputStatistics.CacheRate
			}
			// Decimal percentage thresholds can differ by a few representable
			// floats after division (e.g. 1.1% versus 11/1000). Only absorb
			// that rounding error; do not relax numeric or latency rules.
			scale := math.Max(math.Abs(comparisonValue), math.Abs(comparisonThreshold))
			ulp := math.Nextafter(scale, math.Inf(1)) - scale
			if math.Abs(comparisonValue-comparisonThreshold) <= 4*ulp {
				continue
			}
		}
		if (threshold.Operator == "lt" && comparisonValue < comparisonThreshold) || (threshold.Operator == "gt" && comparisonValue > comparisonThreshold) {
			return "fail", scheduledTestThresholdReason(threshold, value)
		}
	}
	if rule.ExpectedAnswer != "" && (rule.Vote == nil || !rule.Vote.Enabled) {
		answer := strings.TrimSpace(result.ResponseText)
		matched := false
		switch rule.AnswerMatch {
		case "contains":
			matched = strings.Contains(answer, rule.ExpectedAnswer)
		case "numeric":
			value, ok := scheduledTestMetric(result, "output_numeric", 0)
			if !ok {
				value, ok = extractScheduledTestNumber(result.ResponseText)
			}
			expected, err := strconv.ParseFloat(rule.ExpectedAnswer, 64)
			matched = ok && err == nil && value == expected
		default:
			matched = answer == rule.ExpectedAnswer
		}
		if !matched {
			return "fail", "检测答案不符合配置"
		}
	}
	if missing {
		if result.OutputKind == "model_check" {
			return "pending", "缺少有效模型标识，无法确认一致性"
		}
		return "pending", "有效样本不足，等待下一轮"
	}
	return "pass", ""
}

func scheduledTestThresholdReason(threshold ScheduledTestThreshold, observed float64) string {
	label, unit := threshold.Metric, ""
	switch threshold.Metric {
	case "success_rate":
		label, unit = "成功率", "%"
	case "cache_rate":
		label, unit = "缓存率", "%"
	case "avg_first_token_ms":
		label, unit = "平均首字延迟", "ms"
	case "latency_ms":
		label, unit = "检测耗时", "ms"
	case "output_numeric":
		label = "输出数值"
	}
	operator := "小于"
	if threshold.Operator == "gt" {
		operator = "大于"
	}
	return fmt.Sprintf("%s%s%g%s（实际 %g%s）", label, operator, threshold.Value, unit, observed, unit)
}

func scheduledTestMetric(result *ScheduledTestResult, metric string, minSamples int64) (float64, bool) {
	var value *float64
	snapshot := result.OutputStatistics
	if minSamples < 1 {
		minSamples = 1
	}
	switch metric {
	case "latency_ms":
		if result.LatencyMs < 0 {
			return 0, false
		}
		return float64(result.LatencyMs), true
	case "output_numeric":
		// Use the same extraction as result display. Persisted numeric output
		// may have been derived by an older parser; source text is authoritative.
		if strings.EqualFold(strings.TrimSpace(result.OutputKind), "number") && strings.TrimSpace(result.ResponseText) != "" {
			return extractScheduledTestNumber(result.ResponseText)
		}
		value = result.OutputNumeric
	case "success_rate", "cache_rate", "avg_first_token_ms":
		if snapshot == nil || snapshot.TotalRequests < minSamples {
			return 0, false
		}
		switch metric {
		case "success_rate":
			value = snapshot.SuccessRate
		case "cache_rate":
			if snapshot.CacheInputTokens <= 0 || snapshot.CacheSamples < minSamples {
				return 0, false
			}
			value = snapshot.CacheRate
		case "avg_first_token_ms":
			if snapshot.FirstTokenSamples < minSamples {
				return 0, false
			}
			value = snapshot.AvgFirstTokenMs
		}
	}
	if value == nil || math.IsNaN(*value) || math.IsInf(*value, 0) {
		return 0, false
	}
	if metric == "success_rate" || metric == "cache_rate" {
		if *value < 0 || *value > 1 {
			return 0, false
		}
		return *value * 100, true
	}
	return *value, true
}

func (s *ScheduledTestService) protectionRepository() ScheduledTestProtectionRepository {
	if s == nil {
		return nil
	}
	repo, _ := s.resultRepo.(ScheduledTestProtectionRepository)
	return repo
}

func (s *ScheduledTestService) ListVotingResults(ctx context.Context, userID int64) ([]*ScheduledTestVoteResult, error) {
	repo := s.protectionRepository()
	if repo == nil {
		return nil, ErrScheduledTestVoteUnavailable
	}
	rows, err := repo.ListVotingResults(ctx, userID)
	if err != nil {
		return nil, err
	}
	if rows == nil {
		rows = []*ScheduledTestVoteResult{}
	}
	for _, row := range rows {
		sanitizeTestVoteResult(row)
	}
	return rows, nil
}

func (s *ScheduledTestService) CastTestVote(ctx context.Context, userID, resultID int64, vote string) (*ScheduledTestVoteResult, error) {
	if vote != "pass" && vote != "fail" {
		return nil, ErrScheduledTestVoteInvalid
	}
	if userID <= 0 || resultID <= 0 {
		return nil, ErrScheduledTestVoteUnavailable
	}
	repo := s.protectionRepository()
	if repo == nil {
		return nil, ErrScheduledTestVoteUnavailable
	}
	row, err := repo.CastTestVote(ctx, userID, resultID, vote)
	if err != nil {
		return nil, err
	}
	sanitizeTestVoteResult(row)
	return row, nil
}

func sanitizeTestVoteResult(row *ScheduledTestVoteResult) {
	if row == nil || row.Result == nil {
		return
	}
	row.Result.AccountName = ""
	row.Result.ErrorMessage = ""
	normalizeStoredTestResults([]*ScheduledTestResult{row.Result})
}

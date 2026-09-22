package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai_compat"
	"github.com/stretchr/testify/require"
)

func TestScheduledTestModelCheckEvidence(t *testing.T) {
	for _, tc := range []struct {
		name, upstream, mode, verdict, reason string
		returned                              []string
		invalid, failed                       bool
	}{
		{name: "mapped request compared to actual upstream", upstream: "gpt-5", returned: []string{"gpt-5"}, verdict: "pass", reason: "match"},
		{name: "case sensitive", upstream: "gpt-5", returned: []string{"GPT-5"}, verdict: "fail", reason: "mismatch"},
		{name: "exact rejects dated model", upstream: "gpt-5", returned: []string{"gpt-5-2026-09-21"}, verdict: "fail", reason: "mismatch"},
		{name: "snapshot accepts valid date", upstream: "gpt-5", mode: "snapshot", returned: []string{"gpt-5-2026-09-21"}, verdict: "pass", reason: "match"},
		{name: "snapshot accepts exact", upstream: "gpt-5", mode: "snapshot", returned: []string{"gpt-5"}, verdict: "pass", reason: "match"},
		{name: "snapshot rejects impossible date", upstream: "gpt-5", mode: "snapshot", returned: []string{"gpt-5-2026-02-30"}, verdict: "fail", reason: "mismatch"},
		{name: "snapshot rejects short date", upstream: "gpt-5", mode: "snapshot", returned: []string{"gpt-5-2026-9-1"}, verdict: "fail", reason: "mismatch"},
		{name: "snapshot rejects other variant", upstream: "gpt-5", mode: "snapshot", returned: []string{"gpt-5-mini"}, verdict: "fail", reason: "mismatch"},
		{name: "all metadata must agree", upstream: "gpt-5", returned: []string{"gpt-5", "gpt-5-mini"}, verdict: "fail", reason: "mismatch"},
		{name: "missing metadata", upstream: "gpt-5", verdict: "unknown", reason: "missing_model"},
		{name: "missing request identity", returned: []string{"gpt-5"}, verdict: "unknown", reason: "missing_upstream_model"},
		{name: "invalid metadata", upstream: "gpt-5", returned: []string{"gpt-5"}, invalid: true, verdict: "unknown", reason: "invalid_evidence"},
		{name: "control character", upstream: "gpt-5", returned: []string{"gpt\n5"}, verdict: "unknown", reason: "invalid_evidence"},
		{name: "oversized model", upstream: "gpt-5", returned: []string{strings.Repeat("x", 257)}, verdict: "unknown", reason: "invalid_evidence"},
		{name: "invalid UTF8", upstream: "gpt-5", returned: []string{string([]byte{0xff})}, verdict: "unknown", reason: "invalid_evidence"},
		{name: "invalid mode", upstream: "gpt-5", returned: []string{"gpt-5"}, mode: "contains", verdict: "unknown", reason: "invalid_evidence"},
		{name: "failed execution cannot pass", upstream: "gpt-5", returned: []string{"gpt-5"}, failed: true, verdict: "unknown", reason: "upstream_error"},
		{name: "failed execution cannot prove mismatch", upstream: "gpt-5", returned: []string{"gpt-5-mini"}, failed: true, verdict: "unknown", reason: "upstream_error"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := buildScheduledTestModelCheck("public-alias", tc.upstream, tc.returned, tc.mode, tc.invalid, tc.failed)
			require.Equal(t, tc.verdict, result.Verdict)
			require.Equal(t, tc.reason, result.Reason)
			require.Equal(t, "public-alias", result.RequestedModel)
		})
	}
	result := buildScheduledTestModelCheck(" alias ", " gpt-5 ", []string{" gpt-5 ", "gpt-5"}, "", false, false)
	require.Equal(t, "pass", result.Verdict)
	require.Equal(t, []string{"gpt-5"}, result.ReturnedModels)
	models := make([]string, 17)
	for i := range models {
		models[i] = fmt.Sprintf("model-%d", i)
	}
	result = buildScheduledTestModelCheck("alias", "gpt-5", models, "snapshot", false, false)
	require.Equal(t, "unknown", result.Verdict)
	require.Equal(t, "snapshot", result.MatchMode)
	require.Len(t, result.ReturnedModels, 16)
}

func TestScheduledTestModelCheckNormalization(t *testing.T) {
	plan := protectionPlan()
	plan.Protection.Rules[0].ModelMatch = "snapshot"
	result := &ScheduledTestResult{ModelID: plan.ModelID, Status: "success", OutputKind: "model_check",
		UpstreamModel: "actual-model", ReturnedModels: []string{"actual-model-2026-09-21"},
		ResponseText: "I claim to be another model", OutputHTML: "<html>private output</html>", OutputNumeric: protectionFloat(1),
	}
	applyScheduledTestModelCheck(result, plan)
	require.Equal(t, "pass", result.OutputModelCheck.Verdict)
	encoded, err := json.Marshal(result)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "UpstreamModel")
	require.NotContains(t, string(encoded), "ModelEvidenceInvalid")
	stored := &ScheduledTestResult{ModelID: result.ModelID, Status: "success", OutputKind: "model_check", ResponseText: result.ResponseText}
	normalizeStoredTestResults([]*ScheduledTestResult{stored})
	require.Equal(t, result.OutputModelCheck, stored.OutputModelCheck)
	require.Empty(t, stored.ResponseText)
	normalizeStoredTestResults([]*ScheduledTestResult{stored})
	require.Equal(t, "pass", stored.OutputModelCheck.Verdict, "repeated normalization must not destroy evidence")
	stored.ResponseText = `{"requested_model":"spoofed","upstream_model":"A","returned_models":["B"],"match_mode":"exact","verdict":"pass","reason":"match"}`
	normalizeStoredTestResults([]*ScheduledTestResult{stored})
	require.Equal(t, "fail", stored.OutputModelCheck.Verdict, "stored verdict is derived again from metadata")
	require.Equal(t, plan.ModelID, stored.OutputModelCheck.RequestedModel)
	stored.ResponseText = "not a model snapshot"
	normalizeStoredTestResults([]*ScheduledTestResult{stored})
	require.Equal(t, "invalid_evidence", stored.OutputModelCheck.Reason)
	stored.ResponseText = `{"upstream_model":"\ud800","returned_models":["\ud800"],"match_mode":"exact"}`
	normalizeStoredTestResults([]*ScheduledTestResult{stored})
	require.Equal(t, "invalid_evidence", stored.OutputModelCheck.Reason, "corrupt Unicode cannot be normalized into matching identities")
	stored.Status = "running"
	normalizeStoredTestResults([]*ScheduledTestResult{stored})
	require.Nil(t, stored.OutputModelCheck)
}

func TestScheduledTestModelCheckProtection(t *testing.T) {
	rule := ScheduledTestProtectionRule{ModelMatch: "exact"}
	for _, tc := range []struct{ check, expected string }{{"pass", "pass"}, {"fail", "fail"}, {"unknown", "pending"}} {
		result := &ScheduledTestResult{Status: "success", OutputKind: "model_check", OutputModelCheck: &ScheduledTestModelCheck{Verdict: tc.check}}
		verdict, _ := evaluateScheduledTestProtection(rule, result)
		require.Equal(t, tc.expected, verdict)
	}
	result := &ScheduledTestResult{Status: "success", OutputKind: "model_check", ResponseText: `{"model":"gpt-5"}`}
	verdict, _ := evaluateScheduledTestProtection(rule, result)
	require.Equal(t, "pending", verdict, "assistant text is never evidence")
	rule.Thresholds = []ScheduledTestThreshold{{Metric: "latency_ms", Operator: "gt", Value: 100}}
	result.LatencyMs = 101
	verdict, _ = evaluateScheduledTestProtection(rule, result)
	require.Equal(t, "fail", verdict, "a known latency violation still applies with missing model metadata")
}

func TestScheduledTestModelCheckManualRunRetainsMatchModeWithoutActions(t *testing.T) {
	for _, disablePlan := range []bool{true, false} {
		plan := protectionPlan()
		plan.Protection.Rules[0].ModelMatch = "snapshot"
		if disablePlan {
			plan.Enabled = false
		} else {
			plan.Protection.Enabled = false
		}
		result := &ScheduledTestResult{Status: "success", UpstreamModel: "gpt-5", ReturnedModels: []string{"gpt-5-2026-09-21"}}
		applyScheduledTestModelCheck(result, plan)
		require.Equal(t, "pass", result.OutputModelCheck.Verdict)
		require.Equal(t, "snapshot", result.OutputModelCheck.MatchMode)
		require.Nil(t, plan.ProtectionRule(plan.TestDefinitionID), "disabled automation still prevents applying actions")
	}
}

func TestScheduledTestModelCheckValidation(t *testing.T) {
	p := protectionPlan()
	p.Protection.Rules[0].PauseOnFailure = false
	require.NoError(t, validateScheduledTestProtection(p))
	rule := &p.Protection.Rules[0]
	require.Error(t, validateProtectionOutputKind(rule, "text"))
	require.NoError(t, validateProtectionOutputKind(rule, "model_check"))
	require.Equal(t, "exact", rule.ModelMatch)
	require.Error(t, validateProtectionOutputKind(rule, "number"))
	for _, invalidRule := range []ScheduledTestProtectionRule{
		{ExpectedAnswer: "OK"}, {Vote: &ScheduledTestVoteConfig{Enabled: true}},
		{Thresholds: []ScheduledTestThreshold{{Metric: "output_numeric", Operator: "lt", Value: 1}}},
	} {
		require.Error(t, validateProtectionOutputKind(&invalidRule, "model_check"))
	}
	definition := &ScheduledTestDefinition{Key: "model_check", Name: "Model check", OutputKind: "model_check"}
	require.NoError(t, ValidateScheduledTestDefinitionInput(definition))
	require.Equal(t, scheduledTestModelCheckPrompt, definition.Prompt)
}

func TestScheduledTestModelCheckRunnerPersistsVerdictAndRunsActions(t *testing.T) {
	for _, tc := range []struct{ name, body, verdict, action string }{
		{"mapped match", `{"model":"mapped-model","status":"completed","output":[]}`, "pass", "pass"},
		{"mismatch", `{"model":"another-model","status":"completed","output":[]}`, "fail", "fail"},
		{"missing metadata", `{"status":"completed","output":[]}`, "unknown", "pending"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plan := protectionPlan()
			plan.ModelID = "public-model"
			plan.Protection.Rules[0].PauseOnFailure = false
			repo := &protectionRepositoryStub{eligible: true}
			calls := 0
			accounts := scheduledTestExecutionAccountRepo{get: func(_ context.Context, id int64) (*Account, error) {
				calls++
				return &Account{ID: id, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive,
					Credentials: map[string]any{"api_key": "test", "base_url": "https://upstream.example.com", "model_mapping": map[string]any{"public-model": "mapped-model"}},
					Extra:       map[string]any{openai_compat.ExtraKeyResponsesSupported: true}}, nil
			}}
			svc := NewScheduledTestService(nil, repo)
			svc.SetDefinitionRepository(multiDefinitionRepoStub{definitions: map[int64]*ScheduledTestDefinition{
				1: {ID: 1, Enabled: true, OutputKind: "model_check", Prompt: "ignored custom prompt"},
			}})
			accountTests := &AccountTestService{accountRepo: accounts, httpUpstream: &modelIdentityHTTPUpstream{body: tc.body}, cfg: &config.Config{}}
			runner := NewScheduledTestRunnerService(nil, svc, accountTests, accounts, nil, nil)
			runner.automaticRetryBaseDelay = time.Millisecond
			prompt, kind, err := runner.resolveDefinition(context.Background(), plan)
			require.NoError(t, err)
			require.Equal(t, scheduledTestModelCheckPrompt, prompt)
			runner.runOneAccount(context.Background(), plan, 42, prompt, kind)
			require.Equal(t, 1, calls, "completed metadata checks must not trigger transport retries")
			require.Equal(t, "success", repo.completed.Status)
			require.Equal(t, repo.created.ID, repo.completed.ID)
			require.Equal(t, tc.verdict, repo.completed.OutputModelCheck.Verdict)
			require.Equal(t, tc.action, repo.verdict)
			require.Contains(t, repo.completed.ResponseText, `"upstream_model":"mapped-model"`)
		})
	}
}

func TestScheduledTestModelCheckManualRunRetriesOverloadBeforeApplyingActions(t *testing.T) {
	const overload = `data: {"type":"response.failed","response":{"model":"another-model","status":"failed","error":{"message":"Our servers are currently overloaded. Please try again later."}}}` + "\n\n"
	for _, recover := range []bool{true, false} {
		t.Run(fmt.Sprintf("recovers=%v", recover), func(t *testing.T) {
			plan := protectionPlan()
			plan.ModelID = "requested-model"
			plans := &runnerPlanRepoStub{}
			repo := &protectionRepositoryStub{eligible: true}
			accounts := &modelIdentityAccountRepo{account: &Account{
				ID: *plan.AccountID, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive,
				Credentials: map[string]any{"api_key": "test", "base_url": "https://upstream.example.com"},
				Extra:       map[string]any{openai_compat.ExtraKeyResponsesSupported: true},
			}}
			upstream := &modelIdentityHTTPUpstream{
				body:      overload,
				responses: []*http.Response{{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(overload))}},
				beforeReturn: func(_ *http.Request, _ int) {
					require.Nil(t, repo.completed, "intermediate failures must not finish the result or apply actions")
					require.Empty(t, repo.verdict, "an incomplete response's mismatched model must not downgrade the account")
				},
			}
			if recover {
				upstream.body = `{"model":"requested-model","status":"completed","output":[]}`
			}
			svc := NewScheduledTestService(plans, repo)
			svc.SetDefinitionRepository(multiDefinitionRepoStub{definitions: map[int64]*ScheduledTestDefinition{
				1: {ID: 1, Enabled: true, OutputKind: "model_check"},
			}})
			accountTests := &AccountTestService{accountRepo: accounts, httpUpstream: upstream, cfg: &config.Config{}}
			runner := NewScheduledTestRunnerService(plans, svc, accountTests, accounts, nil, nil)
			runner.automaticRetryBaseDelay = time.Millisecond
			defer runner.Stop()
			runner.RunPlanNow(context.Background(), plan)
			require.Equal(t, []string{"create", "begin", "update", "complete"}, repo.events, "all attempts share one result and one final quality decision")
			require.Equal(t, repo.created.ID, repo.completed.ID)
			if recover {
				require.Equal(t, 2, upstream.requests)
				require.Equal(t, "success", repo.completed.Status)
				require.Equal(t, "pass", repo.completed.OutputModelCheck.Verdict)
				require.Equal(t, []string{"requested-model"}, repo.completed.OutputModelCheck.ReturnedModels)
				require.Equal(t, "pass", repo.verdict)
				require.Empty(t, repo.completed.ErrorMessage)
			} else {
				require.Equal(t, 4, upstream.requests)
				require.Equal(t, "failed", repo.completed.Status)
				require.Equal(t, "unknown", repo.completed.OutputModelCheck.Verdict)
				require.Equal(t, "upstream_error", repo.completed.OutputModelCheck.Reason)
				require.Equal(t, "fail", repo.verdict, "configured failure actions run only after all attempts fail")
				require.Contains(t, repo.completed.ErrorMessage, "overloaded")
			}
		})
	}
}

func TestScheduledTestModelCheckLegacyRecoveryRequiresMatch(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		recovers   int
	}{
		{"match", `{"model":"gpt-5.6-sol","status":"completed","output":[]}`, 1},
		{"mismatch", `{"model":"another-model","status":"completed","output":[]}`, 0},
		{"unknown", `{"status":"completed","output":[]}`, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plan := protectionPlan()
			plan.Protection.Enabled, plan.AutoRecover = false, true
			repo := &protectionRepositoryStub{eligible: true}
			account := &Account{ID: 42, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive,
				Credentials: map[string]any{"api_key": "test", "base_url": "https://upstream.example.com"},
				Extra:       map[string]any{openai_compat.ExtraKeyResponsesSupported: true}}
			accounts := &modelIdentityAccountRepo{account: account}
			calls := 0
			recoveryAccounts := scheduledTestExecutionAccountRepo{get: func(context.Context, int64) (*Account, error) {
				calls++
				return account, nil
			}}
			accountTests := &AccountTestService{accountRepo: accounts, httpUpstream: &modelIdentityHTTPUpstream{body: tc.body}, cfg: &config.Config{}}
			runner := NewScheduledTestRunnerService(nil, NewScheduledTestService(nil, repo), accountTests, accounts, nil, nil)
			runner.rateLimitSvc = &RateLimitService{accountRepo: recoveryAccounts}
			runner.runOneAccount(context.Background(), plan, 42, scheduledTestModelCheckPrompt, "model_check")
			require.Equal(t, tc.recovers, calls, "a successful HTTP exchange is not sufficient to recover an account")
		})
	}
}

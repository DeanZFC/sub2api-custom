package service

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

type scheduledTestAccountRepoStub struct {
	AccountRepository
	accounts []Account
}

type scheduledTestDefinitionRepoStub struct {
	ScheduledTestDefinitionRepository
	definition *ScheduledTestDefinition
}

func (r scheduledTestDefinitionRepoStub) GetByID(_ context.Context, _ int64) (*ScheduledTestDefinition, error) {
	return r.definition, nil
}

func (r scheduledTestAccountRepoStub) ListSchedulableByGroupID(_ context.Context, _ int64) ([]Account, error) {
	return r.accounts, nil
}

func TestCreateTestPayloadUsesConfiguredPrompt(t *testing.T) {
	payload, err := createTestPayload("claude-sonnet-4-6", "draw a pelican")
	if err != nil {
		t.Fatalf("createTestPayload() error = %v", err)
	}
	body, err := json.Marshal(payload)
	if err != nil || !strings.Contains(string(body), "draw a pelican") {
		t.Fatalf("custom prompt missing from payload: %s", body)
	}
}

func TestCreateOpenAITestPayloadUsesConfiguredPrompt(t *testing.T) {
	payload := createOpenAITestPayload("deepseek-v4-flash", false, "solve the candy problem")
	body, err := json.Marshal(payload)
	if err != nil || !strings.Contains(string(body), "solve the candy problem") {
		t.Fatalf("custom prompt missing from OpenAI payload: %s", body)
	}
}

func TestApplyAccountTestReasoningEffortUsesNativePayloadShape(t *testing.T) {
	responses := createOpenAITestPayload("gpt-6-astra", false)
	applyAccountTestReasoningEffort(responses, " ultra ")
	if got := responses["reasoning"].(map[string]any)["effort"]; got != "max" {
		t.Fatalf("responses reasoning effort = %v, want max", got)
	}
	chat := createOpenAIChatCompletionsTestPayload("gpt-6-astra", "hi")
	applyAccountTestReasoningEffort(chat, "high")
	if got := chat["reasoning_effort"]; got != "high" {
		t.Fatalf("chat reasoning effort = %v, want high", got)
	}
}

func TestScheduledTestReasoningEffortsFollowModelCapabilities(t *testing.T) {
	if got := ScheduledTestReasoningEfforts("gpt-6-astra"); len(got) == 0 || got[len(got)-1] != "ultra" {
		t.Fatalf("gpt-6-astra reasoning efforts = %v", got)
	}
	if got := ScheduledTestReasoningEfforts("gpt-4o"); len(got) != 0 {
		t.Fatalf("gpt-4o should not advertise reasoning efforts: %v", got)
	}
}

func TestAntigravityTestRequestsUseConfiguredPrompt(t *testing.T) {
	service := &AntigravityGatewayService{}
	claudeBody, err := service.buildClaudeTestRequest("project", "claude-sonnet-4-6", "draw a pelican")
	if err != nil {
		t.Fatalf("buildClaudeTestRequest() error = %v", err)
	}
	if !strings.Contains(string(claudeBody), "draw a pelican") {
		t.Fatalf("Claude request omitted configured prompt: %s", claudeBody)
	}
	geminiBody, err := service.buildGeminiTestRequest("project", "gemini-2.5-flash", "solve the candy problem")
	if err != nil {
		t.Fatalf("buildGeminiTestRequest() error = %v", err)
	}
	if !strings.Contains(string(geminiBody), "solve the candy problem") {
		t.Fatalf("Gemini request omitted configured prompt: %s", geminiBody)
	}
}

func TestScheduledTestRunnerResolvesEverySchedulableGroupAccount(t *testing.T) {
	runner := &ScheduledTestRunnerService{accountRepo: scheduledTestAccountRepoStub{accounts: []Account{{ID: 11}, {ID: 12}, {ID: 13}}}}
	groupID := int64(7)
	ids, err := runner.resolveTargetAccounts(context.Background(), &ScheduledTestPlan{GroupID: &groupID})
	if err != nil {
		t.Fatalf("resolveTargetAccounts() error = %v", err)
	}
	if want := []int64{11, 12, 13}; !reflect.DeepEqual(ids, want) {
		t.Fatalf("resolved account ids = %v, want %v", ids, want)
	}
}

func TestCreatePlanRejectsDisabledDefinition(t *testing.T) {
	definitionID := int64(4)
	plan := &ScheduledTestPlan{
		AccountID:        scheduledTestPtrInt64(1),
		TestDefinitionID: &definitionID,
		ModelID:          "model",
		CronExpression:   "*/5 * * * *",
		Enabled:          true,
	}
	service := NewScheduledTestService(nil, nil)
	service.SetDefinitionRepository(scheduledTestDefinitionRepoStub{definition: &ScheduledTestDefinition{ID: definitionID, Enabled: false}})
	if _, err := service.CreatePlan(context.Background(), plan); err == nil {
		t.Fatal("expected disabled definition to be rejected")
	}
}

func scheduledTestPtrInt64(v int64) *int64 { return &v }

func TestExtractScheduledTestHTMLRemovesMarkdownFence(t *testing.T) {
	got := extractScheduledTestHTML("```html\n<!doctype html><html><body><svg /></body></html>\n```")
	if !strings.HasPrefix(strings.ToLower(got), "<!doctype html>") {
		t.Fatalf("extractScheduledTestHTML() = %q", got)
	}
	if strings.Contains(got, "```") {
		t.Fatalf("markdown fence leaked into HTML result: %q", got)
	}
}

func TestExtractScheduledTestNumberPrefersFinalAnswer(t *testing.T) {
	text := "苹果圆形 7，桃子五角星 6\n其它数据 8\n最终答案：29"
	got, ok := extractScheduledTestNumber(text)
	if !ok || got != 29 {
		t.Fatalf("extractScheduledTestNumber() = (%v, %v), want (29, true)", got, ok)
	}
}

func TestScheduledTestRunnerApplyOutputContract(t *testing.T) {
	runner := &ScheduledTestRunnerService{}

	htmlResult := &ScheduledTestResult{Status: "success", ResponseText: "<svg><circle /></svg>"}
	runner.applyOutputContract(htmlResult, "html")
	if htmlResult.OutputHTML == "" || htmlResult.Status != "success" {
		t.Fatalf("HTML output contract = %#v", htmlResult)
	}

	numberResult := &ScheduledTestResult{Status: "success", ResponseText: "答案是 29"}
	runner.applyOutputContract(numberResult, "number")
	if numberResult.OutputNumeric == nil || *numberResult.OutputNumeric != 29 {
		t.Fatalf("numeric output contract = %#v", numberResult)
	}

	badResult := &ScheduledTestResult{Status: "success", ResponseText: "no structured answer"}
	runner.applyOutputContract(badResult, "number")
	if badResult.Status != "failed" || badResult.ErrorMessage == "" {
		t.Fatalf("invalid numeric output contract = %#v", badResult)
	}
}

func TestValidateScheduledTestPlanRequiresSourcesAndDefinitions(t *testing.T) {
	base := &ScheduledTestPlan{ModelID: "model", CronExpression: "*/5 * * * *"}
	if err := validateScheduledTestPlan(base); err == nil {
		t.Fatal("expected missing groups to be rejected")
	}
	base.GroupIDs = []int64{2, 3}
	if err := validateScheduledTestPlan(base); err == nil {
		t.Fatal("expected missing test definitions to be rejected")
	}
	base.TestDefinitionIDs = []int64{3}
	if err := validateScheduledTestPlan(base); err != nil {
		t.Fatal(err)
	}
	if base.TargetMode != "all_accounts" || base.AccountID != nil {
		t.Fatal("strategy must scan group union")
	}
}

func TestValidateScheduledTestDefinitionInputNormalizesAndValidates(t *testing.T) {
	d := &ScheduledTestDefinition{Key: "  Candy-Test ", Name: "Candy", Prompt: "answer", OutputKind: " NUMBER "}
	if err := ValidateScheduledTestDefinitionInput(d); err != nil {
		t.Fatalf("valid definition rejected: %v", err)
	}
	if d.Key != "candy-test" || d.OutputKind != "number" {
		t.Fatalf("definition was not normalized: %#v", d)
	}

	invalid := &ScheduledTestDefinition{Key: "bad key", Name: "Bad", Prompt: "x", OutputKind: "number"}
	if err := ValidateScheduledTestDefinitionInput(invalid); err == nil {
		t.Fatal("expected invalid definition key to be rejected")
	}
}

package service

import (
	"context"
	"errors"
	"testing"
	"time"
)

type runnerPlanRepoStub struct {
	updated int
}

func (r *runnerPlanRepoStub) Create(context.Context, *ScheduledTestPlan) (*ScheduledTestPlan, error) {
	return nil, errors.New("not implemented")
}
func (r *runnerPlanRepoStub) GetByID(context.Context, int64) (*ScheduledTestPlan, error) {
	return nil, errors.New("not implemented")
}
func (r *runnerPlanRepoStub) ListByAccountID(context.Context, int64) ([]*ScheduledTestPlan, error) {
	return nil, errors.New("not implemented")
}
func (r *runnerPlanRepoStub) List(context.Context) ([]*ScheduledTestPlan, error) {
	return nil, errors.New("not implemented")
}
func (r *runnerPlanRepoStub) ListDue(context.Context, time.Time) ([]*ScheduledTestPlan, error) {
	return nil, errors.New("not implemented")
}
func (r *runnerPlanRepoStub) Update(context.Context, *ScheduledTestPlan) (*ScheduledTestPlan, error) {
	return nil, errors.New("not implemented")
}
func (r *runnerPlanRepoStub) Delete(context.Context, int64) error {
	return errors.New("not implemented")
}
func (r *runnerPlanRepoStub) UpdateAfterRun(ctx context.Context, _ int64, _, _ time.Time) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	r.updated++
	return nil
}

type runnerResultRepoStub struct {
	created         []*ScheduledTestResult
	createdStatuses []string
	createdEfforts  []string
	updatedIDs      []int64
}

func (r *runnerResultRepoStub) GetByID(context.Context, int64) (*ScheduledTestResult, error) {
	return nil, errors.New("not implemented")
}

func (r *runnerResultRepoStub) RestartFailed(context.Context, *ScheduledTestResult) error {
	return errors.New("unexpected retry during normal plan run")
}

func (r *runnerResultRepoStub) Create(ctx context.Context, result *ScheduledTestResult) (*ScheduledTestResult, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	copy := *result
	if copy.ID <= 0 {
		copy.ID = int64(len(r.created) + 1)
	}
	r.created = append(r.created, &copy)
	r.createdStatuses = append(r.createdStatuses, copy.Status)
	r.createdEfforts = append(r.createdEfforts, copy.ReasoningEffort)
	return &copy, nil
}
func (r *runnerResultRepoStub) Update(ctx context.Context, result *ScheduledTestResult) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	r.updatedIDs = append(r.updatedIDs, result.ID)
	for _, existing := range r.created {
		if existing.ID == result.ID {
			*existing = *result
			return nil
		}
	}
	return nil
}
func (r *runnerResultRepoStub) ListByPlanID(context.Context, int64, int) ([]*ScheduledTestResult, error) {
	return nil, nil
}
func (r *runnerResultRepoStub) ListVisible(context.Context, int64, int) ([]*ScheduledTestResult, error) {
	return nil, nil
}
func (r *runnerResultRepoStub) Delete(context.Context, int64) error { return nil }
func (r *runnerResultRepoStub) PruneOldResults(ctx context.Context, _ int64, _ int) error {
	return ctx.Err()
}

func TestExtractScheduledTestHTMLRejectsPlainTextAndDropsTrailingExplanation(t *testing.T) {
	if got := extractScheduledTestHTML("Here is the requested animation."); got != "" {
		t.Fatalf("plain text was accepted as HTML: %q", got)
	}
	got := extractScheduledTestHTML("Here is the document:\n```html\n<!doctype html><html><body><p>ok</p></body></html>\n```\nI hope this helps.")
	want := "<!doctype html><html><body><p>ok</p></body></html>"
	if got != want {
		t.Fatalf("HTML extraction = %q, want %q", got, want)
	}
}

func TestExtractScheduledTestHTMLHandlesSVGAndQuotedAttributes(t *testing.T) {
	got := extractScheduledTestHTML("<svg viewBox=\"0 0 10 > 10\"><svg><circle /></svg></svg> trailing notes")
	want := "<svg viewBox=\"0 0 10 > 10\"><svg><circle /></svg></svg>"
	if got != want {
		t.Fatalf("SVG extraction = %q, want %q", got, want)
	}
	if got := extractScheduledTestHTML("```\n<svg />\n```"); got != "<svg />" {
		t.Fatalf("fenced SVG extraction = %q", got)
	}
}

func TestExtractScheduledTestNumberSupportsFinalMarkersAndNumericForms(t *testing.T) {
	cases := []struct {
		name string
		text string
		want float64
	}{
		{name: "last final answer", text: "candidate answer: 28\nfinal answer: 29", want: 29},
		{name: "negative decimal", text: "答案 = -0.25", want: -0.25},
		{name: "scientific", text: "final result: 1.25e-3", want: 0.00125},
		{name: "standalone", text: "29", want: 29},
		{name: "chinese markdown answer", text: "最少取出 **29个**。\n\n若还没有满足条件，则不可能同时出现：\n1. A 和 B，因此最多 7\n2. C 和 D，因此最多 9", want: 29},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := extractScheduledTestNumber(tc.text)
			if !ok || got != tc.want {
				t.Fatalf("extractScheduledTestNumber(%q) = (%v, %v), want (%v, true)", tc.text, got, ok, tc.want)
			}
		})
	}
}

func TestExtractScheduledTestNumberDoesNotUseStepNumberWhenAnswerIsFormatted(t *testing.T) {
	text := "最少取出 **29个**。\n\n若还没有满足条件，则每种至少 1 个。\n1. 第一种情况最多取出 7 个。\n2. 第二种情况最多取出 9 个。"
	got, ok := extractScheduledTestNumber(text)
	if !ok || got != 29 {
		t.Fatalf("extractScheduledTestNumber() = (%v, %v), want (29, true)", got, ok)
	}
}

func TestExtractScheduledTestNumberRejectsUnmarkedReasoningNumbers(t *testing.T) {
	for _, text := range []string{
		"step 1: inspect the input\nrow 2 contains 7 values",
		"The table has 7 apples and 6 peaches.",
		"No numeric answer was provided.",
	} {
		if got, ok := extractScheduledTestNumber(text); ok {
			t.Fatalf("unmarked reasoning text %q produced (%v, true)", text, got)
		}
	}
}

func TestScheduledTestRunnerPlanRunGuardPreventsOverlap(t *testing.T) {
	runner := &ScheduledTestRunnerService{}
	if !runner.beginPlanRun(42) {
		t.Fatal("first plan run was rejected")
	}
	if runner.beginPlanRun(42) {
		t.Fatal("overlapping plan run was accepted")
	}
	runner.endPlanRun(42)
	if !runner.beginPlanRun(42) {
		t.Fatal("plan did not become runnable after completion")
	}
	runner.endPlanRun(42)
}

func TestScheduledTestRunnerUsesGlobalWorkerLimit(t *testing.T) {
	runner := &ScheduledTestRunnerService{}
	ctx := context.Background()
	for i := 0; i < scheduledTestDefaultMaxWorkers; i++ {
		if !runner.acquireWorker(ctx) {
			t.Fatalf("worker %d was not acquired", i)
		}
	}
	blockedCtx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if runner.acquireWorker(blockedCtx) {
		t.Fatal("worker beyond global limit was acquired")
	}
	runner.releaseWorker()
	if !runner.acquireWorker(context.Background()) {
		t.Fatal("worker was not available after release")
	}
	for i := 0; i < scheduledTestDefaultMaxWorkers; i++ {
		runner.releaseWorker()
	}
}

func TestScheduledTestRunnerPersistsFailureAndAdvancesAfterCancellation(t *testing.T) {
	planRepo := &runnerPlanRepoStub{}
	resultRepo := &runnerResultRepoStub{}
	scheduled := NewScheduledTestService(planRepo, resultRepo)
	runner := &ScheduledTestRunnerService{
		planRepo:     planRepo,
		scheduledSvc: scheduled,
	}
	accountID := int64(17)
	plan := &ScheduledTestPlan{ID: 9, AccountID: &accountID, ModelID: "model", ReasoningEffort: "high", CronExpression: "*/5 * * * *", MaxResults: 3}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	runner.runOnePlan(ctx, plan)
	if planRepo.updated != 1 {
		t.Fatalf("UpdateAfterRun count = %d, want 1", planRepo.updated)
	}
	if len(resultRepo.created) != 1 {
		t.Fatalf("saved result count = %d, want 1", len(resultRepo.created))
	}
	if len(resultRepo.createdStatuses) != 1 || resultRepo.createdStatuses[0] != "running" {
		t.Fatalf("created result statuses = %v, want [running]", resultRepo.createdStatuses)
	}
	if len(resultRepo.updatedIDs) != 1 || resultRepo.updatedIDs[0] != resultRepo.created[0].ID {
		t.Fatalf("updated result ids = %v, want the created result id %d", resultRepo.updatedIDs, resultRepo.created[0].ID)
	}
	if got := resultRepo.created[0]; got.Status != "failed" || got.ErrorMessage == "" {
		t.Fatalf("cancellation result = %#v, want failed result with error", got)
	}
	if got := resultRepo.created[0].ReasoningEffort; got != "high" || resultRepo.createdEfforts[0] != "high" {
		t.Fatalf("cancellation reasoning effort = %q, initially %q, want high", got, resultRepo.createdEfforts[0])
	}
}

func TestScheduledTestRunnerSavesOneResultPerGroupAccount(t *testing.T) {
	planRepo := &runnerPlanRepoStub{}
	resultRepo := &runnerResultRepoStub{}
	scheduled := NewScheduledTestService(planRepo, resultRepo)
	groupID := int64(8)
	runner := &ScheduledTestRunnerService{
		planRepo:       planRepo,
		scheduledSvc:   scheduled,
		accountRepo:    scheduledTestAccountRepoStub{accounts: []Account{{ID: 21}, {ID: 22}}},
		accountTestSvc: nil, // each account records an independent failure result
	}
	plan := &ScheduledTestPlan{ID: 10, GroupID: &groupID, ModelID: "model", ReasoningEffort: "ultra", CronExpression: "*/5 * * * *", MaxResults: 5}
	runner.runOnePlan(context.Background(), plan)
	if planRepo.updated != 1 {
		t.Fatalf("UpdateAfterRun count = %d, want 1", planRepo.updated)
	}
	if len(resultRepo.created) != 2 {
		t.Fatalf("saved group result count = %d, want 2", len(resultRepo.created))
	}
	if len(resultRepo.createdStatuses) != 2 || resultRepo.createdStatuses[0] != "running" || resultRepo.createdStatuses[1] != "running" {
		t.Fatalf("created group result statuses = %v, want [running running]", resultRepo.createdStatuses)
	}
	if len(resultRepo.updatedIDs) != 2 {
		t.Fatalf("updated group result ids = %v, want 2 updates", resultRepo.updatedIDs)
	}
	seen := map[int64]bool{}
	for _, result := range resultRepo.created {
		if result.AccountID == nil {
			t.Fatalf("group result has no account id: %#v", result)
		}
		if result.ReasoningEffort != "ultra" {
			t.Fatalf("group result reasoning effort = %q, want ultra", result.ReasoningEffort)
		}
		seen[*result.AccountID] = true
	}
	if !seen[21] || !seen[22] {
		t.Fatalf("group account results = %v, want accounts 21 and 22", seen)
	}
	for _, effort := range resultRepo.createdEfforts {
		if effort != "ultra" {
			t.Fatalf("running group result reasoning effort = %q, want ultra", effort)
		}
	}
}

func TestScheduledTestRunnerPlanFailureSnapshotsReasoningEffort(t *testing.T) {
	resultRepo := &runnerResultRepoStub{}
	runner := &ScheduledTestRunnerService{scheduledSvc: NewScheduledTestService(nil, resultRepo)}
	plan := &ScheduledTestPlan{ID: 11, ModelID: "gpt-6-astra", ReasoningEffort: "xhigh", MaxResults: 5}
	runner.savePlanFailure(context.Background(), plan, "text", errors.New("no available accounts"))
	plan.ReasoningEffort = "low"
	if len(resultRepo.created) != 1 {
		t.Fatalf("saved result count = %d, want 1", len(resultRepo.created))
	}
	if got := resultRepo.created[0]; got.ReasoningEffort != "xhigh" || got.Status != "failed" {
		t.Fatalf("plan failure result = %#v, want failed result with xhigh effort", got)
	}
}

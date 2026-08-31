package main

import (
	"encoding/json"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

func resetTestState(t *testing.T) {
	t.Helper()
	state.mu.Lock()
	defer state.mu.Unlock()
	state.cfg = config{Enabled: true, DefaultCapacity: 1, RejectWhenFull: true, StateFile: filepath.Join(t.TempDir(), "state.json")}
	state.accounts = make(map[string]accountConfig)
	state.usage = make(map[string]usageStats)
	state.active = make(map[string]int)
	state.pending = nil
	state.requests = make(map[string]string)
	state.pricing = make(map[string]modelPrice)
	state.pricingAt = time.Time{}
	state.pricingSrc = ""
	state.usageByModel = make(map[string]map[string]tokenUsage)
	state.stateLoaded = true
}

func schedulerForTest(t *testing.T, candidates ...pluginapi.SchedulerAuthCandidate) (pluginapi.SchedulerPickResponse, error) {
	t.Helper()
	raw, err := json.Marshal(pluginapi.SchedulerPickRequest{Provider: "codex", Providers: []string{"codex"}, Candidates: candidates})
	if err != nil {
		return pluginapi.SchedulerPickResponse{}, err
	}
	responseRaw, err := schedulerPick(raw)
	if err != nil {
		return pluginapi.SchedulerPickResponse{}, err
	}
	var env envelope
	if err := json.Unmarshal(responseRaw, &env); err != nil {
		return pluginapi.SchedulerPickResponse{}, err
	}
	var response pluginapi.SchedulerPickResponse
	if err := json.Unmarshal(env.Result, &response); err != nil {
		return pluginapi.SchedulerPickResponse{}, err
	}
	return response, nil
}

func TestSchedulerRespectsPerAccountCapacity(t *testing.T) {
	resetTestState(t)
	first, err := schedulerForTest(t, pluginapi.SchedulerAuthCandidate{ID: "a", Provider: "codex"}, pluginapi.SchedulerAuthCandidate{ID: "b", Provider: "codex"})
	if err != nil || first.AuthID != "a" {
		t.Fatalf("first pick = %#v, err=%v", first, err)
	}
	second, err := schedulerForTest(t, pluginapi.SchedulerAuthCandidate{ID: "a", Provider: "codex"}, pluginapi.SchedulerAuthCandidate{ID: "b", Provider: "codex"})
	if err != nil || second.AuthID != "b" {
		t.Fatalf("second pick = %#v, err=%v", second, err)
	}
}

func TestDisabledAccountIsSkipped(t *testing.T) {
	resetTestState(t)
	state.mu.Lock()
	state.accounts["a"] = accountConfig{Capacity: 2, Enabled: false}
	state.mu.Unlock()
	response, err := schedulerForTest(t, pluginapi.SchedulerAuthCandidate{ID: "a", Provider: "codex"}, pluginapi.SchedulerAuthCandidate{ID: "b", Provider: "codex"})
	if err != nil || response.AuthID != "b" {
		t.Fatalf("pick = %#v, err=%v", response, err)
	}
}

func TestCompletionReleasesExactlyOnce(t *testing.T) {
	resetTestState(t)
	response, err := schedulerForTest(t, pluginapi.SchedulerAuthCandidate{ID: "a", Provider: "codex"})
	if err != nil || response.AuthID != "a" {
		t.Fatalf("pick = %#v, err=%v", response, err)
	}
	afterRaw, err := json.Marshal(pluginapi.RequestInterceptRequest{RequestID: "req-1", Metadata: map[string]any{"selected_auth_id": "a"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := requestIntercept("request.intercept_after", afterRaw); err != nil {
		t.Fatal(err)
	}
	completionRaw, err := json.Marshal(pluginapi.RequestCompletion{RequestID: "req-1", Metadata: map[string]any{"selected_auth_id": "a"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := requestComplete(completionRaw); err != nil {
		t.Fatal(err)
	}
	if _, err := requestComplete(completionRaw); err != nil {
		t.Fatal(err)
	}
	state.mu.Lock()
	active := state.active["a"]
	state.mu.Unlock()
	if active != 0 {
		t.Fatalf("active = %d, want 0", active)
	}
}

func TestUsageAccumulatesAndPersistsAtomically(t *testing.T) {
	resetTestState(t)
	recordRaw, err := json.Marshal(pluginapi.UsageRecord{AuthID: "a", Failed: false, Detail: pluginapi.UsageDetail{InputTokens: 10, OutputTokens: 5, TotalTokens: 15}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := usageHandle(recordRaw); err != nil {
		t.Fatal(err)
	}
	state.mu.Lock()
	got := state.usage["a"]
	path := state.cfg.StateFile
	state.mu.Unlock()
	if got.Requests != 1 || got.TotalTokens != 15 || got.Success != 1 {
		t.Fatalf("usage = %#v", got)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("state file not written: %v", err)
	}
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Fatalf("temporary state file remains: %v", err)
	}
}

func TestKeeperPricingChargesUncachedAndCacheSegments(t *testing.T) {
	resetTestState(t)
	state.mu.Lock()
	state.pricing = map[string]modelPrice{
		"claude-4": {Input: 3, Output: 15, CacheRead: 0.3, CacheWrite: 3.75},
	}
	state.mu.Unlock()

	recordRaw, err := json.Marshal(pluginapi.UsageRecord{
		AuthID: "a", Model: "anthropic/claude-4", Alias: "alias",
		Detail: pluginapi.UsageDetail{InputTokens: 1_000_000, OutputTokens: 500_000, CacheReadTokens: 200_000, CacheCreationTokens: 100_000},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := usageHandle(recordRaw); err != nil {
		t.Fatal(err)
	}
	state.mu.Lock()
	got := state.usage["a"].CostUSD
	state.mu.Unlock()
	want := 0.7*3 + 0.2*0.3 + 0.1*3.75 + 0.5*15
	if math.Abs(got-want) > 1e-12 {
		t.Fatalf("cost = %.12f, want %.12f", got, want)
	}
}

func TestCompilePricingCatalogPrefersOfficialProviderAndSupportsPrefix(t *testing.T) {
	input := 3.0
	output := 15.0
	zero := 0.0
	catalog := map[string]pricingProvider{
		"coding-plan": {ID: "gpt-coding-plan", Models: map[string]pricingModel{
			"gpt-5": {ID: "gpt-5", Cost: pricingCost{Input: &zero, Output: &zero}},
		}},
		"openai": {ID: "openai", Models: map[string]pricingModel{
			"gpt-5": {ID: "gpt-5", Cost: pricingCost{Input: &input, Output: &output}},
		}},
	}
	prices := compilePricingCatalog(catalog)
	price, ok := lookupPrice(prices, "openai/gpt-5")
	if !ok || price.Input != input || price.Output != output {
		t.Fatalf("price = %#v, ok=%v", price, ok)
	}
}

func TestMissingKeeperPricingHasZeroCost(t *testing.T) {
	resetTestState(t)
	recordRaw, err := json.Marshal(pluginapi.UsageRecord{AuthID: "a", Model: "unknown", Detail: pluginapi.UsageDetail{InputTokens: 100}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := usageHandle(recordRaw); err != nil {
		t.Fatal(err)
	}
	state.mu.Lock()
	got := state.usage["a"].CostUSD
	state.mu.Unlock()
	if got != 0 {
		t.Fatalf("cost = %v, want 0", got)
	}
}

func TestFullPoolReturns429WhenConfigured(t *testing.T) {
	resetTestState(t)
	if _, err := schedulerForTest(t, pluginapi.SchedulerAuthCandidate{ID: "a", Provider: "codex"}); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(pluginapi.SchedulerPickRequest{Provider: "codex", Providers: []string{"codex"}, Candidates: []pluginapi.SchedulerAuthCandidate{{ID: "a", Provider: "codex"}}})
	if err != nil {
		t.Fatal(err)
	}
	result, err := schedulerPick(raw)
	if err != nil {
		t.Fatal(err)
	}
	var env envelope
	if err := json.Unmarshal(result, &env); err != nil {
		t.Fatal(err)
	}
	if env.OK || env.Error == nil || env.Error.HTTPStatus != http.StatusTooManyRequests {
		t.Fatalf("envelope = %#v", env)
	}
}

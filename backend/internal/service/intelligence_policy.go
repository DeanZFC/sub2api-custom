package service

import (
	"context"
	"fmt"
	"time"
)

// The policy is embedded in the existing plan JSON. An absent policy retains the legacy execution contract.
type IntelligencePolicy struct {
	ModelDefaultEffort     bool   `json:"model_default_effort,omitempty"`
	Version                string `json:"version,omitempty"`
	CandyPrompt            string `json:"candy_prompt,omitempty"`
	CandyMaxOutputTokens   int    `json:"candy_max_output_tokens,omitempty"`
	ArtworkPrompt          string `json:"artwork_prompt,omitempty"`
	ArtworkMaxOutputTokens int    `json:"artwork_max_output_tokens,omitempty"`
	PauseOnAny             bool   `json:"pause_on_any,omitempty"`
	AutoIsolate            bool   `json:"auto_isolate"`
	IntervalMinutes        int    `json:"interval_minutes"`
	CooldownMinutes        int    `json:"cooldown_minutes"`
	RecoveryPasses         int    `json:"recovery_passes"`
}
type IntelligenceTarget struct {
	AccountID int64
	Model     string
}
type IntelligenceRetestRepository interface {
	DueIntelligenceTargets(context.Context, int64, time.Time) ([]IntelligenceTarget, error)
}

// The standard preset owns the definition and scheduling defaults so API clients need only select groups and models.
func (s *ScheduledTestService) prepareIntelligencePolicy(ctx context.Context, p *ScheduledTestPlan) error {
	if p == nil || p.Protection.Intelligence == nil {
		return nil
	}
	cfg := p.Protection.Intelligence
	if cfg.IntervalMinutes == 0 {
		cfg.IntervalMinutes = 60
	}
	if cfg.CooldownMinutes == 0 {
		cfg.CooldownMinutes = 30
	}
	if cfg.RecoveryPasses == 0 {
		cfg.RecoveryPasses = 2
	}
	if cfg.IntervalMinutes < 1 || cfg.IntervalMinutes > 1440 || cfg.CooldownMinutes < 1 || cfg.CooldownMinutes > 1440 || cfg.RecoveryPasses < 1 || cfg.RecoveryPasses > 10 {
		return fmt.Errorf("巡检和隔离复测间隔须为 1–1440 分钟，恢复轮数须为 1–10")
	}
	if s.definitionRepo == nil {
		return fmt.Errorf("检测题库不可用")
	}
	art, err := s.definitionRepo.GetByKey(ctx, "pelican")
	if err != nil || art == nil || (p.Enabled && !art.Enabled) || art.OutputKind != "html" {
		return fmt.Errorf("请启用内置鹈鹕 HTML 检测类型")
	}
	candy, err := s.definitionRepo.GetByKey(ctx, "candy")
	if err != nil || candy == nil || (p.Enabled && !candy.Enabled) || candy.OutputKind != "number" {
		return fmt.Errorf("请启用内置糖果数字检测类型")
	}
	cfg.CandyPrompt = candy.Prompt
	cfg.CandyMaxOutputTokens = candy.MaxOutputTokens
	cfg.ArtworkPrompt = art.Prompt
	cfg.ArtworkMaxOutputTokens = art.MaxOutputTokens
	p.TestDefinitionIDs = []int64{art.ID}
	p.Protection = ScheduledTestProtectionConfig{Intelligence: cfg}
	p.AutoRecover = false
	cfg.ModelDefaultEffort = p.ReasoningEffort == "default" || (p.ReasoningEffort == "" && cfg.ModelDefaultEffort)
	if cfg.ModelDefaultEffort {
		p.ReasoningEffort = ""
	} else if p.ReasoningEffort == "" {
		p.ReasoningEffort = "high"
	}
	if p.Name == "" {
		p.Name = "智商监测"
	}
	p.Schedule = &ScheduledTestSchedule{Timezone: "UTC", Periods: []ScheduledTestSchedulePeriod{{Start: "00:00", End: "24:00", IntervalMinutes: cfg.IntervalMinutes}}}
	p.CronExpression = "0 * * * *"
	p.MaxResults = max(p.MaxResults, 50)
	return nil
}

type IntelligenceAdminResult struct {
	ErrorMessage     string                `json:"error_message,omitempty"`
	Evidence         *IntelligenceEvidence `json:"evidence,omitempty"`
	ReviewReason     string                `json:"review_reason,omitempty"`
	ResultID         int64                 `json:"result_id"`
	AccountID        int64                 `json:"account_id"`
	AccountName      string                `json:"account_name"`
	Model            string                `json:"model"`
	Verdict          string                `json:"verdict"`
	Artwork          string                `json:"artwork"`
	RetryableArtwork bool                  `json:"retryable_artwork"`
	Reviewed         string                `json:"reviewed"`
	FinishedAt       time.Time             `json:"finished_at"`
}

// IntelligenceHoldRound identifies the most recent round that established or
// renewed an active isolation. Subsequent recovery checks do not replace it.
type IntelligenceHoldRound struct {
	ResultID     int64               `json:"result_id"`
	FinishedAt   time.Time           `json:"finished_at"`
	Checks       []IntelligenceCheck `json:"checks"`
	ArtworkVeto  bool                `json:"artwork_veto"`
	ReviewReason string              `json:"review_reason,omitempty"`
}
type IntelligenceAdminState struct {
	HoldRound   *IntelligenceHoldRound `json:"hold_round,omitempty"`
	AccountID   int64                  `json:"account_id"`
	AccountName string                 `json:"account_name"`
	Model       string                 `json:"model"`
	Blocked     bool                   `json:"blocked"`
	Passes      int                    `json:"passes"`
	Verdict     string                 `json:"verdict"`
	NextCheck   *time.Time             `json:"next_check,omitempty"`
}
type IntelligenceAdminOverview struct {
	Results []IntelligenceAdminResult `json:"results"`
	States  []IntelligenceAdminState  `json:"states"`
}
type intelligenceAdminRepository interface {
	ReleaseIntelligenceHold(context.Context, int64, int64, string, int64) error
	IntelligenceOverview(context.Context, int64) (*IntelligenceAdminOverview, error)
	IntelligenceArtwork(context.Context, int64) (string, error)
	ReviewIntelligenceArt(context.Context, int64, int64, string, string) error
}

func (s *ScheduledTestService) IntelligenceOverview(ctx context.Context, plan int64) (*IntelligenceAdminOverview, error) {
	r, ok := s.resultRepo.(intelligenceAdminRepository)
	if !ok {
		return nil, fmt.Errorf("intelligence repository unavailable")
	}
	return r.IntelligenceOverview(ctx, plan)
}
func (s *ScheduledTestService) IntelligenceArtwork(ctx context.Context, result int64) (string, error) {
	r, ok := s.resultRepo.(intelligenceAdminRepository)
	if !ok {
		return "", fmt.Errorf("intelligence repository unavailable")
	}
	return r.IntelligenceArtwork(ctx, result)
}
func (s *ScheduledTestService) ReviewIntelligenceArt(ctx context.Context, result, admin int64, verdict, reason string) error {
	r, ok := s.resultRepo.(intelligenceAdminRepository)
	if !ok {
		return fmt.Errorf("intelligence repository unavailable")
	}
	return r.ReviewIntelligenceArt(ctx, result, admin, verdict, reason)
}

func (s *ScheduledTestService) ReleaseIntelligenceHold(ctx context.Context, plan, account int64, model string, admin int64) error {
	r, ok := s.resultRepo.(intelligenceAdminRepository)
	if !ok {
		return fmt.Errorf("intelligence repository unavailable")
	}
	return r.ReleaseIntelligenceHold(ctx, plan, account, model, admin)
}

// Quality isolation still permits a targeted check. Separately disabled or
// expired accounts should never acquire an upstream monitoring request.
func (a *Account) IntelligenceMonitoringEligible(now time.Time) bool {
	return a != nil && a.Platform == PlatformOpenAI && a.Schedulable &&
		a.SupportsOpenAIEndpointCapability(OpenAIEndpointCapabilityChatCompletions) &&
		(a.Status == StatusActive || a.Status == StatusQualityPaused) &&
		(!a.AutoPauseOnExpired || a.ExpiresAt == nil || now.Before(*a.ExpiresAt))
}

// SupportsIntelligenceModel checks the upstream target that would receive the
// text probe, including account aliases and native passthrough behavior.
func (a *Account) SupportsIntelligenceModel(model string) bool {
	if a == nil || !intelligenceTextModel(model) || !a.IsModelSupported(model) {
		return false
	}
	return intelligenceTextModel(resolveOpenAIAccountUpstreamModelForRequest(a, model, false))
}

func intelligenceTextModel(model string) bool {
	id := codexProviderQualifiedModelID(model)
	return id != "" && !isCodexDedicatedMediaModel(model) && !isGeminiCompatibleImageModel(id)
}

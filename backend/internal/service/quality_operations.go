package service

import (
	"context"
	"errors"
	"fmt"
	"time"
)

var ErrQualityOperationsConflict = errors.New("quality operations configuration changed; refresh and retry")

// QualityOperationsConfig applies to active tests, plan runs, and retries.
type QualityOperationsConfig struct {
	PelicanReview        *ScheduledTestReviewSettings `json:"pelican_review,omitempty"`
	Version              int64                        `json:"version"`
	BudgetEnabled        bool                         `json:"budget_enabled"`
	DailyRequests        int64                        `json:"daily_requests"`
	DailyTokens          int64                        `json:"daily_tokens"`
	AccountDailyRequests int64                        `json:"account_daily_requests"`
	AccountDailyTokens   int64                        `json:"account_daily_tokens"`
	TokenReservation     int64                        `json:"token_reservation"`
	DynamicEnabled       bool                         `json:"dynamic_enabled"`
	HealthyRounds        int                          `json:"healthy_rounds"`
	HealthyBaseSeconds   int                          `json:"healthy_base_seconds"`
	HealthyMaxSeconds    int                          `json:"healthy_max_seconds"`
}

func DefaultQualityOperationsConfig() QualityOperationsConfig {
	return QualityOperationsConfig{Version: 1, DailyRequests: 10000, DailyTokens: 10000000, AccountDailyRequests: 200, AccountDailyTokens: 1000000, TokenReservation: 32768, HealthyRounds: 3, HealthyBaseSeconds: 900, HealthyMaxSeconds: 14400}
}
func (c QualityOperationsConfig) Validate() error {
	if err := c.PelicanReview.Validate(); err != nil {
		return err
	}
	if c.Version < 1 || c.DailyRequests < 0 || c.DailyRequests > 10000000 || c.DailyTokens < 0 || c.DailyTokens > 1000000000000 || c.AccountDailyRequests < 0 || c.AccountDailyRequests > 10000000 || c.AccountDailyTokens < 0 || c.AccountDailyTokens > 1000000000000 {
		return fmt.Errorf("invalid budget limits (0 means unlimited)")
	}
	if c.TokenReservation < 1024 || c.TokenReservation > 10000000 {
		return fmt.Errorf("token reservation must be 1024..10000000")
	}
	if c.HealthyRounds < 1 || c.HealthyRounds > 100 || c.HealthyBaseSeconds < 60 || c.HealthyMaxSeconds < c.HealthyBaseSeconds || c.HealthyMaxSeconds > 86400 {
		return fmt.Errorf("healthy frequency: rounds 1..100, base 60..max, maximum 86400 seconds")
	}
	return nil
}
func (c QualityOperationsConfig) HealthyDelay(streak int) time.Duration {
	if !c.DynamicEnabled || streak < c.HealthyRounds {
		return 0
	}
	delay := c.HealthyBaseSeconds
	for i := c.HealthyRounds; i < streak && delay < c.HealthyMaxSeconds; i++ {
		delay *= 2
	}
	if delay > c.HealthyMaxSeconds {
		delay = c.HealthyMaxSeconds
	}
	return time.Duration(delay) * time.Second
}

type QualityBudgetUsage struct {
	AccountID       int64 `json:"account_id"`
	Requests        int64 `json:"requests"`
	Tokens          int64 `json:"tokens"`
	UnknownRequests int64 `json:"unknown_requests"`
}

type QualityOperationsSnapshot struct {
	Config   QualityOperationsConfig `json:"config"`
	Day      string                  `json:"day"`
	ResetsAt time.Time               `json:"resets_at"`
	Usage    []QualityBudgetUsage    `json:"usage"`
}
type QualityProbeReservation struct {
	ID        int64
	AccountID int64
}
type QualityProbeOutcome struct {
	Tokens *int64
	Kind   string
	At     time.Time
}
type QualityDeferredError struct {
	Reason string
	Until  time.Time
}

func (e *QualityDeferredError) Error() string { return "检测跳过：" + e.Reason }

type QualityOperationsRepository interface {
	OperationsSnapshot(context.Context) (*QualityOperationsSnapshot, error)
	SaveOperationsConfig(context.Context, *QualityOperationsConfig) error
	ReserveQualityProbe(context.Context, int64, int64, bool, string) (*QualityProbeReservation, error)
	SettleQualityProbe(context.Context, *QualityProbeReservation, QualityProbeOutcome) error
	QualityFrequencyDue(context.Context, int64, int64, string) (*time.Time, error)
	RecordQualityFrequency(context.Context, *ScheduledTestPlan, *ScheduledTestResult, string, bool) error
}

// Run-local identity is shared by requests in one active test execution.
type qualityRunKey struct{}

func QualityResultDeferred(r *ScheduledTestResult) bool {
	return r != nil && r.ExecutionInfo != nil && r.ExecutionInfo.FailureKind == "quality_deferred"
}

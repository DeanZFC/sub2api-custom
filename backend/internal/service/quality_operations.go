package service

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// QualityOperationsConfig applies to quality-management tests (including manual
// plan runs, retries and recovery probes), not customer inference traffic.
type QualityOperationsConfig struct {
	Version              int64 `json:"version"`
	BudgetEnabled        bool  `json:"budget_enabled"`
	DailyRequests        int64 `json:"daily_requests"`
	DailyTokens          int64 `json:"daily_tokens"`
	AccountDailyRequests int64 `json:"account_daily_requests"`
	AccountDailyTokens   int64 `json:"account_daily_tokens"`
	TokenReservation     int64 `json:"token_reservation"`
	DynamicEnabled       bool  `json:"dynamic_enabled"`
	HealthyRounds        int   `json:"healthy_rounds"`
	HealthyBaseSeconds   int   `json:"healthy_base_seconds"`
	HealthyMaxSeconds    int   `json:"healthy_max_seconds"`
}

func DefaultQualityOperationsConfig() QualityOperationsConfig {
	return QualityOperationsConfig{Version: 1, DailyRequests: 10000, DailyTokens: 10000000, AccountDailyRequests: 200, AccountDailyTokens: 1000000, TokenReservation: 32768, HealthyRounds: 3, HealthyBaseSeconds: 900, HealthyMaxSeconds: 14400}
}
func (c QualityOperationsConfig) Validate() error {
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
type QualitySupplier struct {
	ID                   int64                   `json:"id"`
	Version              int64                   `json:"version"`
	Name                 string                  `json:"name"`
	AccountIDs           []int64                 `json:"account_ids"`
	Enabled              bool                    `json:"enabled"`
	WindowSeconds        int                     `json:"window_seconds"`
	MinAccounts          int                     `json:"min_accounts"`
	FailurePercent       int                     `json:"failure_percent"`
	ProbeIntervalSeconds int                     `json:"probe_interval_seconds"`
	Members              []QualitySupplierMember `json:"members"`
	Affected             int                     `json:"affected"`
	Observed             int                     `json:"observed"`
	Incident             bool                    `json:"incident"`
	NextProbeAt          *time.Time              `json:"next_probe_at"`
}
type QualitySupplierMember struct {
	AccountID  int64      `json:"account_id"`
	Name       string     `json:"name"`
	Kind       string     `json:"kind"`
	ObservedAt *time.Time `json:"observed_at"`
}

func (s *QualitySupplier) Validate() error {
	s.Name = strings.TrimSpace(s.Name)
	if s.Name == "" || len([]rune(s.Name)) > 100 || s.WindowSeconds < 60 || s.WindowSeconds > 86400 || s.MinAccounts < 2 || s.MinAccounts > 1000 || s.FailurePercent < 1 || s.FailurePercent > 100 || s.ProbeIntervalSeconds < 15 || s.ProbeIntervalSeconds > 3600 || len(s.AccountIDs) > 1000 {
		return fmt.Errorf("invalid supplier: name required, window 60..86400s, minimum accounts 2..1000, failures 1..100%%, probe interval 15..3600s, maximum 1000 members")
	}
	seen := map[int64]bool{}
	for _, id := range s.AccountIDs {
		if id <= 0 || seen[id] {
			return fmt.Errorf("supplier account IDs must be positive and unique")
		}
		seen[id] = true
	}
	return nil
}
func (s *QualitySupplier) Summarize() {
	s.Observed, s.Affected = 0, 0
	for _, m := range s.Members {
		if m.ObservedAt != nil && m.Kind != "unknown" && m.Kind != "" && m.Kind != "canceled" {
			s.Observed++
			if m.Kind == "server" || m.Kind == "network" {
				s.Affected++
			}
		}
	}
	s.Incident = s.Enabled && s.Affected >= s.MinAccounts && s.Affected*100 >= s.Observed*s.FailurePercent
}

type QualityOperationsSnapshot struct {
	Config    QualityOperationsConfig `json:"config"`
	Day       string                  `json:"day"`
	ResetsAt  time.Time               `json:"resets_at"`
	Usage     []QualityBudgetUsage    `json:"usage"`
	Suppliers []QualitySupplier       `json:"suppliers"`
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
	SaveSupplier(context.Context, *QualitySupplier) error
	DeleteSupplier(context.Context, int64, int64) error
	ReserveQualityProbe(context.Context, int64, int64, bool, string) (*QualityProbeReservation, error)
	SettleQualityProbe(context.Context, *QualityProbeReservation, QualityProbeOutcome) error
	QualityFrequencyDue(context.Context, int64, int64, string) (*time.Time, error)
	RecordQualityFrequency(context.Context, *ScheduledTestPlan, *ScheduledTestResult, string, bool) error
	ReplayQualityPolicy(context.Context, QualityReplayRequest) (*QualityReplayResult, error)
}

// Run-local ID lets a supplier sentinel complete its serial test suite while
// concurrent instances are kept out by the shared supplier row lock.
type qualityRunKey struct{}

func QualityResultDeferred(r *ScheduledTestResult) bool {
	return r != nil && r.ExecutionInfo != nil && r.ExecutionInfo.FailureKind == "quality_deferred"
}

type QualityReplayRequest struct {
	Config      PassiveQualityConfig `json:"config"`
	AccountIDs  []int64              `json:"account_ids"`
	From        time.Time            `json:"from"`
	To          time.Time            `json:"to"`
	StepSeconds int                  `json:"step_seconds"`
}

func (r QualityReplayRequest) Validate(now time.Time) error {
	c := r.Config
	if err := c.Validate(); err != nil {
		return err
	}
	if len(r.AccountIDs) < 1 || len(r.AccountIDs) > 10 {
		return fmt.Errorf("select 1..10 replay accounts")
	}
	seen := map[int64]bool{}
	for _, id := range r.AccountIDs {
		if id <= 0 || seen[id] {
			return fmt.Errorf("invalid replay accounts")
		}
		seen[id] = true
	}
	if r.From.IsZero() || !r.To.After(r.From) || r.To.After(now.Add(time.Minute)) || r.From.Before(now.Add(-48*time.Hour)) || r.To.Sub(r.From) > 24*time.Hour || r.StepSeconds < 15 || r.StepSeconds > 3600 || (int(r.To.Sub(r.From).Seconds())/r.StepSeconds+1)*len(r.AccountIDs) > 2000 {
		return fmt.Errorf("replay: last 48 hours, maximum 24-hour range, step 15..3600s, maximum 2000 account evaluations")
	}
	return nil
}

type QualityReplayPoint struct {
	Model     string                              `json:"model,omitempty"`
	Latency   *PassiveQualityMetricState          `json:"latency,omitempty"`
	Cache     map[string]PassiveQualityCacheState `json:"cache,omitempty"`
	At        time.Time                           `json:"at"`
	AccountID int64                               `json:"account_id"`
	Verdict   string                              `json:"verdict"`
	Reason    string                              `json:"reason"`
	Action    string                              `json:"action"`
	Hits      int                                 `json:"hits"`
	Evidence  PassiveQualityEvidence              `json:"evidence"`
}
type QualityReplayResult struct {
	Points      []QualityReplayPoint `json:"points"`
	Evaluations int                  `json:"evaluations"`
	Triggers    int                  `json:"triggers"`
	Pending     int                  `json:"pending"`
	Warnings    []string             `json:"warnings"`
}

package service

import (
	"context"
	"errors"
	"slices"
	"time"
)

// Public runtime DTOs intentionally contain no cookie, credential, response ID,
// client session identity or generated content.
type AstraRouteStatus struct {
	ProxyNode        string     `json:"proxy_node,omitempty"`
	ProxyCountry     string     `json:"proxy_country,omitempty"`
	Answer           string     `json:"answer,omitempty"`
	AccountID        int64      `json:"account_id"`
	State            string     `json:"state"`
	Reason           string     `json:"reason"`
	CheckedAt        *time.Time `json:"checked_at,omitempty"`
	ExpiresAt        *time.Time `json:"expires_at,omitempty"`
	RemainingSeconds int64      `json:"remaining_seconds"`
	Gateway          string     `json:"gateway,omitempty"`
	Active           bool       `json:"active"`
}
type AstraWSStatus struct {
	AccountID        int64      `json:"account_id"`
	Ready            bool       `json:"ready"`
	Reason           string     `json:"reason"`
	ActiveSessions   int        `json:"active_sessions"`
	ExpiresAt        *time.Time `json:"expires_at,omitempty"`
	RemainingSeconds int64      `json:"remaining_seconds"`
}
type AstraRotationCooldown struct {
	AccountID        int64     `json:"account_id"`
	Gateway          string    `json:"gateway"`
	RetryAt          time.Time `json:"retry_at"`
	RemainingSeconds int64     `json:"remaining_seconds"`
}
type AstraGatewayRuntime struct {
	SchedulingRecords     []AstraSchedulingRecord   `json:"scheduling_records"`
	Cooldowns             []AstraRotationCooldown   `json:"cooldowns"`
	Gateways              []AstraGatewayObservation `json:"gateways"`
	UnknownGatewaySamples int64                     `json:"unknown_gateway_samples"`
	Setup                 AstraSetupStatus          `json:"setup"`
	GeneratedAt           time.Time                 `json:"generated_at"`
	Revision              string                    `json:"revision"`
	Sources               []AstraRouteStatus        `json:"sources"`
	Targets               []AstraRouteStatus        `json:"targets"`
	WS                    []AstraWSStatus           `json:"ws"`
	// RouteExpiresAt describes transport availability, independent of diagnostic results.
	RouteExpiresAt *time.Time              `json:"route_expires_at,omitempty"`
	ReadyRoutes    int                     `json:"ready_routes"`
	Preparing      bool                    `json:"preparing"`
	LastTest       *AstraGatewayTestResult `json:"last_test,omitempty"`
}
type AstraGatewayObservation struct {
	Gateway          string    `json:"gateway"`
	SourceAccountIDs []int64   `json:"source_account_ids"`
	Samples          int64     `json:"samples"`
	RepeatedHits     int64     `json:"repeated_hits"`
	SourcePasses     int64     `json:"source_passes"`
	SourceFailures   int64     `json:"source_failures"`
	TargetPasses     int64     `json:"target_passes"`
	TargetFailures   int64     `json:"target_failures"`
	LastSeen         time.Time `json:"last_seen"`
}
type AstraGatewayTestResult struct {
	TestKind    string    `json:"test_kind"`
	Answer      string    `json:"answer,omitempty"`
	Expected    string    `json:"expected,omitempty"`
	HTML        string    `json:"html,omitempty"`
	Action      string    `json:"action"`
	AccountID   int64     `json:"account_id"`
	Success     bool      `json:"success"`
	Reason      string    `json:"reason"`
	DurationMS  int64     `json:"duration_ms"`
	CheckedAt   time.Time `json:"checked_at"`
	OutputChars int       `json:"output_chars"`
}

// Runtime readiness comes from the same persisted account tickets used by forwarding.
func (s *AccountTestService) AstraGatewayStatus(ctx context.Context) AstraGatewayRuntime {
	settings := s.cfg.AstraRouting(ctx)
	result := AstraGatewayRuntime{GeneratedAt: time.Now(), Revision: settings.Revision, Sources: []AstraRouteStatus{}, Targets: []AstraRouteStatus{}, WS: []AstraWSStatus{}, Gateways: []AstraGatewayObservation{}}
	if s.openaiGatewayService != nil {
		result = s.openaiGatewayService.StoredAstraSnapshot(ctx)
	}
	result.SchedulingRecords = astraRecentScheduling.snapshot()
	for i := range result.Targets {
		if !settings.CookiePool.Enabled {
			result.Targets[i].State, result.Targets[i].Reason = "disabled", "disabled"
		}
	}
	s.astraGatewayTestMu.Lock()
	if s.astraGatewayLastTest != nil {
		copy := *s.astraGatewayLastTest
		result.LastTest = &copy
	}
	s.astraGatewayTestMu.Unlock()
	s.astraSetupMu.Lock()
	result.Setup = s.astraSetupStatus
	s.astraSetupMu.Unlock()
	result.Preparing = result.Setup.State == "queued" || result.Setup.State == "running"
	return result
}

// Explicit preparation is bounded and serialized. It uses the donor exit with
// each target's own credentials; only a complete two-shot validation is stored.
func (s *AccountTestService) TestAstraGateway(ctx context.Context, action string, id, _ int64, _ ...string) (AstraGatewayTestResult, error) {
	result := AstraGatewayTestResult{Action: action, AccountID: id, CheckedAt: time.Now(), TestKind: "state_probe"}
	if !s.astraGatewayActionMu.TryLock() {
		return result, errors.New("test_already_running")
	}
	defer s.astraGatewayActionMu.Unlock()
	if s.openaiGatewayService == nil {
		return result, errors.New("runtime_unavailable")
	}
	settings := s.cfg.AstraRouting(ctx)
	if !settings.CookiePool.Enabled {
		return result, errors.New("cookie_pool_disabled")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	var err error
	switch action {
	case "prepare":
		err = s.openaiGatewayService.PrepareStoredAstraSources(ctx)
		if err == nil {
			for _, target := range settings.CookiePool.TargetAccountIDs {
				if s.cfg.AstraRouting(ctx).Revision != settings.Revision {
					err = errors.New("configuration_changed")
					break
				}
				if err = s.openaiGatewayService.VerifyStoredAstraTarget(ctx, target); err != nil {
					break
				}
			}
		}
		if err == nil {
			if _, ready := astraTargetsReady(ctx, s.openaiGatewayService, settings.CookiePool.TargetAccountIDs); !ready {
				err = errors.New("target_not_verified")
			}
		}
	case "verify":
		if !slices.Contains(settings.CookiePool.TargetAccountIDs, id) {
			return result, errors.New("target_not_configured")
		}
		err = s.openaiGatewayService.VerifyStoredAstraTarget(ctx, id)
	default:
		return result, errors.New("invalid_action")
	}
	result.Success = err == nil
	result.Reason = "success"
	if err != nil {
		result.Reason = astraSetupError(err)
	}
	result.DurationMS = time.Since(result.CheckedAt).Milliseconds()
	s.astraGatewayTestMu.Lock()
	s.astraGatewayLastTest = &result
	s.astraGatewayTestMu.Unlock()
	return result, nil
}

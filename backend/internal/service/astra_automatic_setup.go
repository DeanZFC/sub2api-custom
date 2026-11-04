package service

import (
	"context"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
)

type AstraSetupStatus struct {
	Revision   string     `json:"revision"`
	State      string     `json:"state"`
	Phase      string     `json:"phase"`
	AccountID  int64      `json:"account_id"`
	Reason     string     `json:"reason"`
	StartedAt  time.Time  `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
}

// A save schedules one bounded serial preparation run. Saving again cancels
// the previous run, and revision checks prevent stale work publishing readiness.
func (s *AccountTestService) StartAstraAutomaticSetup(settings config.AstraRoutingSettings) {
	if s.cfg.AstraRouting(context.Background()).Revision != settings.Revision {
		return
	}
	s.astraSetupMu.Lock()
	if s.astraSetupStopped {
		s.astraSetupMu.Unlock()
		return
	}
	if s.cfg.AstraRouting(context.Background()).Revision != settings.Revision {
		s.astraSetupMu.Unlock()
		return
	}
	if s.astraSetupCancel != nil {
		s.astraSetupCancel()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	s.astraSetupCancel = cancel
	state := "queued"
	if !settings.CookiePool.Enabled && !settings.WSSession.Enabled {
		state = "disabled"
	}
	s.astraSetupStatus = AstraSetupStatus{Revision: settings.Revision, State: state, StartedAt: time.Now()}
	if state != "disabled" {
		s.astraSetupWG.Add(1)
	}
	s.astraSetupMu.Unlock()
	if state == "disabled" {
		cancel()
		return
	}
	go func() { defer s.astraSetupWG.Done(); s.runAstraAutomaticSetup(ctx, cancel, settings) }()
}
func (s *AccountTestService) runAstraAutomaticSetup(ctx context.Context, cancel context.CancelFunc, settings config.AstraRoutingSettings) {
	defer cancel()
	set := func(state, phase string, id int64, reason string) {
		s.astraSetupMu.Lock()
		defer s.astraSetupMu.Unlock()
		if s.astraSetupStatus.Revision != settings.Revision {
			return
		}
		s.astraSetupStatus.State = state
		s.astraSetupStatus.Phase = phase
		s.astraSetupStatus.AccountID = id
		s.astraSetupStatus.Reason = reason
		if state == "failed" || state == "ready" {
			now := time.Now()
			s.astraSetupStatus.FinishedAt = &now
		}
	}
	// Share serialization with manually requested tests without discarding a save
	// just because an older, cancelled task has not released its lock yet.
	for !s.astraGatewayActionMu.TryLock() {
		select {
		case <-ctx.Done():
			set("failed", "", 0, "setup_cancelled")
			return
		case <-time.After(100 * time.Millisecond):
		}
	}
	defer s.astraGatewayActionMu.Unlock()
	if ctx.Err() != nil || s.cfg.AstraRouting(ctx).Revision != settings.Revision {
		set("failed", "", 0, "setup_cancelled")
		return
	}
	set("running", "source", 0, "")
	provider := s.openaiGatewayService
	if provider == nil {
		set("failed", "source", 0, "runtime_unavailable")
		return
	}
	if err := prepareAstraForSetup(ctx, provider); err != nil {
		set("failed", "source", 0, astraSetupError(err))
		return
	}
	// All selected targets must acquire their own verified ticket on the donor exit.
	for _, id := range settings.CookiePool.TargetAccountIDs {
		if ctx.Err() != nil || s.cfg.AstraRouting(ctx).Revision != settings.Revision {
			set("failed", "target", id, "configuration_changed")
			return
		}
		set("running", "target", id, "")
		if err := provider.VerifyStoredAstraTarget(ctx, id); err != nil {
			set("failed", "target", id, astraSetupError(err))
			return
		}
	}
	if id, ready := astraTargetsReady(ctx, provider, settings.CookiePool.TargetAccountIDs); !ready {
		set("failed", "target", id, "target_not_verified")
		return
	}
	if ctx.Err() != nil || s.cfg.AstraRouting(ctx).Revision != settings.Revision {
		set("failed", "source", 0, "configuration_changed")
		return
	}
	set("ready", "complete", 0, "")
}
func astraSetupError(err error) string {
	switch err.Error() {
	case "account_unavailable", "target_not_verified", "no_qualified_source_route", "source_probe_cooldown", "configuration_changed", "cookie_pool_disabled", "target_probe_degraded", "target_route_changed", "target_probe_failed", "target_validation_in_progress", "preparation_in_progress":
		return err.Error()
	}
	if err == context.Canceled || err == context.DeadlineExceeded {
		return "setup_cancelled"
	}
	return "setup_failed"
}

func (s *AccountTestService) StopAstraAutomaticSetup() {
	s.astraSetupMu.Lock()
	s.astraSetupStopped = true
	if s.astraSetupCancel != nil {
		s.astraSetupCancel()
	}
	s.astraSetupMu.Unlock()
	s.astraSetupWG.Wait()
}

// Tickets can expire while other targets are being prepared. Report setup
// success only when every selected target still has a valid stored ticket.
func astraTargetsReady(ctx context.Context, provider AstraTicketLifecycle, ids []int64) (int64, bool) {
	snapshot := provider.StoredAstraSnapshot(ctx)
	for _, id := range ids {
		ready := false
		for _, row := range snapshot.Targets {
			if row.AccountID == id && row.State == "ready" {
				ready = true
				break
			}
		}
		if !ready {
			return id, false
		}
	}
	return 0, true
}

// Saving may overlap an on-demand preparation. Wait for it rather than
// incorrectly publishing a terminal failure while the source is still running.
func prepareAstraForSetup(ctx context.Context, provider AstraTicketLifecycle) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		err := provider.PrepareStoredAstraSources(ctx)
		if err == nil || err.Error() != "preparation_in_progress" {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
}

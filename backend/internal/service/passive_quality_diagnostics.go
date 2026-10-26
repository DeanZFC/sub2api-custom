package service

import (
	"context"
	"time"
)

// Unknown scope fields match every target; Kind is failure, cache or all.
type PassiveQualityTelemetryGap struct {
	From           time.Time `json:"from"`
	To             time.Time `json:"to"`
	AccountID      int64     `json:"account_id"`
	GroupID        int64     `json:"group_id"`
	Model          string    `json:"model"`
	Kind           string    `json:"kind"`
	DroppedRecords int64     `json:"dropped_records"`
}

type PassiveQualityTelemetryRepository interface {
	RecordTelemetry(context.Context, []PassiveQualityFailure, []PassiveQualityTelemetryGap) error
	PrunePassiveQuality(context.Context) error
}

// Process-local engine health; durable policy states are reported separately.
type PassiveQualityRuntimeDiagnostics struct {
	QueueDepth             int        `json:"queue_depth"`
	QueueCapacity          int        `json:"queue_capacity"`
	PendingRecords         int64      `json:"pending_records"`
	DroppedRecords         int64      `json:"dropped_records"`
	PendingGapScopes       int        `json:"pending_gap_scopes"`
	LastPersistedAt        *time.Time `json:"last_persisted_at"`
	LastPersistError       string     `json:"last_persist_error"`
	LastSweepStartedAt     *time.Time `json:"last_sweep_started_at"`
	LastSweepSucceededAt   *time.Time `json:"last_sweep_succeeded_at"`
	LastSweepDurationMS    int64      `json:"last_sweep_duration_ms"`
	LastSweepError         string     `json:"last_sweep_error"`
	SweepSkippedBacklog    int64      `json:"sweep_skipped_backlog"`
	LastCleanupSucceededAt *time.Time `json:"last_cleanup_succeeded_at"`
	LastCleanupError       string     `json:"last_cleanup_error"`
	CollectionLagSeconds   int64      `json:"collection_lag_seconds"`
}

type passiveQualityGapScope struct {
	account, group int64
	model, kind    string
}

func (s *PassiveQualityService) Diagnostics() PassiveQualityRuntimeDiagnostics {
	s.diagnosticsMu.Lock()
	out := s.diagnostics
	s.diagnosticsMu.Unlock()
	out.QueueDepth, out.QueueCapacity = len(s.queue), cap(s.queue)
	out.PendingRecords, out.DroppedRecords = s.pendingCount.Load(), s.dropped.Load()
	s.gapsMu.Lock()
	out.PendingGapScopes = len(s.gaps)
	s.gapsMu.Unlock()
	if since := s.backlogSince.Load(); since > 0 {
		out.CollectionLagSeconds = int64(time.Since(time.Unix(0, since)).Seconds())
		if out.CollectionLagSeconds < 0 {
			out.CollectionLagSeconds = 0
		}
	}
	return out
}

func (s *PassiveQualityService) markGap(g PassiveQualityTelemetryGap) {
	s.gapsMu.Lock()
	defer s.gapsMu.Unlock()
	if s.gaps == nil {
		s.gaps = make(map[passiveQualityGapScope]PassiveQualityTelemetryGap)
	}
	if g.To.IsZero() {
		g.To = g.From
	}
	key := passiveQualityGapScope{g.AccountID, g.GroupID, g.Model, g.Kind}
	// Bounded memory even when a saturated collector sees many unique models.
	if _, exists := s.gaps[key]; !exists && len(s.gaps) >= 256 {
		key = passiveQualityGapScope{kind: "all"}
		g.AccountID, g.GroupID, g.Model, g.Kind = 0, 0, "", "all"
	}
	if old, exists := s.gaps[key]; exists {
		if old.From.Before(g.From) {
			g.From = old.From
		}
		if old.To.After(g.To) {
			g.To = old.To
		}
		g.DroppedRecords += old.DroppedRecords
	}
	s.gaps[key] = g
}

func (s *PassiveQualityService) takeGaps() []PassiveQualityTelemetryGap {
	s.gapsMu.Lock()
	defer s.gapsMu.Unlock()
	out := make([]PassiveQualityTelemetryGap, 0, len(s.gaps))
	for _, gap := range s.gaps {
		out = append(out, gap)
	}
	s.gaps = nil
	return out
}

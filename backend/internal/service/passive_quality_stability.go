package service

import (
	"fmt"
	"time"
)

// GroupQualityStability separates short availability failures from slower
// performance changes. A non-nil config preserves explicitly selected zeroes.
type GroupQualityStability struct {
	AvailabilityPauseSeconds int `json:"availability_pause_seconds"`
	PerformancePauseSeconds  int `json:"performance_pause_seconds"`
	RecoverySeconds          int `json:"recovery_seconds"`
}

func (c PassiveQualityConfig) GroupTargetStability() GroupQualityStability {
	var stability GroupQualityStability
	if c.Stability != nil {
		stability = *c.Stability
	} else {
		stability = GroupQualityStability{AvailabilityPauseSeconds: 60, PerformancePauseSeconds: 300, RecoverySeconds: 120}
	}
	return stability
}

func (c PassiveQualityConfig) validateGroupTargetStability() error {
	if c.Stability == nil {
		return nil
	}
	if !c.IsGroupQualityTarget() {
		return fmt.Errorf("stability is only supported for group quality targets")
	}
	s := *c.Stability
	if s.AvailabilityPauseSeconds < 0 || s.AvailabilityPauseSeconds > 3600 || s.PerformancePauseSeconds < 0 || s.PerformancePauseSeconds > 3600 || s.RecoverySeconds < 0 || s.RecoverySeconds > 3600 {
		return fmt.Errorf("stability durations must be 0..3600 seconds")
	}
	return nil
}

func (c PassiveQualityConfig) GroupTargetPauseDuration(metric string) time.Duration {
	s := c.GroupTargetStability()
	seconds := s.PerformancePauseSeconds
	if metric == "error_rate" {
		seconds = s.AvailabilityPauseSeconds
	}
	return time.Duration(seconds) * time.Second
}

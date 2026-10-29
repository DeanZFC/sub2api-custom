package service

import (
	"fmt"
	"math"
)

// Group targets are explicit measured-traffic objectives, independent of the
// reuse-opportunity cache control. They own the existing hard hold lifecycle.
func (c PassiveQualityConfig) IsGroupQualityTarget() bool {
	return c.Purpose == "group_quality_target" || c.Purpose == "cache_target"
}

func (c PassiveQualityConfig) IsCacheTarget() bool { return c.Purpose == "cache_target" }

func (c PassiveQualityConfig) IsActiveRecovery() bool {
	return c.IsGroupQualityTarget() && c.RecoveryMode == "active"
}

func (c *PassiveQualityConfig) prepareGroupQualityTarget() error {
	if c.Purpose == "" {
		return nil
	}
	if !c.IsGroupQualityTarget() {
		return fmt.Errorf("unsupported passive quality purpose")
	}
	if (c.SchemaVersion != 0 && c.SchemaVersion != 2) || (c.Scope != "" && c.Scope != "model") || (c.Action != "" && c.Action != "pause") || len(c.GroupIDs) != 1 {
		return fmt.Errorf("group quality targets require V2, model scope, pause action and exactly one group")
	}
	if c.CacheProtection != nil || c.TTFTAction != "" || c.TPSAction != "" || c.ConsecutiveFailures != 0 {
		return fmt.Errorf("group quality targets do not accept reuse protection, latency actions or consecutive failures")
	}
	if c.IsCacheTarget() {
		if c.ErrorRate.Enabled || c.TTFT.Enabled || c.TPS.Enabled {
			return fmt.Errorf("cache targets only accept the cache metric")
		}
		c.CacheRate.Enabled = true
	}
	if c.CacheRate.Enabled && c.CacheRate.Pause >= 100 {
		return fmt.Errorf("cache target must be below 100 percent")
	}
	if c.ErrorRate.Enabled && c.ErrorRate.Pause <= 0 {
		return fmt.Errorf("availability target must be below 100 percent")
	}
	if c.TTFT.Enabled && c.TTFT.Pause <= 0 {
		return fmt.Errorf("first-token target must be greater than zero")
	}
	if c.TPS.Enabled && c.TPS.Pause <= 0 {
		return fmt.Errorf("TPS target must be greater than zero")
	}
	if c.RecoveryMode == "" {
		c.RecoveryMode = "active"
	}
	if c.RecoveryMode != "active" && c.RecoveryMode != "traffic" {
		return fmt.Errorf("unsupported recovery mode")
	}
	c.SchemaVersion, c.Scope, c.Action = 2, "model", "pause"
	if c.Mode == "" {
		c.Mode = "observe"
	}
	if c.TrafficMode == "" {
		c.TrafficMode = "strict"
	}
	if c.TrafficMode != "strict" && c.TrafficMode != "availability" {
		return fmt.Errorf("traffic mode must be strict or availability")
	}
	if c.Name == "" {
		c.Name = "Group quality target"
	}
	minimum, fresh, maxCooldown := 5, 2, 1800
	if c.IsActiveRecovery() {
		minimum, fresh, maxCooldown = 20, 5, 3600
	}
	for _, field := range []struct {
		value    *int
		fallback int
	}{
		{&c.WindowSeconds, 3600}, {&c.MaxSamples, 100}, {&c.MinSamples, minimum},
		{&c.Confirmations, 2}, {&c.NewSamples, fresh}, {&c.CooldownSeconds, 300},
		{&c.MaxCooldownSeconds, maxCooldown}, {&c.TrialSeconds, 900}, {&c.TrialRequests, 20},
		{&c.TrialConcurrency, 1}, {&c.RecoverySamples, 5},
	} {
		if *field.value == 0 {
			*field.value = field.fallback
		}
	}
	if c.IsActiveRecovery() {
		c.WindowSeconds, c.MaxSamples = 3600, 0
		c.TrafficMode = "strict"
	}
	if c.CacheRate.Enabled && c.CacheRate.Recover == 0 {
		c.CacheRate.Recover = math.Min(100, c.CacheRate.Pause+5)
	}
	if c.ErrorRate.Enabled && c.ErrorRate.Recover == 0 {
		c.ErrorRate.Recover = math.Max(0, c.ErrorRate.Pause-5)
	}
	if c.TTFT.Enabled && c.TTFT.Recover == 0 {
		c.TTFT.Recover = c.TTFT.Pause * .8
	}
	if c.TPS.Enabled && c.TPS.Recover == 0 {
		c.TPS.Recover = c.TPS.Pause * 1.2
	}
	return nil
}

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
	if c.CacheProtection != nil || c.TTFTAction != "" || c.ConsecutiveFailures != 0 {
		return fmt.Errorf("group quality targets do not accept reuse protection, latency actions or consecutive failures")
	}
	if c.IsCacheTarget() {
		if c.ErrorRate.Enabled || c.TTFT.Enabled {
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
	c.SchemaVersion, c.Scope, c.Action = 2, "model", "pause"
	if c.Mode == "" {
		c.Mode = "observe"
	}
	if c.Name == "" {
		c.Name = "Group quality target"
	}
	for _, field := range []struct {
		value    *int
		fallback int
	}{
		{&c.WindowSeconds, 3600}, {&c.MaxSamples, 100}, {&c.MinSamples, 5},
		{&c.Confirmations, 2}, {&c.NewSamples, 2}, {&c.CooldownSeconds, 300},
		{&c.MaxCooldownSeconds, 1800}, {&c.TrialSeconds, 900}, {&c.TrialRequests, 20},
		{&c.TrialConcurrency, 1}, {&c.RecoverySamples, 5},
	} {
		if *field.value == 0 {
			*field.value = field.fallback
		}
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
	return nil
}

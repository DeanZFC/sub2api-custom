package config

import "fmt"

type CodexGatewayPinConfig struct {
	NodeCooldownSeconds int     `mapstructure:"node_cooldown_seconds" json:"node_cooldown_seconds"`
	RotateNodes         bool    `mapstructure:"rotate_nodes" json:"rotate_nodes"`
	MaxNodeAttempts     int     `mapstructure:"max_node_attempts" json:"max_node_attempts"`
	IPAffinity          bool    `mapstructure:"ip_affinity" json:"ip_affinity"`
	TTLSeconds          int     `mapstructure:"ttl_seconds" json:"ttl_seconds"`
	Enabled             bool    `mapstructure:"enabled" json:"enabled"`
	SourceAccountIDs    []int64 `mapstructure:"source_account_ids" json:"source_account_ids"`
	TargetAccountIDs    []int64 `mapstructure:"target_account_ids" json:"target_account_ids"`
}

func (c CodexGatewayPinConfig) Validate() error {
	if c.RotateNodes {
		return fmt.Errorf("astra_legacy_rotation_unsupported")
	}
	if c.NodeCooldownSeconds != 0 && (c.NodeCooldownSeconds < 60 || c.NodeCooldownSeconds > 86400) {
		return fmt.Errorf("astra node cooldown must be 60–86400 seconds")
	}
	if c.MaxNodeAttempts < 0 || c.MaxNodeAttempts > 10 {
		return fmt.Errorf("astra node attempts must be 1–10 (0 uses default 3)")
	}
	if c.TTLSeconds != 0 && (c.TTLSeconds < 30 || c.TTLSeconds > 240) {
		return fmt.Errorf("cookie TTL must be 30–240 seconds")
	}
	if !c.Enabled {
		return nil
	}
	if len(c.SourceAccountIDs) == 0 || len(c.TargetAccountIDs) == 0 || len(c.SourceAccountIDs) > 64 || len(c.TargetAccountIDs) > 64 {
		return fmt.Errorf("gateway.codex_gateway_pin requires 1–64 source_account_ids and target_account_ids")
	}
	seen := map[int64]bool{}
	for _, ids := range [][]int64{c.SourceAccountIDs, c.TargetAccountIDs} {
		for _, id := range ids {
			if id <= 0 || seen[id] {
				return fmt.Errorf("gateway.codex_gateway_pin account IDs must be positive, unique and disjoint")
			}
			seen[id] = true
		}
	}
	return nil
}

// CodexWSAnchorConfig reads legacy settings for compatibility. The retired
// fixed-session adapter is unsupported; enabling it is rejected explicitly.
type CodexWSAnchorConfig struct {
	TTLSeconds int     `mapstructure:"ttl_seconds" json:"ttl_seconds"`
	Enabled    bool    `mapstructure:"enabled" json:"enabled"`
	AccountIDs []int64 `mapstructure:"account_ids" json:"account_ids"`
}

func (c CodexWSAnchorConfig) Validate() error {
	if c.Enabled {
		return fmt.Errorf("astra_ws_anchor_unsupported")
	}
	return nil
}

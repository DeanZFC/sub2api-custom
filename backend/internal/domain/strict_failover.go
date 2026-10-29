package domain

// StrictFailoverConfig orders explicit account pools within a group.
// Unlisted accounts never receive traffic while enabled.
type StrictFailoverConfig struct {
	Enabled bool      `json:"enabled"`
	Tiers   [][]int64 `json:"tiers"`
}

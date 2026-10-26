package service

import (
	"context"
	"errors"
	"time"
)

// Query reporting is separate from runtime state access and preserves the legacy API.
var ErrPassiveQualityCursor = errors.New("invalid report cursor")

type PassiveQualityQuery struct {
	PolicyID  int64
	AccountID int64
	Model     string
	Phase     string
	Kind      string
	Since     *time.Time
	Until     *time.Time
	Cursor    string
	Limit     int
}
type PassiveQualityStatePage struct {
	Items      []PassiveQualityState `json:"items"`
	NextCursor string                `json:"next_cursor,omitempty"`
	HasMore    bool                  `json:"has_more"`
	Total      int64                 `json:"total"`
}
type PassiveQualityEventPage struct {
	Items      []PassiveQualityEvent `json:"items"`
	NextCursor string                `json:"next_cursor,omitempty"`
	HasMore    bool                  `json:"has_more"`
}
type PassiveQualityPolicyCounts struct {
	PolicyID int64            `json:"policy_id"`
	Total    int64            `json:"total"`
	Phases   map[string]int64 `json:"phases"`
	Verified int64            `json:"verified"`
}
type PassiveQualityStateSummary struct {
	Total            int64                        `json:"total"`
	PausedAccounts   int64                        `json:"paused_accounts"`
	LimitedAccounts  int64                        `json:"limited_accounts"`
	TrialAccounts    int64                        `json:"trial_accounts"`
	ObservedAccounts int64                        `json:"observed_accounts"`
	ByPolicy         []PassiveQualityPolicyCounts `json:"by_policy"`
}
type PassiveQualityQueryRepository interface {
	QueryStates(context.Context, PassiveQualityQuery) (*PassiveQualityStatePage, error)
	QueryEvents(context.Context, PassiveQualityQuery) (*PassiveQualityEventPage, error)
	StateSummary(context.Context) (*PassiveQualityStateSummary, error)
}

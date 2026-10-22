package service

import (
	"context"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
)

// Quality is a projection of independent owners. Reading or refreshing the
// overview never releases a hold, modifies an account, or runs an upstream test.
type ChannelQualityFilter struct {
	Search       string
	Model        string
	GroupID      int64
	AccountID    int64
	After        int64
	Limit        int
	ProblemsOnly bool
}

type ChannelQualityControl struct {
	Source    string                  `json:"source"`
	ID        int64                   `json:"id"`
	Name      string                  `json:"name"`
	Model     string                  `json:"model"`
	Scope     string                  `json:"scope"`
	Phase     string                  `json:"phase"`
	Reason    string                  `json:"reason"`
	Recovery  string                  `json:"recovery"`
	NextAt    *time.Time              `json:"next_at,omitempty"`
	UpdatedAt time.Time               `json:"updated_at"`
	Blocking  bool                    `json:"blocking"`
	ResultID  int64                   `json:"result_id,omitempty"`
	Evidence  *PassiveQualityEvidence `json:"evidence,omitempty"`
}

type ChannelQualityAccount struct {
	ID              int64                   `json:"id"`
	Name            string                  `json:"name"`
	Platform        string                  `json:"platform"`
	Status          string                  `json:"status"`
	Schedulable     bool                    `json:"schedulable"`
	Controls        []ChannelQualityControl `json:"controls"`
	ActivePlans     int                     `json:"active_plans"`
	PassivePolicies int                     `json:"passive_policies"`
}

type ChannelQualityPage struct {
	Items       []ChannelQualityAccount `json:"items"`
	NextAfter   int64                   `json:"next_after,omitempty"`
	GeneratedAt time.Time               `json:"generated_at"`
}

type ChannelQualityRepository interface {
	ListQualityAccounts(context.Context, ChannelQualityFilter) (*ChannelQualityPage, error)
}

// Routing quality is advisory until the final admission gate. Apply it only to
// request-local account copies, never to the persistent administrator priority.
func (a *Account) PassiveQualityLimit(model string) (concurrency, penalty int) {
	if a == nil {
		return
	}
	holds, _ := a.Extra["passive_quality"].([]any)
	for _, item := range holds {
		h, ok := item.(map[string]any)
		if !ok || h["phase"] != "limited" {
			continue
		}
		if h["scope"] != "account" && (model == "" || h["model"] != model) {
			continue
		}
		cap, _ := h["concurrency_limit"].(float64)
		cost, _ := h["priority_penalty"].(float64)
		if cap > 0 && (concurrency == 0 || int(cap) < concurrency) {
			concurrency = int(cap)
		}
		penalty = max(penalty, int(cost))
	}
	return
}

func qualitySchedulingAccount(a Account, model string) Account {
	if a.qualitySchedulingAdjusted {
		return a
	}
	a.qualitySchedulingAdjusted = true
	limit, penalty := a.PassiveQualityLimit(model)
	a.qualityConcurrencyLimit = limit
	if limit > 0 && (a.Concurrency <= 0 || limit < a.Concurrency) {
		a.Concurrency = limit
	}
	if penalty > 0 {
		// Administrator priorities are bounded in normal writes; saturate rather
		// than overflowing if a legacy record contains an extreme value.
		if a.Priority > int(^uint(0)>>1)-penalty {
			a.Priority = int(^uint(0) >> 1)
		} else {
			a.Priority += penalty
		}
	}
	return a
}

func qualityRequestModel(ctx context.Context) string {
	if public, ok := RequestedPublicModelFromContext(ctx); ok && public != "" {
		return public
	}
	model, _ := ctx.Value(ctxkey.Model).(string)
	return model
}

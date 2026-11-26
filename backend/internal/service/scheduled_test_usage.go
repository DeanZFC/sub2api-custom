package service

import (
	"context"
	"errors"
	"slices"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
)

// ScheduledTestUsage describes one admitted outbound request. The reservation
// ID is stable across persistence retries, including failed connections.
type ScheduledTestUsage struct {
	ReservationID   int64
	AccountID       int64
	Platform        string
	GroupIDs        []int64
	Model           string
	ReasoningEffort string
	Stream          bool
	InputTokens     int64 // uncached input, as stored by the gateway
	Attempt         ScheduledTestRequestAttempt
}

type scheduledTestPlanContextKey struct{}

func scheduledTestUsageGroups(plan *ScheduledTestPlan, account *Account) []int64 {
	selected := plan.GroupIDs
	if len(selected) == 0 && plan.GroupID != nil {
		selected = []int64{*plan.GroupID}
	}
	groups := make([]int64, 0, len(account.GroupIDs))
	for _, id := range account.GroupIDs {
		if id > 0 && (len(selected) == 0 || slices.Contains(selected, id)) {
			groups = append(groups, id)
		}
	}
	slices.Sort(groups)
	return slices.Compact(groups)
}

func (c *accountTestUsageCollector) recordScheduledTestUsage(ctx context.Context, repo QualityOperationsRepository, attempts []ScheduledTestRequestAttempt, model, effort string) {
	plan, _ := ctx.Value(scheduledTestPlanContextKey{}).(*ScheduledTestPlan)
	if repo == nil || c.account == nil || plan == nil {
		return
	}
	groups := scheduledTestUsageGroups(plan, c.account)
	for _, a := range attempts {
		requested := strings.TrimSpace(model)
		if requested == "" {
			requested = a.UpstreamModel
		}
		if requested == "" {
			requested = c.account.providerDefaultTestModel()
		}
		if requested == "" {
			requested = "unknown"
		}
		var input int64
		if a.InputTokens != nil {
			input = *a.InputTokens
			if !a.qualityCacheExclusive && a.CacheReadTokens != nil {
				input -= *a.CacheReadTokens
				if input < 0 {
					input = 0
				}
			}
		}
		u := ScheduledTestUsage{ReservationID: a.reservationID, AccountID: c.account.ID, Platform: c.account.Platform, GroupIDs: groups,
			Model: requested, ReasoningEffort: effort, Stream: a.qualityStream, InputTokens: input, Attempt: a}
		// A cancelled request still needs its terminal record. Repeating a write
		// after a lost commit acknowledgement uses the same reservation.
		var err error
		for range 2 {
			persist, cancel := scheduledTestPersistenceContext()
			err = repo.RecordScheduledTestUsage(persist, u)
			cancel()
			if err == nil || errors.Is(err, context.Canceled) {
				break
			}
		}
		if err != nil {
			logger.LegacyPrintf("service.scheduled_test_runner", "record quality usage account=%d reservation=%d: %v", c.account.ID, a.reservationID, err)
		}
	}
}

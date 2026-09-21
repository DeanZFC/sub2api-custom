package service

import (
	"context"
	"time"
)

// ScheduledTestActionRoundRepository invalidates all conclusions before a full
// plan run. Pending checks retain applied actions but cannot grant a new upgrade.
// A one-result retry intentionally does not begin another full-plan round.
type ScheduledTestActionRoundRepository interface {
	BeginProtectionRun(context.Context, *ScheduledTestPlan, time.Time) error
}

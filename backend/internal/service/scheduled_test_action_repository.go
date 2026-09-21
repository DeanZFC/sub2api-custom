package service

import "context"

// Optional capability for following accounts across automatic group moves.
// Existing adapters without outcome actions retain their original selection.
type ScheduledTestActionRepository interface {
	ListPlanDetectionAccountIDs(context.Context, *ScheduledTestPlan, *int64) ([]int64, error)
}

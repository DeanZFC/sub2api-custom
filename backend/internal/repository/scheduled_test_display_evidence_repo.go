package repository

import (
	"context"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

// Display evidence is optional and never changes governance. A failed evidence
// read must not hide the already-authorized test results themselves.
func (r *scheduledTestResultRepository) hydrateDisplayEvidence(ctx context.Context, results []*service.ScheduledTestResult) {
	_ = r.hydratePassRates(ctx, results)
}

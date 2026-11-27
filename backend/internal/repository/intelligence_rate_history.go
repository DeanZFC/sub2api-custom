package repository

import (
	"context"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

// Look at the two preceding independent rounds in which this target was due.
// A skipped or missing observation in the target's frozen row breaks the chain.
func (r *scheduledTestResultRepository) PreviousIntelligence429(ctx context.Context, result *service.ScheduledTestResult, version string) (int, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT COALESCE(execution_info->'intelligence'->>'candy_http_status','')='429' AND COALESCE(execution_info->'intelligence'->>'policy_version','')=$6
 FROM scheduled_test_results WHERE plan_id=$1 AND account_id=$2 AND model_id=$3 AND reasoning_effort=$4 AND id<$5 AND run_id<>'' AND run_id<>$7
 ORDER BY id DESC LIMIT 2`, result.PlanID, result.AccountID, result.ModelID, result.ReasoningEffort, result.ID, version, result.RunID)
	if err != nil {
		return 0, err
	}
	defer func() { _ = rows.Close() }()
	n := 0
	for rows.Next() {
		var limited bool
		if err := rows.Scan(&limited); err != nil {
			return 0, err
		}
		if !limited {
			break
		}
		n++
	}
	return n, rows.Err()
}

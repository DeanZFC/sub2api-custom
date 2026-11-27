package repository

import (
	"context"
	"database/sql"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
)

// BeginRun holds the plan and each target account lock and has inserted every
// pending result. Membership and result identity are frozen in that transaction.
func snapshotIntelligenceRound(ctx context.Context, tx *sql.Tx, plan *service.ScheduledTestPlan, runID string) error {
	if _, err := tx.ExecContext(ctx, `INSERT INTO intelligence_rounds(run_id,plan_id,started_at,pending)
 SELECT $1,$2,min(started_at),count(*) FILTER(WHERE status IN ('pending','running'))
 FROM scheduled_test_results WHERE plan_id=$2 AND run_id=$1 HAVING count(*)>0`, runID, plan.ID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO intelligence_account_aliases(group_id,account_id)
 SELECT DISTINCT ag.group_id,ag.account_id FROM scheduled_test_results r JOIN account_groups ag ON ag.account_id=r.account_id
 WHERE r.plan_id=$1 AND r.run_id=$2 AND ag.group_id=ANY($3) ORDER BY ag.group_id,ag.account_id
 ON CONFLICT(group_id,account_id) DO NOTHING`, plan.ID, runID, pq.Array(plan.GroupIDs)); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO intelligence_round_targets(run_id,group_id,alias,model,effort,result_id)
 SELECT r.run_id,ag.group_id,a.alias,r.model_id,r.reasoning_effort,r.id FROM scheduled_test_results r
 JOIN account_groups ag ON ag.account_id=r.account_id AND ag.group_id=ANY($3)
 JOIN intelligence_account_aliases a ON a.group_id=ag.group_id AND a.account_id=r.account_id
 WHERE r.plan_id=$1 AND r.run_id=$2`, plan.ID, runID, pq.Array(plan.GroupIDs)); err != nil {
		return err
	}
	// Keep the last complete windows for every group/model even after scope
	// changes, and expire abandoned rounds after the execution lease has expired.
	_, err := tx.ExecContext(ctx, `WITH scope AS (
 SELECT DISTINCT b.run_id,t.group_id,t.model,b.finished_at FROM intelligence_rounds b
 JOIN intelligence_round_targets t ON t.run_id=b.run_id WHERE b.plan_id=$1 AND b.finished_at IS NOT NULL
 ), keep AS (SELECT run_id,ROW_NUMBER() OVER(PARTITION BY group_id,model ORDER BY finished_at DESC,run_id DESC) pos FROM scope)
 DELETE FROM intelligence_rounds b WHERE b.plan_id=$1 AND b.run_id<>$2 AND
 ((b.finished_at IS NOT NULL AND NOT EXISTS (SELECT 1 FROM keep k WHERE k.run_id=b.run_id AND k.pos<=GREATEST($3,20)))
 OR (b.finished_at IS NULL AND b.started_at<NOW()-INTERVAL '7 days'))`, plan.ID, runID, plan.MaxResults)
	return err
}

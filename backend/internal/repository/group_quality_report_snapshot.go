package repository

import (
	"context"
	"encoding/json"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

// Expensive report work finishes before publication takes the policy/snapshot
// locks. Publication is fenced against policy edits.
func (r *passiveQualityRepository) publishGroupQualitySummary(ctx context.Context, p service.PassiveQualityPolicy, s service.GroupQualityTargetSummary) error {
	raw, err := json.Marshal(s)
	if err != nil {
		return err
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var version int64
	if err = tx.QueryRowContext(ctx, `SELECT version FROM passive_quality_policies WHERE id=$1 FOR SHARE`, p.ID).Scan(&version); err != nil {
		return err
	}
	if version != p.Version {
		return nil
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO group_quality_report_snapshots(policy_id,policy_version,measured_at,data) VALUES($1,$2,$3,$4)
 ON CONFLICT(policy_id) DO UPDATE SET policy_version=excluded.policy_version,measured_at=excluded.measured_at,data=excluded.data
 WHERE group_quality_report_snapshots.policy_version<>excluded.policy_version OR group_quality_report_snapshots.measured_at<=excluded.measured_at`, p.ID, p.DecisionVersion(), s.GeneratedAt, raw)
	if err != nil {
		return err
	}
	return tx.Commit()
}

// Sweep cadence, rather than traffic volume, bounds authoritative usage scans.
func (r *passiveQualityRepository) refreshGroupQualityReportSnapshot(ctx context.Context, p service.PassiveQualityPolicy, now time.Time) error {
	if !p.IsGroupQualityTarget() || len(p.GroupIDs) != 1 {
		return nil
	}
	var fresh bool
	if err := r.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM group_quality_report_snapshots WHERE policy_id=$1 AND policy_version=$2 AND measured_at>=$3)`, p.ID, p.DecisionVersion(), now.Add(-time.Minute)).Scan(&fresh); err != nil {
		return err
	}
	if fresh {
		return nil
	}
	summary, err := r.groupQualityTargetSummary(ctx, p, now)
	if err != nil {
		return err
	}
	return r.publishGroupQualitySummary(ctx, p, summary)
}

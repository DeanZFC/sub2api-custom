package repository

import (
	"context"
	"encoding/json"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

var _ service.ScheduledTestProtectionDetailsRepository = (*scheduledTestResultRepository)(nil)

// One statement gives the hold, configuration, current results and ballots a
// consistent snapshot, even if a new round starts while the tooltip is loading.
func (r *scheduledTestResultRepository) ListProtectionHolds(ctx context.Context, accountID int64) ([]service.ScheduledTestProtectionHoldSource, error) {
	rows, err := r.db.QueryContext(ctx, `WITH holds AS (
 SELECT plan_id,account_id,test_definition_id,reason,updated_at,'[]'::jsonb AS rule_ids,trigger_event_id,model_key
 FROM scheduled_test_protection_states WHERE account_id=$1 AND blocked
 UNION ALL
 SELECT plan_id,account_id,0,reason,updated_at,rule_ids,trigger_event_id,model_key
 FROM scheduled_test_combination_states WHERE account_id=$1 AND blocked
 ) SELECT h.plan_id,p.name,h.test_definition_id,h.reason,h.updated_at,h.rule_ids,p.protection,
 COALESCE((SELECT jsonb_agg(jsonb_build_object(
 'test_definition_id',s.test_definition_id,'test_name',d.name,'round_started_at',s.round_started_at,'run_id',r.run_id,
 'current',s.completed AND s.result_started_at>=s.round_started_at
 AND r.id IS NOT NULL AND r.started_at=s.result_started_at AND r.run_id<>'' AND r.run_id=p.latest_run_id,
 'verdict',s.verdict,'automated_verdict',s.automated_verdict,'admin_verdict',s.admin_verdict,
 'reason',s.reason,'rule_config',s.rule_config,
 'result',CASE WHEN r.id IS NULL THEN NULL ELSE jsonb_build_object(
 'id',r.id,'started_at',r.started_at,'status',r.status,'output_kind',r.output_kind,'model_id',r.model_id,
 'response_text',r.response_text,'execution_info',r.execution_info,
 'output_numeric',r.output_numeric,'latency_ms',r.latency_ms) END,
 'pass_count',(SELECT COUNT(*) FROM scheduled_test_votes v WHERE v.result_id=s.result_id AND v.generation=s.generation AND v.vote='pass'),
 'fail_count',(SELECT COUNT(*) FROM scheduled_test_votes v WHERE v.result_id=s.result_id AND v.generation=s.generation AND v.vote='fail')
 ) ORDER BY d.sort_order,s.test_definition_id)
 FROM scheduled_test_protection_states s
 JOIN scheduled_test_definitions d ON d.id=s.test_definition_id
 LEFT JOIN scheduled_test_results r ON r.id=s.result_id
 WHERE s.plan_id=h.plan_id AND s.account_id=h.account_id AND s.model_key=h.model_key
 AND (h.test_definition_id=0 OR s.test_definition_id=h.test_definition_id)), '[]'::jsonb),
 (SELECT snapshot || jsonb_build_object('id',id) FROM scheduled_test_protection_events e WHERE e.id=h.trigger_event_id AND e.account_id=h.account_id AND e.plan_id=h.plan_id),h.model_key
 FROM holds h JOIN scheduled_test_plans p ON p.id=h.plan_id
 JOIN accounts a ON a.id=h.account_id
 WHERE a.deleted_at IS NULL AND a.status='quality_paused'
 ORDER BY (h.test_definition_id=0) DESC,h.updated_at DESC,h.plan_id,h.test_definition_id`, accountID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	items := make([]service.ScheduledTestProtectionHoldSource, 0)
	for rows.Next() {
		var item service.ScheduledTestProtectionHoldSource
		var idsRaw, configRaw, statesRaw, triggerRaw []byte
		if err := rows.Scan(&item.PlanID, &item.PlanName, &item.DefinitionID, &item.Reason, &item.UpdatedAt, &idsRaw, &configRaw, &statesRaw, &triggerRaw, &item.ModelID); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(idsRaw, &item.RuleIDs); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(configRaw, &item.Config); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(statesRaw, &item.States); err != nil {
			return nil, err
		}
		if len(triggerRaw) > 0 {
			if err := json.Unmarshal(triggerRaw, &item.TriggerSnapshot); err != nil {
				return nil, err
			}
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

package repository

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

// Called under the existing plan/account locks, in the same transaction as the
// decision. New holds get an immutable trigger; subsequent rounds keep it.
func recordProtectionEvidence(ctx context.Context, tx *sql.Tx, state *protectionState, before, after *protectionAccountSnapshot, decision service.ScheduledTestProtectionDecision) error {
	if !state.newHold && !state.newCombinedHold && decision.Status != "applied" && state.evidenceSource != "recovery" {
		return nil
	}
	source := service.ScheduledTestProtectionHoldSource{PlanID: state.planID, ModelID: state.modelKey, DefinitionID: state.definitionID, Reason: decision.Reason, UpdatedAt: time.Now().UTC()}
	var configRaw, statesRaw []byte
	var policyUpdatedAt time.Time
	var runID, modelID string
	err := tx.QueryRowContext(ctx, `SELECT name,protection,updated_at,
 COALESCE((SELECT run_id FROM scheduled_test_results WHERE id=$2),''),
 COALESCE((SELECT model_id FROM scheduled_test_results WHERE id=$2),$3)
 FROM scheduled_test_plans WHERE id=$1`, state.planID, state.resultID, state.modelKey).Scan(&source.PlanName, &configRaw, &policyUpdatedAt, &runID, &modelID)
	if err != nil {
		return err
	}
	if err = json.Unmarshal(configRaw, &source.Config); err != nil {
		return err
	}
	err = tx.QueryRowContext(ctx, `SELECT COALESCE(jsonb_agg(jsonb_build_object(
 'test_definition_id',s.test_definition_id,'test_name',d.name,'round_started_at',s.round_started_at,'run_id',r.run_id,
 'current',s.completed AND s.result_started_at>=s.round_started_at AND r.id IS NOT NULL
 AND r.started_at=s.result_started_at AND r.run_id<>'' AND r.run_id=p.latest_run_id,
 'verdict',s.verdict,'automated_verdict',s.automated_verdict,'admin_verdict',s.admin_verdict,
 'reason',s.reason,'rule_config',s.rule_config,
 'result',CASE WHEN r.id IS NULL THEN NULL ELSE jsonb_build_object(
 'id',r.id,'started_at',r.started_at,'status',r.status,'output_kind',r.output_kind,'model_id',r.model_id,
 'response_text',r.response_text,'execution_info',r.execution_info,'output_numeric',r.output_numeric,'latency_ms',r.latency_ms) END,
 'pass_count',(SELECT COUNT(*) FROM scheduled_test_votes v WHERE v.result_id=s.result_id AND v.generation=s.generation AND v.vote='pass'),
 'fail_count',(SELECT COUNT(*) FROM scheduled_test_votes v WHERE v.result_id=s.result_id AND v.generation=s.generation AND v.vote='fail')
 ) ORDER BY d.sort_order,s.test_definition_id),'[]'::jsonb)
 FROM scheduled_test_protection_states s JOIN scheduled_test_plans p ON p.id=s.plan_id
 JOIN scheduled_test_definitions d ON d.id=s.test_definition_id LEFT JOIN scheduled_test_results r ON r.id=s.result_id
 WHERE s.plan_id=$1 AND s.account_id=$2 AND s.model_key=$3`, state.planID, state.accountID, state.modelKey).Scan(&statesRaw)
	if err != nil {
		return err
	}
	if err = json.Unmarshal(statesRaw, &source.States); err != nil {
		return err
	}
	if state.evidenceSource == "recovery" {
		for i := range source.States {
			s := &source.States[i]
			if s.DefinitionID != state.definitionID {
				continue
			}
			// Trial statistics come from their own bounded observation window;
			// the original failed result must never stand in for that evidence.
			s.Current = state.recoveryEvidence != nil
			s.Verdict, s.Automated, s.Reason = decision.Verdict, decision.Verdict, decision.Reason
			if state.rule.Recovery != nil {
				s.Rule.MinSamples = int64(state.rule.Recovery.MinSamples)
			}
			if s.Current {
				s.Result = &service.ScheduledTestResult{ID: state.resultID, ModelID: state.modelKey, StartedAt: state.recoveryEvidence.WindowEnd, Status: "success", OutputStatistics: state.recoveryEvidence}
			}
		}
	}
	var actor *int64
	eventSource := state.evidenceSource
	if eventSource == "" {
		eventSource = "automatic"
		if state.adminVerdict != "" {
			eventSource = "administrator"
			if err = tx.QueryRowContext(ctx, `SELECT admin_user_id FROM scheduled_test_protection_states WHERE plan_id=$1 AND account_id=$2 AND test_definition_id=$3 AND model_key=$4`, state.planID, state.accountID, state.definitionID, state.modelKey).Scan(&actor); err != nil {
				return err
			}
		}
	}
	if state.evidenceActor != nil {
		actor = state.evidenceActor
	}
	policyRaw, err := json.Marshal(source.Config)
	if err != nil {
		return err
	}
	hash := sha256.Sum256(policyRaw)
	definitions := []int64{state.definitionID}
	if state.newCombinedHold || (!state.newHold && (source.Config.UsesCombinations() || source.Config.UsesAllPass())) {
		definitions = []int64{0}
		if state.newHold {
			definitions = append(definitions, state.definitionID)
		}
	}
	for _, definitionID := range definitions {
		source.DefinitionID = definitionID
		if definitionID == 0 {
			var ids []byte
			if err = tx.QueryRowContext(ctx, `SELECT COALESCE((SELECT rule_ids FROM scheduled_test_combination_states WHERE plan_id=$1 AND account_id=$2 AND model_key=$3),'[]'::jsonb)`, state.planID, state.accountID, state.modelKey).Scan(&ids); err != nil {
				return err
			}
			if err = json.Unmarshal(ids, &source.RuleIDs); err != nil {
				return err
			}
		}
		hold := service.BuildScheduledTestProtectionHold(source)
		event := service.ScheduledTestProtectionTrigger{OccurredAt: source.UpdatedAt, PlanID: state.planID, PlanName: source.PlanName,
			TestDefinitionID: definitionID, ResultID: state.resultID, RunID: runID, ModelID: modelID,
			PolicyVersion: hex.EncodeToString(hash[:]), PolicyUpdatedAt: policyUpdatedAt, Policy: source.Config,
			Source: eventSource, ActorUserID: actor, Reason: decision.Reason, Decision: decision,
			Before: protectionEvidenceAccount(before), After: protectionEvidenceAccount(after), Rules: hold.Rules, Tests: hold.Tests}
		raw, err := json.Marshal(event)
		if err != nil {
			return err
		}
		var eventID int64
		if err = tx.QueryRowContext(ctx, `INSERT INTO scheduled_test_protection_events(plan_id,account_id,test_definition_id,result_id,created_at,snapshot) VALUES($1,$2,$3,$4,$5,$6::jsonb) RETURNING id`,
			state.planID, state.accountID, definitionID, state.resultID, event.OccurredAt, raw).Scan(&eventID); err != nil {
			return err
		}
		if definitionID == 0 && state.newCombinedHold {
			_, err = tx.ExecContext(ctx, `UPDATE scheduled_test_combination_states SET trigger_event_id=$3 WHERE plan_id=$1 AND account_id=$2 AND model_key=$4 AND blocked`, state.planID, state.accountID, eventID, state.modelKey)
		} else if definitionID != 0 && state.newHold {
			_, err = tx.ExecContext(ctx, `UPDATE scheduled_test_protection_states SET trigger_event_id=$4 WHERE plan_id=$1 AND account_id=$2 AND test_definition_id=$3 AND model_key=$5 AND blocked`, state.planID, state.accountID, definitionID, eventID, state.modelKey)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func protectionEvidenceAccount(snapshot *protectionAccountSnapshot) service.ScheduledTestProtectionAccountState {
	return service.ScheduledTestProtectionAccountState{Status: snapshot.status, Schedulable: snapshot.schedulable, GroupIDs: sortedProtectionIDs(snapshot.groups)}
}

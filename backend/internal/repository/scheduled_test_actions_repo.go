package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
)

var _ service.ScheduledTestActionRepository = (*scheduledTestResultRepository)(nil)
var _ service.ScheduledTestActionRoundRepository = (*scheduledTestResultRepository)(nil)

// New runs freeze targets once. A result remains authorized for that run after
// automatic routing moves its account. Manual removal revokes that account's
// results without invalidating the rest of the round.
const protectionSourceMembershipSQL = `(r.run_id<>'' AND r.run_id=p.latest_run_id AND NOT r.protection_revoked)`

func (r *scheduledTestResultRepository) listPlanAccountIDs(ctx context.Context, plan *service.ScheduledTestPlan, accountID *int64, eligibleOnly bool) ([]int64, error) {
	if plan == nil {
		return nil, fmt.Errorf("test plan is required")
	}
	rows, err := r.db.QueryContext(ctx, `SELECT a.id FROM accounts a
 WHERE a.deleted_at IS NULL AND ($1::bigint IS NULL OR a.id=$1)
 AND NOT (a.id = ANY(COALESCE($4::bigint[], '{}')))
 AND (NOT $3 OR (a.schedulable AND a.status IN ('active','quality_paused')))
 AND EXISTS (SELECT 1 FROM account_groups ag JOIN groups g ON g.id=ag.group_id
             WHERE ag.account_id=a.id AND ag.group_id=ANY($2) AND g.deleted_at IS NULL AND g.status='active')
 ORDER BY a.id`, accountID, pq.Array(plan.GroupIDs), eligibleOnly, pq.Array(plan.ExcludedAccountIDs))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := make([]int64, 0)
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (r *scheduledTestResultRepository) ListPlanDetectionAccountIDs(ctx context.Context, plan *service.ScheduledTestPlan, accountID *int64) ([]int64, error) {
	return r.listPlanAccountIDs(ctx, plan, accountID, true)
}

func (r *scheduledTestResultRepository) ListPlanTargetAccountIDs(ctx context.Context, plan *service.ScheduledTestPlan, accountID *int64) ([]int64, error) {
	// Freeze only enabled detection targets. Manually disabled accounts must
	// not create queued/skipped results; quality-paused accounts still need
	// detection so their automatic protection can recover.
	return r.listPlanAccountIDs(ctx, plan, accountID, true)
}

func (r *scheduledTestResultRepository) IsPlanRunAccountEligible(ctx context.Context, planID int64, runID string, accountID int64) (bool, error) {
	if runID == "" {
		return false, nil
	}
	var eligible bool
	err := r.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM accounts a JOIN scheduled_test_plans p ON p.id=$1
 WHERE a.id=$3 AND a.deleted_at IS NULL AND a.schedulable AND a.status IN ('active','quality_paused')
 AND p.latest_run_id=$2 AND EXISTS(SELECT 1 FROM scheduled_test_results r
 WHERE r.plan_id=p.id AND r.run_id=$2 AND r.account_id=a.id AND NOT r.protection_revoked) AND `+scheduledTestRuntimeOwnershipSQL+`)`, planID, runID, accountID).Scan(&eligible)
	return eligible, err
}

func (r *scheduledTestPlanRepository) validateProtectionGroups(ctx context.Context, plan *service.ScheduledTestPlan) error {
	if !plan.HasGroupActions() {
		return nil
	}
	ids := make(map[int64]bool)
	for _, id := range plan.ProtectionActionGroupIDs() {
		ids[id] = true
	}
	selected := make(map[int64]bool, len(plan.GroupIDs))
	for _, id := range plan.GroupIDs {
		selected[id] = true
	}
	for id := range ids {
		if !selected[id] {
			return fmt.Errorf("quality target group %d must be selected in group_ids", id)
		}
	}
	var platform string
	if plan.GroupID != nil {
		if err := r.db.QueryRowContext(ctx, `SELECT platform FROM groups WHERE id=$1 AND deleted_at IS NULL`, *plan.GroupID).Scan(&platform); err != nil {
			return err
		}
	} else if plan.AccountID != nil {
		if err := r.db.QueryRowContext(ctx, `SELECT platform FROM accounts WHERE id=$1 AND deleted_at IS NULL`, *plan.AccountID).Scan(&platform); err != nil {
			return err
		}
	}
	// Composite groups do not directly own scheduler account pools.
	if platform == "" || platform == "composite" {
		return fmt.Errorf("quality group actions require a concrete source platform")
	}
	rows, err := r.db.QueryContext(ctx, `SELECT id,platform,status FROM groups WHERE id=ANY($1) AND deleted_at IS NULL`, pq.Array(sortedProtectionIDs(ids)))
	if err != nil {
		return err
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var id int64
		var targetPlatform, status string
		if err := rows.Scan(&id, &targetPlatform, &status); err != nil {
			return err
		}
		if targetPlatform != platform || status != "active" {
			return fmt.Errorf("quality target group %d must be active and use source platform %s", id, platform)
		}
		count++
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if count != len(ids) {
		return fmt.Errorf("quality target group no longer exists")
	}
	return nil
}

// Enroll all types before the first type finishes, so a quick statistics result
// cannot upgrade an account while its candy/pelican result has never been judged.
func initializeProtectionActionStates(ctx context.Context, tx *sql.Tx, planID, accountID int64, modelKey string, config *service.ScheduledTestProtectionConfig) error {
	for _, rule := range config.Rules {
		raw, err := json.Marshal(rule)
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO scheduled_test_protection_states
   (plan_id,account_id,test_definition_id,model_key,result_started_at,rule_config)
   VALUES ($1,$2,$3,$4,'epoch',$5::jsonb) ON CONFLICT (plan_id,account_id,test_definition_id,model_key) DO NOTHING`, planID, accountID, rule.TestDefinitionID, modelKey, raw); err != nil {
			return err
		}
	}
	return nil
}

type protectionRoutingRule struct {
	currentVerdict string
	automated      string
	adminVerdict   string
	rule           service.ScheduledTestProtectionRule
}

type protectionRoutingChange struct {
	scope, desired map[int64]bool
	rules          []protectionRoutingRule
}

// Routing is calculated entirely from the current round. Required automatic
// checks gate every promotion; administrator judgments then outrank normal
// tiers. At a tier, failures win and all automatic checks must pass before
// promotion. Pending ballots do not stop a lower tier from being applied.
func protectionRoutingChanges(rules []protectionRoutingRule, current map[int64]bool) []protectionRoutingChange {
	passBlocked := false
	var hardFailures, admins []protectionRoutingRule
	for _, entry := range rules {
		if entry.rule.RequiredPass && entry.automated != "pass" {
			passBlocked = true
			if entry.automated == "fail" {
				entry.currentVerdict = "fail"
				hardFailures = append(hardFailures, entry)
			}
		}
		if entry.adminVerdict == "pass" || entry.adminVerdict == "fail" {
			entry.currentVerdict = entry.adminVerdict
			admins = append(admins, entry)
		}
	}
	resolve := func(entries []protectionRoutingRule, verdict string) []protectionRoutingChange {
		desired := map[int64]bool{}
		assign := false
		for _, entry := range entries {
			if entry.currentVerdict != verdict {
				continue
			}
			action := entry.rule.OutcomeAction(verdict)
			if action.GroupMode == "assign" {
				assign = true
				for _, id := range action.GroupIDs {
					desired[id] = true
				}
			}
		}
		if !assign {
			return nil
		}
		scope := map[int64]bool{}
		for id := range current {
			scope[id] = true
		}
		for id := range desired {
			scope[id] = true
		}
		return []protectionRoutingChange{{scope: scope, desired: desired, rules: entries}}
	}
	if changes := resolve(hardFailures, "fail"); len(changes) > 0 {
		return changes
	}
	// A rule with no group action can gate automatic peers at the same priority,
	// but it cannot manufacture a separate routing tier by itself.
	hasAction := func(entry protectionRoutingRule, verdict string) bool {
		action := entry.rule.OnPass
		if verdict == "fail" {
			action = entry.rule.OnFail
		}
		if entry.rule.RequiredPass && entry.automated != "pass" && (action == nil || action.GroupMode != "assign") {
			return false
		}
		return action != nil && (action.GroupMode == "assign" || action.GroupMode == "keep")
	}
	selectTier := func(entries []protectionRoutingRule) ([]protectionRoutingChange, bool) {
		priorities := map[int]bool{}
		for _, entry := range entries {
			priorities[entry.rule.Priority] = true
		}
		levels := make([]int, 0, len(priorities))
		for priority := range priorities {
			levels = append(levels, priority)
		}
		sort.Sort(sort.Reverse(sort.IntSlice(levels)))
		for _, priority := range levels {
			var tier []protectionRoutingRule
			failed, pending, pass := false, false, false
			failAction, passAction := false, false
			for _, entry := range entries {
				if entry.rule.Priority != priority {
					continue
				}
				vote := entry.rule.Vote != nil && entry.rule.Vote.Enabled
				if vote && entry.currentVerdict != "pass" && entry.currentVerdict != "fail" {
					continue
				}
				tier = append(tier, entry)
				switch entry.currentVerdict {
				case "fail":
					failed = true
					failAction = failAction || hasAction(entry, "fail")
				case "pass":
					pass = true
					passAction = passAction || hasAction(entry, "pass")
				default:
					pending = true
				}
			}
			if failed {
				if failAction {
					return resolve(tier, "fail"), true
				}
				continue
			}
			if !passBlocked && !pending && pass && passAction {
				return resolve(tier, "pass"), true
			}
		}
		return nil, false
	}
	if changes, decided := selectTier(admins); decided {
		return changes
	}
	changes, _ := selectTier(rules)
	return changes
}

func sortedProtectionIDs(ids map[int64]bool) []int64 {
	out := make([]int64, 0, len(ids))
	for id := range ids {
		out = append(out, id)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// Expressions are evaluated inside one model only. Group memberships belong
// to the account, so their final assignment is resolved across those decisions.
type protectionModelRouting struct {
	verdicts    map[int64]string
	held        bool
	accountWide bool
	decision    service.ScheduledTestCombinationDecision
	reason      string
}

func protectionModelGroupChanges(config service.ScheduledTestProtectionConfig, models map[string]*protectionModelRouting, current map[int64]bool) []protectionRoutingChange {
	type candidate struct {
		action   *service.ScheduledTestOutcomeAction
		priority int
		unsafe   bool
	}
	var candidates []candidate
	unresolved, unsafe := false, false
	for _, model := range models {
		action := model.decision.Action
		failed := false
		for _, verdict := range model.verdicts {
			failed = failed || verdict == "fail"
		}
		pending := action == nil
		if config.UsesAllPass() {
			for _, rule := range config.Rules {
				if (rule.Vote == nil || !rule.Vote.Enabled || rule.HasDurationThreshold() || rule.UsesAutoReview()) && model.verdicts[rule.TestDefinitionID] != "pass" && model.verdicts[rule.TestDefinitionID] != "fail" {
					pending = true
				}
			}
		}
		unresolved = unresolved || pending
		if action == nil {
			// A previous unresolved model hold still prevents promotion, even
			// when no expression currently resolves to a replacement action.
			unsafe = unsafe || model.held
			continue
		}
		isUnsafe := failed || action.Scheduling == "pause" || (model.held && action.Scheduling != "resume")
		unsafe = unsafe || isUnsafe
		priority := 0
		for _, id := range model.decision.RuleIDs {
			for _, rule := range config.Combinations {
				if rule.ID == id && rule.Priority > priority {
					priority = rule.Priority
				}
			}
		}
		candidates = append(candidates, candidate{action: action, priority: priority, unsafe: isUnsafe})
	}
	if len(models) == 0 || (!unsafe && unresolved) {
		return nil
	}
	priority := -1
	desired := map[int64]bool{}
	assigned := false
	for _, candidate := range candidates {
		if candidate.unsafe != unsafe || candidate.priority < priority {
			continue
		}
		if candidate.priority > priority {
			priority = candidate.priority
			desired = map[int64]bool{}
			assigned = false
		}
		if candidate.action.GroupMode == "assign" {
			assigned = true
			for _, id := range candidate.action.GroupIDs {
				desired[id] = true
			}
		}
	}
	if !assigned {
		return nil
	}
	scope := map[int64]bool{}
	for id := range current {
		scope[id] = true
	}
	for id := range desired {
		scope[id] = true
	}
	return []protectionRoutingChange{{scope: scope, desired: desired}}
}

func reconcileProtectionGroups(ctx context.Context, tx *sql.Tx, state *protectionState) error {
	owned, err := scheduledTestAccountOwnershipCurrent(ctx, tx, state.planID, state.accountID)
	if err != nil {
		return err
	}
	if !owned {
		return fmt.Errorf("account belongs to conflicting enabled test strategies")
	}

	var config service.ScheduledTestProtectionConfig
	var configRaw []byte
	if err := tx.QueryRowContext(ctx, `SELECT protection FROM scheduled_test_plans WHERE id=$1`, state.planID).Scan(&configRaw); err != nil {
		return err
	}
	if err := json.Unmarshal(configRaw, &config); err != nil {
		return err
	}
	// All-pass inspects every current-round check before admission.
	if !config.UsesAllPass() {
		current, err := protectionResultCurrent(ctx, tx, state.resultID)
		if err != nil || !current {
			return err
		}
	}
	rows, err := tx.QueryContext(ctx, `SELECT s.model_key,s.rule_config,s.verdict,s.automated_verdict,s.admin_verdict,
 s.completed AND s.result_started_at>=s.round_started_at AND EXISTS (
 SELECT 1 FROM scheduled_test_results evidence WHERE evidence.id=s.result_id
 AND evidence.started_at=s.result_started_at AND evidence.run_id<>'' AND evidence.run_id=p.latest_run_id),
 (SELECT latency_ms FROM scheduled_test_results WHERE id=s.result_id),
 (SELECT status FROM scheduled_test_results WHERE id=s.result_id),d.output_kind
 FROM scheduled_test_protection_states s JOIN scheduled_test_plans p ON p.id=s.plan_id
 JOIN scheduled_test_definitions d ON d.id=s.test_definition_id
 WHERE s.plan_id=$1 AND s.account_id=$2 AND s.round_started_at=$3
 AND s.model_key=ANY(CASE WHEN cardinality(p.model_ids)>0 THEN p.model_ids ELSE ARRAY[p.model_id] END)
 AND p.enabled AND p.protection->>'enabled'='true'
 ORDER BY s.model_key,s.test_definition_id`, state.planID, state.accountID, state.roundStarted)
	if err != nil {
		return err
	}
	models := map[string]*protectionModelRouting{}
	var rules []protectionRoutingRule
	for rows.Next() {
		var entry protectionRoutingRule
		var modelKey, outputKind string
		var raw []byte
		var evidenceCurrent bool
		var latency sql.NullInt64
		var resultStatus sql.NullString
		if err := rows.Scan(&modelKey, &raw, &entry.currentVerdict, &entry.automated, &entry.adminVerdict, &evidenceCurrent, &latency, &resultStatus, &outputKind); err != nil {
			rows.Close()
			return err
		}
		if err := json.Unmarshal(raw, &entry.rule); err != nil {
			rows.Close()
			return err
		}
		if !protectionRuleCurrent(&config, entry.rule) {
			continue
		}
		model := models[modelKey]
		if model == nil {
			model = &protectionModelRouting{verdicts: map[int64]string{}}
			models[modelKey] = model
		}
		if !evidenceCurrent {
			entry.currentVerdict, entry.automated, entry.adminVerdict = "pending", "pending", ""
		}
		rules = append(rules, entry)
		if evidenceCurrent {
			model.verdicts[entry.rule.TestDefinitionID] = entry.currentVerdict
			if config.UsesAllPass() && entry.rule.Vote != nil && entry.rule.Vote.Enabled && !entry.rule.UsesAutoReview() {
				// Manual review is a veto; automatic visual evidence remains automatic.
				model.verdicts[entry.rule.TestDefinitionID] = entry.adminVerdict
				if entry.rule.HasDurationThreshold() {
					ms := int64(-1)
					if latency.Valid {
						ms = latency.Int64
					}
					verdict, _ := service.EvaluateScheduledTestDuration(entry.rule, &service.ScheduledTestResult{Status: resultStatus.String, LatencyMs: ms})
					if entry.adminVerdict != "fail" {
						model.verdicts[entry.rule.TestDefinitionID] = verdict
					}
				}
			}
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	before, err := readProtectionAccountSnapshot(ctx, tx, state.accountID)
	if err != nil {
		return err
	}
	var changes []protectionRoutingChange
	if config.UsesCombinations() || config.UsesAllPass() {
		// A model owns its own scheduling hold. Completing one model must not
		// release or replace the hold/condition evidence of another model.
		rows, err := tx.QueryContext(ctx, `SELECT model_key,blocked,account_wide FROM scheduled_test_combination_states WHERE plan_id=$1 AND account_id=$2`, state.planID, state.accountID)
		if err != nil {
			return err
		}
		for rows.Next() {
			var modelKey string
			var held, accountWide bool
			if err := rows.Scan(&modelKey, &held, &accountWide); err != nil {
				_ = rows.Close()
				return err
			}
			if model := models[modelKey]; model != nil {
				model.held, model.accountWide = held, accountWide
			}
		}
		err = rows.Err()
		closeErr := rows.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
		keys := make([]string, 0, len(models))
		for key := range models {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			model := models[key]
			if config.UsesAllPass() {
				held := model.held
				if key == state.modelKey {
					held = held || state.blocked
				}
				model.decision, model.reason = service.EvaluateScheduledTestAllPass(config, model.verdicts, nil, held)
			} else {
				model.decision = service.EvaluateScheduledTestCombinations(config, model.verdicts)
				labels := make([]string, 0, len(model.decision.RuleIDs))
				for _, id := range model.decision.RuleIDs {
					label := id
					for _, combination := range config.Combinations {
						if combination.ID == id && combination.Name != "" {
							label = combination.Name
							break
						}
					}
					labels = append(labels, label)
				}
				model.reason = "命中组合规则：" + strings.Join(labels, "、")
			}
			if key == state.modelKey {
				state.actionReason = model.reason
				if err := applyCombinationScheduling(ctx, tx, state, model.decision, model.accountWide); err != nil {
					return err
				}
			}
		}
		changes = protectionModelGroupChanges(config, models, before.groups)
	} else {
		// Generic priority rules consist of independent test leaves. Feeding
		// every model's leaves through the existing selector makes failures
		// win at the same priority and required passes gate every promotion.
		changes = protectionRoutingChanges(rules, before.groups)
	}
	if len(changes) == 0 {
		return nil
	}
	// Group membership is account-wide. A single failed model must not move
	// healthy peers into another group or remove their source membership.
	projection, err := readActiveTestProtection(ctx, tx, state.accountID)
	if err != nil {
		return err
	}
	if string(projection.modelHolds) != "{}" && !projection.allModelsBlocked && !projection.accountWide {
		return nil
	}
	change := changes[0]
	if reflect.DeepEqual(before.groups, change.desired) {
		return nil
	}
	// Lock old and new groups in a common order, including deleted memberships
	// that a full replacement is allowed to remove.
	rows, err = tx.QueryContext(ctx, `SELECT id FROM groups WHERE id=ANY($1) ORDER BY id FOR SHARE`, pq.Array(sortedProtectionIDs(change.scope)))
	if err != nil {
		return err
	}
	for rows.Next() {
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	var compatible int
	err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM groups g JOIN accounts a ON a.id=$1 JOIN scheduled_test_plans p ON p.id=$3
 WHERE g.id=ANY($2) AND g.id=ANY(p.group_ids) AND g.deleted_at IS NULL AND g.status='active'
 AND g.platform=a.platform AND g.platform<>'composite'`, state.accountID, pq.Array(sortedProtectionIDs(change.desired)), state.planID).Scan(&compatible)
	if err != nil {
		return err
	}
	if compatible != len(change.desired) {
		return fmt.Errorf("quality target groups must be active, selected and use the account platform")
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM account_groups WHERE account_id=$1 AND NOT(group_id=ANY($2))`, state.accountID, pq.Array(sortedProtectionIDs(change.desired))); err != nil {
		return err
	}
	for _, id := range sortedProtectionIDs(change.desired) {
		if _, err = tx.ExecContext(ctx, `INSERT INTO account_groups(account_id,group_id,priority,created_at) VALUES($1,$2,50,NOW()) ON CONFLICT(account_id,group_id) DO NOTHING`, state.accountID, id); err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx, `UPDATE accounts SET updated_at=NOW() WHERE id=$1`, state.accountID); err != nil {
		return err
	}
	return enqueueSchedulerOutbox(ctx, tx, service.SchedulerOutboxEventAccountGroupsChanged, &state.accountID, nil, buildSchedulerGroupPayload(sortedProtectionIDs(change.scope)))
}

func applyCombinationScheduling(ctx context.Context, tx *sql.Tx, state *protectionState, decision service.ScheduledTestCombinationDecision, accountWide bool) error {
	if decision.Action == nil || (decision.Action.Scheduling != "pause" && decision.Action.Scheduling != "resume") {
		return nil
	}
	raw, err := json.Marshal(decision.RuleIDs)
	if err != nil {
		return err
	}
	blocked := decision.Action.Scheduling == "pause"
	var wasBlocked bool
	err = tx.QueryRowContext(ctx, `SELECT COALESCE((SELECT blocked FROM scheduled_test_combination_states WHERE plan_id=$1 AND account_id=$2 AND model_key=$3),FALSE)`, state.planID, state.accountID, state.modelKey).Scan(&wasBlocked)
	if err != nil {
		return err
	}
	state.newCombinedHold = blocked && !wasBlocked
	_, err = tx.ExecContext(ctx, `INSERT INTO scheduled_test_combination_states(plan_id,account_id,model_key,blocked,reason,rule_ids,account_wide)
 VALUES($1,$2,$3,$4,$5,$6::jsonb,$7) ON CONFLICT(plan_id,account_id,model_key) DO UPDATE SET
	 trigger_event_id=CASE WHEN EXCLUDED.blocked AND scheduled_test_combination_states.blocked THEN scheduled_test_combination_states.trigger_event_id ELSE NULL END,
 blocked=EXCLUDED.blocked,reason=EXCLUDED.reason,rule_ids=EXCLUDED.rule_ids,account_wide=EXCLUDED.account_wide,updated_at=NOW()`,
		state.planID, state.accountID, state.modelKey, blocked, state.actionReason, raw, blocked && accountWide)
	return err
}

// The execution snapshot, rather than live memberships, establishes every
// account in this round. Reset all previous states, including removed accounts,
// so an old review cannot route an account after the next round has begun.
func (r *scheduledTestResultRepository) BeginProtectionRun(ctx context.Context, plan *service.ScheduledTestPlan, started time.Time) error {
	if plan == nil || !plan.Enabled || !plan.Protection.Enabled {
		return nil
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	current, err := scanPlan(tx.QueryRowContext(ctx, `SELECT id, name, sort_order, account_id, group_id, test_definition_id, test_type, target_mode, model_id, reasoning_effort, cron_expression, enabled, max_results, auto_recover, last_run_at, next_run_at, created_at, updated_at, test_definition_ids, protection, group_ids, migration_note, schedule_config, excluded_account_ids, model_ids FROM scheduled_test_plans WHERE id=$1 FOR NO KEY UPDATE`, plan.ID))
	if err != nil {
		return err
	}
	if !scheduledTestSameExecutionPolicy(current, plan) {
		return fmt.Errorf("quality plan changed before execution")
	}
	config := &current.Protection
	rows, err := tx.QueryContext(ctx, `SELECT DISTINCT r.account_id,r.model_id FROM scheduled_test_results r JOIN scheduled_test_plans p ON p.id=r.plan_id
 WHERE r.plan_id=$1 AND r.run_id=p.latest_run_id AND r.run_id<>'' AND NOT r.protection_revoked AND r.account_id IS NOT NULL ORDER BY r.account_id,r.model_id`, plan.ID)
	if err != nil {
		return err
	}
	type modelTarget struct {
		accountID int64
		modelKey  string
	}
	var targets []modelTarget
	for rows.Next() {
		var target modelTarget
		if err := rows.Scan(&target.accountID, &target.modelKey); err != nil {
			rows.Close()
			return err
		}
		targets = append(targets, target)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, target := range targets {
		eligible, err := lockProtectionAccount(ctx, tx, target.accountID)
		if err != nil {
			return err
		}
		if eligible {
			if err := initializeProtectionActionStates(ctx, tx, plan.ID, target.accountID, target.modelKey, config); err != nil {
				return err
			}
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE scheduled_test_protection_states SET round_started_at=$2,
 automated_verdict='pending',verdict='pending',completed=FALSE,generation=generation+1,
 admin_verdict='',admin_user_id=NULL,admin_decided_at=NULL,updated_at=NOW()
 WHERE plan_id=$1 AND round_started_at<$2`, plan.ID, started.Round(time.Microsecond)); err != nil {
		return err
	}
	return tx.Commit()
}

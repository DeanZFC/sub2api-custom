package repository

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"sort"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
)

// Whole-round semantics reuse the individual capability fingerprint and also
// include duration limits, which are independent of cognitive pass-rate gates
// but still mandatory for a successful all-pass round.
func scheduledTestAllPassVersion(ctx context.Context, tx qualityQuery, planID int64, config service.ScheduledTestProtectionConfig) (string, error) {
	rules := append([]service.ScheduledTestProtectionRule(nil), config.Rules...)
	sort.Slice(rules, func(i, j int) bool { return rules[i].TestDefinitionID < rules[j].TestDefinitionID })
	inputs := []any{"all-pass-round-v1"}
	for _, rule := range rules {
		version, err := scheduledTestPassRateVersion(ctx, tx, planID, rule)
		if err != nil {
			return "", err
		}
		inputs = append(inputs, []any{rule.TestDefinitionID, version, rule.Thresholds})
	}
	raw, err := json.Marshal(inputs)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

// A newly enrolled pending round does not discard the preceding successful
// streak. Throttling may resume after N actual successful rounds, but must not
// create deferred pending rounds before that threshold has been reached.
func scheduledTestAllPassNeedsFreshRound(ctx context.Context, q qualityQuery, planID, accountID int64) (bool, error) {
	var needed bool
	err := q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM scheduled_test_plans p
 WHERE p.id=$1 AND p.protection->>'enabled'='true' AND p.protection->>'mode'='all_pass'
 AND (EXISTS(SELECT 1 FROM scheduled_test_combination_states s WHERE s.plan_id=p.id AND s.account_id=$2 AND s.blocked)
 OR EXISTS(SELECT 1 FROM unnest(CASE WHEN cardinality(p.model_ids)>0 THEN p.model_ids ELSE ARRAY[p.model_id] END) model(key)
 LEFT JOIN LATERAL (SELECT h.id,h.verdict,h.rule_version FROM scheduled_test_all_pass_rounds h
 WHERE h.plan_id=p.id AND h.account_id=$2 AND h.model_key=model.key ORDER BY h.started_at DESC,h.id DESC LIMIT 1) latest ON TRUE
 LEFT JOIN LATERAL (SELECT COUNT(*) FILTER(WHERE windowed.verdict='pass' AND windowed.failures=0) AS passed FROM(
 SELECT recent.verdict,COUNT(*) FILTER(WHERE recent.verdict<>'pass' OR recent.rule_version<>latest.rule_version) OVER(ORDER BY recent.started_at DESC,recent.id DESC) AS failures FROM(
 SELECT h.verdict,h.started_at,h.id,h.rule_version FROM scheduled_test_all_pass_rounds h WHERE h.plan_id=p.id AND h.account_id=$2 AND h.model_key=model.key
 AND (latest.verdict<>'pending' OR h.id<>latest.id) ORDER BY h.started_at DESC,h.id DESC LIMIT 100) recent) windowed) streak ON TRUE
 WHERE latest.id IS NULL OR COALESCE(streak.passed,0)<GREATEST(1,COALESCE((p.protection->>'required_consecutive_passes')::int,1)))))`, planID, accountID).Scan(&needed)
	return needed, err
}

// The plan/account locks already held by protection completion serialize every
// update. Only the triggering model's current, non-revoked run may be changed.
func reconcileScheduledTestAllPassProgress(ctx context.Context, tx *sql.Tx, state *protectionState, model string, config service.ScheduledTestProtectionConfig, version, verdict string, verdicts map[int64]string, update bool) (service.ScheduledTestAllPassProgress, error) {
	progress := service.ScheduledTestAllPassProgress{Required: config.AllPassRequiredConsecutivePasses(), RoundVerdict: verdict}
	var currentVersion string
	var failedIDs pq.Int64Array
	err := tx.QueryRowContext(ctx, `SELECT h.rule_version,h.failed_definition_ids FROM scheduled_test_all_pass_rounds h JOIN scheduled_test_plans p ON p.id=h.plan_id
 WHERE h.plan_id=$1 AND h.account_id=$2 AND h.model_key=$3 AND h.run_id=p.latest_run_id`, state.planID, state.accountID, model).Scan(&currentVersion, &failedIDs)
	if err != nil && err != sql.ErrNoRows {
		return progress, err
	}
	if err == nil && currentVersion != version {
		progress.RoundVerdict = "pending"
		return progress, nil
	}
	if update {
		failed := map[int64]bool{}
		if progress.Required > 1 {
			for _, id := range failedIDs {
				failed[id] = true
			}
			if state.evidenceSource == "administrator" && state.adminVerdict == "pass" {
				delete(failed, state.definitionID)
			}
			for id, saved := range verdicts {
				if saved == "fail" {
					failed[id] = true
				}
			}
			if len(failed) > 0 {
				verdict = "fail"
			}
		}
		if _, err := tx.ExecContext(ctx, `UPDATE scheduled_test_all_pass_rounds h SET verdict=$5,failed_definition_ids=$7,updated_at=NOW()
 FROM scheduled_test_plans p,scheduled_test_results r WHERE h.plan_id=p.id AND h.plan_id=$1
 AND h.account_id=$2 AND h.model_key=$3 AND h.run_id=p.latest_run_id
 AND r.id=$4 AND r.plan_id=h.plan_id AND r.account_id=h.account_id AND r.model_id=h.model_key
 AND r.run_id=h.run_id AND NOT r.protection_revoked AND h.rule_version=$6`, state.planID, state.accountID, model, state.resultID, verdict, version, pq.Array(sortedProtectionIDs(failed))); err != nil {
			return progress, err
		}
	}
	rows, err := tx.QueryContext(ctx, `SELECT verdict,rule_version FROM scheduled_test_all_pass_rounds
 WHERE plan_id=$1 AND account_id=$2 AND model_key=$3 ORDER BY started_at DESC,id DESC LIMIT 101`, state.planID, state.accountID, model)
	if err != nil {
		return progress, err
	}
	defer rows.Close()
	first := true
	for rows.Next() {
		var saved, savedVersion string
		if err := rows.Scan(&saved, &savedVersion); err != nil {
			return progress, err
		}
		if savedVersion != version {
			break
		}
		if first {
			first = false
			progress.RoundVerdict = saved
			// A pending current round may display the preceding streak; it
			// still cannot execute the pass action until it resolves to pass.
			if saved == "pending" {
				continue
			}
		}
		if saved != "pass" {
			break
		}
		progress.ConsecutivePasses++
		if progress.ConsecutivePasses >= progress.Required {
			break
		}
	}
	if err := rows.Err(); err != nil {
		return progress, err
	}
	// Legacy fixtures/results without round enrollment retain the N=1 path.
	if first && progress.Required == 1 && verdict == "pass" {
		progress.ConsecutivePasses = 1
	}
	return progress, nil
}

// A single bulk query returns current progress for authorized result IDs;
// historical cards deliberately see the current policy window rather than a
// statistic reconstructed at the historical result's timestamp.
func (r *scheduledTestResultRepository) hydrateAllPassProgress(ctx context.Context, results []*service.ScheduledTestResult) error {
	ids := make([]int64, 0, len(results))
	byID := map[int64][]*service.ScheduledTestResult{}
	for _, result := range results {
		if result == nil {
			continue
		}
		if _, ok := byID[result.ID]; !ok {
			ids = append(ids, result.ID)
		}
		byID[result.ID] = append(byID[result.ID], result)
	}
	if len(ids) == 0 {
		return nil
	}
	rows, err := r.db.QueryContext(ctx, `SELECT r.id,GREATEST(1,COALESCE((p.protection->>'required_consecutive_passes')::int,1)),h.verdict,
 COALESCE(s.blocked,FALSE),COALESCE(streak.count,0),p.id,p.protection,h.rule_version
 FROM scheduled_test_results r JOIN scheduled_test_plans p ON p.id=r.plan_id
 JOIN scheduled_test_all_pass_rounds h ON h.plan_id=p.id AND h.account_id=r.account_id AND h.model_key=r.model_id AND h.run_id=p.latest_run_id
 LEFT JOIN scheduled_test_combination_states s ON s.plan_id=p.id AND s.account_id=r.account_id AND s.model_key=r.model_id
 LEFT JOIN LATERAL (SELECT COUNT(*) FILTER(WHERE previous.verdict='pass' AND previous.failures=0) AS count FROM (
   SELECT recent.verdict,COUNT(*) FILTER(WHERE recent.verdict<>'pass' OR recent.rule_version<>h.rule_version) OVER(ORDER BY recent.started_at DESC,recent.id DESC) AS failures
   FROM (SELECT hist.id,hist.verdict,hist.started_at,hist.rule_version FROM scheduled_test_all_pass_rounds hist
     WHERE hist.plan_id=p.id AND hist.account_id=r.account_id AND hist.model_key=r.model_id
     AND (hist.started_at,hist.id)<=(h.started_at,h.id)
     AND (h.verdict<>'pending' OR hist.id<>h.id)
     ORDER BY hist.started_at DESC,hist.id DESC LIMIT 100) recent
 ) previous) streak ON TRUE
 WHERE r.id=ANY($1) AND p.enabled AND p.protection->>'enabled'='true' AND p.protection->>'mode'='all_pass'
 AND r.model_id=ANY(CASE WHEN cardinality(p.model_ids)>0 THEN p.model_ids ELSE ARRAY[p.model_id] END)
 AND NOT(r.account_id=ANY(p.excluded_account_ids))`, pq.Array(ids))
	if err != nil {
		return err
	}
	type foundProgress struct {
		id, planID int64
		progress   service.ScheduledTestAllPassProgress
		config     service.ScheduledTestProtectionConfig
		version    string
	}
	var found []foundProgress
	for rows.Next() {
		var row foundProgress
		var raw []byte
		progress := &row.progress
		if err := rows.Scan(&row.id, &progress.Required, &progress.RoundVerdict, &progress.Blocked, &progress.ConsecutivePasses, &row.planID, &raw, &row.version); err != nil {
			_ = rows.Close()
			return err
		}
		if err := json.Unmarshal(raw, &row.config); err != nil {
			_ = rows.Close()
			return err
		}
		found = append(found, row)
	}
	err = rows.Err()
	if closeErr := rows.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	versions := map[int64]string{}
	for _, row := range found {
		version, ok := versions[row.planID]
		if !ok {
			version, err = scheduledTestAllPassVersion(ctx, r.db, row.planID, row.config)
			if err != nil {
				return err
			}
			versions[row.planID] = version
		}
		progress := row.progress
		if version != row.version {
			progress.ConsecutivePasses = 0
			progress.RoundVerdict = "pending"
		}
		if progress.ConsecutivePasses > progress.Required {
			progress.ConsecutivePasses = progress.Required
		}
		for _, result := range byID[row.id] {
			copy := progress
			result.AllPassProgress = &copy
		}
	}
	return nil
}

package repository

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
)

// Version only evaluation semantics: changing allocation/actions, a label or
// pass-rate limits must not rewrite historical ability results.
func scheduledTestPassRateVersion(ctx context.Context, tx *sql.Tx, planID int64, rule service.ScheduledTestProtectionRule) (string, error) {
	var prompt, kind, reasoning string
	var maxTokens int
	if err := tx.QueryRowContext(ctx, `SELECT d.prompt,d.output_kind,d.max_output_tokens,p.reasoning_effort
 FROM scheduled_test_definitions d JOIN scheduled_test_plans p ON p.id=$1 WHERE d.id=$2`, planID, rule.TestDefinitionID).Scan(&prompt, &kind, &maxTokens, &reasoning); err != nil {
		return "", err
	}
	semantic := protectionRuleWithoutPublicVote(rule)
	semantic.PassRate = nil
	semantic.Recovery = nil
	semantic.OnPass = nil
	semantic.OnFail = nil
	semantic.Priority = 0
	semantic.RequiredPass = false
	semantic.PauseOnFailure = false
	thresholds := make([]service.ScheduledTestThreshold, 0, len(semantic.Thresholds))
	for _, v := range semantic.Thresholds {
		if v.Metric != "latency_ms" {
			thresholds = append(thresholds, v)
		}
	}
	semantic.Thresholds = thresholds
	var reviewSemantics any
	if rule.UsesAutoReview() {
		config, err := qualityConfig(ctx, tx, false)
		if err != nil {
			return "", err
		}
		if config.PelicanReview != nil {
			reviewSemantics = []any{service.ScheduledTestReviewRubricVersion, config.PelicanReview.JudgeAccountID, config.PelicanReview.JudgeModel}
		}
	}
	// Preserve historical versions for rules that do not use visual review.
	inputs := []any{"active-pass-rate-v1", prompt, kind, maxTokens, reasoning, semantic}
	if rule.UsesAutoReview() {
		inputs = append(inputs, reviewSemantics)
	}
	raw, err := json.Marshal(inputs)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

func beginScheduledTestPassRate(ctx context.Context, tx *sql.Tx, result *service.ScheduledTestResult, rule service.ScheduledTestProtectionRule) error {
	if rule.PassRate == nil || !rule.PassRate.Enabled {
		return nil
	}
	if actual, ok := service.ScheduledTestPassRateSnapshotFromContext(ctx); ok {
		var prompt, kind string
		var maxTokens int
		if err := tx.QueryRowContext(ctx, `SELECT prompt,output_kind,max_output_tokens FROM scheduled_test_definitions WHERE id=$1 FOR SHARE`, rule.TestDefinitionID).Scan(&prompt, &kind, &maxTokens); err != nil {
			return err
		}
		if kind == "model_check" {
			prompt = "Please reply with OK."
		}
		if prompt != actual.Prompt || kind != actual.Kind || maxTokens != actual.MaxOutputTokens {
			return fmt.Errorf("test definition changed before execution; wait for a fresh round")
		}
	}
	version, err := scheduledTestPassRateVersion(ctx, tx, result.PlanID, rule)
	if err != nil {
		return err
	}
	// Retries keep the original version as well as the result and run identity.
	_, err = tx.ExecContext(ctx, `UPDATE scheduled_test_results SET pass_rate_rule_version=$2 WHERE id=$1 AND pass_rate_rule_version=''`, result.ID, version)
	return err
}

func recordScheduledTestPassRate(ctx context.Context, tx *sql.Tx, state *protectionState) error {
	config := state.rule.PassRate
	if config == nil || !config.Enabled || !state.completed {
		return nil
	}
	var raw []byte
	var version, run string
	if err := tx.QueryRowContext(ctx, `SELECT to_jsonb(r),r.pass_rate_rule_version,r.run_id FROM scheduled_test_results r WHERE r.id=$1`, state.resultID).Scan(&raw, &version, &run); err != nil {
		return err
	}
	result := &service.ScheduledTestResult{}
	if err := json.Unmarshal(raw, result); err != nil {
		return err
	}
	if result.OutputKind == "statistics" {
		return nil
	}
	current, err := scheduledTestPassRateVersion(ctx, tx, state.planID, state.rule)
	if err != nil {
		return err
	}
	if version == "" || version != current {
		return nil
	}
	verdict, reason := service.EvaluateScheduledTestPassRateVerdict(state.rule, result)
	cognitive := *state
	cognitive.automated = verdict
	cognitive.reason = reason
	cognitive.rule = state.rule
	cognitive.rule.Thresholds = nil
	pass, fail, err := protectionVoteCounts(ctx, tx, state)
	if err != nil {
		return err
	}
	verdict, _, _ = combineProtectionVerdict(&cognitive, pass, fail)
	// An unavailable transport cannot be converted into a valid capability
	// observation, including by an execution-failure action.
	if result.Status != "success" && result.Status != "passed" && (result.ExecutionInfo == nil || !result.ExecutionInfo.OutputContractFailed) {
		verdict = "pending"
	}
	if run == "" {
		run = fmt.Sprintf("result:%d", result.ID)
	}
	freshEvidence := false
	if verdict == "pass" || verdict == "fail" {
		// A round can become more conservative after review; retries can never
		// overwrite a valid failure with a pass or count the round a second time.
		inserted, insertErr := tx.ExecContext(ctx, `INSERT INTO scheduled_test_pass_rate_evidence
   (result_id,plan_id,account_id,test_definition_id,model_key,rule_version,run_key,verdict,started_at)
   VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)
   ON CONFLICT DO NOTHING`, state.resultID, state.planID, state.accountID, state.definitionID, result.ModelID, version, run, verdict, state.started)
		if insertErr != nil {
			return insertErr
		}
		count, err := inserted.RowsAffected()
		if err != nil {
			return err
		}
		freshEvidence = count > 0
		// Only an explicit administrator correction of this exact visual result
		// may reverse its recorded verdict. Successful execution retries retain
		// the existing conservative, one-observation-per-round behavior.
		if state.rule.UsesAutoReview() && state.evidenceSource == "administrator" {
			changed, err := tx.ExecContext(ctx, `UPDATE scheduled_test_pass_rate_evidence SET verdict=$8
 WHERE plan_id=$1 AND account_id=$2 AND test_definition_id=$3 AND model_key=$4 AND rule_version=$5 AND run_key=$6 AND result_id=$7 AND verdict<>$8`, state.planID, state.accountID, state.definitionID, result.ModelID, version, run, state.resultID, verdict)
			if err != nil {
				return err
			}
			count, err := changed.RowsAffected()
			if err != nil {
				return err
			}
			freshEvidence = freshEvidence || count > 0
		}
		if verdict == "fail" {
			changed, err := tx.ExecContext(ctx, `UPDATE scheduled_test_pass_rate_evidence SET verdict='fail'
   WHERE plan_id=$1 AND account_id=$2 AND test_definition_id=$3 AND model_key=$4 AND rule_version=$5 AND run_key=$6 AND verdict='pass'`, state.planID, state.accountID, state.definitionID, result.ModelID, version, run)
			if err != nil {
				return err
			}
			count, err := changed.RowsAffected()
			if err != nil {
				return err
			}
			freshEvidence = freshEvidence || count > 0
		}
	}
	var held bool
	err = tx.QueryRowContext(ctx, `SELECT blocked FROM scheduled_test_pass_rate_states WHERE plan_id=$1 AND account_id=$2 AND test_definition_id=$3 AND model_key=$4 FOR UPDATE`, state.planID, state.accountID, state.definitionID, result.ModelID).Scan(&held)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	now := time.Now().UTC()
	summary := service.ScheduledTestPassRateSummary{RuleVersion: version, Model: result.ModelID, Blocked: held, WindowSize: config.WindowSize, WindowSeconds: config.WindowSeconds, PauseBelow: config.PauseBelow, RecoverAtLeast: config.RecoverAtLeast, RecoverConsecutive: config.RecoverConsecutive}
	if err = refreshScheduledTestPassRateSummary(ctx, tx, state.planID, state.accountID, state.definitionID, result.ModelID, &summary, now); err != nil {
		return err
	}
	// Only a fresh valid round may change enforcement. A failed execution or a
	// repeated review cannot release a hold using old observations.
	if freshEvidence {
		var newest int64
		err = tx.QueryRowContext(ctx, `SELECT result_id FROM scheduled_test_pass_rate_evidence WHERE plan_id=$1 AND account_id=$2 AND test_definition_id=$3 AND model_key=$4 AND rule_version=$5 ORDER BY started_at DESC,id DESC LIMIT 1`, state.planID, state.accountID, state.definitionID, result.ModelID, version).Scan(&newest)
		if err != nil {
			return err
		}
		if newest == state.resultID {
			observations, err := loadScheduledTestPassRateObservations(ctx, tx, state.planID, state.accountID, state.definitionID, result.ModelID, summary, now)
			if err != nil {
				return err
			}
			summary = service.SummarizeScheduledTestPassRate(*config, observations, held, now)
			summary.RuleVersion = version
			summary.Model = result.ModelID
		}
	}
	data, err := json.Marshal(summary)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO scheduled_test_pass_rate_states(plan_id,account_id,test_definition_id,model_key,rule_version,blocked,summary)
 VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT(plan_id,account_id,test_definition_id,model_key) DO UPDATE SET rule_version=excluded.rule_version,blocked=excluded.blocked,summary=excluded.summary,updated_at=clock_timestamp()`, state.planID, state.accountID, state.definitionID, result.ModelID, version, summary.Blocked, data)
	if err != nil {
		return err
	}
	return syncActivePassRateProjection(ctx, tx, state.accountID)
}

type passRateQueryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func loadScheduledTestPassRateObservations(ctx context.Context, db passRateQueryer, planID, accountID, definitionID int64, model string, s service.ScheduledTestPassRateSummary, now time.Time) ([]service.ScheduledTestPassRateObservation, error) {
	rows, err := db.QueryContext(ctx, `SELECT verdict,started_at FROM scheduled_test_pass_rate_evidence
 WHERE plan_id=$1 AND account_id=$2 AND test_definition_id=$3 AND model_key=$4 AND rule_version=$5
 AND started_at >= $6 AND started_at <= $7 ORDER BY started_at DESC,id DESC LIMIT $8`, planID, accountID, definitionID, model, s.RuleVersion, now.Add(-time.Duration(s.WindowSeconds)*time.Second), now, s.WindowSize)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []service.ScheduledTestPassRateObservation{}
	for rows.Next() {
		var o service.ScheduledTestPassRateObservation
		if err := rows.Scan(&o.Verdict, &o.StartedAt); err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// Read paths refresh the time window but never change enforcement.
func refreshScheduledTestPassRateSummary(ctx context.Context, db passRateQueryer, planID, accountID, definitionID int64, model string, s *service.ScheduledTestPassRateSummary, now time.Time) error {
	c := service.ScheduledTestPassRateConfig{Enabled: true, WindowSize: s.WindowSize, WindowSeconds: s.WindowSeconds, PauseBelow: s.PauseBelow, RecoverAtLeast: s.RecoverAtLeast, RecoverConsecutive: s.RecoverConsecutive}
	c.Normalize()
	s.WindowSize = c.WindowSize
	s.WindowSeconds = c.WindowSeconds
	observations, err := loadScheduledTestPassRateObservations(ctx, db, planID, accountID, definitionID, model, *s, now)
	if err != nil {
		return err
	}
	refreshed := service.SummarizeScheduledTestPassRate(c, observations, s.Blocked, now)
	if !s.Blocked && refreshed.Blocked {
		refreshed.Reason = "pending_verdict"
	}
	refreshed.Blocked = s.Blocked
	refreshed.RuleVersion = s.RuleVersion
	refreshed.Model = model
	if s.Blocked && !refreshed.Stale && refreshed.Reason == "recovered" {
		refreshed.Reason = "recovery_pending"
	}
	*s = refreshed
	return nil
}

func syncActivePassRateProjection(ctx context.Context, tx *sql.Tx, accountID int64) error {
	changed, err := tx.ExecContext(ctx, `WITH holds AS (SELECT COALESCE(jsonb_object_agg(model_key,reason),'{}'::jsonb) value FROM
  (SELECT DISTINCT ON(model_key) model_key,COALESCE(summary->>'reason','recovery_pending') reason FROM scheduled_test_pass_rate_states WHERE account_id=$1 AND blocked ORDER BY model_key,updated_at DESC) h)
 UPDATE accounts a SET extra=CASE WHEN h.value='{}'::jsonb THEN COALESCE(a.extra,'{}'::jsonb)-'active_pass_rate_holds' ELSE jsonb_set(COALESCE(a.extra,'{}'::jsonb),'{active_pass_rate_holds}',h.value) END,updated_at=clock_timestamp()
 FROM holds h WHERE a.id=$1 AND COALESCE(a.extra->'active_pass_rate_holds','{}'::jsonb) IS DISTINCT FROM h.value`, accountID)
	if err != nil {
		return err
	}
	n, err := changed.RowsAffected()
	if err != nil {
		return err
	}
	if n > 0 {
		if err := enqueueSchedulerOutbox(ctx, tx, service.SchedulerOutboxEventAccountChanged, &accountID, nil, nil); err != nil {
			return err
		}
	}
	return reconcileProtectionAccount(ctx, tx, accountID)
}

func (r *scheduledTestResultRepository) hydratePassRates(ctx context.Context, results []*service.ScheduledTestResult) error {
	ids := []int64{}
	for _, v := range results {
		if v != nil {
			ids = append(ids, v.ID)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	rows, err := r.db.QueryContext(ctx, `SELECT r.id,s.plan_id,s.account_id,s.test_definition_id,s.model_key,s.summary,s.blocked FROM scheduled_test_results r JOIN scheduled_test_pass_rate_states s ON s.plan_id=r.plan_id AND s.account_id=r.account_id AND s.test_definition_id=r.test_definition_id AND s.model_key=r.model_id WHERE r.id=ANY($1)`, pq.Array(ids))
	if err != nil {
		return err
	}
	type row struct {
		id, plan, account, definition int64
		model                         string
		s                             service.ScheduledTestPassRateSummary
	}
	found := []row{}
	for rows.Next() {
		var v row
		var raw []byte
		if err = rows.Scan(&v.id, &v.plan, &v.account, &v.definition, &v.model, &raw, &v.s.Blocked); err != nil {
			_ = rows.Close()
			return err
		}
		held := v.s.Blocked
		if err = json.Unmarshal(raw, &v.s); err != nil {
			_ = rows.Close()
			return err
		}
		v.s.Blocked = held
		found = append(found, v)
	}
	err = rows.Err()
	if closeErr := rows.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	byID := map[int64]*service.ScheduledTestResult{}
	for _, v := range results {
		if v != nil {
			byID[v.ID] = v
		}
	}
	now := time.Now().UTC()
	for _, v := range found {
		if err = refreshScheduledTestPassRateSummary(ctx, r.db, v.plan, v.account, v.definition, v.model, &v.s, now); err != nil {
			return err
		}
		byID[v.id].PassRate = &v.s
	}
	return nil
}

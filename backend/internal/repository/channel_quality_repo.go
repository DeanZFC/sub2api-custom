package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"sort"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

type channelQualityRepository struct{ db *sql.DB }

func NewChannelQualityRepository(db *sql.DB) service.ChannelQualityRepository {
	return &channelQualityRepository{db: db}
}

// Page accounts before reading evidence. Independent score windows are fetched
// in one bounded batch after the page cursor has closed; no upstream is called.
func (r *channelQualityRepository) ListQualityAccounts(ctx context.Context, f service.ChannelQualityFilter) (*service.ChannelQualityPage, error) {
	limit := f.Limit
	if limit < 1 || limit > 100 {
		limit = 50
	}
	rows, err := r.db.QueryContext(ctx, `WITH targets AS (
 SELECT a.* FROM accounts a WHERE a.deleted_at IS NULL AND a.id>$1
 AND ($2=0 OR a.id=$2) AND ($3='' OR a.name ILIKE '%'||$3||'%' OR a.id::text=$3)
 AND ($4=0 OR EXISTS(SELECT 1 FROM account_groups ag WHERE ag.account_id=a.id AND ag.group_id=$4))
 AND ($5='' OR EXISTS(SELECT 1 FROM passive_quality_states s WHERE s.account_id=a.id AND (s.data->>'scope'='account' OR s.data->>'model' IN ('',$5)))
 OR EXISTS(SELECT 1 FROM scheduled_test_plans p JOIN account_groups ag ON ag.group_id=ANY(p.group_ids) WHERE ag.account_id=a.id AND p.enabled AND $5=ANY(CASE WHEN cardinality(p.model_ids)>0 THEN p.model_ids ELSE ARRAY[p.model_id] END))
 OR EXISTS(SELECT 1 FROM scheduled_test_protection_states s JOIN scheduled_test_plans p ON p.id=s.plan_id LEFT JOIN scheduled_test_definitions d ON d.id=s.test_definition_id WHERE s.account_id=a.id AND (s.blocked OR p.enabled) AND (s.model_key='' OR s.model_key=$5 OR d.output_kind='statistics'))
 OR EXISTS(SELECT 1 FROM scheduled_test_combination_states s WHERE s.account_id=a.id AND s.blocked AND (s.account_wide OR s.model_key='' OR s.model_key=$5))
 OR EXISTS(SELECT 1 FROM scheduled_test_pass_rate_states s JOIN scheduled_test_plans p ON p.id=s.plan_id WHERE s.account_id=a.id AND (s.blocked OR p.enabled) AND (s.model_key='' OR s.model_key=$5))
 OR EXISTS(SELECT 1 FROM usage_logs ul WHERE ul.account_id=a.id AND COALESCE(NULLIF(TRIM(ul.requested_model),''),ul.model)=$5 AND ul.created_at>=now()-INTERVAL '1 day' AND COALESCE(ul.request_type,0) NOT IN (4,6))
 OR EXISTS(SELECT 1 FROM passive_quality_failures f WHERE f.account_id=a.id AND f.model=$5 AND f.created_at>=now()-INTERVAL '1 day'))
 AND (NOT $6 OR a.status<>'active' OR NOT a.schedulable OR a.rate_limit_reset_at>now() OR a.temp_unschedulable_until>now() OR a.overload_until>now()
 OR EXISTS(SELECT 1 FROM passive_quality_states s WHERE s.account_id=a.id AND ($5='' OR s.data->>'scope'='account' OR s.data->>'model'=$5) AND (
 s.data->>'phase' IN ('limited','paused','probing','trial','suspect','observed')
 OR (s.data->>'phase'<>'exempt' AND (s.data->'latency'->>'phase' IN ('limited','watching') OR s.data->'speed'->>'phase' IN ('limited','watching')
 OR EXISTS(SELECT 1 FROM jsonb_each(CASE WHEN jsonb_typeof(s.data->'cache')='object' THEN s.data->'cache' ELSE '{}'::jsonb END) c
 WHERE c.value->>'phase' IN ('reduced','recovering','watching') AND ($5='' OR c.key=$5))))))
 OR EXISTS(SELECT 1 FROM scheduled_test_protection_states s JOIN scheduled_test_plans p ON p.id=s.plan_id LEFT JOIN scheduled_test_definitions d ON d.id=s.test_definition_id WHERE s.account_id=a.id AND ($5='' OR s.model_key='' OR s.model_key=$5 OR d.output_kind='statistics') AND (s.blocked OR (p.enabled AND s.completed AND s.result_started_at>=s.round_started_at AND s.verdict='fail')))
 OR EXISTS(SELECT 1 FROM scheduled_test_combination_states s WHERE s.account_id=a.id AND s.blocked AND ($5='' OR s.account_wide OR s.model_key='' OR s.model_key=$5))
 OR EXISTS(SELECT 1 FROM scheduled_test_pass_rate_states s WHERE s.account_id=a.id AND s.blocked AND ($5='' OR s.model_key='' OR s.model_key=$5)))
 ORDER BY a.id LIMIT $7
 ) SELECT a.id,a.name,a.platform,a.status,a.schedulable,a.updated_at,
 a.rate_limit_reset_at,a.temp_unschedulable_until,a.overload_until,
		 COALESCE((SELECT jsonb_agg(s.data ORDER BY s.policy_id,s.model_key)
		   FROM passive_quality_states s WHERE s.account_id=a.id),'[]'),
 COALESCE((SELECT jsonb_agg(jsonb_build_object('source','active','id',p.id,'name',p.name,'model',h.model_key,'scope',h.scope,
 'phase',h.phase,'reason',h.reason,'recovery',CASE WHEN h.phase='healthy' THEN 'scheduled_tests' ELSE 'active_tests' END,'blocking',h.blocked,'updated_at',h.updated_at,'next_at',p.next_run_at,'result_id',h.result_id))
 FROM (SELECT s.plan_id,s.reason,s.updated_at,s.blocked,s.result_id,s.model_key,
 CASE WHEN d.output_kind='statistics' OR s.model_key='' THEN 'account' ELSE 'model' END AS scope,
 CASE WHEN s.blocked THEN 'paused' WHEN NOT s.completed OR s.result_started_at<s.round_started_at THEN 'insufficient' WHEN s.verdict='pass' THEN 'healthy' WHEN s.verdict='fail' THEN 'observed' ELSE 'insufficient' END AS phase
 FROM scheduled_test_protection_states s JOIN scheduled_test_plans plan ON plan.id=s.plan_id LEFT JOIN scheduled_test_definitions d ON d.id=s.test_definition_id
 WHERE s.account_id=a.id AND (s.blocked OR plan.enabled) AND ($5='' OR s.model_key='' OR s.model_key=$5 OR d.output_kind='statistics')
 UNION ALL SELECT plan_id,reason,updated_at,blocked,NULL::bigint,model_key,CASE WHEN account_wide OR model_key='' THEN 'account' ELSE 'model' END,'paused' FROM scheduled_test_combination_states WHERE account_id=a.id AND blocked AND ($5='' OR account_wide OR model_key='' OR model_key=$5)) h
 JOIN scheduled_test_plans p ON p.id=h.plan_id),'[]'),
 COALESCE((SELECT jsonb_agg(jsonb_build_object('source','active','metric','pass_rate','id',p.id,'name',p.name,'model',s.model_key,'scope','model',
 'phase',CASE WHEN s.blocked THEN 'paused' WHEN s.summary->>'ready'='true' THEN 'healthy' ELSE 'insufficient' END,
 'reason',s.summary->>'reason','recovery',CASE WHEN s.blocked THEN 'active_tests' ELSE 'scheduled_tests' END,'blocking',s.blocked,
 'updated_at',s.updated_at,'next_at',p.next_run_at,'test_definition_id',s.test_definition_id,
 'pass_rate',s.summary||jsonb_build_object('blocked',s.blocked,'model',s.model_key)) ORDER BY s.plan_id,s.test_definition_id,s.model_key)
 FROM scheduled_test_pass_rate_states s JOIN scheduled_test_plans p ON p.id=s.plan_id
 WHERE s.account_id=a.id AND (s.blocked OR p.enabled) AND ($5='' OR s.model_key='' OR s.model_key=$5)),'[]'),
 (SELECT count(*) FROM scheduled_test_plans p WHERE p.enabled AND NOT (a.id=ANY(p.excluded_account_ids)) AND EXISTS(SELECT 1 FROM account_groups ag WHERE ag.account_id=a.id AND ag.group_id=ANY(p.group_ids))),
 (SELECT count(*) FROM passive_quality_policies p WHERE p.config->>'enabled'='true'
 AND NOT EXISTS(SELECT 1 FROM jsonb_array_elements_text(COALESCE(NULLIF(p.config->'excluded_account_ids','null'::jsonb),'[]')) x WHERE x::bigint=a.id)
 AND EXISTS(SELECT 1 FROM account_groups ag WHERE ag.account_id=a.id AND ag.group_id IN(SELECT x::bigint FROM jsonb_array_elements_text(p.config->'group_ids') x)))
 FROM targets a ORDER BY a.id`, f.After, f.AccountID, f.Search, f.GroupID, f.Model, f.ProblemsOnly, limit+1)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	page := &service.ChannelQualityPage{Items: []service.ChannelQualityAccount{}, GeneratedAt: time.Now()}
	for rows.Next() {
		var a service.ChannelQualityAccount
		var updated time.Time
		var rate, temporary, overload *time.Time
		var passiveRaw, activeRaw, passRateRaw []byte
		if err = rows.Scan(&a.ID, &a.Name, &a.Platform, &a.Status, &a.Schedulable, &updated, &rate, &temporary, &overload, &passiveRaw, &activeRaw, &passRateRaw, &a.ActivePlans, &a.PassivePolicies); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(activeRaw, &a.Controls); err != nil {
			return nil, err
		}
		if a.Controls == nil {
			a.Controls = []service.ChannelQualityControl{}
		}
		var passRateControls []service.ChannelQualityControl
		if err = json.Unmarshal(passRateRaw, &passRateControls); err != nil {
			return nil, err
		}
		a.Controls = append(a.Controls, passRateControls...)
		add := func(phase, reason, recovery string, next *time.Time) {
			a.Controls = append(a.Controls, service.ChannelQualityControl{Source: "account", Name: reason, Phase: phase, Reason: reason, Recovery: recovery, Scope: "account", Blocking: true, NextAt: next, UpdatedAt: updated})
		}
		hasActiveHold := false
		for _, c := range a.Controls {
			hasActiveHold = hasActiveHold || c.Blocking
		}
		if !a.Schedulable {
			add("paused", "manual_stop", "manual", nil)
		}
		if a.Status != "active" && (a.Status != "quality_paused" || !hasActiveHold) {
			add("paused", a.Status, "account_settings", nil)
		}
		for _, hold := range []struct {
			at     *time.Time
			reason string
		}{{rate, "rate_limit"}, {temporary, "temporary_hold"}, {overload, "overload"}} {
			if hold.at != nil && hold.at.After(page.GeneratedAt) {
				add("paused", hold.reason, "cooldown", hold.at)
			}
		}
		var states []service.PassiveQualityState
		if err = json.Unmarshal(passiveRaw, &states); err != nil {
			return nil, err
		}
		for _, state := range states {
			a.Controls = append(a.Controls, passiveQualityOverviewControls(state, f.Model)...)
		}
		page.Items = append(page.Items, a)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	if err = rows.Close(); err != nil {
		return nil, err
	}
	if len(page.Items) > limit {
		page.Items = page.Items[:limit]
		page.NextAfter = page.Items[limit-1].ID
	}
	accountIDs := make([]int64, 0, len(page.Items))
	for _, account := range page.Items {
		accountIDs = append(accountIDs, account.ID)
	}
	scores, err := r.ChannelQualityScores(ctx, accountIDs, f.Model)
	if err != nil {
		return nil, err
	}
	for i := range page.Items {
		page.Items[i].QualityScore = scores[page.Items[i].ID]
	}
	// Expired observations change the displayed window, never the live hold.
	// Close the page cursor first so this also works with one DB connection.
	for ai := range page.Items {
		account := &page.Items[ai]
		for ci := range account.Controls {
			control := &account.Controls[ci]
			if control.PassRate == nil {
				continue
			}
			if err = refreshScheduledTestPassRateSummary(ctx, r.db, control.ID, account.ID, control.TestDefinitionID, control.Model, control.PassRate, page.GeneratedAt); err != nil {
				return nil, err
			}
			control.Reason = control.PassRate.Reason
			if control.Blocking {
				control.Phase = "paused"
			} else if !control.PassRate.Ready {
				control.Phase = "insufficient"
			} else {
				control.Phase = "healthy"
			}
		}
	}
	return page, nil
}

// Each metric owns a separate control. An availability recovery must not make
// a continuing latency/cache restriction disappear from the read-only overview.
func passiveQualityOverviewControls(s service.PassiveQualityState, modelFilter string) []service.ChannelQualityControl {
	if modelFilter != "" && s.Scope == "model" && s.Model != modelFilter {
		return nil
	}
	c := service.ChannelQualityControl{Source: "passive", ID: s.PolicyID, Name: s.PolicyName, Model: s.Model, Scope: s.Scope, Phase: s.Phase, Reason: s.Reason, UpdatedAt: s.UpdatedAt, Evidence: &s.Evidence, Recovery: "production_samples"}
	if s.Latency != nil || s.Speed != nil || len(s.Cache) > 0 {
		c.Metric = "availability"
	}
	groupTarget := s.Purpose == "group_quality_target" || s.Purpose == "cache_target"
	if groupTarget {
		c.Metric = "group_quality_target"
	}
	if s.Phase == "healthy" || s.Phase == "insufficient" || s.Phase == "suspect" || s.Phase == "observed" {
		c.Recovery = "monitoring"
	}
	c.Blocking = s.Mode == "enforce" && (s.Phase == "paused" || s.Phase == "probing")
	if s.Phase == "paused" || s.Phase == "probing" {
		c.Recovery, c.NextAt = "probe_then_trial", &s.NextAt
		if groupTarget {
			c.Recovery = "real_traffic_trial"
		}
	}
	if s.Phase == "trial" {
		c.NextAt = &s.TrialEndsAt
		if groupTarget {
			c.Recovery = "real_traffic_trial"
		}
	}
	if s.Phase == "exempt" {
		c.NextAt, c.Recovery = &s.NextAt, "exemption_expiry"
		return []service.ChannelQualityControl{c}
	}
	controls := []service.ChannelQualityControl{c}
	if s.Latency != nil {
		l := s.Latency
		phase, recovery := l.Phase, "monitoring"
		if phase == "watching" {
			phase = "observed"
		}
		if phase == "limited" {
			recovery = "production_samples"
		}
		control := service.ChannelQualityControl{Source: "passive", ID: s.PolicyID, Name: s.PolicyName, Metric: "latency", Model: s.Model, Scope: s.Scope, Phase: phase, Reason: l.Reason, UpdatedAt: l.UpdatedAt, Evidence: &l.Evidence, Recovery: recovery}
		if !l.HoldUntil.IsZero() {
			control.NextAt = &l.HoldUntil
		}
		controls = append(controls, control)
	}
	if s.Speed != nil {
		l := s.Speed
		phase, recovery := l.Phase, "monitoring"
		if phase == "watching" {
			phase = "observed"
		}
		if phase == "limited" {
			recovery = "production_samples"
		}
		control := service.ChannelQualityControl{Source: "passive", ID: s.PolicyID, Name: s.PolicyName, Metric: "tps", Model: s.Model, Scope: s.Scope, Phase: phase, Reason: l.Reason, UpdatedAt: l.UpdatedAt, Evidence: &l.Evidence, Recovery: recovery}
		if !l.HoldUntil.IsZero() {
			control.NextAt = &l.HoldUntil
		}
		controls = append(controls, control)
	}
	models := make([]string, 0, len(s.Cache))
	for model := range s.Cache {
		models = append(models, model)
	}
	sort.Strings(models)
	for _, model := range models {
		if modelFilter != "" && model != modelFilter {
			continue
		}
		cache := s.Cache[model]
		phase, recovery := cache.Phase, "monitoring"
		switch phase {
		case "reduced", "recovering":
			phase, recovery = "cache_limited", "cache_reuse_samples"
		case "watching":
			phase = "observed"
		}
		// Cache allocation is always per model even when availability uses account scope.
		control := service.ChannelQualityControl{Source: "passive", ID: s.PolicyID, Name: s.PolicyName, Metric: "cache", Model: model, Scope: "model", Phase: phase, Reason: cache.Reason, UpdatedAt: cache.UpdatedAt, Recovery: recovery, AllocationPercent: cache.AllocationPercent, Cache: &cache.Evidence}
		if !cache.HoldUntil.IsZero() {
			control.NextAt = &cache.HoldUntil
		}
		controls = append(controls, control)
	}
	return controls
}

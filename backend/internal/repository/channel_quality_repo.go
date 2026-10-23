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

// Page accounts before reading their evidence; each lateral lookup is bounded
// by account/policy indexes. No usage-log scan and no upstream call is needed.
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
 OR EXISTS(SELECT 1 FROM scheduled_test_plans p JOIN account_groups ag ON ag.group_id=ANY(p.group_ids) WHERE ag.account_id=a.id AND p.enabled AND p.model_id=$5))
 AND (NOT $6 OR a.status<>'active' OR NOT a.schedulable OR a.rate_limit_reset_at>now() OR a.temp_unschedulable_until>now() OR a.overload_until>now()
 OR EXISTS(SELECT 1 FROM passive_quality_states s WHERE s.account_id=a.id AND ($5='' OR s.data->>'scope'='account' OR s.data->>'model'=$5) AND (
 s.data->>'phase' IN ('limited','paused','probing','trial','suspect','observed')
 OR (s.data->>'phase'<>'exempt' AND (s.data->'latency'->>'phase' IN ('limited','watching')
 OR EXISTS(SELECT 1 FROM jsonb_each(CASE WHEN jsonb_typeof(s.data->'cache')='object' THEN s.data->'cache' ELSE '{}'::jsonb END) c
 WHERE c.value->>'phase' IN ('reduced','recovering','watching') AND ($5='' OR c.key=$5))))))
 OR EXISTS(SELECT 1 FROM scheduled_test_protection_states s JOIN scheduled_test_plans p ON p.id=s.plan_id WHERE s.account_id=a.id AND (s.blocked OR (p.enabled AND s.completed AND s.result_started_at>=s.round_started_at AND s.verdict='fail'))))
 ORDER BY a.id LIMIT $7
 ) SELECT a.id,a.name,a.platform,a.status,a.schedulable,a.updated_at,
 a.rate_limit_reset_at,a.temp_unschedulable_until,a.overload_until,
 COALESCE((SELECT jsonb_agg(s.data ORDER BY s.policy_id,s.model_key) FROM passive_quality_states s WHERE s.account_id=a.id),'[]'),
 COALESCE((SELECT jsonb_agg(jsonb_build_object('source','active','id',p.id,'name',p.name,'model',p.model_id,'scope','account',
 'phase',h.phase,'reason',h.reason,'recovery',CASE WHEN h.phase='healthy' THEN 'scheduled_tests' ELSE 'active_tests' END,'blocking',h.blocked,'updated_at',h.updated_at,'next_at',p.next_run_at,'result_id',h.result_id))
 FROM (SELECT s.plan_id,s.reason,s.updated_at,s.blocked,s.result_id,
 CASE WHEN s.blocked THEN 'paused' WHEN NOT s.completed OR s.result_started_at<s.round_started_at THEN 'insufficient' WHEN s.verdict='pass' THEN 'healthy' WHEN s.verdict='fail' THEN 'observed' ELSE 'insufficient' END AS phase
 FROM scheduled_test_protection_states s JOIN scheduled_test_plans plan ON plan.id=s.plan_id
 WHERE s.account_id=a.id AND (s.blocked OR plan.enabled)
 UNION ALL SELECT plan_id,reason,updated_at,blocked,NULL::bigint,'paused' FROM scheduled_test_combination_states WHERE account_id=a.id AND blocked) h
 JOIN scheduled_test_plans p ON p.id=h.plan_id),'[]'),
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
		var passiveRaw, activeRaw []byte
		if err = rows.Scan(&a.ID, &a.Name, &a.Platform, &a.Status, &a.Schedulable, &updated, &rate, &temporary, &overload, &passiveRaw, &activeRaw, &a.ActivePlans, &a.PassivePolicies); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(activeRaw, &a.Controls); err != nil {
			return nil, err
		}
		if a.Controls == nil {
			a.Controls = []service.ChannelQualityControl{}
		}
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
		for _, s := range states {
			a.Controls = append(a.Controls, passiveQualityOverviewControls(s, f.Model)...)
		}
		page.Items = append(page.Items, a)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	if len(page.Items) > limit {
		page.Items = page.Items[:limit]
		page.NextAfter = page.Items[limit-1].ID
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
	if s.Latency != nil || len(s.Cache) > 0 {
		c.Metric = "availability"
	}
	if s.Phase == "healthy" || s.Phase == "insufficient" || s.Phase == "suspect" || s.Phase == "observed" {
		c.Recovery = "monitoring"
	}
	c.Blocking = s.Mode == "enforce" && (s.Phase == "paused" || s.Phase == "probing")
	if s.Phase == "paused" || s.Phase == "probing" {
		c.Recovery, c.NextAt = "probe_then_trial", &s.NextAt
	}
	if s.Phase == "trial" {
		c.NextAt = &s.TrialEndsAt
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

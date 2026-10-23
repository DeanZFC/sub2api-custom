package repository

import (
	"context"
	"database/sql"
	"fmt"
	"slices"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
)

func (r *qualityOperationsRepository) ReplayQualityPolicy(ctx context.Context, req service.QualityReplayRequest) (*service.QualityReplayResult, error) {
	now := time.Now().UTC()
	if err := req.Validate(now); err != nil {
		return nil, err
	}
	// Normalize legacy omitted action fields just as policy saves do.
	if err := req.Config.Validate(); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.ExecContext(ctx, `SET LOCAL statement_timeout='5s'`); err != nil {
		return nil, err
	}
	out := &service.QualityReplayResult{Points: []service.QualityReplayPoint{}, Warnings: []string{"recorded_traffic_only", "current_membership", "no_probe_or_routing_simulation", "capture_coverage_not_guaranteed"}}
	if !req.Config.Enabled {
		out.Warnings = append(out.Warnings, "policy_disabled")
	}
	if req.Config.IsolatedMetrics() && !req.Config.IsGroupQualityTarget() {
		out.Warnings = append(out.Warnings, "cache_pool_guard_not_simulated")
	}
	for _, id := range req.AccountIDs {
		if slices.Contains(req.Config.ExcludedAccountIDs, id) {
			return nil, fmt.Errorf("account %d is excluded by the draft policy", id)
		}
		var eligible bool
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM accounts a WHERE a.id=$1 AND a.deleted_at IS NULL AND EXISTS(SELECT 1 FROM account_groups ag WHERE ag.account_id=a.id AND ag.group_id=ANY($2)))`, id, pq.Array(req.Config.GroupIDs)).Scan(&eligible); err != nil {
			return nil, err
		}
		if !eligible {
			return nil, fmt.Errorf("account %d does not belong to this draft policy", id)
		}
		policy := service.PassiveQualityPolicy{PassiveQualityConfig: req.Config}
		window := req.Config.WindowSeconds
		if req.Config.CacheProtection != nil {
			window = max(window, req.Config.CacheProtection.Quick.WindowSeconds, req.Config.CacheProtection.ActionWindow.WindowSeconds)
		}
		models, e := passiveReplayModels(ctx, tx, policy, id, req.From.Add(-time.Duration(window)*time.Second), req.To)
		if e != nil {
			return nil, e
		}
		steps := int(req.To.Sub(req.From).Seconds())/req.StepSeconds + 1
		if len(models) > 128 || out.Evaluations+len(models)*steps > 2000 {
			return nil, fmt.Errorf("replay exceeds 2000 model evaluations; select a model or shorten the time range")
		}
		if len(models) == 0 && !slices.Contains(out.Warnings, "no_recorded_request_models") {
			out.Warnings = append(out.Warnings, "no_recorded_request_models")
		}
		for _, model := range models {
			policy.Model = model
			state := service.PassiveQualityState{AccountID: id, Model: model, Scope: policy.Scope, Phase: "healthy"}
			triggered := false
			for at := req.From; !at.After(req.To); at = at.Add(time.Duration(req.StepSeconds) * time.Second) {
				if req.Config.IsolatedMetrics() {
					point, events, missing, e := replayPassiveMetrics(ctx, tx, policy, &state, at, now)
					if e != nil {
						return nil, e
					}
					if missing && !slices.Contains(out.Warnings, "cache_reuse_history_unavailable") {
						out.Warnings = append(out.Warnings, "cache_reuse_history_unavailable")
					}
					out.Triggers += events
					out.Evaluations++
					if point.Verdict == "pending" {
						out.Pending++
					}
					out.Points = append(out.Points, point)
					continue
				}
				evidence, e := passiveSamples(ctx, tx, policy, state, at)
				if e != nil {
					return nil, e
				}
				// The failure/cache-presence collector retains 48 hours. An older leading
				// window cannot be treated as complete even if usage logs survive longer.
				if at.Add(-time.Duration(req.Config.WindowSeconds) * time.Second).Before(now.Add(-48 * time.Hour)) {
					evidence.Complete = false
				}
				verdict, reason := service.EvaluatePassiveQuality(req.Config, evidence, state.Phase == "limited")
				point := service.QualityReplayPoint{At: at, AccountID: id, Model: model, Verdict: verdict, Reason: reason, Evidence: evidence}
				if req.Config.Enabled && evidence.Fresh >= req.Config.NewSamples {
					state.LastEvidenceAt = evidence.Latest
					if state.Phase == "limited" {
						if verdict == "pass" {
							state.Hits++
						} else {
							state.Hits = 0
						}
						if state.Hits >= req.Config.Confirmations {
							point.Action = "would_resume"
							state.Phase = "healthy"
							state.Hits = 0
							triggered = false
						}
					} else if !triggered || req.Config.Mode == "observe" {
						if verdict == "fail" {
							state.Hits++
						} else {
							state.Hits = 0
							triggered = false
						}
						if state.Hits >= req.Config.Confirmations && !triggered {
							triggered = true
							out.Triggers++
							if req.Config.Mode == "observe" {
								point.Action = "would_observe"
							} else if req.Config.Action == "limit" {
								point.Action = "would_limit"
								state.Phase = "limited"
								state.Hits = 0
							} else {
								point.Action = "would_pause"
								state.Phase = "paused"
							}
						}
					}
				} else if state.Phase != "limited" && state.Phase != "paused" && evidence.Samples < req.Config.MinSamples {
					state.Hits = 0
					triggered = false
				}
				if state.Phase == "paused" && point.Action == "" {
					point.Action = "recovery_requires_probe"
				}
				point.Hits = state.Hits
				out.Evaluations++
				if verdict == "pending" {
					out.Pending++
				}
				out.Points = append(out.Points, point)
			}
		}
	}
	return out, tx.Commit()
}

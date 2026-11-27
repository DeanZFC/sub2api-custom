package service

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"time"
	"unicode"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const GroupProbeTimeout = 75 * time.Second
const GroupProbePollInterval = 15 * time.Second

var ErrGroupProbeBusy = infraerrors.Conflict("GROUP_PROBE_BUSY", "probe is already pending or running")
var ErrGroupProbeDisabled = infraerrors.BadRequest("GROUP_PROBE_DISABLED", "enable this group probe first")

type GroupProbeResult struct {
	Success       bool                  `json:"success"`
	Detail        string                `json:"detail,omitempty"`
	FirstTokenMS  *int                  `json:"first_token_ms"`
	AccountID     int64                 `json:"account_id,omitempty"`
	Switched      bool                  `json:"switched"`
	Tokens        *int64                `json:"tokens"`
	EstimatedCost *float64              `json:"estimated_cost_usd"`
	PartialUsage  bool                  `json:"partial_usage"`
	Deferred      *QualityDeferredError `json:"-"`
}

// An omitted setting retains its saved value on update and uses the database
// default on creation, including requests from clients predating traffic rules.
type GroupProbeRuleUpdate struct {
	IdleWindowMinutes *int32 `json:"idle_window_minutes"`
	MinRequests       *int32 `json:"min_requests"`
	DurationMinutes   *int32 `json:"duration_minutes"`
	IntervalSeconds   *int32 `json:"interval_seconds"`
}

func (u GroupProbeRuleUpdate) validate() error {
	if u.IdleWindowMinutes != nil && (*u.IdleWindowMinutes < 1 || *u.IdleWindowMinutes > 1440) {
		return infraerrors.BadRequest("GROUP_PROBE_WINDOW", "idle_window_minutes must be between 1 and 1440")
	}
	if u.MinRequests != nil && *u.MinRequests < 1 {
		return infraerrors.BadRequest("GROUP_PROBE_MIN_REQUESTS", "min_requests must be positive")
	}
	if u.DurationMinutes != nil && (*u.DurationMinutes < 1 || *u.DurationMinutes > 1440) {
		return infraerrors.BadRequest("GROUP_PROBE_DURATION", "duration_minutes must be between 1 and 1440")
	}
	if u.IntervalSeconds != nil && (*u.IntervalSeconds < 1 || *u.IntervalSeconds > 86400) {
		return infraerrors.BadRequest("GROUP_PROBE_INTERVAL", "interval_seconds must be between 1 and 86400")
	}
	return nil
}

type GroupProbe struct {
	IdleWindowMinutes int32             `json:"idle_window_minutes"`
	MinRequests       int32             `json:"min_requests"`
	DurationMinutes   int32             `json:"duration_minutes"`
	IntervalSeconds   int32             `json:"interval_seconds"`
	RoundUntil        *time.Time        `json:"round_until"`
	GroupID           int64             `json:"group_id"`
	GroupName         string            `json:"group_name"`
	Model             string            `json:"model"`
	Enabled           bool              `json:"enabled"`
	Pending           bool              `json:"pending"`
	LastUserAt        *time.Time        `json:"last_user_at"`
	LastProbeAt       *time.Time        `json:"last_probe_at"`
	Result            *GroupProbeResult `json:"result"`
	Status            string            `json:"status"`
	DeferredReason    string            `json:"deferred_reason,omitempty"`
}

type GroupProbeJob struct {
	GroupID      int64
	Model, Owner string
}

type GroupProbeRepository interface {
	List(context.Context) ([]GroupProbe, error)
	Configure(ctx context.Context, group int64, model string, enabled bool, rule GroupProbeRuleUpdate) error
	Request(ctx context.Context, group int64) error
	// A zero wait means another group may already be due.
	Claim(context.Context) (job *GroupProbeJob, wait time.Duration, err error)
	Finish(context.Context, *GroupProbeJob, GroupProbeResult) error
}

type GroupProbeExecutor func(context.Context, *Group, string) GroupProbeResult

type GroupProbeService struct {
	repo     GroupProbeRepository
	groups   GroupRepository
	settings *SettingService
	execute  GroupProbeExecutor
	cancel   context.CancelFunc
	wg       sync.WaitGroup
	wake     chan struct{}
}

func NewGroupProbeService(repo GroupProbeRepository, groups GroupRepository, settings *SettingService, execute GroupProbeExecutor) *GroupProbeService {
	ctx, cancel := context.WithCancel(context.Background())
	s := &GroupProbeService{repo: repo, groups: groups, settings: settings, execute: execute, cancel: cancel, wake: make(chan struct{}, 1)}
	// Each worker leases only the job it is about to execute.
	for range 2 {
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			s.loop(ctx)
		}()
	}
	return s
}

func (s *GroupProbeService) Stop() {
	s.cancel()
	s.wg.Wait()
}

func (s *GroupProbeService) List(ctx context.Context) ([]GroupProbe, error) { return s.repo.List(ctx) }
func (s *GroupProbeService) Configure(ctx context.Context, id int64, model string, enabled bool, rule GroupProbeRuleUpdate) error {
	if err := rule.validate(); err != nil {
		return err
	}
	model = strings.TrimSpace(model)
	if model == "" || len(model) > 200 || strings.IndexFunc(model, unicode.IsControl) >= 0 {
		return infraerrors.BadRequest("GROUP_PROBE_MODEL", "invalid probe model")
	}
	g, err := s.groups.GetByIDLite(ctx, id)
	if err != nil {
		return err
	}
	if enabled && (!g.IsActive() || !g.ModelAllowlist.Allows(model) || g.Platform == PlatformTypeSafe || (g.ClaudeCodeOnly && g.FallbackGroupID == nil) || IsGPTImageGenerationModel(model)) {
		return infraerrors.BadRequest("GROUP_PROBE_MODEL", "choose an active group and a text model that accepts Chat Completions")
	}
	if err = s.repo.Configure(ctx, id, model, enabled, rule); err == nil {
		s.notify()
	}
	return err
}

func (s *GroupProbeService) Request(ctx context.Context, id int64) error {
	if err := s.repo.Request(ctx, id); err != nil {
		return err
	}
	s.notify()
	return nil
}

func (s *GroupProbeService) notify() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func (s *GroupProbeService) loop(ctx context.Context) {
	for ctx.Err() == nil {
		wait := GroupProbePollInterval
		if s.settings.GetChannelMonitorRuntime(ctx).Enabled {
			next, err := s.step(ctx)
			if err != nil && ctx.Err() == nil {
				slog.Warn("group probe worker", "error", err)
			}
			if err == nil {
				if next <= 0 {
					continue
				}
				wait = min(wait, next)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		case <-s.wake:
		}
	}
}

func (s *GroupProbeService) step(ctx context.Context) (time.Duration, error) {
	query, cancel := context.WithTimeout(ctx, 8*time.Second)
	job, wait, err := s.repo.Claim(query)
	cancel()
	if err != nil || job == nil {
		return wait, err
	}
	run, stop := context.WithTimeout(ctx, GroupProbeTimeout)
	defer stop()
	g, err := s.groups.GetByIDLite(run, job.GroupID)
	var result GroupProbeResult
	switch {
	case err != nil || g == nil || !g.IsActive():
		result.Deferred = &QualityDeferredError{Reason: "group_unavailable", Until: time.Now().Add(time.Minute)}
	default:
		result = s.execute(run, g, job.Model)
	}
	if errors.Is(ctx.Err(), context.Canceled) {
		return 0, ctx.Err()
	}
	save, done := context.WithTimeout(ctx, 8*time.Second)
	defer done()
	return 0, s.repo.Finish(save, job, result)
}

package service

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
)

const ScheduledTestLeaseTTL = 2 * time.Minute
const scheduledTestLeaseHeartbeat = 20 * time.Second

type ScheduledTestLeaseRepository interface {
	ClaimExecution(context.Context, *ScheduledTestPlan, string, string, bool) (bool, error)
	RenewExecution(context.Context, int64, string, string) (bool, error)
	ReleaseExecution(context.Context, int64, string, string) error
}

// Full rounds exclude retries and other rounds. Retry scopes exclude only
// the same account/type/model, so independent failed results may run together.
func (s *ScheduledTestRunnerService) claimDistributedExecution(ctx context.Context, plan *ScheduledTestPlan, scope string, automatic bool) (context.Context, func(), error) {
	repo, ok := s.planRepo.(ScheduledTestLeaseRepository)
	if !ok {
		return ctx, func() {}, nil
	} // non-database adapters
	owner := uuid.NewString()
	claimCtx, cancel := context.WithTimeout(ctx, scheduledTestPersistenceTimeout)
	claimed, err := repo.ClaimExecution(claimCtx, plan, scope, owner, automatic)
	cancel()
	if err != nil {
		return nil, nil, err
	}
	if !claimed {
		return nil, nil, ErrScheduledTestAccountRunning
	}
	plan.ExecutionLeaseOwner, plan.ExecutionLeaseScope = owner, scope
	return keepScheduledTestLease(ctx, repo, plan.ID, scope, owner, scheduledTestLeaseHeartbeat)
}

func keepScheduledTestLease(ctx context.Context, repo ScheduledTestLeaseRepository, planID int64, scope, owner string, interval time.Duration) (context.Context, func(), error) {
	runCtx, stop := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-runCtx.Done():
				return
			case <-ticker.C:
				renewCtx, cancel := context.WithTimeout(runCtx, scheduledTestPersistenceTimeout)
				alive, err := repo.RenewExecution(renewCtx, planID, scope, owner)
				cancel()
				if err != nil || !alive {
					stop()
					return
				}
			}
		}
	}()
	var once sync.Once
	return runCtx, func() {
		once.Do(func() {
			stop()
			<-done
			cleanup, cancel := scheduledTestPersistenceContext()
			defer cancel()
			_ = repo.ReleaseExecution(cleanup, planID, scope, owner)
		})
	}, nil
}

func (s *ScheduledTestRunnerService) executionLeaseCurrent(ctx context.Context, plan *ScheduledTestPlan) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if plan.ExecutionLeaseOwner == "" {
		return true, nil
	}
	repo, ok := s.planRepo.(ScheduledTestLeaseRepository)
	if !ok {
		return false, fmt.Errorf("execution lease repository unavailable")
	}
	return repo.RenewExecution(ctx, plan.ID, plan.ExecutionLeaseScope, plan.ExecutionLeaseOwner)
}

func scheduledTestRetryScope(result *ScheduledTestResult) string {
	var definition int64
	if result.TestDefinitionID != nil {
		definition = *result.TestDefinitionID
	}
	return fmt.Sprintf("retry:%d:%d:%s", *result.AccountID, definition, result.ModelID)
}

package sourceupdater

import (
	"context"
	"errors"
	"os"
	"time"
)

type statePersistenceError struct{ cause error }

func (e *statePersistenceError) Error() string { return "persist updater state: " + e.cause.Error() }
func (e *statePersistenceError) Unwrap() error { return e.cause }

func (u *Updater) persistenceFailureLocked(jobID string, err error) error {
	failure := &statePersistenceError{cause: err}
	job := u.jobs[jobID]
	job.Message = "state persistence failed; update remains locked pending recovery"
	job.Error = failure.Error()
	u.jobs[jobID] = job
	return failure
}

// Retry only the state write, never the deployment. Until it is durable, keep
// the singleton slot and do not run the process restart hook.
func (u *Updater) finishDurably(ctx context.Context, jobID, status, stage, message, detail string) error {
	for {
		err := u.finish(jobID, status, stage, message, detail)
		if err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return err
		case <-time.After(time.Second):
		}
	}
}

func (u *Updater) recoverInterrupted(job Job) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	// The verified immutable image ID is persisted before deployment. A healthy
	// target may have completed just before the final state write failed.
	if job.TargetImageID != "" && job.Stage != "rolling_back" {
		if err := u.waitHealthy(ctx, job.TargetImageID); err == nil {
			staged, err := u.stageUpdater(ctx, job.JobID, job.TargetImage)
			if err == nil {
				defer func() { _ = os.Remove(staged) }()
				err = u.installUpdater(staged)
			}
			if err == nil {
				_ = u.finishDurably(ctx, job.JobID, "succeeded", "completed", "update completion recovered", "")
				return
			}
		}
	}
	err := u.rollbackImage(job.JobID, job.PreviousImage, errors.New("updater restarted during deployment"))
	var persistenceErr *statePersistenceError
	if !errors.As(err, &persistenceErr) {
		_ = u.finishDurably(ctx, job.JobID, "failed", "failed", "update interrupted", sanitizeError(err))
	}
}

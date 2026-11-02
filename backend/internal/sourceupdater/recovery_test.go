package sourceupdater

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestCompletionMustPersistBeforeReleasingUpdate(t *testing.T) {
	u := newTestUpdater(t, &fakeRunner{})
	u.jobs["test-job"] = Job{JobID: "test-job", Status: "running", Stage: "health_check", PreviousImage: oldID, TargetImage: pinnedImage, TargetImageID: newID}
	u.activeID = "test-job"
	require.NoError(t, u.persistCurrentJob("test-job"))
	dir := u.cfg.StateDir
	require.NoError(t, os.Rename(dir, dir+"-saved"))
	require.NoError(t, os.WriteFile(dir, []byte("unavailable"), 0600))
	require.Error(t, u.finish("test-job", "succeeded", "completed", "done", ""))
	require.Equal(t, "test-job", u.activeID)
	require.Equal(t, "running", u.jobs["test-job"].Status)
	require.Empty(t, u.jobs["test-job"].FinishedAt)
	require.Contains(t, u.jobs["test-job"].Error, "persist updater state")
	require.NoError(t, os.Remove(dir))
	require.NoError(t, os.Rename(dir+"-saved", dir))
	require.NoError(t, u.finishDurably(context.Background(), "test-job", "succeeded", "completed", "done", ""))
	require.Empty(t, u.activeID)
	data, err := os.ReadFile(filepath.Join(dir, "test-job.json"))
	require.NoError(t, err)
	var saved Job
	require.NoError(t, json.Unmarshal(data, &saved))
	require.Equal(t, "succeeded", saved.Status)
	require.NotEmpty(t, saved.FinishedAt)
}

func TestRestartKeepsHealthyTargetAfterLostCompletion(t *testing.T) {
	r := &fakeRunner{}
	u := newTestUpdater(t, r)
	u.jobs["test-job"] = Job{JobID: "test-job", Status: "running", Stage: "health_check", PreviousImage: oldID, TargetImage: pinnedImage, TargetImageID: newID}
	require.NoError(t, u.persistCurrentJob("test-job"))
	require.NoError(t, u.writeOverlay(pinnedImage))
	r.image, r.recreateCount = newID, 1
	recovered, err := NewWithRunner(u.cfg, r)
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		recovered.mu.RLock()
		defer recovered.mu.RUnlock()
		return recovered.jobs["test-job"].Status == "succeeded" && recovered.activeID == ""
	}, time.Second, time.Millisecond)
	r.mu.Lock()
	image, count := r.image, r.recreateCount
	r.mu.Unlock()
	require.Equal(t, newID, image)
	require.Equal(t, 1, count, "healthy deployment must not be recreated or rolled back")
	data, err := os.ReadFile(filepath.Join(u.cfg.StateDir, "test-job.json"))
	require.NoError(t, err)
	var saved Job
	require.NoError(t, json.Unmarshal(data, &saved))
	require.Equal(t, "succeeded", saved.Status)
}

func TestCheckpointFailureStopsBeforeDockerMutation(t *testing.T) {
	r := &fakeRunner{}
	u := newTestUpdater(t, r)
	queue(u)
	u.activeID = "test-job"
	require.NoError(t, os.RemoveAll(u.cfg.StateDir))
	require.Error(t, u.workflow(context.Background(), "test-job"))
	require.Empty(t, r.callList())
	require.Equal(t, "test-job", u.activeID)
}

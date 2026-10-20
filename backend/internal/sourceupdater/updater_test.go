package sourceupdater

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/updatepolicy"
	"github.com/stretchr/testify/require"
)

var oldID = "sha256:" + strings.Repeat("1", 64)
var newID = "sha256:" + strings.Repeat("2", 64)
var pinnedImage = updatepolicy.ImageRepository + "@sha256:" + strings.Repeat("3", 64)

const targetVersion = "0.2.17-custom.13"

var revision = strings.Repeat("a", 40)

type fakeRunner struct {
	mu             sync.Mutex
	calls          []string
	outputs        map[string]string
	errors         map[string]error
	image          string
	newHealth      string
	rollbackHealth string
	recreateCount  int
	wrongImage     bool
}

func (r *fakeRunner) Run(_ context.Context, _ string, stdout, _ io.Writer, name string, args ...string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := commandKey(name, args...)
	r.calls = append(r.calls, key)
	if err := r.errors[key]; err != nil {
		return err
	}
	result, override := r.outputs[key]
	if !override {
		switch {
		case name == "docker" && len(args) > 0 && args[0] == "compose":
			if strings.Contains(key, " ps -q ") {
				result = "sub2api-app"
			}
			if strings.Contains(key, " up ") {
				r.recreateCount++
				if r.recreateCount == 1 {
					r.image = newID
				} else {
					r.image = oldID
				}
			}
		case strings.HasPrefix(key, "docker image inspect "):
			result = newID + "|" + targetVersion + "|" + revision
		case strings.HasPrefix(key, "docker inspect --format {{.Image}}|"):
			health := "healthy"
			if r.image == newID && r.newHealth != "" {
				health = r.newHealth
			}
			if r.image == oldID && r.rollbackHealth != "" {
				health = r.rollbackHealth
			}
			actual := r.image
			if r.wrongImage && actual == newID {
				actual = oldID
			}
			result = actual + "|" + health
		case strings.HasPrefix(key, "docker inspect --format {{.Image}} "):
			result = r.image
		case strings.HasPrefix(key, "docker create "):
			result = "extract-container"
		}
	}
	if name == "docker" && len(args) == 3 && args[0] == "cp" {
		return os.WriteFile(args[2], []byte("new updater"), 0755)
	}
	if stdout != nil {
		_, _ = io.WriteString(stdout, result)
	}
	return nil
}
func (r *fakeRunner) Output(ctx context.Context, dir, name string, args ...string) (string, error) {
	var out bytes.Buffer
	err := r.Run(ctx, dir, &out, &out, name, args...)
	return strings.TrimSpace(out.String()), err
}
func (r *fakeRunner) callList() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.calls...)
}
func commandKey(name string, args ...string) string { return name + " " + strings.Join(args, " ") }
func newTestUpdater(t *testing.T, r *fakeRunner) *Updater {
	t.Helper()
	repo := t.TempDir()
	deploy := filepath.Join(repo, "deploy")
	require.NoError(t, os.MkdirAll(deploy, 0755))
	compose := filepath.Join(deploy, "docker-compose.yml")
	require.NoError(t, os.WriteFile(compose, []byte("services: {}\n"), 0600))
	if r.outputs == nil {
		r.outputs = map[string]string{}
	}
	if r.errors == nil {
		r.errors = map[string]error{}
	}
	r.image = oldID
	u, err := NewWithRunner(Config{Token: strings.Repeat("a", 32), RepoDir: repo, StateDir: filepath.Join(repo, ".state"), UpdaterBinary: filepath.Join(repo, "bin/updater"), ComposeProject: "test-project", ComposeFiles: []string{compose}, AppService: "sub2api", PostgresService: "postgres", AppContainer: "sub2api-app", HealthTimeout: time.Millisecond,
		ResolveManifest: func(_ context.Context, version string) (*updatepolicy.Manifest, error) {
			require.Equal(t, targetVersion, version)
			return &updatepolicy.Manifest{SchemaVersion: 1, Version: targetVersion, Revision: revision, Images: map[string]string{runtime.GOARCH: pinnedImage}}, nil
		}}, r)
	require.NoError(t, err)
	return u
}
func queue(u *Updater) {
	u.jobs["test-job"] = Job{JobID: "test-job", TargetVersion: targetVersion, Status: "queued"}
}
func TestUpdaterRejectsUnauthorizedRequests(t *testing.T) {
	u := newTestUpdater(t, &fakeRunner{})
	rec := httptest.NewRecorder()
	u.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))
	require.Equal(t, 401, rec.Code)
}
func TestUpdaterDuplicateAndConcurrentRequests(t *testing.T) {
	u := newTestUpdater(t, &fakeRunner{})
	u.jobs["existing"] = Job{JobID: "existing", RequestID: "same", Status: "running"}
	u.activeID = "existing"
	request := func(id, version string) int {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/v1/update", strings.NewReader(`{"request_id":"`+id+`","expected_version":"`+version+`"}`))
		req.Header.Set("X-Sub2API-Updater-Token", u.cfg.Token)
		u.Handler().ServeHTTP(rec, req)
		return rec.Code
	}
	require.Equal(t, 202, request("same", targetVersion))
	require.Equal(t, 409, request("different", targetVersion))
	require.Equal(t, 400, request("bad", "latest"))
}
func TestImageWorkflowPinsReleasePreservesConfigAndReplacesUpdater(t *testing.T) {
	r := &fakeRunner{}
	u := newTestUpdater(t, r)
	queue(u)
	require.NoError(t, os.WriteFile(filepath.Join(u.cfg.RepoDir, "FORK_VERSION"), []byte("unrelated source"), 0600))
	original, err := os.ReadFile(u.cfg.ComposeFiles[0])
	require.NoError(t, err)
	require.NoError(t, u.workflow(context.Background(), "test-job"))
	binary, err := os.ReadFile(u.cfg.UpdaterBinary)
	require.NoError(t, err)
	require.Equal(t, "new updater", string(binary))
	after, err := os.ReadFile(u.cfg.ComposeFiles[0])
	require.NoError(t, err)
	require.Equal(t, original, after)
	overlay, err := os.ReadFile(u.overlayPath())
	require.NoError(t, err)
	require.Contains(t, string(overlay), pinnedImage)
	calls := strings.Join(r.callList(), "\n")
	require.Contains(t, calls, "docker pull "+pinnedImage)
	require.Contains(t, calls, "--no-deps --no-build --pull never sub2api")
	require.Contains(t, calls, updatepolicy.UpdaterPath)
	require.NotContains(t, calls, "git ")
	require.NotContains(t, calls, "docker build")
	require.Equal(t, 1, r.recreateCount)
	require.Equal(t, oldID, u.jobs["test-job"].PreviousImage)
}
func TestPullFailureLeavesExistingDeploymentAndOverlayUntouched(t *testing.T) {
	r := &fakeRunner{}
	u := newTestUpdater(t, r)
	queue(u)
	require.NoError(t, u.writeOverlay(oldID))
	before, err := os.ReadFile(u.overlayPath())
	require.NoError(t, err)
	r.errors["docker pull "+pinnedImage] = errors.New("registry offline")
	require.ErrorContains(t, u.workflow(context.Background(), "test-job"), "image pull failed")
	after, err := os.ReadFile(u.overlayPath())
	require.NoError(t, err)
	require.Equal(t, before, after)
	require.Zero(t, r.recreateCount)
}
func TestManifestMustMatchRequestedVersion(t *testing.T) {
	r := &fakeRunner{}
	u := newTestUpdater(t, r)
	queue(u)
	u.cfg.ResolveManifest = func(context.Context, string) (*updatepolicy.Manifest, error) {
		return &updatepolicy.Manifest{SchemaVersion: 1, Version: "0.2.17-custom.14", Revision: revision, Images: map[string]string{runtime.GOARCH: pinnedImage}}, nil
	}
	require.ErrorContains(t, u.workflow(context.Background(), "test-job"), "mismatch")
	require.NotContains(t, strings.Join(r.callList(), "\n"), "docker pull")
}
func TestUnhealthyOrWrongImageRestoresVerifiedOldImage(t *testing.T) {
	for _, wrong := range []bool{false, true} {
		t.Run(map[bool]string{false: "unhealthy", true: "wrong identity"}[wrong], func(t *testing.T) {
			r := &fakeRunner{newHealth: "unhealthy", wrongImage: wrong}
			if wrong {
				r.newHealth = "healthy"
			}
			u := newTestUpdater(t, r)
			queue(u)
			require.ErrorContains(t, u.workflow(context.Background(), "test-job"), "previous application image restored and healthy")
			require.Equal(t, 2, r.recreateCount)
			overlay, err := os.ReadFile(u.overlayPath())
			require.NoError(t, err)
			require.Contains(t, string(overlay), oldID)
			_, err = os.Stat(u.cfg.UpdaterBinary)
			require.True(t, os.IsNotExist(err))
		})
	}
}
func TestRollbackFailureIsReported(t *testing.T) {
	r := &fakeRunner{newHealth: "unhealthy", rollbackHealth: "unhealthy"}
	u := newTestUpdater(t, r)
	queue(u)
	require.ErrorContains(t, u.workflow(context.Background(), "test-job"), "automatic rollback incomplete")
}
func TestImageRevisionMismatchDoesNotDeploy(t *testing.T) {
	r := &fakeRunner{}
	u := newTestUpdater(t, r)
	queue(u)
	key := commandKey("docker", "image", "inspect", "--format", `{{.Id}}|{{index .Config.Labels "org.opencontainers.image.version"}}|{{index .Config.Labels "org.opencontainers.image.revision"}}`, pinnedImage)
	r.outputs[key] = newID + "|" + targetVersion + "|wrong"
	require.ErrorContains(t, u.workflow(context.Background(), "test-job"), "does not match")
	require.Zero(t, r.recreateCount)
}

type pausedDeployRunner struct {
	*fakeRunner
	started chan struct{}
	resume  chan struct{}
}

func (r *pausedDeployRunner) Output(ctx context.Context, dir, name string, args ...string) (string, error) {
	if name == "docker" && strings.Contains(commandKey(name, args...), " up ") {
		close(r.started)
		select {
		case <-r.resume:
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	return r.fakeRunner.Output(ctx, dir, name, args...)
}

func TestRestartRecoversInterruptedDeploymentBeforeAcceptingAnotherUpdate(t *testing.T) {
	r := &fakeRunner{}
	u := newTestUpdater(t, r)
	queue(u)
	u.jobs["test-job"] = Job{JobID: "test-job", Status: "running", Stage: "health_check", PreviousImage: oldID, TargetImage: pinnedImage}
	require.NoError(t, u.persistCurrentJob("test-job"))
	require.NoError(t, u.writeOverlay(pinnedImage))
	r.image, r.recreateCount = newID, 1
	paused := &pausedDeployRunner{fakeRunner: r, started: make(chan struct{}), resume: make(chan struct{})}
	recovered, err := NewWithRunner(u.cfg, paused)
	require.NoError(t, err)
	// Always unblock the recovery goroutine even if an assertion fails.
	t.Cleanup(func() {
		select {
		case <-paused.resume:
		default:
			close(paused.resume)
		}
	})
	select {
	case <-paused.started:
	case <-time.After(time.Second):
		t.Fatal("interrupted update did not attempt recovery")
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/update", strings.NewReader(`{"request_id":"new-request","expected_version":"`+targetVersion+`"}`))
	req.Header.Set("X-Sub2API-Updater-Token", u.cfg.Token)
	rec := httptest.NewRecorder()
	recovered.Handler().ServeHTTP(rec, req)
	require.Equal(t, http.StatusConflict, rec.Code)
	close(paused.resume)
	require.Eventually(t, func() bool {
		recovered.mu.RLock()
		defer recovered.mu.RUnlock()
		return recovered.jobs["test-job"].Status == "failed" && recovered.activeID == ""
	}, time.Second, time.Millisecond)
	data, err := os.ReadFile(filepath.Join(u.cfg.StateDir, "test-job.json"))
	require.NoError(t, err)
	var job Job
	require.NoError(t, json.Unmarshal(data, &job))
	require.Contains(t, job.Error, "previous application image restored and healthy")
	require.NotEmpty(t, job.FinishedAt)
	// Recovery must restore the old image and must not pull or run a new update.
	require.NotContains(t, strings.Join(r.callList(), "\n"), "docker pull")
	require.Equal(t, oldID, r.image)
}

func TestRestartDuringPullDoesNotRecreateApplication(t *testing.T) {
	r := &fakeRunner{}
	u := newTestUpdater(t, r)
	u.jobs["test-job"] = Job{JobID: "test-job", Status: "running", Stage: "pulling", PreviousImage: oldID, TargetImage: pinnedImage}
	require.NoError(t, u.persistCurrentJob("test-job"))
	recovered, err := NewWithRunner(u.cfg, r)
	require.NoError(t, err)
	require.Equal(t, "failed", recovered.jobs["test-job"].Status)
	require.Empty(t, recovered.activeID)
	require.Empty(t, r.callList())
}

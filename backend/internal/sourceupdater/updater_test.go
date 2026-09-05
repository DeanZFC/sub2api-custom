package sourceupdater

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type fakeRunner struct {
	mu      sync.Mutex
	calls   []string
	outputs map[string]string
	errors  map[string]error
}

func (r *fakeRunner) Run(_ context.Context, _ string, stdout, _ io.Writer, name string, args ...string) error {
	key := commandKey(name, args...)
	r.record(key)
	if err := r.errors[key]; err != nil {
		return err
	}
	if name == "docker" && len(args) == 3 && args[0] == "cp" {
		return os.WriteFile(args[2], []byte("new updater"), 0o755)
	}
	if stdout != nil {
		_, _ = io.WriteString(stdout, r.outputs[key])
	}
	return nil
}

func (r *fakeRunner) Output(ctx context.Context, dir, name string, args ...string) (string, error) {
	var output bytes.Buffer
	err := r.Run(ctx, dir, &output, &output, name, args...)
	return strings.TrimSpace(output.String()), err
}

func (r *fakeRunner) record(call string) {
	r.mu.Lock()
	r.calls = append(r.calls, call)
	r.mu.Unlock()
}

func (r *fakeRunner) callList() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.calls...)
}

func commandKey(name string, args ...string) string {
	return name + " " + strings.Join(args, " ")
}

func newTestUpdater(t *testing.T, runner *fakeRunner) *Updater {
	t.Helper()
	repoDir := t.TempDir()
	deployDir := filepath.Join(repoDir, "deploy")
	require.NoError(t, os.MkdirAll(deployDir, 0o755))
	composeFile := filepath.Join(deployDir, "docker-compose.yml")
	require.NoError(t, os.WriteFile(composeFile, []byte("services: {}\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(repoDir, "FORK_VERSION"), []byte("0.2.0-custom.1\n"), 0o600))

	if runner.outputs == nil {
		runner.outputs = make(map[string]string)
	}
	if runner.errors == nil {
		runner.errors = make(map[string]error)
	}
	runner.outputs[commandKey("git", "remote", "get-url", "origin")] = ExpectedRemoteURL
	runner.outputs[commandKey("git", "branch", "--show-current")] = ExpectedBranch
	runner.outputs[commandKey("git", "rev-parse", "HEAD")] = "old-commit"
	runner.outputs[commandKey("docker", "inspect", "--format", "{{.Image}}", "sub2api-app")] = "sha256:old"
	runner.outputs[commandKey("docker", "inspect", "--format", "{{.Config.Image}}", "sub2api-app")] = "sub2api-custom:test"
	runner.outputs[commandKey("docker", "compose", "-p", "test-project", "-f", composeFile, "ps", "-q", "sub2api")] = "sub2api-app"
	runner.outputs[commandKey("docker", "create", "sub2api-source-updater-builder:test-job")] = "builder-container"
	runner.outputs[commandKey("docker", "inspect", "--format", "{{if .State.Health}}{{.State.Health.Status}}{{else}}{{.State.Status}}{{end}}", "sub2api-app")] = "healthy"

	updater, err := NewWithRunner(Config{
		Token:           strings.Repeat("a", 32),
		RepoDir:         repoDir,
		StateDir:        filepath.Join(repoDir, ".state"),
		UpdaterBinary:   filepath.Join(repoDir, "bin", "sub2api-updater"),
		ComposeProject:  "test-project",
		ComposeFiles:    []string{composeFile},
		AppService:      "sub2api",
		PostgresService: "postgres",
		AppContainer:    "sub2api-app",
		HealthTimeout:   time.Millisecond,
	}, runner)
	require.NoError(t, err)
	return updater
}

func TestUpdaterRejectsUnauthorizedRequests(t *testing.T) {
	updater := newTestUpdater(t, &fakeRunner{})
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/health", nil)

	updater.Handler().ServeHTTP(recorder, request)

	require.Equal(t, http.StatusUnauthorized, recorder.Code)
}

func TestUpdaterReturnsExistingJobForDuplicateRequestAndRejectsConcurrentRequest(t *testing.T) {
	updater := newTestUpdater(t, &fakeRunner{})
	existing := Job{JobID: "existing-job", RequestID: "same-request", Status: "running"}
	updater.jobs[existing.JobID] = existing
	updater.activeID = existing.JobID

	request := func(requestID string) *httptest.ResponseRecorder {
		recorder := httptest.NewRecorder()
		body := strings.NewReader(`{"request_id":"` + requestID + `","expected_version":"0.2.0-custom.2"}`)
		req := httptest.NewRequest(http.MethodPost, "/v1/update", body)
		req.Header.Set("X-Sub2API-Updater-Token", updater.cfg.Token)
		updater.Handler().ServeHTTP(recorder, req)
		return recorder
	}

	require.Equal(t, http.StatusAccepted, request("same-request").Code)
	require.Equal(t, http.StatusConflict, request("another-request").Code)
}

func TestUpdaterValidateDeploymentRejectsDirtyWrongRemoteAndWrongBranch(t *testing.T) {
	tests := []struct {
		name    string
		key     string
		value   string
		wantErr string
	}{
		{name: "dirty", key: commandKey("git", "status", "--porcelain", "--untracked-files=no"), value: " M backend/main.go", wantErr: "tracked local changes"},
		{name: "remote", key: commandKey("git", "remote", "get-url", "origin"), value: "https://example.com/other.git", wantErr: "origin must be"},
		{name: "branch", key: commandKey("git", "branch", "--show-current"), value: "main", wantErr: "deployed branch must be"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runner := &fakeRunner{}
			updater := newTestUpdater(t, runner)
			runner.outputs[tt.key] = tt.value

			_, _, _, _, err := updater.validateDeployment(context.Background())

			require.ErrorContains(t, err, tt.wantErr)
		})
	}
}

func TestUpdaterWorkflowRunsBuildDeployHealthAndSelfUpdate(t *testing.T) {
	runner := &fakeRunner{}
	updater := newTestUpdater(t, runner)
	job := Job{JobID: "test-job", Status: "queued"}
	updater.jobs[job.JobID] = job

	err := updater.workflow(context.Background(), job.JobID)

	require.NoError(t, err)
	binary, err := os.ReadFile(updater.cfg.UpdaterBinary)
	require.NoError(t, err)
	require.Equal(t, "new updater", string(binary))
	requireCallsInOrder(t, runner.callList(),
		"git status --porcelain --untracked-files=no",
		"git fetch --prune origin "+ExpectedBranch,
		"git merge --ff-only origin/"+ExpectedBranch,
		"docker build --pull -f "+filepath.Join(updater.cfg.RepoDir, "deploy", "Dockerfile.updater"),
		"docker compose -p test-project -f "+updater.cfg.ComposeFiles[0]+" build --pull sub2api",
		"docker compose -p test-project -f "+updater.cfg.ComposeFiles[0]+" up -d --force-recreate --no-deps sub2api",
		"docker inspect --format {{if .State.Health}}{{.State.Health.Status}}{{else}}{{.State.Status}}{{end}} sub2api-app",
	)
}

func TestUpdaterWorkflowRestoresSourceWhenApplicationBuildFails(t *testing.T) {
	runner := &fakeRunner{}
	updater := newTestUpdater(t, runner)
	updater.jobs["test-job"] = Job{JobID: "test-job"}
	buildKey := commandKey("docker", "compose", "-p", "test-project", "-f", updater.cfg.ComposeFiles[0], "build", "--pull", "sub2api")
	runner.errors[buildKey] = errors.New("build failed")

	err := updater.workflow(context.Background(), "test-job")

	require.ErrorContains(t, err, "image build failed")
	require.Contains(t, runner.callList(), "git reset --hard old-commit")
}

func TestUpdaterWorkflowRollsBackImageWhenHealthCheckFails(t *testing.T) {
	runner := &fakeRunner{}
	updater := newTestUpdater(t, runner)
	updater.jobs["test-job"] = Job{JobID: "test-job"}
	healthKey := commandKey("docker", "inspect", "--format", "{{if .State.Health}}{{.State.Health.Status}}{{else}}{{.State.Status}}{{end}}", "sub2api-app")
	runner.outputs[healthKey] = "unhealthy"

	err := updater.workflow(context.Background(), "test-job")

	require.ErrorContains(t, err, "previous application image restored")
	calls := runner.callList()
	require.Contains(t, calls, "git reset --hard old-commit")
	require.Contains(t, calls, "docker image tag sha256:old sub2api-custom:test")
	require.Contains(t, calls, "docker compose -p test-project -f "+updater.cfg.ComposeFiles[0]+" up -d --force-recreate --no-deps --no-build sub2api")
}

func requireCallsInOrder(t *testing.T, calls []string, fragments ...string) {
	t.Helper()
	index := 0
	for _, call := range calls {
		if index < len(fragments) && strings.Contains(call, fragments[index]) {
			index++
		}
	}
	missing := "<none>"
	if index < len(fragments) {
		missing = fragments[index]
	}
	require.Equalf(t, len(fragments), index, "missing ordered fragment %q in calls:\n%s", missing, strings.Join(calls, "\n"))
}

package sourceupdater

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	ExpectedRemoteURL = "https://github.com/DeanZFC/sub2api-custom.git"
	ExpectedBranch    = "sub2api-custom"
	maxRequestBody    = 16 << 10
	maxCommandOutput  = 16 << 10
)

var safeNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`)

type Config struct {
	Token          string
	RepoDir        string
	StateDir       string
	UpdaterBinary  string
	ComposeProject string
	ComposeFiles   []string
	AppService     string
	// PostgresService is retained for compatibility with existing updater
	// service units. Updates no longer run database backups.
	PostgresService string
	AppContainer    string
	HealthTimeout   time.Duration
}

func (c *Config) Validate() error {
	c.Token = strings.TrimSpace(c.Token)
	c.RepoDir = filepath.Clean(strings.TrimSpace(c.RepoDir))
	c.StateDir = filepath.Clean(strings.TrimSpace(c.StateDir))
	c.UpdaterBinary = filepath.Clean(strings.TrimSpace(c.UpdaterBinary))
	c.ComposeProject = strings.TrimSpace(c.ComposeProject)
	c.AppService = strings.TrimSpace(c.AppService)
	c.PostgresService = strings.TrimSpace(c.PostgresService)
	c.AppContainer = strings.TrimSpace(c.AppContainer)
	if len(c.Token) < 32 {
		return errors.New("updater token must contain at least 32 characters")
	}
	if !filepath.IsAbs(c.RepoDir) || !filepath.IsAbs(c.StateDir) {
		return errors.New("repository and state directories must be absolute paths")
	}
	if c.UpdaterBinary == "" {
		c.UpdaterBinary = "/usr/local/libexec/sub2api-updater"
	}
	if !filepath.IsAbs(c.UpdaterBinary) {
		return errors.New("updater binary path must be absolute")
	}
	if !safeNamePattern.MatchString(c.ComposeProject) || !safeNamePattern.MatchString(c.AppService) ||
		!safeNamePattern.MatchString(c.PostgresService) || !safeNamePattern.MatchString(c.AppContainer) {
		return errors.New("compose project, service, or container name is invalid")
	}
	if len(c.ComposeFiles) == 0 {
		return errors.New("at least one compose file is required")
	}
	for i, file := range c.ComposeFiles {
		file = filepath.Clean(strings.TrimSpace(file))
		if !filepath.IsAbs(file) || !pathWithin(file, c.RepoDir) {
			return fmt.Errorf("compose file must be an absolute path inside repository: %q", file)
		}
		if info, err := os.Stat(file); err != nil || info.IsDir() {
			return fmt.Errorf("compose file is unavailable: %q", file)
		}
		c.ComposeFiles[i] = file
	}
	if c.HealthTimeout <= 0 {
		c.HealthTimeout = 3 * time.Minute
	}
	return nil
}

func pathWithin(path, root string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

type Job struct {
	JobID          string `json:"job_id"`
	RequestID      string `json:"request_id,omitempty"`
	Status         string `json:"status"`
	Stage          string `json:"stage,omitempty"`
	Message        string `json:"message,omitempty"`
	Error          string `json:"error,omitempty"`
	CurrentVersion string `json:"current_version,omitempty"`
	TargetVersion  string `json:"target_version,omitempty"`
	StartedAt      string `json:"started_at,omitempty"`
	FinishedAt     string `json:"finished_at,omitempty"`
}

type Runner interface {
	Run(ctx context.Context, dir string, stdout, stderr io.Writer, name string, args ...string) error
	Output(ctx context.Context, dir, name string, args ...string) (string, error)
}

type commandRunner struct{}

func (commandRunner) Run(ctx context.Context, dir string, stdout, stderr io.Writer, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.Env = append(os.Environ(),
		"GIT_CONFIG_COUNT=1",
		"GIT_CONFIG_KEY_0=safe.directory",
		"GIT_CONFIG_VALUE_0="+dir,
	)
	return cmd.Run()
}

func (r commandRunner) Output(ctx context.Context, dir, name string, args ...string) (string, error) {
	var output limitedBuffer
	err := r.Run(ctx, dir, &output, &output, name, args...)
	return strings.TrimSpace(output.String()), err
}

type Updater struct {
	cfg                Config
	runner             Runner
	onSuccessfulUpdate func()

	mu       sync.RWMutex
	jobs     map[string]Job
	activeID string
}

// SetSuccessfulUpdateHook installs a process-lifecycle hook. The host binary
// uses it to ask systemd for a clean restart after replacing itself, ensuring
// the next job executes the newly compiled updater code.
func (u *Updater) SetSuccessfulUpdateHook(hook func()) {
	if u == nil {
		return
	}
	u.mu.Lock()
	u.onSuccessfulUpdate = hook
	u.mu.Unlock()
}

func New(cfg Config) (*Updater, error) {
	return NewWithRunner(cfg, commandRunner{})
}

func NewWithRunner(cfg Config, runner Runner) (*Updater, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if runner == nil {
		return nil, errors.New("command runner is required")
	}
	if err := os.MkdirAll(cfg.StateDir, 0o700); err != nil {
		return nil, fmt.Errorf("create updater state directory: %w", err)
	}
	u := &Updater{cfg: cfg, runner: runner, jobs: make(map[string]Job)}
	if err := u.loadJobs(); err != nil {
		return nil, err
	}
	return u, nil
}

func (u *Updater) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/update", u.authorize(u.startUpdate))
	mux.HandleFunc("GET /v1/update/{job_id}", u.authorize(u.getUpdate))
	mux.HandleFunc("GET /health", u.authorize(func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}))
	return mux
}

func (u *Updater) authorize(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		provided := r.Header.Get("X-Sub2API-Updater-Token")
		if len(provided) != len(u.cfg.Token) || subtle.ConstantTimeCompare([]byte(provided), []byte(u.cfg.Token)) != 1 {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			return
		}
		next(w, r)
	}
}

type startRequest struct {
	RequestID       string `json:"request_id"`
	ExpectedVersion string `json:"expected_version"`
}

func (u *Updater) startUpdate(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBody)
	var req startRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
		return
	}
	req.RequestID = strings.TrimSpace(req.RequestID)
	req.ExpectedVersion = strings.TrimSpace(req.ExpectedVersion)
	if !safeNamePattern.MatchString(req.RequestID) || len(req.ExpectedVersion) > 64 || strings.ContainsAny(req.ExpectedVersion, "\r\n\x00") {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid update request"})
		return
	}

	u.mu.Lock()
	for _, existing := range u.jobs {
		if existing.RequestID == req.RequestID {
			u.mu.Unlock()
			writeJSON(w, http.StatusAccepted, existing)
			return
		}
	}
	if u.activeID != "" {
		active := u.jobs[u.activeID]
		u.mu.Unlock()
		writeJSON(w, http.StatusConflict, map[string]string{
			"error":   "update already running",
			"message": "update already running: " + active.JobID,
		})
		return
	}
	jobID, err := newJobID()
	if err != nil {
		u.mu.Unlock()
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "cannot create update job"})
		return
	}
	job := Job{
		JobID:         jobID,
		RequestID:     req.RequestID,
		Status:        "queued",
		Stage:         "queued",
		Message:       "update queued",
		TargetVersion: req.ExpectedVersion,
		StartedAt:     time.Now().UTC().Format(time.RFC3339),
	}
	u.jobs[jobID] = job
	u.activeID = jobID
	if err := u.persistLocked(job); err != nil {
		delete(u.jobs, jobID)
		u.activeID = ""
		u.mu.Unlock()
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "cannot persist update job"})
		return
	}
	u.mu.Unlock()

	go u.run(jobID)
	writeJSON(w, http.StatusAccepted, job)
}

func (u *Updater) getUpdate(w http.ResponseWriter, r *http.Request) {
	jobID := strings.TrimSpace(r.PathValue("job_id"))
	if !safeNamePattern.MatchString(jobID) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid job id"})
		return
	}
	u.mu.RLock()
	job, ok := u.jobs[jobID]
	u.mu.RUnlock()
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "job not found"})
		return
	}
	writeJSON(w, http.StatusOK, job)
}

func (u *Updater) run(jobID string) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Minute)
	defer cancel()
	if err := u.workflow(ctx, jobID); err != nil {
		u.finish(jobID, "failed", "failed", "update failed", sanitizeError(err))
		return
	}
	u.finish(jobID, "succeeded", "completed", "update completed", "")
	u.mu.RLock()
	hook := u.onSuccessfulUpdate
	u.mu.RUnlock()
	if hook != nil {
		hook()
	}
}

func (u *Updater) workflow(ctx context.Context, jobID string) error {
	u.update(jobID, "running", "validating", "validating deployment")
	currentVersion, oldCommit, oldImageID, imageName, err := u.validateDeployment(ctx)
	if err != nil {
		return err
	}
	u.mutate(jobID, func(job *Job) { job.CurrentVersion = currentVersion })

	u.update(jobID, "running", "fetching", "fetching source update")
	if _, err := u.runOutput(ctx, "git", "fetch", "--prune", "origin", ExpectedBranch); err != nil {
		return fmt.Errorf("git fetch failed: %w", err)
	}
	if _, err := u.runOutput(ctx, "git", "merge-base", "--is-ancestor", "HEAD", "origin/"+ExpectedBranch); err != nil {
		return errors.New("remote update is not a fast-forward of the deployed commit")
	}
	if _, err := u.runOutput(ctx, "git", "merge", "--ff-only", "origin/"+ExpectedBranch); err != nil {
		return fmt.Errorf("git fast-forward failed: %w", err)
	}
	targetVersion, err := readVersion(filepath.Join(u.cfg.RepoDir, "FORK_VERSION"))
	if err != nil {
		return u.restoreSource(oldCommit, err)
	}
	u.mutate(jobID, func(job *Job) { job.TargetVersion = targetVersion })

	u.update(jobID, "running", "building", "building application image")
	stagedUpdater, err := u.buildUpdater(ctx, jobID)
	if err != nil {
		return u.restoreSource(oldCommit, fmt.Errorf("updater build failed: %w", err))
	}
	defer os.Remove(stagedUpdater)

	if _, err := u.runOutput(ctx, "docker", append(u.composeArgs(), "build", "--pull", u.cfg.AppService)...); err != nil {
		return u.restoreSource(oldCommit, fmt.Errorf("image build failed: %w", err))
	}

	u.update(jobID, "running", "deploying", "recreating application container")
	if _, err := u.runOutput(ctx, "docker", append(u.composeArgs(), "up", "-d", "--force-recreate", "--no-deps", u.cfg.AppService)...); err != nil {
		return u.rollback(ctx, jobID, oldCommit, oldImageID, imageName, fmt.Errorf("container recreation failed: %w", err))
	}

	u.update(jobID, "running", "health_check", "waiting for application health check")
	if err := u.waitHealthy(ctx); err != nil {
		return u.rollback(ctx, jobID, oldCommit, oldImageID, imageName, err)
	}
	if err := u.installUpdater(stagedUpdater); err != nil {
		return u.rollback(ctx, jobID, oldCommit, oldImageID, imageName, fmt.Errorf("updater replacement failed: %w", err))
	}
	return nil
}

func (u *Updater) buildUpdater(ctx context.Context, jobID string) (string, error) {
	imageTag := "sub2api-source-updater-builder:" + jobID
	if _, err := u.runOutput(ctx, "docker", "build", "--pull", "-f", filepath.Join(u.cfg.RepoDir, "deploy", "Dockerfile.updater"), "-t", imageTag, u.cfg.RepoDir); err != nil {
		return "", err
	}
	defer func() { _, _ = u.runOutput(context.Background(), "docker", "image", "rm", imageTag) }()
	containerID, err := u.runOutput(ctx, "docker", "create", imageTag)
	if err != nil || strings.TrimSpace(containerID) == "" {
		return "", fmt.Errorf("create updater builder container: %w", err)
	}
	containerID = strings.TrimSpace(containerID)
	defer func() { _, _ = u.runOutput(context.Background(), "docker", "rm", "-f", containerID) }()

	// Keep the destination absent: docker cp reliably creates a file when the
	// target does not already exist, while behavior for an existing file differs
	// between Docker versions.
	stagedPath := filepath.Join(u.cfg.StateDir, ".sub2api-updater-"+jobID)
	_ = os.Remove(stagedPath)
	if _, err := u.runOutput(ctx, "docker", "cp", containerID+":/sub2api-updater", stagedPath); err != nil {
		_ = os.Remove(stagedPath)
		return "", err
	}
	if err := os.Chmod(stagedPath, 0o755); err != nil {
		_ = os.Remove(stagedPath)
		return "", err
	}
	return stagedPath, nil
}

func (u *Updater) installUpdater(stagedPath string) error {
	if strings.TrimSpace(stagedPath) == "" {
		return errors.New("staged updater path is empty")
	}
	dir := filepath.Dir(u.cfg.UpdaterBinary)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	temp, err := os.CreateTemp(dir, ".sub2api-updater-*")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err := temp.Chmod(0o755); err != nil {
		_ = temp.Close()
		return err
	}
	source, err := os.Open(stagedPath)
	if err != nil {
		_ = temp.Close()
		return err
	}
	_, copyErr := io.Copy(temp, source)
	closeSourceErr := source.Close()
	if copyErr != nil {
		_ = temp.Close()
		return copyErr
	}
	if closeSourceErr != nil {
		_ = temp.Close()
		return closeSourceErr
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(tempPath, u.cfg.UpdaterBinary)
}

func (u *Updater) validateDeployment(ctx context.Context) (version, commit, imageID, imageName string, err error) {
	if dirty, runErr := u.runOutput(ctx, "git", "status", "--porcelain", "--untracked-files=no"); runErr != nil {
		return "", "", "", "", fmt.Errorf("git status failed: %w", runErr)
	} else if dirty != "" {
		return "", "", "", "", errors.New("repository has tracked local changes; automatic update refused")
	}
	remote, err := u.runOutput(ctx, "git", "remote", "get-url", "origin")
	if err != nil || normalizeRemote(remote) != ExpectedRemoteURL {
		return "", "", "", "", fmt.Errorf("origin must be %s", ExpectedRemoteURL)
	}
	branch, err := u.runOutput(ctx, "git", "branch", "--show-current")
	if err != nil || branch != ExpectedBranch {
		return "", "", "", "", fmt.Errorf("deployed branch must be %s", ExpectedBranch)
	}
	commit, err = u.runOutput(ctx, "git", "rev-parse", "HEAD")
	if err != nil {
		return "", "", "", "", fmt.Errorf("read deployed commit: %w", err)
	}
	version, err = readVersion(filepath.Join(u.cfg.RepoDir, "FORK_VERSION"))
	if err != nil {
		return "", "", "", "", err
	}
	appTarget, targetErr := u.currentAppTarget(ctx)
	if targetErr != nil {
		return "", "", "", "", targetErr
	}
	imageID, err = u.runner.Output(ctx, u.cfg.RepoDir, "docker", "inspect", "--format", "{{.Image}}", appTarget)
	if err != nil || imageID == "" {
		return "", "", "", "", fmt.Errorf("inspect current container image: %w", err)
	}
	imageName, err = u.runner.Output(ctx, u.cfg.RepoDir, "docker", "inspect", "--format", "{{.Config.Image}}", appTarget)
	if err != nil || imageName == "" {
		return "", "", "", "", fmt.Errorf("inspect current container image name: %w", err)
	}
	return version, commit, imageID, imageName, nil
}

func (u *Updater) rollback(ctx context.Context, jobID, oldCommit, oldImageID, imageName string, cause error) error {
	_ = ctx
	rollbackCtx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	u.update(jobID, "running", "rolling_back", "health check failed; restoring previous image")
	var failures []string
	if _, err := u.runOutput(rollbackCtx, "git", "reset", "--hard", oldCommit); err != nil {
		failures = append(failures, "source restore: "+err.Error())
	}
	if output, err := u.runner.Output(rollbackCtx, u.cfg.RepoDir, "docker", "image", "tag", oldImageID, imageName); err != nil {
		failures = append(failures, "image restore: "+output)
	}
	if _, err := u.runOutput(rollbackCtx, "docker", append(u.composeArgs(), "up", "-d", "--force-recreate", "--no-deps", "--no-build", u.cfg.AppService)...); err != nil {
		failures = append(failures, "container restore: "+err.Error())
	}
	if len(failures) > 0 {
		return fmt.Errorf("%w; automatic rollback incomplete: %s", cause, strings.Join(failures, "; "))
	}
	return fmt.Errorf("%w; previous application image restored", cause)
}

func (u *Updater) restoreSource(oldCommit string, cause error) error {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if _, err := u.runOutput(ctx, "git", "reset", "--hard", oldCommit); err != nil {
		return fmt.Errorf("%w; source restore failed: %v", cause, err)
	}
	return cause
}

func (u *Updater) waitHealthy(ctx context.Context) error {
	deadline := time.Now().Add(u.cfg.HealthTimeout)
	for time.Now().Before(deadline) {
		appTarget, targetErr := u.currentAppTarget(ctx)
		status, err := "", targetErr
		if targetErr == nil {
			status, err = u.runner.Output(ctx, u.cfg.RepoDir, "docker", "inspect", "--format", "{{if .State.Health}}{{.State.Health.Status}}{{else}}{{.State.Status}}{{end}}", appTarget)
		}
		if err == nil && (status == "healthy" || status == "running") {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
	return errors.New("new application container did not become healthy before timeout")
}

// Compose may replace a legacy fixed-name container with a project-scoped
// generated name after the repository updates its overlay. Resolve the current
// service container on every health poll so old deployments remain upgradeable.
func (u *Updater) currentAppTarget(ctx context.Context) (string, error) {
	containerID, err := u.runOutput(ctx, "docker", append(u.composeArgs(), "ps", "-q", u.cfg.AppService)...)
	if err == nil && strings.TrimSpace(containerID) != "" {
		return strings.TrimSpace(strings.Split(containerID, "\n")[0]), nil
	}
	if u.cfg.AppContainer != "" {
		if _, inspectErr := u.runner.Output(ctx, u.cfg.RepoDir, "docker", "inspect", u.cfg.AppContainer); inspectErr == nil {
			return u.cfg.AppContainer, nil
		}
	}
	if err != nil {
		return "", fmt.Errorf("find application container: %w", err)
	}
	return "", errors.New("find application container: compose returned no container")
}

func (u *Updater) composeArgs() []string {
	args := []string{"compose", "-p", u.cfg.ComposeProject}
	for _, file := range u.cfg.ComposeFiles {
		args = append(args, "-f", file)
	}
	return args
}

func (u *Updater) runOutput(ctx context.Context, name string, args ...string) (string, error) {
	dir := filepath.Join(u.cfg.RepoDir, "deploy")
	if name == "git" {
		dir = u.cfg.RepoDir
	}
	output, err := u.runner.Output(ctx, dir, name, args...)
	if err != nil {
		return output, fmt.Errorf("%s: %w", output, err)
	}
	return output, nil
}

func (u *Updater) update(jobID, status, stage, message string) {
	u.mutate(jobID, func(job *Job) {
		job.Status = status
		job.Stage = stage
		job.Message = message
	})
}

func (u *Updater) finish(jobID, status, stage, message, errorMessage string) {
	u.mu.Lock()
	job := u.jobs[jobID]
	job.Status = status
	job.Stage = stage
	job.Message = message
	job.Error = errorMessage
	job.FinishedAt = time.Now().UTC().Format(time.RFC3339)
	u.jobs[jobID] = job
	if u.activeID == jobID {
		u.activeID = ""
	}
	_ = u.persistLocked(job)
	u.mu.Unlock()
}

func (u *Updater) mutate(jobID string, fn func(*Job)) {
	u.mu.Lock()
	job := u.jobs[jobID]
	fn(&job)
	u.jobs[jobID] = job
	_ = u.persistLocked(job)
	u.mu.Unlock()
}

func (u *Updater) persistLocked(job Job) error {
	data, err := json.MarshalIndent(job, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(u.cfg.StateDir, job.JobID+".json")
	temp, err := os.CreateTemp(u.cfg.StateDir, ".job-*.tmp")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err := temp.Chmod(0o600); err != nil {
		_ = temp.Close()
		return err
	}
	if _, err := temp.Write(data); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(tempPath, path)
}

func (u *Updater) loadJobs() error {
	entries, err := os.ReadDir(u.cfg.StateDir)
	if err != nil {
		return fmt.Errorf("read updater state: %w", err)
	}
	var jobs []Job
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		data, readErr := os.ReadFile(filepath.Join(u.cfg.StateDir, entry.Name()))
		if readErr != nil {
			continue
		}
		var job Job
		if json.Unmarshal(data, &job) == nil && safeNamePattern.MatchString(job.JobID) {
			if job.Status == "queued" || job.Status == "running" {
				job.Status = "failed"
				job.Stage = "failed"
				job.Error = "updater restarted before the job completed"
				job.FinishedAt = time.Now().UTC().Format(time.RFC3339)
			}
			jobs = append(jobs, job)
		}
	}
	sort.Slice(jobs, func(i, j int) bool { return jobs[i].StartedAt > jobs[j].StartedAt })
	if len(jobs) > 50 {
		jobs = jobs[:50]
	}
	for _, job := range jobs {
		u.jobs[job.JobID] = job
		_ = u.persistLocked(job)
	}
	return nil
}

func newJobID() (string, error) {
	var random [8]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", err
	}
	return fmt.Sprintf("upd-%d-%s", time.Now().UTC().Unix(), hex.EncodeToString(random[:])), nil
}

func readVersion(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read FORK_VERSION: %w", err)
	}
	version := strings.TrimSpace(string(data))
	if version == "" || len(version) > 64 || strings.ContainsAny(version, "\r\n\x00") {
		return "", errors.New("FORK_VERSION is invalid")
	}
	return version, nil
}

func normalizeRemote(remote string) string {
	remote = strings.TrimSpace(remote)
	if remote == "git@github.com:DeanZFC/sub2api-custom.git" {
		return ExpectedRemoteURL
	}
	return strings.TrimSuffix(remote, "/")
}

func sanitizeError(err error) string {
	if err == nil {
		return ""
	}
	message := strings.TrimSpace(err.Error())
	if len(message) > maxCommandOutput {
		message = message[len(message)-maxCommandOutput:]
	}
	return message
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

type limitedBuffer struct {
	data []byte
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	b.data = append(b.data, p...)
	if len(b.data) > maxCommandOutput {
		b.data = append([]byte(nil), b.data[len(b.data)-maxCommandOutput:]...)
	}
	return len(p), nil
}

func (b *limitedBuffer) String() string {
	return string(b.data)
}

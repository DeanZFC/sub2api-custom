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
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/updatepolicy"
)

const (
	maxRequestBody   = 16 << 10
	maxCommandOutput = 16 << 10
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
	ResolveManifest func(context.Context, string) (*updatepolicy.Manifest, error)
}

func (c *Config) Validate() error {
	c.Token = strings.TrimSpace(c.Token)
	c.RepoDir = filepath.Clean(strings.TrimSpace(c.RepoDir))
	c.StateDir = filepath.Clean(strings.TrimSpace(c.StateDir))
	c.UpdaterBinary = strings.TrimSpace(c.UpdaterBinary)
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
		c.UpdaterBinary = updatepolicy.UpdaterPath
	}
	c.UpdaterBinary = filepath.Clean(c.UpdaterBinary)
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
	PreviousImage  string `json:"previous_image,omitempty"`
	TargetImage    string `json:"target_image,omitempty"`
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
	// Recover an interrupted container switch before accepting another update.
	var interrupted *Job
	for _, job := range u.jobs {
		if job.Status == "recovering" {
			u.activeID = job.JobID
			interrupted = &job
			break
		}
	}
	if interrupted != nil {
		go func(job Job) {
			err := u.rollbackImage(job.JobID, job.PreviousImage, errors.New("updater restarted during deployment"))
			u.finish(job.JobID, "failed", "failed", "update interrupted", sanitizeError(err))
		}(*interrupted)
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
	if !safeNamePattern.MatchString(req.RequestID) || len(req.ExpectedVersion) > 64 || !updatepolicy.ValidVersion(req.ExpectedVersion) {
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
	oldTarget, err := u.currentAppTarget(ctx)
	if err != nil {
		return err
	}
	oldImage, err := u.runOutput(ctx, "docker", "inspect", "--format", "{{.Image}}", oldTarget)
	if err != nil || !strings.HasPrefix(oldImage, "sha256:") {
		return errors.New("cannot identify current application image")
	}
	u.mu.RLock()
	version := u.jobs[jobID].TargetVersion
	u.mu.RUnlock()
	u.update(jobID, "running", "resolving", "verifying published release manifest")
	resolve := u.cfg.ResolveManifest
	if resolve == nil {
		resolve = updatepolicy.Fetch
	}
	manifest, err := resolve(ctx, version)
	if err != nil {
		return err
	}
	// Apply the same policy even to injected resolvers.
	data, err := json.Marshal(manifest)
	if err != nil {
		return err
	}
	manifest, err = updatepolicy.Parse(data, version)
	if err != nil {
		return err
	}
	image, ok := manifest.Images[runtime.GOARCH]
	if !ok {
		return errors.New("release does not support host architecture")
	}
	u.mutate(jobID, func(job *Job) { job.PreviousImage = oldImage; job.TargetImage = image })
	u.update(jobID, "running", "pulling", "pulling immutable release image")
	if _, err = u.runOutput(ctx, "docker", "pull", image); err != nil {
		return fmt.Errorf("image pull failed: %w", err)
	}
	metadata, err := u.runOutput(ctx, "docker", "image", "inspect", "--format", `{{.Id}}|{{index .Config.Labels "org.opencontainers.image.version"}}|{{index .Config.Labels "org.opencontainers.image.revision"}}`, image)
	if err != nil {
		return fmt.Errorf("inspect release image: %w", err)
	}
	fields := strings.Split(metadata, "|")
	if len(fields) != 3 || !strings.HasPrefix(fields[0], "sha256:") || strings.TrimPrefix(fields[1], "v") != strings.TrimPrefix(manifest.Version, "v") || fields[2] != manifest.Revision {
		return errors.New("pulled image does not match release version and revision")
	}
	staged, err := u.stageUpdater(ctx, jobID, image)
	if err != nil {
		return fmt.Errorf("extract release updater: %w", err)
	}
	defer func() { _ = os.Remove(staged) }()
	// Persist the rollback image before changing any deployment configuration.
	if err = u.persistCurrentJob(jobID); err != nil {
		return err
	}
	u.update(jobID, "running", "deploying", "recreating application container")
	if err = u.persistCurrentJob(jobID); err != nil {
		return err
	}
	if err = u.writeOverlay(image); err != nil {
		return err
	}
	if err = u.recreate(ctx); err != nil {
		return u.rollbackImage(jobID, oldImage, fmt.Errorf("container recreation failed: %w", err))
	}
	u.update(jobID, "running", "health_check", "checking release image and application health")
	if err = u.waitHealthy(ctx, fields[0]); err != nil {
		return u.rollbackImage(jobID, oldImage, err)
	}
	if err = u.installUpdater(staged); err != nil {
		return u.rollbackImage(jobID, oldImage, fmt.Errorf("updater replacement failed: %w", err))
	}
	return nil
}

func (u *Updater) persistCurrentJob(id string) error {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.persistLocked(u.jobs[id])
}
func (u *Updater) overlayPath() string {
	return filepath.Join(u.cfg.RepoDir, "deploy", "docker-compose.updater.json")
}
func (u *Updater) writeOverlay(image string) error {
	data, err := json.Marshal(map[string]any{"services": map[string]any{u.cfg.AppService: map[string]any{"image": image, "pull_policy": "never"}}})
	if err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(u.overlayPath()), ".updater-compose-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(temp.Name()) }()
	if _, err = temp.Write(data); err != nil {
		_ = temp.Close()
		return err
	}
	if err = temp.Sync(); err != nil {
		_ = temp.Close()
		return err
	}
	if err = temp.Close(); err != nil {
		return err
	}
	return os.Rename(temp.Name(), u.overlayPath())
}
func (u *Updater) recreate(ctx context.Context) error {
	_, err := u.runOutput(ctx, "docker", append(u.composeArgs(), "up", "-d", "--force-recreate", "--no-deps", "--no-build", "--pull", "never", u.cfg.AppService)...)
	return err
}
func (u *Updater) rollbackImage(jobID, oldImage string, cause error) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	u.update(jobID, "running", "rolling_back", "restoring previous application image")
	err := u.writeOverlay(oldImage)
	if err == nil {
		err = u.recreate(ctx)
	}
	if err == nil {
		err = u.waitHealthy(ctx, oldImage)
	}
	if err != nil {
		return fmt.Errorf("%w; automatic rollback incomplete: %v", cause, err)
	}
	return fmt.Errorf("%w; previous application image restored and healthy", cause)
}
func (u *Updater) stageUpdater(ctx context.Context, jobID, image string) (string, error) {
	id, err := u.runOutput(ctx, "docker", "create", "--entrypoint", "/bin/true", image)
	if err != nil || id == "" {
		return "", fmt.Errorf("cannot create release extraction container: %v", err)
	}
	defer func() {
		clean, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_, _ = u.runOutput(clean, "docker", "rm", "-f", id)
	}()
	path := filepath.Join(u.cfg.StateDir, ".sub2api-updater-"+jobID)
	_ = os.Remove(path)
	if _, err = u.runOutput(ctx, "docker", "cp", id+":"+updatepolicy.UpdaterPath, path); err != nil {
		_ = os.Remove(path)
		return "", err
	}
	if err = os.Chmod(path, 0755); err != nil {
		_ = os.Remove(path)
		return "", err
	}
	// Execute the harmless flag parser before installing the host binary, catching
	// a wrong architecture or missing dynamic loader before touching the app.
	if _, err = u.runOutput(ctx, path, "--help"); err != nil {
		_ = os.Remove(path)
		return "", fmt.Errorf("release updater is not executable: %w", err)
	}
	return path, nil
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
	defer func() { _ = os.Remove(tempPath) }()
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

func (u *Updater) waitHealthy(ctx context.Context, imageID string) error {
	healthCtx, cancel := context.WithTimeout(ctx, u.cfg.HealthTimeout)
	defer cancel()
	for {
		// Never fall back to a legacy container here: only the Compose service that
		// was just recreated can satisfy the update's health check.
		target, err := u.runOutput(healthCtx, "docker", append(u.composeArgs(), "ps", "-q", u.cfg.AppService)...)
		if err == nil && target != "" && !strings.Contains(target, "\n") {
			state, inspectErr := u.runOutput(healthCtx, "docker", "inspect", "--format", "{{.Image}}|{{if .State.Health}}{{.State.Health.Status}}{{else}}missing{{end}}", target)
			if inspectErr == nil && state == imageID+"|healthy" {
				return nil
			}
		}
		select {
		case <-healthCtx.Done():
			return errors.New("application did not become healthy with the expected image before timeout")
		case <-time.After(2 * time.Second):
		}
	}
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
		if file != u.overlayPath() {
			args = append(args, "-f", file)
		}
	}
	if _, err := os.Stat(u.overlayPath()); err == nil {
		args = append(args, "-f", u.overlayPath())
	}
	return args
}

func (u *Updater) runOutput(ctx context.Context, name string, args ...string) (string, error) {
	dir := filepath.Join(u.cfg.RepoDir, "deploy")
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
	defer func() { _ = os.Remove(tempPath) }()
	if err := temp.Chmod(0o600); err != nil {
		_ = temp.Close()
		return err
	}
	if _, err := temp.Write(data); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
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
			if job.Status == "queued" || job.Status == "running" || job.Status == "recovering" {
				if job.PreviousImage != "" && (job.Stage == "deploying" || job.Stage == "health_check" || job.Stage == "rolling_back" || job.Status == "recovering") {
					job.Status = "recovering"
				} else {
					job.Status = "failed"
					job.Stage = "failed"
					job.Error = "updater restarted before update completion; check application status"
					job.FinishedAt = time.Now().UTC().Format(time.RFC3339)
				}
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

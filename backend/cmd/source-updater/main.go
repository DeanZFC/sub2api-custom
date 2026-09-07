package main

import (
	"context"
	"flag"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/sourceupdater"
)

func main() {
	var (
		socketPath     = flag.String("socket-path", "", "Unix socket path")
		tokenFile      = flag.String("token-file", "", "authentication token file")
		repoDir        = flag.String("repo-dir", "", "deployed Git repository")
		stateDir       = flag.String("state-dir", "", "persistent job state directory")
		updaterBinary  = flag.String("updater-binary", "/usr/local/libexec/sub2api-updater", "host updater binary path")
		composeProject = flag.String("compose-project", "", "fixed Docker Compose project")
		composeFiles   = flag.String("compose-files", "", "comma-separated absolute Compose files")
		appService     = flag.String("app-service", "sub2api", "fixed application service")
		// Retained for compatibility with existing systemd service units. The
		// source updater no longer runs a PostgreSQL backup during updates.
		postgresService = flag.String("postgres-service", "postgres", "legacy PostgreSQL service (unused)")
		appContainer    = flag.String("app-container", "", "fixed application container")
	)
	flag.Parse()

	token, err := os.ReadFile(strings.TrimSpace(*tokenFile))
	if err != nil {
		log.Fatalf("read updater token: %v", err)
	}
	cfg := sourceupdater.Config{
		Token:           strings.TrimSpace(string(token)),
		RepoDir:         *repoDir,
		StateDir:        *stateDir,
		UpdaterBinary:   *updaterBinary,
		ComposeProject:  *composeProject,
		ComposeFiles:    splitNonEmpty(*composeFiles),
		AppService:      *appService,
		PostgresService: *postgresService,
		AppContainer:    *appContainer,
		HealthTimeout:   3 * time.Minute,
	}
	updater, err := sourceupdater.New(cfg)
	if err != nil {
		log.Fatalf("configure updater: %v", err)
	}

	path := filepath.Clean(strings.TrimSpace(*socketPath))
	if !filepath.IsAbs(path) {
		log.Fatal("socket path must be absolute")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		log.Fatalf("create socket directory: %v", err)
	}
	if info, statErr := os.Lstat(path); statErr == nil {
		if info.Mode()&os.ModeSocket == 0 {
			log.Fatalf("refusing to replace non-socket path %s", path)
		}
		if err := os.Remove(path); err != nil {
			log.Fatalf("remove stale socket: %v", err)
		}
	} else if !os.IsNotExist(statErr) {
		log.Fatalf("inspect socket path: %v", statErr)
	}
	listener, err := net.Listen("unix", path)
	if err != nil {
		log.Fatalf("listen on updater socket: %v", err)
	}
	defer listener.Close()
	defer os.Remove(path)
	if err := os.Chmod(path, 0o666); err != nil {
		log.Fatalf("set updater socket permissions: %v", err)
	}

	server := &http.Server{
		Handler:           updater.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       30 * time.Second,
	}
	updater.SetSuccessfulUpdateHook(func() {
		// The service unit has Restart=always. Shutting down cleanly after the
		// job is persisted makes systemd load the atomically replaced binary.
		go func() {
			time.Sleep(100 * time.Millisecond)
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			_ = server.Shutdown(shutdownCtx)
		}()
	})
	log.Printf("sub2api source updater listening on %s", path)
	if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
		log.Fatalf("serve updater API: %v", err)
	}
}

func splitNonEmpty(value string) []string {
	parts := strings.Split(value, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		if item := strings.TrimSpace(part); item != "" {
			result = append(result, item)
		}
	}
	return result
}

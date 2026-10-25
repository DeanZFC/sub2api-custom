// vm2api-bridge is deployed beside vm2api, not inside the Sub2API process.
// Only the vm2api control plane needs runtime/Docker permissions.
package main

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/vm2api"
	"github.com/Wei-Shaw/sub2api/internal/pkg/vmbridge"
)

type settings struct {
	listen, secret, stateDir, namespace, vm2apiURL, masterKey string
	tlsCertFile, tlsKeyFile                                   string
	drainTimeout, dialTimeout, managementTimeout              time.Duration
}

func readSettings(getenv func(string) string) (settings, error) {
	cfg := settings{listen: getenv("VM2API_BRIDGE_LISTEN"), secret: getenv("VM2API_BRIDGE_SECRET"), stateDir: getenv("VM2API_BRIDGE_STATE_DIR"), namespace: getenv("VM2API_BRIDGE_NAMESPACE"), vm2apiURL: getenv("VM2API_URL"), masterKey: getenv("VM2API_MASTER_KEY")}
	cfg.tlsCertFile, cfg.tlsKeyFile = getenv("VM2API_BRIDGE_TLS_CERT"), getenv("VM2API_BRIDGE_TLS_KEY")
	if (cfg.tlsCertFile == "") != (cfg.tlsKeyFile == "") {
		return settings{}, errors.New("VM2API_BRIDGE_TLS_CERT and VM2API_BRIDGE_TLS_KEY must be set together")
	}
	if cfg.listen == "" {
		cfg.listen = "127.0.0.1:8097"
	}
	_, port, err := net.SplitHostPort(cfg.listen)
	if err != nil {
		return settings{}, errors.New("VM2API_BRIDGE_LISTEN must be host:port")
	}
	number, err := strconv.Atoi(port)
	if err != nil || number < 1 || number > 65535 {
		return settings{}, errors.New("VM2API_BRIDGE_LISTEN has an invalid port")
	}
	if len(cfg.secret) < 32 || strings.TrimSpace(cfg.secret) != cfg.secret || strings.ContainsAny(cfg.secret, "\r\n") {
		return settings{}, errors.New("VM2API_BRIDGE_SECRET must contain at least 32 bytes without surrounding whitespace")
	}
	if strings.TrimSpace(cfg.vm2apiURL) == "" || strings.TrimSpace(cfg.masterKey) == "" {
		return settings{}, errors.New("VM2API_URL and VM2API_MASTER_KEY are required")
	}
	if cfg.stateDir == "" {
		cfg.stateDir = "./vm2api-bridge-state"
	}
	if cfg.namespace == "" {
		// Stable for this installation; never log the secret or persist a copy.
		sum := sha256.Sum256([]byte("vm2api-bridge-namespace-v1\x00" + cfg.secret))
		cfg.namespace = "sub2api-" + hex.EncodeToString(sum[:12])
	} else if strings.TrimSpace(cfg.namespace) != cfg.namespace || len(cfg.namespace) > 128 || strings.ContainsAny(cfg.namespace, "\r\n") {
		return settings{}, errors.New("VM2API_BRIDGE_NAMESPACE must be a stable nonblank installation identifier")
	}
	for _, field := range []struct {
		name     string
		target   *time.Duration
		fallback time.Duration
	}{
		{"VM2API_BRIDGE_DRAIN_TIMEOUT", &cfg.drainTimeout, 30 * time.Second},
		{"VM2API_BRIDGE_DIAL_TIMEOUT", &cfg.dialTimeout, 15 * time.Second},
		{"VM2API_BRIDGE_MANAGEMENT_TIMEOUT", &cfg.managementTimeout, 90 * time.Second},
	} {
		*field.target = field.fallback
		if raw := getenv(field.name); raw != "" {
			value, parseErr := time.ParseDuration(raw)
			if parseErr != nil || value <= 0 {
				return settings{}, fmt.Errorf("%s must be a positive duration", field.name)
			}
			*field.target = value
		}
	}
	return cfg, nil
}

func run(ctx context.Context, getenv func(string) string) error {
	cfg, err := readSettings(getenv)
	if err != nil {
		return err
	}
	unlock, err := lockStateDirectory(cfg.stateDir)
	if err != nil {
		return err
	}
	defer unlock()
	transport := &http.Transport{
		Proxy:               nil, // Ignore ambient proxy variables for the trusted control plane.
		DialContext:         (&net.Dialer{Timeout: cfg.dialTimeout, KeepAlive: 30 * time.Second}).DialContext,
		TLSClientConfig:     &tls.Config{MinVersion: tls.VersionTLS12},
		TLSHandshakeTimeout: 10 * time.Second, ResponseHeaderTimeout: 2 * time.Minute,
		MaxIdleConns: 200, MaxIdleConnsPerHost: 50, IdleConnTimeout: 90 * time.Second,
	}
	defer transport.CloseIdleConnections()
	client, err := vm2api.NewClient(vm2api.Config{BaseURL: cfg.vm2apiURL, MasterKey: cfg.masterKey, HTTPClient: &http.Client{Transport: transport}, ManagementTimeout: cfg.managementTimeout})
	if err != nil {
		return errors.New("invalid vm2api client configuration")
	}
	provider, err := vm2api.NewProvider(client, cfg.namespace)
	if err != nil {
		return errors.New("invalid vm2api provider configuration")
	}
	manager, err := vmbridge.New(vmbridge.Config{Secret: cfg.secret, StateDir: cfg.stateDir, DrainTimeout: cfg.drainTimeout, DialTimeout: cfg.dialTimeout}, provider)
	if err != nil {
		return err
	}
	listener, err := net.Listen("tcp", cfg.listen)
	if err != nil {
		return errors.New("cannot listen on VM2API_BRIDGE_LISTEN")
	}
	defer func() { _ = listener.Close() }()
	server := &http.Server{Handler: manager.Handler(), ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 90 * time.Second, MaxHeaderBytes: 64 << 10, BaseContext: func(net.Listener) context.Context { return ctx },
		TLSConfig: &tls.Config{MinVersion: tls.VersionTLS12, NextProtos: []string{"http/1.1"}},
		// HTTP/1.1 is needed for opaque CONNECT hijacking on this shared listener.
		TLSNextProto: make(map[string]func(*http.Server, *tls.Conn, http.Handler)),
	}
	finished := make(chan error, 1)
	go func() {
		if cfg.tlsCertFile != "" {
			finished <- server.ServeTLS(listener, cfg.tlsCertFile, cfg.tlsKeyFile)
		} else {
			finished <- server.Serve(listener)
		}
	}()
	log.Printf("vm2api bridge listening on %s", cfg.listen)
	select {
	case err = <-finished:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return errors.New("bridge HTTP server failed")
	case <-ctx.Done():
		// BaseContext cancellation also closes hijacked CONNECT tunnels, which
		// net/http Shutdown does not otherwise track. Persistent slots survive.
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err = server.Shutdown(shutdownCtx); err != nil {
			_ = server.Close()
			return errors.New("bridge shutdown timed out")
		}
		<-finished
		return nil
	}
}

func main() {
	log.SetFlags(log.Ldate | log.Ltime | log.LUTC)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Getenv); err != nil {
		log.Printf("vm2api bridge stopped: %s", err)
		os.Exit(1)
	}
}

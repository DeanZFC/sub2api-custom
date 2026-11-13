package main

import (
	"context"
	"flag"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/ipv6egress"
)

var Version = "dev"

func main() {
	configPath := flag.String("config", "/etc/sub2api-ovh-ipv6-egress/config.json", "private JSON configuration")
	initialize := flag.Bool("initialize", false, "initialize a NEW allocation store, then exit; never resets existing state")
	version := flag.Bool("version", false, "print version")
	flag.Parse()
	if *version {
		log.Print(Version)
		return
	}
	cfg, err := ipv6egress.ReadConfig(*configPath)
	if err != nil {
		log.Fatal(err)
	}
	a, err := ipv6egress.Open(cfg, *initialize, ipv6egress.Dependencies{})
	if err != nil {
		log.Fatal(err)
	}
	defer func() { _ = a.Close() }()
	if *initialize {
		log.Print("new IPv6 allocation store initialized")
		return
	}
	// Bind listeners before restoring addresses. Failure cannot leave a second
	// agent managing the same host namespace.
	proxyListener, err := net.Listen("tcp", cfg.ProxyListen)
	if err != nil {
		log.Fatal(err)
	}
	defer func() { _ = proxyListener.Close() }()
	controlListener, err := net.Listen("tcp", cfg.ControlListen)
	if err != nil {
		log.Fatal(err)
	}
	defer func() { _ = controlListener.Close() }()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	a.Reconcile(ctx)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		tick := time.NewTicker(2 * time.Second)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				a.Reconcile(ctx)
			}
		}
	}()
	proxy := &http.Server{Handler: a.ProxyHandler(), ReadHeaderTimeout: 10 * time.Second, MaxHeaderBytes: 32 << 10, IdleTimeout: 90 * time.Second}
	control := &http.Server{Handler: a.ControlHandler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 30 * time.Second, MaxHeaderBytes: 8 << 10, IdleTimeout: 30 * time.Second}
	failures := make(chan error, 2)
	go func() { failures <- proxy.Serve(proxyListener) }()
	go func() { failures <- control.Serve(controlListener) }()
	log.Printf("IPv6 egress %s ready", Version)
	select {
	case <-ctx.Done():
	case err := <-failures:
		if err != nil && err != http.ErrServerClosed {
			log.Print("listener failed")
		}
		stop()
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_ = control.Shutdown(shutdown)
	_ = proxy.Shutdown(shutdown)
	wg.Wait()
	_ = a.WaitDrained(context.Background())
}

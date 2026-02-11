package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/docker/docker/client"

	"openclaw-orchestrator/internal/config"
	"openclaw-orchestrator/internal/container"
	"openclaw-orchestrator/internal/policy"
	"openclaw-orchestrator/internal/proxy"
)

func main() {
	configPath := flag.String("config", "./orchestrator.yaml", "path to config file")
	flag.Parse()

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	cfg, err := config.Load(*configPath)
	if err != nil {
		logger.Error("failed to load config", "error", err)
		os.Exit(1)
	}
	logger.Info("config loaded", "agents", len(cfg.Agents), "listen", cfg.Listen)

	// Docker client.
	docker, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		logger.Error("failed to create docker client", "error", err)
		os.Exit(1)
	}
	defer docker.Close()

	// Discover existing containers.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	discovered, err := container.Discover(ctx, docker, logger)
	if err != nil {
		logger.Warn("container discovery failed (continuing without)", "error", err)
	} else {
		logger.Info("container discovery complete", "found", len(discovered))
	}

	mgr := container.NewManager(docker, logger)

	// Build proxy and policies.
	p := proxy.New(logger)
	var policies []policy.Policy

	for name, agent := range cfg.Agents {
		target, err := url.Parse(agent.Backend)
		if err != nil {
			logger.Error("invalid backend URL", "agent", name, "error", err)
			os.Exit(1)
		}

		var pol policy.Policy
		switch agent.Policy {
		case "always-on":
			pol = policy.NewAlwaysOn(mgr, policy.AlwaysOnConfig{
				Agent:              name,
				ContainerName:      agent.Container.Name,
				HealthURL:          agent.Health.URL,
				CheckInterval:      agent.Health.CheckInterval,
				MaxFailures:        agent.Health.MaxFailures,
				MaxRestartAttempts: agent.Health.MaxRestartAttempts,
			}, logger)
		case "unmanaged":
			pol = policy.NewUnmanaged()
		default:
			logger.Error("unknown policy", "agent", name, "policy", agent.Policy)
			os.Exit(1)
		}

		p.Register(agent.Hostname, name, target, pol)
		policies = append(policies, pol)
		logger.Info("agent configured", "name", name, "hostname", agent.Hostname, "policy", agent.Policy)
	}

	// Start policy goroutines.
	for _, pol := range policies {
		go pol.Start(ctx)
	}

	// HTTP server.
	srv := &http.Server{
		Addr:         cfg.Listen,
		Handler:      p,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 0, // no timeout for streaming/SSE/WS
		IdleTimeout:  120 * time.Second,
	}

	// Start server in goroutine.
	go func() {
		logger.Info("server starting", "addr", cfg.Listen)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("server failed", "error", err)
			os.Exit(1)
		}
	}()

	// Wait for shutdown signal.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	sig := <-sigCh

	logger.Info("shutting down", "signal", sig)
	cancel() // stop policy goroutines

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer shutdownCancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Error("shutdown error", "error", err)
	}

	fmt.Println("orchestrator stopped")
}

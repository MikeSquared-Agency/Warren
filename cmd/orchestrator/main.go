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

	"warren/internal/admin"
	"warren/internal/alerts"
	"warren/internal/config"
	"warren/internal/container"
	"warren/internal/events"
	"warren/internal/metrics"
	"warren/internal/policy"
	"warren/internal/proxy"
	"warren/internal/services"
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

	serviceMgr := container.NewManager(docker, logger)
	emitter := events.NewEmitter(logger)

	// Build proxy and policies.
	registry := services.NewRegistry(logger)

	// Wire event-driven service cleanup: purge dynamic routes when agents sleep.
	emitter.OnEvent(func(ev events.Event) {
		if ev.Type == events.AgentSleep {
			registry.DeregisterByAgent(ev.Agent)
		}
	})
	p := proxy.New(registry, logger)
	var policies []policy.Policy
	policyByName := make(map[string]policy.Policy)

	// Build a map of discovered container states for startup reconciliation.
	discoveredState := make(map[string]string) // container name → state
	for _, dc := range discovered {
		discoveredState[dc.Name] = dc.State
	}

	for name, agent := range cfg.Agents {
		target, err := url.Parse(agent.Backend)
		if err != nil {
			logger.Error("invalid backend URL", "agent", name, "error", err)
			os.Exit(1)
		}

		var pol policy.Policy
		switch agent.Policy {
		case "always-on":
			pol = policy.NewAlwaysOn(policy.AlwaysOnConfig{
				Agent:         name,
				HealthURL:     agent.Health.URL,
				CheckInterval: agent.Health.CheckInterval,
				MaxFailures:   agent.Health.MaxFailures,
			}, emitter, logger)
		case "on-demand":
			pol = policy.NewOnDemand(serviceMgr, policy.OnDemandConfig{
				Agent:              name,
				ContainerName:      agent.Container.Name,
				HealthURL:          agent.Health.URL,
				Hostname:           agent.Hostname,
				CheckInterval:      agent.Health.CheckInterval,
				StartupTimeout:     agent.Health.StartupTimeout,
				IdleTimeout:        agent.Idle.Timeout,
				MaxFailures:        agent.Health.MaxFailures,
				MaxRestartAttempts: agent.Health.MaxRestartAttempts,
			}, p.Activity(), p.WSCounter(), emitter, logger)

			// Startup reconciliation: inform policy if container is already running.
			if state, ok := discoveredState[agent.Container.Name]; ok {
				pol.(*policy.OnDemand).SetInitialState(state == "running")
			}
		case "unmanaged":
			pol = policy.NewUnmanaged()
		default:
			logger.Error("unknown policy", "agent", name, "policy", agent.Policy)
			os.Exit(1)
		}

		// Register primary hostname and any additional hostnames.
		p.Register(agent.Hostname, name, target, pol)
		for _, h := range agent.Hostnames {
			p.Register(h, name, target, pol)
		}
		policies = append(policies, pol)
		policyByName[name] = pol
		logger.Info("agent configured", "name", name, "hostname", agent.Hostname, "extra_hostnames", len(agent.Hostnames), "policy", agent.Policy)
	}

	// Wire metrics into event system.
	metrics.RegisterEventHandler(emitter)

	// Wire webhook alerting.
	if len(cfg.Webhooks) > 0 {
		alerter := alerts.NewWebhookAlerter(cfg.Webhooks, logger)
		alerter.RegisterEventHandler(emitter)
		logger.Info("webhook alerting configured", "webhooks", len(cfg.Webhooks))
	}

	// Wire LRU eviction.
	lruMgr := policy.NewLRUManager(p.Activity(), logger)
	for name, pol := range policyByName {
		if od, ok := pol.(*policy.OnDemand); ok {
			agent := cfg.Agents[name]
			lruMgr.Register(name, od, agent.Hostname)
		}
	}
	if cfg.MaxReadyAgents > 0 {
		emitter.OnEvent(func(ev events.Event) {
			if ev.Type == events.AgentReady {
				lruMgr.EvictIfNeeded(ctx, cfg.MaxReadyAgents)
			}
		})
		logger.Info("LRU eviction enabled", "max_ready_agents", cfg.MaxReadyAgents)
	}

	// Start Docker event watcher.
	watcher := container.NewWatcher(docker, func(serviceID, serviceName, action string) {
		emitter.Emit(events.Event{
			Type:  "docker." + action,
			Agent: serviceName,
			Fields: map[string]string{
				"service_id": serviceID,
				"action":     action,
			},
		})
	}, logger)
	go watcher.Watch(ctx)

	// Start policy goroutines.
	for _, pol := range policies {
		go pol.Start(ctx)
	}

	// Admin server (separate port).
	if cfg.AdminListen != "" {
		agentInfos := make(map[string]admin.AgentInfo)
		for name, agent := range cfg.Agents {
			agentInfos[name] = admin.AgentInfo{
				Name:     name,
				Hostname: agent.Hostname,
				Policy:   agent.Policy,
				Backend:  agent.Backend,
			}
		}
		adminSrv := admin.NewServer(agentInfos, policyByName, registry, emitter, serviceMgr, p.WSCounter().Total, logger)

		// Mount metrics on admin handler.
		adminMux := http.NewServeMux()
		adminMux.Handle("/metrics", metrics.Handler())
		adminMux.Handle("/api/services", http.HandlerFunc(p.HandleServiceAPI))
		adminMux.HandleFunc("/api/services/", p.HandleServiceAPI)
		adminMux.Handle("/", adminSrv.Handler())

		go func() {
			srv := &http.Server{Addr: cfg.AdminListen, Handler: adminMux}
			go func() {
				<-ctx.Done()
				shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				srv.Shutdown(shutCtx)
			}()
			logger.Info("admin server starting", "addr", cfg.AdminListen)
			if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				logger.Error("admin server failed", "error", err)
			}
		}()
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

	// Wait for shutdown signal or SIGHUP for reload.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)

	var sig os.Signal
	for {
		sig = <-sigCh
		if sig != syscall.SIGHUP {
			break
		}
		logger.Info("SIGHUP received, reloading config")
		newCfg, err := config.Load(*configPath)
		if err != nil {
			logger.Error("failed to reload config", "error", err)
			continue
		}
		reloadConfig(logger, cfg, newCfg, policyByName)
	}

	activeWS := p.WSCounter().Total()
	logger.Info("shutting down", "signal", sig, "active_websockets", activeWS)
	cancel() // stop policy goroutines

	// Calculate drain timeout: use the max drain_timeout across all agents.
	drainTimeout := 30 * time.Second
	for _, agent := range cfg.Agents {
		if agent.Idle.DrainTimeout > drainTimeout {
			drainTimeout = agent.Idle.DrainTimeout
		}
	}

	// Wait for WebSocket connections to drain naturally.
	if activeWS > 0 {
		logger.Info("waiting for WebSocket connections to drain", "timeout", drainTimeout, "active", activeWS)
		if p.WSCounter().Wait(drainTimeout) {
			logger.Info("all WebSocket connections drained")
		} else {
			logger.Warn("drain timeout reached, forcing shutdown", "remaining_websockets", p.WSCounter().Total())
		}
	}

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer shutdownCancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Error("shutdown error", "error", err)
	}

	fmt.Println("orchestrator stopped")
}

func reloadConfig(logger *slog.Logger, old, new_ *config.Config, policyByName map[string]policy.Policy) {
	// Warn about structural changes that require restart.
	for name := range new_.Agents {
		if _, ok := old.Agents[name]; !ok {
			logger.Warn("config reload: new agent requires restart to take effect", "agent", name)
		}
	}
	for name := range old.Agents {
		if _, ok := new_.Agents[name]; !ok {
			logger.Warn("config reload: removed agent requires restart to take effect", "agent", name)
		}
	}
	for name, oldAgent := range old.Agents {
		newAgent, ok := new_.Agents[name]
		if !ok {
			continue
		}
		if oldAgent.Hostname != newAgent.Hostname {
			logger.Warn("config reload: hostname change requires restart", "agent", name, "old", oldAgent.Hostname, "new", newAgent.Hostname)
		}
		if oldAgent.Backend != newAgent.Backend {
			logger.Warn("config reload: backend change requires restart", "agent", name)
		}
	}

	// Apply runtime-safe changes.
	for name, pol := range policyByName {
		newAgent, ok := new_.Agents[name]
		if !ok {
			continue
		}
		switch p := pol.(type) {
		case *policy.OnDemand:
			p.Reconfigure(newAgent.Idle.Timeout, newAgent.Health.CheckInterval, newAgent.Health.MaxFailures, newAgent.Health.MaxRestartAttempts)
		case *policy.AlwaysOn:
			p.Reconfigure(newAgent.Health.CheckInterval, newAgent.Health.MaxFailures)
		}
	}
	logger.Info("config reload complete")
}

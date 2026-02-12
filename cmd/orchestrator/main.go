package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/docker/docker/client"

	"warren/internal/admin"
	"warren/internal/alexandria"
	"warren/internal/alerts"
	"warren/internal/config"
	"warren/internal/container"
	"warren/internal/events"
	"warren/internal/hermes"
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

	// Connect to Hermes (NATS) if enabled.
	var hermesClient *hermes.Client
	if cfg.Hermes.Enabled {
		hermesClient, err = hermes.Connect(hermes.Config{
			URL:            cfg.Hermes.URL,
			Token:          cfg.Hermes.Token,
			ConnectTimeout: cfg.Hermes.ConnectTimeout,
			ReconnectWait:  cfg.Hermes.ReconnectWait,
			MaxReconnects:  cfg.Hermes.MaxReconnects,
		}, "warren-orchestrator", logger)
		if err != nil {
			logger.Error("failed to connect to hermes", "error", err)
			os.Exit(1)
		}
		defer hermesClient.Close()

		// Provision JetStream streams.
		if err := hermesClient.ProvisionStreams(ctx); err != nil {
			logger.Error("failed to provision hermes streams", "error", err)
			os.Exit(1)
		}
		logger.Info("hermes connected and streams provisioned", "url", cfg.Hermes.URL)

		// Bridge Warren events to Hermes.
		emitter.OnEvent(func(ev events.Event) {
			var subject, eventType string
			var data any

			switch ev.Type {
			case events.AgentWake, events.AgentStarting:
				subject = hermes.AgentSubject(hermes.SubjectAgentStarted, ev.Agent)
				eventType = "agent.started"
				data = hermes.AgentLifecycleData{Agent: ev.Agent, Reason: ev.Fields["reason"]}
			case events.AgentSleep:
				subject = hermes.AgentSubject(hermes.SubjectAgentStopped, ev.Agent)
				eventType = "agent.stopped"
				data = hermes.AgentLifecycleData{Agent: ev.Agent, Reason: ev.Fields["reason"]}
			case events.AgentReady:
				subject = hermes.AgentSubject(hermes.SubjectAgentReady, ev.Agent)
				eventType = "agent.ready"
				data = hermes.AgentLifecycleData{Agent: ev.Agent}
			case events.AgentDegraded:
				subject = hermes.AgentSubject(hermes.SubjectAgentDegraded, ev.Agent)
				eventType = "agent.degraded"
				data = hermes.AgentLifecycleData{Agent: ev.Agent, Reason: ev.Fields["reason"]}
			default:
				return // don't bridge unknown events
			}

			if err := hermesClient.PublishEvent(subject, eventType, data); err != nil {
				logger.Error("hermes publish failed", "subject", subject, "error", err)
			}
		})
	}

	// Alexandria briefing client.
	var alexClient *alexandria.Client
	if cfg.Alexandria.Enabled {
		alexClient = alexandria.NewClient(alexandria.Config{
			Enabled: cfg.Alexandria.Enabled,
			URL:     cfg.Alexandria.URL,
			Timeout: cfg.Alexandria.Timeout,
		}, logger)
		logger.Info("alexandria client configured", "url", cfg.Alexandria.URL)
	}

	// Build proxy and policies.
	registry := services.NewRegistry(logger)

	// Wire event-driven service cleanup: purge dynamic routes when agents sleep.
	emitter.OnEvent(func(ev events.Event) {
		if ev.Type == events.AgentSleep {
			registry.DeregisterByAgent(ev.Agent)
		}
	})
	p := proxy.New(registry, logger)
	policyByName := make(map[string]policy.Policy)
	policyCancels := make(map[string]context.CancelFunc)

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

		pol, polCancel := createPolicy(name, agent, serviceMgr, p, emitter, discoveredState, logger)

		// Register primary hostname and any additional hostnames.
		p.Register(agent.Hostname, name, target, pol)
		for _, h := range agent.Hostnames {
			p.Register(h, name, target, pol)
		}
		// Wire Alexandria briefing hook for on-demand agents.
		if od, ok := pol.(*policy.OnDemand); ok && alexClient != nil {
			agentName := name
			od.OnReady = func(ctx context.Context, agentID string, lastSleepTime time.Time) {
				briefing, err := alexClient.GetBriefing(ctx, agentID, lastSleepTime, 50)
				if err != nil {
					logger.Error("failed to get briefing", "agent", agentID, "error", err)
					return
				}
				if briefing == nil {
					logger.Info("no briefing available", "agent", agentID)
					return
				}

				// Write briefing to file.
				dir := "/tmp/warren-briefings"
				if err := os.MkdirAll(dir, 0755); err != nil {
					logger.Error("failed to create briefing dir", "error", err)
					return
				}
				data, _ := json.Marshal(briefing)
				path := filepath.Join(dir, agentID+".json")
				if err := os.WriteFile(path, data, 0644); err != nil {
					logger.Error("failed to write briefing", "agent", agentID, "error", err)
					return
				}
				logger.Info("briefing written", "agent", agentID, "path", path, "items", briefing.ItemCount)

				// Publish briefed event on Hermes.
				if hermesClient != nil {
					subject := hermes.AgentSubject(hermes.SubjectAgentBriefed, agentName)
					if err := hermesClient.PublishEvent(subject, "agent.briefed", hermes.AgentBriefedData{
						Agent:     agentID,
						ItemCount: briefing.ItemCount,
						Summary:   briefing.Summary,
					}); err != nil {
						logger.Error("failed to publish briefed event", "agent", agentID, "error", err)
					}
				}
			}
		}

		policyByName[name] = pol
		policyCancels[name] = polCancel
		logger.Info("agent configured", "name", name, "hostname", agent.Hostname, "extra_hostnames", len(agent.Hostnames), "policy", agent.Policy)
	}

	// Wire metrics into event system.
	metrics.RegisterEventHandler(emitter)

	// Wire webhook alerting.
	if len(cfg.Webhooks) > 0 {
		alerter := alerts.NewWebhookAlerter(cfg.Webhooks, logger)
		alerter.Start(ctx)
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
	for _, pol := range policyByName {
		go pol.Start(ctx)
	}

	// Admin server (separate port).
	var adminSrv *admin.Server
	if cfg.AdminListen != "" {
		agentInfos := make(map[string]admin.AgentInfo)
		for name, agent := range cfg.Agents {
			agentInfos[name] = admin.AgentInfo{
				Name:          name,
				Hostname:      agent.Hostname,
				Policy:        agent.Policy,
				Backend:       agent.Backend,
				ContainerName: agent.Container.Name,
				HealthURL:     agent.Health.URL,
				IdleTimeout:   agent.Idle.Timeout.String(),
			}
		}
		adminSrv = admin.NewServer(agentInfos, policyByName, policyCancels, registry, emitter, serviceMgr, p, cfg, *configPath, p.WSCounter().Total, logger)

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
		// WriteTimeout is intentionally 0 to support SSE, WebSocket, and streaming
		// responses. Per-request timeouts are enforced at the handler level.
		// A slow client can hold a goroutine indefinitely, but this is acceptable
		// given deployment behind Cloudflare Tunnel which enforces its own timeouts.
		WriteTimeout: 0,
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
		reloadConfig(ctx, logger, cfg, newCfg, policyByName, policyCancels, p, serviceMgr, emitter, adminSrv, discoveredState)
		cfg = newCfg
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

func createPolicy(name string, agent *config.Agent, serviceMgr *container.Manager, p *proxy.Proxy, emitter *events.Emitter, discoveredState map[string]string, logger *slog.Logger) (policy.Policy, context.CancelFunc) {
	policyCtx, policyCancel := context.WithCancel(context.Background())

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
			WakeCooldown:       agent.Idle.WakeCooldown,
			MaxFailures:        agent.Health.MaxFailures,
			MaxRestartAttempts: agent.Health.MaxRestartAttempts,
		}, p.Activity(), p.WSCounter(), emitter, logger)

		// Startup reconciliation: inform policy if container is already running.
		if state, ok := discoveredState[agent.Container.Name]; ok {
			pol.(*policy.OnDemand).SetInitialState(state == "running")
		}
	case "unmanaged":
		pol = policy.NewUnmanaged()
	}

	// The caller is responsible for starting the goroutine with policyCtx.
	// We wrap Start to use the policy-specific context.
	wrapper := &policyWrapper{inner: pol, ctx: policyCtx}
	_ = wrapper // not used directly; we return the raw policy and cancel

	return pol, policyCancel
}

// policyWrapper is unused but reserved for future use.
type policyWrapper struct {
	inner policy.Policy
	ctx   context.Context
}

func reloadConfig(ctx context.Context, logger *slog.Logger, old, new_ *config.Config, policyByName map[string]policy.Policy, policyCancels map[string]context.CancelFunc, p *proxy.Proxy, serviceMgr *container.Manager, emitter *events.Emitter, adminSrv *admin.Server, discoveredState map[string]string) {
	// Add new agents.
	for name, agent := range new_.Agents {
		if _, ok := old.Agents[name]; ok {
			continue // existing agent — handle reconfigure below
		}

		logger.Info("config reload: adding new agent", "agent", name)
		target, err := url.Parse(agent.Backend)
		if err != nil {
			logger.Error("config reload: invalid backend URL for new agent", "agent", name, "error", err)
			continue
		}

		pol, polCancel := createPolicy(name, agent, serviceMgr, p, emitter, discoveredState, logger)

		p.Register(agent.Hostname, name, target, pol)
		for _, h := range agent.Hostnames {
			p.Register(h, name, target, pol)
		}

		policyByName[name] = pol
		policyCancels[name] = polCancel

		// Start policy goroutine.
		go pol.Start(ctx)

		if adminSrv != nil {
			adminSrv.AddAgent(name, admin.AgentInfo{
				Name:          name,
				Hostname:      agent.Hostname,
				Policy:        agent.Policy,
				Backend:       agent.Backend,
				ContainerName: agent.Container.Name,
				HealthURL:     agent.Health.URL,
				IdleTimeout:   agent.Idle.Timeout.String(),
			}, pol, polCancel)
		}

		emitter.Emit(events.Event{Type: events.AgentAdded, Agent: name})
		logger.Info("config reload: agent added", "agent", name, "hostname", agent.Hostname)
	}

	// Remove deleted agents.
	for name, agent := range old.Agents {
		if _, ok := new_.Agents[name]; ok {
			continue // still exists
		}

		logger.Info("config reload: removing agent", "agent", name)

		// Cancel policy goroutine.
		if cancel, ok := policyCancels[name]; ok {
			cancel()
			delete(policyCancels, name)
		}

		// Deregister from proxy.
		p.Deregister(agent.Hostname)
		for _, h := range agent.Hostnames {
			p.Deregister(h)
		}

		delete(policyByName, name)

		if adminSrv != nil {
			adminSrv.RemoveAgentInternal(name)
		}

		emitter.Emit(events.Event{Type: events.AgentRemoved, Agent: name})
		logger.Info("config reload: agent removed", "agent", name)
	}

	// Reconfigure existing agents.
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

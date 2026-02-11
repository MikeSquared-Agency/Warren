package metrics

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"warren/internal/events"
)

var (
	AgentState = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "warren_agent_state",
		Help: "1 if the agent is in the given state",
	}, []string{"agent", "state"})

	AgentRequestsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "warren_agent_requests_total",
		Help: "Total requests per agent",
	}, []string{"agent"})

	AgentHealthChecksTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "warren_agent_health_checks_total",
		Help: "Health check results per agent",
	}, []string{"agent", "result"})

	WSConnectionsActive = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "warren_ws_connections_active",
		Help: "Active WebSocket connections per agent",
	}, []string{"agent"})

	ServiceRegistrations = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "warren_service_registrations",
		Help: "Number of dynamic service registrations",
	})

	AgentWakeTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "warren_agent_wake_total",
		Help: "Wake events per agent",
	}, []string{"agent"})

	AgentSleepTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "warren_agent_sleep_total",
		Help: "Sleep events per agent",
	}, []string{"agent"})
)

func init() {
	prometheus.MustRegister(
		AgentState,
		AgentRequestsTotal,
		AgentHealthChecksTotal,
		WSConnectionsActive,
		ServiceRegistrations,
		AgentWakeTotal,
		AgentSleepTotal,
	)
}

// Handler returns the Prometheus metrics HTTP handler.
func Handler() http.Handler {
	return promhttp.Handler()
}

var allStates = []string{"sleeping", "starting", "ready", "degraded"}

func setAgentState(agent, state string) {
	for _, s := range allStates {
		v := float64(0)
		if s == state {
			v = 1
		}
		AgentState.WithLabelValues(agent, s).Set(v)
	}
}

// RegisterEventHandler wires metric updates to the event emitter.
func RegisterEventHandler(emitter *events.Emitter) {
	emitter.OnEvent(func(ev events.Event) {
		switch ev.Type {
		case events.AgentReady:
			setAgentState(ev.Agent, "ready")
		case events.AgentDegraded:
			setAgentState(ev.Agent, "degraded")
		case events.AgentStarting:
			setAgentState(ev.Agent, "starting")
		case events.AgentSleep:
			setAgentState(ev.Agent, "sleeping")
			AgentSleepTotal.WithLabelValues(ev.Agent).Inc()
		case events.AgentWake:
			AgentWakeTotal.WithLabelValues(ev.Agent).Inc()
		case events.AgentHealthFailed:
			AgentHealthChecksTotal.WithLabelValues(ev.Agent, "fail").Inc()
		}
	})
}

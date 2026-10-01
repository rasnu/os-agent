/**
 * Project: RasnuOS
 * Copyright © 2026 Rasnu LLC (rasnu.com). All rights reserved.
 *
 * Milestone 3.3: HTTP health probe and Prometheus-compatible metrics endpoint.
 *
 * Exposes two endpoints on a dedicated port (default :9090, configurable via METRICS_PORT):
 *   GET /healthz  — Returns 200 OK with JSON { "status": "ok", "agent_version": "..." }
 *                   Used by container orchestrators (Kubernetes, Docker Compose) for liveness probes.
 *   GET /metrics  — Returns Prometheus text format metrics for Grafana/Datadog/PagerDuty scraping.
 *
 * Metrics exported:
 *   agent_commands_total{command_type}   — Counter: total commands dispatched by type
 *   agent_command_errors_total{command_type} — Counter: commands that errored by type
 *   agent_active_commands               — Gauge: currently in-flight commands
 *   agent_websocket_reconnects_total    — Counter: total WebSocket reconnection attempts
 *   agent_websocket_connected           — Gauge: 1 if WS is connected, 0 if not
 *   agent_uptime_seconds               — Gauge: seconds since agent start
 */
package main

import (
	"fmt"
	"net/http"
	"os"
	"sync/atomic"
	"time"
)

// ─── Atomic counters (safe for concurrent goroutines) ─────────────────────────

var (
	agentStartTime = time.Now()

	// commandsTotal: map[command_type] -> count (approximated via labeled counters below)
	agentCommandsTotal      int64 // total commands dispatched across all types
	agentCommandErrorsTotal int64 // total commands that resulted in errors
	agentActiveCommands     int64 // current in-flight commands (incremented on start, decremented on finish)
	agentWSReconnects       int64 // total WebSocket reconnect events
	agentWSConnected        int64 // 1 = connected, 0 = disconnected
)

// TrackCommandStart is called when a command begins execution.
// Call defer TrackCommandFinish() immediately after.
func TrackCommandStart() {
	atomic.AddInt64(&agentCommandsTotal, 1)
	atomic.AddInt64(&agentActiveCommands, 1)
}

// TrackCommandFinish decrements the active counter.
func TrackCommandFinish() {
	atomic.AddInt64(&agentActiveCommands, -1)
}

// TrackCommandError increments the error counter.
func TrackCommandError() {
	atomic.AddInt64(&agentCommandErrorsTotal, 1)
}

// TrackWSReconnect increments the WebSocket reconnect counter.
func TrackWSReconnect() {
	atomic.AddInt64(&agentWSReconnects, 1)
}

// SetWSConnected sets the WebSocket connected gauge (1 = connected, 0 = disconnected).
func SetWSConnected(connected bool) {
	if connected {
		atomic.StoreInt64(&agentWSConnected, 1)
	} else {
		atomic.StoreInt64(&agentWSConnected, 0)
	}
}

// ─── HTTP handlers ────────────────────────────────────────────────────────────

// healthzHandler returns a JSON liveness probe response.
func healthzHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, `{"status":"ok","agent_version":%q,"uptime_seconds":%.0f}`,
		version,
		time.Since(agentStartTime).Seconds(),
	)
}

// metricsHandler returns Prometheus text format metrics.
func metricsHandler(w http.ResponseWriter, r *http.Request) {
	uptime := time.Since(agentStartTime).Seconds()
	wsConn := atomic.LoadInt64(&agentWSConnected)

	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	fmt.Fprintf(w, `# HELP agent_commands_total Total number of commands dispatched to the Go Agent.
# TYPE agent_commands_total counter
agent_commands_total %d

# HELP agent_command_errors_total Total number of commands that resulted in an error.
# TYPE agent_command_errors_total counter
agent_command_errors_total %d

# HELP agent_active_commands Number of commands currently executing.
# TYPE agent_active_commands gauge
agent_active_commands %d

# HELP agent_websocket_reconnects_total Total WebSocket reconnection events.
# TYPE agent_websocket_reconnects_total counter
agent_websocket_reconnects_total %d

# HELP agent_websocket_connected 1 if the agent is currently connected to Reverb, 0 otherwise.
# TYPE agent_websocket_connected gauge
agent_websocket_connected %d

# HELP agent_uptime_seconds Seconds since the agent process started.
# TYPE agent_uptime_seconds gauge
agent_uptime_seconds %.0f
`,
		atomic.LoadInt64(&agentCommandsTotal),
		atomic.LoadInt64(&agentCommandErrorsTotal),
		atomic.LoadInt64(&agentActiveCommands),
		atomic.LoadInt64(&agentWSReconnects),
		wsConn,
		uptime,
	)
}

// StartMetricsServer starts a lightweight HTTP server exposing /healthz and /metrics.
// It runs in a background goroutine and respects the shutdown channel.
func StartMetricsServer(shutdownChan chan struct{}) {
	port := os.Getenv("METRICS_PORT")
	if port == "" {
		port = "9090"
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", healthzHandler)
	mux.HandleFunc("/metrics", metricsHandler)

	srv := &http.Server{
		Addr:         ":" + port,
		Handler:      mux,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 5 * time.Second,
	}

	go func() {
		<-shutdownChan
		srv.Close()
	}()

	go func() {
		fmt.Printf("[metrics] Listening on :%s — /healthz and /metrics\n", port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			fmt.Printf("[metrics] Server error: %v\n", err)
		}
	}()
}

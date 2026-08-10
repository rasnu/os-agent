// Copyright © 2026 Rasnu LLC (rasnu.com). All rights reserved.
// Licensed under the Functional Source License, Version 1.1, ALv2 Future License (FSL-1.1-ALv2).

package observability

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"sync/atomic"
	"time"

	"rasnuos-agent/internal/buildinfo"
)

var (
	agentStartTime          = time.Now()
	agentCommandsTotal      int64
	agentCommandErrorsTotal int64
	agentActiveCommands     int64
	agentWSReconnects       int64
	agentWSConnected        int64
)

func TrackCommandStart() {
	atomic.AddInt64(&agentCommandsTotal, 1)
	atomic.AddInt64(&agentActiveCommands, 1)
}

func TrackCommandFinish() {
	atomic.AddInt64(&agentActiveCommands, -1)
}

func TrackCommandError() {
	atomic.AddInt64(&agentCommandErrorsTotal, 1)
}

func TrackWSReconnect() {
	atomic.AddInt64(&agentWSReconnects, 1)
}

func SetWSConnected(connected bool) {
	if connected {
		atomic.StoreInt64(&agentWSConnected, 1)
	} else {
		atomic.StoreInt64(&agentWSConnected, 0)
	}
}

func healthzHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, `{"status":"ok","agent_version":%q,"uptime_seconds":%.0f}`,
		buildinfo.Version,
		time.Since(agentStartTime).Seconds(),
	)
}

func metricsHandler(w http.ResponseWriter, r *http.Request) {
	uptime := time.Since(agentStartTime).Seconds()
	wsConn := atomic.LoadInt64(&agentWSConnected)

	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	fmt.Fprintf(w, `# HELP agent_commands_total Total number of commands dispatched to the RasnuOS Agent.
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

// metricsListenAddr defaults to loopback (127.0.0.1) so /metrics and /healthz
// are not exposed on public interfaces unless METRICS_BIND is explicitly set.
func metricsListenAddr() string {
	port := os.Getenv("METRICS_PORT")
	switch port {
	case "":
		port = "9090"
	case "off", "disabled", "false":
		return ""
	}

	bind := os.Getenv("METRICS_BIND")
	if bind == "" {
		bind = "127.0.0.1"
	}
	return net.JoinHostPort(bind, port)
}

// StartMetricsServer starts the background HTTP server for /healthz and /metrics
// and shuts it down when shutdownChan is closed.
func StartMetricsServer(shutdownChan chan struct{}) {
	addr := metricsListenAddr()
	if addr == "" {
		fmt.Println("[metrics] Local observability server disabled (METRICS_PORT=off)")
		return
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", healthzHandler)
	mux.HandleFunc("/metrics", metricsHandler)

	srv := &http.Server{
		Addr:         addr,
		Handler:      mux,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 5 * time.Second,
	}

	go func() {
		<-shutdownChan
		srv.Close()
	}()

	go func() {
		fmt.Printf("[metrics] Listening on %s — /healthz and /metrics\n", addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			fmt.Printf("[metrics] Server error: %v\n", err)
		}
	}()
}

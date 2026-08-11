// Copyright © 2026 Rasnu LLC (rasnu.com). All rights reserved.
// Licensed under the Functional Source License, Version 1.1, ALv2 Future License (FSL-1.1-ALv2).

package observability

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestTrackersAndHandlers(t *testing.T) {
	TrackCommandStart()
	TrackCommandError()
	TrackWSReconnect()
	SetWSConnected(true)
	defer func() {
		TrackCommandFinish()
		SetWSConnected(false)
	}()

	// Test /healthz
	reqHealth := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	recHealth := httptest.NewRecorder()
	healthzHandler(recHealth, reqHealth)

	if recHealth.Code != http.StatusOK {
		t.Fatalf("expected status 200 for /healthz, got %d", recHealth.Code)
	}
	var healthPayload map[string]interface{}
	if err := json.Unmarshal(recHealth.Body.Bytes(), &healthPayload); err != nil {
		t.Fatalf("failed to decode /healthz JSON: %v", err)
	}
	if healthPayload["status"] != "ok" {
		t.Errorf("expected health status 'ok', got %v", healthPayload["status"])
	}

	// Test /metrics
	reqMetrics := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	recMetrics := httptest.NewRecorder()
	metricsHandler(recMetrics, reqMetrics)

	if recMetrics.Code != http.StatusOK {
		t.Fatalf("expected status 200 for /metrics, got %d", recMetrics.Code)
	}
	body := recMetrics.Body.String()
	for _, expected := range []string{
		"agent_commands_total ",
		"agent_command_errors_total ",
		"agent_active_commands ",
		"agent_websocket_reconnects_total ",
		"agent_websocket_connected 1",
		"agent_uptime_seconds ",
	} {
		if !strings.Contains(body, expected) {
			t.Errorf("expected /metrics output to contain %q, got:\n%s", expected, body)
		}
	}
}

func TestMetricsListenAddrAndServerLifecycle(t *testing.T) {
	origPort := os.Getenv("METRICS_PORT")
	origBind := os.Getenv("METRICS_BIND")
	defer func() {
		os.Setenv("METRICS_PORT", origPort)
		os.Setenv("METRICS_BIND", origBind)
	}()

	os.Setenv("METRICS_PORT", "")
	os.Setenv("METRICS_BIND", "")
	if addr := metricsListenAddr(); addr != "127.0.0.1:9090" {
		t.Errorf("expected default listen addr 127.0.0.1:9090, got %q", addr)
	}

	for _, disabledVal := range []string{"off", "disabled", "false"} {
		os.Setenv("METRICS_PORT", disabledVal)
		if addr := metricsListenAddr(); addr != "" {
			t.Errorf("expected empty addr for METRICS_PORT=%s, got %q", disabledVal, addr)
		}
	}

	// Disabled server returns immediately
	os.Setenv("METRICS_PORT", "off")
	shutdownDisabled := make(chan struct{})
	StartMetricsServer(shutdownDisabled)
	close(shutdownDisabled)

	// Ephemeral port server starts and shuts down cleanly
	os.Setenv("METRICS_BIND", "127.0.0.1")
	os.Setenv("METRICS_PORT", "0")
	shutdownLive := make(chan struct{})
	StartMetricsServer(shutdownLive)
	time.Sleep(30 * time.Millisecond)
	close(shutdownLive)
	time.Sleep(20 * time.Millisecond)
}

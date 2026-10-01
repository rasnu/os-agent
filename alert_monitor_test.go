package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"

	"rasnuos-agent/os_adapter"
)

// MockAlertHTTPClient intercepts the heartbeatHTTPClient calls
type MockAlertHTTPClient struct {
	Reqs []*http.Request
}

func (m *MockAlertHTTPClient) Do(req *http.Request) (*http.Response, error) {
	m.Reqs = append(m.Reqs, req)
	return &http.Response{
		StatusCode: 200,
		Body:       io.NopCloser(bytes.NewBufferString(`{"status":"ok"}`)),
	}, nil
}

func TestAlertMonitor_CPUSpike(t *testing.T) {
	// Reset global state
	globalAlertMonitor = &AlertMonitor{}
	globalAlertMonitor.lastAlertTime = time.Time{} // Ensure cooldown is expired

	mockHTTP := &MockAlertHTTPClient{}
	originalHTTP := heartbeatHTTPClient
	heartbeatHTTPClient = mockHTTP
	defer func() { heartbeatHTTPClient = originalHTTP }()

	c := &PusherClient{
		ApiURL: "http://localhost",
		Token:  "test-token",
	}

	metricsHighCPU := os_adapter.Metrics{CPUUsage: 96.0, RAMUsage: 50.0}

	// 1st spike
	c.checkAlerts(metricsHighCPU)
	if len(mockHTTP.Reqs) != 0 {
		t.Fatal("Expected no alert on 1st spike")
	}

	// 2nd spike
	c.checkAlerts(metricsHighCPU)
	if len(mockHTTP.Reqs) != 0 {
		t.Fatal("Expected no alert on 2nd spike")
	}

	// 3rd spike - SHOULD trigger
	c.checkAlerts(metricsHighCPU)
	time.Sleep(100 * time.Millisecond) // Wait for goroutine to dispatch

	if len(mockHTTP.Reqs) != 1 {
		t.Fatalf("Expected exactly 1 alert, got %d", len(mockHTTP.Reqs))
	}

	// Verify payload
	req := mockHTTP.Reqs[0]
	var payload AgentAlertPayload
	if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
		t.Fatalf("Failed to decode payload: %v", err)
	}

	if payload.Type != "cpu_spike" {
		t.Errorf("Expected type cpu_spike, got %s", payload.Type)
	}
	if payload.Value != 96.0 {
		t.Errorf("Expected value 96.0, got %f", payload.Value)
	}
}

func TestAlertMonitor_RAMSpike(t *testing.T) {
	globalAlertMonitor = &AlertMonitor{}
	globalAlertMonitor.lastAlertTime = time.Time{}

	mockHTTP := &MockAlertHTTPClient{}
	originalHTTP := heartbeatHTTPClient
	heartbeatHTTPClient = mockHTTP
	defer func() { heartbeatHTTPClient = originalHTTP }()

	c := &PusherClient{ApiURL: "http://localhost", Token: "test-token"}

	metricsHighRAM := os_adapter.Metrics{CPUUsage: 50.0, RAMUsage: 99.0}

	c.checkAlerts(metricsHighRAM)
	c.checkAlerts(metricsHighRAM)
	c.checkAlerts(metricsHighRAM)
	time.Sleep(100 * time.Millisecond)

	if len(mockHTTP.Reqs) != 1 {
		t.Fatalf("Expected exactly 1 RAM alert, got %d", len(mockHTTP.Reqs))
	}

	req := mockHTTP.Reqs[0]
	var payload AgentAlertPayload
	json.NewDecoder(req.Body).Decode(&payload)

	if payload.Type != "ram_spike" {
		t.Errorf("Expected type ram_spike, got %s", payload.Type)
	}
}

func TestAlertMonitor_Cooldown(t *testing.T) {
	globalAlertMonitor = &AlertMonitor{}
	// Set the last alert time to exactly 5 minutes ago (still in 15m cooldown)
	globalAlertMonitor.lastAlertTime = time.Now().Add(-5 * time.Minute)

	mockHTTP := &MockAlertHTTPClient{}
	originalHTTP := heartbeatHTTPClient
	heartbeatHTTPClient = mockHTTP
	defer func() { heartbeatHTTPClient = originalHTTP }()

	c := &PusherClient{ApiURL: "http://localhost", Token: "test-token"}

	metricsHighCPU := os_adapter.Metrics{CPUUsage: 99.0, RAMUsage: 50.0}

	// Trigger 3 spikes
	c.checkAlerts(metricsHighCPU)
	c.checkAlerts(metricsHighCPU)
	c.checkAlerts(metricsHighCPU)
	time.Sleep(100 * time.Millisecond)

	if len(mockHTTP.Reqs) != 0 {
		t.Fatalf("Expected 0 alerts due to cooldown, got %d", len(mockHTTP.Reqs))
	}

	// Advance time past cooldown
	globalAlertMonitor.lastAlertTime = time.Now().Add(-16 * time.Minute)

	// Trigger 3 more spikes
	c.checkAlerts(metricsHighCPU)
	c.checkAlerts(metricsHighCPU)
	c.checkAlerts(metricsHighCPU)
	time.Sleep(100 * time.Millisecond)

	if len(mockHTTP.Reqs) != 1 {
		t.Fatalf("Expected 1 alert after cooldown expired, got %d", len(mockHTTP.Reqs))
	}
}

func TestAlertMonitor_StateReset(t *testing.T) {
	globalAlertMonitor = &AlertMonitor{}
	globalAlertMonitor.lastAlertTime = time.Time{}

	mockHTTP := &MockAlertHTTPClient{}
	originalHTTP := heartbeatHTTPClient
	heartbeatHTTPClient = mockHTTP
	defer func() { heartbeatHTTPClient = originalHTTP }()

	c := &PusherClient{ApiURL: "http://localhost", Token: "test-token"}

	// 2 high CPU spikes
	c.checkAlerts(os_adapter.Metrics{CPUUsage: 99.0})
	c.checkAlerts(os_adapter.Metrics{CPUUsage: 99.0})

	// 1 normal CPU (should reset state)
	c.checkAlerts(os_adapter.Metrics{CPUUsage: 50.0})

	// 1 high CPU
	c.checkAlerts(os_adapter.Metrics{CPUUsage: 99.0})
	time.Sleep(100 * time.Millisecond)

	if len(mockHTTP.Reqs) != 0 {
		t.Fatalf("Expected 0 alerts because state was reset, got %d", len(mockHTTP.Reqs))
	}
}

func TestAlertMonitor_SimultaneousSpike(t *testing.T) {
	globalAlertMonitor = &AlertMonitor{}
	globalAlertMonitor.lastAlertTime = time.Time{}

	mockHTTP := &MockAlertHTTPClient{}
	originalHTTP := heartbeatHTTPClient
	heartbeatHTTPClient = mockHTTP
	defer func() { heartbeatHTTPClient = originalHTTP }()

	c := &PusherClient{ApiURL: "http://localhost", Token: "test-token"}

	// CPU and RAM both high
	metricsBoth := os_adapter.Metrics{CPUUsage: 99.0, RAMUsage: 99.0}

	c.checkAlerts(metricsBoth)
	c.checkAlerts(metricsBoth)
	c.checkAlerts(metricsBoth)
	time.Sleep(100 * time.Millisecond)

	// CPU takes priority, RAM is NOT dispatched on this tick.
	if len(mockHTTP.Reqs) != 1 {
		t.Fatalf("Expected exactly 1 alert, got %d", len(mockHTTP.Reqs))
	}

	req := mockHTTP.Reqs[0]
	var payload AgentAlertPayload
	json.NewDecoder(req.Body).Decode(&payload)

	if payload.Type != "cpu_spike" {
		t.Errorf("Expected type cpu_spike because CPU takes priority, got %s", payload.Type)
	}
}

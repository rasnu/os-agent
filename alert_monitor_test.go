package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"sync"
	"testing"
	"time"

	"rasnuos-agent/os_adapter"
)

// MockAlertHTTPClient intercepts the heartbeatHTTPClient calls
type MockAlertHTTPClient struct {
	mu   sync.Mutex
	Reqs []*http.Request
}

func (m *MockAlertHTTPClient) Do(req *http.Request) (*http.Response, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Reqs = append(m.Reqs, req)
	return &http.Response{
		StatusCode: 200,
		Body:       io.NopCloser(bytes.NewBufferString(`{"status":"ok"}`)),
	}, nil
}

func TestAlertMonitor_CPUSpike(t *testing.T) {
	// Reset global state
	globalAlertMonitor = &AlertMonitor{
		thresholds: AlertThresholds{
			CPU:           95.0,
			RAM:           95.0,
			Disk:          90.0,
			Swap:          80.0,
			IOWait:        50.0,
			LoadMultiplier: 2.0,
		},
	}
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
	globalAlertMonitor = &AlertMonitor{
		thresholds: AlertThresholds{
			CPU:           95.0,
			RAM:           95.0,
			Disk:          90.0,
			Swap:          80.0,
			IOWait:        50.0,
			LoadMultiplier: 2.0,
		},
	}
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
	globalAlertMonitor = &AlertMonitor{
		thresholds: AlertThresholds{
			CPU:           95.0,
			RAM:           95.0,
			Disk:          90.0,
			Swap:          80.0,
			IOWait:        50.0,
			LoadMultiplier: 2.0,
		},
	}
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
	globalAlertMonitor = &AlertMonitor{
		thresholds: AlertThresholds{
			CPU:           95.0,
			RAM:           95.0,
			Disk:          90.0,
			Swap:          80.0,
			IOWait:        50.0,
			LoadMultiplier: 2.0,
		},
	}
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
	globalAlertMonitor = &AlertMonitor{
		thresholds: AlertThresholds{
			CPU:           95.0,
			RAM:           95.0,
			Disk:          90.0,
			Swap:          80.0,
			IOWait:        50.0,
			LoadMultiplier: 2.0,
		},
	}
	globalAlertMonitor.lastAlertTime = time.Time{}

	mockHTTP := &MockAlertHTTPClient{}
	originalHTTP := heartbeatHTTPClient
	heartbeatHTTPClient = mockHTTP
	defer func() { heartbeatHTTPClient = originalHTTP }()

	c := &PusherClient{ApiURL: "http://localhost", Token: "test-token"}

	// CPU, RAM, and Disk all high
	metricsBoth := os_adapter.Metrics{CPUUsage: 99.0, RAMUsage: 99.0, DiskUsage: 99.0}

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

func TestAlertMonitor_DiskFull(t *testing.T) {
	globalAlertMonitor = &AlertMonitor{
		thresholds: AlertThresholds{
			CPU:           95.0,
			RAM:           95.0,
			Disk:          90.0,
			Swap:          80.0,
			IOWait:        50.0,
			LoadMultiplier: 2.0,
		},
	}
	globalAlertMonitor.lastAlertTime = time.Time{}

	mockHTTP := &MockAlertHTTPClient{}
	originalHTTP := heartbeatHTTPClient
	heartbeatHTTPClient = mockHTTP
	defer func() { heartbeatHTTPClient = originalHTTP }()

	c := &PusherClient{ApiURL: "http://localhost", Token: "test-token"}

	metricsHighDisk := os_adapter.Metrics{CPUUsage: 50.0, RAMUsage: 50.0, DiskUsage: 95.0}

	c.checkAlerts(metricsHighDisk)
	c.checkAlerts(metricsHighDisk)
	c.checkAlerts(metricsHighDisk)
	time.Sleep(100 * time.Millisecond)

	if len(mockHTTP.Reqs) != 1 {
		t.Fatalf("Expected exactly 1 Disk alert, got %d", len(mockHTTP.Reqs))
	}

	req := mockHTTP.Reqs[0]
	var payload AgentAlertPayload
	json.NewDecoder(req.Body).Decode(&payload)

	if payload.Type != "disk_full" {
		t.Errorf("Expected type disk_full, got %s", payload.Type)
	}
	if payload.Value != 95.0 {
		t.Errorf("Expected value 95.0, got %f", payload.Value)
	}
}

func TestAlertMonitor_ServiceCrash(t *testing.T) {
	globalAlertMonitor = &AlertMonitor{
		thresholds: AlertThresholds{
			CPU:           95.0,
			RAM:           95.0,
			Disk:          90.0,
			Swap:          80.0,
			IOWait:        50.0,
			LoadMultiplier: 2.0,
		},
	}
	globalAlertMonitor.lastAlertTime = time.Time{}

	mockHTTP := &MockAlertHTTPClient{}
	originalHTTP := heartbeatHTTPClient
	heartbeatHTTPClient = mockHTTP
	defer func() { heartbeatHTTPClient = originalHTTP }()

	c := &PusherClient{ApiURL: "http://localhost", Token: "test-token"}

	// Simulate a service crash (no debouncing needed, should trigger on first interval)
	metricsCrashedService := os_adapter.Metrics{
		CPUUsage:  50.0,
		RAMUsage:  50.0,
		DiskUsage: 50.0,
		Services: []os_adapter.ServiceStatus{
			{Name: "nginx", Status: "active"},
			{Name: "mysql", Status: "failed"}, // Trigger
		},
	}

	c.checkAlerts(metricsCrashedService)
	time.Sleep(100 * time.Millisecond)

	if len(mockHTTP.Reqs) != 1 {
		t.Fatalf("Expected exactly 1 Service Crash alert, got %d", len(mockHTTP.Reqs))
	}

	req := mockHTTP.Reqs[0]
	var payload AgentAlertPayload
	json.NewDecoder(req.Body).Decode(&payload)

	if payload.Type != "service_crash" {
		t.Errorf("Expected type service_crash, got %s", payload.Type)
	}
	if payload.Context["service_name"] != "mysql" {
		t.Errorf("Expected service_name mysql, got %v", payload.Context["service_name"])
	}
}

func TestAlertMonitor_OOMKill(t *testing.T) {
	globalAlertMonitor = &AlertMonitor{
		thresholds: AlertThresholds{
			CPU:           95.0,
			RAM:           95.0,
			Disk:          90.0,
			Swap:          80.0,
			IOWait:        50.0,
			LoadMultiplier: 2.0,
		},
	}
	globalAlertMonitor.lastAlertTime = time.Time{}

	mockHTTP := &MockAlertHTTPClient{}
	originalHTTP := heartbeatHTTPClient
	heartbeatHTTPClient = mockHTTP
	defer func() { heartbeatHTTPClient = originalHTTP }()

	c := &PusherClient{ApiURL: "http://localhost", Token: "test-token"}

	// Simulate OOM kill metric
	metricsOOM := os_adapter.Metrics{
		CPUUsage: 50.0,
		OOMKills: []string{"Out of memory: Killed process 999 (postgres)"},
	}

	// 1st cycle: Should trigger immediately
	c.checkAlerts(metricsOOM)
	time.Sleep(100 * time.Millisecond)

	if len(mockHTTP.Reqs) != 1 {
		t.Fatalf("Expected exactly 1 OOM alert on first sight, got %d", len(mockHTTP.Reqs))
	}

	req := mockHTTP.Reqs[0]
	var payload AgentAlertPayload
	json.NewDecoder(req.Body).Decode(&payload)

	if payload.Type != "oom_kill" {
		t.Errorf("Expected type oom_kill, got %s", payload.Type)
	}

	// 2nd cycle: 15 minutes pass, but the exact SAME OOM message is still in dmesg tail
	globalAlertMonitor.lastAlertTime = time.Now().Add(-16 * time.Minute) // Bypass cooldown lock
	c.checkAlerts(metricsOOM)
	time.Sleep(100 * time.Millisecond)

	// Should NOT trigger again because lastOOMMessage deduplication catches it!
	if len(mockHTTP.Reqs) != 1 {
		t.Fatalf("Expected OOM alert to be deduplicated, but got %d alerts", len(mockHTTP.Reqs))
	}

	// 3rd cycle: A brand NEW OOM kill happens
	metricsOOM.OOMKills = append(metricsOOM.OOMKills, "Out of memory: Killed process 1005 (redis)")
	c.checkAlerts(metricsOOM)
	time.Sleep(100 * time.Millisecond)

	// Should trigger because it's a new unique message
	if len(mockHTTP.Reqs) != 2 {
		t.Fatalf("Expected new OOM alert to trigger, got %d", len(mockHTTP.Reqs))
	}
}

func TestAlertMonitor_SwapPressure(t *testing.T) {
	globalAlertMonitor = &AlertMonitor{
		thresholds: AlertThresholds{
			CPU:           95.0,
			RAM:           95.0,
			Disk:          90.0,
			Swap:          80.0,
			IOWait:        50.0,
			LoadMultiplier: 2.0,
		},
	}
	globalAlertMonitor.lastAlertTime = time.Time{}

	mockHTTP := &MockAlertHTTPClient{}
	originalHTTP := heartbeatHTTPClient
	heartbeatHTTPClient = mockHTTP
	defer func() { heartbeatHTTPClient = originalHTTP }()

	c := &PusherClient{ApiURL: "http://localhost", Token: "test-token"}

	metricsSwap := os_adapter.Metrics{SwapUsage: 85.0}

	c.checkAlerts(metricsSwap)
	c.checkAlerts(metricsSwap)
	c.checkAlerts(metricsSwap)
	time.Sleep(100 * time.Millisecond)

	if len(mockHTTP.Reqs) != 1 {
		t.Fatalf("Expected 1 swap_pressure alert, got %d", len(mockHTTP.Reqs))
	}

	var payload AgentAlertPayload
	json.NewDecoder(mockHTTP.Reqs[0].Body).Decode(&payload)

	if payload.Type != "swap_pressure" {
		t.Errorf("Expected type swap_pressure, got %s", payload.Type)
	}
}

func TestAlertMonitor_IOWait(t *testing.T) {
	globalAlertMonitor = &AlertMonitor{
		thresholds: AlertThresholds{
			CPU:           95.0,
			RAM:           95.0,
			Disk:          90.0,
			Swap:          80.0,
			IOWait:        50.0,
			LoadMultiplier: 2.0,
		},
	}
	globalAlertMonitor.lastAlertTime = time.Time{}

	mockHTTP := &MockAlertHTTPClient{}
	originalHTTP := heartbeatHTTPClient
	heartbeatHTTPClient = mockHTTP
	defer func() { heartbeatHTTPClient = originalHTTP }()

	c := &PusherClient{ApiURL: "http://localhost", Token: "test-token"}

	metricsIOWait := os_adapter.Metrics{IOWait: 55.0}

	c.checkAlerts(metricsIOWait)
	c.checkAlerts(metricsIOWait)
	c.checkAlerts(metricsIOWait)
	time.Sleep(100 * time.Millisecond)

	if len(mockHTTP.Reqs) != 1 {
		t.Fatalf("Expected 1 io_wait alert, got %d", len(mockHTTP.Reqs))
	}

	var payload AgentAlertPayload
	json.NewDecoder(mockHTTP.Reqs[0].Body).Decode(&payload)

	if payload.Type != "io_wait" {
		t.Errorf("Expected type io_wait, got %s", payload.Type)
	}
}

func TestAlertMonitor_HighLoad(t *testing.T) {
	globalAlertMonitor = &AlertMonitor{
		thresholds: AlertThresholds{
			CPU:           95.0,
			RAM:           95.0,
			Disk:          90.0,
			Swap:          80.0,
			IOWait:        50.0,
			LoadMultiplier: 2.0,
		},
	}
	globalAlertMonitor.lastAlertTime = time.Time{}

	mockHTTP := &MockAlertHTTPClient{}
	originalHTTP := heartbeatHTTPClient
	heartbeatHTTPClient = mockHTTP
	defer func() { heartbeatHTTPClient = originalHTTP }()

	c := &PusherClient{ApiURL: "http://localhost", Token: "test-token"}

	// 4 cores, load 8.5 (greater than 8.0)
	metricsLoad := os_adapter.Metrics{CPUCores: 4, Load5: 8.5}

	c.checkAlerts(metricsLoad)
	c.checkAlerts(metricsLoad)
	c.checkAlerts(metricsLoad)
	time.Sleep(100 * time.Millisecond)

	if len(mockHTTP.Reqs) != 1 {
		t.Fatalf("Expected 1 high_load alert, got %d", len(mockHTTP.Reqs))
	}

	var payload AgentAlertPayload
	json.NewDecoder(mockHTTP.Reqs[0].Body).Decode(&payload)

	if payload.Type != "high_load" {
		t.Errorf("Expected type high_load, got %s", payload.Type)
	}
}

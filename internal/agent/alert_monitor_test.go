// Copyright © 2026 Rasnu LLC (rasnu.com). All rights reserved.
// Licensed under the Functional Source License, Version 1.1, ALv2 Future License (FSL-1.1-ALv2).

package agent

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"sync"
	"testing"
	"time"

	"rasnuos-agent/internal/transport"

	"rasnuos-agent/internal/osadapter"
)

// MockAlertHTTPClient intercepts transport.Heartbeat calls.
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
	globalAlertMonitor = &AlertMonitor{
		thresholds: AlertThresholds{
			CPU:            95.0,
			RAM:            95.0,
			Disk:           90.0,
			Swap:           80.0,
			IOWait:         50.0,
			LoadMultiplier: 2.0,
		},
	}
	globalAlertMonitor.lastAlertTime = time.Time{}

	mockHTTP := &MockAlertHTTPClient{}
	originalHTTP := transport.Heartbeat
	transport.Heartbeat = mockHTTP
	defer func() { transport.Heartbeat = originalHTTP }()

	c := &PusherClient{
		ApiURL: "http://localhost",
		Token:  "test-token",
	}

	metricsHighCPU := osadapter.Metrics{CPUUsage: 96.0, RAMUsage: 50.0}

	c.checkAlerts(metricsHighCPU)
	if len(mockHTTP.Reqs) != 0 {
		t.Fatal("Expected no alert on 1st spike")
	}

	c.checkAlerts(metricsHighCPU)
	if len(mockHTTP.Reqs) != 0 {
		t.Fatal("Expected no alert on 2nd spike")
	}

	c.checkAlerts(metricsHighCPU)
	time.Sleep(100 * time.Millisecond)

	if len(mockHTTP.Reqs) != 1 {
		t.Fatalf("Expected exactly 1 alert, got %d", len(mockHTTP.Reqs))
	}

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
			CPU:            95.0,
			RAM:            95.0,
			Disk:           90.0,
			Swap:           80.0,
			IOWait:         50.0,
			LoadMultiplier: 2.0,
		},
	}
	globalAlertMonitor.lastAlertTime = time.Time{}

	mockHTTP := &MockAlertHTTPClient{}
	originalHTTP := transport.Heartbeat
	transport.Heartbeat = mockHTTP
	defer func() { transport.Heartbeat = originalHTTP }()

	c := &PusherClient{ApiURL: "http://localhost", Token: "test-token"}

	metricsHighRAM := osadapter.Metrics{CPUUsage: 50.0, RAMUsage: 99.0}

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
			CPU:            95.0,
			RAM:            95.0,
			Disk:           90.0,
			Swap:           80.0,
			IOWait:         50.0,
			LoadMultiplier: 2.0,
		},
	}
	globalAlertMonitor.lastAlertTime = time.Now().Add(-5 * time.Minute)

	mockHTTP := &MockAlertHTTPClient{}
	originalHTTP := transport.Heartbeat
	transport.Heartbeat = mockHTTP
	defer func() { transport.Heartbeat = originalHTTP }()

	c := &PusherClient{ApiURL: "http://localhost", Token: "test-token"}

	metricsHighCPU := osadapter.Metrics{CPUUsage: 99.0, RAMUsage: 50.0}

	c.checkAlerts(metricsHighCPU)
	c.checkAlerts(metricsHighCPU)
	c.checkAlerts(metricsHighCPU)
	time.Sleep(100 * time.Millisecond)

	if len(mockHTTP.Reqs) != 0 {
		t.Fatalf("Expected 0 alerts due to cooldown, got %d", len(mockHTTP.Reqs))
	}

	globalAlertMonitor.lastAlertTime = time.Now().Add(-16 * time.Minute)

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
			CPU:            95.0,
			RAM:            95.0,
			Disk:           90.0,
			Swap:           80.0,
			IOWait:         50.0,
			LoadMultiplier: 2.0,
		},
	}
	globalAlertMonitor.lastAlertTime = time.Time{}

	mockHTTP := &MockAlertHTTPClient{}
	originalHTTP := transport.Heartbeat
	transport.Heartbeat = mockHTTP
	defer func() { transport.Heartbeat = originalHTTP }()

	c := &PusherClient{ApiURL: "http://localhost", Token: "test-token"}

	c.checkAlerts(osadapter.Metrics{CPUUsage: 99.0})
	c.checkAlerts(osadapter.Metrics{CPUUsage: 99.0})
	c.checkAlerts(osadapter.Metrics{CPUUsage: 50.0})
	c.checkAlerts(osadapter.Metrics{CPUUsage: 99.0})
	time.Sleep(100 * time.Millisecond)

	if len(mockHTTP.Reqs) != 0 {
		t.Fatalf("Expected 0 alerts because state was reset, got %d", len(mockHTTP.Reqs))
	}
}

func TestAlertMonitor_SimultaneousSpike(t *testing.T) {
	globalAlertMonitor = &AlertMonitor{
		thresholds: AlertThresholds{
			CPU:            95.0,
			RAM:            95.0,
			Disk:           90.0,
			Swap:           80.0,
			IOWait:         50.0,
			LoadMultiplier: 2.0,
		},
	}
	globalAlertMonitor.lastAlertTime = time.Time{}

	mockHTTP := &MockAlertHTTPClient{}
	originalHTTP := transport.Heartbeat
	transport.Heartbeat = mockHTTP
	defer func() { transport.Heartbeat = originalHTTP }()

	c := &PusherClient{ApiURL: "http://localhost", Token: "test-token"}

	metricsBoth := osadapter.Metrics{CPUUsage: 99.0, RAMUsage: 99.0, DiskUsage: 99.0}

	c.checkAlerts(metricsBoth)
	c.checkAlerts(metricsBoth)
	c.checkAlerts(metricsBoth)
	time.Sleep(100 * time.Millisecond)

	// CPU takes priority over RAM and Disk on a single tick.
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
			CPU:            95.0,
			RAM:            95.0,
			Disk:           90.0,
			Swap:           80.0,
			IOWait:         50.0,
			LoadMultiplier: 2.0,
		},
	}
	globalAlertMonitor.lastAlertTime = time.Time{}

	mockHTTP := &MockAlertHTTPClient{}
	originalHTTP := transport.Heartbeat
	transport.Heartbeat = mockHTTP
	defer func() { transport.Heartbeat = originalHTTP }()

	c := &PusherClient{ApiURL: "http://localhost", Token: "test-token"}

	metricsHighDisk := osadapter.Metrics{CPUUsage: 50.0, RAMUsage: 50.0, DiskUsage: 95.0}

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
			CPU:            95.0,
			RAM:            95.0,
			Disk:           90.0,
			Swap:           80.0,
			IOWait:         50.0,
			LoadMultiplier: 2.0,
		},
	}
	globalAlertMonitor.lastAlertTime = time.Time{}

	mockHTTP := &MockAlertHTTPClient{}
	originalHTTP := transport.Heartbeat
	transport.Heartbeat = mockHTTP
	defer func() { transport.Heartbeat = originalHTTP }()

	c := &PusherClient{ApiURL: "http://localhost", Token: "test-token"}

	metricsCrashedService := osadapter.Metrics{
		CPUUsage:  50.0,
		RAMUsage:  50.0,
		DiskUsage: 50.0,
		Services: []osadapter.ServiceStatus{
			{Name: "nginx", Status: "active"},
			{Name: "mysql", Status: "failed"},
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
			CPU:            95.0,
			RAM:            95.0,
			Disk:           90.0,
			Swap:           80.0,
			IOWait:         50.0,
			LoadMultiplier: 2.0,
		},
	}
	globalAlertMonitor.lastAlertTime = time.Time{}

	mockHTTP := &MockAlertHTTPClient{}
	originalHTTP := transport.Heartbeat
	transport.Heartbeat = mockHTTP
	defer func() { transport.Heartbeat = originalHTTP }()

	c := &PusherClient{ApiURL: "http://localhost", Token: "test-token"}

	metricsOOM := osadapter.Metrics{
		CPUUsage: 50.0,
		OOMKills: []string{"Out of memory: Killed process 999 (postgres)"},
	}

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

	// Identical OOM messages remain in the dmesg tail after cooldown expiry and must be deduplicated via lastOOMMessage.
	globalAlertMonitor.lastAlertTime = time.Now().Add(-16 * time.Minute)
	c.checkAlerts(metricsOOM)
	time.Sleep(100 * time.Millisecond)

	if len(mockHTTP.Reqs) != 1 {
		t.Fatalf("Expected OOM alert to be deduplicated, but got %d alerts", len(mockHTTP.Reqs))
	}

	metricsOOM.OOMKills = append(metricsOOM.OOMKills, "Out of memory: Killed process 1005 (redis)")
	c.checkAlerts(metricsOOM)
	time.Sleep(100 * time.Millisecond)

	if len(mockHTTP.Reqs) != 2 {
		t.Fatalf("Expected new OOM alert to trigger, got %d", len(mockHTTP.Reqs))
	}
}

func TestAlertMonitor_SwapPressure(t *testing.T) {
	globalAlertMonitor = &AlertMonitor{
		thresholds: AlertThresholds{
			CPU:            95.0,
			RAM:            95.0,
			Disk:           90.0,
			Swap:           80.0,
			IOWait:         50.0,
			LoadMultiplier: 2.0,
		},
	}
	globalAlertMonitor.lastAlertTime = time.Time{}

	mockHTTP := &MockAlertHTTPClient{}
	originalHTTP := transport.Heartbeat
	transport.Heartbeat = mockHTTP
	defer func() { transport.Heartbeat = originalHTTP }()

	c := &PusherClient{ApiURL: "http://localhost", Token: "test-token"}

	metricsSwap := osadapter.Metrics{SwapUsage: 85.0}

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
			CPU:            95.0,
			RAM:            95.0,
			Disk:           90.0,
			Swap:           80.0,
			IOWait:         50.0,
			LoadMultiplier: 2.0,
		},
	}
	globalAlertMonitor.lastAlertTime = time.Time{}

	mockHTTP := &MockAlertHTTPClient{}
	originalHTTP := transport.Heartbeat
	transport.Heartbeat = mockHTTP
	defer func() { transport.Heartbeat = originalHTTP }()

	c := &PusherClient{ApiURL: "http://localhost", Token: "test-token"}

	metricsIOWait := osadapter.Metrics{IOWait: 55.0}

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
			CPU:            95.0,
			RAM:            95.0,
			Disk:           90.0,
			Swap:           80.0,
			IOWait:         50.0,
			LoadMultiplier: 2.0,
		},
	}
	globalAlertMonitor.lastAlertTime = time.Time{}

	mockHTTP := &MockAlertHTTPClient{}
	originalHTTP := transport.Heartbeat
	transport.Heartbeat = mockHTTP
	defer func() { transport.Heartbeat = originalHTTP }()

	c := &PusherClient{ApiURL: "http://localhost", Token: "test-token"}

	metricsLoad := osadapter.Metrics{CPUCores: 4, Load5: 8.5}

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

func TestUpdateAlertThresholds(t *testing.T) {
	UpdateAlertThresholds(map[string]interface{}{
		"cpu":             85.5,
		"ram":             88.0,
		"disk":            75.0,
		"swap":            60.0,
		"iowait":          40.0,
		"load_multiplier": 1.5,
	})

	globalAlertMonitor.mu.Lock()
	th := globalAlertMonitor.thresholds
	globalAlertMonitor.mu.Unlock()

	if th.CPU != 85.5 || th.RAM != 88.0 || th.Disk != 75.0 || th.Swap != 60.0 || th.IOWait != 40.0 || th.LoadMultiplier != 1.5 {
		t.Errorf("Unexpected updated thresholds: %+v", th)
	}
}

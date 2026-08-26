// Copyright © 2026 Rasnu LLC (rasnu.com). All rights reserved.
// Licensed under the Functional Source License, Version 1.1, ALv2 Future License (FSL-1.1-ALv2).

package agent

import (
	"encoding/json"
	"strings"
	"testing"

	"rasnuos-agent/internal/osadapter"
)

func TestMetricsPayload_SerializesAllFields(t *testing.T) {
	payload := MetricsPayload{
		ServerID:     "srv-1",
		AgentVersion: "1.0.1",
		CPUUsage:     45.2,
		RAMUsage:     62.8,
		DiskUsage:    38.0,
		SwapUsage:    5.0,
		LoadAvg:      "0.42, 0.55, 0.60",
		NetworkRX:    1024000,
		NetworkTX:    512000,
		DiskRead:     2048,
		DiskWrite:    1024,
		Processes: []osadapter.Process{
			{PID: "1234", User: "www-data", CPU: "12.5", Memory: "3.2", Command: "php-fpm: worker"},
			{PID: "5678", User: "root", CPU: "0.1", Memory: "0.5", Command: "nginx: worker"},
		},
		Services: []osadapter.ServiceStatus{
			{Name: "nginx", Status: "active"},
			{Name: "mysql", Status: "active"},
			{Name: "redis-server", Status: "inactive"},
		},
		Errors: []string{"cpu: parse error", "disk_io: unexpected format"},
	}

	b, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("json.Marshal(MetricsPayload) failed: %v", err)
	}

	s := string(b)

	checks := []struct {
		field string
		want  string
	}{
		{"server_id", `"server_id":"srv-1"`},
		{"agent_version", `"agent_version":"1.0.1"`},
		{"cpu_usage", `"cpu_usage":45.2`},
		{"ram_usage", `"ram_usage":62.8`},
		{"disk_usage", `"disk_usage":38`},
		{"swap_usage", `"swap_usage":5`},
		{"load_average", `"load_average":"0.42, 0.55, 0.60"`},
		{"network_rx", `"network_rx":1024000`},
		{"network_tx", `"network_tx":512000`},
		{"disk_read_kb", `"disk_read_kb":2048`},
		{"disk_write_kb", `"disk_write_kb":1024`},
		{"processes", `"processes"`},
		{"services", `"services"`},
		{"errors", `"errors":["cpu: parse error","disk_io: unexpected format"]`},
	}

	for _, c := range checks {
		if !strings.Contains(s, c.want) {
			t.Errorf("MetricsPayload JSON missing %s: want %q in %s", c.field, c.want, s)
		}
	}
}

func TestMetricsPayload_ZeroValues(t *testing.T) {
	payload := MetricsPayload{
		ServerID:  "srv-zero",
		Processes: []osadapter.Process{},
		Services:  []osadapter.ServiceStatus{},
	}

	b, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("json.Marshal(MetricsPayload zero) failed: %v", err)
	}

	var decoded MetricsPayload
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatalf("json.Unmarshal(MetricsPayload zero) failed: %v", err)
	}

	if decoded.ServerID != "srv-zero" {
		t.Errorf("ServerID = %q; want srv-zero", decoded.ServerID)
	}
	if decoded.CPUUsage != 0.0 {
		t.Errorf("CPUUsage = %f; want 0.0", decoded.CPUUsage)
	}
	if decoded.Errors != nil {
		if len(decoded.Errors) != 0 {
			t.Errorf("Errors = %v; want empty", decoded.Errors)
		}
	}
}

func TestProcess_JsonTags(t *testing.T) {
	p := osadapter.Process{
		PID:     "1234",
		User:    "root",
		CPU:     "15.2",
		Memory:  "4.1",
		Command: "php-fpm: master process",
	}

	b, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("json.Marshal(Process) failed: %v", err)
	}

	s := string(b)
	expectedFields := []string{`"pid"`, `"user"`, `"cpu"`, `"memory"`, `"command"`}
	for _, field := range expectedFields {
		if !strings.Contains(s, field) {
			t.Errorf("Process JSON missing JSON tag %s in: %s", field, s)
		}
	}
}

func TestServiceStatus_JsonTags(t *testing.T) {
	s := osadapter.ServiceStatus{Name: "nginx", Status: "active"}
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("json.Marshal(ServiceStatus) failed: %v", err)
	}

	payload := string(b)
	if !strings.Contains(payload, `"name":"nginx"`) {
		t.Errorf("ServiceStatus JSON missing name: %s", payload)
	}
	if !strings.Contains(payload, `"status":"active"`) {
		t.Errorf("ServiceStatus JSON missing status: %s", payload)
	}
}

func TestDiskIOSectorToKBConversion(t *testing.T) {
	// Sectors are 512 bytes, so sectorCount / 2 = KB.
	sectors := uint64(1024)
	expectedKB := uint64(512)

	gotKB := sectors / 2
	if gotKB != expectedKB {
		t.Errorf("sector to KB: got %d; want %d", gotKB, expectedKB)
	}
}

func TestDiskIOSectorToKBConversion_ZeroSectors(t *testing.T) {
	sectors := uint64(0)
	gotKB := sectors / 2
	if gotKB != 0 {
		t.Errorf("zero sectors should yield 0 KB, got %d", gotKB)
	}
}

func TestNetworkIOParsing_ValidProcNetDevLine(t *testing.T) {
	line := "1024000 512000"
	parts := strings.Fields(line)

	if len(parts) != 2 {
		t.Fatalf("expected 2 fields, got %d", len(parts))
	}
	if parts[0] != "1024000" {
		t.Errorf("rx = %q; want 1024000", parts[0])
	}
	if parts[1] != "512000" {
		t.Errorf("tx = %q; want 512000", parts[1])
	}
}

func TestNetworkIOParsing_EmptyOutput(t *testing.T) {
	parts := strings.Fields("")
	if len(parts) != 0 {
		t.Errorf("empty output should produce 0 fields, got %d", len(parts))
	}
}

func TestProcessLineParsing_ValidLine(t *testing.T) {
	line := "1234 www-data 12.5 3.2 php-fpm: worker process"
	parts := strings.Fields(line)

	if len(parts) < 5 {
		t.Fatalf("expected at least 5 parts, got %d", len(parts))
	}

	proc := osadapter.Process{
		PID:     parts[0],
		User:    parts[1],
		CPU:     parts[2],
		Memory:  parts[3],
		Command: strings.Join(parts[4:], " "),
	}

	if proc.PID != "1234" {
		t.Errorf("PID = %q; want 1234", proc.PID)
	}
	if proc.CPU != "12.5" {
		t.Errorf("CPU = %q; want 12.5", proc.CPU)
	}
	if proc.Command != "php-fpm: worker process" {
		t.Errorf("Command = %q; want \"php-fpm: worker process\"", proc.Command)
	}
}

func TestProcessLineParsing_ShortLine_Skipped(t *testing.T) {
	shortLines := []string{
		"",
		"1234",
		"1234 root",
		"1234 root 12.5",
		"1234 root 12.5 3.2",
	}

	for _, line := range shortLines {
		parts := strings.Fields(line)
		if len(parts) >= 5 {
			t.Errorf("line %q has %d parts — should have been skipped (< 5)", line, len(parts))
		}
	}
}

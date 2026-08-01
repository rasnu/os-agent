// Copyright © 2026 Rasnu LLC (rasnu.com). All rights reserved.
// Licensed under the Functional Source License, Version 1.1, ALv2 Future License (FSL-1.1-ALv2).

//go:build linux
// +build linux

package osadapter

import (
	"context"
	"strings"
	"testing"
)

func TestLinuxAdapter_GetMetrics(t *testing.T) {
	adapter := &LinuxAdapter{}

	metrics, errs := adapter.GetMetrics()

	if metrics.CPUUsage < 0 {
		t.Errorf("Expected CPU usage >= 0, got %v", metrics.CPUUsage)
	}

	for _, err := range errs {
		if err == nil {
			t.Errorf("Received nil error in errors slice")
		}
	}
}

func TestLinuxAdapter_GetShellCmd(t *testing.T) {
	adapter := &LinuxAdapter{}

	cmd := adapter.GetShellCmd(context.Background(), "echo hello", "")
	if cmd == nil {
		t.Fatalf("Expected non-nil command")
	}

	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("Failed to execute bash cmd: %v", err)
	}

	if strings.TrimSpace(string(out)) != "hello" {
		t.Errorf("Expected 'hello', got '%s'", string(out))
	}
}

func TestLinuxAdapter_StartPTY(t *testing.T) {
	adapter := &LinuxAdapter{}

	ptyInst, err := adapter.StartPTY(80, 24)
	if err != nil {
		t.Logf("Failed to start PTY in this test environment: %v", err)
		return
	}

	defer ptyInst.Kill()
	defer ptyInst.Close()

	err = ptyInst.Resize(100, 30)
	if err != nil {
		t.Errorf("Resize failed: %v", err)
	}

	_, _ = ptyInst.Write([]byte("echo test\n"))
	buf := make([]byte, 1024)
	_, _ = ptyInst.Read(buf)
}

func TestLinuxAdapter_ManageService(t *testing.T) {
	adapter := &LinuxAdapter{}
	_, _ = adapter.ManageService("status", "nonexistent-service")
}

func TestLinuxAdapter_KillProcess(t *testing.T) {
	adapter := &LinuxAdapter{}
	_, _ = adapter.KillProcess("99999999")
}

func TestLinuxAdapter_WriteCronJobs(t *testing.T) {
	adapter := &LinuxAdapter{}
	_ = adapter.WriteCronJobs("test content")
}

func TestLinuxAdapter_InstallUninstallService(t *testing.T) {
	adapter := &LinuxAdapter{}
	_ = adapter.InstallService("/dummy/path")
	_ = adapter.UninstallService()
}

func TestLinuxAdapter_GetMachineIDAndDisplay(t *testing.T) {
	adapter := &LinuxAdapter{}

	id := adapter.GetMachineID()
	if id == "" {
		t.Logf("GetMachineID returned empty string on this host")
	}

	if adapter.LogFilePath() == "" {
		t.Errorf("Expected non-empty LogFilePath")
	}

	// Test setupDisplayEnvironment under both X11 and Wayland env vars
	t.Setenv("WAYLAND_DISPLAY", "wayland-0")
	if !adapter.setupDisplayEnvironment() {
		t.Errorf("Expected setupDisplayEnvironment to return true when WAYLAND_DISPLAY is set")
	}

	t.Setenv("WAYLAND_DISPLAY", "")
	t.Setenv("XDG_SESSION_TYPE", "x11")
	t.Setenv("DISPLAY", "")
	if adapter.setupDisplayEnvironment() {
		t.Errorf("Expected setupDisplayEnvironment to return false for X11 session")
	}

	// Test unknown computer action returns clean error without invoking GUI tools
	if _, err := adapter.ExecuteComputerAction("invalid_action_xyz", "", 0, 0); err == nil {
		t.Errorf("Expected error for unknown computer action")
	}
}

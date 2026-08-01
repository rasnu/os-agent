// Copyright © 2026 Rasnu LLC (rasnu.com). All rights reserved.
// Licensed under the Functional Source License, Version 1.1, ALv2 Future License (FSL-1.1-ALv2).

//go:build windows
// +build windows

package osadapter

import (
	"context"
	"strings"
	"testing"
)

func TestWindowsAdapter_GetMetrics(t *testing.T) {
	adapter := &WindowsAdapter{}

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

func TestWindowsAdapter_GetShellCmd(t *testing.T) {
	adapter := &WindowsAdapter{}

	cmd := adapter.GetShellCmd(context.Background(), "Write-Output 'hello'", "")
	if cmd == nil {
		t.Fatalf("Expected non-nil command")
	}

	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("Failed to execute powershell cmd: %v", err)
	}

	if !strings.Contains(string(out), "hello") {
		t.Errorf("Expected 'hello', got '%s'", string(out))
	}
}

func TestWindowsAdapter_StartPTY(t *testing.T) {
	adapter := &WindowsAdapter{}

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
}

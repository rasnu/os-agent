//go:build darwin
// +build darwin

package os_adapter

import (
	"context"
	"strings"
	"testing"
)

func TestMacOSAdapter_GetMetrics(t *testing.T) {
	adapter := &MacOSAdapter{}

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

func TestMacOSAdapter_GetShellCmd(t *testing.T) {
	adapter := &MacOSAdapter{}

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

func TestMacOSAdapter_StartPTY(t *testing.T) {
	adapter := &MacOSAdapter{}

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

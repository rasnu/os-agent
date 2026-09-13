//go:build linux
// +build linux

package os_adapter

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLinuxAdapter_VerifyBinary(t *testing.T) {
	adapter := &LinuxAdapter{}

	// Create a dummy executable script
	tempDir := t.TempDir()
	scriptPath := filepath.Join(tempDir, "dummy.sh")
	scriptContent := "#!/bin/sh\nexit 0\n"
	err := os.WriteFile(scriptPath, []byte(scriptContent), 0755)
	if err != nil {
		t.Fatalf("Failed to create dummy script: %v", err)
	}

	err = adapter.VerifyBinary(scriptPath)
	if err != nil {
		t.Errorf("VerifyBinary failed, expected success: %v", err)
	}

	// Test with a failing script
	failScriptPath := filepath.Join(tempDir, "fail.sh")
	failScriptContent := "#!/bin/sh\nexit 1\n"
	os.WriteFile(failScriptPath, []byte(failScriptContent), 0755)

	err = adapter.VerifyBinary(failScriptPath)
	if err == nil {
		t.Errorf("VerifyBinary succeeded on failing script, expected error")
	}
}

func TestLinuxAdapter_ReplaceBinary(t *testing.T) {
	adapter := &LinuxAdapter{}
	tempDir := t.TempDir()

	source := filepath.Join(tempDir, "source.bin")
	target := filepath.Join(tempDir, "target.bin")

	os.WriteFile(source, []byte("new data"), 0644)
	os.WriteFile(target, []byte("old data"), 0644)

	err := adapter.ReplaceBinary(source, target)
	if err != nil {
		t.Fatalf("ReplaceBinary failed: %v", err)
	}

	content, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("Failed to read target: %v", err)
	}
	if string(content) != "new data" {
		t.Errorf("Expected 'new data', got '%s'", string(content))
	}

	// Ensure source is gone
	if _, err := os.Stat(source); !os.IsNotExist(err) {
		t.Errorf("Expected source file to be removed after rename")
	}
}

func TestLinuxAdapter_LogFilePath(t *testing.T) {
	adapter := &LinuxAdapter{}
	path := adapter.LogFilePath()
	if path != "/var/log/rasnuos-agent.log" {
		t.Errorf("Expected /var/log/rasnuos-agent.log, got %s", path)
	}
}

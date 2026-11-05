// Copyright © 2026 Rasnu LLC (rasnu.com). All rights reserved.
// Licensed under the Functional Source License, Version 1.1, ALv2 Future License (FSL-1.1-ALv2).

//go:build linux
// +build linux

package osadapter

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLinuxAdapter_VerifyBinary(t *testing.T) {
	adapter := &LinuxAdapter{}

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

	failScriptPath := filepath.Join(tempDir, "fail.sh")
	failScriptContent := "#!/bin/sh\nexit 1\n"
	os.WriteFile(failScriptPath, []byte(failScriptContent), 0755)

	err = adapter.VerifyBinary(failScriptPath)
	if err == nil {
		t.Errorf("VerifyBinary succeeded on failing script, expected error")
	}

	downgradeScriptPath := filepath.Join(tempDir, "downgrade.sh")
	downgradeScriptContent := "#!/bin/sh\necho 'rasnuos-agent 0.5.0'\nexit 0\n"
	os.WriteFile(downgradeScriptPath, []byte(downgradeScriptContent), 0755)

	err = adapter.VerifyBinary(downgradeScriptPath)
	if err == nil {
		t.Errorf("VerifyBinary succeeded on downgrade script, expected downgrade rejection error")
	}

	upgradeScriptPath := filepath.Join(tempDir, "upgrade.sh")
	upgradeScriptContent := "#!/bin/sh\necho 'rasnuos-agent 2.0.0'\nexit 0\n"
	os.WriteFile(upgradeScriptPath, []byte(upgradeScriptContent), 0755)

	err = adapter.VerifyBinary(upgradeScriptPath)
	if err != nil {
		t.Errorf("VerifyBinary failed on valid upgrade script: %v", err)
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

	if _, err := os.Stat(source); !os.IsNotExist(err) {
		t.Errorf("Expected source file to be removed after rename")
	}

	bakContent, err := os.ReadFile(target + ".bak")
	if err != nil {
		t.Fatalf("Expected .bak rollback file to be preserved: %v", err)
	}
	if string(bakContent) != "old data" {
		t.Errorf("Expected .bak content 'old data', got '%s'", string(bakContent))
	}
}

func TestLinuxAdapter_LogFilePath(t *testing.T) {
	adapter := &LinuxAdapter{}
	path := adapter.LogFilePath()
	if path != "/var/log/rasnuos-agent.log" {
		t.Errorf("Expected /var/log/rasnuos-agent.log, got %s", path)
	}
}

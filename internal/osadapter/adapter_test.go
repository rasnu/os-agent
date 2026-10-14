// Copyright © 2026 Rasnu LLC (rasnu.com). All rights reserved.
// Licensed under the Functional Source License, Version 1.1, ALv2 Future License (FSL-1.1-ALv2).

package osadapter

import (
	"context"
	"os/exec"
	"testing"
)

type MockAdapter struct{}

func (m *MockAdapter) GetMetrics() (Metrics, []error) {
	return Metrics{}, nil
}

func (m *MockAdapter) GetShellCmd(ctx context.Context, script string, runAsUser string) *exec.Cmd {
	return nil
}

func (m *MockAdapter) StartPTY(cols, rows uint16) (PTY, error) {
	return nil, nil
}

func (m *MockAdapter) InstallService(executablePath string) error {
	return nil
}

func (m *MockAdapter) UninstallService() error {
	return nil
}

func (m *MockAdapter) VerifyBinary(path string) error {
	return nil
}

func (m *MockAdapter) ReplaceBinary(tempPath, currentPath string) error {
	return nil
}

func (m *MockAdapter) RestartAgent() error {
	return nil
}

func (m *MockAdapter) ManageService(action, service string) (string, error) {
	return "", nil
}

func (m *MockAdapter) KillProcess(pid string) (string, error) {
	return "", nil
}

func (m *MockAdapter) WriteCronJobs(content string) error {
	return nil
}

func (m *MockAdapter) LogFilePath() string {
	return "rasnuos-agent.log"
}

func (m *MockAdapter) PunchFirewallHole(port int) error {
	return nil
}

func (m *MockAdapter) CloseFirewallHole(port int) error {
	return nil
}

func (m *MockAdapter) ExecuteComputerAction(action, text string, x, y int) ([]byte, error) {
	return nil, nil
}

func (m *MockAdapter) GetMachineID() string {
	return "mock-machine-id"
}

func TestCurrentPanicsWhenNil(t *testing.T) {
	originalAdapter := currentAdapter
	defer func() { currentAdapter = originalAdapter }()

	currentAdapter = nil

	defer func() {
		if r := recover(); r == nil {
			t.Errorf("Expected Current() to panic when adapter is nil")
		}
	}()
	Current()
}

func TestSetAndGetCurrent(t *testing.T) {
	originalAdapter := currentAdapter
	defer func() { currentAdapter = originalAdapter }()

	mock := &MockAdapter{}
	SetAdapter(mock)

	if Current() != mock {
		t.Errorf("Current() did not return the set adapter")
	}
}

func TestMergeManagedCrontabPreservesUserEntries(t *testing.T) {
	existing := "0 3 * * * /usr/local/bin/backup.sh\n# BEGIN RASNUOS MANAGED CRON JOBS\n*/5 * * * * /old/job.sh\n# END RASNUOS MANAGED CRON JOBS\n15 4 * * * /usr/local/bin/cleanup.sh\n"
	updated := MergeManagedCrontab(existing, "*/10 * * * * /new/job.sh")

	if !containsLine(updated, "0 3 * * * /usr/local/bin/backup.sh") {
		t.Errorf("Expected backup.sh to be preserved, got:\n%s", updated)
	}
	if !containsLine(updated, "15 4 * * * /usr/local/bin/cleanup.sh") {
		t.Errorf("Expected cleanup.sh to be preserved, got:\n%s", updated)
	}
	if containsLine(updated, "*/5 * * * * /old/job.sh") {
		t.Errorf("Expected old managed job to be replaced, got:\n%s", updated)
	}
	if !containsLine(updated, "*/10 * * * * /new/job.sh") {
		t.Errorf("Expected new managed job to be present, got:\n%s", updated)
	}

	// Clearing managed content should still preserve user entries
	cleared := MergeManagedCrontab(updated, "")
	if !containsLine(cleared, "0 3 * * * /usr/local/bin/backup.sh") || !containsLine(cleared, "15 4 * * * /usr/local/bin/cleanup.sh") {
		t.Errorf("Expected user entries to remain when managed content is cleared, got:\n%s", cleared)
	}
	if containsLine(cleared, "RASNUOS MANAGED CRON JOBS") {
		t.Errorf("Expected managed block markers to be removed when empty, got:\n%s", cleared)
	}
}

func TestHasExecutableCronLines(t *testing.T) {
	if HasExecutableCronLines("") {
		t.Errorf("Expected empty string to have no executable lines")
	}
	if HasExecutableCronLines("# RasnuOS Auto-Generated Cron Jobs\n# Do not edit manually\n\n") {
		t.Errorf("Expected comment-only string to have no executable lines")
	}
	if !HasExecutableCronLines("# Header\n* * * * * root echo hi\n") {
		t.Errorf("Expected cron entry to be detected as executable")
	}
}

func containsLine(text, sub string) bool {
	for i := 0; i+len(sub) <= len(text); i++ {
		if text[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

package os_adapter

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

func TestCurrentPanicsWhenNil(t *testing.T) {
	// Ensure we start with nil for this test
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

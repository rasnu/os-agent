package main

import (
	"context"
	"os/exec"

	"rasnuone-agent/os_adapter"
)

type MockPTY struct {
	ReadFunc   func(p []byte) (n int, err error)
	WriteFunc  func(p []byte) (n int, err error)
	ResizeFunc func(cols, rows uint16) error
	CloseFunc  func() error
	KillFunc   func() error
}

func (m *MockPTY) Read(p []byte) (n int, err error) {
	if m.ReadFunc != nil {
		return m.ReadFunc(p)
	}
	return 0, nil
}

func (m *MockPTY) Write(p []byte) (n int, err error) {
	if m.WriteFunc != nil {
		return m.WriteFunc(p)
	}
	return len(p), nil
}

func (m *MockPTY) Resize(cols, rows uint16) error {
	if m.ResizeFunc != nil {
		return m.ResizeFunc(cols, rows)
	}
	return nil
}

func (m *MockPTY) Close() error {
	if m.CloseFunc != nil {
		return m.CloseFunc()
	}
	return nil
}

func (m *MockPTY) Kill() error {
	if m.KillFunc != nil {
		return m.KillFunc()
	}
	return nil
}

type MockOSAdapter struct {
	MockMetrics        os_adapter.Metrics
	MockMetricsErrs    []error
	MockShellOutput    string
	MockShellError     error
	MockPTYInstance    os_adapter.PTY
	MockPTYError       error
	MockInstallError   error
	MockUninstallError error

	// Callbacks for asserting behaviors
	VerifyBinaryFunc  func(path string) error
	ReplaceBinaryFunc func(tempPath, currentPath string) error
	RestartAgentFunc  func() error
	ManageServiceFunc func(action, service string) (string, error)
	KillProcessFunc   func(pid string) (string, error)
	StartPTYFunc      func(cols, rows uint16) (os_adapter.PTY, error)
	WriteCronJobsFunc func(content string) error
	GetShellCmdFunc   func(ctx context.Context, script string, runAsUser string) *exec.Cmd
}

func (m *MockOSAdapter) GetMetrics() (os_adapter.Metrics, []error) {
	return m.MockMetrics, m.MockMetricsErrs
}

func (m *MockOSAdapter) GetShellCmd(ctx context.Context, script string, runAsUser string) *exec.Cmd {
	if m.GetShellCmdFunc != nil {
		return m.GetShellCmdFunc(ctx, script, runAsUser)
	}
	return exec.CommandContext(ctx, "echo", m.MockShellOutput)
}

func (m *MockOSAdapter) StartPTY(cols, rows uint16) (os_adapter.PTY, error) {
	if m.StartPTYFunc != nil {
		return m.StartPTYFunc(cols, rows)
	}
	return m.MockPTYInstance, m.MockPTYError
}

func (m *MockOSAdapter) InstallService(executablePath string) error {
	return m.MockInstallError
}

func (m *MockOSAdapter) UninstallService() error {
	return m.MockUninstallError
}

func (m *MockOSAdapter) VerifyBinary(path string) error {
	if m.VerifyBinaryFunc != nil {
		return m.VerifyBinaryFunc(path)
	}
	return nil
}

func (m *MockOSAdapter) ReplaceBinary(tempPath, currentPath string) error {
	if m.ReplaceBinaryFunc != nil {
		return m.ReplaceBinaryFunc(tempPath, currentPath)
	}
	return nil
}

func (m *MockOSAdapter) RestartAgent() error {
	if m.RestartAgentFunc != nil {
		return m.RestartAgentFunc()
	}
	return nil
}

func (m *MockOSAdapter) ManageService(action, service string) (string, error) {
	if m.ManageServiceFunc != nil {
		return m.ManageServiceFunc(action, service)
	}
	return "", nil
}

func (m *MockOSAdapter) KillProcess(pid string) (string, error) {
	if m.KillProcessFunc != nil {
		return m.KillProcessFunc(pid)
	}
	return "", nil
}

func (m *MockOSAdapter) WriteCronJobs(content string) error {
	if m.WriteCronJobsFunc != nil {
		return m.WriteCronJobsFunc(content)
	}
	return nil
}

func (m *MockOSAdapter) LogFilePath() string {
	return "rasnuone-agent.log"
}

func SetupMockAdapter() *MockOSAdapter {
	mock := &MockOSAdapter{
		MockMetrics: os_adapter.Metrics{
			CPUUsage: 12.5,
		},
	}
	os_adapter.SetAdapter(mock)
	return mock
}

func init() {
	SetupMockAdapter()
}

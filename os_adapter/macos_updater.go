//go:build darwin
// +build darwin

package os_adapter

import (
	"os"
	"os/exec"
	"syscall"
)

func (a *MacOSAdapter) VerifyBinary(path string) error {
	return exec.Command(path, "--version").Run()
}

func (a *MacOSAdapter) ReplaceBinary(tempPath, currentPath string) error {
	return os.Rename(tempPath, currentPath)
}

func (a *MacOSAdapter) RestartAgent() error {
	proc, err := os.FindProcess(os.Getpid())
	if err == nil {
		proc.Signal(syscall.SIGTERM)
	} else {
		os.Exit(0)
	}
	return nil
}

func (a *MacOSAdapter) ManageService(action, service string) (string, error) {
	out, err := exec.Command("launchctl", action, service).CombinedOutput()
	return string(out), err
}

func (a *MacOSAdapter) KillProcess(pid string) (string, error) {
	out, err := exec.Command("kill", "-9", pid).CombinedOutput()
	return string(out), err
}

func (a *MacOSAdapter) WriteCronJobs(content string) error {
	// Not natively applicable via simple daemon restarts
	return nil
}

func (a *MacOSAdapter) LogFilePath() string {
	return "/var/log/rasnuone-agent.log"
}

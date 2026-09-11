//go:build linux
// +build linux

package os_adapter

import (
	"os"
	"os/exec"
	"syscall"
)

func (a *LinuxAdapter) VerifyBinary(path string) error {
	return exec.Command(path, "--version").Run()
}

func (a *LinuxAdapter) ReplaceBinary(tempPath, currentPath string) error {
	return os.Rename(tempPath, currentPath)
}

func (a *LinuxAdapter) RestartAgent() error {
	proc, err := os.FindProcess(os.Getpid())
	if err == nil {
		proc.Signal(syscall.SIGTERM)
	} else {
		os.Exit(0)
	}
	return nil
}

func (a *LinuxAdapter) ManageService(action, service string) (string, error) {
	out, err := exec.Command("systemctl", action, service).CombinedOutput()
	return string(out), err
}

func (a *LinuxAdapter) KillProcess(pid string) (string, error) {
	out, err := exec.Command("kill", "-9", pid).CombinedOutput()
	return string(out), err
}

func (a *LinuxAdapter) WriteCronJobs(content string) error {
	err := os.WriteFile("/etc/cron.d/rasnuone-jobs", []byte(content), 0644)
	if err != nil {
		return err
	}
	return exec.Command("systemctl", "restart", "cron").Run()
}

func (a *LinuxAdapter) LogFilePath() string {
	return "/var/log/rasnuone-agent.log"
}

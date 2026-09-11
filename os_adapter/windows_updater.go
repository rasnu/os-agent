//go:build windows
// +build windows

package os_adapter

import (
	"fmt"
	"os"
	"os/exec"
)

func (a *WindowsAdapter) VerifyBinary(path string) error {
	return exec.Command(path, "--version").Run()
}

func (a *WindowsAdapter) ReplaceBinary(tempPath, currentPath string) error {
	oldPath := currentPath + ".old"
	os.Remove(oldPath)
	err := os.Rename(currentPath, oldPath)
	if err != nil {
		return err
	}
	return os.Rename(tempPath, currentPath)
}

func (a *WindowsAdapter) RestartAgent() error {
	cmd := exec.Command("powershell", "-NoProfile", "-WindowStyle", "Hidden", "-Command", "Start-Sleep -Seconds 2; Restart-Service -Name RasnuCloudAgent")
	if err := cmd.Start(); err != nil {
		os.Exit(0)
	}
	os.Exit(0)
	return nil
}

func (a *WindowsAdapter) ManageService(action, service string) (string, error) {
	psAction := "Restart-Service"
	if action == "start" {
		psAction = "Start-Service"
	} else if action == "stop" {
		psAction = "Stop-Service"
	}
	out, err := exec.Command("powershell", "-NoProfile", "-Command", fmt.Sprintf("%s -Name %s", psAction, service)).CombinedOutput()
	return string(out), err
}

func (a *WindowsAdapter) KillProcess(pid string) (string, error) {
	out, err := exec.Command("taskkill", "/F", "/PID", pid).CombinedOutput()
	return string(out), err
}

func (a *WindowsAdapter) WriteCronJobs(content string) error {
	// Task Scheduler handles this natively
	return nil
}

func (a *WindowsAdapter) LogFilePath() string {
	return `C:\ProgramData\RasnuOne\Agent\rasnuone-agent.log`
}

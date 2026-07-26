// Copyright © 2026 Rasnu LLC (rasnu.com). All rights reserved.
// Licensed under the Functional Source License, Version 1.1, ALv2 Future License (FSL-1.1-ALv2).

//go:build linux
// +build linux

package osadapter

import (
	"os"
	"os/exec"
	"syscall"
)

func (a *LinuxAdapter) VerifyBinary(path string) error {
	return exec.Command(path, "--version").Run()
}

func (a *LinuxAdapter) ReplaceBinary(tempPath, currentPath string) error {
	backupPath := currentPath + ".bak"
	if origData, err := os.ReadFile(currentPath); err == nil {
		_ = os.WriteFile(backupPath, origData, 0755)
	}
	if err := os.Rename(tempPath, currentPath); err != nil {
		if _, statErr := os.Stat(backupPath); statErr == nil {
			_ = os.Rename(backupPath, currentPath)
		}
		return err
	}
	return nil
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
	err := os.WriteFile("/etc/cron.d/rasnuos-jobs", []byte(content), 0644)
	if err != nil {
		return err
	}
	return exec.Command("systemctl", "restart", "cron").Run()
}

func (a *LinuxAdapter) LogFilePath() string {
	return "/var/log/rasnuos-agent.log"
}

// Copyright © 2026 Rasnu LLC (rasnu.com). All rights reserved.
// Licensed under the Functional Source License, Version 1.1, ALv2 Future License (FSL-1.1-ALv2).

//go:build darwin
// +build darwin

package osadapter

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
)

func (a *MacOSAdapter) VerifyBinary(path string) error {
	return exec.Command(path, "--version").Run()
}

func (a *MacOSAdapter) ReplaceBinary(tempPath, currentPath string) error {
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
	// Prefer Homebrew services when available, falling back to launchctl for system daemons.
	if action == "restart" {
		exec.Command("brew", "services", "stop", service).Run()
		out, err := exec.Command("brew", "services", "start", service).CombinedOutput()
		if err == nil {
			return string(out), nil
		}
	} else {
		out, err := exec.Command("brew", "services", action, service).CombinedOutput()
		if err == nil {
			return string(out), nil
		}
	}

	launchctlAction := "kickstart"
	if action == "stop" {
		launchctlAction = "kill"
	}
	out, err := exec.Command("launchctl", launchctlAction, service).CombinedOutput()
	return string(out), err
}

func (a *MacOSAdapter) KillProcess(pid string) (string, error) {
	out, err := exec.Command("kill", "-9", pid).CombinedOutput()
	return string(out), err
}

func (a *MacOSAdapter) WriteCronJobs(content string) error {
	existingOut, _ := exec.Command("crontab", "-l").Output()
	finalCron := MergeManagedCrontab(string(existingOut), content)

	cmd := exec.Command("crontab", "-")
	cmd.Stdin = strings.NewReader(finalCron)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("WriteCronJobs: crontab error: %w — %s", err, string(out))
	}
	return nil
}

func (a *MacOSAdapter) LogFilePath() string {
	return "/var/log/rasnuos-agent.log"
}

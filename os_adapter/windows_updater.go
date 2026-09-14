//go:build windows
// +build windows

package os_adapter

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/shirou/gopsutil/v3/disk"
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
	cmd := exec.Command("powershell", "-NoProfile", "-WindowStyle", "Hidden", "-Command", "Start-Sleep -Seconds 2; Restart-Service -Name RasnuOSAgent")
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
	// On Windows, cron-style scheduling is handled via Task Scheduler.
	// We write the content as a PowerShell script and register a scheduled task
	// that runs it every minute (matching the cron minimum granularity).
	scriptPath := `C:\ProgramData\RasnuOS\cron_job.ps1`
	if err := os.WriteFile(scriptPath, []byte(content), 0600); err != nil {
		return fmt.Errorf("WriteCronJobs: failed to write script: %w", err)
	}
	psCmd := fmt.Sprintf(
		`$action = New-ScheduledTaskAction -Execute 'powershell.exe' -Argument '-NoProfile -NonInteractive -File "%s"';
$trigger = New-ScheduledTaskTrigger -RepetitionInterval (New-TimeSpan -Minutes 1) -Once -At (Get-Date);
Register-ScheduledTask -TaskName 'RasnuOS_CronJob' -Action $action -Trigger $trigger -RunLevel Highest -Force | Out-Null`,
		scriptPath,
	)
	out, err := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", psCmd).CombinedOutput()
	if err != nil {
		return fmt.Errorf("WriteCronJobs: task scheduler error: %w — %s", err, string(out))
	}
	return nil
}

func (a *WindowsAdapter) LogFilePath() string {
	return `C:\ProgramData\RasnuOS\Agent\rasnuos-agent.log`
}

// getDiskUsage enumerates all fixed drives and returns the average usage across them.
// This replaces the windows.go stub that only checked C:.
func getDiskUsage() (float64, error) {
	partitions, err := disk.Partitions(false)
	if err != nil || len(partitions) == 0 {
		d, err2 := disk.Usage("C:")
		if err2 != nil {
			return 0.0, err2
		}
		return d.UsedPercent, nil
	}
	var total, sum float64
	for _, p := range partitions {
		d, err := disk.Usage(p.Mountpoint)
		if err != nil {
			continue
		}
		sum += d.UsedPercent
		total++
	}
	if total == 0 {
		return 0.0, nil
	}
	return sum / total, nil
}

// getServiceStatuses checks Windows-native services instead of Linux daemon names.
func getServiceStatuses() ([]ServiceStatus, error) {
	servicesToCheck := []string{
		// Web servers
		"W3SVC", "nginx",
		// Databases
		"MSSQLSERVER", "MySQL80", "MySQL", "postgresql-x64-15", "postgresql-x64-14", "MongoDB",
		// Containers
		"docker", "com.docker.service",
		// Remote management
		"WinRM", "TermService",
		// Windows Update
		"wuauserv",
		// Security
		"WinDefend", "MpsSvc",
		// Task Scheduler
		"Schedule",
	}
	statuses := make([]ServiceStatus, 0)

	for _, srv := range servicesToCheck {
		statusStr := "inactive"
		cmd := fmt.Sprintf(`(Get-Service '%s' -ErrorAction SilentlyContinue).Status`, srv)
		out, err := exec.Command("powershell", "-NoProfile", "-Command", cmd).Output()
		if err == nil && strings.Contains(strings.ToLower(string(out)), "running") {
			statusStr = "active"
		} else if err == nil && strings.TrimSpace(string(out)) != "" {
			statusStr = strings.ToLower(strings.TrimSpace(string(out)))
		} else {
			// Service not installed — skip it from the list entirely.
			continue
		}
		statuses = append(statuses, ServiceStatus{
			Name:   srv,
			Status: statusStr,
		})
	}

	return statuses, nil
}

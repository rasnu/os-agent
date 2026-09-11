//go:build darwin
// +build darwin

package os_adapter

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/creack/pty"
	"github.com/shirou/gopsutil/v3/cpu"
	"github.com/shirou/gopsutil/v3/disk"
	"github.com/shirou/gopsutil/v3/load"
	"github.com/shirou/gopsutil/v3/mem"
	"github.com/shirou/gopsutil/v3/process"
)

func init() {
	SetAdapter(&MacOSAdapter{})
}

type MacOSAdapter struct{}

func (a *MacOSAdapter) GetMetrics() (Metrics, []error) {
	var errs []error
	var metrics Metrics

	if val, err := getCPUUsage(); err != nil {
		errs = append(errs, fmt.Errorf("cpu: %w", err))
	} else {
		metrics.CPUUsage = val
	}

	if val, err := getRAMUsage(); err != nil {
		errs = append(errs, fmt.Errorf("ram: %w", err))
	} else {
		metrics.RAMUsage = val
	}

	if val, err := getDiskUsage(); err != nil {
		errs = append(errs, fmt.Errorf("disk: %w", err))
	} else {
		metrics.DiskUsage = val
	}

	// MacOS swap usage
	metrics.SwapUsage = 0.0

	if val, err := getLoadAvg(); err != nil {
		errs = append(errs, fmt.Errorf("load: %w", err))
	} else {
		metrics.LoadAvg = val
	}

	if processes, err := getTopProcesses(); err != nil {
		errs = append(errs, fmt.Errorf("processes: %w", err))
	} else {
		metrics.Processes = processes
	}

	if services, err := getServiceStatuses(); err != nil {
		errs = append(errs, fmt.Errorf("services: %w", err))
	} else {
		metrics.Services = services
	}

	return metrics, errs
}

func (a *MacOSAdapter) GetShellCmd(ctx context.Context, script string, runAsUser string) *exec.Cmd {
	if runAsUser != "" {
		// macOS su supports similar syntax to linux
		return exec.CommandContext(ctx, "su", "-s", "/bin/bash", runAsUser, "-c", script)
	}
	return exec.CommandContext(ctx, "bash", "-c", script)
}

func (a *MacOSAdapter) StartPTY(cols, rows uint16) (PTY, error) {
	cmd := exec.Command("bash")
	cmd.Env = append(os.Environ(), "TERM=xterm-256color")

	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{
		Rows: rows,
		Cols: cols,
	})
	if err != nil {
		return nil, err
	}

	return &MacOSPTY{
		ptyFile: ptmx,
		cmd:     cmd,
	}, nil
}

func (a *MacOSAdapter) InstallService(executablePath string) error {
	// macOS launchd service
	plistContent := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>com.rasnucloud.agent</string>
    <key>ProgramArguments</key>
    <array>
        <string>%s</string>
    </array>
    <key>RunAtLoad</key>
    <true/>
    <key>KeepAlive</key>
    <true/>
</dict>
</plist>`, executablePath)

	plistPath := "/Library/LaunchDaemons/com.rasnucloud.agent.plist"
	err := os.WriteFile(plistPath, []byte(plistContent), 0644)
	if err != nil {
		return err
	}

	return exec.Command("launchctl", "load", "-w", plistPath).Run()
}

func (a *MacOSAdapter) UninstallService() error {
	plistPath := "/Library/LaunchDaemons/com.rasnucloud.agent.plist"
	exec.Command("launchctl", "unload", "-w", plistPath).Run()
	return os.Remove(plistPath)
}

type MacOSPTY struct {
	ptyFile *os.File
	cmd     *exec.Cmd
}

func (l *MacOSPTY) Read(p []byte) (n int, err error) {
	return l.ptyFile.Read(p)
}

func (l *MacOSPTY) Write(p []byte) (n int, err error) {
	return l.ptyFile.Write(p)
}

func (l *MacOSPTY) Resize(cols, rows uint16) error {
	return pty.Setsize(l.ptyFile, &pty.Winsize{
		Rows: rows,
		Cols: cols,
	})
}

func (l *MacOSPTY) Close() error {
	return l.ptyFile.Close()
}

func (l *MacOSPTY) Kill() error {
	if l.cmd != nil && l.cmd.Process != nil {
		l.cmd.Process.Signal(syscall.SIGTERM)

		go func(p *os.Process) {
			time.Sleep(2 * time.Second)
			p.Signal(syscall.SIGKILL)
		}(l.cmd.Process)

		l.cmd.Wait()
	}
	return nil
}

// ---------------------------------------------------------
// Helper functions for macOS
// ---------------------------------------------------------

func getCPUUsage() (float64, error) {
	percentages, err := cpu.Percent(0, false)
	if err != nil || len(percentages) == 0 {
		return 0.0, err
	}
	return percentages[0], nil
}

func getRAMUsage() (float64, error) {
	v, err := mem.VirtualMemory()
	if err != nil {
		return 0.0, err
	}
	return v.UsedPercent, nil
}

func getDiskUsage() (float64, error) {
	d, err := disk.Usage("/")
	if err != nil {
		return 0.0, err
	}
	return d.UsedPercent, nil
}

func getLoadAvg() (string, error) {
	l, err := load.Avg()
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%.2f, %.2f, %.2f", l.Load1, l.Load5, l.Load15), nil
}

func getTopProcesses() ([]Process, error) {
	var result []Process
	procs, err := process.Processes()
	if err != nil {
		return result, err
	}

	type procInfo struct {
		Process Process
		CpuPct  float64
	}
	var infos []procInfo

	for _, p := range procs {
		cpuPct, err := p.CPUPercent()
		if err != nil || cpuPct == 0 {
			continue
		}
		memPct, _ := p.MemoryPercent()
		user, _ := p.Username()
		cmd, _ := p.Cmdline()
		if cmd == "" {
			cmd, _ = p.Name()
		}

		infos = append(infos, procInfo{
			Process: Process{
				PID:     fmt.Sprintf("%d", p.Pid),
				User:    user,
				CPU:     fmt.Sprintf("%.1f", cpuPct),
				Memory:  fmt.Sprintf("%.1f", memPct),
				Command: cmd,
			},
			CpuPct: cpuPct,
		})
	}

	sort.Slice(infos, func(i, j int) bool {
		return infos[i].CpuPct > infos[j].CpuPct
	})

	limit := 20
	if len(infos) < 20 {
		limit = len(infos)
	}

	for i := 0; i < limit; i++ {
		result = append(result, infos[i].Process)
	}
	return result, nil
}

func getServiceStatuses() ([]ServiceStatus, error) {
	servicesToCheck := []string{"nginx", "mysql", "docker", "php"}
	statuses := make([]ServiceStatus, 0)

	out, _ := exec.Command("launchctl", "list").Output()
	outputStr := string(out)

	for _, srv := range servicesToCheck {
		status := "inactive"
		if strings.Contains(strings.ToLower(outputStr), srv) {
			status = "active"
		}
		statuses = append(statuses, ServiceStatus{
			Name:   srv,
			Status: status,
		})
	}
	return statuses, nil
}

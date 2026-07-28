// Copyright © 2026 Rasnu LLC (rasnu.com). All rights reserved.
// Licensed under the Functional Source License, Version 1.1, ALv2 Future License (FSL-1.1-ALv2).

//go:build darwin
// +build darwin

package osadapter

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
	net_util "github.com/shirou/gopsutil/v3/net"
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

	if swapMem, err := mem.SwapMemory(); err != nil {
		errs = append(errs, fmt.Errorf("swap: %w", err))
	} else if swapMem.Total > 0 {
		metrics.SwapUsage = swapMem.UsedPercent
	}

	if l_str, l_5, err := getLoadAvg(); err != nil {
		errs = append(errs, fmt.Errorf("load: %w", err))
	} else {
		metrics.LoadAvg = l_str
		metrics.Load5 = l_5
	}
	metrics.CPUCores, _ = cpu.Counts(true)

	if rx, tx, err := getMacNetworkIO(); err != nil {
		errs = append(errs, fmt.Errorf("network_io: %w", err))
	} else {
		metrics.NetworkRX = rx
		metrics.NetworkTX = tx
	}

	if read, write, err := getMacDiskIO(); err != nil {
		errs = append(errs, fmt.Errorf("disk_io: %w", err))
	} else {
		metrics.DiskRead = read
		metrics.DiskWrite = write
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

	metrics.OOMKills = getOOMKills()

	return metrics, errs
}

func getOOMKills() []string {
	var ooms []string
	// Query the macOS unified log for Jetsam memory pressure terminations over the last 5 minutes.
	cmdStr := `log show --predicate "eventMessage contains 'Jetsam'" --last 5m 2>/dev/null | grep Jetsam | tail -n 5`
	out, err := exec.Command("sh", "-c", cmdStr).Output()
	if err == nil && len(out) > 0 {
		lines := strings.Split(strings.TrimSpace(string(out)), "\n")
		for _, line := range lines {
			if strings.TrimSpace(line) != "" {
				ooms = append(ooms, strings.TrimSpace(line))
			}
		}
	}
	return ooms
}

func (a *MacOSAdapter) GetShellCmd(ctx context.Context, script string, runAsUser string) *exec.Cmd {
	var cmd *exec.Cmd
	if runAsUser != "" {
		cmd = exec.CommandContext(ctx, "su", "-s", "/bin/bash", runAsUser, "-c", script)
	} else {
		cmd = exec.CommandContext(ctx, "bash", "-c", script)
	}

	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}

	return cmd
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
	plistContent := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>com.rasnuos.agent</string>
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

	plistPath := "/Library/LaunchDaemons/com.rasnuos.agent.plist"
	err := os.WriteFile(plistPath, []byte(plistContent), 0644)
	if err != nil {
		return err
	}

	return exec.Command("launchctl", "load", "-w", plistPath).Run()
}

func (a *MacOSAdapter) UninstallService() error {
	plistPath := "/Library/LaunchDaemons/com.rasnuos.agent.plist"
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

func getLoadAvg() (string, float64, error) {
	l, err := load.Avg()
	if err != nil {
		return "", 0.0, err
	}
	return fmt.Sprintf("%.2f, %.2f, %.2f", l.Load1, l.Load5, l.Load15), l.Load5, nil
}

func getMacNetworkIO() (uint64, uint64, error) {
	counters, err := net_util.IOCounters(false)
	if err != nil || len(counters) == 0 {
		return 0, 0, err
	}
	return counters[0].BytesRecv, counters[0].BytesSent, nil
}

func getMacDiskIO() (uint64, uint64, error) {
	counters, err := disk.IOCounters()
	if err != nil {
		return 0, 0, err
	}
	var read, write uint64
	for _, c := range counters {
		read += c.ReadBytes / 1024
		write += c.WriteBytes / 1024
	}
	return read, write, nil
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

	// Report non-Apple launchctl jobs that exited with a non-zero status code as failed.
	cmdStr := `launchctl list | awk '$1 == "-" && $2 != "0" && $3 !~ /^com\.apple\./ {print $3}'`
	failedOut, err := exec.Command("sh", "-c", cmdStr).Output()
	if err == nil && len(failedOut) > 0 {
		lines := strings.Split(strings.TrimSpace(string(failedOut)), "\n")
		for _, line := range lines {
			failedSrv := strings.TrimSpace(line)
			if failedSrv != "" {
				alreadyAdded := false
				for _, s := range statuses {
					if s.Name == failedSrv {
						alreadyAdded = true
						break
					}
				}
				if !alreadyAdded {
					statuses = append(statuses, ServiceStatus{
						Name:   failedSrv,
						Status: "failed",
					})
				}
			}
		}
	}

	return statuses, nil
}

func (a *MacOSAdapter) PunchFirewallHole(port int) error {
	// macOS pf requires reloading the full anchor ruleset rather than inserting individual port rules.
	return nil
}

func (a *MacOSAdapter) CloseFirewallHole(port int) error {
	return nil
}

func (a *MacOSAdapter) ExecuteComputerAction(action, text string, x, y int) ([]byte, error) {
	switch action {
	case "open_application":
		app := text
		switch strings.ToLower(text) {
		case "terminal":
			app = "Terminal.app"
		case "file manager", "explorer":
			app = "Finder.app"
		case "browser":
			app = "Safari.app"
		}

		var cmd *exec.Cmd
		if app == "Finder.app" && (strings.ToLower(text) == "file manager" || strings.ToLower(text) == "explorer") {
			homeDir, _ := os.UserHomeDir()
			cmd = exec.Command("open", homeDir)
		} else if app == "Safari.app" && strings.ToLower(text) == "browser" {
			cmd = exec.Command("open", "https://google.com")
		} else {
			cmd = exec.Command("open", "-a", app)
		}

		err := cmd.Start()
		if err != nil {
			return nil, fmt.Errorf("failed to open application %s: %w", text, err)
		}
		go func() {
			cmd.Wait()
		}()

		return []byte(fmt.Sprintf("Launched %s successfully", text)), nil
	case "list_windows":
		script := `
set windowList to ""
tell application "System Events"
	repeat with p in (every process whose visible is true)
		set wNames to name of every window of p
		repeat with wName in wNames
			if wName is not missing value and wName is not "" then
				set windowList to windowList & wName & linefeed
			end if
		end repeat
	end repeat
end tell
return windowList`
		return exec.Command("osascript", "-e", script).CombinedOutput()
	case "activate_window":
		safeText := strings.ReplaceAll(text, `\`, `\\`)
		safeText = strings.ReplaceAll(safeText, `"`, `\"`)
		script := fmt.Sprintf(`
tell application "System Events"
	set targetTitle to "%s"
	repeat with p in (every process whose visible is true)
		if exists (window 1 of p whose name is targetTitle) then
			set frontmost of p to true
			tell p to perform action "AXRaise" of (window 1 whose name is targetTitle)
			exit repeat
		end if
	end repeat
end tell`, safeText)
		return exec.Command("osascript", "-e", script).CombinedOutput()
	case "close_window":
		safeText := strings.ReplaceAll(text, `\`, `\\`)
		safeText = strings.ReplaceAll(safeText, `"`, `\"`)
		script := fmt.Sprintf(`
tell application "System Events"
	set targetTitle to "%s"
	repeat with p in (every process whose visible is true)
		if exists (window 1 of p whose name is targetTitle) then
			tell p to click button 1 of (window 1 whose name is targetTitle)
			exit repeat
		end if
	end repeat
end tell`, safeText)
		return exec.Command("osascript", "-e", script).CombinedOutput()
	case "move_window":
		safeText := strings.ReplaceAll(text, `\`, `\\`)
		safeText = strings.ReplaceAll(safeText, `"`, `\"`)
		script := fmt.Sprintf(`
tell application "System Events"
	set targetTitle to "%s"
	repeat with p in (every process whose visible is true)
		if exists (window 1 of p whose name is targetTitle) then
			set position of (window 1 of p whose name is targetTitle) to {%d, %d}
			exit repeat
		end if
	end repeat
end tell`, safeText, x, y)
		return exec.Command("osascript", "-e", script).CombinedOutput()
	case "resize_window":
		safeText := strings.ReplaceAll(text, `\`, `\\`)
		safeText = strings.ReplaceAll(safeText, `"`, `\"`)
		script := fmt.Sprintf(`
tell application "System Events"
	set targetTitle to "%s"
	repeat with p in (every process whose visible is true)
		if exists (window 1 of p whose name is targetTitle) then
			set size of (window 1 of p whose name is targetTitle) to {%d, %d}
			exit repeat
		end if
	end repeat
end tell`, safeText, x, y)
		return exec.Command("osascript", "-e", script).CombinedOutput()
	case "mouse_move":
		return exec.Command("cliclick", fmt.Sprintf("m:%d,%d", x, y)).CombinedOutput()
	case "left_click":
		return exec.Command("cliclick", "c:.").CombinedOutput()
	case "right_click":
		return exec.Command("cliclick", "rc:.").CombinedOutput()
	case "double_click":
		return exec.Command("cliclick", "dc:.").CombinedOutput()
	case "type":
		safeText := strings.ReplaceAll(text, `\`, `\\`)
		safeText = strings.ReplaceAll(safeText, `"`, `\"`)
		script := fmt.Sprintf(`tell application "System Events" to keystroke "%s"`, safeText)
		return exec.Command("osascript", "-e", script).CombinedOutput()
	case "key":
		return exec.Command("cliclick", fmt.Sprintf("kp:%s", text)).CombinedOutput()
	case "screenshot":
		tmpFile := fmt.Sprintf("/tmp/rasnuos_screenshot_%d.png", time.Now().UnixNano())
		defer os.Remove(tmpFile)
		err := exec.Command("screencapture", "-x", tmpFile).Run()
		if err != nil {
			return nil, fmt.Errorf("failed to take screenshot: %w", err)
		}
		data, err := os.ReadFile(tmpFile)
		return data, err
	default:
		return nil, fmt.Errorf("unknown computer action: %s", action)
	}
}

func (a *MacOSAdapter) GetMachineID() string {
	out, err := exec.Command("ioreg", "-rd1", "-c", "IOPlatformExpertDevice").Output()
	if err == nil {
		for _, line := range strings.Split(string(out), "\n") {
			if strings.Contains(line, "IOPlatformUUID") {
				parts := strings.Split(line, "=")
				if len(parts) == 2 {
					return strings.Trim(strings.TrimSpace(parts[1]), "\"")
				}
			}
		}
	}
	return ""
}

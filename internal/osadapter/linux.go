// Copyright © 2026 Rasnu LLC (rasnu.com). All rights reserved.
// Licensed under the Functional Source License, Version 1.1, ALv2 Future License (FSL-1.1-ALv2).

//go:build linux
// +build linux

package osadapter

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/creack/pty"
	"github.com/shirou/gopsutil/v3/cpu"
	"github.com/shirou/gopsutil/v3/disk"
	"github.com/shirou/gopsutil/v3/load"
	"github.com/shirou/gopsutil/v3/mem"
	"github.com/shirou/gopsutil/v3/net"
	"github.com/shirou/gopsutil/v3/process"
)

func init() {
	SetAdapter(&LinuxAdapter{})
}

type LinuxAdapter struct{}

func (a *LinuxAdapter) GetMetrics() (Metrics, []error) {
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

	if val, err := getSwapUsage(); err != nil {
		errs = append(errs, fmt.Errorf("swap: %w", err))
	} else {
		metrics.SwapUsage = val
	}

	metrics.IOWait = getIOWait()

	if l_str, l_5, err := getLoadAvg(); err != nil {
		errs = append(errs, fmt.Errorf("load: %w", err))
	} else {
		metrics.LoadAvg = l_str
		metrics.Load5 = l_5
	}
	metrics.CPUCores, _ = cpu.Counts(true)

	if rx, tx, err := getNetworkIO(); err != nil {
		errs = append(errs, fmt.Errorf("network: %w", err))
	} else {
		metrics.NetworkRX = rx
		metrics.NetworkTX = tx
	}

	if read, write, err := getDiskIO(); err != nil {
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
	out, err := exec.Command("sh", "-c", "dmesg -T | grep -i -E 'Out of memory: Killed process|oom-killer' | tail -n 5").Output()
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

func (a *LinuxAdapter) GetShellCmd(ctx context.Context, script string, runAsUser string) *exec.Cmd {
	var cmd *exec.Cmd
	if runAsUser != "" {
		// Pass -s /bin/bash so service accounts configured with /usr/sbin/nologin or /bin/false can still execute commands.
		cmd = exec.CommandContext(ctx, "su", "-s", "/bin/bash", runAsUser, "-c", script)
	} else {
		cmd = exec.CommandContext(ctx, "bash", "-c", script)
	}

	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		// Kill the entire process group (-pgid) so timed-out commands do not leave orphaned child processes.
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}

	return cmd
}

func (a *LinuxAdapter) StartPTY(cols, rows uint16) (PTY, error) {
	cmd := exec.Command("bash")
	cmd.Env = append(os.Environ(), "TERM=xterm-256color")

	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{
		Rows: rows,
		Cols: cols,
	})
	if err != nil {
		return nil, err
	}

	return &LinuxPTY{
		ptyFile: ptmx,
		cmd:     cmd,
	}, nil
}

func (a *LinuxAdapter) InstallService(executablePath string) error {
	serviceContent := fmt.Sprintf(`[Unit]
Description=RasnuOS Agent
After=network.target

[Service]
Type=simple
ExecStart=%s
Restart=always
RestartSec=10
LimitNOFILE=65535

[Install]
WantedBy=multi-user.target
`, executablePath)

	err := os.WriteFile("/etc/systemd/system/rasnuos-agent.service", []byte(serviceContent), 0644)
	if err != nil {
		return err
	}

	exec.Command("systemctl", "daemon-reload").Run()
	exec.Command("systemctl", "enable", "rasnuos-agent").Run()
	return exec.Command("systemctl", "start", "rasnuos-agent").Run()
}

func (a *LinuxAdapter) UninstallService() error {
	exec.Command("systemctl", "stop", "rasnuos-agent").Run()
	exec.Command("systemctl", "disable", "rasnuos-agent").Run()
	err := os.Remove("/etc/systemd/system/rasnuos-agent.service")
	if err != nil {
		return err
	}
	return exec.Command("systemctl", "daemon-reload").Run()
}

type LinuxPTY struct {
	ptyFile *os.File
	cmd     *exec.Cmd
}

func (l *LinuxPTY) Read(p []byte) (n int, err error) {
	return l.ptyFile.Read(p)
}

func (l *LinuxPTY) Write(p []byte) (n int, err error) {
	return l.ptyFile.Write(p)
}

func (l *LinuxPTY) Resize(cols, rows uint16) error {
	return pty.Setsize(l.ptyFile, &pty.Winsize{
		Rows: rows,
		Cols: cols,
	})
}

func (l *LinuxPTY) Close() error {
	return l.ptyFile.Close()
}

func (l *LinuxPTY) Kill() error {
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

func getSwapUsage() (float64, error) {
	s, err := mem.SwapMemory()
	if err != nil {
		return 0.0, err
	}
	if s.Total == 0 {
		return 0.0, nil
	}
	return s.UsedPercent, nil
}

func getIOWait() float64 {
	// Sample vmstat twice over 1s; the first row is the boot-time average, so read column 16 (wa) from the second row.
	out, err := exec.Command("sh", "-c", "vmstat 1 2 | tail -1 | awk '{print $16}'").Output()
	if err == nil {
		val := strings.TrimSpace(string(out))
		var iowait float64
		fmt.Sscanf(val, "%f", &iowait)
		return iowait
	}
	return 0.0
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

func getNetworkIO() (uint64, uint64, error) {
	counters, err := net.IOCounters(false)
	if err != nil || len(counters) == 0 {
		return 0, 0, err
	}
	return counters[0].BytesRecv, counters[0].BytesSent, nil
}

func getDiskIO() (uint64, uint64, error) {
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
	servicesToCheck := []string{
		"nginx", "apache2", "caddy", "litespeed",
		"mysql", "mariadb", "postgresql", "mongod", "redis-server", "memcached",
		"docker", "containerd", "supervisor", "pm2", "php-fpm",
		"php8.0-fpm", "php8.1-fpm", "php8.2-fpm", "php8.3-fpm", "php8.4-fpm",
		"ufw", "fail2ban", "ssh", "cron", "crond",
	}

	statuses := make([]ServiceStatus, 0)
	out, err := exec.Command("systemctl", "list-units", "--type=service", "--all", "--no-pager", "--no-legend").Output()
	if err != nil {
		return statuses, err
	}

	outputStr := string(out)

	for _, srv := range servicesToCheck {
		srvName := srv + ".service"
		if strings.Contains(outputStr, srvName) {
			statusOut, _ := exec.Command("systemctl", "is-active", srv).Output()
			status := strings.TrimSpace(string(statusOut))
			if status == "" {
				status = "unknown"
			}
			statuses = append(statuses, ServiceStatus{
				Name:   srv,
				Status: status,
			})
		}
	}

	// Include any systemd unit in a failed state even if it is not in the default check list.
	failedOut, err := exec.Command("systemctl", "list-units", "--type=service", "--state=failed", "--no-pager", "--no-legend").Output()
	if err == nil && len(failedOut) > 0 {
		lines := strings.Split(strings.TrimSpace(string(failedOut)), "\n")
		for _, line := range lines {
			parts := strings.Fields(line)
			if len(parts) > 0 {
				failedSrv := strings.TrimSuffix(parts[0], ".service")
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

func (a *LinuxAdapter) PunchFirewallHole(port int) error {
	portStr := fmt.Sprintf("%d", port)
	exec.Command("ufw", "allow", fmt.Sprintf("%s/tcp", portStr)).Run()
	exec.Command("iptables", "-I", "INPUT", "-p", "tcp", "--dport", portStr, "-j", "ACCEPT").Run()
	return nil
}

func (a *LinuxAdapter) CloseFirewallHole(port int) error {
	portStr := fmt.Sprintf("%d", port)
	exec.Command("ufw", "delete", "allow", fmt.Sprintf("%s/tcp", portStr)).Run()
	exec.Command("iptables", "-D", "INPUT", "-p", "tcp", "--dport", portStr, "-j", "ACCEPT").Run()
	return nil
}

func (a *LinuxAdapter) ExecuteComputerAction(action, text string, x, y int) ([]byte, error) {
	isWayland := a.setupDisplayEnvironment()

	switch action {
	case "open_application":
		app := text
		switch strings.ToLower(text) {
		case "terminal":
			app = "x-terminal-emulator"
			if err := exec.Command("which", app).Run(); err != nil {
				app = "gnome-terminal"
			}
		case "file manager", "explorer":
			app = "xdg-open"
		case "browser":
			app = "x-www-browser"
			if err := exec.Command("which", app).Run(); err != nil {
				app = "xdg-open"
			}
		}

		var cmd *exec.Cmd
		if app == "xdg-open" && (strings.ToLower(text) == "file manager" || strings.ToLower(text) == "explorer") {
			homeDir, _ := os.UserHomeDir()
			cmd = exec.Command("xdg-open", homeDir)
		} else if app == "xdg-open" && strings.ToLower(text) == "browser" {
			cmd = exec.Command("xdg-open", "https://google.com")
		} else {
			cmd = exec.Command(app)
		}

		err := cmd.Start()
		if err != nil {
			return nil, fmt.Errorf("failed to open application %s: %w", text, err)
		}

		// Reap child process exit status asynchronously to avoid zombies.
		go func() {
			cmd.Wait()
		}()

		return []byte(fmt.Sprintf("Launched %s successfully", text)), nil
	case "list_windows":
		if isWayland {
			return nil, fmt.Errorf("Window management (list_windows) is strictly blocked by the Wayland security model. Please switch to X11, or use coordinate-based mouse interactions")
		}
		out, err := exec.Command("wmctrl", "-l").CombinedOutput()
		if err != nil {
			return nil, fmt.Errorf("wmctrl failed (is it installed?): %w, output: %s", err, string(out))
		}
		// wmctrl -l columns: <window ID> <desktop ID> <client machine> <window title...>
		var titles []string
		lines := strings.Split(string(out), "\n")
		for _, line := range lines {
			parts := strings.Fields(line)
			if len(parts) >= 4 {
				titles = append(titles, strings.Join(parts[3:], " "))
			}
		}
		return []byte(strings.Join(titles, "\n")), nil
	case "activate_window":
		if isWayland {
			return nil, fmt.Errorf("activate_window is blocked by Wayland security model")
		}
		return exec.Command("wmctrl", "-a", text).CombinedOutput()
	case "close_window":
		if isWayland {
			return nil, fmt.Errorf("close_window is blocked by Wayland security model")
		}
		return exec.Command("wmctrl", "-c", text).CombinedOutput()
	case "move_window":
		if isWayland {
			return nil, fmt.Errorf("move_window is blocked by Wayland security model")
		}
		return exec.Command("wmctrl", "-r", text, "-e", fmt.Sprintf("0,%d,%d,-1,-1", x, y)).CombinedOutput()
	case "resize_window":
		if isWayland {
			return nil, fmt.Errorf("resize_window is blocked by Wayland security model")
		}
		return exec.Command("wmctrl", "-r", text, "-e", fmt.Sprintf("0,-1,-1,%d,%d", x, y)).CombinedOutput()
	case "mouse_move":
		if isWayland {
			return exec.Command("ydotool", "mousemove", "-x", fmt.Sprintf("%d", x), "-y", fmt.Sprintf("%d", y)).CombinedOutput()
		}
		return exec.Command("xdotool", "mousemove", fmt.Sprintf("%d", x), fmt.Sprintf("%d", y)).CombinedOutput()
	case "left_click":
		if isWayland {
			return exec.Command("ydotool", "click", "0xC0").CombinedOutput()
		}
		return exec.Command("xdotool", "click", "1").CombinedOutput()
	case "right_click":
		if isWayland {
			return exec.Command("ydotool", "click", "0xC1").CombinedOutput()
		}
		return exec.Command("xdotool", "click", "3").CombinedOutput()
	case "double_click":
		if isWayland {
			exec.Command("ydotool", "click", "0xC0").Run()
			return exec.Command("ydotool", "click", "0xC0").CombinedOutput()
		}
		return exec.Command("xdotool", "click", "--repeat", "2", "1").CombinedOutput()
	case "type":
		if isWayland {
			return exec.Command("ydotool", "type", text).CombinedOutput()
		}
		return exec.Command("xdotool", "type", text).CombinedOutput()
	case "key":
		if isWayland {
			return exec.Command("ydotool", "key", text).CombinedOutput()
		}
		return exec.Command("xdotool", "key", text).CombinedOutput()
	case "screenshot":
		tmpFile := fmt.Sprintf("/tmp/rasnuos_screenshot_%d.png", time.Now().UnixNano())
		defer os.Remove(tmpFile)

		if isWayland {
			err := exec.Command("grim", tmpFile).Run()
			if err != nil {
				return nil, fmt.Errorf("grim failed (is it installed for Wayland screenshots?): %w", err)
			}
		} else {
			err := exec.Command("scrot", "-z", tmpFile).Run()
			if err != nil {
				err = exec.Command("import", "-window", "root", tmpFile).Run()
				if err != nil {
					return nil, fmt.Errorf("failed to take screenshot (ensure scrot or imagemagick is installed): %w", err)
				}
			}
		}
		data, err := os.ReadFile(tmpFile)
		return data, err
	default:
		return nil, fmt.Errorf("unknown computer action: %s", action)
	}
}

func (l *LinuxAdapter) GetMachineID() string {
	b, err := os.ReadFile("/etc/machine-id")
	if err == nil && len(b) > 0 {
		return strings.TrimSpace(string(b))
	}
	b, err = os.ReadFile("/var/lib/dbus/machine-id")
	if err == nil && len(b) > 0 {
		return strings.TrimSpace(string(b))
	}
	return ""
}

func (a *LinuxAdapter) setupDisplayEnvironment() bool {
	if os.Getenv("WAYLAND_DISPLAY") != "" || os.Getenv("XDG_SESSION_TYPE") == "wayland" {
		return true
	}

	if os.Getenv("DISPLAY") == "" {
		displays, _ := filepath.Glob("/tmp/.X11-unix/X*")
		if len(displays) > 0 {
			displayNum := strings.TrimPrefix(filepath.Base(displays[0]), "X")
			os.Setenv("DISPLAY", ":"+displayNum)
		} else {
			os.Setenv("DISPLAY", ":0")
		}
	}

	if os.Getenv("XAUTHORITY") == "" {
		homeDir, err := os.UserHomeDir()
		if err == nil {
			authPath := filepath.Join(homeDir, ".Xauthority")
			if _, err := os.Stat(authPath); err == nil {
				os.Setenv("XAUTHORITY", authPath)
			}
		}
	}

	return false
}

//go:build linux
// +build linux

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

	if val, err := getLoadAvg(); err != nil {
		errs = append(errs, fmt.Errorf("load: %w", err))
	} else {
		metrics.LoadAvg = val
	}

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

	return metrics, errs
}

func (a *LinuxAdapter) GetShellCmd(ctx context.Context, script string, runAsUser string) *exec.Cmd {
	if runAsUser != "" {
		// Use su to switch to the user. -s /bin/bash ensures a valid shell even if the user has /bin/false
		return exec.CommandContext(ctx, "su", "-s", "/bin/bash", runAsUser, "-c", script)
	}
	return exec.CommandContext(ctx, "bash", "-c", script)
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
	// Implementation for installing systemd service
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

// ---------------------------------------------------------
// Helper functions adapted from previous metrics.go
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
	return statuses, nil
}

func (a *LinuxAdapter) PunchFirewallHole(port int) error {
	// Ignore errors since ufw or iptables might not be installed
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

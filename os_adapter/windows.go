//go:build windows
// +build windows

package os_adapter

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"sort"
	"strings"

	"github.com/shirou/gopsutil/v3/cpu"
	"github.com/shirou/gopsutil/v3/disk"
	"github.com/shirou/gopsutil/v3/load"
	"github.com/shirou/gopsutil/v3/mem"
	"github.com/shirou/gopsutil/v3/net"
	"github.com/shirou/gopsutil/v3/process"
)

func init() {
	SetAdapter(&WindowsAdapter{})
}

type WindowsAdapter struct{}

func (a *WindowsAdapter) GetMetrics() (Metrics, []error) {
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

func (a *WindowsAdapter) GetShellCmd(ctx context.Context, script string, runAsUser string) *exec.Cmd {
	if runAsUser != "" {
		// Stream a visible warning back to the Laravel frontend UI that Windows impersonation is ignored.
		script = fmt.Sprintf("Write-Output 'Warning: run_as_user (%s) is ignored on Windows without credentials. Running as SYSTEM.';\n%s", runAsUser, script)
	}
	return exec.CommandContext(ctx, "powershell", "-NoProfile", "-NonInteractive", "-Command", script)
}

func (a *WindowsAdapter) StartPTY(cols, rows uint16) (PTY, error) {
	// A rudimentary PTY wrapper for Windows using standard pipes.
	// True ConPTY requires complex CGO or syscall setups which are beyond a portable MVP.
	cmd := exec.Command("cmd.exe")

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}

	// We multiplex stderr into stdout for the terminal experience
	cmd.Stderr = cmd.Stdout

	if err := cmd.Start(); err != nil {
		return nil, err
	}

	return &WindowsPTY{
		cmd:    cmd,
		stdin:  stdin,
		stdout: stdout,
	}, nil
}

func (a *WindowsAdapter) InstallService(executablePath string) error {
	// In Windows, installing as a service typically uses New-Service
	psCmd := fmt.Sprintf(`New-Service -Name "RasnuCloudAgent" -BinaryPathName "%s" -DisplayName "RasnuCloud Agent" -StartupType Automatic`, executablePath)
	return exec.Command("powershell", "-NoProfile", "-Command", psCmd).Run()
}

func (a *WindowsAdapter) UninstallService() error {
	exec.Command("powershell", "-NoProfile", "-Command", `Stop-Service -Name "RasnuCloudAgent"`).Run()
	return exec.Command("powershell", "-NoProfile", "-Command", `Remove-Service -Name "RasnuCloudAgent"`).Run()
}

type WindowsPTY struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser
}

func (w *WindowsPTY) Read(p []byte) (n int, err error) {
	return w.stdout.Read(p)
}

func (w *WindowsPTY) Write(p []byte) (n int, err error) {
	return w.stdin.Write(p)
}

func (w *WindowsPTY) Resize(cols, rows uint16) error {
	// Not supported natively by simple pipes, silently ignore
	return nil
}

func (w *WindowsPTY) Close() error {
	w.stdin.Close()
	return w.stdout.Close()
}

func (w *WindowsPTY) Kill() error {
	if w.cmd != nil && w.cmd.Process != nil {
		w.cmd.Process.Kill()
		w.cmd.Wait()
	}
	return nil
}

// ---------------------------------------------------------
// Helper functions for windows
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
	d, err := disk.Usage("C:") // Default to C: for windows
	if err != nil {
		return 0.0, err
	}
	return d.UsedPercent, nil
}

func getLoadAvg() (string, error) {
	l, err := load.Avg()
	if err != nil {
		// Windows load avg via gopsutil might sometimes be unimplemented or empty.
		return "0.0, 0.0, 0.0", nil
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
	servicesToCheck := []string{"nginx", "mysql", "docker", "sshd"}
	statuses := make([]ServiceStatus, 0)

	for _, srv := range servicesToCheck {
		statusStr := "inactive"
		// This uses powershell for simplicity as gopsutil does not natively support services.
		cmd := fmt.Sprintf(`(Get-Service '%s' -ErrorAction SilentlyContinue).Status`, srv)
		out, err := exec.Command("powershell", "-NoProfile", "-Command", cmd).Output()
		if err == nil && strings.Contains(strings.ToLower(string(out)), "running") {
			statusStr = "active"
		}
		statuses = append(statuses, ServiceStatus{
			Name:   srv,
			Status: statusStr,
		})
	}

	return statuses, nil
}

//go:build windows
// +build windows

package os_adapter

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"

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
	// The agent runs as a Windows Service in Session 0 (isolated from the
	// interactive desktop). These helper functions let PowerShell scripts work
	// with the user's interactive session without any P/Invoke or C# Add-Type.
	const session0Preamble = `
# ── RasnuOS Session-Bridge Helpers ─────────────────────────────────────────────
# Returns the session ID of the first active interactive user (explorer owner).
function _RasnuGetUserSession {
    $p = Get-Process explorer -ErrorAction SilentlyContinue | Select-Object -First 1
    if ($p) { return $p.SessionId } else { return 1 }
}

# Lists all processes that belong to the interactive user session.
# Use this instead of filtering by MainWindowTitle (which is empty from Session 0).
function _RasnuGetUserProcesses {
    $sid = _RasnuGetUserSession
    Get-Process | Where-Object { $_.SessionId -eq $sid } | Sort-Object WorkingSet64 -Descending
}

# Launches a GUI application in the interactive user session via Task Scheduler.
# This is the ONLY reliable way to open a visible window from Session 0.
# Usage: _RasnuLaunchAsUser "C:\path\to\app.exe" ["-arg1" "-arg2"]
function _RasnuLaunchAsUser {
    param([string]$FilePath, [string[]]$ArgumentList = @())
    if (-not (Test-Path $FilePath)) { throw "Executable not found: $FilePath" }
    $taskName = "RasnuOS_Launch_" + [System.Guid]::NewGuid().ToString("N").Substring(0,8)
    $args     = if ($ArgumentList.Count -gt 0) { $ArgumentList -join " " } else { $null }
    $action   = if ($args) {
        New-ScheduledTaskAction -Execute $FilePath -Argument $args
    } else {
        New-ScheduledTaskAction -Execute $FilePath
    }
    $trigger   = New-ScheduledTaskTrigger -Once -At (Get-Date).AddSeconds(2)
    $userName  = (Get-Process explorer -ErrorAction SilentlyContinue |
                  Select-Object -First 1 |
                  ForEach-Object { (Get-WmiObject Win32_Process -Filter "ProcessId=$($_.Id)").GetOwner() } |
                  ForEach-Object { "$($_.Domain)\$($_.User)" })
    $principal = New-ScheduledTaskPrincipal -UserId $userName -LogonType Interactive -RunLevel Highest
    Register-ScheduledTask -TaskName $taskName -Action $action -Trigger $trigger -Principal $principal -Force | Out-Null
    Start-Sleep -Milliseconds 500
    Start-ScheduledTask -TaskName $taskName
    Start-Sleep -Seconds 2
    Unregister-ScheduledTask -TaskName $taskName -Confirm:$false -ErrorAction SilentlyContinue
    Write-Output "Launched: $FilePath (via user session)"
}
# ────────────────────────────────────────────────────────────────────────────────
`

	fullScript := session0Preamble + "\n" + script

	if runAsUser != "" {
		// Stream a visible warning back to the Laravel frontend UI that Windows impersonation is ignored.
		fullScript = fmt.Sprintf("Write-Output 'Warning: run_as_user (%s) is ignored on Windows without credentials. Running as SYSTEM.';\n%s", runAsUser, fullScript)
	}

	return exec.CommandContext(ctx, "powershell", "-NoProfile", "-NonInteractive", "-Command", fullScript)
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
	psCmd := fmt.Sprintf(`New-Service -Name "RasnuOSAgent" -BinaryPathName "%s" -DisplayName "RasnuOS Agent" -StartupType Automatic`, executablePath)
	return exec.Command("powershell", "-NoProfile", "-Command", psCmd).Run()
}

func (a *WindowsAdapter) UninstallService() error {
	// Stop the service gracefully first (ignore errors — it may already be stopped)
	exec.Command("powershell", "-NoProfile", "-Command", `Stop-Service -Name "RasnuOSAgent" -Force -ErrorAction SilentlyContinue`).Run()
	// Use sc.exe delete — works on ALL Windows versions (Vista+).
	// Remove-Service only exists in PowerShell 6+ (Core), not the default PS 5.1.
	return exec.Command("sc.exe", "delete", "RasnuOSAgent").Run()
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

func (a *WindowsAdapter) PunchFirewallHole(port int) error {
	cmdStr := fmt.Sprintf(`New-NetFirewallRule -DisplayName "RasnuOS Port %d" -Direction Inbound -LocalPort %d -Protocol TCP -Action Allow`, port, port)
	cmd := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", cmdStr)
	return cmd.Run()
}

func (a *WindowsAdapter) CloseFirewallHole(port int) error {
	cmdStr := fmt.Sprintf(`Remove-NetFirewallRule -DisplayName "RasnuOS Port %d" -ErrorAction SilentlyContinue`, port)
	cmd := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", cmdStr)
	return cmd.Run()
}

func (a *WindowsAdapter) ExecuteComputerAction(action, text string, x, y int) ([]byte, error) {
	// Because the Go Agent runs as a Windows Service in Session 0, we cannot interact
	// with the GUI directly. We must write a PowerShell script and launch it in the interactive session.
	// Securely escape strings for PowerShell single-quote literal strings
	safeAction := strings.ReplaceAll(action, `'`, `''`)
	safeText := strings.ReplaceAll(text, `'`, `''`)

	unixTime := time.Now().UnixNano()
	scriptPath := fmt.Sprintf(`C:\Windows\Temp\rasnuos_gui_%d.ps1`, unixTime)
	outPath := fmt.Sprintf(`C:\Windows\Temp\rasnuos_screen_%d.png`, unixTime)
	outTxtPath := fmt.Sprintf(`C:\Windows\Temp\rasnuos_out_%d.txt`, unixTime)

	// Prevent temp file leaks on error/panic
	defer os.Remove(scriptPath)
	defer os.Remove(outPath)
	defer os.Remove(outTxtPath)

	psScript := `
Add-Type -AssemblyName System.Windows.Forms
Add-Type -AssemblyName System.Drawing
Add-Type -MemberDefinition '[DllImport("user32.dll")] public static extern void mouse_event(int flags, int dx, int dy, int buttons, int extraInfo); [DllImport("user32.dll")] public static extern bool SetCursorPos(int X, int Y);' -Name Native -Namespace Win32

$action = '` + safeAction + `'
$x = ` + fmt.Sprintf("%d", x) + `
$y = ` + fmt.Sprintf("%d", y) + `
$text = '` + safeText + `'
$outPath = '` + outPath + `'
$outTxtPath = '` + outTxtPath + `'

try {
	switch ($action) {
		"open_application" { 
			$app = $text
			if ($text.ToLower() -eq "terminal") { 
				if (Get-Command wt.exe -ErrorAction SilentlyContinue) { $app = "wt.exe" } else { $app = "cmd.exe" }
			}
			elseif ($text.ToLower() -eq "file manager" -or $text.ToLower() -eq "explorer") { $app = "explorer.exe" }
			elseif ($text.ToLower() -eq "browser") { 
                Start-Process "https://google.com"
                return
            }
			Start-Process $app 
		}
		"list_windows" {
			$titles = Get-Process | Where-Object {$_.MainWindowTitle} | Select-Object -ExpandProperty MainWindowTitle
			($titles -join [Environment]::NewLine) | Out-File -FilePath $outTxtPath -Encoding UTF8
		}
		"activate_window" {
			Add-Type @"
			using System;
			using System.Runtime.InteropServices;
			public class Win32 {
				[DllImport("user32.dll")]
				[return: MarshalAs(UnmanagedType.Bool)]
				public static extern bool SetForegroundWindow(IntPtr hWnd);
			}
"@
			$proc = Get-Process | Where-Object {$_.MainWindowTitle -match $text} | Select-Object -First 1
			if ($proc) {
				[Win32]::SetForegroundWindow($proc.MainWindowHandle)
			}
		}
		"close_window" {
			$proc = Get-Process | Where-Object {$_.MainWindowTitle -match $text} | Select-Object -First 1
			if ($proc) {
				$proc.CloseMainWindow()
			}
		}
		"move_window", "resize_window" {
			Add-Type @"
			using System;
			using System.Runtime.InteropServices;
			public class Win32Move {
				[DllImport("user32.dll", SetLastError = true)]
				public static extern bool MoveWindow(IntPtr hWnd, int X, int Y, int nWidth, int nHeight, bool bRepaint);
				[DllImport("user32.dll")]
				public static extern bool GetWindowRect(IntPtr hwnd, out RECT lpRect);
				public struct RECT { public int Left; public int Top; public int Right; public int Bottom; }
			}
"@
			$proc = Get-Process | Where-Object {$_.MainWindowTitle -match $text} | Select-Object -First 1
			if ($proc) {
				$rect = New-Object Win32Move+RECT
				[Win32Move]::GetWindowRect($proc.MainWindowHandle, [ref]$rect)
				$width = $rect.Right - $rect.Left
				$height = $rect.Bottom - $rect.Top
				if ($action -eq "move_window") {
					[Win32Move]::MoveWindow($proc.MainWindowHandle, $x, $y, $width, $height, $true)
				} else {
					[Win32Move]::MoveWindow($proc.MainWindowHandle, $rect.Left, $rect.Top, $x, $y, $true)
				}
			}
		}
		"mouse_move" { [Win32.Native]::SetCursorPos($x, $y) }
		"left_click" { [Win32.Native]::mouse_event(0x02, 0, 0, 0, 0); [Win32.Native]::mouse_event(0x04, 0, 0, 0, 0) }
		"right_click" { [Win32.Native]::mouse_event(0x08, 0, 0, 0, 0); [Win32.Native]::mouse_event(0x10, 0, 0, 0, 0) }
		"double_click" { [Win32.Native]::mouse_event(0x02, 0, 0, 0, 0); [Win32.Native]::mouse_event(0x04, 0, 0, 0, 0); Start-Sleep -Milliseconds 100; [Win32.Native]::mouse_event(0x02, 0, 0, 0, 0); [Win32.Native]::mouse_event(0x04, 0, 0, 0, 0) }
		"type" { [System.Windows.Forms.SendKeys]::SendWait($text) }
		"key" { [System.Windows.Forms.SendKeys]::SendWait("{$text}") }
		"screenshot" {
			$bounds = [System.Windows.Forms.Screen]::PrimaryScreen.Bounds
			$bmp = New-Object System.Drawing.Bitmap $bounds.Width, $bounds.Height
			$graphics = [System.Drawing.Graphics]::FromImage($bmp)
			$graphics.CopyFromScreen($bounds.Location, [System.Drawing.Point]::Empty, $bounds.Size)
			$bmp.Save($outPath, [System.Drawing.Imaging.ImageFormat]::Png)
			$graphics.Dispose()
			$bmp.Dispose()
		}
	}
} catch {
	Write-Error $_.Exception.Message
}
`
	os.WriteFile(scriptPath, []byte(psScript), 0644)

	// Create a command that uses _RasnuLaunchAsUser to execute the script in Session 1
	// We handle output within the PS script (Out-File) to bypass shell redirection issues.
	cmdStr := fmt.Sprintf("_RasnuLaunchAsUser powershell.exe '-ExecutionPolicy Bypass -WindowStyle Hidden -File %s'", scriptPath)
	cmd := a.GetShellCmd(context.Background(), cmdStr, "")
	err := cmd.Run()

	if err != nil {
		return nil, fmt.Errorf("failed to launch GUI task: %w", err)
	}

	// Read the text output file (e.g., from list_windows) if it exists
	var outBytes []byte
	if data, err := os.ReadFile(outTxtPath); err == nil {
		outBytes = data
	}

	// If screenshot, wait a moment and read the file
	if action == "screenshot" {
		// Wait up to 5 seconds for the file to appear
		for i := 0; i < 50; i++ {
			if data, err := os.ReadFile(outPath); err == nil {
				return data, nil
			}
			time.Sleep(100 * time.Millisecond)
		}
		return nil, fmt.Errorf("screenshot file was not created by the interactive session")
	}

	return outBytes, nil
}

func (a *WindowsAdapter) GetMachineID() string {
	// The standard way to get a machine ID on Windows is to read MachineGuid
	out, err := exec.Command("powershell", "-NoProfile", "-Command", `(Get-ItemProperty -Path 'HKLM:\SOFTWARE\Microsoft\Cryptography' -Name 'MachineGuid').MachineGuid`).Output()
	if err == nil {
		return strings.TrimSpace(string(out))
	}
	return ""
}

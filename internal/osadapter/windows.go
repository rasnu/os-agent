// Copyright © 2026 Rasnu LLC (rasnu.com). All rights reserved.
// Licensed under the Functional Source License, Version 1.1, ALv2 Future License (FSL-1.1-ALv2).

//go:build windows
// +build windows

package osadapter

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
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
	// Windows records memory exhaustion events under Event ID 2004 in the System log.
	cmdStr := `Get-EventLog -LogName System -Source "Microsoft-Windows-Resource-Exhaustion-Detector" -Newest 5 -ErrorAction SilentlyContinue | Select-Object -ExpandProperty Message`
	out, err := exec.Command("powershell", "-NoProfile", "-Command", cmdStr).Output()
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

func (a *WindowsAdapter) GetShellCmd(ctx context.Context, script string, runAsUser string) *exec.Cmd {
	// Windows services run in Session 0, isolated from the interactive desktop.
	// These PowerShell helpers allow scripts to query or launch processes in the active user session.
	const session0Preamble = `
function _RasnuGetUserSession {
    $p = Get-Process explorer -ErrorAction SilentlyContinue | Select-Object -First 1
    if ($p) { return $p.SessionId } else { return 1 }
}

function _RasnuGetUserProcesses {
    $sid = _RasnuGetUserSession
    Get-Process | Where-Object { $_.SessionId -eq $sid } | Sort-Object WorkingSet64 -Descending
}

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
`

	fullScript := session0Preamble + "\n" + script

	if runAsUser != "" {
		fullScript = fmt.Sprintf("Write-Output 'Warning: run_as_user (%s) is ignored on Windows without credentials. Running as SYSTEM.';\n%s", runAsUser, fullScript)
	}

	return exec.CommandContext(ctx, "powershell", "-NoProfile", "-NonInteractive", "-Command", fullScript)
}

func (a *WindowsAdapter) StartPTY(cols, rows uint16) (PTY, error) {
	if conpty, err := startWindowsConPTY(cols, rows); err == nil {
		return conpty, nil
	} else {
		log.Printf("[PTY] ConPTY unavailable (%v), falling back to pipe PTY", err)
	}

	cmd := exec.Command("cmd.exe")

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}

	cmd.Stderr = cmd.Stdout

	if err := cmd.Start(); err != nil {
		return nil, err
	}

	pr, pw := io.Pipe()
	echoCh := make(chan []byte, 256)
	go func() {
		_, _ = io.Copy(pw, stdout)
		_ = pw.Close()
	}()
	go func() {
		for b := range echoCh {
			_, _ = pw.Write(b)
		}
	}()

	return &WindowsPTY{
		cmd:       cmd,
		stdin:     stdin,
		stdout:    stdout,
		outReader: pr,
		outWriter: pw,
		echoCh:    echoCh,
	}, nil
}

func (a *WindowsAdapter) InstallService(executablePath string) error {
	psCmd := fmt.Sprintf(`New-Service -Name "RasnuOSAgent" -BinaryPathName "%s" -DisplayName "RasnuOS Agent" -StartupType Automatic`, executablePath)
	return exec.Command("powershell", "-NoProfile", "-Command", psCmd).Run()
}

func (a *WindowsAdapter) UninstallService() error {
	exec.Command("powershell", "-NoProfile", "-Command", `Stop-Service -Name "RasnuOSAgent" -Force -ErrorAction SilentlyContinue`).Run()
	// Use sc.exe delete because Remove-Service requires PowerShell 6+ and is absent in Windows PowerShell 5.1.
	return exec.Command("sc.exe", "delete", "RasnuOSAgent").Run()
}

type WindowsPTY struct {
	mu        sync.Mutex
	cmd       *exec.Cmd
	stdin     io.WriteCloser
	stdout    io.ReadCloser
	outReader *io.PipeReader
	outWriter *io.PipeWriter
	echoCh    chan []byte
	lineBuf   []byte
	closed    bool
}

func (w *WindowsPTY) echo(b []byte) {
	if w.closed {
		return
	}
	select {
	case w.echoCh <- b:
	default:
	}
}

func (w *WindowsPTY) Read(p []byte) (n int, err error) {
	return w.outReader.Read(p)
}

func (w *WindowsPTY) Write(p []byte) (n int, err error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	for _, b := range p {
		switch b {
		case '\r', '\n':
			w.echo([]byte("\r"))
			line := append(append([]byte(nil), w.lineBuf...), '\r', '\n')
			w.lineBuf = w.lineBuf[:0]
			if _, err := w.stdin.Write(line); err != nil {
				return 0, err
			}
		case 0x7f, '\b':
			if len(w.lineBuf) > 0 {
				w.lineBuf = w.lineBuf[:len(w.lineBuf)-1]
				w.echo([]byte("\b \b"))
			}
		case 0x03: // Ctrl+C
			w.lineBuf = w.lineBuf[:0]
			w.echo([]byte("^C\r\n"))
			_, _ = w.stdin.Write([]byte("\r\n"))
		default:
			if b >= 0x20 {
				w.lineBuf = append(w.lineBuf, b)
				w.echo([]byte{b})
			}
		}
	}
	return len(p), nil
}

func (w *WindowsPTY) Resize(cols, rows uint16) error {
	return nil
}

func (w *WindowsPTY) Close() error {
	w.mu.Lock()
	if !w.closed {
		w.closed = true
		close(w.echoCh)
	}
	w.mu.Unlock()
	_ = w.stdin.Close()
	_ = w.outWriter.Close()
	_ = w.outReader.Close()
	return w.stdout.Close()
}

func (w *WindowsPTY) Kill() error {
	if w.cmd != nil && w.cmd.Process != nil {
		w.cmd.Process.Kill()
		w.cmd.Wait()
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

func getLoadAvg() (string, float64, error) {
	l, err := load.Avg()
	if err != nil {
		return "0.0, 0.0, 0.0", 0.0, nil
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
	// Because the Windows service runs in Session 0, GUI actions are written to a temporary
	// PowerShell script and dispatched into the interactive user session via _RasnuLaunchAsUser.
	safeAction := strings.ReplaceAll(action, `'`, `''`)
	safeText := strings.ReplaceAll(text, `'`, `''`)

	unixTime := time.Now().UnixNano()
	scriptPath := fmt.Sprintf(`C:\Windows\Temp\rasnuos_gui_%d.ps1`, unixTime)
	outPath := fmt.Sprintf(`C:\Windows\Temp\rasnuos_screen_%d.png`, unixTime)
	outTxtPath := fmt.Sprintf(`C:\Windows\Temp\rasnuos_out_%d.txt`, unixTime)

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

	cmdStr := fmt.Sprintf("_RasnuLaunchAsUser powershell.exe '-ExecutionPolicy Bypass -WindowStyle Hidden -File %s'", scriptPath)
	cmd := a.GetShellCmd(context.Background(), cmdStr, "")
	err := cmd.Run()

	if err != nil {
		return nil, fmt.Errorf("failed to launch GUI task: %w", err)
	}

	var outBytes []byte
	if data, err := os.ReadFile(outTxtPath); err == nil {
		outBytes = data
	}

	if action == "screenshot" {
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
	out, err := exec.Command("powershell", "-NoProfile", "-Command", `(Get-ItemProperty -Path 'HKLM:\SOFTWARE\Microsoft\Cryptography' -Name 'MachineGuid').MachineGuid`).Output()
	if err == nil {
		return strings.TrimSpace(string(out))
	}
	return ""
}

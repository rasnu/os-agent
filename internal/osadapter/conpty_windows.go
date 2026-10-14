// Copyright © 2026 Rasnu LLC (rasnu.com). All rights reserved.
// Licensed under the Functional Source License, Version 1.1, ALv2 Future License (FSL-1.1-ALv2).

//go:build windows
// +build windows

package osadapter

import (
	"os"
	"path/filepath"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	modkernel32                   = windows.NewLazySystemDLL("kernel32.dll")
	procUpdateProcThreadAttribute = modkernel32.NewProc("UpdateProcThreadAttribute")
)

// WindowsConPTY wraps a native Windows 10 1809+ / Server 2019+ Pseudo Console (ConPTY)
// supporting true terminal resizing (ResizePseudoConsole) and VT/ANSI sequences.
type WindowsConPTY struct {
	mu      sync.Mutex
	hpc     windows.Handle
	pi      windows.ProcessInformation
	inPipe  windows.Handle
	outPipe windows.Handle
	closed  bool
}

func startWindowsConPTY(cols, rows uint16) (PTY, error) {
	if cols == 0 {
		cols = 80
	}
	if rows == 0 {
		rows = 24
	}

	var ptyInRead, ptyInWrite windows.Handle
	if err := windows.CreatePipe(&ptyInRead, &ptyInWrite, nil, 0); err != nil {
		return nil, err
	}

	var ptyOutRead, ptyOutWrite windows.Handle
	if err := windows.CreatePipe(&ptyOutRead, &ptyOutWrite, nil, 0); err != nil {
		_ = windows.CloseHandle(ptyInRead)
		_ = windows.CloseHandle(ptyInWrite)
		return nil, err
	}

	size := windows.Coord{X: int16(cols), Y: int16(rows)}
	var hpc windows.Handle
	if err := windows.CreatePseudoConsole(size, ptyInRead, ptyOutWrite, 0, &hpc); err != nil {
		_ = windows.CloseHandle(ptyInRead)
		_ = windows.CloseHandle(ptyInWrite)
		_ = windows.CloseHandle(ptyOutRead)
		_ = windows.CloseHandle(ptyOutWrite)
		return nil, err
	}

	// Close the child-side pipe handles now owned by the ConPTY host
	_ = windows.CloseHandle(ptyInRead)
	_ = windows.CloseHandle(ptyOutWrite)

	attrList, err := windows.NewProcThreadAttributeList(1)
	if err != nil {
		windows.ClosePseudoConsole(hpc)
		_ = windows.CloseHandle(ptyInWrite)
		_ = windows.CloseHandle(ptyOutRead)
		return nil, err
	}
	defer attrList.Delete()

	// Win32 UpdateProcThreadAttribute expects the HPCON handle value itself (not &hpc)
	// in lpValue for PROC_THREAD_ATTRIBUTE_PSEUDOCONSOLE. Calling the proc directly
	// with uintptr(hpc) avoids storing a non-heap handle in attrList's []unsafe.Pointer.
	r1, _, e1 := procUpdateProcThreadAttribute.Call(
		uintptr(unsafe.Pointer(attrList.List())),
		0,
		uintptr(windows.PROC_THREAD_ATTRIBUTE_PSEUDOCONSOLE),
		uintptr(hpc),
		unsafe.Sizeof(hpc),
		0,
		0,
	)
	if r1 == 0 {
		windows.ClosePseudoConsole(hpc)
		_ = windows.CloseHandle(ptyInWrite)
		_ = windows.CloseHandle(ptyOutRead)
		return nil, e1
	}

	siEx := windows.StartupInfoEx{
		StartupInfo: windows.StartupInfo{
			Cb: uint32(unsafe.Sizeof(windows.StartupInfoEx{})),
		},
		ProcThreadAttributeList: attrList.List(),
	}

	sysRoot := os.Getenv("SystemRoot")
	if sysRoot == "" {
		sysRoot = `C:\Windows`
	}
	psPath := filepath.Join(sysRoot, "System32", "WindowsPowerShell", "v1.0", "powershell.exe")
	var commandLineStr string
	if _, err := os.Stat(psPath); err == nil {
		// Unload PSReadLine in Session 0 service context so PowerShell uses native ConPTY
		// console input without blocking on ANSI DSR (\x1b[6n) cursor queries.
		commandLineStr = `"` + psPath + `" -NoLogo -NoExit -Command "Remove-Module PSReadLine -ErrorAction SilentlyContinue"`
	} else {
		commandLineStr = filepath.Join(sysRoot, "System32", "cmd.exe")
	}

	cmdLine, err := windows.UTF16PtrFromString(commandLineStr)
	if err != nil {
		windows.ClosePseudoConsole(hpc)
		_ = windows.CloseHandle(ptyInWrite)
		_ = windows.CloseHandle(ptyOutRead)
		return nil, err
	}

	var pi windows.ProcessInformation
	createErr := windows.CreateProcess(
		nil,
		cmdLine,
		nil,
		nil,
		false,
		windows.EXTENDED_STARTUPINFO_PRESENT,
		nil,
		nil,
		&siEx.StartupInfo,
		&pi,
	)
	if createErr != nil {
		windows.ClosePseudoConsole(hpc)
		_ = windows.CloseHandle(ptyInWrite)
		_ = windows.CloseHandle(ptyOutRead)
		return nil, createErr
	}

	return &WindowsConPTY{
		hpc:     hpc,
		pi:      pi,
		inPipe:  ptyInWrite,
		outPipe: ptyOutRead,
	}, nil
}

func (c *WindowsConPTY) Read(p []byte) (int, error) {
	c.mu.Lock()
	h := c.outPipe
	c.mu.Unlock()
	if h == 0 {
		return 0, os.ErrClosed
	}
	var done uint32
	err := windows.ReadFile(h, p, &done, nil)
	return int(done), err
}

func (c *WindowsConPTY) Write(p []byte) (int, error) {
	c.mu.Lock()
	h := c.inPipe
	c.mu.Unlock()
	if h == 0 {
		return 0, os.ErrClosed
	}
	var done uint32
	err := windows.WriteFile(h, p, &done, nil)
	return int(done), err
}

func (c *WindowsConPTY) Resize(cols, rows uint16) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed || c.hpc == 0 {
		return nil
	}
	if cols == 0 {
		cols = 80
	}
	if rows == 0 {
		rows = 24
	}
	return windows.ResizePseudoConsole(c.hpc, windows.Coord{X: int16(cols), Y: int16(rows)})
}

func (c *WindowsConPTY) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil
	}
	c.closed = true
	if c.hpc != 0 {
		windows.ClosePseudoConsole(c.hpc)
		c.hpc = 0
	}
	if c.inPipe != 0 {
		_ = windows.CloseHandle(c.inPipe)
		c.inPipe = 0
	}
	var err error
	if c.outPipe != 0 {
		err = windows.CloseHandle(c.outPipe)
		c.outPipe = 0
	}
	if c.pi.Thread != 0 {
		_ = windows.CloseHandle(c.pi.Thread)
		c.pi.Thread = 0
	}
	if c.pi.Process != 0 {
		_ = windows.CloseHandle(c.pi.Process)
		c.pi.Process = 0
	}
	return err
}

func (c *WindowsConPTY) Kill() error {
	c.mu.Lock()
	procHandle := c.pi.Process
	c.mu.Unlock()
	if procHandle != 0 {
		_ = windows.TerminateProcess(procHandle, 1)
	}
	return c.Close()
}

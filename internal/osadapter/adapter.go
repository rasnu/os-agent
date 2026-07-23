// Copyright © 2026 Rasnu LLC (rasnu.com). All rights reserved.
// Licensed under the Functional Source License, Version 1.1, ALv2 Future License (FSL-1.1-ALv2).

package osadapter

import (
	"context"
	"os/exec"
	"sync"
)

type Process struct {
	PID     string `json:"pid"`
	User    string `json:"user"`
	CPU     string `json:"cpu"`
	Memory  string `json:"memory"`
	Command string `json:"command"`
}

type ServiceStatus struct {
	Name   string `json:"name"`
	Status string `json:"status"`
}

type Metrics struct {
	CPUUsage  float64
	RAMUsage  float64
	DiskUsage float64
	SwapUsage float64
	IOWait    float64
	Load5     float64
	CPUCores  int
	LoadAvg   string
	NetworkRX uint64
	NetworkTX uint64
	DiskRead  uint64
	DiskWrite uint64
	Processes []Process
	Services  []ServiceStatus
	OOMKills  []string
}

type PTY interface {
	Read(p []byte) (n int, err error)
	Write(p []byte) (n int, err error)
	Resize(cols, rows uint16) error
	Close() error
	Kill() error
}

type OSAdapter interface {
	GetMetrics() (Metrics, []error)
	GetShellCmd(ctx context.Context, script string, runAsUser string) *exec.Cmd
	StartPTY(cols, rows uint16) (PTY, error)
	InstallService(executablePath string) error
	UninstallService() error

	VerifyBinary(path string) error
	ReplaceBinary(tempPath, currentPath string) error
	RestartAgent() error

	ManageService(action, service string) (string, error)
	KillProcess(pid string) (string, error)
	WriteCronJobs(content string) error

	LogFilePath() string

	PunchFirewallHole(port int) error
	CloseFirewallHole(port int) error

	ExecuteComputerAction(action, text string, x, y int) ([]byte, error)

	GetMachineID() string
}

var (
	adapterMutex   sync.RWMutex
	currentAdapter OSAdapter
)

func Current() OSAdapter {
	adapterMutex.RLock()
	defer adapterMutex.RUnlock()
	if currentAdapter == nil {
		panic("OSAdapter is not initialized. Ensure an OS-specific init() is called.")
	}
	return currentAdapter
}

func SetAdapter(a OSAdapter) {
	adapterMutex.Lock()
	defer adapterMutex.Unlock()
	currentAdapter = a
}

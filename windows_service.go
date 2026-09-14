//go:build windows
// +build windows

/**
 * Project: RasnuOS
 * Copyright © 2026 Rasnu LLC (rasnu.com). All rights reserved.
 */

package main

import (
	"fmt"
	"os"
	"time"

	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/eventlog"
)

const serviceName = "RasnuOSAgent"

// windowsService implements the svc.Handler interface required by the Windows
// Service Control Manager (SCM). It delegates actual agent logic to run().
type windowsService struct{}

func (w *windowsService) Execute(args []string, req <-chan svc.ChangeRequest, status chan<- svc.Status) (bool, uint32) {
	const cmdsAccepted = svc.AcceptStop | svc.AcceptShutdown

	// Tell SCM we are starting.
	status <- svc.Status{State: svc.StartPending}

	// Start the actual agent logic in a goroutine.
	errCh := make(chan error, 1)
	shutdown := make(chan struct{})

	go func() {
		errCh <- runWithShutdown(shutdown)
	}()

	// Signal SCM we are now running.
	status <- svc.Status{State: svc.Running, Accepts: cmdsAccepted}

	for {
		select {
		case c := <-req:
			switch c.Cmd {
			case svc.Stop, svc.Shutdown:
				status <- svc.Status{State: svc.StopPending}
				close(shutdown)
				select {
				case <-errCh:
				case <-time.After(10 * time.Second):
				}
				return false, 0
			default:
				// Ignore unknown control codes.
			}
		case err := <-errCh:
			if err != nil {
				logToEventLog(fmt.Sprintf("Agent exited with error: %v", err))
			}
			return false, 0
		}
	}
}

// runAsWindowsService runs the agent under Windows SCM control.
func runAsWindowsService() error {
	return svc.Run(serviceName, &windowsService{})
}

// isRunningAsWindowsService reports whether the process was started by SCM.
func isRunningAsWindowsService() bool {
	ok, err := svc.IsWindowsService()
	return err == nil && ok
}

func logToEventLog(msg string) {
	el, err := eventlog.Open(serviceName)
	if err != nil {
		return
	}
	defer el.Close()
	el.Error(1, msg) //nolint:errcheck
}

// runWithShutdown is identical to run() but respects an external shutdown channel
// instead of waiting for OS signals (which SCM doesn't use).
func runWithShutdown(shutdown chan struct{}) error {
	// Re-use os.Args[1:] — env vars were injected by SCM via the registry
	// HKLM\SYSTEM\CurrentControlSet\Services\RasnuOSAgent\Environment key.
	return runAgent(os.Args[1:], shutdown)
}

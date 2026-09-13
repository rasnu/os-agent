// Copyright © 2026 Rasnu LLC (rasnu.com). All rights reserved.
// Licensed under the Functional Source License, Version 1.1, ALv2 Future License (FSL-1.1-ALv2).

//go:build windows
// +build windows

package agent

import (
	"fmt"
	"os"
	"time"

	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/eventlog"
)

const serviceName = "RasnuOSAgent"

// windowsService implements svc.Handler for the Windows Service Control Manager (SCM).
type windowsService struct{}

func (w *windowsService) Execute(args []string, req <-chan svc.ChangeRequest, status chan<- svc.Status) (bool, uint32) {
	const cmdsAccepted = svc.AcceptStop | svc.AcceptShutdown

	status <- svc.Status{State: svc.StartPending}

	errCh := make(chan error, 1)
	shutdown := make(chan struct{})

	go func() {
		errCh <- runWithShutdown(shutdown)
	}()

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
			}
		case err := <-errCh:
			if err != nil {
				logToEventLog(fmt.Sprintf("Agent exited with error: %v", err))
			}
			return false, 0
		}
	}
}

func runAsWindowsService() error {
	return svc.Run(serviceName, &windowsService{})
}

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

func runWithShutdown(shutdown chan struct{}) error {
	// Environment variables are injected by SCM from HKLM\SYSTEM\CurrentControlSet\Services\RasnuOSAgent\Environment.
	return runAgent(os.Args[1:], shutdown)
}

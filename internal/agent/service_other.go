// Copyright © 2026 Rasnu LLC (rasnu.com). All rights reserved.
// Licensed under the Functional Source License, Version 1.1, ALv2 Future License (FSL-1.1-ALv2).

//go:build !windows
// +build !windows

package agent

func isRunningAsWindowsService() bool {
	return false
}

func runAsWindowsService() error {
	panic("runAsWindowsService called on non-Windows platform")
}

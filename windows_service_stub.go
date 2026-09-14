//go:build !windows
// +build !windows

/**
 * Project: RasnuOS
 * Copyright © 2026 Rasnu LLC (rasnu.com). All rights reserved.
 */

package main

// isRunningAsWindowsService always returns false on non-Windows platforms.
func isRunningAsWindowsService() bool {
	return false
}

// runAsWindowsService is unreachable on non-Windows platforms.
func runAsWindowsService() error {
	panic("runAsWindowsService called on non-Windows platform")
}

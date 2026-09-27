// Copyright © 2026 Rasnu LLC (rasnu.com). All rights reserved.
// Licensed under the Functional Source License, Version 1.1, ALv2 Future License (FSL-1.1-ALv2).

package agent

import (
	"os"
	"path/filepath"
	"testing"

	"rasnuos-agent/internal/audit"
)

// TestMain isolates package-wide side effects so the test suite never writes into
// the working tree. Without this, any test that triggers audit.Log() falls back
// to "./audit.jsonl" (the repo root) when the OS log directory is not writable.
func TestMain(m *testing.M) {
	tmpDir, err := os.MkdirTemp("", "rasnuos-agent-test-")
	if err != nil {
		panic(err)
	}

	audit.SetPathOverride(filepath.Join(tmpDir, "audit.jsonl"))

	code := m.Run()

	os.RemoveAll(tmpDir)
	os.Exit(code)
}

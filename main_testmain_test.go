package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestMain isolates package-wide side effects so the test suite never writes into
// the working tree. Without this, any test that triggers logAuditEvent() falls back
// to "./audit.jsonl" (the repo root) when the OS log directory is not writable.
func TestMain(m *testing.M) {
	tmpDir, err := os.MkdirTemp("", "rasnuos-agent-test-")
	if err != nil {
		panic(err)
	}

	auditFilePathOverride = filepath.Join(tmpDir, "audit.jsonl")

	code := m.Run()

	os.RemoveAll(tmpDir)
	os.Exit(code)
}

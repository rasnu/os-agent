package main

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
)

func TestLogAuditEvent_FileCreationAndPermissions(t *testing.T) {
	tmpDir := t.TempDir()
	testAuditPath := filepath.Join(tmpDir, "audit.jsonl")

	auditMutex.Lock()
	oldOverride := auditFilePathOverride
	auditFilePathOverride = testAuditPath
	auditMutex.Unlock()

	defer func() {
		auditMutex.Lock()
		auditFilePathOverride = oldOverride
		auditMutex.Unlock()
	}()

	event := AuditEvent{
		Action:     "test_action",
		PipelineID: "pipe-123",
		StepID:     "step-456",
		Command:    "echo test",
		RunAsUser:  "root",
		Success:    true,
	}

	logAuditEvent(event)

	info, err := os.Stat(testAuditPath)
	if err != nil {
		t.Fatalf("Expected audit file to exist, got err: %v", err)
	}

	if runtime.GOOS != "windows" {
		perm := info.Mode().Perm()
		if perm != 0600 {
			t.Errorf("Expected audit file permission 0600, got %04o", perm)
		}
	}

	data, err := os.ReadFile(testAuditPath)
	if err != nil {
		t.Fatalf("Failed to read audit file: %v", err)
	}

	var parsed AuditEvent
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("Failed to unmarshal audit event JSON: %v", err)
	}

	if parsed.Action != "test_action" || parsed.PipelineID != "pipe-123" || parsed.StepID != "step-456" || !parsed.Success {
		t.Errorf("Unexpected event content: %+v", parsed)
	}
	if parsed.Timestamp == "" {
		t.Errorf("Expected timestamp to be populated, got empty string")
	}
}

func TestLogAuditEvent_TightensExistingPerms(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX file permissions do not apply on Windows")
	}

	tmpDir := t.TempDir()
	testAuditPath := filepath.Join(tmpDir, "existing_audit.jsonl")

	// Pre-create file with loose 0644 permissions
	if err := os.WriteFile(testAuditPath, []byte(""), 0644); err != nil {
		t.Fatalf("Failed to pre-create test audit file: %v", err)
	}
	_ = os.Chmod(testAuditPath, 0644)

	auditMutex.Lock()
	oldOverride := auditFilePathOverride
	auditFilePathOverride = testAuditPath
	auditMutex.Unlock()

	defer func() {
		auditMutex.Lock()
		auditFilePathOverride = oldOverride
		auditMutex.Unlock()
	}()

	event := AuditEvent{
		Action:  "tighten_perm_test",
		Success: true,
	}
	logAuditEvent(event)

	info, err := os.Stat(testAuditPath)
	if err != nil {
		t.Fatalf("Failed to stat audit file: %v", err)
	}

	perm := info.Mode().Perm()
	if perm != 0600 {
		t.Errorf("Expected existing file perms to be tightened to 0600, got %04o", perm)
	}
}

func TestLogAuditEvent_ConcurrentWrites(t *testing.T) {
	tmpDir := t.TempDir()
	testAuditPath := filepath.Join(tmpDir, "concurrent_audit.jsonl")

	auditMutex.Lock()
	oldOverride := auditFilePathOverride
	auditFilePathOverride = testAuditPath
	auditMutex.Unlock()

	defer func() {
		auditMutex.Lock()
		auditFilePathOverride = oldOverride
		auditMutex.Unlock()
	}()

	const numRoutines = 20
	const writesPerRoutine = 5
	var wg sync.WaitGroup

	for i := 0; i < numRoutines; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for j := 0; j < writesPerRoutine; j++ {
				logAuditEvent(AuditEvent{
					Action:     "concurrent_write",
					PipelineID: "p-concurrent",
					StepID:     "s-concurrent",
					Success:    true,
				})
			}
		}(i)
	}

	wg.Wait()

	file, err := os.Open(testAuditPath)
	if err != nil {
		t.Fatalf("Failed to open audit file: %v", err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	lineCount := 0
	for scanner.Scan() {
		lineCount++
		var ev AuditEvent
		if err := json.Unmarshal(scanner.Bytes(), &ev); err != nil {
			t.Fatalf("Corrupted JSON line on line %d: %v", lineCount, err)
		}
		if ev.Action != "concurrent_write" {
			t.Fatalf("Mismatched action on line %d: %s", lineCount, ev.Action)
		}
	}

	expectedCount := numRoutines * writesPerRoutine
	if lineCount != expectedCount {
		t.Errorf("Expected %d audit entries, counted %d", expectedCount, lineCount)
	}
}

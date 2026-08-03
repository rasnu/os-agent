// Copyright © 2026 Rasnu LLC (rasnu.com). All rights reserved.
// Licensed under the Functional Source License, Version 1.1, ALv2 Future License (FSL-1.1-ALv2).

package audit

import (
	"bufio"
	"bytes"
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

	mu.Lock()
	oldOverride := pathOverride
	pathOverride = testAuditPath
	mu.Unlock()

	defer func() {
		mu.Lock()
		pathOverride = oldOverride
		mu.Unlock()
	}()

	event := Event{
		Action:     "test_action",
		PipelineID: "pipe-123",
		StepID:     "step-456",
		Command:    "echo test",
		RunAsUser:  "root",
		Success:    true,
	}

	Log(event)

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

	var parsed Event
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

	if err := os.WriteFile(testAuditPath, []byte(""), 0644); err != nil {
		t.Fatalf("Failed to pre-create test audit file: %v", err)
	}
	_ = os.Chmod(testAuditPath, 0644)

	mu.Lock()
	oldOverride := pathOverride
	pathOverride = testAuditPath
	mu.Unlock()

	defer func() {
		mu.Lock()
		pathOverride = oldOverride
		mu.Unlock()
	}()

	event := Event{
		Action:  "tighten_perm_test",
		Success: true,
	}
	Log(event)

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

	mu.Lock()
	oldOverride := pathOverride
	pathOverride = testAuditPath
	mu.Unlock()

	defer func() {
		mu.Lock()
		pathOverride = oldOverride
		mu.Unlock()
	}()

	const numRoutines = 20
	const writesPerRoutine = 5
	var wg sync.WaitGroup

	for i := 0; i < numRoutines; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for j := 0; j < writesPerRoutine; j++ {
				Log(Event{
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
		var ev Event
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

	verifiedCount, err := VerifyChain(testAuditPath)
	if err != nil {
		t.Fatalf("Expected concurrent audit chain to be cryptographically valid, got err: %v", err)
	}
	if verifiedCount != expectedCount {
		t.Errorf("Expected VerifyChain count %d, got %d", expectedCount, verifiedCount)
	}
}

func TestLogAuditEvent_HashChainAndTamperDetection(t *testing.T) {
	tmpDir := t.TempDir()
	testAuditPath := filepath.Join(tmpDir, "chained_audit.jsonl")

	oldOverride := SetPathOverride(testAuditPath)
	defer SetPathOverride(oldOverride)

	Log(Event{Action: "step_1", Command: "systemctl status nginx", Success: true})
	Log(Event{Action: "step_2", Command: "systemctl restart nginx", Success: true})

	// Simulate agent restart by clearing in-memory lastHashByFile cache before step_3
	mu.Lock()
	delete(lastHashByFile, testAuditPath)
	mu.Unlock()

	Log(Event{Action: "step_3", Command: "nginx -t", Success: true})

	count, err := VerifyChain(testAuditPath)
	if err != nil {
		t.Fatalf("VerifyChain failed on valid log: %v", err)
	}
	if count != 3 {
		t.Fatalf("Expected 3 verified entries, got %d", count)
	}

	// Tamper with the second entry and verify that VerifyChain catches it
	rawBytes, err := os.ReadFile(testAuditPath)
	if err != nil {
		t.Fatalf("Failed to read audit file: %v", err)
	}
	tampered := bytes.Replace(rawBytes, []byte("systemctl restart nginx"), []byte("systemctl stop nginx   "), 1)
	if err := os.WriteFile(testAuditPath, tampered, 0600); err != nil {
		t.Fatalf("Failed to write tampered audit file: %v", err)
	}

	if _, err := VerifyChain(testAuditPath); err == nil {
		t.Errorf("Expected VerifyChain to fail on tampered audit log, but it succeeded")
	}
}

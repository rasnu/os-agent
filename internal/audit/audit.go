// Copyright © 2026 Rasnu LLC (rasnu.com). All rights reserved.
// Licensed under the Functional Source License, Version 1.1, ALv2 Future License (FSL-1.1-ALv2).

// Package audit writes the agent's local, append-only audit trail (JSON Lines,
// 0600 permissions) next to the OS-specific agent log file.
package audit

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"rasnuos-agent/internal/osadapter"
)

// GenesisHash is the 64-zero root anchor for the first entry in a local audit chain.
const GenesisHash = "0000000000000000000000000000000000000000000000000000000000000000"

// Event represents a single local security audit entry. Timestamp, PrevHash, and Hash are populated by Log.
type Event struct {
	Timestamp  string `json:"timestamp"`
	Action     string `json:"action"`
	PipelineID string `json:"pipeline_id,omitempty"`
	StepID     string `json:"step_id,omitempty"`
	Command    string `json:"command,omitempty"`
	RunAsUser  string `json:"run_as_user,omitempty"`
	Success    bool   `json:"success"`
	Error      string `json:"error,omitempty"`
	PrevHash   string `json:"prev_hash,omitempty"`
	Hash       string `json:"hash,omitempty"`
}

var (
	mu             sync.Mutex
	pathOverride   string
	lastHashByFile = make(map[string]string)
)

// SetPathOverride redirects the audit log to path ("" restores the OS default)
// and returns the previous override.
func SetPathOverride(path string) (previous string) {
	mu.Lock()
	defer mu.Unlock()
	previous = pathOverride
	pathOverride = path
	return previous
}

func filePath() string {
	if pathOverride != "" {
		return pathOverride
	}
	logFile := osadapter.Current().LogFilePath()
	logDir := filepath.Dir(logFile)
	return filepath.Join(logDir, "audit.jsonl")
}

func computeEventHash(ev Event) string {
	payload := fmt.Sprintf("%s|%s|%s|%s|%s|%s|%t|%s|%s",
		ev.PrevHash,
		ev.Timestamp,
		ev.Action,
		ev.PipelineID,
		ev.StepID,
		ev.Command,
		ev.Success,
		ev.RunAsUser,
		ev.Error,
	)
	sum := sha256.Sum256([]byte(payload))
	return hex.EncodeToString(sum[:])
}

func resolvePrevHash(auditFile string) string {
	info, err := os.Stat(auditFile)
	if err != nil || info.Size() == 0 {
		delete(lastHashByFile, auditFile)
		return GenesisHash
	}
	if cached, ok := lastHashByFile[auditFile]; ok && cached != "" {
		return cached
	}
	f, err := os.Open(auditFile)
	if err != nil {
		return GenesisHash
	}
	defer f.Close()

	var lastLine string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line != "" {
			lastLine = line
		}
	}
	if lastLine == "" {
		return GenesisHash
	}
	var lastEv Event
	if err := json.Unmarshal([]byte(lastLine), &lastEv); err == nil && lastEv.Hash != "" {
		lastHashByFile[auditFile] = lastEv.Hash
		return lastEv.Hash
	}
	return GenesisHash
}

// VerifyChain validates the SHA-256 cryptographic hash chain of an audit.jsonl file.
// It returns the number of verified entries or an error identifying the first tampered line.
func VerifyChain(path string) (int, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()

	expectedPrev := GenesisHash
	lineNum := 0
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		raw := strings.TrimSpace(scanner.Text())
		if raw == "" {
			continue
		}
		lineNum++
		var ev Event
		if err := json.Unmarshal([]byte(raw), &ev); err != nil {
			return lineNum - 1, fmt.Errorf("line %d: invalid JSON: %w", lineNum, err)
		}
		if ev.PrevHash != expectedPrev {
			return lineNum - 1, fmt.Errorf("line %d: prev_hash mismatch (got %q, expected %q)", lineNum, ev.PrevHash, expectedPrev)
		}
		expectedHash := computeEventHash(ev)
		if ev.Hash != expectedHash {
			return lineNum - 1, fmt.Errorf("line %d: hash mismatch (got %q, expected %q)", lineNum, ev.Hash, expectedHash)
		}
		expectedPrev = ev.Hash
	}
	if err := scanner.Err(); err != nil {
		return lineNum, err
	}
	return lineNum, nil
}

// Log appends event to the audit trail. Audit write errors are logged to stderr
// rather than returned so logging failures cannot abort in-flight operations.
func Log(event Event) {
	mu.Lock()
	defer mu.Unlock()

	event.Timestamp = time.Now().UTC().Format(time.RFC3339)

	auditFile := filePath()
	logDir := filepath.Dir(auditFile)

	if err := os.MkdirAll(logDir, 0700); err != nil {
		log.Printf("ERROR: [Audit Subsystem] Failed to create audit log directory %q: %v", logDir, err)
		return
	}

	prevHash := resolvePrevHash(auditFile)

	f, err := os.OpenFile(auditFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		// Fall back to the working directory when running unprivileged without access to /var/log.
		if pathOverride == "" {
			auditFile = "audit.jsonl"
			prevHash = resolvePrevHash(auditFile)
			f, err = os.OpenFile(auditFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
		}
		if err != nil {
			log.Printf("ERROR: [Audit Subsystem] Failed to open audit log %q: %v", auditFile, err)
			return
		}
	}
	defer f.Close()

	// Tighten permissions if the file already existed with broader access bits.
	_ = os.Chmod(auditFile, 0600)

	event.PrevHash = prevHash
	event.Hash = computeEventHash(event)

	b, err := json.Marshal(event)
	if err != nil {
		log.Printf("ERROR: [Audit Subsystem] Failed to marshal audit event: %v", err)
		return
	}

	if _, err := f.Write(b); err != nil {
		log.Printf("ERROR: [Audit Subsystem] Failed to write audit event: %v", err)
		return
	}
	if _, err := f.Write([]byte("\n")); err != nil {
		log.Printf("ERROR: [Audit Subsystem] Failed to write newline to audit log: %v", err)
		return
	}
	lastHashByFile[auditFile] = event.Hash
}

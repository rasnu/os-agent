package main

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"rasnuos-agent/os_adapter"
)

type AuditEvent struct {
	Timestamp  string `json:"timestamp"`
	Action     string `json:"action"`
	PipelineID string `json:"pipeline_id,omitempty"`
	StepID     string `json:"step_id,omitempty"`
	Command    string `json:"command,omitempty"`
	RunAsUser  string `json:"run_as_user,omitempty"`
	Success    bool   `json:"success"`
	Error      string `json:"error,omitempty"`
}

var (
	auditMutex            sync.Mutex
	auditFilePathOverride string
)

func getAuditFilePath() string {
	if auditFilePathOverride != "" {
		return auditFilePathOverride
	}
	logFile := os_adapter.Current().LogFilePath()
	logDir := filepath.Dir(logFile)
	return filepath.Join(logDir, "audit.jsonl")
}

func logAuditEvent(event AuditEvent) {
	auditMutex.Lock()
	defer auditMutex.Unlock()

	event.Timestamp = time.Now().UTC().Format(time.RFC3339)

	auditFile := getAuditFilePath()
	logDir := filepath.Dir(auditFile)

	if err := os.MkdirAll(logDir, 0700); err != nil {
		log.Printf("ERROR: [Audit Subsystem] Failed to create audit log directory %q: %v", logDir, err)
		return
	}

	f, err := os.OpenFile(auditFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		// Fallback to local directory if system log path is inaccessible
		if auditFilePathOverride == "" {
			auditFile = "audit.jsonl"
			f, err = os.OpenFile(auditFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
		}
		if err != nil {
			log.Printf("ERROR: [Audit Subsystem] Failed to open audit log %q: %v", auditFile, err)
			return
		}
	}
	defer f.Close()

	// Enforce 0600 permissions even if the file was created with looser permissions previously
	_ = os.Chmod(auditFile, 0600)

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
	}
}

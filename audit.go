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

var auditMutex sync.Mutex

func logAuditEvent(event AuditEvent) {
	auditMutex.Lock()
	defer auditMutex.Unlock()

	event.Timestamp = time.Now().UTC().Format(time.RFC3339)

	logFile := os_adapter.Current().LogFilePath()
	logDir := filepath.Dir(logFile)
	auditFile := filepath.Join(logDir, "audit.jsonl")

	if err := os.MkdirAll(logDir, 0755); err != nil {
		log.Printf("ERROR: [Audit Subsystem] Failed to create audit log directory %q: %v", logDir, err)
		return
	}

	f, err := os.OpenFile(auditFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		log.Printf("ERROR: [Audit Subsystem] Failed to open audit log %q: %v", auditFile, err)
		return
	}
	defer f.Close()

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

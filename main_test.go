package main

import (
	"bytes"
	"io"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestMain_VersionFlag(t *testing.T) {
	// Redirect stdout to capture output
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := run([]string{"--version"})
	
	w.Close()
	os.Stdout = oldStdout

	if err != nil {
		t.Fatalf("Expected nil error for --version, got %v", err)
	}

	var buf bytes.Buffer
	io.Copy(&buf, r)
	output := buf.String()

	if !strings.Contains(output, "rasnuone-agent") {
		t.Errorf("Expected output to contain 'rasnuone-agent', got %q", output)
	}
}

func TestMain_MissingFlags(t *testing.T) {
	// Temporarily clear env vars to ensure we fall back to flags
	os.Unsetenv("BEARER_TOKEN")
	os.Unsetenv("SERVER_ID")
	
	err := run([]string{})
	if err == nil {
		t.Fatalf("Expected error for missing token/server-id, got nil")
	}
	
	if !strings.Contains(err.Error(), "are required") {
		t.Errorf("Expected 'are required' error, got %v", err)
	}
}

func TestMain_GracefulInterrupt(t *testing.T) {
	// Setup env vars so run() passes the required flags check
	os.Setenv("BEARER_TOKEN", "test-token")
	os.Setenv("SERVER_ID", "test-server-id")
	defer os.Unsetenv("BEARER_TOKEN")
	defer os.Unsetenv("SERVER_ID")

	errCh := make(chan error, 1)

	go func() {
		errCh <- run([]string{})
	}()

	// Wait briefly for the agent to start and set up signal handlers
	time.Sleep(200 * time.Millisecond)

	// Send SIGTERM to our own process
	// The run() function's signal.Notify should catch this and exit cleanly.
	p, err := os.FindProcess(os.Getpid())
	if err != nil {
		t.Fatalf("Failed to find process: %v", err)
	}
	p.Signal(syscall.SIGTERM)

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Expected clean exit, got error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("Timeout waiting for run() to exit after SIGTERM")
	}
}

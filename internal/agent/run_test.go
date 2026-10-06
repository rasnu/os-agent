// Copyright © 2026 Rasnu LLC (rasnu.com). All rights reserved.
// Licensed under the Functional Source License, Version 1.1, ALv2 Future License (FSL-1.1-ALv2).

package agent

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

	if !strings.Contains(output, "rasnuos-agent") {
		t.Errorf("Expected output to contain 'rasnuos-agent', got %q", output)
	}
}

func TestMain_MissingFlags(t *testing.T) {
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
	os.Setenv("BEARER_TOKEN", "test-token")
	os.Setenv("SERVER_ID", "test-server-id")
	os.Setenv("METRICS_PORT", "0")
	defer os.Unsetenv("BEARER_TOKEN")
	defer os.Unsetenv("SERVER_ID")
	defer os.Unsetenv("METRICS_PORT")

	errCh := make(chan error, 1)

	go func() {
		errCh <- run([]string{})
	}()

	time.Sleep(300 * time.Millisecond)

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
	case <-time.After(10 * time.Second):
		t.Fatalf("Timeout waiting for run() to exit after SIGTERM")
	}
}

func TestMain_KeyInfoFlag(t *testing.T) {
	origKey := AgentPublicKeyBase64
	defer func() { AgentPublicKeyBase64 = origKey }()

	AgentPublicKeyBase64 = ""
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := run([]string{"--key-info"})
	w.Close()
	os.Stdout = oldStdout

	if err == nil {
		t.Errorf("Expected error for --key-info when unconfigured")
	}
	var buf bytes.Buffer
	io.Copy(&buf, r)
	if !strings.Contains(buf.String(), "unconfigured") {
		t.Errorf("Expected 'unconfigured' in output, got: %s", buf.String())
	}

	testKey := "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="
	AgentPublicKeyBase64 = testKey
	r2, w2, _ := os.Pipe()
	os.Stdout = w2

	err = run([]string{"--key-info"})
	w2.Close()
	os.Stdout = oldStdout

	if err != nil {
		t.Errorf("Expected nil error for --key-info when configured, got: %v", err)
	}
	var buf2 bytes.Buffer
	io.Copy(&buf2, r2)
	if !strings.Contains(buf2.String(), "configured") || !strings.Contains(buf2.String(), testKey) {
		t.Errorf("Expected 'configured' and key in output, got: %s", buf2.String())
	}
}

// Copyright © 2026 Rasnu LLC (rasnu.com). All rights reserved.
// Licensed under the Functional Source License, Version 1.1, ALv2 Future License (FSL-1.1-ALv2).

package agent

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"os/exec"
	"testing"
	"time"

	"rasnuos-agent/internal/transport"

	"rasnuos-agent/internal/osadapter"
)

func TestExecuteRawCommand(t *testing.T) {
	originalClient := transport.Default()
	mockHTTP := &MockHTTPClient{
		Responses: []*http.Response{
			{
				StatusCode: 200,
				Body:       io.NopCloser(bytes.NewBufferString(`{"status":"ok"}`)),
			},
		},
	}
	transport.SetDefault(mockHTTP)
	defer func() { transport.SetDefault(originalClient) }()

	originalAdapter := osadapter.Current()
	mockAdapter := &MockOSAdapter{
		MockShellOutput: "hello world",
	}
	osadapter.SetAdapter(mockAdapter)
	defer osadapter.SetAdapter(originalAdapter)

	client := &PusherClient{
		ApiURL: "http://test",
		Token:  "test-token",
	}

	client.executeRawCommand("req-1", "echo hello world", "")
}

func TestExecuteRawCommand_Timeout(t *testing.T) {
	originalClient := transport.Default()

	var capturedBody string
	mockHTTP := &MockHTTPClient{
		Responses: []*http.Response{
			{
				StatusCode: 200,
				Body:       io.NopCloser(bytes.NewBufferString(`{"status":"ok"}`)),
			},
		},
		OnRequest: func(req *http.Request) {
			b, _ := io.ReadAll(req.Body)
			capturedBody = string(b)
		},
	}
	transport.SetDefault(mockHTTP)
	defer func() { transport.SetDefault(originalClient) }()

	originalAdapter := osadapter.Current()
	mockAdapter := &MockOSAdapter{
		GetShellCmdFunc: func(ctx context.Context, script string, runAsUser string) *exec.Cmd {
			return exec.CommandContext(ctx, "sleep", "2")
		},
	}
	osadapter.SetAdapter(mockAdapter)
	defer osadapter.SetAdapter(originalAdapter)

	originalTimeout := commandTimeout
	commandTimeout = 100 * time.Millisecond
	defer func() { commandTimeout = originalTimeout }()

	client := &PusherClient{
		ApiURL: "http://test",
		Token:  "test-token",
	}

	client.executeRawCommand("req-timeout", "sleep 2", "")

	if !bytes.Contains([]byte(capturedBody), []byte(`"success":false`)) {
		t.Errorf("Expected success:false in completion request due to timeout, got: %s", capturedBody)
	}
	if !bytes.Contains([]byte(capturedBody), []byte(`killed`)) && !bytes.Contains([]byte(capturedBody), []byte(`signal`)) {
		t.Errorf("Expected log to mention 'killed' or 'signal' due to context cancellation, got: %s", capturedBody)
	}
}

func TestExecuteDiscovery(t *testing.T) {
	originalClient := transport.Default()
	mockHTTP := &MockHTTPClient{
		Responses: []*http.Response{
			{
				StatusCode: 200,
				Body:       io.NopCloser(bytes.NewBufferString(`{"status":"ok"}`)),
			},
		},
	}
	transport.SetDefault(mockHTTP)
	defer func() { transport.SetDefault(originalClient) }()

	originalAdapter := osadapter.Current()
	mockAdapter := &MockOSAdapter{
		MockShellOutput: "discovery data",
	}
	osadapter.SetAdapter(mockAdapter)
	defer osadapter.SetAdapter(originalAdapter)

	client := &PusherClient{
		ServerID: "test-server",
		ApiURL:   "http://test",
		Token:    "test-token",
	}

	client.executeDiscovery("test-script.sh")
}

func TestStreamBatcher_Flush(t *testing.T) {
	originalClient := transport.Default()
	mockHTTP := &MockHTTPClient{
		Responses: []*http.Response{
			{
				StatusCode: 200,
				Body:       io.NopCloser(bytes.NewBufferString(`{"status":"ok"}`)),
			},
		},
	}
	transport.SetDefault(mockHTTP)
	defer func() { transport.SetDefault(originalClient) }()

	client := &PusherClient{
		ServerID: "test-server",
		ApiURL:   "http://test",
		Token:    "test-token",
	}

	client.closed = true

	logBuf := &LogBuffer{}
	batcher := NewStreamBatcher(client, "pipe-1", "step-1", logBuf)

	batcher.Write("chunk1\n")
	batcher.Write("chunk2\n")

	batcher.flushWS()
	batcher.flushHTTP()

	batcher.Write("chunk3\n")
	batcher.Close()
	client.wg.Wait()

	if logBuf.String() != "chunk1\nchunk2\nchunk3\n" {
		t.Errorf("Expected full log buffer, got %q", logBuf.String())
	}
}

func TestExecuteRawCommand_GuardrailBlock(t *testing.T) {
	originalClient := transport.Default()
	var capturedBody string
	mockHTTP := &MockHTTPClient{
		Responses: []*http.Response{
			{
				StatusCode: 200,
				Body:       io.NopCloser(bytes.NewBufferString(`{"status":"ok"}`)),
			},
		},
		OnRequest: func(req *http.Request) {
			b, _ := io.ReadAll(req.Body)
			capturedBody = string(b)
		},
	}
	transport.SetDefault(mockHTTP)
	defer func() { transport.SetDefault(originalClient) }()

	shellExecuted := false
	originalAdapter := osadapter.Current()
	mockAdapter := &MockOSAdapter{
		GetShellCmdFunc: func(ctx context.Context, script string, runAsUser string) *exec.Cmd {
			shellExecuted = true
			return exec.CommandContext(ctx, "echo", "should not run")
		},
	}
	osadapter.SetAdapter(mockAdapter)
	defer osadapter.SetAdapter(originalAdapter)

	client := &PusherClient{
		ApiURL: "http://test",
		Token:  "test-token",
	}

	client.executeRawCommand("req-destructive", "rm -rf /", "")

	if shellExecuted {
		t.Errorf("Expected shell command NOT to execute for destructive command rm -rf /")
	}
	if !bytes.Contains([]byte(capturedBody), []byte(`"success":false`)) {
		t.Errorf("Expected success:false in completion request, got: %s", capturedBody)
	}
	if !bytes.Contains([]byte(capturedBody), []byte(`GUARDRAIL BLOCK`)) {
		t.Errorf("Expected log to mention 'GUARDRAIL BLOCK', got: %s", capturedBody)
	}
}

func TestExecuteAndStream_GuardrailBlock(t *testing.T) {
	originalClient := transport.Default()
	var capturedBody string
	mockHTTP := &MockHTTPClient{
		Responses: []*http.Response{
			{
				StatusCode: 200,
				Body:       io.NopCloser(bytes.NewBufferString(`{"status":"ok"}`)),
			},
		},
		OnRequest: func(req *http.Request) {
			b, _ := io.ReadAll(req.Body)
			capturedBody = string(b)
		},
	}
	transport.SetDefault(mockHTTP)
	defer func() { transport.SetDefault(originalClient) }()

	shellExecuted := false
	originalAdapter := osadapter.Current()
	mockAdapter := &MockOSAdapter{
		GetShellCmdFunc: func(ctx context.Context, script string, runAsUser string) *exec.Cmd {
			shellExecuted = true
			return exec.CommandContext(ctx, "echo", "should not run")
		},
	}
	osadapter.SetAdapter(mockAdapter)
	defer osadapter.SetAdapter(originalAdapter)

	client := &PusherClient{
		ApiURL: "http://test",
		Token:  "test-token",
		closed: true,
	}

	client.executeAndStream("pipe-1", "step-destructive", "rm -rf /*", "", 60)

	if shellExecuted {
		t.Errorf("Expected shell command NOT to execute for destructive command rm -rf /*")
	}
	if !bytes.Contains([]byte(capturedBody), []byte(`"success":false`)) {
		t.Errorf("Expected success:false in completion request, got: %s", capturedBody)
	}
	if !bytes.Contains([]byte(capturedBody), []byte(`GUARDRAIL BLOCK`)) {
		t.Errorf("Expected log to mention 'GUARDRAIL BLOCK', got: %s", capturedBody)
	}
}

func TestExecuteComputerIntent(t *testing.T) {
	originalClient := transport.Default()
	var capturedBodies []string
	mockHTTP := &MockHTTPClient{
		OnRequest: func(req *http.Request) {
			if req.Body != nil {
				b, _ := io.ReadAll(req.Body)
				capturedBodies = append(capturedBodies, string(b))
			}
		},
	}
	transport.SetDefault(mockHTTP)
	defer func() { transport.SetDefault(originalClient) }()

	originalAdapter := osadapter.Current()
	defer osadapter.SetAdapter(originalAdapter)

	client := &PusherClient{
		ApiURL:   "http://test",
		Token:    "test-token",
		ServerID: "srv-1",
		closed:   true,
	}

	// 1. Invalid prefix
	client.executeComputerIntent("p1", "s1", "wrong-prefix")
	if len(capturedBodies) == 0 || !bytes.Contains([]byte(capturedBodies[len(capturedBodies)-1]), []byte("Invalid computer intent prefix")) {
		t.Errorf("Expected invalid prefix completion error")
	}

	// 2. Invalid JSON
	client.executeComputerIntent("p1", "s2", "[COMPUTER_INTENT] {bad-json")
	if !bytes.Contains([]byte(capturedBodies[len(capturedBodies)-1]), []byte("Invalid computer intent json")) {
		t.Errorf("Expected invalid JSON completion error")
	}

	// 3. Screenshot success with Base64 framing
	osadapter.SetAdapter(&MockOSAdapter{
		ExecuteComputerActionFunc: func(action, text string, x, y int) ([]byte, error) {
			return []byte("fake-png-binary-bytes"), nil
		},
	})
	client.executeComputerIntent("p1", "s3", `[COMPUTER_INTENT] {"action":"screenshot","coordinate":[100,200]}`)
	if !bytes.Contains([]byte(capturedBodies[len(capturedBodies)-1]), []byte("SCREENSHOT_BASE64_START")) {
		t.Errorf("Expected screenshot base64 payload in completion log, got: %s", capturedBodies[len(capturedBodies)-1])
	}

	// 4. Non-screenshot action returning text output
	osadapter.SetAdapter(&MockOSAdapter{
		ExecuteComputerActionFunc: func(action, text string, x, y int) ([]byte, error) {
			return []byte("clicked at 100,200"), nil
		},
	})
	client.executeComputerIntent("p1", "s4", `[COMPUTER_INTENT] {"action":"left_click","coordinate":[100,200]}`)
	if !bytes.Contains([]byte(capturedBodies[len(capturedBodies)-1]), []byte("clicked at 100,200")) {
		t.Errorf("Expected action text output in completion log")
	}

	// 5. Action error branch
	osadapter.SetAdapter(&MockOSAdapter{
		ExecuteComputerActionFunc: func(action, text string, x, y int) ([]byte, error) {
			return nil, context.DeadlineExceeded
		},
	})
	client.executeComputerIntent("p1", "s5", `[COMPUTER_INTENT] {"action":"type","text":"hello"}`)
	if !bytes.Contains([]byte(capturedBodies[len(capturedBodies)-1]), []byte("GUI action failed")) {
		t.Errorf("Expected GUI action failed message in completion log")
	}
}

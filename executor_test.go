package main

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"os/exec"
	"testing"
	"time"

	"rasnuone-agent/os_adapter"
)

func TestExecuteRawCommand(t *testing.T) {
	originalClient := defaultHTTPClient
	mockHTTP := &MockHTTPClient{
		Responses: []*http.Response{
			{
				StatusCode: 200,
				Body:       io.NopCloser(bytes.NewBufferString(`{"status":"ok"}`)),
			},
		},
	}
	defaultHTTPClient = mockHTTP
	defer func() { defaultHTTPClient = originalClient }()

	originalAdapter := os_adapter.Current()
	mockAdapter := &MockOSAdapter{
		MockShellOutput: "hello world",
	}
	os_adapter.SetAdapter(mockAdapter)
	defer os_adapter.SetAdapter(originalAdapter)

	client := &PusherClient{
		ApiURL: "http://test",
		Token:  "test-token",
	}

	client.executeRawCommand("req-1", "echo hello world", "")
}

func TestExecuteRawCommand_Timeout(t *testing.T) {
	originalClient := defaultHTTPClient

	// We want to capture the request to see the final completion status
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
	defaultHTTPClient = mockHTTP
	defer func() { defaultHTTPClient = originalClient }()

	originalAdapter := os_adapter.Current()
	mockAdapter := &MockOSAdapter{
		// Instead of "echo", we'll tell the mock to run a sleep command
		GetShellCmdFunc: func(ctx context.Context, script string, runAsUser string) *exec.Cmd {
			return exec.CommandContext(ctx, "sleep", "2")
		},
	}
	os_adapter.SetAdapter(mockAdapter)
	defer os_adapter.SetAdapter(originalAdapter)

	// Make the timeout very short so it triggers before the sleep 2 finishes
	originalTimeout := commandTimeout
	commandTimeout = 100 * time.Millisecond
	defer func() { commandTimeout = originalTimeout }()

	client := &PusherClient{
		ApiURL: "http://test",
		Token:  "test-token",
	}

	client.executeRawCommand("req-timeout", "sleep 2", "")

	// The mock HTTP client should receive a completion request with success = false
	// and a log indicating it was killed/cancelled.
	if !bytes.Contains([]byte(capturedBody), []byte(`"success":false`)) {
		t.Errorf("Expected success:false in completion request due to timeout, got: %s", capturedBody)
	}
	if !bytes.Contains([]byte(capturedBody), []byte(`killed`)) && !bytes.Contains([]byte(capturedBody), []byte(`signal`)) {
		t.Errorf("Expected log to mention 'killed' or 'signal' due to context cancellation, got: %s", capturedBody)
	}
}

func TestExecuteDiscovery(t *testing.T) {
	originalClient := defaultHTTPClient
	mockHTTP := &MockHTTPClient{
		Responses: []*http.Response{
			{
				StatusCode: 200,
				Body:       io.NopCloser(bytes.NewBufferString(`{"status":"ok"}`)),
			},
		},
	}
	defaultHTTPClient = mockHTTP
	defer func() { defaultHTTPClient = originalClient }()

	originalAdapter := os_adapter.Current()
	mockAdapter := &MockOSAdapter{
		MockShellOutput: "discovery data",
	}
	os_adapter.SetAdapter(mockAdapter)
	defer os_adapter.SetAdapter(originalAdapter)

	client := &PusherClient{
		ServerID: "test-server",
		ApiURL:   "http://test",
		Token:    "test-token",
	}

	client.executeDiscovery("test-script.sh")
}

func TestStreamBatcher_Flush(t *testing.T) {
	originalClient := defaultHTTPClient
	mockHTTP := &MockHTTPClient{
		Responses: []*http.Response{
			{
				StatusCode: 200,
				Body:       io.NopCloser(bytes.NewBufferString(`{"status":"ok"}`)),
			},
		},
	}
	defaultHTTPClient = mockHTTP
	defer func() { defaultHTTPClient = originalClient }()

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

	if logBuf.String() != "chunk1\nchunk2\nchunk3\n" {
		t.Errorf("Expected full log buffer, got %q", logBuf.String())
	}
}

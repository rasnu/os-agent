// Copyright © 2026 Rasnu LLC (rasnu.com). All rights reserved.
// Licensed under the Functional Source License, Version 1.1, ALv2 Future License (FSL-1.1-ALv2).

package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"rasnuos-agent/internal/transport"
)

func TestHandleMigrationCommand(t *testing.T) {
	client := &PusherClient{ApiURL: "http://fake", Token: "test-token"}

	client.handleMigrationCommand("unknown", map[string]interface{}{})
	client.handleMigrationCommand("prepare-migration-receiver", map[string]interface{}{})
	client.handleMigrationCommand("execute-migration-sender", map[string]interface{}{})
}

func TestMigrationEngine_SingleFilePush(t *testing.T) {
	var mu sync.Mutex
	var webhookPayload map[string]interface{}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var p map[string]interface{}
		json.Unmarshal(body, &p)
		mu.Lock()
		if r.URL.Path == "/agent/migration/complete" {
			webhookPayload = p
		}
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	originalClient := transport.Default()
	transport.SetDefault(ts.Client())
	defer func() { transport.SetDefault(originalClient) }()

	client := &PusherClient{ApiURL: ts.URL, Token: "test-token"}

	tempDir, _ := os.MkdirTemp("", "migration_test")
	defer os.RemoveAll(tempDir)

	senderDir := filepath.Join(tempDir, "sender")
	receiverDir := filepath.Join(tempDir, "receiver")
	os.MkdirAll(senderDir, 0755)
	os.MkdirAll(receiverDir, 0755)

	sourceFile := filepath.Join(senderDir, "test.txt")
	targetFile := filepath.Join(receiverDir, "test.txt")
	os.WriteFile(sourceFile, []byte("super secret migration data"), 0644)

	token := "secure-token-123"

	client.prepareMigrationReceiver("pipe-1", "step-1", targetFile, token, 0)
	client.wg.Wait()

	if success, ok := webhookPayload["success"].(bool); !ok || !success {
		t.Fatalf("prepareMigrationReceiver failed: %v", webhookPayload["log"])
	}

	data := webhookPayload["data"].(map[string]interface{})
	port := int(data["port"].(float64))
	fingerprint := data["fingerprint"].(string)
	rsyncPassword := data["rsync_password"].(string)

	client.executeMigrationSender("pipe-1", "step-2", "push", sourceFile, targetFile, "127.0.0.1", port, token, fingerprint, rsyncPassword)
	client.wg.Wait()

	if success, ok := webhookPayload["success"].(bool); !ok || !success {
		t.Fatalf("executeMigrationSender failed: %v", webhookPayload["log"])
	}

	content, err := os.ReadFile(targetFile)
	if err != nil {
		t.Fatalf("Target file not created: %v", err)
	}
	if string(content) != "super secret migration data" {
		t.Errorf("Content mismatch. Got: %s", string(content))
	}

	shutdownMigrationServer(token)
}

func TestMigrationEngine_Security_FingerprintMismatch(t *testing.T) {
	var mu sync.Mutex
	var webhookPayload map[string]interface{}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var p map[string]interface{}
		json.Unmarshal(body, &p)
		mu.Lock()
		if r.URL.Path == "/agent/migration/complete" {
			webhookPayload = p
		}
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	originalClient := transport.Default()
	transport.SetDefault(ts.Client())
	defer func() { transport.SetDefault(originalClient) }()

	client := &PusherClient{ApiURL: ts.URL, Token: "test"}

	tempDir, _ := os.MkdirTemp("", "migration_test_sec")
	defer os.RemoveAll(tempDir)
	targetFile := filepath.Join(tempDir, "test.txt")
	sourceFile := filepath.Join(tempDir, "source.txt")
	os.WriteFile(sourceFile, []byte("data"), 0644)

	client.prepareMigrationReceiver("p1", "s1", targetFile, "token", 0)
	client.wg.Wait()
	data := webhookPayload["data"].(map[string]interface{})
	port := int(data["port"].(float64))

	badFingerprint := hex.EncodeToString(sha256.New().Sum([]byte("fake")))

	client.executeMigrationSender("p1", "s2", "push", sourceFile, targetFile, "127.0.0.1", port, "token", badFingerprint, "")
	client.wg.Wait()

	if success, ok := webhookPayload["success"].(bool); ok && success {
		t.Fatalf("Sender should have failed due to fingerprint mismatch, but succeeded!")
	}

	shutdownMigrationServer("token")
}

func TestMigrationEngine_DirectoryPull(t *testing.T) {
	var mu sync.Mutex
	var webhookPayload map[string]interface{}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var p map[string]interface{}
		json.Unmarshal(body, &p)
		mu.Lock()
		if r.URL.Path == "/agent/migration/complete" {
			webhookPayload = p
		}
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	originalClient := transport.Default()
	transport.SetDefault(ts.Client())
	defer func() { transport.SetDefault(originalClient) }()

	client := &PusherClient{ApiURL: ts.URL, Token: "test-token"}

	tempDir, _ := os.MkdirTemp("", "migration_dir_test")
	defer os.RemoveAll(tempDir)

	sourceDir := filepath.Join(tempDir, "source")
	targetDir := filepath.Join(tempDir, "target")
	os.MkdirAll(sourceDir, 0755)

	os.WriteFile(filepath.Join(sourceDir, "file1.txt"), []byte("file1"), 0644)
	os.WriteFile(filepath.Join(sourceDir, "file2.txt"), []byte("file2"), 0644)

	token := "secure-token"

	client.prepareMigrationReceiver("pipe", "step1", sourceDir, token, 0)
	client.wg.Wait()
	data := webhookPayload["data"].(map[string]interface{})
	port := int(data["port"].(float64))
	fingerprint := data["fingerprint"].(string)
	rsyncPassword := data["rsync_password"].(string)

	client.executeMigrationSender("pipe", "step2", "pull", sourceDir, targetDir+"/", "127.0.0.1", port, token, fingerprint, rsyncPassword)
	client.wg.Wait()

	if success, ok := webhookPayload["success"].(bool); !ok || !success {
		t.Fatalf("Directory pull failed: %v", webhookPayload["log"])
	}

	f1, err := os.ReadFile(filepath.Join(targetDir, "source", "file1.txt"))
	if err != nil || string(f1) != "file1" {
		t.Errorf("Extraction failed for file1")
	}
	f2, err := os.ReadFile(filepath.Join(targetDir, "source", "file2.txt"))
	if err != nil || string(f2) != "file2" {
		t.Errorf("Extraction failed for file2")
	}

	shutdownMigrationServer(token)
}

func TestMigrationEngine_NetworkInterrupt(t *testing.T) {
	var mu sync.Mutex
	var webhookPayload map[string]interface{}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var p map[string]interface{}
		json.Unmarshal(body, &p)
		mu.Lock()
		if r.URL.Path == "/agent/migration/complete" {
			webhookPayload = p
		}
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	originalExec := execCommand
	execCommand = func(name string, arg ...string) *exec.Cmd {
		return exec.CommandContext(context.Background(), "sh", "-c", "echo 'rsync: connection unexpectedly closed' >&2; exit 12")
	}
	defer func() { execCommand = originalExec }()

	originalClient := transport.Default()
	transport.SetDefault(ts.Client())
	defer func() { transport.SetDefault(originalClient) }()

	client := &PusherClient{ApiURL: ts.URL, Token: "test-token"}

	client.executeMigrationSender("pipe-interrupt", "step-interrupt", "push", "/fake/src", "/fake/dst", "127.0.0.1", 12345, "token", "fake-fingerprint", "fake-pass")
	client.wg.Wait()

	if success, ok := webhookPayload["success"].(bool); ok && success {
		t.Fatalf("executeMigrationSender should have failed due to network interrupt, but it succeeded")
	}

	logStr, ok := webhookPayload["log"].(string)
	if !ok || (!contains(logStr, "rsync: connection unexpectedly closed") && !contains(logStr, "exit status 12")) {
		t.Errorf("Expected log to contain rsync failure details, got: %s", logStr)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && s[:len(substr)] == substr || bytesContains(s, substr)
}

func bytesContains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func TestMigrationEngine_P2pGuardrails(t *testing.T) {
	var mu sync.Mutex
	var webhookPayload map[string]interface{}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var p map[string]interface{}
		json.Unmarshal(body, &p)
		mu.Lock()
		if r.URL.Path == "/agent/migration/complete" {
			webhookPayload = p
		}
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	originalClient := transport.Default()
	transport.SetDefault(ts.Client())
	defer func() { transport.SetDefault(originalClient) }()

	client := &PusherClient{ApiURL: ts.URL, Token: "test-token"}

	client.prepareP2pReceiver("pipe-guard", "step-recv", "rm -rf /", "token-64", 0)
	client.wg.Wait()

	mu.Lock()
	if success, ok := webhookPayload["success"].(bool); !ok || success {
		t.Fatalf("Expected prepareP2pReceiver to block destructive command")
	}
	mu.Unlock()

	client.runP2pSender("pipe-guard", "step-send", "rm -rf /", "127.0.0.1", 9999, "fp", "token-64")
	client.wg.Wait()

	mu.Lock()
	if success, ok := webhookPayload["success"].(bool); !ok || success {
		t.Fatalf("Expected runP2pSender to block destructive command")
	}
	mu.Unlock()
}

func TestValidateTransferURL(t *testing.T) {
	if err := validateTransferURL("https://os.rasnu.com/api/agent/file/transfer/abc"); err != nil {
		t.Errorf("Expected valid HTTPS URL to pass, got: %v", err)
	}
	if err := validateTransferURL("http://169.254.169.254/latest/meta-data/"); err == nil {
		t.Errorf("Expected AWS/cloud metadata IP to be blocked")
	}
	if err := validateTransferURL("http://metadata.google.internal/computeMetadata/v1/"); err == nil {
		t.Errorf("Expected GCP metadata host to be blocked")
	}
	if err := validateTransferURL("file:///etc/passwd"); err == nil {
		t.Errorf("Expected file:// scheme to be blocked")
	}
}

func TestMigrationEngine_P2pEndToEndAndCommands(t *testing.T) {
	var mu sync.Mutex
	var webhookPayloads []map[string]interface{}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var p map[string]interface{}
		json.Unmarshal(body, &p)
		mu.Lock()
		if r.URL.Path == "/agent/migration/complete" {
			webhookPayloads = append(webhookPayloads, p)
		}
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	originalClient := transport.Default()
	transport.SetDefault(ts.Client())
	defer func() { transport.SetDefault(originalClient) }()

	client := &PusherClient{ApiURL: ts.URL, Token: "test-token"}

	tempDir, err := os.MkdirTemp("", "p2p_e2e_test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	receivedFile := filepath.Join(tempDir, "received.txt")
	p2pToken := strings.Repeat("a", 64) // 64 bytes

	// 1. Start P2P receiver via handleMigrationCommand
	client.handleMigrationCommand("prepare-p2p-receiver", map[string]interface{}{
		"pipeline_id":     "pipe-p2p",
		"step_id":         "step-recv",
		"token":           p2pToken,
		"port":            float64(0),
		"receive_command": "cat > " + receivedFile,
	})
	client.wg.Wait()

	mu.Lock()
	if len(webhookPayloads) == 0 {
		mu.Unlock()
		t.Fatalf("Expected completion webhook from prepare-p2p-receiver")
	}
	recvPayload := webhookPayloads[len(webhookPayloads)-1]
	mu.Unlock()

	if !recvPayload["success"].(bool) {
		t.Fatalf("prepare-p2p-receiver failed: %v", recvPayload["log"])
	}
	data := recvPayload["data"].(map[string]interface{})
	port := int(data["port"].(float64))
	fingerprint := data["fingerprint"].(string)

	// 2. Run P2P sender via handleMigrationCommand
	client.handleMigrationCommand("run-p2p-sender", map[string]interface{}{
		"pipeline_id":  "pipe-p2p",
		"step_id":      "step-send",
		"token":        p2pToken,
		"host":         "127.0.0.1",
		"port":         float64(port),
		"fingerprint":  fingerprint,
		"send_command": "printf 'hello-p2p-stream-data'",
	})
	client.wg.Wait()

	mu.Lock()
	sendPayload := webhookPayloads[len(webhookPayloads)-1]
	mu.Unlock()

	if !sendPayload["success"].(bool) {
		t.Fatalf("run-p2p-sender failed: %v", sendPayload["log"])
	}

	// Verify the receiver command finished writing the file
	shutdownMigrationServer(p2pToken)
	gotBytes, err := os.ReadFile(receivedFile)
	if err != nil {
		t.Fatalf("Failed to read received P2P file: %v", err)
	}
	if string(gotBytes) != "hello-p2p-stream-data" {
		t.Errorf("Expected 'hello-p2p-stream-data', got %q", string(gotBytes))
	}

	// 3. Test progressWriter directly (including >1s report branch)
	pw := &progressWriter{
		pipelineId: "pipe-p2p",
		stepId:     "step-pw",
		client:     client,
	}
	n, err := pw.Write([]byte("chunk-of-bytes"))
	if err != nil || n != len("chunk-of-bytes") {
		t.Errorf("Unexpected progressWriter.Write result: n=%d, err=%v", n, err)
	}

	// 4. Test remaining handleMigrationCommand branches (stop-migration-receiver, stop-p2p-receiver, inject-secret, unknown)
	client.handleMigrationCommand("stop-migration-receiver", map[string]interface{}{
		"pipeline_id": "pipe-p2p",
		"step_id":     "step-stop-mig",
		"token":       p2pToken,
	})
	client.handleMigrationCommand("stop-p2p-receiver", map[string]interface{}{
		"pipeline_id": "pipe-p2p",
		"step_id":     "step-stop-p2p",
		"token":       p2pToken,
	})
	client.handleMigrationCommand("inject-secret", map[string]interface{}{
		"pipeline_id": "pipe-p2p",
		"step_id":     "step-secret",
		"token":       p2pToken,
		"key":         "DB_PASS",
		"value":       "secret123",
	})
	client.handleMigrationCommand("unknown-action-xyz", map[string]interface{}{
		"pipeline_id": "pipe-p2p",
		"step_id":     "step-unk",
		"token":       p2pToken,
	})
}

package main

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
	"sync"
	"testing"
)

func TestHandleMigrationCommand(t *testing.T) {
	client := &PusherClient{ApiURL: "http://fake", Token: "test-token"}

	// Test unknown command
	client.handleMigrationCommand("unknown", map[string]interface{}{})

	// Test missing args for prepare
	client.handleMigrationCommand("prepare-migration-receiver", map[string]interface{}{})

	// Test missing args for sender
	client.handleMigrationCommand("execute-migration-sender", map[string]interface{}{})

	// Coverage will just run these branches, ensuring no panic.
}

func TestMigrationEngine_SingleFilePush(t *testing.T) {
	// 1. Setup mock Laravel Webhook
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

	originalClient := getDefaultHTTPClient()
	setDefaultHTTPClient(ts.Client())
	defer func() { setDefaultHTTPClient(originalClient) }()

	client := &PusherClient{ApiURL: ts.URL, Token: "test-token"}

	// 2. Setup temp directories for Sender and Receiver
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

	// 3. Start Receiver
	client.prepareMigrationReceiver("pipe-1", "step-1", targetFile, token, 0)
	client.wg.Wait()

	if success, ok := webhookPayload["success"].(bool); !ok || !success {
		t.Fatalf("prepareMigrationReceiver failed: %v", webhookPayload["log"])
	}

	data := webhookPayload["data"].(map[string]interface{})
	port := int(data["port"].(float64))
	fingerprint := data["fingerprint"].(string)
	rsyncPassword := data["rsync_password"].(string)

	// 4. Start Sender
	client.executeMigrationSender("pipe-1", "step-2", "push", sourceFile, targetFile, "127.0.0.1", port, token, fingerprint, rsyncPassword)
	client.wg.Wait()

	if success, ok := webhookPayload["success"].(bool); !ok || !success {
		t.Fatalf("executeMigrationSender failed: %v", webhookPayload["log"])
	}

	// 5. Verify the file arrived
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

	originalClient := getDefaultHTTPClient()
	setDefaultHTTPClient(ts.Client())
	defer func() { setDefaultHTTPClient(originalClient) }()

	client := &PusherClient{ApiURL: ts.URL, Token: "test"}

	tempDir, _ := os.MkdirTemp("", "migration_test_sec")
	defer os.RemoveAll(tempDir)
	targetFile := filepath.Join(tempDir, "test.txt")
	sourceFile := filepath.Join(tempDir, "source.txt")
	os.WriteFile(sourceFile, []byte("data"), 0644)

	// Receiver
	client.prepareMigrationReceiver("p1", "s1", targetFile, "token", 0)
	client.wg.Wait()
	data := webhookPayload["data"].(map[string]interface{})
	port := int(data["port"].(float64))

	// BAD Fingerprint
	badFingerprint := hex.EncodeToString(sha256.New().Sum([]byte("fake")))

	// Sender should FAIL
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

	originalClient := getDefaultHTTPClient()
	setDefaultHTTPClient(ts.Client())
	defer func() { setDefaultHTTPClient(originalClient) }()

	client := &PusherClient{ApiURL: ts.URL, Token: "test-token"}

	tempDir, _ := os.MkdirTemp("", "migration_dir_test")
	defer os.RemoveAll(tempDir)

	sourceDir := filepath.Join(tempDir, "source")
	targetDir := filepath.Join(tempDir, "target")
	os.MkdirAll(sourceDir, 0755)

	os.WriteFile(filepath.Join(sourceDir, "file1.txt"), []byte("file1"), 0644)
	os.WriteFile(filepath.Join(sourceDir, "file2.txt"), []byte("file2"), 0644)

	token := "secure-token"

	// Receiver serves the directory (pull mode)
	client.prepareMigrationReceiver("pipe", "step1", sourceDir, token, 0)
	client.wg.Wait()
	data := webhookPayload["data"].(map[string]interface{})
	port := int(data["port"].(float64))
	fingerprint := data["fingerprint"].(string)
	rsyncPassword := data["rsync_password"].(string)

	// Sender pulls the directory
	client.executeMigrationSender("pipe", "step2", "pull", sourceDir, targetDir+"/", "127.0.0.1", port, token, fingerprint, rsyncPassword)
	client.wg.Wait()

	if success, ok := webhookPayload["success"].(bool); !ok || !success {
		t.Fatalf("Directory pull failed: %v", webhookPayload["log"])
	}

	// Verify extracted files
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
		// Simulate a dropped connection / rsync failure
		return exec.CommandContext(context.Background(), "sh", "-c", "echo 'rsync: connection unexpectedly closed' >&2; exit 12")
	}
	defer func() { execCommand = originalExec }()

	originalClient := getDefaultHTTPClient()
	setDefaultHTTPClient(ts.Client())
	defer func() { setDefaultHTTPClient(originalClient) }()

	client := &PusherClient{ApiURL: ts.URL, Token: "test-token"}

	// Sender attempts to push, but rsync fails (simulated by our mock)
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

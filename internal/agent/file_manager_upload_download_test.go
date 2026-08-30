// Copyright © 2026 Rasnu LLC (rasnu.com). All rights reserved.
// Licensed under the Functional Source License, Version 1.1, ALv2 Future License (FSL-1.1-ALv2).

package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"rasnuos-agent/internal/transport"
)

func TestFileManager_UploadFileToDashboard(t *testing.T) {
	expectedContent := []byte("large file content")
	hash := sha256.Sum256(expectedContent)
	expectedHash := hex.EncodeToString(hash[:])

	var receivedHash string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" && r.URL.Path == "/agent/file/upload-chunk" {
			receivedHash = r.Header.Get("X-File-Hash")
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.Method == "POST" && r.URL.Path == "/agent/complete" {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer ts.Close()

	originalClient := transport.Default()
	transport.SetDefault(ts.Client())
	transport.FileTransfer = ts.Client()
	defer func() {
		transport.SetDefault(originalClient)
		transport.FileTransfer = &http.Client{Timeout: 0}
	}()

	client := &PusherClient{ApiURL: ts.URL, Token: "test"}

	tempDir, _ := os.MkdirTemp("", "test_upload")
	defer os.RemoveAll(tempDir)
	testFile := filepath.Join(tempDir, "source.txt")
	os.WriteFile(testFile, expectedContent, 0644)

	client.uploadFileToDashboard("pipe", testFile, ts.URL+"/agent/file/upload-chunk")
	client.wg.Wait()

	if receivedHash != expectedHash {
		t.Errorf("Expected X-File-Hash %s, got %s", expectedHash, receivedHash)
	}
}

func TestFileManager_DownloadFileFromDashboard(t *testing.T) {
	expectedContent := []byte("downloaded content")
	hash := sha256.Sum256(expectedContent)
	expectedHash := hex.EncodeToString(hash[:])

	var lastLog map[string]interface{}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/agent/file/download-stream-valid" {
			w.Header().Set("X-File-Hash", expectedHash)
			w.WriteHeader(http.StatusOK)
			w.Write(expectedContent)
			return
		}
		if r.URL.Path == "/agent/file/download-stream-invalid" {
			w.Header().Set("X-File-Hash", "badhash")
			w.WriteHeader(http.StatusOK)
			w.Write(expectedContent)
			return
		}
		if r.Method == "POST" && r.URL.Path == "/agent/complete" {
			body, _ := io.ReadAll(r.Body)
			json.Unmarshal(body, &lastLog)
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer ts.Close()

	originalClient := transport.Default()
	transport.SetDefault(ts.Client())
	transport.FileTransfer = ts.Client()
	defer func() {
		transport.SetDefault(originalClient)
		transport.FileTransfer = &http.Client{Timeout: 0}
	}()

	client := &PusherClient{ApiURL: ts.URL, Token: "test"}

	tempDir, _ := os.MkdirTemp("", "test_download")
	defer os.RemoveAll(tempDir)

	targetFile1 := filepath.Join(tempDir, "target1.txt")
	client.downloadFileFromDashboard("pipe", targetFile1, ts.URL+"/agent/file/download-stream-valid")
	client.wg.Wait()

	content, err := os.ReadFile(targetFile1)
	if err != nil {
		t.Fatalf("Failed to read valid downloaded file: %v", err)
	}
	if string(content) != "downloaded content" {
		t.Errorf("Content mismatch: %s", string(content))
	}
	if success, _ := lastLog["success"].(bool); !success {
		t.Errorf("Expected success for valid hash, got log: %v", lastLog)
	}

	targetFile2 := filepath.Join(tempDir, "target2.txt")
	client.downloadFileFromDashboard("pipe", targetFile2, ts.URL+"/agent/file/download-stream-invalid")
	client.wg.Wait()

	if _, err := os.Stat(targetFile2); !os.IsNotExist(err) {
		t.Errorf("File with invalid hash was not deleted")
	}
	if success, _ := lastLog["success"].(bool); success {
		t.Errorf("Expected failure for invalid hash, got log: %v", lastLog)
	}
}

func TestFileManager_UploadFileStream(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" && r.URL.Path == "/agent/file/upload-stream" {
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.Method == "POST" && r.URL.Path == "/agent/complete" {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer ts.Close()

	originalClient := transport.Default()
	transport.SetDefault(ts.Client())
	defer func() { transport.SetDefault(originalClient) }()

	client := &PusherClient{ApiURL: ts.URL, Token: "test"}

	tempDir, _ := os.MkdirTemp("", "test_upload_stream")
	defer os.RemoveAll(tempDir)
	testFile := filepath.Join(tempDir, "source.txt")
	os.WriteFile(testFile, []byte("large file content stream"), 0644)

	client.uploadFileStream(ts.URL+"/agent/file/upload-stream", testFile)
	client.wg.Wait()
}

func TestDownloadFile_DiskFull(t *testing.T) {
	var lastLog string
	var successStatus bool

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("mock data"))
			return
		}
		if r.Method == "POST" && r.URL.Path == "/agent/complete" {
			body, _ := io.ReadAll(r.Body)
			var payload map[string]interface{}
			json.Unmarshal(body, &payload)
			lastLog = payload["log"].(string)
			successStatus = payload["success"].(bool)
			w.WriteHeader(http.StatusOK)
			return
		}
	}))
	defer ts.Close()

	originalClient := transport.Default()
	transport.SetDefault(ts.Client())
	transport.FileTransfer = ts.Client()
	defer func() {
		transport.SetDefault(originalClient)
		transport.FileTransfer = originalClient
	}()

	client := &PusherClient{ApiURL: ts.URL, Token: "test"}

	tempDir, _ := os.MkdirTemp("", "test_disk_full")
	defer os.RemoveAll(tempDir)

	readOnlyDir := filepath.Join(tempDir, "readonly")
	os.Mkdir(readOnlyDir, 0555)

	targetFile := filepath.Join(readOnlyDir, "should_fail.txt")

	client.downloadFileFromDashboard("pipe", targetFile, ts.URL+"/agent/file/download")
	client.wg.Wait()

	if successStatus {
		t.Fatalf("Download to read-only directory should have failed, but it succeeded")
	}

	if !contains(lastLog, "Failed to create target file") && !contains(lastLog, "permission denied") {
		t.Errorf("Expected log to mention create failure or permission denied, got: %s", lastLog)
	}
}

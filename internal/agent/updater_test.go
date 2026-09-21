// Copyright © 2026 Rasnu LLC (rasnu.com). All rights reserved.
// Licensed under the Functional Source License, Version 1.1, ALv2 Future License (FSL-1.1-ALv2).

package agent

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"rasnuos-agent/internal/transport"

	"rasnuos-agent/internal/osadapter"
)

func setupMockHTTP() (*MockHTTPClient, func()) {
	orig := transport.Default()
	mock := &MockHTTPClient{
		Responses: []*http.Response{
			{
				StatusCode: 200,
				Body:       io.NopCloser(bytes.NewBufferString(`{"status":"ok"}`)),
			},
		},
	}
	transport.SetDefault(mock)
	return mock, func() { transport.SetDefault(orig) }
}

func TestExecuteSelfUpdate(t *testing.T) {
	_, cleanupHTTP := setupMockHTTP()
	defer cleanupHTTP()

	pubKey, privKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("Failed to generate test keys: %v", err)
	}

	mockBinaryData := []byte("mock binary data")
	signatureBytes := ed25519.Sign(privKey, mockBinaryData)
	signatureBase64 := base64.StdEncoding.EncodeToString(signatureBytes)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write(mockBinaryData)
	}))
	defer ts.Close()

	verifyCalled := false
	replaceCalled := false

	originalAdapter := osadapter.Current()
	mockAdapter := &MockOSAdapter{
		VerifyBinaryFunc: func(path string) error {
			verifyCalled = true
			return nil
		},
		ReplaceBinaryFunc: func(tempPath, currentPath string) error {
			replaceCalled = true
			return nil
		},
		RestartAgentFunc: func() error {
			return nil
		},
	}
	osadapter.SetAdapter(mockAdapter)
	defer osadapter.SetAdapter(originalAdapter)

	client := &PusherClient{
		ServerID: "test-server",
		ApiURL:   "http://mock-backend",
	}

	originalKey := AgentPublicKeyBase64
	AgentPublicKeyBase64 = base64.StdEncoding.EncodeToString(pubKey)
	defer func() { AgentPublicKeyBase64 = originalKey }()

	client.executeSelfUpdate(ts.URL, signatureBase64, "pipe-1", "step-1")

	time.Sleep(100 * time.Millisecond)

	if !verifyCalled {
		t.Error("Expected VerifyBinary to be called for validly signed update")
	}
	if !replaceCalled {
		t.Error("Expected ReplaceBinary to be called for validly signed update")
	}
}

func TestExecuteSelfUpdate_DownloadFails(t *testing.T) {
	_, cleanupHTTP := setupMockHTTP()
	defer cleanupHTTP()

	pubKey, privKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("Failed to generate test keys: %v", err)
	}

	mockBinaryData := []byte("mock binary data")
	signatureBytes := ed25519.Sign(privKey, mockBinaryData)
	signatureBase64 := base64.StdEncoding.EncodeToString(signatureBytes)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer ts.Close()

	replaceCalled := false
	originalAdapter := osadapter.Current()
	mockAdapter := &MockOSAdapter{
		ReplaceBinaryFunc: func(tempPath, currentPath string) error {
			replaceCalled = true
			return nil
		},
	}
	osadapter.SetAdapter(mockAdapter)
	defer osadapter.SetAdapter(originalAdapter)

	client := &PusherClient{
		ServerID: "test-server",
		ApiURL:   "http://mock-backend",
	}

	originalKey := AgentPublicKeyBase64
	AgentPublicKeyBase64 = base64.StdEncoding.EncodeToString(pubKey)
	defer func() { AgentPublicKeyBase64 = originalKey }()

	client.executeSelfUpdate(ts.URL, signatureBase64, "pipe-2", "step-2")

	if replaceCalled {
		t.Error("Expected ReplaceBinary to NOT be called if download failed")
	}
}

func TestExecuteSelfUpdate_UnconfiguredPublicKeyFailsClosed(t *testing.T) {
	_, cleanupHTTP := setupMockHTTP()
	defer cleanupHTTP()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("mock binary data"))
	}))
	defer ts.Close()

	replaceCalled := false
	originalAdapter := osadapter.Current()
	mockAdapter := &MockOSAdapter{
		ReplaceBinaryFunc: func(tempPath, currentPath string) error {
			replaceCalled = true
			return nil
		},
	}
	osadapter.SetAdapter(mockAdapter)
	defer osadapter.SetAdapter(originalAdapter)

	client := &PusherClient{
		ServerID: "test-server",
		ApiURL:   "http://mock-backend",
	}

	originalKey := AgentPublicKeyBase64
	AgentPublicKeyBase64 = ""
	defer func() { AgentPublicKeyBase64 = originalKey }()

	client.executeSelfUpdate(ts.URL, "c29tZXNpZ25hdHVyZQ==", "pipe-unconf", "step-unconf")

	if replaceCalled {
		t.Error("Expected ReplaceBinary to NOT be called when AgentPublicKeyBase64 is empty")
	}

	AgentPublicKeyBase64 = "1111111111111111111111111111111111111111111="
	client.executeSelfUpdate(ts.URL, "c29tZXNpZ25hdHVyZQ==", "pipe-unconf2", "step-unconf2")

	if replaceCalled {
		t.Error("Expected ReplaceBinary to NOT be called when AgentPublicKeyBase64 is placeholder")
	}
}

func TestExecuteSelfUpdate_MissingSignatureFailsClosed(t *testing.T) {
	_, cleanupHTTP := setupMockHTTP()
	defer cleanupHTTP()

	pubKey, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("Failed to generate test keys: %v", err)
	}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("mock binary data"))
	}))
	defer ts.Close()

	replaceCalled := false
	originalAdapter := osadapter.Current()
	mockAdapter := &MockOSAdapter{
		ReplaceBinaryFunc: func(tempPath, currentPath string) error {
			replaceCalled = true
			return nil
		},
	}
	osadapter.SetAdapter(mockAdapter)
	defer osadapter.SetAdapter(originalAdapter)

	client := &PusherClient{
		ServerID: "test-server",
		ApiURL:   "http://mock-backend",
	}

	originalKey := AgentPublicKeyBase64
	AgentPublicKeyBase64 = base64.StdEncoding.EncodeToString(pubKey)
	defer func() { AgentPublicKeyBase64 = originalKey }()

	client.executeSelfUpdate(ts.URL, "", "pipe-nosig", "step-nosig")

	if replaceCalled {
		t.Error("Expected ReplaceBinary to NOT be called when signature is empty")
	}
}

func TestGetAgentPublicKey(t *testing.T) {
	origKey := AgentPublicKeyBase64
	defer func() { AgentPublicKeyBase64 = origKey }()

	t.Run("empty key fails closed", func(t *testing.T) {
		AgentPublicKeyBase64 = ""
		key, err := getAgentPublicKey()
		if err == nil || key != nil {
			t.Errorf("expected error for empty public key, got key=%v, err=%v", key, err)
		}
	})

	t.Run("placeholder key fails closed", func(t *testing.T) {
		AgentPublicKeyBase64 = "1111111111111111111111111111111111111111111="
		key, err := getAgentPublicKey()
		if err == nil || key != nil {
			t.Errorf("expected error for placeholder public key, got key=%v, err=%v", key, err)
		}
	})

	t.Run("invalid base64 fails closed", func(t *testing.T) {
		AgentPublicKeyBase64 = "!!!invalid-base64!!!"
		key, err := getAgentPublicKey()
		if err == nil || key != nil {
			t.Errorf("expected error for invalid base64 public key, got key=%v, err=%v", key, err)
		}
	})

	t.Run("invalid length (too short) fails closed", func(t *testing.T) {
		shortBytes := make([]byte, 16)
		AgentPublicKeyBase64 = base64.StdEncoding.EncodeToString(shortBytes)
		key, err := getAgentPublicKey()
		if err == nil || key != nil {
			t.Errorf("expected error for short public key length, got key=%v, err=%v", key, err)
		}
	})

	t.Run("invalid length (too long) fails closed", func(t *testing.T) {
		longBytes := make([]byte, 64)
		AgentPublicKeyBase64 = base64.StdEncoding.EncodeToString(longBytes)
		key, err := getAgentPublicKey()
		if err == nil || key != nil {
			t.Errorf("expected error for long public key length, got key=%v, err=%v", key, err)
		}
	})

	t.Run("valid 32-byte Ed25519 public key succeeds", func(t *testing.T) {
		pubKey, _, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			t.Fatalf("failed to generate test key: %v", err)
		}
		AgentPublicKeyBase64 = base64.StdEncoding.EncodeToString(pubKey)
		gotKey, err := getAgentPublicKey()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !bytes.Equal(gotKey, pubKey) {
			t.Errorf("expected key %v, got %v", pubKey, gotKey)
		}
	})
}

func TestExecuteSelfUpdate_InvalidSignatureFormatFailsClosed(t *testing.T) {
	_, cleanupHTTP := setupMockHTTP()
	defer cleanupHTTP()

	pubKey, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("Failed to generate test keys: %v", err)
	}

	downloadAttempted := false
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		downloadAttempted = true
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("mock binary data"))
	}))
	defer ts.Close()

	replaceCalled := false
	originalAdapter := osadapter.Current()
	mockAdapter := &MockOSAdapter{
		ReplaceBinaryFunc: func(tempPath, currentPath string) error {
			replaceCalled = true
			return nil
		},
	}
	osadapter.SetAdapter(mockAdapter)
	defer osadapter.SetAdapter(originalAdapter)

	client := &PusherClient{
		ServerID: "test-server",
		ApiURL:   "http://mock-backend",
	}

	originalKey := AgentPublicKeyBase64
	AgentPublicKeyBase64 = base64.StdEncoding.EncodeToString(pubKey)
	defer func() { AgentPublicKeyBase64 = originalKey }()

	client.executeSelfUpdate(ts.URL, "invalid-base64-!!!", "pipe-inv1", "step-inv1")
	if downloadAttempted {
		t.Error("Expected download to NOT occur when signature is invalid base64")
	}
	if replaceCalled {
		t.Error("Expected ReplaceBinary to NOT be called for invalid base64 signature")
	}

	downloadAttempted = false
	shortSig := base64.StdEncoding.EncodeToString(make([]byte, 32))
	client.executeSelfUpdate(ts.URL, shortSig, "pipe-inv2", "step-inv2")
	if downloadAttempted {
		t.Error("Expected download to NOT occur when signature is wrong length")
	}
	if replaceCalled {
		t.Error("Expected ReplaceBinary to NOT be called for short signature")
	}
}

func TestRollbackUpdateAndPostReplaceVerifyFailure(t *testing.T) {
	_, cleanupHTTP := setupMockHTTP()
	defer cleanupHTTP()

	tmpDir := t.TempDir()
	binPath := tmpDir + "/agent-bin"
	bakPath := binPath + ".bak"
	if err := os.WriteFile(binPath, []byte("corrupted-new-binary"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bakPath, []byte("known-good-previous-binary"), 0755); err != nil {
		t.Fatal(err)
	}

	rollbackUpdate(binPath)

	restored, err := os.ReadFile(binPath)
	if err != nil || string(restored) != "known-good-previous-binary" {
		t.Fatalf("expected rollbackUpdate to restore known-good-previous-binary, got %q (err=%v)", string(restored), err)
	}

	// Also test executeSelfUpdate when second VerifyBinary (post-replace) fails
	pubKey, privKey, _ := ed25519.GenerateKey(rand.Reader)
	mockBinaryData := []byte("signed binary payload")
	sigB64 := base64.StdEncoding.EncodeToString(ed25519.Sign(privKey, mockBinaryData))

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write(mockBinaryData)
	}))
	defer ts.Close()

	verifyCalls := 0
	restartCalled := false
	originalAdapter := osadapter.Current()
	mockAdapter := &MockOSAdapter{
		VerifyBinaryFunc: func(path string) error {
			verifyCalls++
			if verifyCalls == 2 {
				return io.ErrUnexpectedEOF
			}
			return nil
		},
		ReplaceBinaryFunc: func(tempPath, currentPath string) error {
			return nil
		},
		RestartAgentFunc: func() error {
			restartCalled = true
			return nil
		},
	}
	osadapter.SetAdapter(mockAdapter)
	defer osadapter.SetAdapter(originalAdapter)

	origKey := AgentPublicKeyBase64
	AgentPublicKeyBase64 = base64.StdEncoding.EncodeToString(pubKey)
	defer func() { AgentPublicKeyBase64 = origKey }()

	client := &PusherClient{ServerID: "srv-1", ApiURL: "http://mock-backend"}
	client.executeSelfUpdate(ts.URL, sigB64, "pipe-rb", "step-rb")

	if verifyCalls != 2 {
		t.Errorf("expected 2 VerifyBinary calls (staging + post-replace), got %d", verifyCalls)
	}
	if restartCalled {
		t.Errorf("expected RestartAgent NOT to be called when post-replace verification fails")
	}
}

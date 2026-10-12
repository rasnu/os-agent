package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"rasnuos-agent/os_adapter"
)

func setupMockHTTP() (*MockHTTPClient, func()) {
	orig := defaultHTTPClient
	mock := &MockHTTPClient{
		Responses: []*http.Response{
			{
				StatusCode: 200,
				Body:       io.NopCloser(bytes.NewBufferString(`{"status":"ok"}`)),
			},
		},
	}
	defaultHTTPClient = mock
	return mock, func() { defaultHTTPClient = orig }
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

	originalAdapter := os_adapter.Current()
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
	os_adapter.SetAdapter(mockAdapter)
	defer os_adapter.SetAdapter(originalAdapter)

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
	originalAdapter := os_adapter.Current()
	mockAdapter := &MockOSAdapter{
		ReplaceBinaryFunc: func(tempPath, currentPath string) error {
			replaceCalled = true
			return nil
		},
	}
	os_adapter.SetAdapter(mockAdapter)
	defer os_adapter.SetAdapter(originalAdapter)

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
	originalAdapter := os_adapter.Current()
	mockAdapter := &MockOSAdapter{
		ReplaceBinaryFunc: func(tempPath, currentPath string) error {
			replaceCalled = true
			return nil
		},
	}
	os_adapter.SetAdapter(mockAdapter)
	defer os_adapter.SetAdapter(originalAdapter)

	client := &PusherClient{
		ServerID: "test-server",
		ApiURL:   "http://mock-backend",
	}

	originalKey := AgentPublicKeyBase64
	// Test empty public key (default)
	AgentPublicKeyBase64 = ""
	defer func() { AgentPublicKeyBase64 = originalKey }()

	client.executeSelfUpdate(ts.URL, "c29tZXNpZ25hdHVyZQ==", "pipe-unconf", "step-unconf")

	if replaceCalled {
		t.Error("Expected ReplaceBinary to NOT be called when AgentPublicKeyBase64 is empty")
	}

	// Test placeholder public key
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
	originalAdapter := os_adapter.Current()
	mockAdapter := &MockOSAdapter{
		ReplaceBinaryFunc: func(tempPath, currentPath string) error {
			replaceCalled = true
			return nil
		},
	}
	os_adapter.SetAdapter(mockAdapter)
	defer os_adapter.SetAdapter(originalAdapter)

	client := &PusherClient{
		ServerID: "test-server",
		ApiURL:   "http://mock-backend",
	}

	originalKey := AgentPublicKeyBase64
	AgentPublicKeyBase64 = base64.StdEncoding.EncodeToString(pubKey)
	defer func() { AgentPublicKeyBase64 = originalKey }()

	// Calling with empty signature must fail closed even when valid public key is present
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
	originalAdapter := os_adapter.Current()
	mockAdapter := &MockOSAdapter{
		ReplaceBinaryFunc: func(tempPath, currentPath string) error {
			replaceCalled = true
			return nil
		},
	}
	os_adapter.SetAdapter(mockAdapter)
	defer os_adapter.SetAdapter(originalAdapter)

	client := &PusherClient{
		ServerID: "test-server",
		ApiURL:   "http://mock-backend",
	}

	originalKey := AgentPublicKeyBase64
	AgentPublicKeyBase64 = base64.StdEncoding.EncodeToString(pubKey)
	defer func() { AgentPublicKeyBase64 = originalKey }()

	// Case 1: Corrupted base64 signature
	client.executeSelfUpdate(ts.URL, "invalid-base64-!!!", "pipe-inv1", "step-inv1")
	if downloadAttempted {
		t.Error("Expected download to NOT occur when signature is invalid base64")
	}
	if replaceCalled {
		t.Error("Expected ReplaceBinary to NOT be called for invalid base64 signature")
	}

	// Case 2: Wrong length signature (32 bytes instead of 64 bytes)
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


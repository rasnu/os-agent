package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"rasnuos-agent/os_adapter"
)

func TestExecuteSelfUpdate_WithValidSignature(t *testing.T) {
	// Generate an Ed25519 keypair dynamically for the test
	pubKey, privKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("Failed to generate test keys: %v", err)
	}

	mockBinaryData := []byte("this is a secure update binary")

	// Create the signature
	signatureBytes := ed25519.Sign(privKey, mockBinaryData)
	signatureBase64 := base64.StdEncoding.EncodeToString(signatureBytes)

	// Spin up a mock HTTP server returning the binary data
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write(mockBinaryData)
	}))
	defer ts.Close()

	// Mock the adapter
	replaceCalled := false
	originalAdapter := os_adapter.Current()
	mockAdapter := &MockOSAdapter{
		VerifyBinaryFunc: func(path string) error { return nil },
		ReplaceBinaryFunc: func(tempPath, currentPath string) error {
			replaceCalled = true
			return nil
		},
		RestartAgentFunc: func() error { return nil },
	}
	os_adapter.SetAdapter(mockAdapter)
	defer os_adapter.SetAdapter(originalAdapter)

	client := &PusherClient{ServerID: "test-server"}

	// Temporarily override the global public key for the test
	originalKey := AgentPublicKeyBase64
	AgentPublicKeyBase64 = base64.StdEncoding.EncodeToString(pubKey)
	defer func() { AgentPublicKeyBase64 = originalKey }()

	client.executeSelfUpdate(ts.URL, signatureBase64, "pipe-1", "step-1")
	time.Sleep(100 * time.Millisecond)

	if !replaceCalled {
		t.Error("Expected ReplaceBinary to be called for VALID signature")
	}
}

func TestExecuteSelfUpdate_WithInvalidSignature(t *testing.T) {
	// Generate an Ed25519 keypair
	pubKey, privKey, _ := ed25519.GenerateKey(rand.Reader)

	mockBinaryData := []byte("this is a secure update binary")
	badBinaryData := []byte("this is an evil hacked binary")

	// Sign the good data, but serve the bad data!
	signatureBytes := ed25519.Sign(privKey, mockBinaryData)
	signatureBase64 := base64.StdEncoding.EncodeToString(signatureBytes)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write(badBinaryData)
	}))
	defer ts.Close()

	replaceCalled := false
	originalAdapter := os_adapter.Current()
	mockAdapter := &MockOSAdapter{
		VerifyBinaryFunc: func(path string) error { return nil },
		ReplaceBinaryFunc: func(tempPath, currentPath string) error {
			replaceCalled = true
			return nil
		},
		RestartAgentFunc: func() error { return nil },
	}
	os_adapter.SetAdapter(mockAdapter)
	defer os_adapter.SetAdapter(originalAdapter)

	client := &PusherClient{ServerID: "test-server"}

	originalKey := AgentPublicKeyBase64
	AgentPublicKeyBase64 = base64.StdEncoding.EncodeToString(pubKey)
	defer func() { AgentPublicKeyBase64 = originalKey }()

	client.executeSelfUpdate(ts.URL, signatureBase64, "pipe-2", "step-2")
	time.Sleep(100 * time.Millisecond)

	if replaceCalled {
		t.Error("Expected ReplaceBinary to NOT be called for INVALID signature (tampered payload)")
	}
}

func TestExecuteSelfUpdate_ProductionFailsClosed(t *testing.T) {
	pubKey, _, _ := ed25519.GenerateKey(rand.Reader)
	mockBinaryData := []byte("binary data")

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write(mockBinaryData)
	}))
	defer ts.Close()

	replaceCalled := false
	originalAdapter := os_adapter.Current()
	mockAdapter := &MockOSAdapter{
		VerifyBinaryFunc: func(path string) error { return nil },
		ReplaceBinaryFunc: func(tempPath, currentPath string) error {
			replaceCalled = true
			return nil
		},
		RestartAgentFunc: func() error { return nil },
	}
	os_adapter.SetAdapter(mockAdapter)
	defer os_adapter.SetAdapter(originalAdapter)

	client := &PusherClient{ServerID: "test-server"}

	originalKey := AgentPublicKeyBase64
	AgentPublicKeyBase64 = base64.StdEncoding.EncodeToString(pubKey)
	defer func() { AgentPublicKeyBase64 = originalKey }()

	// Send an empty signature in production mode! It should FAIL CLOSED!
	client.executeSelfUpdate(ts.URL, "", "pipe-3", "step-3")
	time.Sleep(100 * time.Millisecond)

	if replaceCalled {
		t.Error("Expected ReplaceBinary to NOT be called when signature is omitted in production mode")
	}
}

// Copyright © 2026 Rasnu LLC (rasnu.com). All rights reserved.
// Licensed under the Functional Source License, Version 1.1, ALv2 Future License (FSL-1.1-ALv2).

package agent

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"rasnuos-agent/internal/osadapter"
)

func TestExecuteSelfUpdate_WithValidSignature(t *testing.T) {
	_, cleanupHTTP := setupMockHTTP()
	defer cleanupHTTP()

	pubKey, privKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("Failed to generate test keys: %v", err)
	}

	mockBinaryData := []byte("this is a secure update binary")

	signatureBytes := ed25519.Sign(privKey, mockBinaryData)
	signatureBase64 := base64.StdEncoding.EncodeToString(signatureBytes)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write(mockBinaryData)
	}))
	defer ts.Close()

	replaceCalled := false
	originalAdapter := osadapter.Current()
	mockAdapter := &MockOSAdapter{
		VerifyBinaryFunc: func(path string) error { return nil },
		ReplaceBinaryFunc: func(tempPath, currentPath string) error {
			replaceCalled = true
			return nil
		},
		RestartAgentFunc: func() error { return nil },
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

	if !replaceCalled {
		t.Error("Expected ReplaceBinary to be called for VALID signature")
	}
}

func TestExecuteSelfUpdate_WithInvalidSignature(t *testing.T) {
	_, cleanupHTTP := setupMockHTTP()
	defer cleanupHTTP()

	pubKey, privKey, _ := ed25519.GenerateKey(rand.Reader)

	mockBinaryData := []byte("this is a secure update binary")
	badBinaryData := []byte("this is an evil hacked binary")

	signatureBytes := ed25519.Sign(privKey, mockBinaryData)
	signatureBase64 := base64.StdEncoding.EncodeToString(signatureBytes)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write(badBinaryData)
	}))
	defer ts.Close()

	replaceCalled := false
	originalAdapter := osadapter.Current()
	mockAdapter := &MockOSAdapter{
		VerifyBinaryFunc: func(path string) error { return nil },
		ReplaceBinaryFunc: func(tempPath, currentPath string) error {
			replaceCalled = true
			return nil
		},
		RestartAgentFunc: func() error { return nil },
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
	time.Sleep(100 * time.Millisecond)

	if replaceCalled {
		t.Error("Expected ReplaceBinary to NOT be called for INVALID signature (tampered payload)")
	}
}

func TestExecuteSelfUpdate_ProductionFailsClosed(t *testing.T) {
	_, cleanupHTTP := setupMockHTTP()
	defer cleanupHTTP()

	pubKey, _, _ := ed25519.GenerateKey(rand.Reader)
	mockBinaryData := []byte("binary data")

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write(mockBinaryData)
	}))
	defer ts.Close()

	replaceCalled := false
	originalAdapter := osadapter.Current()
	mockAdapter := &MockOSAdapter{
		VerifyBinaryFunc: func(path string) error { return nil },
		ReplaceBinaryFunc: func(tempPath, currentPath string) error {
			replaceCalled = true
			return nil
		},
		RestartAgentFunc: func() error { return nil },
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

	client.executeSelfUpdate(ts.URL, "", "pipe-3", "step-3")
	time.Sleep(100 * time.Millisecond)

	if replaceCalled {
		t.Error("Expected ReplaceBinary to NOT be called when signature is omitted in production mode")
	}
}

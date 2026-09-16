// Copyright © 2026 Rasnu LLC (rasnu.com). All rights reserved.
// Licensed under the Functional Source License, Version 1.1, ALv2 Future License (FSL-1.1-ALv2).

package main

import (
	"crypto/ed25519"
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"
)

func TestGenerateAndSaveKeyPair(t *testing.T) {
	pub, priv, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair failed: %v", err)
	}

	pubBytes, err := base64.StdEncoding.DecodeString(pub)
	if err != nil || len(pubBytes) != ed25519.PublicKeySize {
		t.Fatalf("Invalid public key format or size %d (expected %d)", len(pubBytes), ed25519.PublicKeySize)
	}

	privBytes, err := base64.StdEncoding.DecodeString(priv)
	if err != nil || len(privBytes) != ed25519.PrivateKeySize {
		t.Fatalf("Invalid private key format or size %d (expected %d)", len(privBytes), ed25519.PrivateKeySize)
	}

	tmpDir := t.TempDir()
	pubPath, privPath, err := SaveKeyPair(tmpDir, pub, priv)
	if err != nil {
		t.Fatalf("SaveKeyPair failed: %v", err)
	}

	info, err := os.Stat(privPath)
	if err != nil {
		t.Fatalf("Stat private key failed: %v", err)
	}
	if mode := info.Mode().Perm(); mode != 0600 {
		t.Errorf("Expected private key permission 0600, got %o", mode)
	}

	loadedPriv, err := LoadPrivateKey(privPath)
	if err != nil {
		t.Fatalf("LoadPrivateKey from path failed: %v", err)
	}
	if len(loadedPriv) != ed25519.PrivateKeySize {
		t.Errorf("Loaded private key size mismatch: %d", len(loadedPriv))
	}

	loadedPub, err := LoadPublicKey(pubPath)
	if err != nil {
		t.Fatalf("LoadPublicKey from path failed: %v", err)
	}
	if len(loadedPub) != ed25519.PublicKeySize {
		t.Errorf("Loaded public key size mismatch: %d", len(loadedPub))
	}
}

func TestSignAndVerifyFile(t *testing.T) {
	pub, priv, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair failed: %v", err)
	}

	privKey, err := LoadPrivateKey(priv)
	if err != nil {
		t.Fatalf("LoadPrivateKey failed: %v", err)
	}

	pubKey, err := LoadPublicKey(pub)
	if err != nil {
		t.Fatalf("LoadPublicKey failed: %v", err)
	}

	tmpDir := t.TempDir()
	targetFile := filepath.Join(tmpDir, "sample_binary")
	content := []byte("this is a test executable binary payload")
	if err := os.WriteFile(targetFile, content, 0755); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	sig, err := SignFile(privKey, targetFile)
	if err != nil {
		t.Fatalf("SignFile failed: %v", err)
	}

	ok, err := VerifyFile(pubKey, targetFile, sig)
	if err != nil {
		t.Fatalf("VerifyFile error: %v", err)
	}
	if !ok {
		t.Error("Expected signature verification to succeed")
	}

	sigPath := filepath.Join(tmpDir, "sample_binary.sig")
	if err := os.WriteFile(sigPath, []byte(sig+"\n"), 0644); err != nil {
		t.Fatalf("WriteFile sig failed: %v", err)
	}
	ok, err = VerifyFile(pubKey, targetFile, sigPath)
	if err != nil || !ok {
		t.Errorf("VerifyFile from path failed: ok=%v, err=%v", ok, err)
	}

	tamperedFile := filepath.Join(tmpDir, "tampered_binary")
	if err := os.WriteFile(tamperedFile, []byte("evil payload"), 0755); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}
	ok, err = VerifyFile(pubKey, tamperedFile, sig)
	if err != nil {
		t.Fatalf("VerifyFile on tampered file returned unexpected error: %v", err)
	}
	if ok {
		t.Error("Expected verification to FAIL on tampered file")
	}
}

func TestLoadKeyErrors(t *testing.T) {
	if _, err := LoadPrivateKey(""); err == nil {
		t.Error("Expected error for empty private key")
	}
	if _, err := LoadPublicKey(""); err == nil {
		t.Error("Expected error for empty public key")
	}

	if _, err := LoadPrivateKey("!!!not-base64!!!"); err == nil {
		t.Error("Expected error for invalid base64 private key")
	}
	if _, err := LoadPublicKey("!!!not-base64!!!"); err == nil {
		t.Error("Expected error for invalid base64 public key")
	}

	shortBytes := make([]byte, 10)
	shortB64 := base64.StdEncoding.EncodeToString(shortBytes)
	if _, err := LoadPrivateKey(shortB64); err == nil {
		t.Error("Expected error for short private key")
	}
	if _, err := LoadPublicKey(shortB64); err == nil {
		t.Error("Expected error for short public key")
	}

	// 32-byte seed key should succeed
	seedBytes := make([]byte, ed25519.SeedSize)
	seedB64 := base64.StdEncoding.EncodeToString(seedBytes)
	privFromSeed, err := LoadPrivateKey(seedB64)
	if err != nil || len(privFromSeed) != ed25519.PrivateKeySize {
		t.Errorf("Expected 32-byte seed to derive 64-byte private key, got err=%v", err)
	}

	printUsage()
}

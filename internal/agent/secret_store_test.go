// Copyright © 2026 Rasnu LLC (rasnu.com). All rights reserved.
// Licensed under the Functional Source License, Version 1.1, ALv2 Future License (FSL-1.1-ALv2).

package agent

import (
	"reflect"
	"testing"
	"time"
)

func TestSecretStoreOperations(t *testing.T) {
	secretStore.Lock()
	secretStore.entries = make(map[string]*SecretEntry)
	secretStore.Unlock()

	token := "test-token-123"
	secrets := map[string]string{
		"DB_PASS": "supersecret",
		"API_KEY": "12345",
	}

	storeSecrets(token, secrets)

	secretStore.RLock()
	entry, exists := secretStore.entries[token]
	secretStore.RUnlock()

	if !exists {
		t.Fatalf("Secret was not stored")
	}
	if !reflect.DeepEqual(entry.Secrets, secrets) {
		t.Errorf("Stored secrets don't match. Got %v, want %v", entry.Secrets, secrets)
	}

	retrieved := retrieveAndWipeSecrets(token)
	if !reflect.DeepEqual(retrieved, secrets) {
		t.Errorf("Retrieved secrets don't match. Got %v, want %v", retrieved, secrets)
	}

	secretStore.RLock()
	_, existsAfterWipe := secretStore.entries[token]
	secretStore.RUnlock()

	if existsAfterWipe {
		t.Errorf("Secret was not wiped after retrieval")
	}

	if retrieveAndWipeSecrets("non-existent") != nil {
		t.Errorf("Expected nil for non-existent token")
	}
}

func TestBuildEnvWithSecrets(t *testing.T) {
	token := "env-token"
	secrets := map[string]string{
		"INJECTED_VAR": "value123",
	}
	storeSecrets(token, secrets)

	baseEnv := []string{"EXISTING_VAR=abc"}

	env, injectedKeys := buildEnvWithSecrets(token, baseEnv)

	if len(env) != 2 {
		t.Errorf("Expected env length 2, got %d", len(env))
	}
	if env[0] != "EXISTING_VAR=abc" {
		t.Errorf("Base env modified or missing")
	}

	if env[1] != "INJECTED_VAR=value123" {
		t.Errorf("Secret not properly injected, got %s", env[1])
	}

	if len(injectedKeys) != 1 || injectedKeys[0] != "INJECTED_VAR" {
		t.Errorf("Injected keys not correct, got %v", injectedKeys)
	}

	env2, injectedKeys2 := buildEnvWithSecrets("non-existent", baseEnv)
	if !reflect.DeepEqual(env2, baseEnv) {
		t.Errorf("Expected base env returned unmodified, got %v", env2)
	}
	if injectedKeys2 != nil {
		t.Errorf("Expected nil injected keys, got %v", injectedKeys2)
	}
}

func TestSecretExpiry(t *testing.T) {
	token := "expire-token"

	secretStore.Lock()
	secretStore.entries[token] = &SecretEntry{
		Secrets:   map[string]string{"K": "V"},
		ExpiresAt: time.Now().Add(-1 * time.Minute),
	}
	secretStore.Unlock()

	retrieved := retrieveAndWipeSecrets(token)
	if retrieved != nil {
		t.Errorf("Expected nil for expired secret, got %v", retrieved)
	}
}

func TestWipeSecrets(t *testing.T) {
	token := "wipe-token"
	storeSecrets(token, map[string]string{"K": "V"})

	wipeSecrets(token)

	secretStore.RLock()
	_, exists := secretStore.entries[token]
	secretStore.RUnlock()

	if exists {
		t.Errorf("Secret was not wiped by wipeSecrets")
	}
}

func TestSecretBuffersZeroedInMemory(t *testing.T) {
	token := "zero-memory-token"
	storeSecrets(token, map[string]string{"PRIVATE_KEY": "secret-payload-999"})

	secretStore.RLock()
	bufRef := secretStore.entries[token].SecretBuffers["PRIVATE_KEY"]
	secretStore.RUnlock()

	if len(bufRef) == 0 {
		t.Fatalf("Expected non-empty SecretBuffers slice")
	}

	retrieved := retrieveAndWipeSecrets(token)
	if retrieved["PRIVATE_KEY"] != "secret-payload-999" {
		t.Fatalf("Expected retrieved value 'secret-payload-999', got %q", retrieved["PRIVATE_KEY"])
	}

	// Verify that every byte of the underlying slice in memory was overwritten with 0x00
	for i, b := range bufRef {
		if b != 0 {
			t.Fatalf("Expected byte %d of secret buffer to be zeroed (0x00), got 0x%02x", i, b)
		}
	}
}

func TestHandleInjectSecret(t *testing.T) {
	_, cleanup := setupMockHTTP()
	defer cleanup()

	c := &PusherClient{ServerID: "srv-1", ApiURL: "http://mock-backend"}

	// Empty token rejected
	c.handleInjectSecret("p1", "s1", map[string]interface{}{})

	// Missing secrets map rejected
	c.handleInjectSecret("p1", "s2", map[string]interface{}{"token": "tok-abc"})

	// Non-string secret value rejected
	c.handleInjectSecret("p1", "s3", map[string]interface{}{
		"token":   "tok-abc",
		"secrets": map[string]interface{}{"BAD_KEY": 12345},
	})

	// Valid secret payload stored and retrievable once
	c.handleInjectSecret("p1", "s4", map[string]interface{}{
		"token": "tok-valid",
		"secrets": map[string]interface{}{
			"DB_PASSWORD": "my-db-password",
		},
	})

	got := retrieveAndWipeSecrets("tok-valid")
	if got["DB_PASSWORD"] != "my-db-password" {
		t.Fatalf("expected DB_PASSWORD='my-db-password', got %v", got)
	}
}

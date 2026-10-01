package main

import (
	"reflect"
	"testing"
	"time"
)

func TestSecretStoreOperations(t *testing.T) {
	// Clear the store before tests
	secretStore.Lock()
	secretStore.entries = make(map[string]*SecretEntry)
	secretStore.Unlock()

	token := "test-token-123"
	secrets := map[string]string{
		"DB_PASS": "supersecret",
		"API_KEY": "12345",
	}

	// Test store
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

	// Test retrieve and wipe
	retrieved := retrieveAndWipeSecrets(token)
	if !reflect.DeepEqual(retrieved, secrets) {
		t.Errorf("Retrieved secrets don't match. Got %v, want %v", retrieved, secrets)
	}

	// Verify wiped
	secretStore.RLock()
	_, existsAfterWipe := secretStore.entries[token]
	secretStore.RUnlock()
	
	if existsAfterWipe {
		t.Errorf("Secret was not wiped after retrieval")
	}

	// Test retrieve non-existent
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
	
	// Map order isn't guaranteed, but since there's only 1 secret, it will be at index 1
	if env[1] != "INJECTED_VAR=value123" {
		t.Errorf("Secret not properly injected, got %s", env[1])
	}
	
	if len(injectedKeys) != 1 || injectedKeys[0] != "INJECTED_VAR" {
		t.Errorf("Injected keys not correct, got %v", injectedKeys)
	}
	
	// Test build with no secrets
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
		ExpiresAt: time.Now().Add(-1 * time.Minute), // Already expired
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

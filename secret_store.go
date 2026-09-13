/**
 * Project: RasnuOS
 * Copyright © 2026 Rasnu LLC (rasnu.com). All rights reserved.
 *
 * secret_store.go — In-RAM Zero-Trust Secret Injection Engine
 *
 * Secrets are NEVER written to disk. They live in a process-level sync.Map,
 * keyed by a one-time cryptographic token. They are automatically wiped after
 * TTL expiry or after first retrieval (single-use model).
 *
 * Architecture:
 *   1. Laravel sends [BACKEND_INTENT:inject_secret] with a key-value secrets map
 *      and a scope token.
 *   2. The Go Agent stores them in RAM only.
 *   3. The next `execute-and-stream` command with the matching scope token
 *      receives the secrets injected as OS environment variables.
 *   4. After injection (or TTL), the secrets are wiped from memory.
 *   5. The execution log NEVER contains the secret values — only the key names
 *      (e.g. "[VAULT] Injected: DOCKER_REGISTRY_PASSWORD=***REDACTED***").
 */
package main

import (
	"fmt"
	"log"
	"sync"
	"time"
)

// SecretEntry holds a set of key-value secrets with an expiry time.
type SecretEntry struct {
	Secrets   map[string]string
	ExpiresAt time.Time
}

// secretStore is the process-level, in-RAM vault. Never persisted to disk.
var secretStore = struct {
	sync.RWMutex
	entries map[string]*SecretEntry
}{
	entries: make(map[string]*SecretEntry),
}

const secretTTL = 5 * time.Minute

// storeSecrets saves the secrets map into the in-RAM vault under the given token.
// The token is the single-use key. The entry auto-expires after secretTTL.
func storeSecrets(token string, secrets map[string]string) {
	secretStore.Lock()
	defer secretStore.Unlock()

	secretStore.entries[token] = &SecretEntry{
		Secrets:   secrets,
		ExpiresAt: time.Now().Add(secretTTL),
	}

	log.Printf("[VAULT] Stored %d secret(s) for token scope. TTL: %s", len(secrets), secretTTL)

	// Schedule auto-wipe after TTL
	go func() {
		time.Sleep(secretTTL + 1*time.Second)
		wipeSecrets(token)
	}()
}

// retrieveAndWipeSecrets fetches a secret set from the vault and immediately wipes it (single-use).
// Returns nil if the token is not found or has expired.
func retrieveAndWipeSecrets(token string) map[string]string {
	secretStore.Lock()
	defer secretStore.Unlock()

	entry, ok := secretStore.entries[token]
	if !ok {
		return nil
	}

	// Check expiry
	if time.Now().After(entry.ExpiresAt) {
		delete(secretStore.entries, token)
		log.Printf("[VAULT] Secret for token scope expired and was purged.")
		return nil
	}

	secrets := entry.Secrets
	delete(secretStore.entries, token) // Wipe on first use (single-use guarantee)
	log.Printf("[VAULT] Retrieved and wiped %d secret(s). Zero-Trust single-use guarantee enforced.", len(secrets))
	return secrets
}

// wipeSecrets removes a token's secrets from the vault if still present.
func wipeSecrets(token string) {
	secretStore.Lock()
	defer secretStore.Unlock()
	if _, ok := secretStore.entries[token]; ok {
		delete(secretStore.entries, token)
		log.Printf("[VAULT] Auto-wiped secret scope after TTL.")
	}
}

// buildEnvWithSecrets takes the current OS environment and appends injected secrets.
// Secrets are appended to a copy — the process environment is never modified globally.
func buildEnvWithSecrets(token string, baseEnv []string) ([]string, []string) {
	secrets := retrieveAndWipeSecrets(token)
	if secrets == nil || len(secrets) == 0 {
		return baseEnv, nil
	}

	env := make([]string, len(baseEnv))
	copy(env, baseEnv)

	var injectedKeys []string
	for k, v := range secrets {
		env = append(env, fmt.Sprintf("%s=%s", k, v))
		injectedKeys = append(injectedKeys, k)
	}

	return env, injectedKeys
}

// PusherClient method — handles an inject-secret command dispatched from Laravel.
func (c *PusherClient) handleInjectSecret(pipelineId, stepId string, args map[string]interface{}) {
	token := getString(args, "token")
	if token == "" {
		log.Printf("[VAULT] SECURITY ALERT: inject-secret called with empty token. Rejecting.")
		c.sendMigrationCompletion(pipelineId, stepId, false, "[VAULT] SECURITY ALERT: inject-secret requires a non-empty token scope.", nil)
		return
	}

	secretsRaw, ok := args["secrets"].(map[string]interface{})
	if !ok || len(secretsRaw) == 0 {
		log.Printf("[VAULT] inject-secret called with empty or invalid secrets payload.")
		c.sendMigrationCompletion(pipelineId, stepId, false, "[VAULT] No secrets provided in payload.", nil)
		return
	}

	// Convert to string map. All values must be strings.
	secrets := make(map[string]string, len(secretsRaw))
	var keyNames []string
	for k, v := range secretsRaw {
		strVal, isStr := v.(string)
		if !isStr {
			c.sendMigrationCompletion(pipelineId, stepId, false, fmt.Sprintf("[VAULT] Secret value for key '%s' must be a string.", k), nil)
			return
		}
		secrets[k] = strVal
		keyNames = append(keyNames, k)
	}

	storeSecrets(token, secrets)

	// Build audit-safe log line. NEVER log the values.
	safeLog := fmt.Sprintf("[VAULT] Secrets securely injected into RAM. Keys available for this scope: %v. Auto-wipe in %s.", keyNames, secretTTL)
	log.Printf(safeLog)

	c.sendMigrationCompletion(pipelineId, stepId, true, safeLog, map[string]interface{}{
		"injected_keys": keyNames,
		"ttl_seconds":   int(secretTTL.Seconds()),
	})
}

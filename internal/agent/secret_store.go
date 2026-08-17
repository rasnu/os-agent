// Copyright © 2026 Rasnu LLC (rasnu.com). All rights reserved.
// Licensed under the Functional Source License, Version 1.1, ALv2 Future License (FSL-1.1-ALv2).

package agent

import (
	"fmt"
	"log"
	"sync"
	"time"
)

type SecretEntry struct {
	Secrets       map[string]string
	SecretBuffers map[string][]byte
	ExpiresAt     time.Time
}

// zeroEntry overwrites all mutable secret byte buffers with 0x00 and clears map entries
// before releasing references to the garbage collector.
func zeroEntry(entry *SecretEntry) {
	if entry == nil {
		return
	}
	for k, buf := range entry.SecretBuffers {
		for i := range buf {
			buf[i] = 0
		}
		delete(entry.SecretBuffers, k)
	}
	for k := range entry.Secrets {
		entry.Secrets[k] = ""
		delete(entry.Secrets, k)
	}
}

// secretStore holds single-use secrets in process memory only; entries are never
// written to disk and are wiped on first read or TTL expiry.
var secretStore = struct {
	sync.RWMutex
	entries map[string]*SecretEntry
}{
	entries: make(map[string]*SecretEntry),
}

const secretTTL = 5 * time.Minute

func storeSecrets(token string, secrets map[string]string) {
	secretStore.Lock()
	defer secretStore.Unlock()

	if existing, ok := secretStore.entries[token]; ok {
		zeroEntry(existing)
	}

	copiedSecrets := make(map[string]string, len(secrets))
	buffers := make(map[string][]byte, len(secrets))
	for k, v := range secrets {
		copiedSecrets[k] = v
		buffers[k] = []byte(v)
	}

	secretStore.entries[token] = &SecretEntry{
		Secrets:       copiedSecrets,
		SecretBuffers: buffers,
		ExpiresAt:     time.Now().Add(secretTTL),
	}

	log.Printf("[VAULT] Stored %d secret(s) for token scope. TTL: %s", len(secrets), secretTTL)

	go func() {
		time.Sleep(secretTTL + 1*time.Second)
		wipeSecrets(token)
	}()
}

// retrieveAndWipeSecrets fetches a secret set by scope token and immediately zeroes & deletes it.
func retrieveAndWipeSecrets(token string) map[string]string {
	secretStore.Lock()
	defer secretStore.Unlock()

	entry, ok := secretStore.entries[token]
	if !ok {
		return nil
	}

	if time.Now().After(entry.ExpiresAt) {
		zeroEntry(entry)
		delete(secretStore.entries, token)
		log.Printf("[VAULT] Secret for token scope expired and was purged.")
		return nil
	}

	secrets := make(map[string]string, len(entry.Secrets))
	for k, buf := range entry.SecretBuffers {
		secrets[k] = string(buf)
	}
	for k, v := range entry.Secrets {
		if _, hasBuf := secrets[k]; !hasBuf {
			secrets[k] = v
		}
	}

	zeroEntry(entry)
	delete(secretStore.entries, token)
	log.Printf("[VAULT] Retrieved and wiped %d secret(s). Zero-Trust single-use guarantee enforced.", len(secrets))
	return secrets
}

func wipeSecrets(token string) {
	secretStore.Lock()
	defer secretStore.Unlock()
	if entry, ok := secretStore.entries[token]; ok {
		zeroEntry(entry)
		delete(secretStore.entries, token)
		log.Printf("[VAULT] Auto-wiped secret scope after TTL.")
	}
}

// buildEnvWithSecrets appends single-use secrets to a copy of baseEnv so the
// agent process environment is never modified globally.
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

	safeLog := fmt.Sprintf("[VAULT] Secrets securely injected into RAM. Keys available for this scope: %v. Auto-wipe in %s.", keyNames, secretTTL)
	log.Println(safeLog)

	c.sendMigrationCompletion(pipelineId, stepId, true, safeLog, map[string]interface{}{
		"injected_keys": keyNames,
		"ttl_seconds":   int(secretTTL.Seconds()),
	})
}

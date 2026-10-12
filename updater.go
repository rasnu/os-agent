package main

import (
	"crypto/ed25519"
	"encoding/base64"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"time"

	"rasnuos-agent/os_adapter"
)

// In production, this key must be injected during the agent build process via -ldflags.
// Example: -ldflags "-X main.AgentPublicKeyBase64=<base64_pubkey>"
// It matches the private key stored securely in the Laravel backend.
// By default, it is unconfigured ("") to enforce a strict fail-closed architecture.
var AgentPublicKeyBase64 = ""

// getAgentPublicKey returns the decoded Ed25519 public key if configured.
// Fails closed if unconfigured, placeholder, or invalid length.
func getAgentPublicKey() (ed25519.PublicKey, error) {
	if AgentPublicKeyBase64 == "" || AgentPublicKeyBase64 == "1111111111111111111111111111111111111111111=" {
		return nil, fmt.Errorf("agent public key is not configured (fail-closed)")
	}
	pubKeyBytes, err := base64.StdEncoding.DecodeString(AgentPublicKeyBase64)
	if err != nil {
		return nil, fmt.Errorf("invalid base64 encoding in agent public key: %w", err)
	}
	if len(pubKeyBytes) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("invalid public key length %d (expected %d bytes)", len(pubKeyBytes), ed25519.PublicKeySize)
	}
	return ed25519.PublicKey(pubKeyBytes), nil
}

func (c *PusherClient) executeSelfUpdate(downloadUrl, signatureBase64, pipelineId, stepId string) {
	log.Printf("Starting self-update from %s", downloadUrl)

	// STRICT FAIL-CLOSED SECURITY:
	// 1. Verify agent has a valid embedded public key BEFORE downloading
	pubKey, err := getAgentPublicKey()
	if err != nil {
		c.reportUpdateFailure(pipelineId, stepId, "SECURITY ALERT: This agent was compiled without a valid production public key. Self-update aborted to prevent unverified execution.")
		return
	}

	// 2. Cryptographic signature is strictly mandatory for all self-updates
	if signatureBase64 == "" {
		c.reportUpdateFailure(pipelineId, stepId, "SECURITY ALERT: This agent requires a cryptographically signed update. No signature was provided. Update aborted.")
		return
	}

	// 3. Decode and validate signature encoding and length (Ed25519 signatures are 64 bytes)
	sigBytes, err := base64.StdEncoding.DecodeString(signatureBase64)
	if err != nil || len(sigBytes) != ed25519.SignatureSize {
		c.reportUpdateFailure(pipelineId, stepId, "SECURITY ALERT: Invalid base64 signature format or length from backend. Update aborted.")
		return
	}

	// Determine current executable path
	executablePath, err := os.Executable()
	if err != nil {
		c.reportUpdateFailure(pipelineId, stepId, fmt.Sprintf("Failed to get executable path: %v", err))
		return
	}

	// Download the new binary
	tempPath := executablePath + ".tmp"
	err = downloadFile(downloadUrl, tempPath)
	if err != nil {
		c.reportUpdateFailure(pipelineId, stepId, fmt.Sprintf("Failed to download update: %v", err))
		return
	}
	defer os.Remove(tempPath)

	log.Printf("Verifying binary signature...")
	binaryData, err := os.ReadFile(tempPath)
	if err != nil {
		c.reportUpdateFailure(pipelineId, stepId, fmt.Sprintf("Failed to read downloaded binary for verification: %v", err))
		return
	}

	if !ed25519.Verify(pubKey, binaryData, sigBytes) {
		c.reportUpdateFailure(pipelineId, stepId, "SECURITY ALERT: Signature verification failed. The update payload may have been tampered with.")
		return
	}
	log.Printf("Signature verification passed.")

	// Make it executable
	err = os.Chmod(tempPath, 0755)
	if err != nil {
		c.reportUpdateFailure(pipelineId, stepId, fmt.Sprintf("Failed to set executable permissions: %v", err))
		return
	}

	// Verify the binary works before overwriting
	err = os_adapter.Current().VerifyBinary(tempPath)
	if err != nil {
		c.reportUpdateFailure(pipelineId, stepId, fmt.Sprintf("Failed to verify downloaded binary: %v", err))
		return
	}

	// Replace the old binary
	err = os_adapter.Current().ReplaceBinary(tempPath, executablePath)
	if err != nil {
		c.reportUpdateFailure(pipelineId, stepId, fmt.Sprintf("Failed to replace binary: %v", err))
		return
	}

	log.Printf("Update installed successfully. Scheduling restart...")
	logAuditEvent(AuditEvent{Action: "execute_self_update", PipelineID: pipelineId, StepID: stepId, Success: true})

	// Report success if part of a pipeline
	if pipelineId != "" && stepId != "" {
		c.sendCompletion(pipelineId, stepId, true, "Agent updated successfully. Restarting...")
	}

	// Schedule restart in background to allow completion message to send
	go func() {
		time.Sleep(2 * time.Second)
		log.Printf("Restarting agent using OS-specific adapter...")
		os_adapter.Current().RestartAgent()
	}()
}

func (c *PusherClient) reportUpdateFailure(pipelineId, stepId, errStr string) {
	log.Printf("Self-Update failed: %s", errStr)
	logAuditEvent(AuditEvent{Action: "execute_self_update", PipelineID: pipelineId, StepID: stepId, Success: false, Error: errStr})
	if pipelineId != "" && stepId != "" {
		c.sendCompletion(pipelineId, stepId, false, errStr)
	}
}

func downloadFile(url, dest string) error {
	// Create the temporary file with 0600 permissions to prevent local TOCTOU file replacement attacks
	out, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	defer out.Close()

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return err
	}
	resp, err := updaterHTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("bad status: %s", resp.Status)
	}

	// Limit download to 100MB to prevent Out-Of-Memory (OOM) / Disk Exhaustion DoS attacks
	_, err = io.Copy(out, io.LimitReader(resp.Body, 100*1024*1024))
	return err
}

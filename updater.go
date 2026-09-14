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

// In production, this key will be injected during the agent build process.
// It matches the private key stored securely in the Laravel backend.
var AgentPublicKeyBase64 = "1111111111111111111111111111111111111111111=" // Replace with actual base64 pubkey

func (c *PusherClient) executeSelfUpdate(downloadUrl, signatureBase64, pipelineId, stepId string) {

	log.Printf("Starting self-update from %s", downloadUrl)

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

	// Fail-closed security architecture: if the backend sends a signature, we MUST verify it.
	if signatureBase64 != "" {
		if AgentPublicKeyBase64 == "1111111111111111111111111111111111111111111=" {
			c.reportUpdateFailure(pipelineId, stepId, "SECURITY ALERT: Update signature was provided, but this agent was compiled without a valid public key. Aborting to prevent bypass.")
			return
		}

		log.Printf("Verifying binary signature...")
		pubKeyBytes, err := base64.StdEncoding.DecodeString(AgentPublicKeyBase64)
		if err != nil || len(pubKeyBytes) != ed25519.PublicKeySize {
			c.reportUpdateFailure(pipelineId, stepId, "Invalid embedded agent public key")
			return
		}

		sigBytes, err := base64.StdEncoding.DecodeString(signatureBase64)
		if err != nil {
			c.reportUpdateFailure(pipelineId, stepId, "Invalid base64 signature from backend")
			return
		}

		binaryData, err := os.ReadFile(tempPath)
		if err != nil {
			c.reportUpdateFailure(pipelineId, stepId, fmt.Sprintf("Failed to read downloaded binary for verification: %v", err))
			return
		}

		if !ed25519.Verify(pubKeyBytes, binaryData, sigBytes) {
			c.reportUpdateFailure(pipelineId, stepId, "SECURITY ALERT: Signature verification failed. The update payload may have been tampered with.")
			return
		}
		log.Printf("Signature verification passed.")
	} else {
		// No signature provided by backend.
		// If we are in production (we have a real embedded key), we MUST reject unsigned updates to prevent MITM bypass!
		if AgentPublicKeyBase64 != "1111111111111111111111111111111111111111111=" {
			c.reportUpdateFailure(pipelineId, stepId, "SECURITY ALERT: This agent is in strict production mode and requires a cryptographically signed update. No signature was provided. Update aborted.")
			return
		}
		log.Printf("Warning: Skipping signature verification (dev mode active, no signature provided)")
	}

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

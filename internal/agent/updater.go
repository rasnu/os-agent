// Copyright © 2026 Rasnu LLC (rasnu.com). All rights reserved.
// Licensed under the Functional Source License, Version 1.1, ALv2 Future License (FSL-1.1-ALv2).

package agent

import (
	"crypto/ed25519"
	"encoding/base64"
	"fmt"
	"io"
	"log"
	"net/http"
	neturl "net/url"
	"os"
	"strings"
	"time"

	"rasnuos-agent/internal/audit"
	"rasnuos-agent/internal/osadapter"
	"rasnuos-agent/internal/transport"
)

// AgentPublicKeyBase64 holds the Ed25519 public key injected at build time via -ldflags;
// defaults to empty so unconfigured builds fail closed on self-update.
var AgentPublicKeyBase64 = ""

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

	pubKey, err := getAgentPublicKey()
	if err != nil {
		c.reportUpdateFailure(pipelineId, stepId, "SECURITY ALERT: This agent was compiled without a valid production public key. Self-update aborted to prevent unverified execution.")
		return
	}

	if signatureBase64 == "" {
		c.reportUpdateFailure(pipelineId, stepId, "SECURITY ALERT: This agent requires a cryptographically signed update. No signature was provided. Update aborted.")
		return
	}

	sigBytes, err := base64.StdEncoding.DecodeString(signatureBase64)
	if err != nil || len(sigBytes) != ed25519.SignatureSize {
		c.reportUpdateFailure(pipelineId, stepId, "SECURITY ALERT: Invalid base64 signature format or length from backend. Update aborted.")
		return
	}

	executablePath, err := os.Executable()
	if err != nil {
		c.reportUpdateFailure(pipelineId, stepId, fmt.Sprintf("Failed to get executable path: %v", err))
		return
	}

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

	err = os.Chmod(tempPath, 0755)
	if err != nil {
		c.reportUpdateFailure(pipelineId, stepId, fmt.Sprintf("Failed to set executable permissions: %v", err))
		return
	}

	err = osadapter.Current().VerifyBinary(tempPath)
	if err != nil {
		c.reportUpdateFailure(pipelineId, stepId, fmt.Sprintf("Failed to verify downloaded binary: %v", err))
		return
	}

	err = osadapter.Current().ReplaceBinary(tempPath, executablePath)
	if err != nil {
		c.reportUpdateFailure(pipelineId, stepId, fmt.Sprintf("Failed to replace binary: %v", err))
		return
	}

	if err := osadapter.Current().VerifyBinary(executablePath); err != nil {
		rollbackUpdate(executablePath)
		c.reportUpdateFailure(pipelineId, stepId, fmt.Sprintf("Post-replace verification failed (rolled back to previous binary): %v", err))
		return
	}

	log.Printf("Update installed successfully. Scheduling restart...")
	audit.Log(audit.Event{Action: "execute_self_update", PipelineID: pipelineId, StepID: stepId, Success: true})

	if pipelineId != "" && stepId != "" {
		c.sendCompletion(pipelineId, stepId, true, "Agent updated successfully. Restarting...")
	}

	go func() {
		time.Sleep(2 * time.Second)
		log.Printf("Restarting agent using OS-specific adapter...")
		osadapter.Current().RestartAgent()
	}()
}

func rollbackUpdate(executablePath string) {
	for _, suffix := range []string{".bak", ".old"} {
		backupPath := executablePath + suffix
		if _, err := os.Stat(backupPath); err == nil {
			_ = os.Remove(executablePath)
			if renameErr := os.Rename(backupPath, executablePath); renameErr == nil {
				log.Printf("Rolled back executable from %s", backupPath)
				return
			}
		}
	}
}

func (c *PusherClient) reportUpdateFailure(pipelineId, stepId, errStr string) {
	log.Printf("Self-Update failed: %s", errStr)
	audit.Log(audit.Event{Action: "execute_self_update", PipelineID: pipelineId, StepID: stepId, Success: false, Error: errStr})
	if pipelineId != "" && stepId != "" {
		c.sendCompletion(pipelineId, stepId, false, errStr)
	}
}

func validateTransferURL(rawURL string) error {
	parsed, err := neturl.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("unsupported URL scheme: %s", parsed.Scheme)
	}
	host := strings.ToLower(parsed.Hostname())
	if host == "" {
		return fmt.Errorf("URL missing host")
	}
	if host == "169.254.169.254" || host == "fd00:ec2::254" || host == "metadata.google.internal" {
		return fmt.Errorf("access to cloud metadata endpoint is forbidden")
	}
	return nil
}

func downloadFile(url, dest string) error {
	if err := validateTransferURL(url); err != nil {
		return err
	}

	// Use 0600 permissions on the staging file to prevent local TOCTOU replacement before verification.
	out, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	defer out.Close()

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return err
	}
	resp, err := transport.Updater.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("bad status: %s", resp.Status)
	}

	// Cap download size at 100MB to prevent disk or memory exhaustion.
	const maxDownloadBytes = 100 * 1024 * 1024
	n, err := io.Copy(out, io.LimitReader(resp.Body, maxDownloadBytes+1))
	if err != nil {
		return err
	}
	if n > maxDownloadBytes {
		return fmt.Errorf("download exceeds maximum allowed size of 100MB")
	}
	return nil
}

package main

import (
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
)

// secureTLSConfig generates a pinned TLS configuration using hex-encoded SHA-256 SPKI hashes.
func secureTLSConfig(pins string) *tls.Config {
	pinList := strings.Split(pins, ",")
	for i := range pinList {
		pinList[i] = strings.ToLower(strings.TrimSpace(pinList[i]))
	}

	return &tls.Config{
		// Enforce system trust store verification to prevent chain injection attacks.
		InsecureSkipVerify: false,

		// VerifyConnection asserts SPKI match against cryptographically verified chains.
		VerifyConnection: func(cs tls.ConnectionState) error {
			// Iterate over the cryptographically verified chains
			for _, chain := range cs.VerifiedChains {
				for _, cert := range chain {
					// Hash SPKI
					hash := sha256.Sum256(cert.RawSubjectPublicKeyInfo)
					hashHex := hex.EncodeToString(hash[:])

					for _, pin := range pinList {
						// Support optional "sha256/" prefix
						cleanPin := strings.TrimPrefix(pin, "sha256/")
						if hashHex == cleanPin {
							return nil // Match found, connection authorized
						}
					}
				}
			}

			return fmt.Errorf("certificate pinning failure: no verified certificate public key matched any pinned hashes")
		},
	}
}

// applyTLSPinToDefaultTransport intercepts the global HTTP transport and all agent HTTP clients to enforce pinning.
func applyTLSPinToDefaultTransport(pins string) {
	if strings.TrimSpace(pins) == "" {
		return
	}
	cfg := secureTLSConfig(pins)
	if transport, ok := http.DefaultTransport.(*http.Transport); ok {
		transport.TLSClientConfig = cfg
	}
	SetGlobalTLSConfig(cfg)
}

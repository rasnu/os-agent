// Copyright © 2026 Rasnu LLC (rasnu.com). All rights reserved.
// Licensed under the Functional Source License, Version 1.1, ALv2 Future License (FSL-1.1-ALv2).

package transport

import (
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
)

// PinnedTLSConfig returns a *tls.Config that validates the server certificate chain
// against the system root store and requires at least one certificate in the verified
// chain to match a comma-separated hex SHA-256 SPKI digest in pins.
func PinnedTLSConfig(pins string) *tls.Config {
	pinList := strings.Split(pins, ",")
	for i := range pinList {
		pinList[i] = strings.ToLower(strings.TrimSpace(pinList[i]))
	}

	return &tls.Config{
		InsecureSkipVerify: false,

		VerifyConnection: func(cs tls.ConnectionState) error {
			for _, chain := range cs.VerifiedChains {
				for _, cert := range chain {
					hash := sha256.Sum256(cert.RawSubjectPublicKeyInfo)
					hashHex := hex.EncodeToString(hash[:])

					for _, pin := range pinList {
						cleanPin := strings.TrimPrefix(pin, "sha256/")
						if hashHex == cleanPin {
							return nil
						}
					}
				}
			}

			return fmt.Errorf("certificate pinning failure: no verified certificate public key matched any pinned hashes")
		},
	}
}

// ApplyTLSPin configures SPKI certificate pinning on http.DefaultTransport and all agent HTTP clients.
func ApplyTLSPin(pins string) {
	if strings.TrimSpace(pins) == "" {
		return
	}
	cfg := PinnedTLSConfig(pins)
	if transport, ok := http.DefaultTransport.(*http.Transport); ok {
		transport.TLSClientConfig = cfg
	}
	SetGlobalTLSConfig(cfg)
}

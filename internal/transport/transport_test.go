// Copyright © 2026 Rasnu LLC (rasnu.com). All rights reserved.
// Licensed under the Functional Source License, Version 1.1, ALv2 Future License (FSL-1.1-ALv2).

package transport

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"rasnuos-agent/internal/buildinfo"
)

func TestTransport_RoundTripAndGlobalTLS(t *testing.T) {
	var receivedUA string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedUA = r.Header.Get("User-Agent")
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	// Verify SetGlobalTLSConfig updates the global TLS config and applies to default HTTP clients
	customTLS := &tls.Config{MinVersion: tls.VersionTLS13}
	SetGlobalTLSConfig(customTLS)
	if getGlobalTLSConfig() != customTLS {
		t.Fatalf("Expected getGlobalTLSConfig to return customTLS")
	}

	// Test Default() and SetDefault()
	orig := Default()
	defer SetDefault(orig)

	req, err := http.NewRequest("GET", ts.URL, nil)
	if err != nil {
		t.Fatalf("NewRequest failed: %v", err)
	}

	resp, err := Default().Do(req)
	if err != nil {
		t.Fatalf("Default().Do failed: %v", err)
	}
	resp.Body.Close()

	expectedUA := "RasnuOSAgent/" + buildinfo.Version
	if receivedUA != expectedUA {
		t.Errorf("Expected User-Agent %q, got %q", expectedUA, receivedUA)
	}
	if !strings.HasPrefix(receivedUA, "RasnuOSAgent/") {
		t.Errorf("Expected User-Agent prefix RasnuOSAgent/, got %q", receivedUA)
	}

	// Also verify SetGlobalTLSConfig handles an http.Client with nil Transport and direct *http.Transport
	bareClient := &http.Client{Transport: nil}
	SetDefault(bareClient)
	SetGlobalTLSConfig(customTLS)
	if bareClient.Transport == nil {
		t.Errorf("Expected SetGlobalTLSConfig to initialize nil Transport on bareClient")
	}

	directTransportClient := &http.Client{Transport: &http.Transport{}}
	SetDefault(directTransportClient)
	SetGlobalTLSConfig(customTLS)
	if tr, ok := directTransportClient.Transport.(*http.Transport); !ok || tr.TLSClientConfig != customTLS {
		t.Errorf("Expected SetGlobalTLSConfig to set TLSClientConfig on direct *http.Transport")
	}
}

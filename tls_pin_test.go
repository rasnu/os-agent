package main

import (
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"net/http"
	"testing"
	"time"
)

func TestSecureTLSConfig(t *testing.T) {
	
	// Create a dummy certificate structure with dummy SPKI
	dummySPKI := []byte("dummy_public_key_info")
	cert := &x509.Certificate{
		RawSubjectPublicKeyInfo: dummySPKI,
	}

	hash := sha256.Sum256(dummySPKI)
	hashHex := hex.EncodeToString(hash[:])

	// Create connection state with verified chains containing our cert
	cs := tls.ConnectionState{
		VerifiedChains: [][]*x509.Certificate{
			{cert},
		},
	}

	t.Run("Valid pin", func(t *testing.T) {
		config := secureTLSConfig(hashHex)
		err := config.VerifyConnection(cs)
		if err != nil {
			t.Errorf("Expected nil error for valid pin, got %v", err)
		}
	})

	t.Run("Valid pin with prefix", func(t *testing.T) {
		config := secureTLSConfig("sha256/" + hashHex)
		err := config.VerifyConnection(cs)
		if err != nil {
			t.Errorf("Expected nil error for valid pin with prefix, got %v", err)
		}
	})

	t.Run("Invalid pin", func(t *testing.T) {
		config := secureTLSConfig("invalid_hash")
		err := config.VerifyConnection(cs)
		if err == nil {
			t.Errorf("Expected error for invalid pin, got nil")
		}
	})
	
	t.Run("Multiple pins including valid", func(t *testing.T) {
		config := secureTLSConfig("invalid_hash," + hashHex)
		err := config.VerifyConnection(cs)
		if err != nil {
			t.Errorf("Expected nil error for valid pin in list, got %v", err)
		}
	})
}

func TestApplyTLSPinToDefaultTransport(t *testing.T) {
	origDefault := getDefaultHTTPClient()
	origHeartbeat := heartbeatHTTPClient
	origUpdater := updaterHTTPClient
	origFileTransfer := fileTransferHTTPClient
	defer func() {
		setDefaultHTTPClient(origDefault)
		heartbeatHTTPClient = origHeartbeat
		updaterHTTPClient = origUpdater
		fileTransferHTTPClient = origFileTransfer
		SetGlobalTLSConfig(nil)
	}()

	setDefaultHTTPClient(&http.Client{Timeout: 30 * time.Second, Transport: defaultTransport()})
	heartbeatHTTPClient = &http.Client{Timeout: 30 * time.Second, Transport: defaultTransport()}
	updaterHTTPClient = &http.Client{Timeout: 240 * time.Second, Transport: defaultTransport()}
	fileTransferHTTPClient = &http.Client{Timeout: 0, Transport: defaultTransport()}

	applyTLSPinToDefaultTransport("dummy_pin")
	
	transport, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		t.Fatalf("http.DefaultTransport is not an *http.Transport")
	}
	
	if transport.TLSClientConfig == nil {
		t.Fatalf("TLSClientConfig was not set on http.DefaultTransport")
	}
	
	if transport.TLSClientConfig.InsecureSkipVerify {
		t.Errorf("InsecureSkipVerify should be false on http.DefaultTransport")
	}

	// Verify all global agent HTTP clients have the pinned TLS configuration
	clients := map[string]HTTPDoer{
		"defaultHTTPClient":      getDefaultHTTPClient(),
		"heartbeatHTTPClient":    heartbeatHTTPClient,
		"updaterHTTPClient":      updaterHTTPClient,
		"fileTransferHTTPClient": fileTransferHTTPClient,
	}

	for name, doer := range clients {
		client, ok := doer.(*http.Client)
		if !ok || client == nil {
			t.Fatalf("%s is not an *http.Client", name)
		}

		rt, ok := client.Transport.(*userAgentRoundTripper)
		if !ok || rt == nil {
			t.Fatalf("%s does not wrap userAgentRoundTripper", name)
		}

		innerTransport, ok := rt.inner.(*http.Transport)
		if !ok || innerTransport == nil {
			t.Fatalf("%s inner transport is not *http.Transport", name)
		}

		if innerTransport.TLSClientConfig == nil {
			t.Errorf("%s has nil TLSClientConfig", name)
		} else {
			if innerTransport.TLSClientConfig.InsecureSkipVerify {
				t.Errorf("%s has InsecureSkipVerify=true, expected false", name)
			}
			if innerTransport.TLSClientConfig.VerifyConnection == nil {
				t.Errorf("%s has nil VerifyConnection", name)
			}
		}
	}

	// Verify a newly created transport also inherits the pinned TLS configuration
	newTransport := defaultTransport()
	newRT, ok := newTransport.(*userAgentRoundTripper)
	if !ok || newRT == nil {
		t.Fatalf("newly created transport does not wrap userAgentRoundTripper")
	}
	newInner, ok := newRT.inner.(*http.Transport)
	if !ok || newInner == nil {
		t.Fatalf("newly created transport inner is not *http.Transport")
	}
	if newInner.TLSClientConfig == nil {
		t.Errorf("newly created transport has nil TLSClientConfig")
	}
}

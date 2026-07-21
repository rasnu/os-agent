// Copyright © 2026 Rasnu LLC (rasnu.com). All rights reserved.
// Licensed under the Functional Source License, Version 1.1, ALv2 Future License (FSL-1.1-ALv2).

package transport

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
	dummySPKI := []byte("dummy_public_key_info")
	cert := &x509.Certificate{
		RawSubjectPublicKeyInfo: dummySPKI,
	}

	hash := sha256.Sum256(dummySPKI)
	hashHex := hex.EncodeToString(hash[:])

	cs := tls.ConnectionState{
		VerifiedChains: [][]*x509.Certificate{
			{cert},
		},
	}

	t.Run("Valid pin", func(t *testing.T) {
		config := PinnedTLSConfig(hashHex)
		err := config.VerifyConnection(cs)
		if err != nil {
			t.Errorf("Expected nil error for valid pin, got %v", err)
		}
	})

	t.Run("Valid pin with prefix", func(t *testing.T) {
		config := PinnedTLSConfig("sha256/" + hashHex)
		err := config.VerifyConnection(cs)
		if err != nil {
			t.Errorf("Expected nil error for valid pin with prefix, got %v", err)
		}
	})

	t.Run("Invalid pin", func(t *testing.T) {
		config := PinnedTLSConfig("invalid_hash")
		err := config.VerifyConnection(cs)
		if err == nil {
			t.Errorf("Expected error for invalid pin, got nil")
		}
	})

	t.Run("Multiple pins including valid", func(t *testing.T) {
		config := PinnedTLSConfig("invalid_hash," + hashHex)
		err := config.VerifyConnection(cs)
		if err != nil {
			t.Errorf("Expected nil error for valid pin in list, got %v", err)
		}
	})
}

func TestApplyTLSPinToDefaultTransport(t *testing.T) {
	origDefault := Default()
	origHeartbeat := Heartbeat
	origUpdater := Updater
	origFileTransfer := FileTransfer
	defer func() {
		SetDefault(origDefault)
		Heartbeat = origHeartbeat
		Updater = origUpdater
		FileTransfer = origFileTransfer
		SetGlobalTLSConfig(nil)
	}()

	SetDefault(&http.Client{Timeout: 30 * time.Second, Transport: defaultTransport()})
	Heartbeat = &http.Client{Timeout: 30 * time.Second, Transport: defaultTransport()}
	Updater = &http.Client{Timeout: 240 * time.Second, Transport: defaultTransport()}
	FileTransfer = &http.Client{Timeout: 0, Transport: defaultTransport()}

	ApplyTLSPin("dummy_pin")

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

	clients := map[string]HTTPDoer{
		"defaultClient": Default(),
		"Heartbeat":     Heartbeat,
		"Updater":       Updater,
		"FileTransfer":  FileTransfer,
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

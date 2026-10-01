package main

import (
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"net/http"
	"testing"
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
	applyTLSPinToDefaultTransport("dummy_pin")
	
	transport, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		t.Fatalf("http.DefaultTransport is not an *http.Transport")
	}
	
	if transport.TLSClientConfig == nil {
		t.Fatalf("TLSClientConfig was not set")
	}
	
	if transport.TLSClientConfig.InsecureSkipVerify {
		t.Errorf("InsecureSkipVerify should be false")
	}
}

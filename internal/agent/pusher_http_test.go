// Copyright © 2026 Rasnu LLC (rasnu.com). All rights reserved.
// Licensed under the Functional Source License, Version 1.1, ALv2 Future License (FSL-1.1-ALv2).

package agent

import (
	"bytes"
	"io"
	"net/http"
	"testing"

	"rasnuos-agent/internal/transport"
)

func TestPusherClient_SendHeartbeat(t *testing.T) {
	originalClient := transport.Heartbeat
	mockHTTP := &MockHTTPClient{
		Responses: []*http.Response{
			{
				StatusCode: 200,
				Body:       io.NopCloser(bytes.NewBufferString(`{"status":"ok"}`)),
			},
		},
	}
	transport.Heartbeat = mockHTTP
	defer func() { transport.Heartbeat = originalClient }()

	client := &PusherClient{
		ServerID: "test-server",
		ApiURL:   "http://test",
		Token:    "test-token",
	}

	_ = client.sendHeartbeatWithStatus()

	if mockHTTP.CallCount == 0 {
		t.Error("Expected sendHeartbeatWithStatus to make an HTTP request")
	}
}

func TestPusherClient_AuthenticateChannel(t *testing.T) {
	originalClient := transport.Default()
	mockHTTP := &MockHTTPClient{
		Responses: []*http.Response{
			{
				StatusCode: 200,
				Body:       io.NopCloser(bytes.NewBufferString(`{"auth":"mock-signature"}`)),
			},
		},
	}
	transport.SetDefault(mockHTTP)
	defer func() { transport.SetDefault(originalClient) }()

	client := &PusherClient{
		ServerID: "test-server",
		ApiURL:   "http://test",
		Token:    "test-token",
	}

	auth, err := client.authenticateChannel("123.456", "private-server.test-server")
	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}
	if auth != "mock-signature" {
		t.Errorf("Expected mock-signature, got %s", auth)
	}
}

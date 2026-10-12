package main

import (
	"bytes"
	"io"
	"net/http"
	"testing"
)

func TestPusherClient_SendHeartbeat(t *testing.T) {
	originalClient := heartbeatHTTPClient
	mockHTTP := &MockHTTPClient{
		Responses: []*http.Response{
			{
				StatusCode: 200,
				Body:       io.NopCloser(bytes.NewBufferString(`{"status":"ok"}`)),
			},
		},
	}
	heartbeatHTTPClient = mockHTTP
	defer func() { heartbeatHTTPClient = originalClient }()

	client := &PusherClient{
		ServerID: "test-server",
		ApiURL:   "http://test",
		Token:    "test-token",
	}

	client.sendHeartbeat()

	if mockHTTP.CallCount == 0 {
		t.Error("Expected sendHeartbeat to make an HTTP request")
	}
}

func TestPusherClient_AuthenticateChannel(t *testing.T) {
	originalClient := getDefaultHTTPClient()
	mockHTTP := &MockHTTPClient{
		Responses: []*http.Response{
			{
				StatusCode: 200,
				Body:       io.NopCloser(bytes.NewBufferString(`{"auth":"mock-signature"}`)),
			},
		},
	}
	setDefaultHTTPClient(mockHTTP)
	defer func() { setDefaultHTTPClient(originalClient) }()

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

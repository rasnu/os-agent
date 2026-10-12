package main

import (
	"testing"
	"time"
)

func TestStartMetricsEngine(t *testing.T) {
	origDefault := getDefaultHTTPClient()
	origHeartbeat := heartbeatHTTPClient
	defer func() {
		setDefaultHTTPClient(origDefault)
		heartbeatHTTPClient = origHeartbeat
	}()

	mockHTTP := &MockHTTPClient{}
	setDefaultHTTPClient(mockHTTP)
	heartbeatHTTPClient = mockHTTP

	client := NewPusherClient("ws://", "http://localhost/api", "token", "srv", "")

	shutdownChan := make(chan struct{})
	done := make(chan struct{})

	go func() {
		client.StartMetricsEngine(shutdownChan)
		close(done)
	}()

	close(shutdownChan)

	select {
	case <-done:
	case <-time.After(1 * time.Second):
		t.Fatal("StartMetricsEngine did not exit on shutdown")
	}
}

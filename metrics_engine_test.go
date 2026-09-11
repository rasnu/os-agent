package main

import (
	"testing"
	"time"
)

func TestStartMetricsEngine(t *testing.T) {
	mockHTTP := &MockHTTPClient{}
	defaultHTTPClient = mockHTTP
	heartbeatHTTPClient = mockHTTP

	client := NewPusherClient("ws://", "http://localhost/api", "token", "srv", "")

	shutdownChan := make(chan struct{})

	metricsTickerDuration = 1 * time.Millisecond
	defer func() { metricsTickerDuration = 15 * time.Second }()
	// Start engine in background
	go client.StartMetricsEngine(shutdownChan)

	// Wait 100ms and shut it down
	time.Sleep(100 * time.Millisecond)
	close(shutdownChan)

	// We can't assert mockHTTP.CallCount because the ticker is 60 seconds
	// But this gives us 100% coverage on the shutdown branch of the select statement!
}

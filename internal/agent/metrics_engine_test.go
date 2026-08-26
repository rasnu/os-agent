// Copyright © 2026 Rasnu LLC (rasnu.com). All rights reserved.
// Licensed under the Functional Source License, Version 1.1, ALv2 Future License (FSL-1.1-ALv2).

package agent

import (
	"testing"
	"time"

	"rasnuos-agent/internal/transport"
)

func TestStartMetricsEngine(t *testing.T) {
	origDefault := transport.Default()
	origHeartbeat := transport.Heartbeat
	defer func() {
		transport.SetDefault(origDefault)
		transport.Heartbeat = origHeartbeat
	}()

	mockHTTP := &MockHTTPClient{}
	transport.SetDefault(mockHTTP)
	transport.Heartbeat = mockHTTP

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

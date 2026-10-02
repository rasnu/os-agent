// Copyright © 2026 Rasnu LLC (rasnu.com). All rights reserved.
// Licensed under the Functional Source License, Version 1.1, ALv2 Future License (FSL-1.1-ALv2).

package agent

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestPusherClient_ConnectAndMonitor_Shutdown(t *testing.T) {
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"auth": "signed_auth"}`))
	}))
	defer httpServer.Close()

	client := NewPusherClient("ws://localhost:9999", httpServer.URL, "token", "srv1", "")

	shutdownChan := make(chan struct{})
	done := make(chan struct{})

	go func() {
		client.ConnectAndMonitor(shutdownChan)
		close(done)
	}()

	time.Sleep(100 * time.Millisecond)
	close(shutdownChan)

	<-done
}

func TestPusherClient_MonitorConnection_Shutdown(t *testing.T) {
	client := NewPusherClient("ws://localhost:9999", "http://localhost/api", "token", "srv1", "")
	client.MonitorInterval = 1 * time.Millisecond

	shutdownChan := make(chan struct{})
	done := make(chan struct{})

	go func() {
		client.monitorConnection(shutdownChan)
		close(done)
	}()

	time.Sleep(50 * time.Millisecond)
	close(shutdownChan)

	<-done
}

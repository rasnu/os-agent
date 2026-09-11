package main

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

	// Start in background. Since Connect fails, it will trigger the retry loop.
	go client.ConnectAndMonitor(shutdownChan)

	// Wait for it to hit the retry sleep, then close
	time.Sleep(100 * time.Millisecond)
	close(shutdownChan)
}

func TestPusherClient_MonitorConnection_Shutdown(t *testing.T) {
	client := NewPusherClient("ws://localhost:9999", "http://localhost/api", "token", "srv1", "")

	shutdownChan := make(chan struct{})

	monitorTickerDuration = 1 * time.Millisecond
	defer func() { monitorTickerDuration = 30 * time.Second }()
	// Start monitor
	go client.monitorConnection(shutdownChan)

	// Wait and close
	time.Sleep(100 * time.Millisecond)
	close(shutdownChan)
}

package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{}

func TestPusherClient_ConnectAndSubscribe(t *testing.T) {
	// 1. Setup mock WebSocket server
	wsServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()

		// Send connection established
		msg := `{"event":"pusher:connection_established","data":"{\"socket_id\":\"123.456\"}"}`
		c.WriteMessage(websocket.TextMessage, []byte(msg))

		// Wait for pusher:subscribe
		_, message, _ := c.ReadMessage()
		if !strings.Contains(string(message), "pusher:subscribe") {
			t.Errorf("expected subscribe, got %s", string(message))
		}

		// Close to end the loop
		c.Close()
	}))
	defer wsServer.Close()

	wsURL := "ws" + strings.TrimPrefix(wsServer.URL, "http")

	// 2. Setup mock HTTP auth server
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "auth") {
			w.Write([]byte(`{"auth": "signed_auth"}`))
		} else if strings.Contains(r.URL.Path, "heartbeat") {
			w.Write([]byte(`{"status": "ok"}`))
		}
	}))
	defer httpServer.Close()

	// Create client with standard http client pointing to mock server
	client := NewPusherClient(wsURL, httpServer.URL, "token", "srv1", "")

	// We replace heartbeat client to hit local server without custom HTTPDoer mocks
	// because we pass the URL directly.

	origDefault := defaultHTTPClient
	origHeartbeat := heartbeatHTTPClient
	defer func() {
		defaultHTTPClient = origDefault
		heartbeatHTTPClient = origHeartbeat
	}()

	defaultHTTPClient = &http.Client{}
	heartbeatHTTPClient = &http.Client{}

	err := client.Connect()
	if err != nil {
		t.Fatalf("Connect failed: %v", err)
	}

	client.authenticateChannel("123", "test")
	client.sendHeartbeat()
	client.subscribe("123")

	client.Close()
	<-client.done
}

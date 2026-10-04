// Copyright © 2026 Rasnu LLC (rasnu.com). All rights reserved.
// Licensed under the Functional Source License, Version 1.1, ALv2 Future License (FSL-1.1-ALv2).

package agent

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"rasnuos-agent/internal/transport"

	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{}

func TestPusherClient_ConnectAndSubscribe(t *testing.T) {
	wsServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()

		msg := `{"event":"pusher:connection_established","data":"{\"socket_id\":\"123.456\"}"}`
		c.WriteMessage(websocket.TextMessage, []byte(msg))

		_, message, _ := c.ReadMessage()
		if !strings.Contains(string(message), "pusher:subscribe") {
			t.Errorf("expected subscribe, got %s", string(message))
		}

		c.Close()
	}))
	defer wsServer.Close()

	wsURL := "ws" + strings.TrimPrefix(wsServer.URL, "http")

	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "auth") {
			w.Write([]byte(`{"auth": "signed_auth"}`))
		} else if strings.Contains(r.URL.Path, "heartbeat") {
			w.Write([]byte(`{"status": "ok"}`))
		}
	}))
	defer httpServer.Close()

	client := NewPusherClient(wsURL, httpServer.URL, "token", "srv1", "")

	origDefault := transport.Default()
	origHeartbeat := transport.Heartbeat
	defer func() {
		transport.SetDefault(origDefault)
		transport.Heartbeat = origHeartbeat
	}()

	transport.SetDefault(&http.Client{})
	transport.Heartbeat = &http.Client{}

	err := client.Connect()
	if err != nil {
		t.Fatalf("Connect failed: %v", err)
	}

	client.authenticateChannel("123", "test")
	_ = client.sendHeartbeatWithStatus()
	client.subscribe("123")

	client.Close()
	<-client.done
}

// Copyright © 2026 Rasnu LLC (rasnu.com). All rights reserved.
// Licensed under the Functional Source License, Version 1.1, ALv2 Future License (FSL-1.1-ALv2).

package agent

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestPusherClient_ReadPump_ErrorBranches(t *testing.T) {
	wsServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()

		c.WriteMessage(websocket.TextMessage, []byte(`{bad json`))
		c.WriteMessage(websocket.TextMessage, []byte(`{"event":"pusher:connection_established","data":"not json"}`))
		c.WriteMessage(websocket.TextMessage, []byte(`{"event":"pusher:connection_established","data":"{}"}`))
		c.WriteMessage(websocket.TextMessage, []byte(`{"event":"pusher:ping"}`))
		c.WriteMessage(websocket.TextMessage, []byte(`{"event":"ExecuteCommand","data":"not json"}`))
		c.WriteMessage(websocket.TextMessage, []byte(`{"event":"ExecuteCommand","data":"{\"command\": \"ls\"}"}`))
		c.WriteMessage(websocket.TextMessage, []byte(`{"event":"CancelCommand","data":"not json"}`))
		c.WriteMessage(websocket.TextMessage, []byte(`{"event":"CancelCommand","data":"{}"}`))
		c.WriteMessage(websocket.TextMessage, []byte(`{"event":"ExecuteDiscovery","data":"not json"}`))
		c.WriteMessage(websocket.TextMessage, []byte(`{"event":"ExecuteDiscovery","data":"{}"}`))
		c.WriteMessage(websocket.TextMessage, []byte(`{"event":"AgentCommandIssued","data":"not json"}`))
		c.WriteMessage(websocket.TextMessage, []byte(`{"event":"AgentCommandIssued","data":"{}"}`))
		c.WriteMessage(websocket.TextMessage, []byte(`{"event":"client-TerminalInput","data":"not json"}`))
		c.WriteMessage(websocket.TextMessage, []byte(`{"event":"client-TerminalInput","data":"{}"}`))
		c.WriteMessage(websocket.TextMessage, []byte(`{"event":"client-TerminalResize","data":"not json"}`))
		c.WriteMessage(websocket.TextMessage, []byte(`{"event":"client-TerminalResize","data":"{}"}`))

		time.Sleep(50 * time.Millisecond)
		c.Close()
	}))
	defer wsServer.Close()

	wsURL := "ws" + strings.TrimPrefix(wsServer.URL, "http")

	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"auth": "signed_auth"}`))
	}))
	defer httpServer.Close()

	client := NewPusherClient(wsURL, httpServer.URL, "token", "srv1", "")

	err := client.Connect()
	if err != nil {
		t.Fatalf("Connect failed: %v", err)
	}

	<-client.done
}

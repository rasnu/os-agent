package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestPusherClient_ReadPumpFullEvents(t *testing.T) {
	wsServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()

		c.WriteMessage(websocket.TextMessage, []byte(`{"event":"pusher:connection_established","data":"{\"socket_id\":\"123.456\"}"}`))

		// Send Discovery event
		c.WriteMessage(websocket.TextMessage, []byte(`{
			"event": "ExecuteDiscovery",
			"data": "{\"script\": \"echo hello\"}"
		}`))

		// Send CancelCommand event
		c.WriteMessage(websocket.TextMessage, []byte(`{
			"event": "CancelCommand",
			"data": "{\"command_id\": \"step1\"}"
		}`))

		// Send ExecuteCommand event
		c.WriteMessage(websocket.TextMessage, []byte(`{
			"event": "ExecuteCommand",
			"data": "{\"command_id\": \"step2\", \"script\": \"echo test\"}"
		}`))

		// Send AgentCommandIssued (DB Query)
		c.WriteMessage(websocket.TextMessage, []byte(`{
			"event": "AgentCommandIssued",
			"data": "{\"command_id\": \"q1\", \"command\": \"db-query\", \"parameters\": {\"database_type\": \"mysql\", \"database_name\": \"mydb\", \"query\": \"SELECT 1\", \"readonly\": true}}"
		}`))

		// Send AgentCommandIssued (Firewall)
		c.WriteMessage(websocket.TextMessage, []byte(`{
			"event": "AgentCommandIssued",
			"data": "{\"command_id\": \"f1\", \"command\": \"firewall-manage\", \"parameters\": {\"action\": \"list\"}}"
		}`))

		// Send Terminal Input
		c.WriteMessage(websocket.TextMessage, []byte(`{
			"event": "client-TerminalInput",
			"data": "{\"term_id\": \"term1\", \"data\": \"ls -la\\n\"}"
		}`))

		// Send Terminal Resize
		c.WriteMessage(websocket.TextMessage, []byte(`{
			"event": "client-TerminalResize",
			"data": "{\"term_id\": \"term1\", \"rows\": 24, \"cols\": 80}"
		}`))

		// Send Pusher Error
		c.WriteMessage(websocket.TextMessage, []byte(`{
			"event": "pusher:error",
			"data": "{\"message\": \"auth failed\"}"
		}`))

		// Send Subscription Succeeded
		c.WriteMessage(websocket.TextMessage, []byte(`{
			"event": "pusher_internal:subscription_succeeded",
			"data": "{}"
		}`))

		// Send unknown event
		c.WriteMessage(websocket.TextMessage, []byte(`{
			"event": "UnknownEvent",
			"data": "{}"
		}`))

		// Give agent time to process, then close
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

	// This will block until the wsServer closes and the connection drops
	<-client.done
	client.CancelAllCommands()
	client.WaitCommands()
}

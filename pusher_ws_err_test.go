package main

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

		// 1. Bad JSON
		c.WriteMessage(websocket.TextMessage, []byte(`{bad json`))

		// 2. Bad Internal Data parsing
		c.WriteMessage(websocket.TextMessage, []byte(`{"event":"pusher:connection_established","data":"not json"}`))

		// 3. connection_established missing socket_id
		c.WriteMessage(websocket.TextMessage, []byte(`{"event":"pusher:connection_established","data":"{}"}`))

		// 4. pusher:ping
		c.WriteMessage(websocket.TextMessage, []byte(`{"event":"pusher:ping"}`))

		// 5. ExecuteCommand parsing error
		c.WriteMessage(websocket.TextMessage, []byte(`{"event":"ExecuteCommand","data":"not json"}`))

		// 6. ExecuteCommand missing step_id
		c.WriteMessage(websocket.TextMessage, []byte(`{"event":"ExecuteCommand","data":"{\"command\": \"ls\"}"}`))

		// 7. CancelCommand parsing error
		c.WriteMessage(websocket.TextMessage, []byte(`{"event":"CancelCommand","data":"not json"}`))

		// 8. CancelCommand missing step_id
		c.WriteMessage(websocket.TextMessage, []byte(`{"event":"CancelCommand","data":"{}"}`))

		// 9. ExecuteDiscovery parsing error
		c.WriteMessage(websocket.TextMessage, []byte(`{"event":"ExecuteDiscovery","data":"not json"}`))

		// 10. ExecuteDiscovery missing script
		c.WriteMessage(websocket.TextMessage, []byte(`{"event":"ExecuteDiscovery","data":"{}"}`))

		// 11. AgentCommandIssued parsing error
		c.WriteMessage(websocket.TextMessage, []byte(`{"event":"AgentCommandIssued","data":"not json"}`))

		// 12. AgentCommandIssued missing command_id
		c.WriteMessage(websocket.TextMessage, []byte(`{"event":"AgentCommandIssued","data":"{}"}`))

		// 13. TerminalInput parsing error
		c.WriteMessage(websocket.TextMessage, []byte(`{"event":"client-TerminalInput","data":"not json"}`))

		// 14. TerminalInput missing term_id
		c.WriteMessage(websocket.TextMessage, []byte(`{"event":"client-TerminalInput","data":"{}"}`))

		// 15. TerminalResize parsing error
		c.WriteMessage(websocket.TextMessage, []byte(`{"event":"client-TerminalResize","data":"not json"}`))

		// 16. TerminalResize missing term_id
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

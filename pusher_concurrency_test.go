package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestPusherClient_ConcurrencyLimit2(t *testing.T) {
	wsServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()

		// Send MaxConcurrentCommands + 2 commands
		for i := 0; i < MaxConcurrentCommands+2; i++ {
			c.WriteMessage(websocket.TextMessage, []byte(`{"event":"ExecuteCommand","data":"{\"step_id\": \"step1\", \"command\": \"sleep 10\"}"}`))
		}

		time.Sleep(100 * time.Millisecond)
		c.Close()
	}))
	defer wsServer.Close()

	wsURL := "ws" + strings.TrimPrefix(wsServer.URL, "http")
	client := NewPusherClient(wsURL, "http://localhost", "token", "srv1", "")
	client.Connect()
	<-client.done
}

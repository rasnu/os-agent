package main

import (
	"fmt"
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
			c.WriteMessage(websocket.TextMessage, []byte(fmt.Sprintf(`{"event":"ExecuteCommand","data":"{\"step_id\": \"step-%d\", \"command\": \"sleep 1\"}"}`, i)))
		}

		time.Sleep(100 * time.Millisecond)
		c.Close()
	}))
	defer wsServer.Close()

	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"success": true}`))
	}))
	defer httpServer.Close()

	wsURL := "ws" + strings.TrimPrefix(wsServer.URL, "http")
	client := NewPusherClient(wsURL, httpServer.URL, "token", "srv1", "")
	client.Connect()
	<-client.done

	client.CancelAllCommands()
	client.WaitCommands()
}


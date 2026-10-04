// Copyright © 2026 Rasnu LLC (rasnu.com). All rights reserved.
// Licensed under the Functional Source License, Version 1.1, ALv2 Future License (FSL-1.1-ALv2).

package agent

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"rasnuos-agent/internal/osadapter"

	"github.com/gorilla/websocket"
)

func TestAgent_E2E_ExecuteCommand_And_Stream_Completion(t *testing.T) {
	originalAdapter := osadapter.Current()
	osadapter.SetAdapter(&MockOSAdapter{
		MockShellOutput: "E2E_Test_Success",
	})
	defer osadapter.SetAdapter(originalAdapter)

	logChan := make(chan string, 10)
	completionChan := make(chan bool, 1)

	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/agent/broadcasting/auth" {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"auth": "mock_signed_auth"}`))
			return
		}

		if r.URL.Path == "/agent/log" {
			bodyBytes, _ := io.ReadAll(r.Body)
			var payload map[string]interface{}
			json.Unmarshal(bodyBytes, &payload)

			if chunk, ok := payload["log_chunk"].(string); ok {
				logChan <- chunk
			}
			return
		}

		if r.URL.Path == "/agent/complete" {
			bodyBytes, _ := io.ReadAll(r.Body)
			var payload map[string]interface{}
			json.Unmarshal(bodyBytes, &payload)

			if fullLog, ok := payload["log"].(string); ok {
				logChan <- fullLog
			}

			if success, ok := payload["success"].(bool); ok {
				completionChan <- success
			}
			return
		}

		w.WriteHeader(404)
	}))
	defer httpServer.Close()

	wsServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()

		c.WriteMessage(websocket.TextMessage, []byte(`{"event":"pusher:connection_established","data":"{\"socket_id\":\"123.456\"}"}`))

		commandSent := false
		for {
			_, msgBytes, err := c.ReadMessage()
			if err != nil {
				return
			}

			var msg struct {
				Event string                 `json:"event"`
				Data  map[string]interface{} `json:"data"`
			}
			if err := json.Unmarshal(msgBytes, &msg); err == nil {
				if msg.Event == "pusher:subscribe" {
					channel, _ := msg.Data["channel"].(string)
					ackPayload, _ := json.Marshal(map[string]string{"channel": channel})
					ackEvent, _ := json.Marshal(map[string]interface{}{
						"event":   "pusher_internal:subscription_succeeded",
						"channel": channel,
						"data":    string(ackPayload),
					})
					c.WriteMessage(websocket.TextMessage, ackEvent)

					if strings.HasSuffix(channel, ".commands") && !commandSent {
						commandSent = true
						c.WriteMessage(websocket.TextMessage, []byte(`{
							"event": "ExecuteCommand",
							"data": "{\"pipeline_id\": \"pipe-e2e\", \"step_id\": \"step-e2e\", \"command\": \"echo E2E_Test_Success\"}"
						}`))
					}
				} else if msg.Event == "pusher:ping" {
					c.WriteMessage(websocket.TextMessage, []byte(`{"event":"pusher:pong"}`))
				}
			}
		}
	}))
	defer wsServer.Close()

	wsURL := "ws" + strings.TrimPrefix(wsServer.URL, "http")

	client := NewPusherClient(wsURL, httpServer.URL, "test-token", "srv-e2e", "")
	defer client.Close()
	defer client.WaitCommands()

	err := client.Connect()
	if err != nil {
		t.Fatalf("Failed to connect agent to mock websocket: %v", err)
	}

	var receivedLog string
	var completionSuccess bool

	timeout := time.After(10 * time.Second)
	completed := false

	for !completed {
		select {
		case chunk := <-logChan:
			receivedLog += chunk
		case success := <-completionChan:
			completionSuccess = success
			completed = true
		case <-timeout:
			t.Fatalf("Timeout waiting for agent to execute command and post HTTP completion")
		}
	}

	// Drain trailing chunks because streamLogHTTP runs asynchronously.
	time.Sleep(100 * time.Millisecond)
drainLoop:
	for {
		select {
		case chunk := <-logChan:
			receivedLog += chunk
		default:
			break drainLoop
		}
	}

	if !completionSuccess {
		t.Errorf("Agent reported completion failure instead of success")
	}
	if !strings.Contains(receivedLog, "E2E_Test_Success") {
		t.Errorf("Streamed log did not contain expected output. Got: %q", receivedLog)
	}
}

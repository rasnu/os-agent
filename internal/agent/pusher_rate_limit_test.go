// Copyright © 2026 Rasnu LLC (rasnu.com). All rights reserved.
// Licensed under the Functional Source License, Version 1.1, ALv2 Future License (FSL-1.1-ALv2).

package agent

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"rasnuos-agent/internal/audit"
	"rasnuos-agent/internal/ratelimit"
	"rasnuos-agent/internal/transport"

	"github.com/gorilla/websocket"
)

func TestPusherRateLimit_DefaultConfig(t *testing.T) {
	client := NewPusherClient("ws://localhost:6001", "http://localhost:8000/api", "token", "srv1", "")
	if client.rateLimiter == nil {
		t.Fatalf("expected rateLimiter to be initialized on PusherClient")
	}
	if client.rateLimiter.Capacity() != DefaultCommandRateLimit {
		t.Fatalf("expected default capacity %d, got %d", DefaultCommandRateLimit, client.rateLimiter.Capacity())
	}

	os.Setenv("AGENT_RATE_LIMIT_PER_MINUTE", "50")
	defer os.Unsetenv("AGENT_RATE_LIMIT_PER_MINUTE")

	clientEnv := NewPusherClient("ws://localhost:6001", "http://localhost:8000/api", "token", "srv1", "")
	if clientEnv.rateLimiter.Capacity() != 50 {
		t.Fatalf("expected overridden capacity 50, got %d", clientEnv.rateLimiter.Capacity())
	}
}

func TestPusherRateLimit_EventThrottling(t *testing.T) {
	tmpDir := t.TempDir()
	prevAuditOverride := audit.SetPathOverride(tmpDir + "/test_audit.jsonl")
	defer audit.SetPathOverride(prevAuditOverride)

	var mu sync.Mutex
	completions := make([]map[string]interface{}, 0)

	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "complete") {
			body, _ := io.ReadAll(r.Body)
			var payload map[string]interface{}
			_ = json.Unmarshal(body, &payload)
			mu.Lock()
			completions = append(completions, payload)
			mu.Unlock()
			w.WriteHeader(http.StatusOK)
			return
		}
		if strings.Contains(r.URL.Path, "auth") {
			w.Write([]byte(`{"auth": "signed_auth"}`))
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer httpServer.Close()

	origHTTP := transport.Default()
	transport.SetDefault(httpServer.Client())
	defer func() { transport.SetDefault(origHTTP) }()

	wsServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()

		_ = c.WriteMessage(websocket.TextMessage, []byte(`{"event":"pusher:connection_established","data":"{\"socket_id\":\"123.456\"}"}`))

		_, _, _ = c.ReadMessage()

		cmd1 := `{"event":"ExecuteCommand","data":"{\"pipeline_id\":\"p1\",\"step_id\":\"s1\",\"command\":\"echo 1\",\"timeout_seconds\":5}"}`
		cmd2 := `{"event":"ExecuteCommand","data":"{\"pipeline_id\":\"p1\",\"step_id\":\"s2\",\"command\":\"echo 2\",\"timeout_seconds\":5}"}`
		cmd3 := `{"event":"ExecuteCommand","data":"{\"pipeline_id\":\"p1\",\"step_id\":\"s3\",\"command\":\"echo 3\",\"timeout_seconds\":5}"}`

		_ = c.WriteMessage(websocket.TextMessage, []byte(cmd1))
		time.Sleep(50 * time.Millisecond)
		_ = c.WriteMessage(websocket.TextMessage, []byte(cmd2))
		time.Sleep(50 * time.Millisecond)
		_ = c.WriteMessage(websocket.TextMessage, []byte(cmd3))

		time.Sleep(300 * time.Millisecond)
		c.Close()
	}))
	defer wsServer.Close()

	wsURL := "ws" + strings.TrimPrefix(wsServer.URL, "http")

	client := NewPusherClient(wsURL, httpServer.URL, "token", "srv1", "")
	client.SetRateLimiter(ratelimit.New(2))

	err := client.Connect()
	if err != nil {
		t.Fatalf("Connect failed: %v", err)
	}

	select {
	case <-client.done:
	case <-time.After(2 * time.Second):
		t.Fatalf("Timed out waiting for client to disconnect")
	}

	client.CancelAllCommands()
	client.WaitCommands()

	mu.Lock()
	defer mu.Unlock()

	var rateLimitRejected bool
	for _, comp := range completions {
		stepId, _ := comp["step_id"].(string)
		success, _ := comp["success"].(bool)
		logStr, _ := comp["log"].(string)

		if stepId == "s3" && !success && strings.Contains(logStr, "rate limit exceeded") {
			rateLimitRejected = true
		}
	}

	if !rateLimitRejected {
		t.Fatalf("expected step s3 to be rejected with rate limit exceeded, received: %+v", completions)
	}
}

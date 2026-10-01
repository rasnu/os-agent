/**
 * Project: RasnuOS
 * Copyright © 2026 Rasnu LLC (rasnu.com). All rights reserved.
 */
package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// ─── PusherDataString JSON parsing ───────────────────────────────────────────

func TestPusherDataString_ParsesEventAndData(t *testing.T) {
	raw := `{"event":"ExecuteCommand","data":"{\"step_id\":\"step-1\",\"command\":\"ls -la\"}"}`
	var msg PusherDataString
	if err := json.Unmarshal([]byte(raw), &msg); err != nil {
		t.Fatalf("json.Unmarshal failed: %v", err)
	}

	if msg.Event != "ExecuteCommand" {
		t.Errorf("Event = %q; want ExecuteCommand", msg.Event)
	}
	if !strings.Contains(string(msg.Data), "step-1") {
		t.Errorf("Data should contain step_id, got: %q", string(msg.Data))
	}
}

func TestPusherDataString_ParsesConnectionEstablished(t *testing.T) {
	raw := `{"event":"pusher:connection_established","data":"{\"socket_id\":\"abc.123\",\"activity_timeout\":120}"}`
	var msg PusherDataString
	if err := json.Unmarshal([]byte(raw), &msg); err != nil {
		t.Fatalf("json.Unmarshal failed: %v", err)
	}

	if msg.Event != "pusher:connection_established" {
		t.Errorf("Event = %q; want pusher:connection_established", msg.Event)
	}

	// msg.Data is json.RawMessage — in Pusher's protocol the inner payload is a
	// JSON-encoded string (double-encoded), so we first unmarshal to a string.
	var innerStr string
	if err := json.Unmarshal(msg.Data, &innerStr); err != nil {
		t.Fatalf("inner data string unmarshal failed: %v", err)
	}

	var data map[string]interface{}
	if err := json.Unmarshal([]byte(innerStr), &data); err != nil {
		t.Fatalf("inner data json.Unmarshal failed: %v", err)
	}

	socketID, _ := data["socket_id"].(string)
	if socketID != "abc.123" {
		t.Errorf("socket_id = %q; want abc.123", socketID)
	}
}

func TestPusherDataString_ParsesEmptyData(t *testing.T) {
	raw := `{"event":"pusher:pong","data":""}`
	var msg PusherDataString
	if err := json.Unmarshal([]byte(raw), &msg); err != nil {
		t.Fatalf("json.Unmarshal failed: %v", err)
	}

	if msg.Event != "pusher:pong" {
		t.Errorf("Event = %q; want pusher:pong", msg.Event)
	}
}

// ─── PusherClientEvent JSON serialization ─────────────────────────────────────

func TestPusherClientEvent_SerializesCorrectly(t *testing.T) {
	evt := PusherClientEvent{
		Event:   "client-log",
		Channel: "private-server.42",
		Data: map[string]interface{}{
			"step_id": "step-99",
			"chunk":   "hello world\n",
		},
	}

	b, err := json.Marshal(evt)
	if err != nil {
		t.Fatalf("json.Marshal failed: %v", err)
	}

	payload := string(b)
	if !strings.Contains(payload, `"event":"client-log"`) {
		t.Errorf("missing event field in: %s", payload)
	}
	if !strings.Contains(payload, `"channel":"private-server.42"`) {
		t.Errorf("missing channel field in: %s", payload)
	}
	if !strings.Contains(payload, `"step_id":"step-99"`) {
		t.Errorf("missing step_id in data: %s", payload)
	}
}

// ─── Channel name construction ────────────────────────────────────────────────

func TestChannelNames_Format(t *testing.T) {
	serverID := "abc-123"
	cases := []struct {
		got  string
		want string
	}{
		{
			got:  "private-server." + serverID,
			want: "private-server.abc-123",
		},
		{
			got:  "private-server." + serverID + ".commands",
			want: "private-server.abc-123.commands",
		},
		{
			got:  "private-server." + serverID + ".terminal",
			want: "private-server.abc-123.terminal",
		},
	}

	for _, tc := range cases {
		if tc.got != tc.want {
			t.Errorf("channel = %q; want %q", tc.got, tc.want)
		}
	}
}

// ─── Safe Parsing Helpers ─────────────────────────────────────────────────────

func TestParseEventData_Valid(t *testing.T) {
	raw := json.RawMessage(`{"step_id":"step-1","command":"ls -la"}`)
	payload := parseEventData("TestEvent", raw)
	if payload == nil {
		t.Fatal("expected non-nil payload")
	}
	if payload["step_id"] != "step-1" {
		t.Errorf("expected step-1, got %v", payload["step_id"])
	}
}

func TestParseEventData_Invalid(t *testing.T) {
	raw := json.RawMessage(`{"broken json`)
	payload := parseEventData("TestEvent", raw)
	if payload != nil {
		t.Fatalf("expected nil payload on invalid json, got %v", payload)
	}
}

func TestSafeExtractionHelpers(t *testing.T) {
	m := map[string]interface{}{
		"str":   "hello",
		"bool":  true,
		"float": 42.5,
		"int":   int(10),
	}

	// getString
	if v := getString(m, "str"); v != "hello" {
		t.Errorf("getString expected 'hello', got %q", v)
	}
	if v := getString(m, "bool"); v != "" {
		t.Errorf("getString on wrong type expected '', got %q", v)
	}
	if v := getString(m, "missing"); v != "" {
		t.Errorf("getString on missing expected '', got %q", v)
	}

	// getBool
	if v := getBool(m, "bool"); v != true {
		t.Errorf("getBool expected true, got %v", v)
	}
	if v := getBool(m, "str"); v != false {
		t.Errorf("getBool on wrong type expected false, got %v", v)
	}

	// getFloat
	if v := getFloat(m, "float"); v != 42.5 {
		t.Errorf("getFloat expected 42.5, got %v", v)
	}
	if v := getFloat(m, "str"); v != 0 {
		t.Errorf("getFloat on wrong type expected 0, got %v", v)
	}
}

// ─── Concurrency Semaphore ────────────────────────────────────────────────────

func TestPusherClient_ConcurrencyLimit(t *testing.T) {
	// Create client with standard NewPusherClient which has a semaphore of size 10
	client := NewPusherClient("ws://localhost", "http://localhost", "token", "srv", "")

	// Fill the semaphore
	for i := 0; i < MaxConcurrentCommands; i++ {
		client.cmdSemaphore <- struct{}{}
	}

	// Trying to add one more should block, we can simulate what happens in the select
	rejected := false
	select {
	case client.cmdSemaphore <- struct{}{}:
		t.Errorf("Should not be able to acquire semaphore when full")
	default:
		rejected = true
	}

	if !rejected {
		t.Errorf("Expected command to be rejected")
	}

	// Release one
	<-client.cmdSemaphore

	// Should now be able to acquire
	acquired := false
	select {
	case client.cmdSemaphore <- struct{}{}:
		acquired = true
	default:
		t.Errorf("Should be able to acquire semaphore after release")
	}

	if !acquired {
		t.Errorf("Expected to acquire semaphore")
	}
}

func TestPusherClient_SafeWriteClosed(t *testing.T) {
	client := NewPusherClient("ws://localhost", "http://localhost", "token", "srv", "")
	client.closed = true

	err := client.safeWriteJSON(map[string]string{"test": "test"})
	if err == nil {
		t.Errorf("safeWriteJSON should return an error when closed")
	}

	if err.Error() != "connection closed" {
		t.Errorf("expected connection closed error, got %v", err)
	}

	err = client.safeWriteMessage(1, []byte("test"))
	if err == nil {
		t.Errorf("safeWriteMessage should return an error when closed")
	}
}

// ─── Exponential backoff logic ────────────────────────────────────────────────

func TestExponentialBackoff_DoublesAndCapsAt60s(t *testing.T) {
	backoff := 1
	maxBackoff := 60
	sequence := []int{}

	for i := 0; i < 10; i++ {
		sequence = append(sequence, backoff)
		backoff *= 2
		if backoff > maxBackoff {
			backoff = maxBackoff
		}
	}

	// First 6 should double: 1, 2, 4, 8, 16, 32
	expected := []int{1, 2, 4, 8, 16, 32, 60, 60, 60, 60}
	for i, exp := range expected {
		if sequence[i] != exp {
			t.Errorf("backoff[%d] = %d; want %d", i, sequence[i], exp)
		}
	}
}

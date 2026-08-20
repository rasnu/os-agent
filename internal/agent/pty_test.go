// Copyright © 2026 Rasnu LLC (rasnu.com). All rights reserved.
// Licensed under the Functional Source License, Version 1.1, ALv2 Future License (FSL-1.1-ALv2).

package agent

import (
	"bytes"
	"testing"
	"time"

	"rasnuos-agent/internal/osadapter"
)

func TestStartTerminal(t *testing.T) {
	originalAdapter := osadapter.Current()
	defer osadapter.SetAdapter(originalAdapter)

	mockAdapter := &MockOSAdapter{
		StartPTYFunc: func(cols, rows uint16) (osadapter.PTY, error) {
			return &MockPTY{
				ReadFunc: func(p []byte) (n int, err error) {
					time.Sleep(1 * time.Second)
					return 0, nil
				},
			}, nil
		},
	}
	osadapter.SetAdapter(mockAdapter)

	client := &PusherClient{
		ServerID: "test-server",
	}

	termID := "test-term-1"

	session := client.StartTerminal(termID, true, "req-123")
	if session == nil {
		t.Fatal("Failed to start terminal session")
	}

	sessionMutex.Lock()
	_, exists := activeSessions[termID]
	sessionMutex.Unlock()

	if !exists {
		t.Fatal("Expected terminal session to be created")
	}

	time.Sleep(100 * time.Millisecond)

	sessionMutex.Lock()
	session = activeSessions[termID]
	sessionMutex.Unlock()

	if session != nil {
		session.Close()
	}
}

func TestPTYSession_Resize(t *testing.T) {
	resizeCalled := false
	mockPTY := &MockPTY{
		ResizeFunc: func(cols, rows uint16) error {
			resizeCalled = true
			if cols != 100 || rows != 30 {
				t.Errorf("Expected 100x30, got %dx%d", cols, rows)
			}
			return nil
		},
	}

	session := &PTYSession{
		ptyInstance:  mockPTY,
		lastActivity: time.Now(),
	}

	err := session.Resize(30, 100)
	if err != nil {
		t.Fatalf("Resize error: %v", err)
	}
	if !resizeCalled {
		t.Fatal("Resize function was not called")
	}
}

func TestPTYSession_Write(t *testing.T) {
	var writtenData []byte
	mockPTY := &MockPTY{
		WriteFunc: func(p []byte) (n int, err error) {
			writtenData = append(writtenData, p...)
			return len(p), nil
		},
	}

	session := &PTYSession{
		ptyInstance:  mockPTY,
		lastActivity: time.Now(),
	}

	err := session.Write([]byte("ls -la\n"))
	if err != nil {
		t.Fatalf("Write error: %v", err)
	}

	if !bytes.Equal(writtenData, []byte("ls -la\n")) {
		t.Errorf("Expected 'ls -la\\n', got %s", string(writtenData))
	}
}

func TestIdleSessionCleanupLogic(t *testing.T) {
	now := time.Now()
	activeSession := &PTYSession{
		termID:       "active-1",
		lastActivity: now.Add(-5 * time.Minute),
	}
	idleSession := &PTYSession{
		termID:       "idle-1",
		lastActivity: now.Add(-35 * time.Minute),
	}

	idleDurationActive := now.Sub(activeSession.lastActivity)
	if idleDurationActive > 30*time.Minute {
		t.Errorf("active session should not be marked as idle")
	}

	idleDurationIdle := now.Sub(idleSession.lastActivity)
	if idleDurationIdle <= 30*time.Minute {
		t.Errorf("idle session should be marked as idle")
	}

	shutdownChan := make(chan struct{})

	sessionMutex.Lock()
	activeSessions["test-shutdown"] = &PTYSession{
		termID:      "test-shutdown",
		ptyInstance: &MockPTY{},
	}
	sessionMutex.Unlock()

	close(shutdownChan)
	CleanupIdleSessions(shutdownChan)

	sessionMutex.Lock()
	_, exists := activeSessions["test-shutdown"]
	sessionMutex.Unlock()
	if exists {
		t.Errorf("Expected session to be removed during shutdown")
	}
}

func TestStreamTerminalOutput(t *testing.T) {
	client := NewPusherClient("ws://fake", "http://fake", "token", "server1", "")
	client.streamTerminalOutput("term1", "hello world")
}

func TestIsValidTermID(t *testing.T) {
	valid := []string{"term-1", "term_2", "123", "a-B_c-9"}
	for _, id := range valid {
		if !isValidTermID(id) {
			t.Errorf("Expected %q to be valid term ID", id)
		}
	}

	invalid := []string{
		"",
		"term with spaces",
		"term/with/slashes",
		"../escape",
		"term;drop",
		"term$eval",
		"this-term-id-is-way-too-long-and-exceeds-the-maximum-sixty-four-characters-allowed-by-agent-guardrails",
	}
	for _, id := range invalid {
		if isValidTermID(id) {
			t.Errorf("Expected %q to be rejected as invalid term ID", id)
		}
	}
}

func TestStartTerminal_LimitsAndValidation(t *testing.T) {
	originalAdapter := osadapter.Current()
	defer osadapter.SetAdapter(originalAdapter)

	mockAdapter := &MockOSAdapter{
		StartPTYFunc: func(cols, rows uint16) (osadapter.PTY, error) {
			return &MockPTY{
				ReadFunc: func(p []byte) (n int, err error) {
					time.Sleep(1 * time.Second)
					return 0, nil
				},
			}, nil
		},
	}
	osadapter.SetAdapter(mockAdapter)

	client := &PusherClient{ServerID: "test-server"}

	if sess := client.StartTerminal("../bad-id", false, ""); sess != nil {
		t.Errorf("Expected StartTerminal to return nil for traversal termID")
	}

	sessionMutex.Lock()
	for k := range activeSessions {
		delete(activeSessions, k)
	}
	sessionMutex.Unlock()

	sessions := make([]*PTYSession, 0, maxConcurrentSessions)
	for i := 0; i < maxConcurrentSessions; i++ {
		termID := "test-session-" + string(rune('a'+i))
		sess := client.StartTerminal(termID, false, "")
		if sess == nil {
			t.Fatalf("Failed to start session %d", i)
		}
		sessions = append(sessions, sess)
	}

	excess := client.StartTerminal("test-session-overflow", false, "")
	if excess != nil {
		t.Errorf("Expected session creation beyond limit to be rejected")
	}

	for _, s := range sessions {
		s.Close()
	}
}

func TestPTYSession_ActivityMonitorStallAndReconnected(t *testing.T) {
	client := &PusherClient{
		ApiURL:   "http://127.0.0.1:0",
		Token:    "test-token",
		ServerID: "srv-1",
		closed:   true,
	}

	// Test broadcastReconnected (safe no-op when closed)
	client.broadcastReconnected()

	// Test activityMonitor detecting interactive prompt suffix ("[y/N] ")
	sess := &PTYSession{
		ptyInstance:      &MockPTY{},
		client:           client,
		termID:           "stall-term",
		currentRequestID: "req-stall",
		lastActivity:     time.Now().Add(-2 * time.Second),
		outputBuffer:     "Do you want to continue? [y/N] ",
		isAgentOwned:     true,
		tickerStop:       make(chan struct{}),
	}

	done := make(chan struct{})
	go func() {
		sess.activityMonitor()
		close(done)
	}()

	// Wait for one 500ms tick of activityMonitor
	time.Sleep(650 * time.Millisecond)
	sess.Close()
	<-done

	sess.mu.Lock()
	stalled := sess.isStalled
	sess.mu.Unlock()
	if !stalled {
		t.Errorf("Expected activityMonitor to mark session as stalled when buffer ends with '[y/N] '")
	}
}

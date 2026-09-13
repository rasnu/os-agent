package main

import (
	"bytes"
	"testing"
	"time"

	"rasnuos-agent/os_adapter"
)

func TestStartTerminal(t *testing.T) {
	mockAdapter := &MockOSAdapter{
		StartPTYFunc: func(cols, rows uint16) (os_adapter.PTY, error) {
			return &MockPTY{
				ReadFunc: func(p []byte) (n int, err error) {
					time.Sleep(1 * time.Second)
					return 0, nil
				},
			}, nil
		},
	}
	os_adapter.SetAdapter(mockAdapter)

	client := &PusherClient{
		ServerID: "test-server",
	}

	termID := "test-term-1"

	client.StartTerminal(termID)

	sessionMutex.Lock()
	_, exists := activeSessions[termID]
	sessionMutex.Unlock()

	if !exists {
		t.Fatal("Expected terminal session to be created")
	}

	time.Sleep(100 * time.Millisecond)

	sessionMutex.Lock()
	session := activeSessions[termID]
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

	// Test the shutdown branch of CleanupIdleSessions
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

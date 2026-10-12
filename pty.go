/**
 * Project: RasnuOS
 * Copyright © 2026 Rasnu LLC (rasnu.com). All rights reserved.
 */
package main

import (
	"encoding/json"
	"log"
	"strings"
	"sync"
	"time"

	"rasnuos-agent/os_adapter"
)

type PTYSession struct {
	ptyInstance      os_adapter.PTY
	client           *PusherClient
	termID           string
	currentRequestID string
	mu               sync.Mutex
	lastActivity     time.Time
	outputBuffer     string
	isStalled        bool
	isAgentOwned     bool
	isClosed         bool
	tickerStop       chan struct{}
}

var (
	activeSessions = make(map[string]*PTYSession)
	sessionMutex   sync.Mutex
)

// CleanupIdleSessions runs periodically and kills sessions idle for >30 minutes.
func CleanupIdleSessions(shutdownChan chan struct{}) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("PANIC RECOVERED in CleanupIdleSessions: %v", r)
		}
	}()
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-shutdownChan:
			sessionMutex.Lock()
			sessionsToClose := make([]*PTYSession, 0, len(activeSessions))
			for termID, session := range activeSessions {
				log.Printf("Shutting down terminal session %s due to agent exit", termID)
				sessionsToClose = append(sessionsToClose, session)
				delete(activeSessions, termID)
			}
			sessionMutex.Unlock()

			for _, session := range sessionsToClose {
				session.closeInternal()
			}
			return
		case <-ticker.C:
			now := time.Now()
			sessionMutex.Lock()
			sessionsToClose := make([]*PTYSession, 0)
			for termID, session := range activeSessions {
				session.mu.Lock()
				idleDuration := now.Sub(session.lastActivity)
				session.mu.Unlock()

				if idleDuration > 30*time.Minute {
					log.Printf("Closing terminal session %s due to inactivity", termID)
					sessionsToClose = append(sessionsToClose, session)
					delete(activeSessions, termID)
				}
			}
			sessionMutex.Unlock()

			for _, session := range sessionsToClose {
				session.closeInternal()
			}
		}
	}
}

const maxConcurrentSessions = 5

func isValidTermID(termID string) bool {
	if len(termID) == 0 || len(termID) > 64 {
		return false
	}
	for _, r := range termID {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_') {
			return false
		}
	}
	return true
}

func (c *PusherClient) StartTerminal(termID string, isAgentOwned bool, requestID string) *PTYSession {
	if !isValidTermID(termID) {
		log.Printf("Terminal session rejected: invalid term_id %q", termID)
		logAuditEvent(AuditEvent{Action: "pty_session_start", Command: termID, Success: false, Error: "invalid_term_id"})
		return nil
	}

	sessionMutex.Lock()
	defer sessionMutex.Unlock()

	if session, exists := activeSessions[termID]; exists {
		session.mu.Lock()
		closed := session.isClosed
		session.mu.Unlock()

		if closed {
			// Stale entry — the session was closed but never removed from the map
			// (e.g., TerminalStop whisper was lost during a server switch).
			// Clean it up and fall through to create a fresh session below.
			log.Printf("Terminal session %s exists but is closed — replacing with fresh session", termID)
			delete(activeSessions, termID)
			session.closeInternal()
		} else {
			log.Printf("Terminal session %s already exists and is alive", termID)
			if isAgentOwned {
				session.mu.Lock()
				session.currentRequestID = requestID
				session.isStalled = false
				session.mu.Unlock()
			}
			return session
		}
	}

	if len(activeSessions) >= maxConcurrentSessions {
		log.Printf("Terminal session rejected: maximum concurrent sessions (%d) reached", maxConcurrentSessions)
		logAuditEvent(AuditEvent{Action: "pty_session_start", Command: termID, Success: false, Error: "max_concurrent_sessions_exceeded"})
		return nil
	}

	ptyInstance, err := os_adapter.Current().StartPTY(80, 24)
	if err != nil {
		log.Printf("Failed to start pty: %v", err)
		logAuditEvent(AuditEvent{Action: "pty_session_start", Command: termID, Success: false, Error: err.Error()})
		return nil
	}

	session := &PTYSession{
		ptyInstance:      ptyInstance,
		client:           c,
		termID:           termID,
		currentRequestID: requestID,
		lastActivity:     time.Now(),
		isAgentOwned:     isAgentOwned,
		tickerStop:       make(chan struct{}),
	}

	activeSessions[termID] = session
	logAuditEvent(AuditEvent{Action: "pty_session_start", Command: termID, Success: true})

	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		defer func() {
			if r := recover(); r != nil {
				log.Printf("PANIC in pty readLoop: %v", r)
			}
		}()
		session.readLoop()
	}()

	if isAgentOwned {
		c.wg.Add(1)
		go func() {
			defer c.wg.Done()
			session.activityMonitor()
		}()
	}

	log.Printf("Terminal session %s started (AgentOwned: %v)", termID, isAgentOwned)
	return session
}

func (s *PTYSession) activityMonitor() {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-s.tickerStop:
			return
		case <-ticker.C:
			s.mu.Lock()
			if s.isClosed || s.isStalled {
				s.mu.Unlock()
				continue
			}

			idleTime := time.Since(s.lastActivity)
			buffer := s.outputBuffer
			reqID := s.currentRequestID
			s.mu.Unlock()

			isStalled := false

			// 1. Regex/Heuristic fast-track
			if idleTime > 500*time.Millisecond && len(buffer) > 0 {
				if strings.HasSuffix(buffer, "[y/N] ") ||
					strings.HasSuffix(buffer, "password: ") ||
					strings.HasSuffix(buffer, "Password: ") ||
					strings.HasSuffix(buffer, "(yes/no) ") ||
					strings.HasSuffix(buffer, "Select: ") ||
					strings.HasSuffix(buffer, "? ") {
					isStalled = true
				}
			}

			// 2. Fallback timeout
			if idleTime > 30*time.Second {
				isStalled = true
			}

			if isStalled {
				s.mu.Lock()
				s.isStalled = true
				s.outputBuffer = "" // Clear buffer so next stall only shows new data
				s.mu.Unlock()

				log.Printf("PTY %s stalled waiting for input. Firing webhook to reqID %s.", s.termID, reqID)
				s.client.sendMigrationCompletion(reqID, reqID, true, buffer, map[string]interface{}{
					"status":  "stalled",
					"term_id": s.termID,
				})
			}
		}
	}
}

func (s *PTYSession) readLoop() {
	buf := make([]byte, 4096)
	for {
		n, err := s.ptyInstance.Read(buf)
		if err != nil {
			log.Printf("PTY read error for %s: %v", s.termID, err)
			break
		}

		s.mu.Lock()
		s.lastActivity = time.Now()
		s.isStalled = false
		s.outputBuffer += string(buf[:n])
		s.mu.Unlock()

		// Send terminal output to UI client via WebSocket
		s.client.streamTerminalOutput(s.termID, string(buf[:n]))
	}

	s.mu.Lock()
	if s.isAgentOwned && !s.isClosed {
		// Do NOT set isClosed=true here, let Close() handle it so tickerStop is safely closed
		reqID := s.currentRequestID
		finalBuf := s.outputBuffer
		s.mu.Unlock()
		s.client.sendMigrationCompletion(reqID, reqID, true, finalBuf, map[string]interface{}{
			"status":  "finished",
			"term_id": s.termID,
		})
	} else {
		s.mu.Unlock()
	}

	s.Close()
}

func (s *PTYSession) Write(data []byte) error {
	s.mu.Lock()
	s.lastActivity = time.Now()
	s.isStalled = false // typing something breaks the stall
	s.mu.Unlock()

	_, err := s.ptyInstance.Write(data)
	return err
}

func (s *PTYSession) Resize(rows, cols uint16) error {
	s.mu.Lock()
	s.lastActivity = time.Now()
	s.mu.Unlock()

	return s.ptyInstance.Resize(cols, rows)
}

func (s *PTYSession) closeInternal() {
	if s.ptyInstance != nil {
		s.ptyInstance.Kill()
		s.ptyInstance.Close()
	}
}

func (s *PTYSession) Close() {
	sessionMutex.Lock()
	if _, exists := activeSessions[s.termID]; exists {
		log.Printf("Closing terminal session %s", s.termID)
		delete(activeSessions, s.termID)
		logAuditEvent(AuditEvent{Action: "pty_session_close", Command: s.termID, Success: true})
	}
	sessionMutex.Unlock()

	s.mu.Lock()
	if !s.isClosed {
		s.isClosed = true
		if s.tickerStop != nil {
			close(s.tickerStop)
		}
	}
	s.mu.Unlock()

	s.closeInternal()
}

func (c *PusherClient) streamTerminalOutput(termID string, data string) {
	dataObj := map[string]interface{}{
		"term_id": termID,
		"data":    data,
	}
	dataBytes, _ := json.Marshal(dataObj)

	payload := PusherClientEvent{
		Event:   "client-TerminalOutput",
		Channel: "private-server." + c.ServerID + ".terminal",
		Data:    string(dataBytes),
	}

	if err := c.safeWriteJSON(payload); err != nil {
		log.Printf("Error sending terminal output: %v", err)
	}
}

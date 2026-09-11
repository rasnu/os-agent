/**
 * Project: RasnuOne
 * Copyright © 2026 Rasnu LLC (rasnu.com). All rights reserved.
 */
package main

import (
	"encoding/json"
	"log"
	"sync"
	"time"

	"rasnuone-agent/os_adapter"
)

type PTYSession struct {
	ptyInstance  os_adapter.PTY
	client       *PusherClient
	termID       string
	mu           sync.Mutex
	lastActivity time.Time
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
			// On shutdown, close all active sessions
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

func (c *PusherClient) StartTerminal(termID string) {
	sessionMutex.Lock()
	defer sessionMutex.Unlock()

	if _, exists := activeSessions[termID]; exists {
		log.Printf("Terminal session %s already exists", termID)
		return
	}

	ptyInstance, err := os_adapter.Current().StartPTY(80, 24)
	if err != nil {
		log.Printf("Failed to start pty: %v", err)
		return
	}

	session := &PTYSession{
		ptyInstance:  ptyInstance,
		client:       c,
		termID:       termID,
		lastActivity: time.Now(),
	}

	activeSessions[termID] = session

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

	log.Printf("Terminal session %s started", termID)
}

func (s *PTYSession) readLoop() {
	buf := make([]byte, 4096) // Increased from 1024 to 4096 for better throughput
	for {
		n, err := s.ptyInstance.Read(buf)
		if err != nil {
			log.Printf("PTY read error for %s: %v", s.termID, err)
			break
		}

		// Update activity on read as well
		s.mu.Lock()
		s.lastActivity = time.Now()
		s.mu.Unlock()

		// Send terminal output to client via WebSocket
		s.client.streamTerminalOutput(s.termID, string(buf[:n]))
	}

	s.Close()
}

func (s *PTYSession) Write(data []byte) error {
	s.mu.Lock()
	s.lastActivity = time.Now()
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

// closeInternal shuts down the process and closes files. Must NOT be called while holding sessionMutex.
func (s *PTYSession) closeInternal() {
	if s.ptyInstance != nil {
		s.ptyInstance.Kill()
		s.ptyInstance.Close()
	}
}

// Close is the public method to close a session
func (s *PTYSession) Close() {
	sessionMutex.Lock()
	if _, exists := activeSessions[s.termID]; !exists {
		sessionMutex.Unlock()
		return
	}

	log.Printf("Closing terminal session %s", s.termID)
	delete(activeSessions, s.termID)
	sessionMutex.Unlock()

	s.closeInternal()
}

func (c *PusherClient) streamTerminalOutput(termID string, data string) {
	dataObj := map[string]interface{}{
		"term_id": termID,
		"data":    data,
	}
	dataBytes, _ := json.Marshal(dataObj)

	// Send a standard event that the frontend can listen to
	payload := PusherClientEvent{
		Event:   "client-TerminalOutput",
		Channel: "private-server." + c.ServerID + ".terminal",
		Data:    string(dataBytes),
	}

	if err := c.safeWriteJSON(payload); err != nil {
		log.Printf("Error sending terminal output: %v", err)
	}
}

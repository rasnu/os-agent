/**
 * Project: RasnuOS
 * Copyright © 2026 Rasnu LLC (rasnu.com). All rights reserved.
 */
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net/http"
	"rasnuos-agent/os_adapter"
	"regexp"
	"runtime"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// MaxConcurrentCommands limits concurrent command execution. Excess requests are rejected.
const MaxConcurrentCommands = 10

// serviceNamePattern prevents shell injection via systemctl.
var serviceNamePattern = regexp.MustCompile(`^[a-zA-Z0-9._@\-]+$`)

// pidPattern validates process IDs for the kill command.
var pidPattern = regexp.MustCompile(`^[0-9]+$`)

type PusherClientEvent struct {
	Event   string      `json:"event"`
	Channel string      `json:"channel"`
	Data    interface{} `json:"data"`
}

type PusherClient struct {
	ServerURL      string
	ApiURL         string
	Token          string
	ServerID       string
	TLSPin         string
	conn           *websocket.Conn
	done           chan struct{}
	writeMutex     sync.Mutex
	wg             sync.WaitGroup
	activeCommands map[string]context.CancelFunc
	cmdMutex       sync.Mutex
	cmdSemaphore   chan struct{} // Limits concurrent command execution
	closed         bool          // Prevents write-after-close panics
}

type PusherMessage struct {
	Event string      `json:"event"`
	Data  interface{} `json:"data,omitempty"`
}

const (
	pongWait   = 60 * time.Second
	pingPeriod = (pongWait * 9) / 10
)

type PusherDataString struct {
	Event string `json:"event"`
	Data  string `json:"data"` // Pusher sends payload data as a stringified JSON
}

func NewPusherClient(serverURL, apiURL, token, serverId, tlsPin string) *PusherClient {
	return &PusherClient{
		ServerURL:      serverURL,
		ApiURL:         apiURL,
		Token:          token,
		ServerID:       serverId,
		TLSPin:         tlsPin,
		activeCommands: make(map[string]context.CancelFunc),
		cmdSemaphore:   make(chan struct{}, MaxConcurrentCommands),
	}
}

func (c *PusherClient) Connect() error {
	log.Printf("Connecting to %s", c.ServerURL)

	headers := http.Header{}
	headers.Add("User-Agent", fmt.Sprintf("RasnuOSAgent/%s", version))

	dialer := websocket.DefaultDialer
	if c.TLSPin != "" {
		dialer = &websocket.Dialer{
			Proxy:            websocket.DefaultDialer.Proxy,
			HandshakeTimeout: websocket.DefaultDialer.HandshakeTimeout,
			TLSClientConfig:  secureTLSConfig(c.TLSPin),
		}
	}

	conn, _, err := dialer.Dial(c.ServerURL, headers)
	if err != nil {
		return err
	}

	c.writeMutex.Lock()
	c.conn = conn
	c.closed = false
	c.writeMutex.Unlock()

	// Set read deadline. Connection drops if no ping/message is received within pongWait.
	c.conn.SetReadDeadline(time.Now().Add(pongWait))
	c.conn.SetPongHandler(func(string) error {
		c.conn.SetReadDeadline(time.Now().Add(pongWait))
		return nil
	})

	c.done = make(chan struct{})

	go c.readPump()

	return nil
}

func (c *PusherClient) ConnectAndMonitor(shutdownChan chan struct{}) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("PANIC RECOVERED in ConnectAndMonitor: %v", r)
		}
	}()
	backoff := 1 * time.Second
	maxBackoff := 60 * time.Second

	// Seed random generator for jitter
	// rand.Seed is deprecated since Go 1.20 and no longer necessary

	for {
		err := c.Connect()
		if err == nil {
			backoff = 1 * time.Second
			c.monitorConnection(shutdownChan)
		} else {
			log.Printf("Connection error: %v", err)
		}

		// Calculate jitter up to 50% of the current backoff
		jitter := time.Duration(rand.Float64() * float64(backoff) * 0.5)
		jitterBackoff := backoff + jitter

		select {
		case <-shutdownChan:
			log.Println("Shutdown requested, stopping reconnect loop")
			return
		case <-time.After(jitterBackoff):
			log.Printf("Reconnecting after %v...", jitterBackoff)
			backoff *= 2
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
		}
	}
}

var monitorTickerDuration = pingPeriod

func (c *PusherClient) monitorConnection(shutdownChan chan struct{}) {
	ticker := time.NewTicker(monitorTickerDuration)
	defer ticker.Stop()
	failures := 0

	for {
		select {
		case <-c.done:
			log.Println("Connection closed, exiting monitor")
			return
		case <-shutdownChan:
			log.Println("Graceful shutdown initiated, closing connection")
			c.Close()
			return
		case <-ticker.C:
			go c.sendHeartbeat()

			if err := c.safeWriteMessage(websocket.PingMessage, []byte{}); err != nil {
				log.Printf("Ping failed: %v", err)
				failures++
				if failures >= 3 {
					log.Println("3 ping failures, dropping connection")
					c.Close()
					return
				}
			} else {
				failures = 0
			}
		}
	}
}

// safeWriteMessage writes a raw WebSocket message. Safe for concurrent use.
func (c *PusherClient) safeWriteMessage(messageType int, data []byte) error {
	c.writeMutex.Lock()
	defer c.writeMutex.Unlock()
	if c.closed || c.conn == nil {
		return fmt.Errorf("connection closed")
	}
	return c.conn.WriteMessage(messageType, data)
}

// safeWriteJSON writes a JSON WebSocket message. Safe for concurrent use.
func (c *PusherClient) safeWriteJSON(v interface{}) error {
	c.writeMutex.Lock()
	defer c.writeMutex.Unlock()
	if c.closed || c.conn == nil {
		return fmt.Errorf("connection closed")
	}
	return c.conn.WriteJSON(v)
}

// parseEventData unmarshals stringified JSON event payloads, returning nil on failure.
func parseEventData(eventName, rawData string) map[string]interface{} {
	var payload map[string]interface{}
	if err := json.Unmarshal([]byte(rawData), &payload); err != nil {
		log.Printf("Failed to parse %s data: %v (raw: %.200s)", eventName, err, rawData)
		return nil
	}
	return payload
}

// getString extracts a string from a map, defaulting to an empty string.
func getString(m map[string]interface{}, key string) string {
	v, _ := m[key].(string)
	return v
}

// getBool extracts a boolean from a map, defaulting to false.
func getBool(m map[string]interface{}, key string) bool {
	v, _ := m[key].(bool)
	return v
}

// getFloat extracts a float64 from a map, defaulting to zero.
func getFloat(m map[string]interface{}, key string) float64 {
	v, _ := m[key].(float64)
	return v
}

func (c *PusherClient) runCommand(f func()) {
	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		defer func() {
			if r := recover(); r != nil {
				log.Printf("PANIC RECOVERED in command handler: %v", r)
			}
		}()
		f()
	}()
}

func (c *PusherClient) readPump() {
	defer close(c.done)
	defer func() {
		if r := recover(); r != nil {
			log.Printf("PANIC RECOVERED in readPump: %v", r)
		}
	}()
	for {
		_, message, err := c.conn.ReadMessage()
		if err != nil {
			log.Println("read:", err)
			return
		}

		// Reset the read deadline any time we successfully read a message
		c.conn.SetReadDeadline(time.Now().Add(pongWait))

		var pm PusherDataString
		if err := json.Unmarshal(message, &pm); err != nil {
			log.Printf("Failed to unmarshal message: %s", message)
			continue
		}

		switch pm.Event {
		case "pusher:connection_established":
			data := parseEventData("connection_established", pm.Data)
			if data == nil {
				log.Printf("Failed to parse connection_established data, cannot subscribe")
				continue
			}
			socketID := getString(data, "socket_id")
			if socketID == "" {
				log.Printf("connection_established missing socket_id, cannot subscribe")
				continue
			}
			log.Printf("Connection established. Socket ID: %s", socketID)
			c.subscribe(socketID)

		case "pusher:ping":
			c.sendMessage(PusherMessage{Event: "pusher:pong"})

		case "ExecuteSelfUpdate":
			payload := parseEventData("ExecuteSelfUpdate", pm.Data)
			if payload == nil {
				continue
			}

			downloadUrl := getString(payload, "download_url")
			if downloadUrl == "" {
				log.Printf("ExecuteSelfUpdate missing download_url, skipping")
				continue
			}

			pipelineId := getString(payload, "pipeline_id")
			stepId := getString(payload, "step_id")
			signature := getString(payload, "signature")

			log.Printf("Received self-update command, downloading from %s", downloadUrl)
			c.runCommand(func() { c.executeSelfUpdate(downloadUrl, signature, pipelineId, stepId) })

		case "ExecuteCommand":
			payload := parseEventData("ExecuteCommand", pm.Data)
			if payload == nil {
				continue
			}

			pipelineId := getString(payload, "pipeline_id")
			stepId := getString(payload, "step_id")
			command := getString(payload, "command")
			runAsUser := getString(payload, "run_as_user")

			if stepId == "" || command == "" {
				log.Printf("ExecuteCommand missing step_id or command, skipping")
				continue
			}

			log.Printf("Received command for pipeline %s, step %s: %s", pipelineId, stepId, redactSecrets(command))

			// Enforce concurrency limit — reject if at capacity
			select {
			case c.cmdSemaphore <- struct{}{}:
				c.runCommand(func() {
					defer func() { <-c.cmdSemaphore }()
					c.executeAndStream(pipelineId, stepId, command, runAsUser)
				})
			default:
				log.Printf("Command rejected for pipeline %s, step %s: max concurrency (%d) reached", pipelineId, stepId, MaxConcurrentCommands)
				c.sendCompletion(pipelineId, stepId, false, fmt.Sprintf("Agent rejected: maximum concurrent commands (%d) reached. Try again shortly.", MaxConcurrentCommands))
			}

		case "CancelCommand":
			payload := parseEventData("CancelCommand", pm.Data)
			if payload == nil {
				continue
			}

			pipelineId := getString(payload, "pipeline_id")
			stepId := getString(payload, "step_id")
			if stepId == "" {
				continue
			}
			log.Printf("Received CancelCommand for pipeline %s, step %s", pipelineId, stepId)

			c.cmdMutex.Lock()
			if cancel, exists := c.activeCommands[stepId]; exists {
				cancel()
				delete(c.activeCommands, stepId)
				log.Printf("Cancelled command for pipeline %s, step %s", pipelineId, stepId)
			}
			c.cmdMutex.Unlock()

		case "ExecuteDiscovery":
			payload := parseEventData("ExecuteDiscovery", pm.Data)
			if payload == nil {
				continue
			}

			script := getString(payload, "script")
			if script == "" {
				log.Printf("ExecuteDiscovery missing script, skipping")
				continue
			}

			log.Printf("Received discovery request")
			c.runCommand(func() { c.executeDiscovery(script) })

		case "AgentCommandIssued":
			payload := parseEventData("AgentCommandIssued", pm.Data)
			if payload == nil {
				continue
			}

			command := getString(payload, "command")
			args, ok := payload["payload"].(map[string]interface{})
			if !ok {
				args = make(map[string]interface{})
			}

			switch command {
			case "execute-file-command":
				action := getString(args, "action")
				if action != "" {
					c.runCommand(func() { c.handleFileCommand(action, args) })
				}
			case "list-directory", "read-file", "write-file", "delete-path", "create-directory", "rename-path", "chmod-path", "chown-path":
				c.runCommand(func() { c.handleFileCommand(command, args) })
			case "execute-migration-command":
				action := getString(args, "action")
				if action != "" {
					c.runCommand(func() { c.handleMigrationCommand(action, args) })
				}
			case "execute-tunnel-command":
				action := getString(args, "action")
				if action != "" {
					c.runCommand(func() { c.handleTunnelCommand(action, args) })
				}
			case "execute-command", "restart-service", "start-service", "stop-service", "kill-process", "sync-cron-jobs", "upload-file-to-dashboard", "download-file-from-dashboard":
				c.runCommand(func() { c.handleAgentCommand(command, args) })
			default:
				log.Printf("Unknown AgentCommandIssued command: %s", command)
			}

		case "client-TerminalStart":
			payload := parseEventData("client-TerminalStart", pm.Data)
			if payload == nil {
				continue
			}
			termID := getString(payload, "term_id")
			if termID != "" {
				c.runCommand(func() { c.StartTerminal(termID) })
			}

		case "client-TerminalInput":
			payload := parseEventData("client-TerminalInput", pm.Data)
			if payload == nil {
				continue
			}
			termID := getString(payload, "term_id")
			data := getString(payload, "data")

			if termID != "" {
				sessionMutex.Lock()
				if session, exists := activeSessions[termID]; exists {
					session.Write([]byte(data))
				}
				sessionMutex.Unlock()
			}

		case "client-TerminalResize":
			payload := parseEventData("client-TerminalResize", pm.Data)
			if payload == nil {
				continue
			}
			termID := getString(payload, "term_id")
			cols := getFloat(payload, "cols")
			rows := getFloat(payload, "rows")

			if termID != "" {
				sessionMutex.Lock()
				if session, exists := activeSessions[termID]; exists {
					session.Resize(uint16(rows), uint16(cols))
				}
				sessionMutex.Unlock()
			}

		case "client-TerminalStop":
			payload := parseEventData("client-TerminalStop", pm.Data)
			if payload == nil {
				continue
			}
			termID := getString(payload, "term_id")

			if termID != "" {
				sessionMutex.Lock()
				if session, exists := activeSessions[termID]; exists {
					session.Close()
				}
				sessionMutex.Unlock()
			}

		default:
			// Ignore other events like pusher:pong, pusher_internal:subscription_succeeded
		}
	}
}

// handleAgentCommand routes OS-level management commands (services, processes) to the OS adapter.
func (c *PusherClient) handleAgentCommand(command string, args map[string]interface{}) {

	var errMsg string
	pipelineId := getString(args, "pipeline_id")
	stepId := getString(args, "step_id")

	switch command {
	case "execute-self-update":
		downloadUrl := getString(args, "download_url")
		if downloadUrl == "" {
			errMsg = "download_url is required for self update"
			break
		}
		signature := getString(args, "signature")
		c.runCommand(func() { c.executeSelfUpdate(downloadUrl, signature, pipelineId, stepId) })

	case "execute-command":
		cmdStr := getString(args, "command")
		requestId := getString(args, "request_id")
		runAsUser := getString(args, "run_as_user")

		if cmdStr == "" || requestId == "" {
			errMsg = "command and request_id are required"
			break
		}

		log.Printf("Executing raw command: %s (User: %s)", cmdStr, runAsUser)
		c.runCommand(func() { c.executeRawCommand(requestId, cmdStr, runAsUser) })

	case "upload-file-to-dashboard":
		path := getString(args, "path")
		token := getString(args, "token")
		requestId := getString(args, "request_id")
		if path == "" || token == "" || requestId == "" {
			errMsg = "path, token, and request_id are required"
			break
		}
		url := fmt.Sprintf("%s/agent/file/transfer/%s", c.ApiURL, token)
		c.runCommand(func() { c.uploadFileToDashboard(requestId, path, url) })

	case "download-file-from-dashboard":
		path := getString(args, "path")
		token := getString(args, "token")
		requestId := getString(args, "request_id")
		if path == "" || token == "" || requestId == "" {
			errMsg = "path, token, and request_id are required"
			break
		}
		url := fmt.Sprintf("%s/agent/file/transfer/%s", c.ApiURL, token)
		c.runCommand(func() { c.downloadFileFromDashboard(requestId, path, url) })

	case "restart-service", "start-service", "stop-service":
		service := getString(args, "service")
		if service == "" {
			errMsg = "service name is required"
			break
		}
		if !serviceNamePattern.MatchString(service) {
			errMsg = fmt.Sprintf("invalid service name: %q", service)
			break
		}

		action := "restart"
		if command == "start-service" {
			action = "start"
		} else if command == "stop-service" {
			action = "stop"
		}

		log.Printf("%sing service: %s", action, service)
		output, err := os_adapter.Current().ManageService(action, service)
		if err != nil {
			errMsg = fmt.Sprintf("Failed to %s service %s: %v\nOutput: %s", action, service, err, string(output))
			log.Printf("%s", errMsg)
			logAuditEvent(AuditEvent{Action: command, PipelineID: pipelineId, StepID: stepId, Command: service, Success: false, Error: errMsg})
		} else {
			successMsg := fmt.Sprintf("Service %s %sed successfully.\nOutput: %s", service, action, string(output))
			log.Printf("%s", successMsg)
			logAuditEvent(AuditEvent{Action: command, PipelineID: pipelineId, StepID: stepId, Command: service, Success: true})
		}

	case "kill-process":
		pid := getString(args, "pid")
		if pid == "" {
			errMsg = "pid is required"
			break
		}
		if !pidPattern.MatchString(pid) {
			errMsg = fmt.Sprintf("invalid pid: %q", pid)
			break
		}

		log.Printf("Killing process %s", pid)
		output, err := os_adapter.Current().KillProcess(pid)
		if err != nil {
			errMsg = fmt.Sprintf("Failed to kill process %s: %v\nOutput: %s", pid, err, string(output))
			log.Printf("%s", errMsg)
			logAuditEvent(AuditEvent{Action: command, PipelineID: pipelineId, StepID: stepId, Command: pid, Success: false, Error: errMsg})
		} else {
			successMsg := fmt.Sprintf("Process %s killed successfully.\nOutput: %s", pid, string(output))
			log.Printf("%s", successMsg)
			logAuditEvent(AuditEvent{Action: command, PipelineID: pipelineId, StepID: stepId, Command: pid, Success: true})
		}

	case "sync-cron-jobs":
		content := getString(args, "content")
		log.Printf("Syncing cron jobs to OS-specific scheduler")
		if err := os_adapter.Current().WriteCronJobs(content); err != nil {
			errMsg = fmt.Sprintf("error syncing cron jobs: %v", err)
			log.Printf("%s", errMsg)
			logAuditEvent(AuditEvent{Action: command, PipelineID: pipelineId, StepID: stepId, Success: false, Error: errMsg})
		} else {
			log.Printf("Successfully synced cron jobs")
			logAuditEvent(AuditEvent{Action: command, PipelineID: pipelineId, StepID: stepId, Success: true})
		}

	default:
		errMsg = fmt.Sprintf("unknown agent command: %q", command)
		log.Printf("%s", errMsg)
	}
}

func (c *PusherClient) subscribe(socketID string) {
	channels := []string{
		fmt.Sprintf("private-server.%s", c.ServerID),
		fmt.Sprintf("private-server.%s.commands", c.ServerID),
		fmt.Sprintf("private-server.%s.terminal", c.ServerID),
	}

	for _, channelName := range channels {
		log.Printf("Authenticating subscription to %s...", channelName)

		authSignature, err := c.authenticateChannel(socketID, channelName)
		if err != nil {
			log.Printf("Channel authentication failed for %s: %v", channelName, err)
			continue
		}

		subscribeMsg := PusherMessage{
			Event: "pusher:subscribe",
			Data: map[string]interface{}{
				"auth":    authSignature,
				"channel": channelName,
			},
		}

		c.sendMessage(subscribeMsg)
		log.Printf("Subscribed to %s", channelName)
	}
}

func (c *PusherClient) authenticateChannel(socketID, channelName string) (string, error) {
	authURL := fmt.Sprintf("%s/agent/broadcasting/auth", c.ApiURL)

	reqBody, err := json.Marshal(map[string]string{
		"socket_id":    socketID,
		"channel_name": channelName,
	})
	if err != nil {
		return "", fmt.Errorf("failed to marshal auth request: %w", err)
	}

	req, err := http.NewRequest("POST", authURL, bytes.NewBuffer(reqBody))
	if err != nil {
		return "", err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.Token)

	resp, err := defaultHTTPClient.Do(req)
	if err != nil {
		return "", err
	}
	if resp != nil {
		defer resp.Body.Close()
	} else {
		return "", fmt.Errorf("auth failed: nil response")
	}

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("auth failed with status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var authResp map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&authResp); err != nil {
		return "", err
	}

	return authResp["auth"], nil
}

func (c *PusherClient) sendMessage(msg PusherMessage) {
	if err := c.safeWriteJSON(msg); err != nil {
		log.Println("write error:", err)
	}
}

// Close terminates the connection cleanly. Safe for concurrent use.
func (c *PusherClient) Close() {
	c.writeMutex.Lock()
	defer c.writeMutex.Unlock()

	if c.closed || c.conn == nil {
		return
	}

	c.closed = true

	// Best-effort close frame — ignore errors since we're shutting down
	c.conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
	c.conn.Close()
}

func (c *PusherClient) sendHeartbeat() {
	heartbeatURL := fmt.Sprintf("%s/agent/heartbeat", c.ApiURL)

	reqBody, err := json.Marshal(map[string]interface{}{
		"server_id":     c.ServerID,
		"agent_version": version,
		"os_type":       runtime.GOOS,
		"cpu_arch":      runtime.GOARCH,
	})
	if err != nil {
		log.Printf("Error marshaling heartbeat: %v", err)
		return
	}

	req, err := http.NewRequest("POST", heartbeatURL, bytes.NewBuffer(reqBody))
	if err != nil {
		return
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.Token)

	resp, err := heartbeatHTTPClient.Do(req)
	if err == nil {
		resp.Body.Close()
	}
}

func (c *PusherClient) WaitCommands() {
	c.wg.Wait()
}

// Copyright © 2026 Rasnu LLC (rasnu.com). All rights reserved.
// Licensed under the Functional Source License, Version 1.1, ALv2 Future License (FSL-1.1-ALv2).

package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net/http"
	neturl "net/url"
	"os"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"rasnuos-agent/internal/audit"
	"rasnuos-agent/internal/buildinfo"
	"rasnuos-agent/internal/observability"
	"rasnuos-agent/internal/osadapter"
	"rasnuos-agent/internal/ratelimit"
	"rasnuos-agent/internal/transport"

	"github.com/gorilla/websocket"
)

const MaxConcurrentCommands = 10
const DefaultCommandRateLimit = 100

// serviceNamePattern prevents shell injection via systemctl.
var serviceNamePattern = regexp.MustCompile(`^[a-zA-Z0-9._@\-]+$`)

var pidPattern = regexp.MustCompile(`^[0-9]+$`)

type PusherClientEvent struct {
	Event   string      `json:"event"`
	Channel string      `json:"channel"`
	Data    interface{} `json:"data"`
}

type PusherClient struct {
	ServerURL        string
	ApiURL           string
	Token            string
	ServerID         string
	TLSPin           string
	conn             *websocket.Conn
	done             chan struct{}
	writeMutex       sync.Mutex
	wg               sync.WaitGroup
	activeOps        int64
	activeCommands   map[string]context.CancelFunc
	cmdMutex         sync.Mutex
	cmdSemaphore     chan struct{}
	rateLimiter      *ratelimit.Limiter
	closed           bool
	hasConnectedOnce bool
	logTailer        *LogTailer
	MonitorInterval  time.Duration
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
	Event string          `json:"event"`
	Data  json.RawMessage `json:"data"` // Handles both stringified JSON (Pusher) and raw objects (Reverb)
}

func NewPusherClient(serverURL, apiURL, token, serverId, tlsPin string) *PusherClient {
	rateLimit := DefaultCommandRateLimit
	if envLimit := os.Getenv("AGENT_RATE_LIMIT_PER_MINUTE"); envLimit != "" {
		if parsed, err := strconv.Atoi(envLimit); err == nil {
			rateLimit = parsed
		}
	}

	return &PusherClient{
		ServerURL:      serverURL,
		ApiURL:         apiURL,
		Token:          token,
		ServerID:       serverId,
		TLSPin:         tlsPin,
		activeCommands: make(map[string]context.CancelFunc),
		cmdSemaphore:   make(chan struct{}, MaxConcurrentCommands),
		rateLimiter:    ratelimit.New(rateLimit),
		logTailer:      NewLogTailer(apiURL, token, serverId),
	}
}

func (c *PusherClient) SetRateLimiter(rl *ratelimit.Limiter) {
	c.rateLimiter = rl
}

func (c *PusherClient) Connect() error {
	log.Printf("Connecting to %s", c.ServerURL)

	headers := http.Header{}
	headers.Add("User-Agent", fmt.Sprintf("RasnuOSAgent/%s", buildinfo.Version))

	dialer := websocket.DefaultDialer
	if c.TLSPin != "" {
		dialer = &websocket.Dialer{
			Proxy:            websocket.DefaultDialer.Proxy,
			HandshakeTimeout: websocket.DefaultDialer.HandshakeTimeout,
			TLSClientConfig:  transport.PinnedTLSConfig(c.TLSPin),
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

	for {
		err := c.Connect()
		if err == nil {
			backoff = 1 * time.Second
			c.monitorConnection(shutdownChan)
		} else {
			log.Printf("Connection error: %v", err)
		}

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

func (c *PusherClient) monitorConnection(shutdownChan chan struct{}) {
	dur := c.MonitorInterval
	if dur == 0 {
		dur = pingPeriod
	}
	ticker := time.NewTicker(dur)
	defer ticker.Stop()
	failures := 0
	heartbeatFailures := 0

	for {
		select {
		case <-c.done:
			log.Println("Connection closed, exiting monitor")
			observability.SetWSConnected(false)
			return
		case <-shutdownChan:
			log.Println("Graceful shutdown initiated, closing connection")
			c.Close()
			return
		case <-ticker.C:
			hbErr := c.sendHeartbeatWithStatus()
			if hbErr != nil {
				heartbeatFailures++
				if heartbeatFailures >= 3 {
					log.Printf("3 consecutive heartbeat HTTP failures — network likely down, forcing WebSocket reconnect")
					c.Close()
					return
				}
			} else {
				heartbeatFailures = 0
			}

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

// safeWriteMessage serializes writes on conn and sets a 10-second write deadline
// to detect half-open TCP connections.
func (c *PusherClient) safeWriteMessage(messageType int, data []byte) error {
	c.writeMutex.Lock()
	defer c.writeMutex.Unlock()
	if c.closed || c.conn == nil {
		return fmt.Errorf("connection closed")
	}
	c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	err := c.conn.WriteMessage(messageType, data)
	c.conn.SetWriteDeadline(time.Time{})
	return err
}

func (c *PusherClient) safeWriteJSON(v interface{}) error {
	c.writeMutex.Lock()
	defer c.writeMutex.Unlock()
	if c.closed || c.conn == nil {
		return fmt.Errorf("connection closed")
	}
	c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	err := c.conn.WriteJSON(v)
	c.conn.SetWriteDeadline(time.Time{})
	return err
}

// parseEventData unmarshals event payloads encoded either as stringified JSON (Pusher) or raw objects (Reverb).
func parseEventData(eventName string, rawData json.RawMessage) map[string]interface{} {
	if len(rawData) == 0 || string(rawData) == "null" {
		return nil
	}

	var strData string
	if err := json.Unmarshal(rawData, &strData); err == nil {
		var payload map[string]interface{}
		if err := json.Unmarshal([]byte(strData), &payload); err != nil {
			log.Printf("Failed to parse %s inner data: %v (raw: %.200s)", eventName, err, strData)
			return nil
		}
		return payload
	}

	var payload map[string]interface{}
	if err := json.Unmarshal(rawData, &payload); err != nil {
		log.Printf("Failed to parse %s data directly: %v (raw: %.200s)", eventName, err, string(rawData))
		return nil
	}
	return payload
}

func getString(m map[string]interface{}, key string) string {
	v, _ := m[key].(string)
	return v
}

func getBool(m map[string]interface{}, key string) bool {
	v, _ := m[key].(bool)
	return v
}

func getFloat(m map[string]interface{}, key string) float64 {
	v, _ := m[key].(float64)
	return v
}

func (c *PusherClient) runCommand(f func()) {
	c.wg.Add(1)
	atomic.AddInt64(&c.activeOps, 1)
	go func() {
		defer c.wg.Done()
		defer atomic.AddInt64(&c.activeOps, -1)
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

		c.conn.SetReadDeadline(time.Now().Add(pongWait))

		var pm PusherDataString
		if err := json.Unmarshal(message, &pm); err != nil {
			log.Printf("Failed to unmarshal message: %s", string(message))
			continue
		}

		log.Printf("Received Pusher Event: '%s'", pm.Event)

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
			observability.SetWSConnected(true)
			// Only broadcast reconnected on subsequent connects so initial startup does not
			// emit spurious reconnect notices in open terminals.
			if c.hasConnectedOnce {
				go c.broadcastReconnected()
				observability.TrackWSReconnect()
			}
			c.hasConnectedOnce = true
		case "pusher:ping":
			c.sendMessage(PusherMessage{Event: "pusher:pong"})

		case "ExecuteSelfUpdate":
			payload := parseEventData("ExecuteSelfUpdate", pm.Data)
			if payload == nil {
				continue
			}

			pipelineId := getString(payload, "pipeline_id")
			stepId := getString(payload, "step_id")

			if c.rateLimiter != nil && !c.rateLimiter.Allow() {
				log.Printf("Rate limit exceeded for ExecuteSelfUpdate (max %d commands/min)", c.rateLimiter.Capacity())
				audit.Log(audit.Event{Action: "rate_limit_exceeded", Command: "ExecuteSelfUpdate", Success: false, Error: "Rate limit exceeded (100 commands/min)"})
				c.sendCompletion(pipelineId, stepId, false, fmt.Sprintf("Agent rejected self-update: rate limit exceeded (maximum %d commands/minute).", c.rateLimiter.Capacity()))
				continue
			}

			downloadUrl := getString(payload, "download_url")
			if downloadUrl == "" {
				log.Printf("ExecuteSelfUpdate missing download_url, skipping")
				continue
			}

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
			timeoutSeconds := int(getFloat(payload, "timeout_seconds"))

			if stepId == "" || command == "" {
				log.Printf("ExecuteCommand missing step_id or command, skipping")
				continue
			}

			if c.rateLimiter != nil && !c.rateLimiter.Allow() {
				log.Printf("Rate limit exceeded for ExecuteCommand on step %s (max %d commands/min)", stepId, c.rateLimiter.Capacity())
				audit.Log(audit.Event{Action: "rate_limit_exceeded", Command: fmt.Sprintf("ExecuteCommand:%s", stepId), Success: false, Error: "Rate limit exceeded"})
				observability.TrackCommandError()
				c.sendCompletion(pipelineId, stepId, false, fmt.Sprintf("Agent rejected command: rate limit exceeded (maximum %d commands/minute). Try again shortly.", c.rateLimiter.Capacity()))
				continue
			}

			if strings.HasPrefix(strings.TrimSpace(command), "[COMPUTER_INTENT]") {
				log.Printf("Received Computer Intent for pipeline %s, step %s", pipelineId, stepId)
				c.runCommand(func() {
					c.executeComputerIntent(pipelineId, stepId, command)
				})
				continue
			}

			if strings.HasPrefix(strings.TrimSpace(command), "[IDE_INTENT]") {
				log.Printf("Received IDE Intent for pipeline %s, step %s", pipelineId, stepId)
				c.runCommand(func() {
					c.executeIdeIntent(pipelineId, stepId, command)
				})
				continue
			}

			log.Printf("Received command for pipeline %s, step %s: %s", pipelineId, stepId, redactSecrets(command))

			select {
			case c.cmdSemaphore <- struct{}{}:
				observability.TrackCommandStart()
				c.runCommand(func() {
					defer func() { <-c.cmdSemaphore }()
					defer observability.TrackCommandFinish()
					c.executeAndStream(pipelineId, stepId, command, runAsUser, timeoutSeconds)
				})
			default:
				log.Printf("Command rejected for pipeline %s, step %s: max concurrency (%d) reached", pipelineId, stepId, MaxConcurrentCommands)
				observability.TrackCommandError()
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

			if c.rateLimiter != nil && !c.rateLimiter.Allow() {
				log.Printf("Rate limit exceeded for ExecuteDiscovery (max %d commands/min)", c.rateLimiter.Capacity())
				audit.Log(audit.Event{Action: "rate_limit_exceeded", Command: "ExecuteDiscovery", Success: false, Error: "Rate limit exceeded"})
				continue
			}

			log.Printf("Received discovery request")
			c.runCommand(func() { c.executeDiscovery(script) })

		case "AgentCommandIssued", "App\\Events\\AgentCommandIssued":
			payload := parseEventData("AgentCommandIssued", pm.Data)
			if payload == nil {
				continue
			}

			command := getString(payload, "command")
			args, ok := payload["payload"].(map[string]interface{})
			if !ok {
				args = make(map[string]interface{})
			}

			if c.rateLimiter != nil && !c.rateLimiter.Allow() {
				log.Printf("Rate limit exceeded for AgentCommandIssued:%s (max %d commands/min)", command, c.rateLimiter.Capacity())
				audit.Log(audit.Event{Action: "rate_limit_exceeded", Command: fmt.Sprintf("AgentCommandIssued:%s", command), Success: false, Error: "Rate limit exceeded"})
				pipelineId := getString(args, "pipeline_id")
				stepId := getString(args, "step_id")
				if stepId != "" {
					c.sendCompletion(pipelineId, stepId, false, fmt.Sprintf("Agent rejected command %s: rate limit exceeded (%d commands/min).", command, c.rateLimiter.Capacity()))
				}
				continue
			}

			switch command {
			case "execute-file-command":
				action := getString(args, "action")
				if action != "" {
					c.runCommand(func() { c.handleFileCommand(action, args) })
				}
			case "list-directory", "read-file", "write-file", "write-files", "delete-path", "create-directory", "rename-path", "chmod-path", "chown-path":
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
			case "execute-self-update", "update_config", "execute-command", "pty-execute", "pty-input", "restart-service", "start-service", "stop-service", "kill-process", "sync-cron-jobs", "upload-file-to-dashboard", "download-file-from-dashboard":
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
			if termID != "" && isValidTermID(termID) {
				if c.rateLimiter != nil && !c.rateLimiter.Allow() {
					log.Printf("Rate limit exceeded for client-TerminalStart on session %s (max %d commands/min)", termID, c.rateLimiter.Capacity())
					audit.Log(audit.Event{Action: "pty_session_rate_limited", Command: termID, Success: false, Error: "Rate limit exceeded"})
					continue
				}
				c.runCommand(func() { c.StartTerminal(termID, false, "") })
			}

		case "client-TerminalInput":
			payload := parseEventData("client-TerminalInput", pm.Data)
			if payload == nil {
				continue
			}
			termID := getString(payload, "term_id")
			data := getString(payload, "data")

			if termID != "" && isValidTermID(termID) {
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

			if termID != "" && isValidTermID(termID) {
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

			if termID != "" && isValidTermID(termID) {
				// Remove from activeSessions and close the PTY directly without calling
				// session.Close(), which also locks sessionMutex.
				sessionMutex.Lock()
				session, exists := activeSessions[termID]
				if exists {
					delete(activeSessions, termID)
				}
				sessionMutex.Unlock()

				if exists {
					session.mu.Lock()
					if !session.isClosed {
						session.isClosed = true
						if session.tickerStop != nil {
							close(session.tickerStop)
						}
					}
					session.mu.Unlock()
					session.closeInternal()
					audit.Log(audit.Event{Action: "pty_session_close", Command: termID, Success: true})
					log.Printf("Terminal session %s stopped by client", termID)
				}
			}

		default:
		}
	}
}

func (c *PusherClient) handleAgentCommand(command string, args map[string]interface{}) {

	var errMsg string
	pipelineId := getString(args, "pipeline_id")
	stepId := getString(args, "step_id")

	switch command {
	case "update_config":
		log.Printf("Received dynamic autopilot_config update via WebSocket")
		if thresholdsRaw, ok := args["thresholds"]; ok {
			if thresholds, ok := thresholdsRaw.(map[string]interface{}); ok {
				UpdateAlertThresholds(thresholds)
			}
		}

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

		if strings.HasPrefix(cmdStr, "[COMPUTER_INTENT]") {
			c.runCommand(func() { c.executeComputerIntent("sync", requestId, cmdStr) })
		} else if strings.HasPrefix(cmdStr, "[IDE_INTENT]") {
			c.runCommand(func() { c.executeIdeIntent("sync", requestId, cmdStr) })
		} else {
			c.runCommand(func() { c.executeRawCommand(requestId, cmdStr, runAsUser) })
		}

	case "pty-execute":
		cmdStr := getString(args, "command")
		requestId := getString(args, "request_id")

		if cmdStr == "" || requestId == "" {
			errMsg = "command and request_id are required"
			break
		}

		log.Printf("Executing interactive PTY command: %s", cmdStr)
		c.runCommand(func() {
			session := c.StartTerminal(requestId, true, requestId)
			if session != nil {
				session.Write([]byte(cmdStr + "\n"))
			} else {
				c.sendMigrationCompletion(requestId, requestId, false, "Failed to start PTY session", nil)
			}
		})

	case "pty-input":
		input := getString(args, "input")
		termId := getString(args, "term_id")
		requestId := getString(args, "request_id")

		if input == "" || termId == "" || requestId == "" {
			errMsg = "input, term_id, and request_id are required"
			break
		}

		log.Printf("Sending PTY input to %s", termId)
		c.runCommand(func() {
			sessionMutex.Lock()
			session, exists := activeSessions[termId]
			sessionMutex.Unlock()

			if exists {
				session.mu.Lock()
				session.currentRequestID = requestId
				session.isStalled = false
				session.mu.Unlock()
				session.Write([]byte(input))
			} else {
				c.sendMigrationCompletion(requestId, requestId, false, "PTY session expired or not found", nil)
			}
		})

	case "upload-file-to-dashboard":
		path := getString(args, "path")
		token := getString(args, "token")
		requestId := getString(args, "request_id")
		if path == "" || token == "" || requestId == "" {
			errMsg = "path, token, and request_id are required"
			break
		}
		url := fmt.Sprintf("%s/agent/file/transfer/%s", c.ApiURL, neturl.PathEscape(token))
		c.runCommand(func() { c.uploadFileToDashboard(requestId, path, url) })

	case "download-file-from-dashboard":
		path := getString(args, "path")
		token := getString(args, "token")
		requestId := getString(args, "request_id")
		if path == "" || token == "" || requestId == "" {
			errMsg = "path, token, and request_id are required"
			break
		}
		url := fmt.Sprintf("%s/agent/file/transfer/%s", c.ApiURL, neturl.PathEscape(token))
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
		output, err := osadapter.Current().ManageService(action, service)
		if err != nil {
			errMsg = fmt.Sprintf("Failed to %s service %s: %v\nOutput: %s", action, service, err, string(output))
			log.Printf("%s", errMsg)
			audit.Log(audit.Event{Action: command, PipelineID: pipelineId, StepID: stepId, Command: service, Success: false, Error: errMsg})
		} else {
			successMsg := fmt.Sprintf("Service %s %sed successfully.\nOutput: %s", service, action, string(output))
			log.Printf("%s", successMsg)
			audit.Log(audit.Event{Action: command, PipelineID: pipelineId, StepID: stepId, Command: service, Success: true})
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
		output, err := osadapter.Current().KillProcess(pid)
		if err != nil {
			errMsg = fmt.Sprintf("Failed to kill process %s: %v\nOutput: %s", pid, err, string(output))
			log.Printf("%s", errMsg)
			audit.Log(audit.Event{Action: command, PipelineID: pipelineId, StepID: stepId, Command: pid, Success: false, Error: errMsg})
		} else {
			successMsg := fmt.Sprintf("Process %s killed successfully.\nOutput: %s", pid, string(output))
			log.Printf("%s", successMsg)
			audit.Log(audit.Event{Action: command, PipelineID: pipelineId, StepID: stepId, Command: pid, Success: true})
		}

	case "sync-cron-jobs":
		content := getString(args, "content")
		log.Printf("Syncing cron jobs to OS-specific scheduler")
		if err := osadapter.Current().WriteCronJobs(content); err != nil {
			errMsg = fmt.Sprintf("error syncing cron jobs: %v", err)
			log.Printf("%s", errMsg)
			audit.Log(audit.Event{Action: command, PipelineID: pipelineId, StepID: stepId, Success: false, Error: errMsg})
		} else {
			log.Printf("Successfully synced cron jobs")
			audit.Log(audit.Event{Action: command, PipelineID: pipelineId, StepID: stepId, Success: true})
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

	resp, err := transport.Default().Do(req)
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

// broadcastReconnected notifies the frontend that the agent WebSocket has
// reconnected so active browser terminals can re-subscribe.
func (c *PusherClient) broadcastReconnected() {
	time.Sleep(500 * time.Millisecond)
	payload := PusherClientEvent{
		Event:   "client-agent-reconnected",
		Channel: fmt.Sprintf("private-server.%s.terminal", c.ServerID),
		Data:    `{"reconnected":true}`,
	}
	if err := c.safeWriteJSON(payload); err != nil {
		log.Printf("Failed to broadcast agent-reconnected: %v", err)
	} else {
		log.Printf("Broadcast client-agent-reconnected on terminal channel")
	}
}

func (c *PusherClient) sendMessage(msg PusherMessage) {
	if err := c.safeWriteJSON(msg); err != nil {
		log.Println("write error:", err)
	}
}

func (c *PusherClient) Close() {
	c.writeMutex.Lock()
	defer c.writeMutex.Unlock()

	if c.closed || c.conn == nil {
		return
	}

	c.closed = true

	c.conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
	c.conn.Close()
}

// sendHeartbeatWithStatus sends the HTTP heartbeat so monitorConnection can detect
// network outages that WebSocket pings alone may miss.
func (c *PusherClient) sendHeartbeatWithStatus() error {
	heartbeatURL := fmt.Sprintf("%s/agent/heartbeat", c.ApiURL)

	reqBody, err := json.Marshal(map[string]interface{}{
		"server_id":        c.ServerID,
		"agent_version":    buildinfo.Version,
		"os_type":          runtime.GOOS,
		"cpu_arch":         runtime.GOARCH,
		"machine_id":       osadapter.Current().GetMachineID(),
	})
	if err != nil {
		log.Printf("Error marshaling heartbeat: %v", err)
		return err
	}

	req, err := http.NewRequest("POST", heartbeatURL, bytes.NewBuffer(reqBody))
	if err != nil {
		return err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.Token)

	resp, err := transport.Heartbeat.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	var res map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&res); err == nil {
		if logs, ok := res["monitored_logs"].([]interface{}); ok {
			var strLogs []string
			for _, l := range logs {
				if str, isStr := l.(string); isStr {
					strLogs = append(strLogs, str)
				}
			}
			if c.logTailer != nil {
				c.logTailer.UpdateMonitoredLogs(strLogs)
			}
		}
		if config, ok := res["autopilot_config"].(map[string]interface{}); ok {
			if thresholdsRaw, ok := config["thresholds"]; ok {
				if thresholds, ok := thresholdsRaw.(map[string]interface{}); ok {
					UpdateAlertThresholds(thresholds)
				}
			}
		}
	}
	return nil
}

func (c *PusherClient) ActiveCommandCount() int {
	c.cmdMutex.Lock()
	cmds := len(c.activeCommands)
	c.cmdMutex.Unlock()

	ops := int(atomic.LoadInt64(&c.activeOps))
	if ops > cmds {
		return ops
	}
	return cmds
}

// WaitUntilIdle blocks until no active commands, file/migration/discovery operations, tunnels,
// or interactive terminal sessions are open on this agent (or maxWait expires).
func (c *PusherClient) WaitUntilIdle(maxWait time.Duration) {
	deadline := time.Now().Add(maxWait)
	for time.Now().Before(deadline) {
		activeCmds := c.ActiveCommandCount()
		activeTerms := ActiveTerminalSessionCount()
		activeTunnels := ActiveTunnelCount()
		if activeCmds == 0 && activeTerms == 0 && activeTunnels == 0 {
			return
		}
		log.Printf("Self-update waiting for %d active operation(s), %d open terminal(s), %d active tunnel(s) to finish before restarting...", activeCmds, activeTerms, activeTunnels)
		time.Sleep(500 * time.Millisecond)
	}
	log.Printf("Self-update idle wait timed out after %v; proceeding with restart.", maxWait)
}

func (c *PusherClient) CancelAllCommands() {
	c.cmdMutex.Lock()
	defer c.cmdMutex.Unlock()
	for stepId, cancel := range c.activeCommands {
		log.Printf("Cancelling in-flight command for step %s due to shutdown", stepId)
		cancel()
	}
}

// WaitCommands waits up to 10 seconds for in-flight command goroutines to finish before exiting.
func (c *PusherClient) WaitCommands() {
	done := make(chan struct{})
	go func() {
		c.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		log.Println("All commands finished cleanly.")
	case <-time.After(10 * time.Second):
		log.Println("Shutdown deadline exceeded (10s). Forcing exit — some commands may not have finished cleanly.")
	}
}

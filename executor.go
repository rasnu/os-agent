/**
 * Project: RasnuOne
 * Copyright © 2026 Rasnu LLC (rasnu.com). All rights reserved.
 */
package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"regexp"
	"sync"
	"time"

	"rasnuone-agent/os_adapter"
)

// secretPatterns mask likely credential values (mysql -p, --password, redis-cli -a, and
// *PASS*/*PWD*/*AUTH* env assignments) before a command is ever written to a log sink.
// Some over-redaction of unrelated flags is an accepted tradeoff to never leak a real secret.
var secretPatterns = []*regexp.Regexp{
	// mysql-style `-p<value>` / `-p'<value>'` (no space between the flag and the value)
	regexp.MustCompile(`(^|\s)-p('[^']*'|"[^"]*"|[^\s]+)`),
	// `--password=<value>` or `--password <value>`
	regexp.MustCompile(`(?i)(--password[= ])('[^']*'|"[^"]*"|[^\s]+)`),
	// redis-cli `-a <value>`
	regexp.MustCompile(`(^|\s)-a\s+('[^']*'|"[^"]*"|[^\s]+)`),
	// bare KEY=VALUE assignments for common secret-ish env vars (REDISCLI_AUTH, MYSQL_PWD, ...)
	regexp.MustCompile(`(?i)(\b[A-Z0-9_]*(?:PASS|PWD|SECRET|AUTH)[A-Z0-9_]*=)('[^']*'|"[^"]*"|[^\s]+)`),
}

func redactSecrets(command string) string {
	redacted := command
	for _, re := range secretPatterns {
		redacted = re.ReplaceAllString(redacted, "${1}***REDACTED***")
	}
	return redacted
}

type LogBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (l *LogBuffer) WriteString(s string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.buf.WriteString(s)

	const maxSize = 1024 * 1024 // 1MB
	if l.buf.Len() > maxSize {
		l.buf.Next(l.buf.Len() - maxSize)
	}
}

func (l *LogBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.String()
}

// StreamBatcher buffers output chunks and sends them in batches to avoid overwhelming
// the WebSocket connection and Reverb server with high-frequency tiny messages.
type StreamBatcher struct {
	client     *PusherClient
	pipelineId string
	stepId     string
	logBuf     *LogBuffer
	mu         sync.Mutex
	wsBuf      bytes.Buffer
	httpBuf    bytes.Buffer
	wsTimer    *time.Timer
	httpTimer  *time.Timer
}

func NewStreamBatcher(client *PusherClient, pipelineId string, stepId string, logBuf *LogBuffer) *StreamBatcher {
	return &StreamBatcher{
		client:     client,
		pipelineId: pipelineId,
		stepId:     stepId,
		logBuf:     logBuf,
	}
}

func (b *StreamBatcher) Write(chunk string) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.wsBuf.WriteString(chunk)
	b.httpBuf.WriteString(chunk)

	if b.wsTimer == nil {
		b.wsTimer = time.AfterFunc(50*time.Millisecond, b.flushWS)
	}
	if b.httpTimer == nil {
		b.httpTimer = time.AfterFunc(1*time.Second, b.flushHTTP)
	}
}

func (b *StreamBatcher) flushWS() {
	b.mu.Lock()
	chunk := b.wsBuf.String()
	b.wsBuf.Reset()
	b.wsTimer = nil
	b.mu.Unlock()

	if chunk != "" && b.client != nil {
		b.client.streamLogWS(b.pipelineId, b.stepId, chunk, b.logBuf)
	}
}

func (b *StreamBatcher) flushHTTP() {
	b.mu.Lock()
	chunk := b.httpBuf.String()
	b.httpBuf.Reset()
	b.httpTimer = nil
	b.mu.Unlock()

	if chunk != "" && b.client != nil {
		b.client.streamLogHTTP(b.pipelineId, b.stepId, chunk)
	}
}

func (b *StreamBatcher) Close() {
	b.mu.Lock()
	if b.wsTimer != nil {
		b.wsTimer.Stop()
		b.wsTimer = nil
	}
	if b.httpTimer != nil {
		b.httpTimer.Stop()
		b.httpTimer = nil
	}
	wsChunk := b.wsBuf.String()
	b.wsBuf.Reset()
	httpChunk := b.httpBuf.String()
	b.httpBuf.Reset()
	b.mu.Unlock()

	if wsChunk != "" && b.client != nil {
		b.client.streamLogWS(b.pipelineId, b.stepId, wsChunk, b.logBuf)
	}
	if httpChunk != "" && b.client != nil {
		b.client.streamLogHTTP(b.pipelineId, b.stepId, httpChunk)
	}
}

func (c *PusherClient) executeAndStream(pipelineId string, stepId string, command string, runAsUser string) {

	safeCommand := redactSecrets(command)
	log.Printf("Executing: %s", safeCommand)

	if f, err := os.OpenFile(os_adapter.Current().LogFilePath(), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600); err == nil {
		f.WriteString(fmt.Sprintf("[%s] EXEC: %s\n", time.Now().Format(time.RFC3339), safeCommand))
		f.Close()
	} else if f, err := os.OpenFile("rasnuone-agent.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600); err == nil {
		f.WriteString(fmt.Sprintf("[%s] EXEC: %s\n", time.Now().Format(time.RFC3339), safeCommand))
		f.Close()
	}

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Hour)
	defer cancel()

	c.cmdMutex.Lock()
	if c.activeCommands == nil {
		c.activeCommands = make(map[string]context.CancelFunc)
	}
	c.activeCommands[stepId] = cancel
	c.cmdMutex.Unlock()

	defer func() {
		c.cmdMutex.Lock()
		delete(c.activeCommands, stepId)
		c.cmdMutex.Unlock()
	}()

	cmd := os_adapter.Current().GetShellCmd(ctx, command, runAsUser)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		c.streamLogWS(pipelineId, stepId, fmt.Sprintf("Error creating stdout pipe: %v\n", err), nil)
		return
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		c.streamLogWS(pipelineId, stepId, fmt.Sprintf("Error creating stderr pipe: %v\n", err), nil)
		return
	}

	if err := cmd.Start(); err != nil {
		c.streamLogWS(pipelineId, stepId, fmt.Sprintf("Error starting command: %v\n", err), nil)
		return
	}

	logBuf := &LogBuffer{}
	batcher := NewStreamBatcher(c, pipelineId, stepId, logBuf)

	var streamWg sync.WaitGroup
	streamWg.Add(2)

	go c.readStream(stepId, stdout, batcher, &streamWg)
	go c.readStream(stepId, stderr, batcher, &streamWg)

	// CRITICAL: We MUST wait for all streams to finish reading BEFORE calling cmd.Wait().
	// If cmd.Wait() is called first, it will instantly close the pipes when the process exits,
	// causing readStream to read EOF and drop output for fast-executing commands.
	streamWg.Wait()

	cmdErr := cmd.Wait()
	batcher.Close()

	if cmdErr != nil {
		c.streamLogWS(pipelineId, stepId, fmt.Sprintf("\nCommand finished with error: %v\n", cmdErr), logBuf)
		c.sendCompletion(pipelineId, stepId, false, logBuf.String())
	} else {
		c.streamLogWS(pipelineId, stepId, "\nCommand finished successfully.\n", logBuf)
		c.sendCompletion(pipelineId, stepId, true, logBuf.String())
	}
}

func (c *PusherClient) readStream(stepId string, reader io.Reader, batcher *StreamBatcher, wg *sync.WaitGroup) {
	defer wg.Done()
	defer func() {
		if r := recover(); r != nil {
			log.Printf("PANIC in readStream: %v", r)
		}
	}()

	scanner := bufio.NewScanner(reader)

	// Increase scanner buffer to 1MB to handle very long lines (e.g. base64 output)
	const maxCapacity = 1024 * 1024
	buf := make([]byte, 64*1024) // Initial size 64KB
	scanner.Buffer(buf, maxCapacity)

	for scanner.Scan() {
		text := scanner.Text() + "\n"
		batcher.Write(text)
	}

	if err := scanner.Err(); err != nil {
		batcher.Write(fmt.Sprintf("\nError reading stream: %v\n", err))
	}
}

func (c *PusherClient) streamLogWS(pipelineId string, stepId string, chunk string, logBuf *LogBuffer) {
	if logBuf != nil {
		logBuf.WriteString(chunk)
	}
	channelName := fmt.Sprintf("private-server.%s", c.ServerID)

	dataBytes, _ := json.Marshal(map[string]interface{}{
		"pipeline_id": pipelineId,
		"step_id":     stepId,
		"chunk":       chunk,
	})

	payload := PusherClientEvent{
		Event:   "client-log",
		Channel: channelName,
		Data:    string(dataBytes),
	}

	if err := c.safeWriteJSON(payload); err != nil {
		log.Printf("Error sending WS log stream: %v", err)
	}
}

func (c *PusherClient) streamLogHTTP(pipelineId string, stepId string, chunk string) {
	go func(pid string, sid string, chk string) {
		streamURL := fmt.Sprintf("%s/agent/stream", c.ApiURL)
		reqBody, err := json.Marshal(map[string]interface{}{
			"pipeline_id": pid,
			"step_id":     sid,
			"chunk":       chk,
		})
		if err == nil {
			req, err := http.NewRequest("POST", streamURL, bytes.NewBuffer(reqBody))
			if err == nil {
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("Authorization", "Bearer "+c.Token)
				if resp, err := defaultHTTPClient.Do(req); err == nil && resp != nil {
					resp.Body.Close()
				}
			}
		}
	}(pipelineId, stepId, chunk)
}

func (c *PusherClient) sendCompletion(pipelineId string, stepId string, success bool, finalLog string) {
	completionURL := fmt.Sprintf("%s/agent/complete", c.ApiURL)

	reqBody, err := json.Marshal(map[string]interface{}{
		"pipeline_id": pipelineId,
		"step_id":     stepId,
		"success":     success,
		"log":         finalLog,
	})
	if err != nil {
		log.Printf("Error marshaling completion request: %v", err)
		return
	}

	req, err := http.NewRequest("POST", completionURL, bytes.NewBuffer(reqBody))
	if err != nil {
		log.Printf("Error creating completion request: %v", err)
		return
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.Token)

	resp, err := defaultHTTPClient.Do(req)
	if err != nil {
		log.Printf("Error sending completion: %v", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		log.Printf("Completion failed with status %d: %s", resp.StatusCode, string(bodyBytes))
	} else {
		log.Printf("Successfully reported completion for step %s", stepId)
	}
}

func (c *PusherClient) executeDiscovery(script string) {

	log.Printf("Executing Discovery Script")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	cmd := os_adapter.Current().GetShellCmd(ctx, script, "")

	output, err := cmd.CombinedOutput()

	if err != nil {
		log.Printf("Discovery execution finished with error: %v", err)
	} else {
		log.Printf("Discovery execution finished successfully.")
	}

	c.sendDiscoveryCompletion(string(output))
}

func (c *PusherClient) sendDiscoveryCompletion(output string) {
	completionURL := fmt.Sprintf("%s/agent/discovery/complete", c.ApiURL)

	reqBody, err := json.Marshal(map[string]interface{}{
		"server_id": c.ServerID,
		"output":    output,
	})
	if err != nil {
		log.Printf("Error marshaling discovery completion: %v", err)
		return
	}

	req, err := http.NewRequest("POST", completionURL, bytes.NewBuffer(reqBody))
	if err != nil {
		log.Printf("Error creating discovery completion request: %v", err)
		return
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.Token)

	resp, err := defaultHTTPClient.Do(req)
	if err != nil {
		log.Printf("Error sending discovery completion: %v", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		log.Printf("Discovery completion failed with status %d: %s", resp.StatusCode, string(bodyBytes))
	} else {
		log.Printf("Successfully reported discovery completion")
	}
}

var commandTimeout = 1 * time.Hour

func (c *PusherClient) executeRawCommand(requestId, cmdStr, runAsUser string) {
	ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
	defer cancel()

	cmd := os_adapter.Current().GetShellCmd(ctx, cmdStr, runAsUser)
	output, err := cmd.CombinedOutput()

	success := err == nil
	logMsg := string(output)
	if err != nil {
		logMsg = fmt.Sprintf("Command failed: %v\nOutput: %s", err, string(output))
	}

	c.sendCompletion("sync", requestId, success, logMsg)
}

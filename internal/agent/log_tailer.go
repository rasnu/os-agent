// Copyright © 2026 Rasnu LLC (rasnu.com). All rights reserved.
// Licensed under the Functional Source License, Version 1.1, ALv2 Future License (FSL-1.1-ALv2).

package agent

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"rasnuos-agent/internal/transport"

	"github.com/nxadm/tail"
)

const LogContextSize = 50
const MaxPayloadBytes = 10240

var suspiciousKeywords = []string{
	"error", "fatal", "panic", "oom", "segfault", "denied",
	"exception", "critical", "traceback",
}

var piiRegexes = []*regexp.Regexp{
	regexp.MustCompile(`(?i)(password|passwd|pwd)\s*[:=]\s*["']?[^"'\s]+["']?`),
	regexp.MustCompile(`(?i)bearer\s+[a-zA-Z0-9_\-\.]+`),
	regexp.MustCompile(`(?i)sk_live_[a-zA-Z0-9]+`),
}

// sigRegexes strip variable tokens (numbers, UUIDs, IPv4s) before hashing.
var sigRegexes = []*regexp.Regexp{
	regexp.MustCompile(`\b\d+\b`),
	regexp.MustCompile(`[a-fA-F0-9]{8}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{12}`),
	regexp.MustCompile(`\b(?:[0-9]{1,3}\.){3}[0-9]{1,3}\b`),
}

var newLogEntryPattern = regexp.MustCompile(`^(\[|\d{4}-\d{2}-\d{2}|\w{3}\s+\d{1,2}\s+\d{2}:\d{2}:\d{2})`)

type RingBuffer struct {
	lines []string
	size  int
	pos   int
	mutex sync.Mutex
}

func NewRingBuffer(size int) *RingBuffer {
	return &RingBuffer{
		lines: make([]string, 0, size),
		size:  size,
	}
}

func (r *RingBuffer) Add(line string) {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	if len(r.lines) < r.size {
		r.lines = append(r.lines, line)
	} else {
		r.lines[r.pos] = line
		r.pos = (r.pos + 1) % r.size
	}
}

func (r *RingBuffer) GetAll() []string {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	out := make([]string, 0, len(r.lines))
	if len(r.lines) < r.size {
		out = append(out, r.lines...)
	} else {
		out = append(out, r.lines[r.pos:]...)
		out = append(out, r.lines[:r.pos]...)
	}
	return out
}

type LogAnomaly struct {
	ServerID    string `json:"server_id"`
	LogPath     string `json:"log_path"`
	Signature   string `json:"signature"`
	TriggerLine string `json:"trigger_line"`
	Context     string `json:"context"`
	Occurrences int    `json:"occurrences"`
	Timestamp   int64  `json:"timestamp"`
}

type LogTailer struct {
	ApiURL      string
	Token       string
	ServerID    string
	ActiveTails map[string]*tail.Tail
	ActiveMutex sync.Mutex
	stopChan    chan struct{}

	batchMutex sync.Mutex
	batchMap   map[string]*LogAnomaly
}

func NewLogTailer(apiURL, token, serverID string) *LogTailer {
	lt := &LogTailer{
		ApiURL:      apiURL,
		Token:       token,
		ServerID:    serverID,
		ActiveTails: make(map[string]*tail.Tail),
		stopChan:    make(chan struct{}),
		batchMap:    make(map[string]*LogAnomaly),
	}
	go lt.flushLoop()
	return lt
}

func (lt *LogTailer) flushLoop() {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-lt.stopChan:
			return
		case <-ticker.C:
			lt.flushBatch()
		}
	}
}

func (lt *LogTailer) flushBatch() {
	lt.batchMutex.Lock()
	if len(lt.batchMap) == 0 {
		lt.batchMutex.Unlock()
		return
	}

	payload := make([]*LogAnomaly, 0, len(lt.batchMap))
	for _, a := range lt.batchMap {
		payload = append(payload, a)
	}
	lt.batchMutex.Unlock()

	url := fmt.Sprintf("%s/agent/logs", lt.ApiURL)
	reqBody, _ := json.Marshal(map[string]interface{}{
		"anomalies": payload,
	})

	req, err := http.NewRequest("POST", url, bytes.NewBuffer(reqBody))
	if err != nil {
		log.Printf("LogTailer: Error creating request: %v", err)
		return
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+lt.Token)

	resp, err := transport.Heartbeat.Do(req)
	if err != nil {
		log.Printf("LogTailer: Failed to send batch to backend: %v", err)
		return
	}
	bodyBytes, _ := io.ReadAll(resp.Body)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted {
		log.Printf("LogTailer: Backend returned status %d. Body: %s", resp.StatusCode, string(bodyBytes))
		return
	}

	lt.batchMutex.Lock()
	lt.batchMap = make(map[string]*LogAnomaly)
	lt.batchMutex.Unlock()
}

// UpdateMonitoredLogs updates the actively monitored log files.
func (lt *LogTailer) UpdateMonitoredLogs(logs []string) {
	lt.ActiveMutex.Lock()
	defer lt.ActiveMutex.Unlock()

	desired := make(map[string]bool)
	for _, l := range logs {
		desired[l] = true
	}

	for path, t := range lt.ActiveTails {
		if !desired[path] {
			log.Printf("LogTailer: Stopping tail for %s", path)
			t.Stop()
			t.Cleanup()
			delete(lt.ActiveTails, path)
		}
	}

	for path := range desired {
		if _, exists := lt.ActiveTails[path]; !exists {
			log.Printf("LogTailer: Starting tail for %s", path)
			t, err := tail.TailFile(path, tail.Config{
				Follow:    true,
				ReOpen:    true,
				MustExist: false,
				Location:  &tail.SeekInfo{Offset: 0, Whence: 2},
				Poll:      true,
			})
			if err != nil {
				log.Printf("LogTailer: Failed to tail %s: %v", path, err)
				continue
			}
			lt.ActiveTails[path] = t
			go lt.processTail(path, t)
		}
	}
}

func (lt *LogTailer) processTail(path string, t *tail.Tail) {
	buffer := NewRingBuffer(LogContextSize)

	var currentEvent strings.Builder
	var lastLine string

	flushEvent := func() {
		eventText := currentEvent.String()
		if eventText == "" {
			return
		}

		buffer.Add(eventText)

		lowerText := strings.ToLower(eventText)
		suspicious := false
		for _, kw := range suspiciousKeywords {
			if strings.Contains(lowerText, kw) {
				suspicious = true
				break
			}
		}

		if suspicious {
			lt.queueAnomaly(path, eventText, buffer.GetAll())
		}

		currentEvent.Reset()
	}

	for line := range t.Lines {
		if line.Err != nil {
			log.Printf("LogTailer error reading %s: %v", path, line.Err)
			continue
		}

		text := strings.TrimRight(line.Text, "\r")

		if strings.HasPrefix(text, " ") || strings.HasPrefix(text, "\t") || (!newLogEntryPattern.MatchString(text) && currentEvent.Len() > 0) {
			currentEvent.WriteString("\n")
			currentEvent.WriteString(text)
		} else {
			flushEvent()
			currentEvent.WriteString(text)
		}

		lastLine = text
	}

	_ = lastLine
}

func (lt *LogTailer) queueAnomaly(path, triggerLine string, contextLines []string) {
	sigStr := triggerLine
	for _, re := range sigRegexes {
		sigStr = re.ReplaceAllString(sigStr, "")
	}
	if len(sigStr) > 500 {
		sigStr = sigStr[:500]
	}

	hash := sha256.Sum256([]byte(sigStr))
	signature := hex.EncodeToString(hash[:])

	var buf bytes.Buffer
	for _, l := range contextLines {
		buf.WriteString(l)
		buf.WriteString("\n")
	}
	payloadStr := buf.String()

	if len(payloadStr) > MaxPayloadBytes {
		// Truncate on rune boundaries so multi-byte UTF-8 sequences are not split.
		runes := []rune(payloadStr)
		if len(runes) > MaxPayloadBytes {
			payloadStr = string(runes[len(runes)-MaxPayloadBytes:])
		}
	}

	for _, re := range piiRegexes {
		payloadStr = re.ReplaceAllString(payloadStr, "[REDACTED]")
		triggerLine = re.ReplaceAllString(triggerLine, "[REDACTED]")
	}

	lt.batchMutex.Lock()
	defer lt.batchMutex.Unlock()

	if existing, found := lt.batchMap[signature]; found {
		existing.Occurrences++
	} else {
		lt.batchMap[signature] = &LogAnomaly{
			ServerID:    lt.ServerID,
			LogPath:     path,
			Signature:   signature,
			TriggerLine: triggerLine,
			Context:     payloadStr,
			Occurrences: 1,
			Timestamp:   time.Now().Unix(),
		}
	}
}

func (lt *LogTailer) Stop() {
	lt.ActiveMutex.Lock()
	defer lt.ActiveMutex.Unlock()
	for _, t := range lt.ActiveTails {
		t.Stop()
		t.Cleanup()
	}
	close(lt.stopChan)
}

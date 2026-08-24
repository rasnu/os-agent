// Copyright © 2026 Rasnu LLC (rasnu.com). All rights reserved.
// Licensed under the Functional Source License, Version 1.1, ALv2 Future License (FSL-1.1-ALv2).

package agent

import (
	"bytes"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"rasnuos-agent/internal/transport"
)

func TestRingBufferWrapAround(t *testing.T) {
	rb := NewRingBuffer(3)
	rb.Add("line-1")
	rb.Add("line-2")
	got1 := rb.GetAll()
	if len(got1) != 2 || got1[0] != "line-1" || got1[1] != "line-2" {
		t.Fatalf("unexpected initial RingBuffer items: %v", got1)
	}

	rb.Add("line-3")
	rb.Add("line-4")
	rb.Add("line-5")
	got2 := rb.GetAll()
	if len(got2) != 3 || got2[0] != "line-3" || got2[1] != "line-4" || got2[2] != "line-5" {
		t.Fatalf("expected wrapped RingBuffer [line-3 line-4 line-5], got %v", got2)
	}
}

func TestLogTailerQueueAnomalyAndFlushBatch(t *testing.T) {
	origHB := transport.Heartbeat
	mockHB := &MockHTTPClient{
		Responses: []*http.Response{
			{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(bytes.NewBufferString(`{"ok":true}`)),
			},
		},
	}
	transport.Heartbeat = mockHB
	defer func() { transport.Heartbeat = origHB }()

	lt := NewLogTailer("http://mock-backend", "tok-123", "srv-99")
	defer lt.Stop()

	// Empty flush should no-op cleanly
	lt.flushBatch()

	// Queue two anomalies with different numbers/IPs that normalize to the same signature, plus PII
	longContext := strings.Repeat("x", MaxPayloadBytes+200)
	lt.queueAnomaly(
		"/var/log/app.log",
		"2026-10-14 ERROR connection failed to 10.0.0.12 port 5432 password=mySecretPassword123",
		[]string{longContext, "Bearer eyJhbGciOiJIUzI1NiJ9.secret", "sk_live_987654321"},
	)
	lt.queueAnomaly(
		"/var/log/app.log",
		"2026-10-14 ERROR connection failed to 10.0.0.99 port 5432 password=mySecretPassword123",
		[]string{"second context"},
	)

	lt.batchMutex.Lock()
	if len(lt.batchMap) != 1 {
		t.Fatalf("expected 1 deduplicated anomaly signature, got %d", len(lt.batchMap))
	}
	var anomaly *LogAnomaly
	for _, a := range lt.batchMap {
		anomaly = a
	}
	lt.batchMutex.Unlock()

	if anomaly.Occurrences != 2 {
		t.Errorf("expected Occurrences=2, got %d", anomaly.Occurrences)
	}
	if strings.Contains(anomaly.TriggerLine, "mySecretPassword123") {
		t.Errorf("expected password to be redacted in TriggerLine, got %q", anomaly.TriggerLine)
	}
	if strings.Contains(anomaly.Context, "sk_live_987654321") || strings.Contains(anomaly.Context, "eyJhbGciOiJIUzI1NiJ9") {
		t.Errorf("expected Bearer and sk_live tokens to be redacted in Context")
	}

	lt.flushBatch()

	lt.batchMutex.Lock()
	remaining := len(lt.batchMap)
	lt.batchMutex.Unlock()
	if remaining != 0 {
		t.Errorf("expected batchMap to be cleared after 200 OK flushBatch, got %d entries", remaining)
	}
}

func TestLogTailerUpdateMonitoredLogs(t *testing.T) {
	tmpDir := t.TempDir()
	logFile := filepath.Join(tmpDir, "service.log")
	if err := os.WriteFile(logFile, []byte("2026-10-14 INFO starting\n"), 0644); err != nil {
		t.Fatal(err)
	}

	lt := NewLogTailer("http://mock-backend", "tok-123", "srv-99")
	lt.UpdateMonitoredLogs([]string{logFile})

	// Append an error line with stacktrace continuation and a subsequent entry to trigger flushEvent
	f, err := os.OpenFile(logFile, os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString("2026-10-14 04:00:00 FATAL database panic\n\tat main.go:42\n2026-10-14 04:00:01 INFO recovered\n")
	_ = f.Close()

	time.Sleep(350 * time.Millisecond)

	// Remove log from monitored list
	lt.UpdateMonitoredLogs([]string{})
	lt.Stop()
}

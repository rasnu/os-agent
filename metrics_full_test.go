package main

import (
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"
)

func TestCollectAndSendMetrics(t *testing.T) {
	mockHTTP := &MockHTTPClient{}
	defaultHTTPClient = mockHTTP
	heartbeatHTTPClient = mockHTTP

	client := NewPusherClient("ws://", "http://localhost/api", "token", "srv", "")

	// Directly call collectAndSendMetrics to test the full pipeline
	client.collectAndSendMetrics()

	// Wait for the async flush goroutine (up to 1 second)
	for i := 0; i < 10; i++ {
		if mockHTTP.CallCount > 0 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	if mockHTTP.CallCount == 0 {
		t.Fatalf("expected HTTP call for metrics")
	}

	req := mockHTTP.Requests[0]
	if !strings.Contains(req.URL.String(), "/agent/metrics") {
		t.Errorf("wrong URL: %v", req.URL)
	}

	body, _ := io.ReadAll(req.Body)
	var payload MetricsPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("failed to decode metrics payload: %v", err)
	}

	if payload.ServerID != "srv" {
		t.Errorf("wrong server id: %v", payload.ServerID)
	}
	if payload.CPUUsage != 12.5 {
		t.Errorf("wrong cpu: %v", payload.CPUUsage)
	}
}

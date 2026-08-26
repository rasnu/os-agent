// Copyright © 2026 Rasnu LLC (rasnu.com). All rights reserved.
// Licensed under the Functional Source License, Version 1.1, ALv2 Future License (FSL-1.1-ALv2).

package agent

import (
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"

	"rasnuos-agent/internal/transport"
)

func TestCollectAndSendMetrics(t *testing.T) {
	origDefault := transport.Default()
	origHeartbeat := transport.Heartbeat
	defer func() {
		transport.SetDefault(origDefault)
		transport.Heartbeat = origHeartbeat
	}()

	mockHTTP := &MockHTTPClient{}
	transport.SetDefault(mockHTTP)
	transport.Heartbeat = mockHTTP

	client := NewPusherClient("ws://", "http://localhost/api", "token", "srv", "")

	client.collectAndSendMetrics()

	for i := 0; i < 10; i++ {
		if mockHTTP.GetCallCount() > 0 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	if mockHTTP.GetCallCount() == 0 {
		t.Fatalf("expected HTTP call for metrics")
	}

	reqs := mockHTTP.GetRequests()
	req := reqs[0]
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

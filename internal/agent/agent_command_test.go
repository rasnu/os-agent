// Copyright © 2026 Rasnu LLC (rasnu.com). All rights reserved.
// Licensed under the Functional Source License, Version 1.1, ALv2 Future License (FSL-1.1-ALv2).

package agent

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"rasnuos-agent/internal/osadapter"
)

func TestHandleAgentCommand(t *testing.T) {
	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		w.Write([]byte(`{"success": true}`))
	}))
	defer testServer.Close()

	client := &PusherClient{
		cmdSemaphore: make(chan struct{}, 10),
		ApiURL:       testServer.URL,
		Token:        "test-token",
	}

	originalAdapter := osadapter.Current()
	mockAdapter := &MockOSAdapter{
		ManageServiceFunc: func(action, service string) (string, error) {
			return "managed " + service, nil
		},
		KillProcessFunc: func(pid string) (string, error) {
			return "killed " + pid, nil
		},
		WriteCronJobsFunc: func(content string) error {
			return nil
		},
	}
	osadapter.SetAdapter(mockAdapter)
	defer osadapter.SetAdapter(originalAdapter)

	client.handleAgentCommand("unknown-cmd", nil)

	client.handleAgentCommand("execute-self-update", map[string]interface{}{})
	client.handleAgentCommand("execute-self-update", map[string]interface{}{
		"download_url": "http://test",
		"pipeline_id":  "1",
		"step_id":      "1",
	})

	client.handleAgentCommand("execute-command", map[string]interface{}{})
	client.handleAgentCommand("execute-command", map[string]interface{}{
		"command":    "ls",
		"request_id": "req1",
	})

	client.handleAgentCommand("upload-file-to-dashboard", map[string]interface{}{})
	client.handleAgentCommand("upload-file-to-dashboard", map[string]interface{}{
		"path":       "/tmp/a",
		"token":      "tok",
		"request_id": "req2",
	})

	client.handleAgentCommand("download-file-from-dashboard", map[string]interface{}{})
	client.handleAgentCommand("download-file-from-dashboard", map[string]interface{}{
		"path":       "/tmp/b",
		"token":      "tok",
		"request_id": "req3",
	})

	client.handleAgentCommand("restart-service", map[string]interface{}{})
	client.handleAgentCommand("start-service", map[string]interface{}{
		"service": "invalid&name",
	})
	client.handleAgentCommand("start-service", map[string]interface{}{
		"service": "nginx",
	})
	client.handleAgentCommand("stop-service", map[string]interface{}{
		"service": "nginx",
	})

	client.handleAgentCommand("kill-process", map[string]interface{}{})
	client.handleAgentCommand("kill-process", map[string]interface{}{
		"pid": "not-a-number",
	})
	client.handleAgentCommand("kill-process", map[string]interface{}{
		"pid": "1234",
	})

	client.handleAgentCommand("sync-cron-jobs", map[string]interface{}{
		"content": "* * * * * test",
	})

	client.wg.Wait()
}

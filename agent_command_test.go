package main

import (
	"rasnuos-agent/os_adapter"
	"testing"
)

func TestHandleAgentCommand(t *testing.T) {
	client := &PusherClient{
		cmdSemaphore: make(chan struct{}, 10),
		ApiURL:       "http://localhost",
		Token:        "test-token",
	}

	// Mock the adapter
	originalAdapter := os_adapter.Current()
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
	os_adapter.SetAdapter(mockAdapter)
	defer os_adapter.SetAdapter(originalAdapter)

	// 1. Unknown command
	client.handleAgentCommand("unknown-cmd", nil)

	// 2. Self update missing url
	client.handleAgentCommand("execute-self-update", map[string]interface{}{})
	// 3. Self update correct
	client.handleAgentCommand("execute-self-update", map[string]interface{}{
		"download_url": "http://test",
		"pipeline_id":  "1",
		"step_id":      "1",
	})

	// 4. Execute command missing args
	client.handleAgentCommand("execute-command", map[string]interface{}{})
	// 5. Execute command correct
	client.handleAgentCommand("execute-command", map[string]interface{}{
		"command":    "ls",
		"request_id": "req1",
	})

	// 6. Upload file missing args
	client.handleAgentCommand("upload-file-to-dashboard", map[string]interface{}{})
	// 7. Upload file correct
	client.handleAgentCommand("upload-file-to-dashboard", map[string]interface{}{
		"path":       "/tmp/a",
		"token":      "tok",
		"request_id": "req2",
	})

	// 8. Download file missing args
	client.handleAgentCommand("download-file-from-dashboard", map[string]interface{}{})
	// 9. Download file correct
	client.handleAgentCommand("download-file-from-dashboard", map[string]interface{}{
		"path":       "/tmp/b",
		"token":      "tok",
		"request_id": "req3",
	})

	// 10. Service missing args
	client.handleAgentCommand("restart-service", map[string]interface{}{})
	// 11. Service invalid name
	client.handleAgentCommand("start-service", map[string]interface{}{
		"service": "invalid&name",
	})
	// 12. Service start
	client.handleAgentCommand("start-service", map[string]interface{}{
		"service": "nginx",
	})
	// 13. Service stop
	client.handleAgentCommand("stop-service", map[string]interface{}{
		"service": "nginx",
	})

	// 14. Kill missing pid
	client.handleAgentCommand("kill-process", map[string]interface{}{})
	// 15. Kill invalid pid
	client.handleAgentCommand("kill-process", map[string]interface{}{
		"pid": "not-a-number",
	})
	// 16. Kill correct
	client.handleAgentCommand("kill-process", map[string]interface{}{
		"pid": "1234",
	})

	// 17. Sync cron
	client.handleAgentCommand("sync-cron-jobs", map[string]interface{}{
		"content": "* * * * * test",
	})

	// Wait for any async commands to trigger
	client.wg.Wait()
}

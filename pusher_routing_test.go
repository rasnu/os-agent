package main

import (
	"rasnuos-agent/os_adapter"
	"testing"
)

func TestHandleAgentCommand_UnknownCommand(t *testing.T) {
	client := &PusherClient{
		ServerID:     "test-server",
		cmdSemaphore: make(chan struct{}, 10),
	}

	payload := map[string]interface{}{
		"step_id": "123",
	}

	client.handleAgentCommand("UnknownCommandThatDoesNotExist", payload)
}

func TestHandleAgentCommand_SelfUpdate(t *testing.T) {
	client := &PusherClient{
		ServerID:     "test-server",
		cmdSemaphore: make(chan struct{}, 10),
	}

	originalAdapter := os_adapter.Current()
	mockAdapter := &MockOSAdapter{
		VerifyBinaryFunc:  func(p string) error { return nil },
		ReplaceBinaryFunc: func(t, c string) error { return nil },
	}
	os_adapter.SetAdapter(mockAdapter)
	defer os_adapter.SetAdapter(originalAdapter)

	payload := map[string]interface{}{
		"step_id":      "123",
		"pipeline_id":  "456",
		"download_url": "http://fake-url.com/update",
	}

	client.handleAgentCommand("execute-self-update", payload)
}

func TestHandleAgentCommand_RestartService(t *testing.T) {
	client := &PusherClient{
		ServerID:     "test-server",
		cmdSemaphore: make(chan struct{}, 10),
	}

	manageCalled := false
	originalAdapter := os_adapter.Current()
	mockAdapter := &MockOSAdapter{
		ManageServiceFunc: func(action, service string) (string, error) {
			manageCalled = true
			if action != "restart" || service != "nginx" {
				t.Errorf("Expected restart nginx, got %s %s", action, service)
			}
			return "ok", nil
		},
	}
	os_adapter.SetAdapter(mockAdapter)
	defer os_adapter.SetAdapter(originalAdapter)

	payload := map[string]interface{}{
		"step_id":     "123",
		"pipeline_id": "456",
		"service":     "nginx",
	}

	client.handleAgentCommand("restart-service", payload)

	if !manageCalled {
		t.Error("Expected ManageService to be called")
	}
}

func TestHandleAgentCommand_KillProcess(t *testing.T) {
	client := &PusherClient{
		ServerID:     "test-server",
		cmdSemaphore: make(chan struct{}, 10),
	}

	originalAdapter := os_adapter.Current()
	mockAdapter := &MockOSAdapter{
		KillProcessFunc: func(pid string) (string, error) {
			if pid != "9999" {
				t.Errorf("Expected pid 9999, got %s", pid)
			}
			return "ok", nil
		},
	}
	os_adapter.SetAdapter(mockAdapter)
	defer os_adapter.SetAdapter(originalAdapter)

	payload := map[string]interface{}{
		"step_id":     "123",
		"pipeline_id": "456",
		"pid":         "9999",
	}

	client.handleAgentCommand("kill-process", payload)
}

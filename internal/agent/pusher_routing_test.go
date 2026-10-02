// Copyright © 2026 Rasnu LLC (rasnu.com). All rights reserved.
// Licensed under the Functional Source License, Version 1.1, ALv2 Future License (FSL-1.1-ALv2).

package agent

import (
	"testing"

	"rasnuos-agent/internal/osadapter"
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

	originalAdapter := osadapter.Current()
	mockAdapter := &MockOSAdapter{
		VerifyBinaryFunc:  func(p string) error { return nil },
		ReplaceBinaryFunc: func(t, c string) error { return nil },
	}
	osadapter.SetAdapter(mockAdapter)
	defer osadapter.SetAdapter(originalAdapter)

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
	originalAdapter := osadapter.Current()
	mockAdapter := &MockOSAdapter{
		ManageServiceFunc: func(action, service string) (string, error) {
			manageCalled = true
			if action != "restart" || service != "nginx" {
				t.Errorf("Expected restart nginx, got %s %s", action, service)
			}
			return "ok", nil
		},
	}
	osadapter.SetAdapter(mockAdapter)
	defer osadapter.SetAdapter(originalAdapter)

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

	originalAdapter := osadapter.Current()
	mockAdapter := &MockOSAdapter{
		KillProcessFunc: func(pid string) (string, error) {
			if pid != "9999" {
				t.Errorf("Expected pid 9999, got %s", pid)
			}
			return "ok", nil
		},
	}
	osadapter.SetAdapter(mockAdapter)
	defer osadapter.SetAdapter(originalAdapter)

	payload := map[string]interface{}{
		"step_id":     "123",
		"pipeline_id": "456",
		"pid":         "9999",
	}

	client.handleAgentCommand("kill-process", payload)
}

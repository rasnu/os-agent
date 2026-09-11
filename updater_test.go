package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"rasnuone-agent/os_adapter"
)

func TestExecuteSelfUpdate(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("mock binary data"))
	}))
	defer ts.Close()

	verifyCalled := false
	replaceCalled := false

	originalAdapter := os_adapter.Current()
	mockAdapter := &MockOSAdapter{
		VerifyBinaryFunc: func(path string) error {
			verifyCalled = true
			return nil
		},
		ReplaceBinaryFunc: func(tempPath, currentPath string) error {
			replaceCalled = true
			return nil
		},
		RestartAgentFunc: func() error {
			return nil
		},
	}
	os_adapter.SetAdapter(mockAdapter)
	defer os_adapter.SetAdapter(originalAdapter)

	client := &PusherClient{
		ServerID: "test-server",
	}

	client.executeSelfUpdate(ts.URL, "", "pipe-1", "step-1")

	time.Sleep(100 * time.Millisecond)

	if !verifyCalled {
		t.Error("Expected VerifyBinary to be called")
	}
	if !replaceCalled {
		t.Error("Expected ReplaceBinary to be called")
	}
}

func TestExecuteSelfUpdate_DownloadFails(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer ts.Close()

	replaceCalled := false
	originalAdapter := os_adapter.Current()
	mockAdapter := &MockOSAdapter{
		ReplaceBinaryFunc: func(tempPath, currentPath string) error {
			replaceCalled = true
			return nil
		},
	}
	os_adapter.SetAdapter(mockAdapter)
	defer os_adapter.SetAdapter(originalAdapter)

	client := &PusherClient{
		ServerID: "test-server",
	}

	client.executeSelfUpdate(ts.URL, "", "pipe-2", "step-2")

	if replaceCalled {
		t.Error("Expected ReplaceBinary to NOT be called if download failed")
	}
}

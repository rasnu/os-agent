// Copyright © 2026 Rasnu LLC (rasnu.com). All rights reserved.
// Licensed under the Functional Source License, Version 1.1, ALv2 Future License (FSL-1.1-ALv2).

package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os/exec"
	"testing"
	"time"

	"rasnuos-agent/internal/transport"

	"rasnuos-agent/internal/osadapter"
)

type DummyAdapter struct{}

func (m *DummyAdapter) GetMetrics() (osadapter.Metrics, []error)                      { return osadapter.Metrics{}, nil }
func (m *DummyAdapter) GetShellCmd(ctx context.Context, s string, u string) *exec.Cmd { return nil }
func (m *DummyAdapter) StartPTY(cols, rows uint16) (osadapter.PTY, error)             { return nil, nil }
func (m *DummyAdapter) InstallService(executablePath string) error                    { return nil }
func (m *DummyAdapter) UninstallService() error                                       { return nil }
func (m *DummyAdapter) VerifyBinary(path string) error                                { return nil }
func (m *DummyAdapter) ReplaceBinary(tempPath, currentPath string) error              { return nil }
func (m *DummyAdapter) RestartAgent() error                                           { return nil }
func (m *DummyAdapter) ManageService(action, service string) (string, error)          { return "", nil }
func (m *DummyAdapter) KillProcess(pid string) (string, error)                        { return "", nil }
func (m *DummyAdapter) WriteCronJobs(content string) error                            { return nil }
func (m *DummyAdapter) LogFilePath() string                                           { return "" }
func (m *DummyAdapter) PunchFirewallHole(port int) error                              { return nil }
func (m *DummyAdapter) CloseFirewallHole(port int) error                              { return nil }
func (m *DummyAdapter) ExecuteComputerAction(action, text string, x, y int) ([]byte, error) {
	return nil, nil
}
func (m *DummyAdapter) GetMachineID() string { return "dummy-machine-id" }

func TestTunnelEngine_EndToEnd(t *testing.T) {
	originalAdapter := osadapter.Current()
	osadapter.SetAdapter(&DummyAdapter{})
	defer osadapter.SetAdapter(originalAdapter)

	var capturedBodies [][]byte
	mockHTTP := &MockHTTPClient{
		OnRequest: func(req *http.Request) {
			if req.Body != nil {
				bodyBytes, _ := io.ReadAll(req.Body)
				capturedBodies = append(capturedBodies, bodyBytes)
			}
		},
	}

	originalClient := transport.Default()
	transport.SetDefault(mockHTTP)
	defer func() { transport.SetDefault(originalClient) }()

	client := &PusherClient{
		ApiURL: "http://localhost",
		Token:  "test-token",
	}

	dummyListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Failed to start dummy local service: %v", err)
	}
	localPort := dummyListener.Addr().(*net.TCPAddr).Port
	defer dummyListener.Close()

	go func() {
		for {
			conn, err := dummyListener.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				buf := make([]byte, 1024)
				var n int
				var err error
				for {
					n, err = c.Read(buf)
					if err != nil {
						return
					}
					if n > 0 {
						break
					}
				}
				response := append([]byte("ECHO: "), buf[:n]...)
				c.Write(response)
			}(conn)
		}
	}()

	token := "secure-test-token-64-bytes-long-xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx"
	publicPort := 0

	client.prepareTunnelHost("pipe-1", "step-1", token, publicPort, "", false)

	if len(capturedBodies) == 0 {
		t.Fatalf("Host did not send completion payload")
	}

	var payload map[string]interface{}
	err = json.Unmarshal(capturedBodies[0], &payload)
	if err != nil {
		t.Fatalf("Failed to unmarshal payload: %v", err)
	}

	if !payload["success"].(bool) {
		t.Fatalf("prepareTunnelHost failed: %v", payload["log"])
	}

	data := payload["data"].(map[string]interface{})
	controlPort := int(data["control_port"].(float64))
	actualPublicPort := int(data["public_port"].(float64))
	fingerprint := data["fingerprint"].(string)

	capturedBodies = nil

	client.executeTunnelClient("pipe-1", "step-2", "127.0.0.1", controlPort, fingerprint, localPort, token)

	time.Sleep(100 * time.Millisecond)

	pubConn, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", actualPublicPort))
	if err != nil {
		t.Fatalf("Failed to connect to public port: %v", err)
	}
	defer pubConn.Close()

	msg := []byte("hello world")
	_, err = pubConn.Write(msg)
	if err != nil {
		t.Fatalf("Failed to write to public port: %v", err)
	}

	expected := "ECHO: hello world"
	buf := make([]byte, len(expected))
	pubConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, err = io.ReadFull(pubConn, buf)
	if err != nil {
		t.Fatalf("Failed to read expected bytes from public port: %v", err)
	}

	if string(buf) != expected {
		t.Fatalf("Expected %q, got %q", expected, string(buf))
	}

	badConn, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", controlPort))
	if err != nil {
		t.Fatalf("Failed to connect to control port: %v", err)
	}
	defer badConn.Close()

	shutdownTunnelClient(token)
	shutdownTunnelHost(token)
}

func TestHandleTunnelCommand(t *testing.T) {
	originalAdapter := osadapter.Current()
	osadapter.SetAdapter(&DummyAdapter{})
	defer osadapter.SetAdapter(originalAdapter)

	var capturedBodies [][]byte
	mockHTTP := &MockHTTPClient{
		OnRequest: func(req *http.Request) {
			if req.Body != nil {
				bodyBytes, _ := io.ReadAll(req.Body)
				capturedBodies = append(capturedBodies, bodyBytes)
			}
		},
	}

	originalClient := transport.Default()
	transport.SetDefault(mockHTTP)
	defer func() { transport.SetDefault(originalClient) }()

	client := &PusherClient{
		ApiURL: "http://localhost",
		Token:  "test-token",
	}

	hostToken := "host-test-token-64-bytes-long-xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx"

	// 1. Test prepare-tunnel-host via handleTunnelCommand and then shutdown-tunnel-host directly
	// so activeTunnels holds *TunnelServer and exercises 100% of shutdownTunnelHost
	client.handleTunnelCommand("prepare-tunnel-host", map[string]interface{}{
		"pipeline_id":    "pipe-tun",
		"step_id":        "step-prep",
		"token":          hostToken,
		"public_port":    float64(0),
		"domain":         "",
		"use_native_ssl": false,
	})

	if len(capturedBodies) == 0 {
		t.Fatalf("Expected completion payload from prepare-tunnel-host")
	}

	var prepPayload map[string]interface{}
	_ = json.Unmarshal(capturedBodies[len(capturedBodies)-1], &prepPayload)
	if !prepPayload["success"].(bool) {
		t.Fatalf("prepare-tunnel-host failed: %v", prepPayload["log"])
	}

	client.handleTunnelCommand("shutdown-tunnel-host", map[string]interface{}{
		"pipeline_id": "pipe-tun",
		"step_id":     "step-stop-host",
		"token":       hostToken,
	})

	if _, exists := activeTunnels.Load(hostToken); exists {
		t.Errorf("Expected host tunnel to be removed from activeTunnels after shutdown-tunnel-host")
	}

	// 2. Test execute-tunnel-client failure path (unreachable port) & shutdown-tunnel-client via handleTunnelCommand
	clientToken := "client-test-token-64-bytes-long-xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx"
	client.handleTunnelCommand("execute-tunnel-client", map[string]interface{}{
		"pipeline_id":       "pipe-tun",
		"step_id":           "step-client",
		"token":             clientToken,
		"host_ip":           "127.0.0.1",
		"host_control_port": float64(1),
		"fingerprint":       "deadbeef",
		"local_port":        float64(8080),
	})

	client.handleTunnelCommand("shutdown-tunnel-client", map[string]interface{}{
		"pipeline_id": "pipe-tun",
		"step_id":     "step-stop-client",
		"token":       clientToken,
	})

	// 3. Test unknown action
	capturedBodies = nil
	client.handleTunnelCommand("invalid-tunnel-action", map[string]interface{}{
		"pipeline_id": "pipe-tun",
		"step_id":     "step-unknown",
		"token":       hostToken,
	})
	if len(capturedBodies) == 0 {
		t.Fatalf("Expected completion payload for unknown tunnel action")
	}
	var unknownPayload map[string]interface{}
	_ = json.Unmarshal(capturedBodies[0], &unknownPayload)
	if unknownPayload["success"].(bool) {
		t.Errorf("Expected success=false for unknown tunnel action")
	}
}

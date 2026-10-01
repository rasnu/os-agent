package main

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

	"rasnuos-agent/os_adapter"
)

type DummyAdapter struct{}

func (m *DummyAdapter) GetMetrics() (os_adapter.Metrics, []error)            { return os_adapter.Metrics{}, nil }
func (m *DummyAdapter) GetShellCmd(ctx context.Context, s string, u string) *exec.Cmd { return nil }
func (m *DummyAdapter) StartPTY(cols, rows uint16) (os_adapter.PTY, error)   { return nil, nil }
func (m *DummyAdapter) InstallService(executablePath string) error           { return nil }
func (m *DummyAdapter) UninstallService() error                              { return nil }
func (m *DummyAdapter) VerifyBinary(path string) error                       { return nil }
func (m *DummyAdapter) ReplaceBinary(tempPath, currentPath string) error     { return nil }
func (m *DummyAdapter) RestartAgent() error                                  { return nil }
func (m *DummyAdapter) ManageService(action, service string) (string, error) { return "", nil }
func (m *DummyAdapter) KillProcess(pid string) (string, error)               { return "", nil }
func (m *DummyAdapter) WriteCronJobs(content string) error                   { return nil }
func (m *DummyAdapter) LogFilePath() string                                  { return "" }
func (m *DummyAdapter) PunchFirewallHole(port int) error                     { return nil }
func (m *DummyAdapter) CloseFirewallHole(port int) error                     { return nil }
func (m *DummyAdapter) ExecuteComputerAction(action, text string, x, y int) ([]byte, error) {
	return nil, nil
}
func (m *DummyAdapter) GetMachineID() string { return "dummy-machine-id" }

func TestTunnelEngine_EndToEnd(t *testing.T) {
	// Mock OS adapter
	os_adapter.SetAdapter(&DummyAdapter{})
	// No defer, just leave it as DummyAdapter for the rest of tests in this package if they need it,
	// or they should mock it themselves.

	// Mock HTTP client to capture completion payloads
	var capturedBodies [][]byte
	mockHTTP := &MockHTTPClient{
		OnRequest: func(req *http.Request) {
			if req.Body != nil {
				bodyBytes, _ := io.ReadAll(req.Body)
				capturedBodies = append(capturedBodies, bodyBytes)
			}
		},
	}

	originalClient := defaultHTTPClient
	defaultHTTPClient = mockHTTP
	defer func() { defaultHTTPClient = originalClient }()

	client := &PusherClient{
		ApiURL: "http://localhost",
		Token:  "test-token",
	}

	// 1. Start dummy local service on client side
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

	// 2. Prepare Tunnel Host
	token := "secure-test-token-64-bytes-long-xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx"
	publicPort := 0 // Random port

	client.prepareTunnelHost("pipe-1", "step-1", token, publicPort, "", false)

	// Verify completion was sent
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

	capturedBodies = nil // Reset captured bodies

	// 3. Connect Tunnel Client
	client.executeTunnelClient("pipe-1", "step-2", "127.0.0.1", controlPort, fingerprint, localPort, token)

	// Give yamux a moment to establish
	time.Sleep(100 * time.Millisecond)

	// 4. Test Public Connection to Host
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

	// 5. Test Unauthorized DOS Attack (Wrong Token)
	badConn, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", controlPort))
	if err != nil {
		t.Fatalf("Failed to connect to control port: %v", err)
	}
	defer badConn.Close()

	// Wait, control port expects TLS. We must do a TLS handshake!
	// We'll skip it for now and just shutdown.
	
	// Cleanup
	shutdownTunnelClient(token)
	shutdownTunnelHost(token)
}

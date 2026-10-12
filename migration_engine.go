package main

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math/big"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"
	"time"

	"rasnuos-agent/os_adapter"
)

// platformShellCmd returns an exec.Cmd that runs the given script via the
// appropriate shell for the current OS. It uses the package-level execCommand
// variable so that it can be mocked in tests.
func platformShellCmd(script string) *exec.Cmd {
	if runtime.GOOS == "windows" {
		return execCommand("powershell", "-NoProfile", "-NonInteractive", "-Command", script)
	}
	return execCommand("bash", "-c", script)
}

type MigrationServer struct {
	listener    net.Listener
	fingerprint string
	port        int
	cmd         *exec.Cmd
}

var activeMigrationServers sync.Map // Map of token -> *MigrationServer

func (c *PusherClient) handleMigrationCommand(action string, args map[string]interface{}) {
	pipelineId := getString(args, "pipeline_id")
	stepId := getString(args, "step_id")
	mode := getString(args, "mode") // "push" or "pull"
	targetPath := getString(args, "path")
	token := getString(args, "token")

	if pipelineId == "" || stepId == "" || token == "" {
		log.Printf("Migration command missing required arguments")
		if stepId != "" {
			c.sendMigrationCompletion(pipelineId, stepId, false, "Migration command missing required arguments", nil)
		}
		return
	}

	switch action {
	case "prepare-migration-receiver":
		portF, ok := args["port"].(float64)
		port := 0
		if ok {
			port = int(portF)
		}
		c.prepareMigrationReceiver(pipelineId, stepId, targetPath, token, port)
		logAuditEvent(AuditEvent{Action: "prepare_migration_receiver", PipelineID: pipelineId, StepID: stepId, Command: fmt.Sprintf("Port:%d Path:%s", port, targetPath), Success: true})
	case "execute-migration-sender":
		host := getString(args, "host")
		portF, ok := args["port"].(float64)
		port := 0
		if ok {
			port = int(portF)
		}
		fingerprint := getString(args, "fingerprint")
		rsyncPassword := getString(args, "rsync_password")
		sourcePath := getString(args, "source_path")
		if host == "" || port == 0 || fingerprint == "" || rsyncPassword == "" || sourcePath == "" {
			log.Printf("Migration sender missing arguments")
			c.sendMigrationCompletion(pipelineId, stepId, false, "Migration sender missing required arguments", nil)
			return
		}
		c.executeMigrationSender(pipelineId, stepId, mode, sourcePath, targetPath, host, port, token, fingerprint, rsyncPassword)
		logAuditEvent(AuditEvent{Action: "execute_migration_sender", PipelineID: pipelineId, StepID: stepId, Command: fmt.Sprintf("Host:%s Source:%s Dest:%s", host, sourcePath, targetPath), Success: true})
	case "stop-migration-receiver":
		shutdownMigrationServer(token)
		c.sendMigrationCompletion(pipelineId, stepId, true, "Migration receiver stopped", nil)
		logAuditEvent(AuditEvent{Action: "stop_migration_receiver", PipelineID: pipelineId, StepID: stepId, Success: true})
	case "prepare-p2p-receiver":
		portF, ok := args["port"].(float64)
		port := 0
		if ok { port = int(portF) }
		receiveCmd := getString(args, "receive_command")
		c.prepareP2pReceiver(pipelineId, stepId, receiveCmd, token, port)
		logAuditEvent(AuditEvent{Action: "prepare_p2p_receiver", PipelineID: pipelineId, StepID: stepId, Command: redactSecrets(receiveCmd), Success: true})
	case "run-p2p-sender":
		host := getString(args, "host")
		portF, ok := args["port"].(float64)
		port := 0
		if ok { port = int(portF) }
		fingerprint := getString(args, "fingerprint")
		sendCmd := getString(args, "send_command")
		c.runP2pSender(pipelineId, stepId, sendCmd, host, port, fingerprint, token)
		logAuditEvent(AuditEvent{Action: "run_p2p_sender", PipelineID: pipelineId, StepID: stepId, Command: redactSecrets(sendCmd), Success: true})
	case "stop-p2p-receiver":
		shutdownMigrationServer(token)
		c.sendMigrationCompletion(pipelineId, stepId, true, "P2P receiver stopped", nil)
		logAuditEvent(AuditEvent{Action: "stop_p2p_receiver", PipelineID: pipelineId, StepID: stepId, Success: true})
	case "inject-secret":
		// Pillar 3: Zero-Trust in-RAM secret injection.
		// SECURITY: secrets are stored in process RAM only, never written to disk.
		// The token arg here is the secret scope key, NOT the agent auth token.
		c.handleInjectSecret(pipelineId, stepId, args)
		logAuditEvent(AuditEvent{Action: "inject_secret", PipelineID: pipelineId, StepID: stepId, Success: true})
	default:
		c.sendMigrationCompletion(pipelineId, stepId, false, "Unknown migration action", nil)
		logAuditEvent(AuditEvent{Action: "unknown_migration", PipelineID: pipelineId, StepID: stepId, Success: false, Error: "Unknown migration action"})
	}
}

// ---------------------------------------------------------
// RECEIVER / HOST (mTLS Tunnel & Rsync Daemon)
// ---------------------------------------------------------
func (c *PusherClient) prepareMigrationReceiver(pipelineId, stepId, targetPath, token string, requestedPort int) {
	// 1. Generate mTLS Cert
	cert, _, err := generateSelfSignedCert()
	if err != nil {
		c.sendMigrationCompletion(pipelineId, stepId, false, fmt.Sprintf("Failed to generate cert: %v", err), nil)
		return
	}

	hash := sha256.Sum256(cert.Certificate[0])
	fingerprint := hex.EncodeToString(hash[:])

	// 2. Generate secure rsync password
	randBytes := make([]byte, 16)
	rand.Read(randBytes)
	rsyncPassword := hex.EncodeToString(randBytes)

	// 3. Write secrets file
	secretsFile := filepath.Join(os.TempDir(), fmt.Sprintf("rsyncd_%s.secrets", token))
	err = os.WriteFile(secretsFile, []byte(fmt.Sprintf("rasnuos:%s\n", rsyncPassword)), 0600)
	if err != nil {
		c.sendMigrationCompletion(pipelineId, stepId, false, fmt.Sprintf("Failed to write secrets: %v", err), nil)
		return
	}

	// 4. Find an open local port for rsync
	rsyncListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		c.sendMigrationCompletion(pipelineId, stepId, false, fmt.Sprintf("Failed to find local rsync port: %v", err), nil)
		return
	}
	localRsyncPort := rsyncListener.Addr().(*net.TCPAddr).Port
	rsyncListener.Close() // Free it so rsync can bind

	// 5. Write rsyncd config
	configFile := filepath.Join(os.TempDir(), fmt.Sprintf("rsyncd_%s.conf", token))
	pidFile := filepath.Join(os.TempDir(), fmt.Sprintf("rsyncd_%s.pid", token))
	lockFile := filepath.Join(os.TempDir(), fmt.Sprintf("rsyncd_%s.lock", token))
	logFile := filepath.Join(os.TempDir(), fmt.Sprintf("rsyncd_%s.log", token))

	uidGidConfig := "uid = root\ngid = root"
	if os.Geteuid() != 0 {
		uidGidConfig = ""
	}

	configContent := fmt.Sprintf(`
port = %d
use chroot = no
read only = no
%s
pid file = %s
lock file = %s
log file = %s
[migration]
path = /
auth users = rasnuos
secrets file = %s
`, localRsyncPort, uidGidConfig, pidFile, lockFile, logFile, secretsFile)

	err = os.WriteFile(configFile, []byte(configContent), 0644)
	if err != nil {
		c.sendMigrationCompletion(pipelineId, stepId, false, fmt.Sprintf("Failed to write config: %v", err), nil)
		return
	}

	// 6. Start rsync daemon
	cmd := execCommand("rsync", "--daemon", "--no-detach", fmt.Sprintf("--config=%s", configFile))
	err = cmd.Start()
	if err != nil {
		c.sendMigrationCompletion(pipelineId, stepId, false, fmt.Sprintf("Failed to start rsync daemon: %v", err), nil)
		return
	}

	// 7. Start mTLS Listener on Public Port
	addr := fmt.Sprintf("0.0.0.0:%d", requestedPort)
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		cmd.Process.Kill()
		c.sendMigrationCompletion(pipelineId, stepId, false, fmt.Sprintf("Failed to start listener on %s: %v", addr, err), nil)
		return
	}
	publicPort := listener.Addr().(*net.TCPAddr).Port

	// Punch hole in local firewalls using OS Adapter
	os_adapter.Current().PunchFirewallHole(publicPort)

	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{*cert},
	}
	tlsListener := tls.NewListener(listener, tlsConfig)

	// Proxy incoming mTLS connections to local rsync daemon
	go func() {
		for {
			conn, err := tlsListener.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				rsyncConn, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", localRsyncPort))
				if err != nil {
					log.Printf("Failed to connect to local rsync: %v", err)
					return
				}
				defer rsyncConn.Close()
				proxyConn(c, rsyncConn)
			}(conn)
		}
	}()

	ms := &MigrationServer{
		listener:    tlsListener,
		fingerprint: fingerprint,
		port:        publicPort,
		cmd:         cmd,
	}
	activeMigrationServers.Store(token, ms)

	// Fail-safe: Hard timeout after 4 hours
	go func() {
		time.Sleep(4 * time.Hour)
		shutdownMigrationServer(token)
	}()

	data := map[string]interface{}{
		"port":           publicPort,
		"fingerprint":    fingerprint,
		"rsync_password": rsyncPassword,
	}
	c.sendMigrationCompletion(pipelineId, stepId, true, "Migration receiver ready", data)
}

func shutdownMigrationServer(token string) {
	if val, ok := activeMigrationServers.Load(token); ok {
		ms := val.(*MigrationServer)
		ms.listener.Close()

		// Clean up firewall rules
		os_adapter.Current().CloseFirewallHole(ms.port)

		if ms.cmd != nil && ms.cmd.Process != nil {
			ms.cmd.Process.Kill()
		}
		os.Remove(filepath.Join(os.TempDir(), fmt.Sprintf("rsyncd_%s.conf", token)))
		os.Remove(filepath.Join(os.TempDir(), fmt.Sprintf("rsyncd_%s.secrets", token)))
		activeMigrationServers.Delete(token)
	}
}

// ---------------------------------------------------------
// SENDER / CLIENT (mTLS Proxy & Rsync Client)
// ---------------------------------------------------------
func (c *PusherClient) executeMigrationSender(pipelineId, stepId, mode, sourcePath, targetPath, host string, port int, token, fingerprint, rsyncPassword string) {
	// 1. Setup local listener
	localListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		c.sendMigrationCompletion(pipelineId, stepId, false, fmt.Sprintf("Failed to setup local listener: %v", err), nil)
		return
	}
	localPort := localListener.Addr().(*net.TCPAddr).Port

	// 2. Proxy local connections to remote mTLS server
	go func() {
		for {
			localConn, err := localListener.Accept()
			if err != nil {
				return
			}
			go func(lc net.Conn) {
				defer lc.Close()

				tlsConfig := &tls.Config{
					InsecureSkipVerify: true,
					VerifyPeerCertificate: func(rawCerts [][]byte, verifiedChains [][]*x509.Certificate) error {
						if len(rawCerts) == 0 {
							return fmt.Errorf("no certificates provided by server")
						}
						hash := sha256.Sum256(rawCerts[0])
						certFingerprint := hex.EncodeToString(hash[:])
						if certFingerprint != fingerprint {
							return fmt.Errorf("certificate fingerprint mismatch! Expected %s, got %s", fingerprint, certFingerprint)
						}
						return nil
					},
				}

				dialer := &net.Dialer{
					Timeout: 10 * time.Second,
				}
				remoteConn, err := tls.DialWithDialer(dialer, "tcp", net.JoinHostPort(host, strconv.Itoa(port)), tlsConfig)
				if err != nil {
					log.Printf("Failed to connect to remote mTLS server: %v", err)
					return
				}
				defer remoteConn.Close()
				proxyConn(lc, remoteConn)
			}(localConn)
		}
	}()

	// 3. Execute rsync client with Retry Loop
	var rsyncArgs []string
	if mode == "push" {
		rsyncArgs = []string{"-avz", "--progress", sourcePath, fmt.Sprintf("rsync://rasnuos@127.0.0.1:%d/migration%s", localPort, targetPath)}
	} else {
		rsyncArgs = []string{"-avz", "--progress", fmt.Sprintf("rsync://rasnuos@127.0.0.1:%d/migration%s", localPort, sourcePath), targetPath}
	}

	var out bytes.Buffer

	for attempt := 1; attempt <= 5; attempt++ {
		cmd := execCommand("rsync", rsyncArgs...)
		cmd.Env = append(os.Environ(), fmt.Sprintf("RSYNC_PASSWORD=%s", rsyncPassword))

		stdout, pipeErr := cmd.StdoutPipe()
		if pipeErr != nil {
			c.sendMigrationCompletion(pipelineId, stepId, false, fmt.Sprintf("Error creating stdout pipe: %v", pipeErr), nil)
			return
		}

		stderr, pipeErr := cmd.StderrPipe()
		if pipeErr != nil {
			c.sendMigrationCompletion(pipelineId, stepId, false, fmt.Sprintf("Error creating stderr pipe: %v", pipeErr), nil)
			return
		}

		if startErr := cmd.Start(); startErr != nil {
			log.Printf("Rsync attempt %d failed to start: %v", attempt, startErr)
			time.Sleep(2 * time.Second)
			continue
		}

		logBuf := &LogBuffer{}
		batcher := NewStreamBatcher(c, pipelineId, stepId, logBuf)

		var streamWg sync.WaitGroup
		streamWg.Add(2)

		go c.readStream(stepId, stdout, batcher, &streamWg)
		go c.readStream(stepId, stderr, batcher, &streamWg)

		streamWg.Wait()
		err = cmd.Wait()
		batcher.Close()

		out.WriteString(logBuf.String())

		if err == nil {
			break // Success!
		}

		log.Printf("Rsync attempt %d failed: %v", attempt, err)
		time.Sleep(2 * time.Second)
	}

	localListener.Close() // Stop accepting connections once rsync finishes

	if err != nil {
		c.sendMigrationCompletion(pipelineId, stepId, false, fmt.Sprintf("Rsync failed after 5 attempts: %v\nOutput: %s", err, out.String()), nil)
		return
	}

	c.sendMigrationCompletion(pipelineId, stepId, true, out.String(), nil)
}

// ---------------------------------------------------------
// HELPERS
// ---------------------------------------------------------
func generateSelfSignedCert() (*tls.Certificate, interface{}, error) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}

	notBefore := time.Now().Add(-1 * time.Hour) // Valid from 1 hour ago to prevent clock skew issues
	notAfter := time.Now().Add(24 * time.Hour)  // Valid for 1 day
	serialNumberLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	serialNumber, err := rand.Int(rand.Reader, serialNumberLimit)
	if err != nil {
		return nil, nil, err
	}

	template := x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			Organization: []string{"RasnuOS Temporary Migration"},
		},
		NotBefore:             notBefore,
		NotAfter:              notAfter,
		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}

	derBytes, err := x509.CreateCertificate(rand.Reader, &template, &template, &priv.PublicKey, priv)
	if err != nil {
		return nil, nil, err
	}

	cert := &tls.Certificate{
		Certificate: [][]byte{derBytes},
		PrivateKey:  priv,
	}
	return cert, priv, nil
}

func (c *PusherClient) sendMigrationCompletion(pipelineId, stepId string, success bool, logMsg string, data interface{}) {
	completionURL := fmt.Sprintf("%s/agent/migration/complete", c.ApiURL)

	reqBody, _ := json.Marshal(map[string]interface{}{
		"pipeline_id": pipelineId,
		"step_id":     stepId,
		"success":     success,
		"log":         logMsg,
		"data":        data,
	})

	req, _ := http.NewRequest("POST", completionURL, bytes.NewBuffer(reqBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.Token)
	resp, err := getDefaultHTTPClient().Do(req)
	if err != nil {
		log.Printf("Failed to send migration completion webhook: %v", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		log.Printf("Webhook error [%d]: %s", resp.StatusCode, string(body))
	}
}

// ---------------------------------------------------------
// P2P STREAM RECEIVER (mTLS Tunnel)
// ---------------------------------------------------------
func (c *PusherClient) prepareP2pReceiver(pipelineId, stepId, receiveCmd, token string, requestedPort int) {
	// Cross-platform guard: rsync is not available on Windows.
	if runtime.GOOS == "windows" {
		c.sendMigrationCompletion(pipelineId, stepId, false, "P2P migration via rsync is not supported on Windows. The source/destination servers must be Linux or macOS.", nil)
		return
	}

	cert, _, err := generateSelfSignedCert()
	if err != nil {
		c.sendMigrationCompletion(pipelineId, stepId, false, fmt.Sprintf("Failed to generate cert: %v", err), nil)
		return
	}
	hash := sha256.Sum256(cert.Certificate[0])
	fingerprint := hex.EncodeToString(hash[:])

	addr := fmt.Sprintf("0.0.0.0:%d", requestedPort)
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		c.sendMigrationCompletion(pipelineId, stepId, false, fmt.Sprintf("Failed to start listener on %s: %v", addr, err), nil)
		return
	}
	publicPort := listener.Addr().(*net.TCPAddr).Port

	os_adapter.Current().PunchFirewallHole(publicPort)

	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{*cert},
	}
	tlsListener := tls.NewListener(listener, tlsConfig)

	ms := &MigrationServer{
		listener:    tlsListener,
		fingerprint: fingerprint,
		port:        publicPort,
		cmd:         nil,
	}
	activeMigrationServers.Store(token, ms)

	go func() {
		defer shutdownMigrationServer(token)

		// Wait for one connection
		conn, err := tlsListener.Accept()
		if err != nil {
			log.Printf("P2P listener error or closed: %v", err)
			return
		}
		defer conn.Close()

		// 1. Enterprise Security: Token-Based Pre-Authentication
		// We read exactly 64 bytes to verify the incoming connection is authorized
		tokenBuf := make([]byte, 64)
		conn.SetReadDeadline(time.Now().Add(10 * time.Second))
		if _, err := io.ReadFull(conn, tokenBuf); err != nil {
			log.Printf("P2P auth failed (did not receive token): %v", err)
			return
		}
		conn.SetReadDeadline(time.Time{}) // Reset deadline

		if string(tokenBuf) != token {
			log.Printf("P2P auth failed: Invalid token received")
			return
		}

		// Execute the receive command
		// Use the OS-appropriate shell (PowerShell on Windows, bash on Linux/macOS).
		// Uses execCommand so that it can be mocked in tests.
		cmd := platformShellCmd(receiveCmd)
		ms.cmd = cmd

		// Pipe the network connection directly into cmd.Stdin
		cmd.Stdin = conn
		cmd.Stdout = conn
		
		// 2. Enterprise Observability: Pipe Receiver's Stderr back to WS safely using StreamBatcher
		stderrPipe, err := cmd.StderrPipe()
		if err == nil {
			batcher := NewStreamBatcher(c, pipelineId, stepId, nil)
			var streamWg sync.WaitGroup
			streamWg.Add(1)
			go func() {
				defer batcher.Close()
				c.readStream(stepId, stderrPipe, batcher, &streamWg)
			}()
		}

		if err := cmd.Start(); err != nil {
			log.Printf("Failed to start receive command: %v", err)
			return
		}

		cmd.Wait()
	}()

	// Fail-safe: Hard timeout after 4 hours
	go func() {
		time.Sleep(4 * time.Hour)
		shutdownMigrationServer(token)
	}()

	data := map[string]interface{}{
		"port":        publicPort,
		"fingerprint": fingerprint,
	}
	c.sendMigrationCompletion(pipelineId, stepId, true, "P2P Receiver ready", data)
}

// ---------------------------------------------------------
// P2P STREAM SENDER (mTLS Client)
// ---------------------------------------------------------
type progressWriter struct {
	pipelineId string
	stepId     string
	client     *PusherClient
	totalBytes int64
	lastReport time.Time
}

func (pw *progressWriter) Write(p []byte) (int, error) {
	n := len(p)
	pw.totalBytes += int64(n)
	now := time.Now()
	if now.Sub(pw.lastReport) > time.Second {
		pw.lastReport = now
		mb := float64(pw.totalBytes) / 1024.0 / 1024.0
		msg := fmt.Sprintf("\rStreaming P2P Data... Transferred: %.2f MB", mb)
		pw.client.streamLogHTTP(pw.pipelineId, pw.stepId, msg)
	}
	return n, nil
}

func (c *PusherClient) runP2pSender(pipelineId, stepId, sendCmd, host string, port int, fingerprint, token string) {
	// Cross-platform guard: rsync is not available on Windows.
	if runtime.GOOS == "windows" {
		c.sendMigrationCompletion(pipelineId, stepId, false, "P2P migration via rsync is not supported on Windows. The source/destination servers must be Linux or macOS.", nil)
		return
	}

	tlsConfig := &tls.Config{
		InsecureSkipVerify: true,
		VerifyPeerCertificate: func(rawCerts [][]byte, verifiedChains [][]*x509.Certificate) error {
			if len(rawCerts) == 0 {
				return fmt.Errorf("no certificates provided")
			}
			hash := sha256.Sum256(rawCerts[0])
			certFingerprint := hex.EncodeToString(hash[:])
			if certFingerprint != fingerprint {
				return fmt.Errorf("certificate fingerprint mismatch")
			}
			return nil
		},
	}

	dialer := &net.Dialer{Timeout: 10 * time.Second}
	remoteConn, err := tls.DialWithDialer(dialer, "tcp", net.JoinHostPort(host, strconv.Itoa(port)), tlsConfig)
	if err != nil {
		c.sendMigrationCompletion(pipelineId, stepId, false, fmt.Sprintf("Failed to dial P2P receiver: %v", err), nil)
		return
	}
	defer remoteConn.Close()

	// 1. Enterprise Security: Authenticate with the Receiver by sending the 64-byte token
	if len(token) != 64 {
		c.sendMigrationCompletion(pipelineId, stepId, false, "Invalid P2P token length", nil)
		return
	}
	if _, err := remoteConn.Write([]byte(token)); err != nil {
		c.sendMigrationCompletion(pipelineId, stepId, false, fmt.Sprintf("Failed to send P2P token: %v", err), nil)
		return
	}

	// Use the OS-appropriate shell (PowerShell on Windows, bash on Linux/macOS).
	// Uses execCommand so that it can be mocked in tests.
	cmd := platformShellCmd(sendCmd)
	
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		c.sendMigrationCompletion(pipelineId, stepId, false, fmt.Sprintf("Failed to get stdout pipe: %v", err), nil)
		return
	}

	stderrPipe, _ := cmd.StderrPipe()
	
	batcher := NewStreamBatcher(c, pipelineId, stepId, nil)
	var streamWg sync.WaitGroup

	streamWg.Add(1)
	go c.readStream(stepId, stderrPipe, batcher, &streamWg)

	if err := cmd.Start(); err != nil {
		c.sendMigrationCompletion(pipelineId, stepId, false, fmt.Sprintf("Failed to start send command: %v", err), nil)
		return
	}

	pw := &progressWriter{
		pipelineId: pipelineId,
		stepId:     stepId,
		client:     c,
		lastReport: time.Now(),
	}

	go func() {
		mw := io.MultiWriter(remoteConn, pw)
		io.Copy(mw, stdoutPipe)
	}()

	// 3. Enterprise Observability & Dead-lock Prevention:
	// Read the Receiver's output from the mTLS tunnel to prevent TCP buffer full (dead-lock)
	// and stream it back to the UI securely using StreamBatcher.
	streamWg.Add(1)
	go c.readStream(stepId, remoteConn, batcher, &streamWg)

	go func() {
		streamWg.Wait()
		batcher.Close()
	}()

	// 2. Enterprise Observability: Enforce strict cmd.Wait() error checking
	if err := cmd.Wait(); err != nil {
		c.streamLogHTTP(pipelineId, stepId, fmt.Sprintf("\r\nP2P Sender Command Failed: %v\n", err))
		c.sendMigrationCompletion(pipelineId, stepId, false, fmt.Sprintf("P2P sender execution failed: %v", err), nil)
		return
	}

	mb := float64(pw.totalBytes) / 1024.0 / 1024.0
	c.streamLogHTTP(pipelineId, stepId, fmt.Sprintf("\r\nCompleted P2P Transfer: %.2f MB\n", mb))

	c.sendMigrationCompletion(pipelineId, stepId, true, "P2P sender execution complete", nil)
}

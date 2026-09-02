// Copyright © 2026 Rasnu LLC (rasnu.com). All rights reserved.
// Licensed under the Functional Source License, Version 1.1, ALv2 Future License (FSL-1.1-ALv2).

package agent

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
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

	"rasnuos-agent/internal/audit"
	"rasnuos-agent/internal/guardrails"
	"rasnuos-agent/internal/osadapter"
	"rasnuos-agent/internal/transport"
)

func safeMigrationFileID(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:8])
}

func platformShellCmd(script string) *exec.Cmd {
	if runtime.GOOS == "windows" {
		return execCommand("powershell", "-NoProfile", "-NonInteractive", "-Command", script)
	}
	return execCommand("bash", "-c", script)
}

type MigrationServer struct {
	mu          sync.Mutex
	listener    net.Listener
	fingerprint string
	port        int
	cmd         *exec.Cmd
}

var activeMigrationServers sync.Map

func (c *PusherClient) handleMigrationCommand(action string, args map[string]interface{}) {
	pipelineId := getString(args, "pipeline_id")
	stepId := getString(args, "step_id")
	mode := getString(args, "mode")
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
		audit.Log(audit.Event{Action: "prepare_migration_receiver", PipelineID: pipelineId, StepID: stepId, Command: fmt.Sprintf("Port:%d Path:%s", port, targetPath), Success: true})
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
		audit.Log(audit.Event{Action: "execute_migration_sender", PipelineID: pipelineId, StepID: stepId, Command: fmt.Sprintf("Host:%s Source:%s Dest:%s", host, sourcePath, targetPath), Success: true})
	case "stop-migration-receiver":
		shutdownMigrationServer(token)
		c.sendMigrationCompletion(pipelineId, stepId, true, "Migration receiver stopped", nil)
		audit.Log(audit.Event{Action: "stop_migration_receiver", PipelineID: pipelineId, StepID: stepId, Success: true})
	case "prepare-p2p-receiver":
		portF, ok := args["port"].(float64)
		port := 0
		if ok {
			port = int(portF)
		}
		receiveCmd := getString(args, "receive_command")
		c.prepareP2pReceiver(pipelineId, stepId, receiveCmd, token, port)
		audit.Log(audit.Event{Action: "prepare_p2p_receiver", PipelineID: pipelineId, StepID: stepId, Command: redactSecrets(receiveCmd), Success: true})
	case "run-p2p-sender":
		host := getString(args, "host")
		portF, ok := args["port"].(float64)
		port := 0
		if ok {
			port = int(portF)
		}
		fingerprint := getString(args, "fingerprint")
		sendCmd := getString(args, "send_command")
		c.runP2pSender(pipelineId, stepId, sendCmd, host, port, fingerprint, token)
		audit.Log(audit.Event{Action: "run_p2p_sender", PipelineID: pipelineId, StepID: stepId, Command: redactSecrets(sendCmd), Success: true})
	case "stop-p2p-receiver":
		shutdownMigrationServer(token)
		c.sendMigrationCompletion(pipelineId, stepId, true, "P2P receiver stopped", nil)
		audit.Log(audit.Event{Action: "stop_p2p_receiver", PipelineID: pipelineId, StepID: stepId, Success: true})
	case "inject-secret":
		// Secrets are stored in process RAM only under the scope token and never written to disk.
		c.handleInjectSecret(pipelineId, stepId, args)
		audit.Log(audit.Event{Action: "inject_secret", PipelineID: pipelineId, StepID: stepId, Success: true})
	default:
		c.sendMigrationCompletion(pipelineId, stepId, false, "Unknown migration action", nil)
		audit.Log(audit.Event{Action: "unknown_migration", PipelineID: pipelineId, StepID: stepId, Success: false, Error: "Unknown migration action"})
	}
}

func (c *PusherClient) prepareMigrationReceiver(pipelineId, stepId, targetPath, token string, requestedPort int) {
	cert, _, err := generateSelfSignedCert()
	if err != nil {
		c.sendMigrationCompletion(pipelineId, stepId, false, fmt.Sprintf("Failed to generate cert: %v", err), nil)
		return
	}

	hash := sha256.Sum256(cert.Certificate[0])
	fingerprint := hex.EncodeToString(hash[:])

	randBytes := make([]byte, 16)
	rand.Read(randBytes)
	rsyncPassword := hex.EncodeToString(randBytes)

	fileID := safeMigrationFileID(token)
	secretsFile := filepath.Join(os.TempDir(), fmt.Sprintf("rsyncd_%s.secrets", fileID))
	err = os.WriteFile(secretsFile, []byte(fmt.Sprintf("rasnuos:%s\n", rsyncPassword)), 0600)
	if err != nil {
		c.sendMigrationCompletion(pipelineId, stepId, false, fmt.Sprintf("Failed to write secrets: %v", err), nil)
		return
	}

	rsyncListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		c.sendMigrationCompletion(pipelineId, stepId, false, fmt.Sprintf("Failed to find local rsync port: %v", err), nil)
		return
	}
	localRsyncPort := rsyncListener.Addr().(*net.TCPAddr).Port
	rsyncListener.Close()

	configFile := filepath.Join(os.TempDir(), fmt.Sprintf("rsyncd_%s.conf", fileID))
	pidFile := filepath.Join(os.TempDir(), fmt.Sprintf("rsyncd_%s.pid", fileID))
	lockFile := filepath.Join(os.TempDir(), fmt.Sprintf("rsyncd_%s.lock", fileID))
	logFile := filepath.Join(os.TempDir(), fmt.Sprintf("rsyncd_%s.log", fileID))

	uidGidConfig := "uid = root\ngid = root"
	if os.Geteuid() != 0 {
		uidGidConfig = ""
	}

	configContent := fmt.Sprintf(`
address = 127.0.0.1
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

	err = os.WriteFile(configFile, []byte(configContent), 0600)
	if err != nil {
		c.sendMigrationCompletion(pipelineId, stepId, false, fmt.Sprintf("Failed to write config: %v", err), nil)
		return
	}

	cmd := execCommand("rsync", "--daemon", "--no-detach", fmt.Sprintf("--config=%s", configFile))
	err = cmd.Start()
	if err != nil {
		c.sendMigrationCompletion(pipelineId, stepId, false, fmt.Sprintf("Failed to start rsync daemon: %v", err), nil)
		return
	}

	addr := fmt.Sprintf("0.0.0.0:%d", requestedPort)
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		cmd.Process.Kill()
		c.sendMigrationCompletion(pipelineId, stepId, false, fmt.Sprintf("Failed to start listener on %s: %v", addr, err), nil)
		return
	}
	publicPort := listener.Addr().(*net.TCPAddr).Port

	osadapter.Current().PunchFirewallHole(publicPort)

	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{*cert},
	}
	tlsListener := tls.NewListener(listener, tlsConfig)

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

		osadapter.Current().CloseFirewallHole(ms.port)

		ms.mu.Lock()
		if ms.cmd != nil && ms.cmd.Process != nil {
			ms.cmd.Process.Kill()
		}
		ms.mu.Unlock()
		fileID := safeMigrationFileID(token)
		os.Remove(filepath.Join(os.TempDir(), fmt.Sprintf("rsyncd_%s.conf", fileID)))
		os.Remove(filepath.Join(os.TempDir(), fmt.Sprintf("rsyncd_%s.secrets", fileID)))
		os.Remove(filepath.Join(os.TempDir(), fmt.Sprintf("rsyncd_%s.pid", fileID)))
		os.Remove(filepath.Join(os.TempDir(), fmt.Sprintf("rsyncd_%s.lock", fileID)))
		os.Remove(filepath.Join(os.TempDir(), fmt.Sprintf("rsyncd_%s.log", fileID)))
		activeMigrationServers.Delete(token)
	}
}

func (c *PusherClient) executeMigrationSender(pipelineId, stepId, mode, sourcePath, targetPath, host string, port int, token, fingerprint, rsyncPassword string) {
	localListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		c.sendMigrationCompletion(pipelineId, stepId, false, fmt.Sprintf("Failed to setup local listener: %v", err), nil)
		return
	}
	localPort := localListener.Addr().(*net.TCPAddr).Port

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
						if subtle.ConstantTimeCompare([]byte(certFingerprint), []byte(fingerprint)) != 1 {
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
			break
		}

		log.Printf("Rsync attempt %d failed: %v", attempt, err)
		time.Sleep(2 * time.Second)
	}

	localListener.Close()

	if err != nil {
		c.sendMigrationCompletion(pipelineId, stepId, false, fmt.Sprintf("Rsync failed after 5 attempts: %v\nOutput: %s", err, out.String()), nil)
		return
	}

	c.sendMigrationCompletion(pipelineId, stepId, true, out.String(), nil)
}

func generateSelfSignedCert() (*tls.Certificate, interface{}, error) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}

	notBefore := time.Now().Add(-1 * time.Hour) // Backdate 1h to tolerate clock skew
	notAfter := time.Now().Add(24 * time.Hour)
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
	resp, err := transport.Default().Do(req)
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

func (c *PusherClient) prepareP2pReceiver(pipelineId, stepId, receiveCmd, token string, requestedPort int) {
	if runtime.GOOS == "windows" {
		c.sendMigrationCompletion(pipelineId, stepId, false, "P2P migration via rsync is not supported on Windows. The source/destination servers must be Linux or macOS.", nil)
		return
	}

	if blocked, pattern := guardrails.IsDestructive(receiveCmd); blocked {
		errMsg := fmt.Sprintf("SECURITY ALERT: Agent blocked destructive P2P receive command matching pattern %s", pattern)
		audit.Log(audit.Event{Action: "guardrail_blocked", PipelineID: pipelineId, StepID: stepId, Command: redactSecrets(receiveCmd), Success: false, Error: errMsg})
		c.sendMigrationCompletion(pipelineId, stepId, false, errMsg, nil)
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

	osadapter.Current().PunchFirewallHole(publicPort)

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

		conn, err := tlsListener.Accept()
		if err != nil {
			log.Printf("P2P listener error or closed: %v", err)
			return
		}
		defer conn.Close()

		// Require the 64-byte pre-shared token before piping the connection to the shell.
		tokenBuf := make([]byte, 64)
		conn.SetReadDeadline(time.Now().Add(10 * time.Second))
		if _, err := io.ReadFull(conn, tokenBuf); err != nil {
			log.Printf("P2P auth failed (did not receive token): %v", err)
			return
		}
		conn.SetReadDeadline(time.Time{})

		if subtle.ConstantTimeCompare(tokenBuf, []byte(token)) != 1 {
			log.Printf("P2P auth failed: Invalid token received")
			return
		}

		cmd := platformShellCmd(receiveCmd)

		cmd.Stdin = conn
		cmd.Stdout = conn

		stderrPipe, err := cmd.StderrPipe()
		var streamWg sync.WaitGroup
		if err == nil {
			batcher := NewStreamBatcher(c, pipelineId, stepId, nil)
			streamWg.Add(1)
			go func() {
				defer batcher.Close()
				c.readStream(stepId, stderrPipe, batcher, &streamWg)
			}()
		}

		ms.mu.Lock()
		if err := cmd.Start(); err != nil {
			ms.mu.Unlock()
			log.Printf("Failed to start receive command: %v", err)
			return
		}
		ms.cmd = cmd
		ms.mu.Unlock()

		streamWg.Wait()
		cmd.Wait()
	}()

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
	if runtime.GOOS == "windows" {
		c.sendMigrationCompletion(pipelineId, stepId, false, "P2P migration via rsync is not supported on Windows. The source/destination servers must be Linux or macOS.", nil)
		return
	}

	if blocked, pattern := guardrails.IsDestructive(sendCmd); blocked {
		errMsg := fmt.Sprintf("SECURITY ALERT: Agent blocked destructive P2P send command matching pattern %s", pattern)
		audit.Log(audit.Event{Action: "guardrail_blocked", PipelineID: pipelineId, StepID: stepId, Command: redactSecrets(sendCmd), Success: false, Error: errMsg})
		c.sendMigrationCompletion(pipelineId, stepId, false, errMsg, nil)
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
			if subtle.ConstantTimeCompare([]byte(certFingerprint), []byte(fingerprint)) != 1 {
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

	if len(token) != 64 {
		c.sendMigrationCompletion(pipelineId, stepId, false, "Invalid P2P token length", nil)
		return
	}
	if _, err := remoteConn.Write([]byte(token)); err != nil {
		c.sendMigrationCompletion(pipelineId, stepId, false, fmt.Sprintf("Failed to send P2P token: %v", err), nil)
		return
	}

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

	var stdoutWg sync.WaitGroup
	stdoutWg.Add(1)
	go func() {
		defer stdoutWg.Done()
		mw := io.MultiWriter(remoteConn, pw)
		io.Copy(mw, stdoutPipe)
	}()

	// Drain receiver output from the mTLS stream so the remote TCP send buffer never blocks.
	streamWg.Add(1)
	go c.readStream(stepId, remoteConn, batcher, &streamWg)

	// Wait for stdoutPipe to hit EOF before calling cmd.Wait() (per os/exec.Cmd.StdoutPipe contract)
	stdoutWg.Wait()
	cmdErr := cmd.Wait()

	// Signal EOF to the P2P receiver's stdin and wait for receiver stream & stderr to finish
	_ = remoteConn.CloseWrite()
	streamWg.Wait()
	batcher.Close()

	if cmdErr != nil {
		c.streamLogHTTP(pipelineId, stepId, fmt.Sprintf("\r\nP2P Sender Command Failed: %v\n", cmdErr))
		c.sendMigrationCompletion(pipelineId, stepId, false, fmt.Sprintf("P2P sender execution failed: %v", cmdErr), nil)
		return
	}

	mb := float64(pw.totalBytes) / 1024.0 / 1024.0
	c.streamLogHTTP(pipelineId, stepId, fmt.Sprintf("\r\nCompleted P2P Transfer: %.2f MB\n", mb))

	c.sendMigrationCompletion(pipelineId, stepId, true, "P2P sender execution complete", nil)
}

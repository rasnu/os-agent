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
	"sync"
	"time"
)

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

	if pipelineId == "" || stepId == "" || mode == "" || targetPath == "" || token == "" {
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
	case "stop-migration-receiver":
		shutdownMigrationServer(token)
		c.sendMigrationCompletion(pipelineId, stepId, true, "Migration receiver stopped", nil)
	default:
		c.sendMigrationCompletion(pipelineId, stepId, false, "Unknown migration action", nil)
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
	err = os.WriteFile(secretsFile, []byte(fmt.Sprintf("rasnuone:%s\n", rsyncPassword)), 0600)
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
auth users = rasnuone
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

				remoteConn, err := tls.Dial("tcp", fmt.Sprintf("%s:%d", host, port), tlsConfig)
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
		rsyncArgs = []string{"-avz", "--progress", sourcePath, fmt.Sprintf("rsync://rasnuone@127.0.0.1:%d/migration%s", localPort, targetPath)}
	} else {
		rsyncArgs = []string{"-avz", "--progress", fmt.Sprintf("rsync://rasnuone@127.0.0.1:%d/migration%s", localPort, sourcePath), targetPath}
	}

	var out bytes.Buffer

	for attempt := 1; attempt <= 5; attempt++ {
		cmd := execCommand("rsync", rsyncArgs...)
		cmd.Env = append(os.Environ(), fmt.Sprintf("RSYNC_PASSWORD=%s", rsyncPassword))

		out.Reset()
		cmd.Stdout = io.MultiWriter(&out, os.Stdout)
		cmd.Stderr = io.MultiWriter(&out, os.Stderr)

		err = cmd.Run()
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
			Organization: []string{"RasnuOne Temporary Migration"},
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
	resp, err := defaultHTTPClient.Do(req)
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

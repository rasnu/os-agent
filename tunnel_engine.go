package main

import (
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/hashicorp/yamux"
	"golang.org/x/crypto/acme/autocert"
	"rasnuos-agent/os_adapter"
)

var (
	activeTunnels sync.Map
)

func (c *PusherClient) handleTunnelCommand(action string, args map[string]interface{}) {
	pipelineId := getString(args, "pipeline_id")
	stepId := getString(args, "step_id")
	token := getString(args, "token")

	switch action {
	case "prepare-tunnel-host":
		publicPort := int(getFloat(args, "public_port"))
		domain := getString(args, "domain")
		useNativeSSL := false
		if val, ok := args["use_native_ssl"].(bool); ok {
			useNativeSSL = val
		}
		c.prepareTunnelHost(pipelineId, stepId, token, publicPort, domain, useNativeSSL)
		logAuditEvent(AuditEvent{Action: "tunnel_prepare_host", PipelineID: pipelineId, StepID: stepId, Command: fmt.Sprintf("PublicPort:%d", publicPort), Success: true})
	case "execute-tunnel-client":
		hostIp := getString(args, "host_ip")
		hostControlPort := int(getFloat(args, "host_control_port"))
		fingerprint := getString(args, "fingerprint")
		localPort := int(getFloat(args, "local_port"))
		c.executeTunnelClient(pipelineId, stepId, hostIp, hostControlPort, fingerprint, localPort, token)
		logAuditEvent(AuditEvent{Action: "tunnel_execute_client", PipelineID: pipelineId, StepID: stepId, Command: fmt.Sprintf("Host:%s LocalPort:%d", hostIp, localPort), Success: true})
	case "shutdown-tunnel-host":
		shutdownTunnelHost(token)
		c.sendMigrationCompletion(pipelineId, stepId, true, "Tunnel host shut down", nil)
		logAuditEvent(AuditEvent{Action: "tunnel_shutdown_host", PipelineID: pipelineId, StepID: stepId, Success: true})
	case "shutdown-tunnel-client":
		shutdownTunnelClient(token)
		c.sendMigrationCompletion(pipelineId, stepId, true, "Tunnel client shut down", nil)
		logAuditEvent(AuditEvent{Action: "tunnel_shutdown_client", PipelineID: pipelineId, StepID: stepId, Success: true})
	default:
		c.sendMigrationCompletion(pipelineId, stepId, false, "Unknown tunnel action", nil)
		logAuditEvent(AuditEvent{Action: "tunnel_unknown", PipelineID: pipelineId, StepID: stepId, Success: false, Error: "Unknown tunnel action"})
	}
}

type TunnelServer struct {
	controlListener net.Listener
	publicListener  net.Listener
	session         *yamux.Session
	fingerprint     string
	controlPort     int
	publicPort      int
}

// ---------------------------------------------------------
// HOST / SERVER (Public Vultr Node)
// ---------------------------------------------------------
func (c *PusherClient) prepareTunnelHost(pipelineId, stepId, token string, requestedPublicPort int, domain string, useNativeSSL bool) {
	// 1. Generate mTLS Cert
	cert, _, err := generateSelfSignedCert()
	if err != nil {
		c.sendMigrationCompletion(pipelineId, stepId, false, fmt.Sprintf("Failed to generate cert: %v", err), nil)
		return
	}

	hash := sha256.Sum256(cert.Certificate[0])
	fingerprint := hex.EncodeToString(hash[:])

	// 2. Start mTLS Control Listener (random port)
	controlListener, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		c.sendMigrationCompletion(pipelineId, stepId, false, fmt.Sprintf("Failed to start control listener: %v", err), nil)
		return
	}
	controlPort := controlListener.Addr().(*net.TCPAddr).Port

	// Punch hole in local firewalls for both ports
	os_adapter.Current().PunchFirewallHole(controlPort)
	os_adapter.Current().PunchFirewallHole(requestedPublicPort)

	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{*cert},
	}
	tlsControlListener := tls.NewListener(controlListener, tlsConfig)

	// 3. Start Public Listener
	var publicListener net.Listener
	var sslWarning string

	if useNativeSSL && domain != "" {
		// Use an absolute, OS-aware path for the Let's Encrypt cert cache.
		// A relative path would resolve to the process CWD, which on Windows
		// (SYSTEM account) is C:\Windows\System32 — completely wrong.
		var cacheDir string
		if baseDir, err := os.UserCacheDir(); err == nil {
			cacheDir = filepath.Join(baseDir, "rasnuos-agent", "acme")
		} else {
			// Fallback: use the OS temp directory — always writable.
			cacheDir = filepath.Join(os.TempDir(), "rasnuos-acme")
		}
		os.MkdirAll(cacheDir, 0700)
		log.Printf("[SSL] Using Let's Encrypt cache dir: %s", cacheDir)

		m := &autocert.Manager{
			Prompt:     autocert.AcceptTOS,
			HostPolicy: autocert.HostWhitelist(domain),
			Cache:      autocert.DirCache(cacheDir),
		}

		l, err := net.Listen("tcp", ":443")
		if err == nil {
			publicListener = tls.NewListener(l, m.TLSConfig())
			requestedPublicPort = 443
			log.Printf("Native SSL active on port 443 for domain %s", domain)
		} else {
			sslWarning = fmt.Sprintf("Port 443 is already in use. Native SSL cannot start. Falling back to unencrypted HTTP tunnel on port %d.", requestedPublicPort)
			log.Printf("Native SSL fallback: %v", err)
		}
	}

	if publicListener == nil {
		var err error
		publicListener, err = net.Listen("tcp", fmt.Sprintf("0.0.0.0:%d", requestedPublicPort))
		if err != nil {
			tlsControlListener.Close()
			c.sendMigrationCompletion(pipelineId, stepId, false, fmt.Sprintf("Failed to start public listener on port %d: %v", requestedPublicPort, err), nil)
			return
		}
	}

	actualPublicPort := publicListener.Addr().(*net.TCPAddr).Port

	ts := &TunnelServer{
		controlListener: tlsControlListener,
		publicListener:  publicListener,
		fingerprint:     fingerprint,
		controlPort:     controlPort,
		publicPort:      actualPublicPort,
	}
	activeTunnels.Store(token, ts)

	// Accept a single mTLS connection for the yamux control session
	go func() {
		defer shutdownTunnelHost(token)

		var conn net.Conn
		var err error
		var session *yamux.Session
		for {
			conn, err = tlsControlListener.Accept()
			if err != nil {
				log.Printf("mTLS Tunnel control accept error: %v", err)
				continue
			}

			tlsConn := conn.(*tls.Conn)
			tlsConn.SetDeadline(time.Now().Add(10 * time.Second))

			// 1. Force TLS Handshake
			if err := tlsConn.Handshake(); err != nil {
				log.Printf("mTLS handshake failed from %s: %v", conn.RemoteAddr(), err)
				conn.Close()
				continue
			}

			// 2. Read Authentication Token (64 bytes)
			tokenBuf := make([]byte, 64)
			if _, err := io.ReadFull(tlsConn, tokenBuf); err != nil {
				log.Printf("UNAUTHORIZED mTLS TUNNEL CONNECTION ATTEMPT from %s: Failed to read auth token: %v", conn.RemoteAddr(), err)
				conn.Close()
				continue
			}

			if string(tokenBuf) != token {
				log.Printf("UNAUTHORIZED mTLS TUNNEL CONNECTION ATTEMPT from %s: Invalid token provided.", conn.RemoteAddr())
				conn.Close()
				continue
			}

			tlsConn.SetDeadline(time.Time{})

			session, err = yamux.Server(conn, yamux.DefaultConfig())
			if err != nil {
				log.Printf("Failed to setup yamux server, continuing to listen: %v", err)
				conn.Close()
				continue
			}
			break // Successfully authenticated and established yamux session
		}

		ts.session = session

		// Now we can accept public connections
		for {
			pubConn, err := ts.publicListener.Accept()
			if err != nil {
				return
			}
			go func(pc net.Conn) {
				defer pc.Close()

				if ts.session == nil || ts.session.IsClosed() {
					log.Printf("No active yamux session to handle public traffic")
					return
				}

				stream, err := ts.session.Open()
				if err != nil {
					log.Printf("Failed to open yamux stream: %v", err)
					return
				}
				defer stream.Close()

				proxyConn(pc, stream)
			}(pubConn)
		}
	}()

	data := map[string]interface{}{
		"control_port": controlPort,
		"public_port":  actualPublicPort,
		"fingerprint":  fingerprint,
	}
	if sslWarning != "" {
		data["ssl_warning"] = sslWarning
	}
	c.sendMigrationCompletion(pipelineId, stepId, true, "Tunnel host ready", data)
}

func shutdownTunnelHost(token string) {
	if val, ok := activeTunnels.Load(token); ok {
		ts := val.(*TunnelServer)
		if ts.session != nil {
			ts.session.Close()
		}
		if ts.controlListener != nil {
			ts.controlListener.Close()
		}
		if ts.publicListener != nil {
			ts.publicListener.Close()
		}

		os_adapter.Current().CloseFirewallHole(ts.controlPort)
		os_adapter.Current().CloseFirewallHole(ts.publicPort)
		activeTunnels.Delete(token)
	}
}

// ---------------------------------------------------------
// CLIENT (MacBook behind CGNAT)
// ---------------------------------------------------------
type TunnelClient struct {
	session *yamux.Session
}

func (c *PusherClient) executeTunnelClient(pipelineId, stepId, hostIp string, hostControlPort int, fingerprint string, localPort int, token string) {
	tlsConfig := &tls.Config{
		InsecureSkipVerify: true,
		VerifyPeerCertificate: func(rawCerts [][]byte, verifiedChains [][]*x509.Certificate) error {
			if len(rawCerts) == 0 {
				return fmt.Errorf("no certificates provided")
			}
			hash := sha256.Sum256(rawCerts[0])
			presentedFingerprint := hex.EncodeToString(hash[:])
			if presentedFingerprint != fingerprint {
				return fmt.Errorf("certificate fingerprint mismatch")
			}
			return nil
		},
	}

	addr := fmt.Sprintf("%s:%d", hostIp, hostControlPort)
	dialer := &net.Dialer{Timeout: 30 * time.Second}
	conn, err := tls.DialWithDialer(dialer, "tcp", addr, tlsConfig)
	if err != nil {
		c.sendMigrationCompletion(pipelineId, stepId, false, fmt.Sprintf("Failed to connect to host: %v", err), nil)
		return
	}

	// Authenticate by sending the 64-byte token
	conn.SetDeadline(time.Now().Add(10 * time.Second))
	if _, err := conn.Write([]byte(token)); err != nil {
		conn.Close()
		c.sendMigrationCompletion(pipelineId, stepId, false, fmt.Sprintf("Failed to send auth token: %v", err), nil)
		return
	}
	conn.SetDeadline(time.Time{})

	session, err := yamux.Client(conn, yamux.DefaultConfig())
	if err != nil {
		conn.Close()
		c.sendMigrationCompletion(pipelineId, stepId, false, fmt.Sprintf("Failed to start yamux client: %v", err), nil)
		return
	}

	tc := &TunnelClient{session: session}
	activeTunnels.Store(token, tc)

	go func() {
		defer shutdownTunnelClient(token)
		for {
			stream, err := session.Accept()
			if err != nil {
				if err != io.EOF {
					log.Printf("Yamux session accept error: %v", err)
				}
				return
			}
			go func(s net.Conn) {
				defer s.Close()
				localConn, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", localPort))
				if err != nil {
					log.Printf("Failed to dial local port %d: %v", localPort, err)
					return
				}
				defer localConn.Close()
				proxyConn(s, localConn)
			}(stream)
		}
	}()

	c.sendMigrationCompletion(pipelineId, stepId, true, "Tunnel client connected", nil)
}

func shutdownTunnelClient(token string) {
	if val, ok := activeTunnels.Load(token); ok {
		tc := val.(*TunnelClient)
		if tc.session != nil {
			tc.session.Close()
		}
		activeTunnels.Delete(token)
	}
}

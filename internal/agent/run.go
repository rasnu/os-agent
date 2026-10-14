// Copyright © 2026 Rasnu LLC (rasnu.com). All rights reserved.
// Licensed under the Functional Source License, Version 1.1, ALv2 Future License (FSL-1.1-ALv2).

package agent

import (
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"syscall"

	"rasnuos-agent/internal/buildinfo"
	"rasnuos-agent/internal/observability"
	"rasnuos-agent/internal/transport"

	"rasnuos-agent/internal/osadapter"

	"gopkg.in/natefinch/lumberjack.v2"
)

// firstNonEmpty prefers an explicit CLI flag but falls back to the environment so that
// production installs can source secrets from a chmod-600 EnvironmentFile instead of
// exposing them in `ps aux` (CLI args) — see deploy/rasnuos-agent.service.
func firstNonEmpty(flagValue string, envKeys ...string) string {
	if flagValue != "" {
		return flagValue
	}
	for _, key := range envKeys {
		if v := os.Getenv(key); v != "" {
			return v
		}
	}
	return ""
}

func buildServerURL(flagValue string) string {
	if flagValue != "" {
		return flagValue
	}
	scheme := firstNonEmpty("", "REVERB_SCHEME")
	if scheme == "" {
		scheme = "ws"
	} else if scheme == "https" {
		scheme = "wss"
	} else if scheme == "http" {
		scheme = "ws"
	}
	host := firstNonEmpty("", "REVERB_HOST")
	if host == "" {
		host = "localhost"
	}
	port := firstNonEmpty("", "REVERB_PORT")
	if port == "" {
		port = "8080"
	}
	appKey := firstNonEmpty("", "REVERB_APP_KEY")
	if appKey == "" {
		appKey = "rasnuos-key"
	}

	// Omit port from the URL when it's the default for the scheme.
	// When the agent connects via wss:// (behind Cloudflare/nginx), including
	// port 8080 would bypass the reverse proxy and hit Reverb directly over
	// an unencrypted socket, causing broken pipe errors.
	hostPort := fmt.Sprintf("%s:%s", host, port)
	if (scheme == "wss" && port == "443") || (scheme == "ws" && port == "80") {
		hostPort = host
	}

	return fmt.Sprintf("%s://%s/app/%s?protocol=7&client=rasnuos-agent&version=1.0.0", scheme, hostPort, appKey)
}

// runAgent contains the full agent lifecycle. It accepts an external shutdown
// channel so it can be controlled by both OS signals (Linux/macOS/direct run)
// and the Windows SCM stop handler (windows_service.go).
func runAgent(args []string, shutdown chan struct{}) error {
	logFilePath := osadapter.Current().LogFilePath()
	if logFilePath != "" {
		lumberjackLogger := &lumberjack.Logger{
			Filename:   logFilePath,
			MaxSize:    50,
			MaxBackups: 3,
			MaxAge:     30,
			Compress:   true,
		}
		multiWriter := io.MultiWriter(os.Stdout, lumberjackLogger)
		log.SetOutput(multiWriter)
	}

	fs := flag.NewFlagSet("rasnuos-agent", flag.ContinueOnError)
	serverURLFlag := fs.String("server", "", "Reverb WebSocket URL (falls back to REVERB_SCHEME/REVERB_HOST/REVERB_PORT/REVERB_APP_KEY env vars)")
	apiURLFlag := fs.String("api", "", "RasnuOS Control Plane API URL (falls back to API_URL env var)")
	tokenFlag := fs.String("token", "", "Agent Authentication Token")
	serverIdFlag := fs.String("server-id", "", "ID of the Server in RasnuOS")
	tlsPinFlag := fs.String("tls-pin", "", "Comma-separated list of SHA-256 SPKI hashes for TLS certificate pinning (falls back to TLS_PIN env var)")
	readOnlyFlag := fs.Bool("read-only", false, "Run agent in read-only mode, disabling shell execution, PTY, and file/service mutations (falls back to AGENT_READ_ONLY=true)")
	versionFlag := fs.Bool("version", false, "Print version and exit")
	keyInfoFlag := fs.Bool("key-info", false, "Print update public key status and exit")

	if err := fs.Parse(args); err != nil {
		return err
	}

	if *versionFlag {
		fmt.Printf("rasnuos-agent %s\n", buildinfo.Version)
		return nil
	}

	if *keyInfoFlag {
		pubKey, err := getAgentPublicKey()
		if err != nil {
			fmt.Printf("Update Public Key: unconfigured (%v)\n", err)
			return err
		}
		fmt.Printf("Update Public Key: configured (%s)\n", AgentPublicKeyBase64)
		_ = pubKey
		return nil
	}

	serverURL := buildServerURL(*serverURLFlag)
	apiURL := firstNonEmpty(*apiURLFlag, "API_URL")
	if apiURL == "" {
		apiURL = "http://localhost:8000/api"
	}
	token := firstNonEmpty(*tokenFlag, "BEARER_TOKEN")
	serverId := firstNonEmpty(*serverIdFlag, "SERVER_ID")
	tlsPin := firstNonEmpty(*tlsPinFlag, "TLS_PIN")

	if token == "" || serverId == "" {
		return fmt.Errorf("--token/--server-id (or BEARER_TOKEN/SERVER_ID env vars) are required")
	}

	log.Printf("Starting RasnuOS Agent %s", buildinfo.Version)
	log.Printf("Target Server ID: %s", serverId)

	if tlsPin != "" {
		log.Printf("TLS Certificate Pinning is ENABLED.")
		transport.ApplyTLSPin(tlsPin)
	} else {
		log.Printf("TLS Certificate Pinning is DISABLED.")
	}

	client := NewPusherClient(serverURL, apiURL, token, serverId, tlsPin)
	if *readOnlyFlag {
		client.ReadOnly = true
	}
	if client.ReadOnly {
		log.Printf("Agent is running in READ-ONLY mode (--read-only / AGENT_READ_ONLY=true).")
	}
	go client.ConnectAndMonitor(shutdown)
	go client.StartMetricsEngine(shutdown)
	go CleanupIdleSessions(shutdown)
	observability.StartMetricsServer(shutdown)

	<-shutdown

	log.Printf("Shutdown signal received, stopping agent...")
	client.CancelAllCommands()
	client.WaitCommands()
	log.Println("Agent stopped.")
	return nil
}

func run(args []string) error {
	shutdown := make(chan struct{})

	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(interrupt)
	go func() {
		sig := <-interrupt
		if sig != nil {
			log.Printf("Received %s, shutting down agent...", sig)
			close(shutdown)
		}
	}()

	return runAgent(args, shutdown)
}

// Main is the process entry point used by cmd/rasnuos-agent. On Windows it hands off to
// the Service Control Manager when started as a service; otherwise it runs in the foreground.
func Main() {
	if isRunningAsWindowsService() {
		if err := runAsWindowsService(); err != nil {
			log.Fatalf("Service exited with error: %v", err)
		}
		return
	}

	if err := run(os.Args[1:]); err != nil {
		log.Fatal(err)
	}
}

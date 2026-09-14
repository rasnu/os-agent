/**
 * Project: RasnuOS
 * Copyright © 2026 Rasnu LLC (rasnu.com). All rights reserved.
 */
package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"syscall"

	"rasnuos-agent/os_adapter"

	"gopkg.in/natefinch/lumberjack.v2"
)

// version is set at build time via -ldflags "-X main.version=x.y.z".
// Falls back to "dev" for local builds.
var version = "dev"

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

	return fmt.Sprintf("%s://%s:%s/app/%s?protocol=7&client=rasnuos-agent&version=1.0.0", scheme, host, port, appKey)
}

// runAgent contains the full agent lifecycle. It accepts an external shutdown
// channel so it can be controlled by both OS signals (Linux/macOS/direct run)
// and the Windows SCM stop handler (windows_service.go).
func runAgent(args []string, shutdown chan struct{}) error {
	logFilePath := os_adapter.Current().LogFilePath()
	if logFilePath != "" {
		lumberjackLogger := &lumberjack.Logger{
			Filename:   logFilePath,
			MaxSize:    50, // megabytes
			MaxBackups: 3,
			MaxAge:     30,   // days
			Compress:   true, // disabled by default
		}
		multiWriter := io.MultiWriter(os.Stdout, lumberjackLogger)
		log.SetOutput(multiWriter)
	}

	fs := flag.NewFlagSet("rasnuos-agent", flag.ContinueOnError)
	serverURLFlag := fs.String("server", "", "Reverb WebSocket URL (falls back to REVERB_SCHEME/REVERB_HOST/REVERB_PORT/REVERB_APP_KEY env vars)")
	apiURLFlag := fs.String("api", "", "RasnuOS Laravel API URL (falls back to API_URL env var)")
	tokenFlag := fs.String("token", "", "Agent Authentication Token")
	serverIdFlag := fs.String("server-id", "", "ID of the Server in RasnuOS")
	tlsPinFlag := fs.String("tls-pin", "", "Comma-separated list of SHA-256 SPKI hashes for TLS certificate pinning (falls back to TLS_PIN env var)")
	versionFlag := fs.Bool("version", false, "Print version and exit")

	if err := fs.Parse(args); err != nil {
		return err
	}

	if *versionFlag {
		fmt.Printf("rasnuos-agent %s\n", version)
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

	log.Printf("Starting RasnuOS Agent %s", version)
	log.Printf("Target Server ID: %s", serverId)

	if tlsPin != "" {
		log.Printf("TLS Certificate Pinning is ENABLED.")
		applyTLSPinToDefaultTransport(tlsPin)
	} else {
		log.Printf("TLS Certificate Pinning is DISABLED.")
	}

	client := NewPusherClient(serverURL, apiURL, token, serverId, tlsPin)
	go client.ConnectAndMonitor(shutdown)
	go client.StartMetricsEngine(shutdown)
	go CleanupIdleSessions(shutdown)

	<-shutdown

	log.Printf("Shutdown signal received, stopping agent...")
	client.WaitCommands()
	log.Println("Agent stopped.")
	return nil
}

// run is the entry point for non-service (direct / CLI) execution. It wires
// OS signals into the shared runAgent() shutdown channel.
func run(args []string) error {
	shutdown := make(chan struct{})

	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt, syscall.SIGTERM)
	go func() {
		sig := <-interrupt
		log.Printf("Received %s, shutting down agent...", sig)
		close(shutdown)
	}()

	return runAgent(args, shutdown)
}

func main() {
	// On Windows, detect whether we were started by SCM and hand off to the
	// service runner. This is a no-op on Linux/macOS (isRunningAsWindowsService
	// is defined to always return false on non-Windows builds).
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


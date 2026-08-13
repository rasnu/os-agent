// Copyright © 2026 Rasnu LLC (rasnu.com). All rights reserved.
// Licensed under the Functional Source License, Version 1.1, ALv2 Future License (FSL-1.1-ALv2).

// Package agent is the core of the RasnuOS agent. It keeps a persistent, outbound-only
// Pusher/Reverb WebSocket to the control plane and dispatches the events it receives:
// command execution with live output streaming, file management, PTY sessions,
// server migrations, CGNAT tunnels, service/process control, cron sync, metrics
// heartbeats, log anomaly tailing and Ed25519-verified self-updates.
//
// The process entry point is Main (see cmd/rasnuos-agent).
package agent

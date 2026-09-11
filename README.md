# RasnuOne Agent

[![Go Version](https://img.shields.io/badge/Go-1.21+-00ADD8.svg)](https://go.dev/)
[![License](https://img.shields.io/badge/license-MIT-green.svg)](LICENSE)
[![Build](https://img.shields.io/badge/build-passing-brightgreen.svg)]()
[![Platforms](https://img.shields.io/badge/platforms-Linux%20%7C%20macOS%20%7C%20Windows-blue.svg)]()

The **RasnuOne Agent** is a lightweight, open-source daemon written in Go. It is installed on your server and acts as the secure bridge between your infrastructure and the RasnuOne management dashboard. It connects outbound to our WebSocket server, receives instructions, executes them on your machine, and streams the results back in real time.

**This repository is fully open-source so you can audit every line of code before putting it on your server.** We believe no one should install a black-box binary with root access without being able to read exactly what it does. Everything described in this document is implemented in the code you are looking at right now — nothing more, nothing less.

---

## Table of Contents

- [How It Works](#how-it-works)
- [Security Architecture](#security-architecture)
- [Secret Protection](#secret-protection)
- [Core Features](#core-features)
  - [Command Execution and Streaming](#1-command-execution-and-streaming)
  - [Interactive Terminal (PTY)](#2-interactive-terminal-pty)
  - [File Manager](#3-file-manager)
  - [Server Migration Engine](#4-server-migration-engine)
  - [Real-Time Metrics](#5-real-time-metrics)
  - [Service and Process Management](#6-service-and-process-management)
  - [Self-Update with Signature Verification](#7-self-update-with-signature-verification)
- [Configuration](#configuration)
- [Building from Source](#building-from-source)
- [Running Tests](#running-tests)
- [License](#license)

---

## How It Works

When the agent starts, it does three things concurrently:

1. **Connects to the RasnuOne Reverb WebSocket server** and subscribes to three private, server-specific channels: a general command channel, an OS management channel, and a dedicated terminal channel.
2. **Starts a metrics engine** that collects CPU, RAM, disk, swap, network I/O, running processes, and service states every 15 seconds, then ships that data to the dashboard.
3. **Starts an idle terminal session cleaner** that closes any PTY session that has been inactive for more than 30 minutes.

The agent never opens any listening port for incoming connections. All communication is agent-initiated and outbound-only. When the dashboard wants your server to do something, it pushes an event over the existing WebSocket connection that the agent already has open.

On shutdown (SIGTERM or Ctrl+C), the agent stops accepting new commands, waits for all in-progress commands to finish, then exits cleanly.

---

## Security Architecture

Security is not an afterthought here. The agent was designed with a layered, defense-in-depth approach. Here is exactly what is in place and how each piece works.

### Outbound-Only Connection

The agent establishes a WebSocket connection to the Reverb server on startup and holds it open. It does not listen on any TCP port for incoming traffic. The only exception is the [Server Migration Engine](#4-server-migration-engine), which opens a temporary TLS listener for the duration of an active server-to-server data transfer and closes it immediately when done.

### Private Channel Authentication

After the WebSocket handshake succeeds, the agent receives a `socket_id` from the server. Before it can subscribe to any channel, it must authenticate each one by posting that `socket_id` and the channel name to `/api/agent/broadcasting/auth` with its Bearer Token. The server validates the token and returns a signed `auth` string. The agent sends this auth string with the subscribe request. No subscription is accepted without this server-signed token. The three channels subscribed are always scoped to the specific server's ID:

- `private-server.<server_id>`
- `private-server.<server_id>.commands`
- `private-server.<server_id>.terminal`

### Bearer Token Authentication

Every outbound HTTP request the agent makes — whether posting metrics, streaming log output, reporting command completion, or authenticating channel subscriptions — includes the `Authorization: Bearer <token>` header. The token is a secret issued by the dashboard when the server is registered, and the server validates it on every single API call.

### Secrets Never Exposed in Process List

The agent reads its configuration from environment variables, not command-line flags. On Linux, these live in a `chmod 600` environment file that only root can read. This design means the token and other secrets are never visible in the output of `ps aux`, where any user on the machine could read them. If you do pass a flag like `--token` on the command line, it still works, but the recommended production setup uses environment variables sourced from a locked-down file.

### TLS Certificate Pinning

If you pass a `--tls-pin` flag (or set `TLS_PIN` in the environment), the agent applies SHA-256 SPKI pinning to **every** outbound HTTP and WebSocket connection. Pinning is implemented in `tls_pin.go`:

- `InsecureSkipVerify` is set to `false` — standard certificate chain validation still runs first.
- After the chain is verified, the agent hashes the `SubjectPublicKeyInfo` of every certificate in every verified chain using SHA-256 and checks whether any of those hashes match the configured pin list.
- If nothing matches, the connection is refused with an explicit "certificate pinning failure" error.
- The pins support an optional `sha256/` prefix for compatibility.

Without this flag the agent uses the system's default TLS trust store and the connection is still encrypted, just not pinned.

### Concurrency Limit

A buffered channel of size 10 acts as a semaphore for pipeline command execution. When a new `ExecuteCommand` event arrives, the agent tries to acquire a slot non-blockingly. If all 10 slots are taken, the command is immediately rejected and the dashboard is notified with a clear error message. This prevents a flood of queued commands from exhausting the server's CPU or memory.

### Input Validation Against Injection

Service names passed to `restart-service`, `start-service`, and `stop-service` are validated against the pattern `^[a-zA-Z0-9._@\-]+$` before they are passed to `systemctl`. Any name that does not match this pattern is rejected with an error. Similarly, process IDs passed to `kill-process` are validated against `^[0-9]+$`. Neither of these values is ever interpolated into a shell string — they are passed as discrete arguments to `exec.Command`.

### Panic Recovery

Every goroutine that runs user-triggered work — command execution, metrics collection, PTY sessions, the connection monitor — is wrapped in a `recover()` call. An unexpected panic in one area does not crash the entire agent process.

---

## Secret Protection

This is the part most people care most about, so it deserves its own section.

**Your secrets and file contents never pass through our servers.**

Here is precisely how that works:

**Secrets in commands are redacted before logging.** When the agent receives a command to execute, it runs it through `redactSecrets()` in `executor.go` before writing anything to the log file. This function applies four regular expressions that detect and replace the values of common credential patterns:

- `mysql -p<value>` or `-p'<value>'`
- `--password=<value>` or `--password <value>`
- `redis-cli -a <value>`
- Environment variable assignments matching `*PASS*`, `*PWD*`, `*SECRET*`, or `*AUTH*` (e.g. `MYSQL_PASSWORD=secret` becomes `MYSQL_PASSWORD=***REDACTED***`)

The redacted version is what goes into the log file and what is sent back to the dashboard. The actual command is executed by the OS using Go's `exec.Command`, which receives the full, unredacted string — that string is never stored or transmitted anywhere.

**File reads over WebSocket are base64-encoded, not stored.** When you use the file browser to read a file, the agent reads the file from disk, base64-encodes the content in memory, and sends it directly back to the dashboard over the authenticated WebSocket. The content is never written to any intermediate location on disk.

**File transfers use one-time tokens.** The `upload-file-to-dashboard` and `download-file-from-dashboard` commands use a short-lived token embedded in the transfer URL. The agent constructs the URL as `<api_url>/agent/file/transfer/<token>` and streams the bytes directly. The binary content is routed to its destination and is not stored in any server-side log.

**File integrity is verified with SHA-256.** On uploads, the agent computes a SHA-256 hash of the entire file before transmission and sends it in the `X-File-Hash` header. On downloads, if the server provides an `X-File-Hash` header, the agent re-hashes the file after writing it to disk. If the hashes do not match, the downloaded file is deleted and an error is reported. Nothing is silently accepted.

**Migration credentials never leave the two servers involved.** During a server-to-server migration, a 32-character random hex rsync password is generated on the receiver using `crypto/rand`, written to a `chmod 600` secrets file in `os.TempDir()`, and sent back to the dashboard over the authenticated API — not stored anywhere permanently. The TLS certificate used for the migration tunnel is a freshly generated ephemeral ECDSA P-256 certificate that is only valid for 24 hours and only exists for the duration of the migration.

---

## Core Features

### 1. Command Execution and Streaming

When the dashboard triggers a pipeline step, the agent receives an `ExecuteCommand` WebSocket event containing the command string and an optional `run_as_user` field.

The agent runs the command using `bash -c <command>` on Linux/macOS via the `os_adapter`. If `run_as_user` is set, on Linux the command is executed as `su -s /bin/bash <user> -c <command>`, allowing unprivileged execution without giving the dashboard direct SSH access to that user.

Output from both STDOUT and STDERR is read in parallel using two goroutines and fed into a `StreamBatcher`. The batcher accumulates chunks and flushes them:

- **Every 50 milliseconds** over the WebSocket as `client-log` events for real-time output in the dashboard.
- **Every 1 second** over HTTP to `/api/agent/stream` as a backup, ensuring output is persisted even if the WebSocket is momentarily unavailable.

Commands have a hard timeout of **1 hour**. Any command can be cancelled mid-execution by the dashboard sending a `CancelCommand` event with the matching `step_id`. When cancelled, Go's `context.WithTimeout` triggers, which sends a kill signal to the running process.

The scanner buffer is set to 1 MB to correctly handle long lines such as base64-encoded output without truncating.

When the command finishes, the agent posts the final aggregated log and a success/failure flag to `/api/agent/complete`.

### 2. Interactive Terminal (PTY)

When you open a terminal in the dashboard, the agent receives a `client-TerminalStart` event and spawns a native Pseudo-Terminal (PTY) using the `creack/pty` library on Linux and macOS.

- The shell is started with `TERM=xterm-256color` so terminal programs render correctly.
- Terminal output is read in a loop with a 4096-byte buffer and sent back to the dashboard over the WebSocket as `client-TerminalOutput` events in the `private-server.<server_id>.terminal` channel.
- Input (`client-TerminalInput`) is written directly to the PTY's file descriptor.
- Resize events (`client-TerminalResize`) call `pty.Setsize` with the new column and row counts, so programs that react to `SIGWINCH` (like `vim` or `htop`) respond correctly.
- A background goroutine runs every 60 seconds and closes any session that has had no activity for 30 minutes.
- On agent shutdown, all active PTY sessions are closed immediately.

Multiple concurrent terminal sessions are supported. Each session is tracked by a unique `term_id` in a thread-safe map protected by a mutex.

### 3. File Manager

The file manager handles all filesystem operations requested through the dashboard's file browser. Every operation requires an absolute path — relative paths are rejected outright. Paths are also cleaned with `filepath.Clean` to eliminate any `..` traversal sequences before any OS call is made.

Supported operations:

| Action | What it does |
|---|---|
| `list-directory` | Returns up to 2000 entries with name, size, mode string, modification time (Unix timestamp), and whether the entry is a directory or symlink. For symlinks, the target is stat'd to determine if it resolves to a directory. |
| `read-file` | Reads a regular file (rejects devices, pipes, sockets) up to 5 MB and returns the content as a base64 string for safe JSON transport. |
| `write-file` | Accepts base64-encoded content and an optional octal mode string (defaults to `0644`). Decodes the content in memory and writes it to the given path. |
| `delete-file` | Removes a single file. |
| `download-file` | Downloads a file from a given URL to the specified path. |
| `upload-file` | Streams a file from the server to a given URL using multipart form encoding. |
| `chmod-path` | Changes file permissions via the OS adapter. |
| `chown-path` | Changes file ownership via the OS adapter. |
| `create-directory` | Creates a new directory. |
| `rename-path` | Renames or moves a file or directory. |
| `delete-path` | Removes a file or directory. |

All operations report success or failure back to the dashboard via `/api/agent/file/complete`.

### 4. Server Migration Engine

The migration engine allows moving data directly from one server to another without routing the actual file content through our platform. The process works as follows:

**On the destination server (receiver):**

1. An ephemeral ECDSA P-256 self-signed TLS certificate is generated in memory using `crypto/rand`. It is valid for 24 hours.
2. A 32-character cryptographically random hex password is generated using `crypto/rand` and written to a `chmod 600` temporary file for rsync to use.
3. An rsync daemon is started on a loopback port (`127.0.0.1:<random_port>`) so it is not accessible externally.
4. A TLS listener is opened on `0.0.0.0` at the requested port to accept the incoming connection from the source server.
5. Connections arriving on the TLS listener are proxied through to the local rsync daemon.
6. The port number, TLS certificate SHA-256 fingerprint, and rsync password are sent back to the dashboard, which forwards them to the source server.
7. A 4-hour hard timeout automatically shuts down the migration server if it is never explicitly stopped.

**On the source server (sender):**

1. The sender receives the destination host, port, certificate fingerprint, and rsync password from the dashboard.
2. It opens a local TCP listener on a loopback port and sets up a proxy that connects to the destination over TLS, verifying the certificate's SHA-256 fingerprint byte-for-byte before forwarding any data.
3. It runs `rsync -avz --progress` pointed at the local loopback proxy, with the rsync password injected via the `RSYNC_PASSWORD` environment variable (never as a CLI flag).
4. Rsync retries up to 5 times with a 2-second delay between attempts.

The result: your file data travels directly between the two servers over an encrypted, fingerprint-verified tunnel. It never passes through RasnuOne servers.

### 5. Real-Time Metrics

A background goroutine ticks every 15 seconds and collects a full system snapshot using the `gopsutil` library:

| Metric | Source |
|---|---|
| CPU usage % | `gopsutil/cpu` |
| RAM usage % | `gopsutil/mem` (virtual memory) |
| Disk usage % | `gopsutil/disk` (root partition `/`) |
| Swap usage % | `gopsutil/mem` (swap memory) |
| Load average (1m/5m/15m) | `gopsutil/load` |
| Network RX/TX (bytes) | `gopsutil/net` (all interfaces combined) |
| Disk read/write (KB) | `gopsutil/disk` (I/O counters, all devices) |
| Top 20 processes by CPU | `gopsutil/process` |
| Service statuses | `systemctl is-active <service>` for a curated list |

The curated service list checked on every metrics cycle includes: `nginx`, `apache2`, `caddy`, `litespeed`, `mysql`, `mariadb`, `postgresql`, `mongod`, `redis-server`, `memcached`, `docker`, `containerd`, `supervisor`, `pm2`, `php-fpm`, all `php<version>-fpm` variants from 8.0 to 8.4, `ufw`, `fail2ban`, `ssh`, `cron`, and `crond`.

Collected snapshots are queued in memory. If the network is unavailable, they are retained and flushed in order when connectivity returns, up to a maximum of 1000 queued items (the oldest is dropped when this limit is reached). To prevent memory bloat during long outages, only the most recent item in the queue retains the full process and service arrays; older items have those arrays cleared, keeping memory use at a few kilobytes instead of potentially hundreds of megabytes.

Heartbeats are also sent periodically to `/api/agent/heartbeat`, reporting the server ID, agent version, operating system type, and CPU architecture.

### 6. Service and Process Management

The agent can manage OS-level services and processes on demand:

- **`restart-service` / `start-service` / `stop-service`**: Passes the service name to `systemctl <action> <service>` via the OS adapter after validating the name matches `^[a-zA-Z0-9._@\-]+$`. Invalid names are rejected before any system call is made.
- **`kill-process`**: Validates the PID is a pure numeric string matching `^[0-9]+$`, then delegates to the OS adapter's `KillProcess` implementation. On Linux this sends `SIGTERM` followed by `SIGKILL` after 2 seconds.
- **`sync-cron-jobs`**: Accepts a cron job content string and writes it through the OS adapter's `WriteCronJobs` implementation, which handles the platform-specific crontab installation.

### 7. Self-Update with Signature Verification

The agent can update itself when instructed by the dashboard via an `ExecuteSelfUpdate` event. The update process is designed to fail closed — a tampered or unsigned binary will never be installed.

**The update flow:**

1. The new binary is downloaded to a temporary file named `<current_executable>.tmp` with `0600` permissions (preventing other processes from reading or replacing it during download).
2. Download size is capped at **100 MB** using `io.LimitReader` to prevent disk exhaustion attacks.
3. If the dashboard provides a base64-encoded Ed25519 signature:
   - The agent decodes the embedded public key and the provided signature.
   - It verifies the Ed25519 signature against the full downloaded binary using `crypto/ed25519`.
   - If verification fails, the update is aborted with a "SECURITY ALERT" message and the temp file is deleted.
   - If the agent was compiled without a real public key (dev mode) and a signature is provided, the update is also aborted — providing a signature to a keyless build is treated as an error, not a pass.
4. If the agent is in production mode (has a real embedded key) and no signature is provided, the update is rejected. Unsigned updates are never allowed in production.
5. The binary is marked executable (`chmod 0755`).
6. The OS adapter's `VerifyBinary` is called to confirm the downloaded file actually runs before the old binary is overwritten.
7. The old binary is replaced using the OS adapter's `ReplaceBinary`, which on Linux performs an atomic rename.
8. After a 2-second delay (to allow the completion message to be sent), the agent restarts itself via the OS adapter.

---

## Configuration

The agent reads configuration from CLI flags, falling back to environment variables. The recommended production approach is to use only environment variables, stored in a file readable only by root.

| Flag | Environment Variable | Description |
|---|---|---|
| `--server` | `REVERB_SCHEME`, `REVERB_HOST`, `REVERB_PORT`, `REVERB_APP_KEY` | The Reverb WebSocket URL. The flag accepts the full URL; the env vars are combined to build it. |
| `--api` | `API_URL` | The base URL of the RasnuOne Laravel API. |
| `--token` | `BEARER_TOKEN` | The agent's authentication token issued by the dashboard. **Required.** |
| `--server-id` | `SERVER_ID` | The server's ID in the RasnuOne platform. **Required.** |
| `--tls-pin` | `TLS_PIN` | Comma-separated SHA-256 SPKI hashes for TLS certificate pinning. Optional. |
| `--version` | — | Prints the agent version and exits. |

**Why environment variables instead of flags?** Arguments passed to a process are visible to every user on the machine via `ps aux`. Environment variables sourced from a `chmod 600` file are only accessible to the process and root. The agent explicitly prefers environment variables over flags to keep your token off the process list.

---

## Building from Source

The agent has almost no external dependencies — the only required library for the core WebSocket logic is `gorilla/websocket`. Everything else uses the Go standard library.

**Prerequisites:** Go 1.21 or higher.

```bash
# Clone the repository
git clone https://github.com/rasnu/rasnuone-agent.git
cd rasnuone-agent

# Download dependencies
go mod tidy

# Build for your current platform
go build -o rasnuone-agent .
```

To build all six platform variants at once, use the provided script:

```bash
# Builds Linux AMD64/ARM64, Windows AMD64/ARM64, macOS AMD64/ARM64
# and generates a SHA-256 checksum file for each binary
bash build-all.sh
```

Binaries are placed in `../public/agent-binaries/`. Each binary is built with `-ldflags="-s -w"` to strip debug symbols and reduce file size.

### Platform Support Matrix

| Platform | Architecture |
|---|---|
| Linux | AMD64, ARM64 |
| Windows | AMD64, ARM64 |
| macOS | AMD64 (Intel), ARM64 (Apple Silicon M1/M2/M3) |

---

## Running Tests

The test suite covers the WebSocket routing logic, command execution pipeline, file manager operations (including upload/download), the metrics engine, the updater's signature verification, and the PTY session lifecycle.

```bash
# Run all tests
go test ./...

# Run with verbose output to see each test case
go test -v ./...

# Run with coverage report
go test -cover ./...

# Generate an interactive HTML coverage report
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out -o coverage.html
```

To run a specific test or group of tests:

```bash
# Run only the file manager tests
go test -v -run TestFileManager ./...

# Run only the updater signature tests
go test -v -run TestSignature ./...
```

---

## License and Copyright

Project: **RasnuOne**  
Copyright © 2026 Rasnu LLC (rasnu.com). All rights reserved.

This agent is open-sourced under the MIT License to ensure complete transparency for our infrastructure management community. You are free to audit, fork, and compile it yourself.

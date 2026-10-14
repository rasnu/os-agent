# Security Policy

`rasnuos-agent` runs as a privileged host daemon on infrastructure managed by [RasnuOS](https://os.rasnu.com). We treat every security report affecting transport integrity, command execution guardrails, file system isolation, or binary update verification with the highest priority.

---

## Supported Versions

Only the latest `1.0.x` release line receives active security patches and signed over-the-air (OTA) binary updates. Hosts running outdated agent versions below `RASNUOS_MIN_AGENT_VERSION` receive `HTTP 426 Upgrade Required` from the control plane.

| Version | Supported | Ed25519 Signed Releases |
| :--- | :---: | :---: |
| `1.0.x` | Yes | Yes |
| `< 1.0.0` | No | No |

---

## Reporting a Vulnerability

> [!IMPORTANT]
> **Do not open public GitHub issues or pull requests for suspected security vulnerabilities.**

Please report security vulnerabilities privately through one of the following channels:

1. **Email:** Send a detailed report to **[security@rasnu.com](mailto:security@rasnu.com)**.
2. **GitHub Private Vulnerability Reporting:** Use the [Report a vulnerability](https://github.com/rasnu/os-agent/security/advisories/new) form under the repository's **Security** tab.

### What to Include
- Affected component and source file (e.g., `internal/guardrails/guardrails.go`, `internal/agent/updater.go`, `internal/osadapter/conpty_windows.go`).
- Target operating system and architecture (`linux/amd64`, `darwin/arm64`, `windows/amd64`, etc.).
- Step-by-step reproduction instructions or a minimal proof-of-concept test case.
- Assessment of impact (e.g., guardrail bypass, symlink traversal, privilege escalation, denial of service).

### Response Timeline (SLA)
| Milestone | Target SLA |
| :--- | :--- |
| **Initial Acknowledgment** | Within **24 hours** |
| **Triage & Severity Assessment** | Within **72 hours** |
| **Patch & Signed Release (`Critical` / `High`)** | Within **7 days** |
| **Patch & Signed Release (`Medium` / `Low`)** | Within **14–30 days** |

---

## In-Scope Security Areas

We particularly welcome technical audits and reports covering:

1. **Ed25519 Self-Update Verification ([`internal/agent/updater.go`](internal/agent/updater.go)):**
   - Signature verification bypasses, TOCTOU race conditions during binary swap, or downgrade vectors.
2. **Read-Only Mode Enforcement ([`internal/agent/pusher.go`](internal/agent/pusher.go) & [`internal/agent/file_manager.go`](internal/agent/file_manager.go)):**
   - Any code path that permits command execution, file modification/deletion, IDE patching, process termination, or PTY spawning when `--read-only` (`AGENT_READ_ONLY=true`) is enabled.
3. **Filesystem & Symlink Jail ([`internal/agent/ide_executor.go`](internal/agent/ide_executor.go) & [`internal/agent/file_manager.go`](internal/agent/file_manager.go)):**
   - Path traversal or symlink races escaping restricted system prefixes (`/etc`, `/usr`, `/root`, `/bin`, `/sbin`, `/var`, `C:\Windows`, `C:\Program Files`).
4. **Pre-Execution Command Guardrails ([`internal/guardrails/guardrails.go`](internal/guardrails/guardrails.go)):**
   - Evasion of the 29 destructive command regular expressions.
5. **TLS SPKI Pinning & Transport Security ([`internal/transport/tlspin.go`](internal/transport/tlspin.go)):**
   - Certificate chain validation or SPKI pin bypasses on WebSocket (`wss://`) or HTTPS connections.
6. **Peer-to-Peer Migration & Yamux Tunnels ([`internal/agent/migration_engine.go`](internal/agent/migration_engine.go) & [`internal/agent/tunnel_engine.go`](internal/agent/tunnel_engine.go)):**
   - Ephemeral TLS fingerprint verification or token authentication weaknesses.
7. **In-Memory Secret Store & Redaction ([`internal/agent/secret_store.go`](internal/agent/secret_store.go) & [`internal/agent/executor.go`](internal/agent/executor.go)):**
   - Leakage of runtime secrets into disk logs (`rasnuos-agent.log` or `audit.jsonl`).

---

## Verifying Official Release Signatures

Every official `rasnuos-agent` binary published on [GitHub Releases](https://github.com/rasnu/os-agent/releases) is signed with Rasnu's offline Ed25519 release key and accompanied by a `.sig` detached signature and `SHA256SUMS.txt`.

- **Official Production Ed25519 Public Key (Base64):**
  ```text
  GJkzaBMAhuYIMYR8UiDyC49nr2Uhz4fh0A9vOkTyE/E=
  ```

To verify a downloaded binary before deploying it to production:

```bash
# 1. Verify SHA-256 checksum
sha256sum -c SHA256SUMS.txt --ignore-missing

# 2. Verify Ed25519 cryptographic signature
go run ./cmd/signer verify \
  -key "GJkzaBMAhuYIMYR8UiDyC49nr2Uhz4fh0A9vOkTyE/E=" \
  -file ./rasnuos-agent-linux-amd64 \
  -sig ./rasnuos-agent-linux-amd64.sig
```

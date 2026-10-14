# Contributing to `rasnuos-agent`

Thank you for your interest in auditing, testing, and improving `rasnuos-agent`, the host daemon for [RasnuOS](https://os.rasnu.com).

Because `rasnuos-agent` executes with system privileges across production Linux, macOS, and Windows hosts, every change must meet strict standards for determinism, memory safety, cross-platform compatibility, and fail-closed security.

---

## Ways to Contribute

- **Security Audits & Guardrail Hardening:** Propose improvements to the 29 pre-execution command guardrails in [`internal/guardrails/guardrails.go`](internal/guardrails/guardrails.go), path canonicalization in [`internal/agent/ide_executor.go`](internal/agent/ide_executor.go), or credential scrubbing in [`internal/agent/executor.go`](internal/agent/executor.go). *(For exploitable vulnerabilities, please follow [`SECURITY.md`](SECURITY.md) rather than opening a public issue.)*
- **OS Adapter Enhancements:** Improve platform-specific service discovery, OOM detection, or PTY handling in [`internal/osadapter/`](internal/osadapter).
- **Bug Reports:** Open a GitHub Issue with your OS version, architecture (`amd64` / `arm64`), agent version (`rasnuos-agent --version`), and relevant sanitized lines from `rasnuos-agent.log`.

---

## Development Setup & Prerequisites

- **Go 1.25+** (`CGO_ENABLED=0` static builds)
- Standard POSIX shell (`bash`) for running [`scripts/build-all.sh`](scripts/build-all.sh)

Clone the repository and verify dependencies:

```bash
git clone https://github.com/rasnu/os-agent.git
cd os-agent
go mod verify
```

---

## Pre-Submission Checklist (Required for All Pull Requests)

Our GitHub Actions CI pipeline ([`.github/workflows/ci.yml`](.github/workflows/ci.yml)) enforces the following checks on every pull request. Please run them locally before submitting:

### 1. Format & Static Analysis
```bash
gofmt -w .
go vet ./...
go mod tidy
```

### 2. Unit & Race-Detector Suite
All tests must pass with the Go race detector enabled and must not leave untracked files in the working tree:
```bash
go test -v -race -count=1 ./...
git status --porcelain
```

### 3. Cross-Platform Compilation & Signing Dry-Run
Verify that changes compile cleanly across all 6 target triples (`linux/amd64`, `linux/arm64`, `darwin/amd64`, `darwin/arm64`, `windows/amd64`, `windows/arm64`):
```bash
OUTPUT_DIR="./dist-test" KEY_DIR="./.ci-keys" ./scripts/build-all.sh
rm -rf ./dist-test ./.ci-keys
```

---

## Architectural Guidelines

1. **Zero CGo (`CGO_ENABLED=0`):** The agent must compile to a standalone static executable across all supported OS/Arch targets without requiring target hosts to have `libc` or system headers installed.
2. **OS Isolation via Build Tags:** Keep platform-specific syscalls, registry reads, and ConPTY/ioctl logic isolated inside [`internal/osadapter/`](internal/osadapter) behind `//go:build linux`, `//go:build darwin`, or `//go:build windows` tags.
3. **Respect `--read-only` Mode:** Any new WebSocket or HTTP intent that mutates host state (processes, files, services, packages, firewall rules, crontabs) **must** check `c.readOnly` in [`internal/agent/pusher.go`](internal/agent/pusher.go) / [`internal/agent/file_manager.go`](internal/agent/file_manager.go) and record a denied entry in the local audit log when `--read-only` is active.
4. **Bounded Resource Consumption:** Never buffer unbounded command output or offline telemetry in RAM. Use bounded ring buffers (`LogBuffer`, `metricQueue` cap of 1,000 snapshots) and explicit context timeouts.

---

## License

By contributing to `rasnuos-agent`, you agree that your contributions will be licensed under the [Functional Source License, Version 1.1, ALv2 Future License (`FSL-1.1-ALv2`)](LICENSE).

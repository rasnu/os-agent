# rasnuos-agent

[![Go Version](https://img.shields.io/badge/Go-1.25+-00ADD8?logo=go&logoColor=white)](go.mod)
[![Platforms](https://img.shields.io/badge/Platforms-Linux%20%7C%20macOS%20%7C%20Windows-informational)](#platform-support)
[![Architectures](https://img.shields.io/badge/Arch-amd64%20%7C%20arm64-success)](#building-and-verifying-releases)
[![License: FSL-1.1-ALv2](https://img.shields.io/badge/License-FSL--1.1--ALv2-blue.svg)](LICENSE)

`rasnuos-agent` is the host daemon for [RasnuOS](https://rasnu.com). It is written in Go, compiles to a single static binary with `CGO_ENABLED=0`, and runs on Linux, macOS, and Windows across `amd64` and `arm64` architectures.

> [!IMPORTANT]
> **Source Availability & Security Auditing**  
> Because `rasnuos-agent` runs with system privileges on managed infrastructure, the complete source code is published in this repository for technical inspection. Infrastructure engineers and security teams can review the transport layer, inspect all 29 pre-execution command guardrails, verify official Ed25519 release signatures, or build the binary directly from source.  
> **License:** Distributed under the [Functional Source License (FSL-1.1-ALv2)](LICENSE), permitting source inspection, internal use, and non-commercial research while prohibiting competing commercial use.

---

## Contents

- [Overview](#overview)
- [Architecture](#architecture)
- [Subsystems](#subsystems)
- [Security Model](#security-model)
- [Command Guardrails (29 Rules)](#command-guardrails-29-rules)
- [Platform Support](#platform-support)
- [Local Observability & Audit Log](#local-observability--audit-log)
- [Configuration](#configuration)
- [Building and Verifying Releases](#building-and-verifying-releases)
- [Directory Layout](#directory-layout)
- [License](#license)

---

## Overview

Traditional configuration management and deployment tools typically require opening inbound SSH ports (`22/tcp`) to internet-facing bastion hosts or storing long-lived private SSH keys in third-party control planes.

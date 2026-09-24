#!/bin/bash
# Copyright © 2026 Rasnu LLC (rasnu.com). All rights reserved.
# Licensed under the Functional Source License, Version 1.1, ALv2 Future License (FSL-1.1-ALv2).

set -e

echo "==================================================================="
echo "  RasnuOS Enterprise Agent Multi-Platform Build & Sign Pipeline    "
echo "==================================================================="

cd "$(dirname "$0")/.."

AGENT_PKG="./cmd/rasnuos-agent"

VERSION="${VERSION:-1.0.0}"
BUILD_TIME=$(date -u +"%Y-%m-%dT%H:%M:%SZ")
KEY_DIR="${KEY_DIR:-.agent_update_keys}"

# Official RasnuOS production public key used when CI secrets are not set.
DEFAULT_PRODUCTION_PUBLIC_KEY="GJkzaBMAhuYIMYR8UiDyC49nr2Uhz4fh0A9vOkTyE/E="

if [ -z "${AGENT_PUBLIC_KEY}" ] && [ -f "${KEY_DIR}/agent_ed25519_public.pub" ]; then
    AGENT_PUBLIC_KEY=$(cat "${KEY_DIR}/agent_ed25519_public.pub" | tr -d ' \n\r')
fi

if [ -z "${AGENT_PUBLIC_KEY}" ]; then
    if [ "${RELEASE}" = "1" ] || [ "${ENV}" = "production" ]; then
        if [ -n "${DEFAULT_PRODUCTION_PUBLIC_KEY}" ]; then
            echo "[INFO] AGENT_PUBLIC_KEY not specified in CI secrets; using official RasnuOS production public key."
            AGENT_PUBLIC_KEY="${DEFAULT_PRODUCTION_PUBLIC_KEY}"
        else
            echo "[ERROR] Production build requires AGENT_PUBLIC_KEY."
            echo "        Set AGENT_PUBLIC_KEY in the environment or CI pipeline."
            exit 1
        fi
    else
        echo "[INFO] No AGENT_PUBLIC_KEY specified. Generating local keypair in ${KEY_DIR}..."
        mkdir -p "${KEY_DIR}"
        go run ./cmd/signer generate-keys -out-dir "${KEY_DIR}" > /dev/null
        AGENT_PUBLIC_KEY=$(cat "${KEY_DIR}/agent_ed25519_public.pub" | tr -d ' \n\r')
        echo "[OK] Generated local development keys:"
        echo "  Public Key  : ${AGENT_PUBLIC_KEY}"
        echo "  Private Key : saved to ${KEY_DIR}/agent_ed25519_private.key (mode 0600)"
        echo ""
    fi
fi

echo "Validating Ed25519 Public Key..."
go run ./cmd/signer key-info -key "${AGENT_PUBLIC_KEY}"
echo ""

# -X requires the full module import path; a mismatched path is silently ignored by the linker.
LDFLAGS="-s -w -X rasnuos-agent/internal/buildinfo.Version=${VERSION} -X rasnuos-agent/internal/agent.AgentPublicKeyBase64=${AGENT_PUBLIC_KEY}"

echo "Build Configuration:"
echo "  Version          : ${VERSION}"
echo "  Built at         : ${BUILD_TIME}"
echo "  Public Key       : ${AGENT_PUBLIC_KEY}"
echo "  Fail-Closed Mode : Enabled (Ed25519 verified OTA updates)"
echo ""

OUTPUT_DIR="${OUTPUT_DIR:-../backend/public/agent-binaries}"
mkdir -p "${OUTPUT_DIR}"

echo "-> Building Linux AMD64..."
GOOS=linux GOARCH=amd64 go build -ldflags="${LDFLAGS}" -o "${OUTPUT_DIR}/rasnuos-agent-linux-amd64" "${AGENT_PKG}"

echo "-> Building Linux ARM64..."
GOOS=linux GOARCH=arm64 go build -ldflags="${LDFLAGS}" -o "${OUTPUT_DIR}/rasnuos-agent-linux-arm64" "${AGENT_PKG}"

echo "-> Building Windows AMD64..."
GOOS=windows GOARCH=amd64 go build -ldflags="${LDFLAGS}" -o "${OUTPUT_DIR}/rasnuos-agent-windows-amd64.exe" "${AGENT_PKG}"

echo "-> Building Windows ARM64..."
GOOS=windows GOARCH=arm64 go build -ldflags="${LDFLAGS}" -o "${OUTPUT_DIR}/rasnuos-agent-windows-arm64.exe" "${AGENT_PKG}"

echo "-> Building macOS AMD64..."
GOOS=darwin GOARCH=amd64 go build -ldflags="${LDFLAGS}" -o "${OUTPUT_DIR}/rasnuos-agent-macos-amd64" "${AGENT_PKG}"

echo "-> Building macOS ARM64 (Apple Silicon)..."
GOOS=darwin GOARCH=arm64 go build -ldflags="${LDFLAGS}" -o "${OUTPUT_DIR}/rasnuos-agent-macos-arm64" "${AGENT_PKG}"

echo ""
echo "[OK] All 6 binaries compiled successfully."
echo ""

PRIV_KEY="${AGENT_PRIVATE_KEY}"
if [ -z "${PRIV_KEY}" ] && [ -f "${KEY_DIR}/agent_ed25519_private.key" ]; then
    PRIV_KEY=$(cat "${KEY_DIR}/agent_ed25519_private.key" | tr -d ' \n\r')
fi

if [ -n "${PRIV_KEY}" ]; then
    echo "-> Generating Ed25519 Cryptographic Signatures (.sig)..."
    for bin in "${OUTPUT_DIR}"/rasnuos-agent-*; do
        if [[ "${bin}" != *.sig && "${bin}" != *.sha256 ]]; then
            go run ./cmd/signer sign -key "${PRIV_KEY}" -file "${bin}" -out "${bin}.sig"
            go run ./cmd/signer verify -key "${AGENT_PUBLIC_KEY}" -file "${bin}" -sig "${bin}.sig" > /dev/null
            echo "  [OK] $(basename "${bin}").sig (verified)"
        fi
    done
    echo ""
else
    if [ "${RELEASE}" = "1" ] || [ "${ENV}" = "production" ]; then
        echo "[ERROR] Release build requires AGENT_PRIVATE_KEY to sign binaries."
        echo "        Installed agents refuse unsigned self-updates, so an unsigned release"
        echo "        cannot be rolled out over the air. Set AGENT_PRIVATE_KEY in CI secrets."
        exit 1
    fi
    echo "[WARN] Private key not found (AGENT_PRIVATE_KEY or ${KEY_DIR}/agent_ed25519_private.key)."
    echo "       Binaries compiled with embedded public key, but .sig signatures were skipped."
    echo ""
fi

echo "-> Generating SHA-256 Checksums (.sha256)..."
cd "${OUTPUT_DIR}"
for file in rasnuos-agent-*; do
    if [[ "${file}" != *.sha256 && "${file}" != *.sig ]]; then
        sha256sum "${file}" > "${file}.sha256"
        echo "  [OK] ${file}.sha256"
    fi
done
cd - > /dev/null

echo ""
echo "-> Performing sanity check on compiled host binary..."
HOST_BIN="${OUTPUT_DIR}/rasnuos-agent-linux-amd64"
if [ -f "${HOST_BIN}" ] && [ "$(uname -s)" = "Linux" ] && [ "$(uname -m)" = "x86_64" ]; then
    chmod +x "${HOST_BIN}"
    echo "  Version check : $("${HOST_BIN}" --version)"
    echo "  Key check     : $("${HOST_BIN}" --key-info)"
fi

echo ""
echo "==================================================================="
echo "  [OK] Build Complete. Binaries stored in: ${OUTPUT_DIR}"
echo "==================================================================="
ls -lh "${OUTPUT_DIR}"/rasnuos-agent-* | grep -v '\.sha256' | grep -v '\.sig'

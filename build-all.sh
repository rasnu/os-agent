#!/bin/bash
set -e

echo "==================================================================="
echo "  RasnuOS Enterprise Agent Multi-Platform Build & Sign Pipeline    "
echo "==================================================================="

cd "$(dirname "$0")"

# ── 1. Build metadata & configuration ──────────────────────────────────────────
VERSION="${VERSION:-1.0.0}"
BUILD_TIME=$(date -u +"%Y-%m-%dT%H:%M:%SZ")
KEY_DIR="${KEY_DIR:-.agent_update_keys}"

# Known official RasnuOS production public key (fallback if CI secret is not set)
DEFAULT_PRODUCTION_PUBLIC_KEY="GJkzaBMAhuYIMYR8UiDyC49nr2Uhz4fh0A9vOkTyE/E="

if [ -z "${AGENT_PUBLIC_KEY}" ] && [ -f "${KEY_DIR}/agent_ed25519_public.pub" ]; then
    AGENT_PUBLIC_KEY=$(cat "${KEY_DIR}/agent_ed25519_public.pub" | tr -d ' \n\r')
fi

if [ -z "${AGENT_PUBLIC_KEY}" ]; then
    if [ "${RELEASE}" = "1" ] || [ "${ENV}" = "production" ]; then
        if [ -n "${DEFAULT_PRODUCTION_PUBLIC_KEY}" ]; then
            echo "ℹ️  AGENT_PUBLIC_KEY not specified in CI secrets; using official RasnuOS production public key."
            AGENT_PUBLIC_KEY="${DEFAULT_PRODUCTION_PUBLIC_KEY}"
        else
            echo "❌ [SECURITY ERROR] Production build requires AGENT_PUBLIC_KEY!"
            echo "   Set AGENT_PUBLIC_KEY in your environment or CI pipeline."
            exit 1
        fi
    else
        echo "ℹ️  No AGENT_PUBLIC_KEY specified. Generating local keypair in ${KEY_DIR}..."
        mkdir -p "${KEY_DIR}"
        go run ./cmd/signer generate-keys -out-dir "${KEY_DIR}" > /dev/null
        AGENT_PUBLIC_KEY=$(cat "${KEY_DIR}/agent_ed25519_public.pub" | tr -d ' \n\r')
        echo "✓ Generated local development keys:"
        echo "  Public Key  : ${AGENT_PUBLIC_KEY}"
        echo "  Private Key : saved to ${KEY_DIR}/agent_ed25519_private.key (mode 0600)"
        echo ""
    fi
fi

# Validate public key format and length using signer tool
echo "Validating Ed25519 Public Key..."
go run ./cmd/signer key-info -key "${AGENT_PUBLIC_KEY}"
echo ""

# ── 3. Configure LDFLAGS with Injected Key ─────────────────────────────────────
LDFLAGS="-s -w -X main.version=${VERSION} -X main.AgentPublicKeyBase64=${AGENT_PUBLIC_KEY}"

echo "Build Configuration:"
echo "  Version          : ${VERSION}"
echo "  Built at         : ${BUILD_TIME}"
echo "  Public Key       : ${AGENT_PUBLIC_KEY}"
echo "  Fail-Closed Mode : Enabled (Ed25519 verified OTA updates)"
echo ""

# Output directory in backend public storage
OUTPUT_DIR="${OUTPUT_DIR:-../backend/public/agent-binaries}"
mkdir -p "${OUTPUT_DIR}"

# ── 4. Cross-Platform Compilation Matrix ──────────────────────────────────────
# 1. Linux
echo "▶ Building Linux AMD64..."
GOOS=linux GOARCH=amd64 go build -ldflags="${LDFLAGS}" -o "${OUTPUT_DIR}/rasnuos-agent-linux-amd64" .

echo "▶ Building Linux ARM64..."
GOOS=linux GOARCH=arm64 go build -ldflags="${LDFLAGS}" -o "${OUTPUT_DIR}/rasnuos-agent-linux-arm64" .

# 2. Windows
echo "▶ Building Windows AMD64..."
GOOS=windows GOARCH=amd64 go build -ldflags="${LDFLAGS}" -o "${OUTPUT_DIR}/rasnuos-agent-windows-amd64.exe" .

echo "▶ Building Windows ARM64..."
GOOS=windows GOARCH=arm64 go build -ldflags="${LDFLAGS}" -o "${OUTPUT_DIR}/rasnuos-agent-windows-arm64.exe" .

# 3. macOS
echo "▶ Building macOS AMD64..."
GOOS=darwin GOARCH=amd64 go build -ldflags="${LDFLAGS}" -o "${OUTPUT_DIR}/rasnuos-agent-macos-amd64" .

echo "▶ Building macOS ARM64 (Apple Silicon)..."
GOOS=darwin GOARCH=arm64 go build -ldflags="${LDFLAGS}" -o "${OUTPUT_DIR}/rasnuos-agent-macos-arm64" .

echo ""
echo "✅ All 6 binaries compiled successfully."
echo ""

# ── 5. Cryptographic Ed25519 Signing ──────────────────────────────────────────
# Check for private key to sign binaries
PRIV_KEY="${AGENT_PRIVATE_KEY}"
if [ -z "${PRIV_KEY}" ] && [ -f "${KEY_DIR}/agent_ed25519_private.key" ]; then
    PRIV_KEY=$(cat "${KEY_DIR}/agent_ed25519_private.key" | tr -d ' \n\r')
fi

if [ -n "${PRIV_KEY}" ]; then
    echo "▶ Generating Ed25519 Cryptographic Signatures (.sig)..."
    for bin in "${OUTPUT_DIR}"/rasnuos-agent-*; do
        # Skip signatures, checksums, or non-binary files
        if [[ "${bin}" != *.sig && "${bin}" != *.sha256 ]]; then
            go run ./cmd/signer sign -key "${PRIV_KEY}" -file "${bin}" -out "${bin}.sig"
            # Verify signature immediately
            go run ./cmd/signer verify -key "${AGENT_PUBLIC_KEY}" -file "${bin}" -sig "${bin}.sig" > /dev/null
            echo "  ✓ $(basename "${bin}").sig (verified OK)"
        fi
    done
    echo ""
else
    if [ "${RELEASE}" = "1" ] || [ "${ENV}" = "production" ]; then
        echo "❌ [SECURITY ERROR] Release build requires AGENT_PRIVATE_KEY to sign binaries."
        echo "   Installed agents refuse unsigned self-updates, so an unsigned release would be"
        echo "   impossible to roll out over the air. Add the AGENT_PRIVATE_KEY repository secret."
        exit 1
    fi
    echo "⚠️  Notice: Private key not found (AGENT_PRIVATE_KEY or ${KEY_DIR}/agent_ed25519_private.key)."
    echo "   Binaries compiled with embedded public key, but .sig signatures were skipped."
    echo ""
fi

# ── 6. SHA-256 Checksums ──────────────────────────────────────────────────────
echo "▶ Generating SHA-256 Checksums (.sha256)..."
cd "${OUTPUT_DIR}"
for file in rasnuos-agent-*; do
    if [[ "${file}" != *.sha256 && "${file}" != *.sig ]]; then
        sha256sum "${file}" > "${file}.sha256"
        echo "  ✓ ${file}.sha256"
    fi
done
cd - > /dev/null

# ── 7. Verification of Compiled Host Binary ───────────────────────────────────
echo ""
echo "▶ Performing sanity check on compiled host binary..."
HOST_BIN="${OUTPUT_DIR}/rasnuos-agent-linux-amd64"
if [ -f "${HOST_BIN}" ] && [ "$(uname -s)" = "Linux" ] && [ "$(uname -m)" = "x86_64" ]; then
    chmod +x "${HOST_BIN}"
    echo "  Version check : $("${HOST_BIN}" --version)"
    echo "  Key check     : $("${HOST_BIN}" --key-info)"
fi

echo ""
echo "==================================================================="
echo "  🎉 Build Complete! Binaries stored in: ${OUTPUT_DIR}"
echo "==================================================================="
ls -lh "${OUTPUT_DIR}"/rasnuos-agent-* | grep -v '\.sha256' | grep -v '\.sig'

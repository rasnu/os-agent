#!/bin/bash
set -e

echo "Building all 6 RasnuOne Enterprise Agent variants..."

cd $(dirname "$0")

# Ensure output directory exists
mkdir -p ../public/agent-binaries

# 1. Linux
echo "Building Linux AMD64..."
GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o ../public/agent-binaries/rasnuone-agent-linux-amd64 .
echo "Building Linux ARM64..."
GOOS=linux GOARCH=arm64 go build -ldflags="-s -w" -o ../public/agent-binaries/rasnuone-agent-linux-arm64 .

# 2. Windows
echo "Building Windows AMD64..."
GOOS=windows GOARCH=amd64 go build -ldflags="-s -w" -o ../public/agent-binaries/rasnuone-agent-windows-amd64.exe .
echo "Building Windows ARM64..."
GOOS=windows GOARCH=arm64 go build -ldflags="-s -w" -o ../public/agent-binaries/rasnuone-agent-windows-arm64.exe .

# 3. macOS
echo "Building macOS AMD64..."
GOOS=darwin GOARCH=amd64 go build -ldflags="-s -w" -o ../public/agent-binaries/rasnuone-agent-macos-amd64 .
echo "Building macOS ARM64..."
GOOS=darwin GOARCH=arm64 go build -ldflags="-s -w" -o ../public/agent-binaries/rasnuone-agent-macos-arm64 .

echo "Generating SHA-256 Checksums..."
cd ../public/agent-binaries
for file in rasnuone-agent-*; do
    if [[ "$file" != *.sha256 ]]; then
        sha256sum "$file" > "${file}.sha256"
        echo "  - ${file}.sha256"
    fi
done

echo "✅ All 6 Enterprise binaries built and signed successfully!"

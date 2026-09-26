#!/usr/bin/env bash
set -Eeuo pipefail
export COPYFILE_DISABLE=1

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
OUTPUT_DIR="${ROOT_DIR}/dist"
mkdir -p "${OUTPUT_DIR}"

for arch in ${ARCHES:-amd64 arm64}; do
    echo "Building spot-agent-linux-${arch}"
    CGO_ENABLED=0 GOOS=linux GOARCH="${arch}" go build -trimpath -ldflags='-s -w' \
        -o "${OUTPUT_DIR}/spot-agent-linux-${arch}" "${ROOT_DIR}"
done
echo "Build complete in ${OUTPUT_DIR}"

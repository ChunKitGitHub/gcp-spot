#!/usr/bin/env bash
set -Eeuo pipefail
export COPYFILE_DISABLE=1

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
OUTPUT_DIR="${ROOT_DIR}/dist"
mkdir -p "${OUTPUT_DIR}"

# 确保 spot-agent 已编译并打包进去提供自动下载
if [ -d "${ROOT_DIR}/../spot/dist" ]; then
    mkdir -p "${ROOT_DIR}/dist/bin"
    cp -r "${ROOT_DIR}/../spot/dist/"* "${ROOT_DIR}/dist/bin/" 2>/dev/null || true
fi

for arch in ${ARCHES:-amd64 arm64}; do
    name="newspot-relay-linux-${arch}"
    stage="${OUTPUT_DIR}/${name}"
    rm -rf "${stage}"
    mkdir -p "${stage}"

    echo "Building ${name}..."
    CGO_ENABLED=0 GOOS=linux GOARCH="${arch}" go build -trimpath -ldflags='-s -w' \
        -o "${stage}/newspot-relay" "${ROOT_DIR}"

    cp "${ROOT_DIR}/deploy/install.sh" \
       "${ROOT_DIR}/deploy/newspot-relay.service" \
       "${ROOT_DIR}/deploy/env.example" \
       "${stage}/"

    # 如果有 spot-agent 二进制，也放入打包目录
    if [ -f "${ROOT_DIR}/../spot/dist/spot-agent-linux-${arch}" ]; then
        mkdir -p "${stage}/bin"
        cp "${ROOT_DIR}/../spot/dist/spot-agent-linux-"* "${stage}/bin/" 2>/dev/null || true
    fi

    chmod 0755 "${stage}/newspot-relay" "${stage}/install.sh"

    tar -C "${OUTPUT_DIR}" -czf "${OUTPUT_DIR}/${name}.tar.gz" "${name}"
    rm -rf "${stage}"
    echo "  -> ${OUTPUT_DIR}/${name}.tar.gz"
done

#!/usr/bin/env bash
set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BIN_NAME="newspot-relay"

ARCH="$(uname -m)"
case "${ARCH}" in
    x86_64)  ARCH_NAME="amd64" ;;
    aarch64|arm64) ARCH_NAME="arm64" ;;
    *) echo "Unsupported architecture: ${ARCH}"; exit 1 ;;
esac

if [ -f "${SCRIPT_DIR}/../dist/newspot-relay-linux-${ARCH_NAME}" ]; then
    SRC_BIN="${SCRIPT_DIR}/../dist/newspot-relay-linux-${ARCH_NAME}"
elif [ -f "${SCRIPT_DIR}/${BIN_NAME}" ]; then
    SRC_BIN="${SCRIPT_DIR}/${BIN_NAME}"
else
    echo "Binary not found. Please build first."
    exit 1
fi

echo "Installing newspot-relay (${ARCH_NAME})..."
sudo install -m 0755 "${SRC_BIN}" /usr/local/bin/newspot-relay
sudo install -m 0644 "${SCRIPT_DIR}/newspot-relay.service" /etc/systemd/system/newspot-relay.service

sudo mkdir -p /etc/newspot-relay /var/lib/newspot-relay/bin

# 复制 spot-agent 供自动分发
if [ -d "${SCRIPT_DIR}/bin" ]; then
    sudo cp -r "${SCRIPT_DIR}/bin/"* /var/lib/newspot-relay/bin/ 2>/dev/null || true
fi

if [ ! -f /etc/newspot-relay/env ]; then
    echo "Creating /etc/newspot-relay/env from template..."
    sudo cp "${SCRIPT_DIR}/env.example" /etc/newspot-relay/env
fi

sudo systemctl daemon-reload
echo "Installation complete!"
echo "Next steps:"
echo "1. Edit /etc/newspot-relay/env"
echo "2. Run: sudo systemctl enable --now newspot-relay"
echo "3. Access Web UI at: http://<IP>:9090/"

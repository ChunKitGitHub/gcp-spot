#!/usr/bin/env bash
set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BIN_NAME="spot-agent"

# 检测架构
ARCH="$(uname -m)"
case "${ARCH}" in
    x86_64)  ARCH_NAME="amd64" ;;
    aarch64|arm64) ARCH_NAME="arm64" ;;
    *) echo "Unsupported architecture: ${ARCH}"; exit 1 ;;
esac

# 寻找二进制文件
if [ -f "${SCRIPT_DIR}/../dist/spot-agent-linux-${ARCH_NAME}" ]; then
    SRC_BIN="${SCRIPT_DIR}/../dist/spot-agent-linux-${ARCH_NAME}"
elif [ -f "${SCRIPT_DIR}/${BIN_NAME}" ]; then
    SRC_BIN="${SCRIPT_DIR}/${BIN_NAME}"
else
    echo "Binary not found. Please build first."
    exit 1
fi

echo "Installing spot-agent (${ARCH_NAME})..."
sudo install -m 0755 "${SRC_BIN}" /usr/local/bin/spot-agent
sudo install -m 0644 "${SCRIPT_DIR}/spot-agent.service" /etc/systemd/system/spot-agent.service

if [ ! -f /etc/default/spot-agent ]; then
    echo "Creating default /etc/default/spot-agent..."
    sudo tee /etc/default/spot-agent > /dev/null << 'EOF'
RELAY_URL=http://10.140.0.2:9090
AGENT_TOKEN=change_this_to_relay_agent_token
SOCKS_LISTEN=0.0.0.0:1080
EOF
fi

sudo systemctl daemon-reload
echo "spot-agent installed. Edit /etc/default/spot-agent then run: sudo systemctl enable --now spot-agent"

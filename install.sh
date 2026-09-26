#!/usr/bin/env bash
set -Eeuo pipefail

echo "========================================="
echo "⚡ gcp-spot Relay 一键安装脚本"
echo "========================================="

if [ "$(id -u)" -ne 0 ]; then
    echo "错误: 请使用 root 权限运行此脚本 (例如: curl ... | sudo bash)"
    exit 1
fi

ARCH="$(uname -m)"
case "${ARCH}" in
    x86_64)        ARCH_NAME="amd64" ;;
    aarch64|arm64) ARCH_NAME="arm64" ;;
    *) echo "错误: 不支持的系统架构: ${ARCH}"; exit 1 ;;
esac

TAR_NAME="newspot-relay-linux-${ARCH_NAME}.tar.gz"
DOWNLOAD_URL="https://raw.githubusercontent.com/ChunKitGitHub/gcp-spot/main/dist/${TAR_NAME}"

echo "[1/4] 下载 gcp-spot Relay 安装包 (${ARCH_NAME})..."
TEMP_DIR="$(mktemp -d)"
trap 'rm -rf "${TEMP_DIR}"' EXIT

if ! curl -fsSL "${DOWNLOAD_URL}" -o "${TEMP_DIR}/${TAR_NAME}"; then
    echo "错误: 从 GitHub 下载失败: ${DOWNLOAD_URL}"
    echo "提示: 请检查网络连接是否通畅。"
    exit 1
fi

echo "[2/4] 解压并安装程序..."
tar -xzf "${TEMP_DIR}/${TAR_NAME}" -C "${TEMP_DIR}"
UNPACKED_DIR="${TEMP_DIR}/newspot-relay-linux-${ARCH_NAME}"
if [ ! -d "${UNPACKED_DIR}" ]; then
    UNPACKED_DIR="${TEMP_DIR}"
fi

rm -f /usr/local/bin/newspot-relay /usr/local/bin/relay
install -m 0755 "${UNPACKED_DIR}/newspot-relay" /usr/local/bin/newspot-relay
# 创建快捷命令 relay -> newspot-relay
ln -sf /usr/local/bin/newspot-relay /usr/local/bin/relay

# 安装 systemd 服务
if [ -f "${UNPACKED_DIR}/newspot-relay.service" ]; then
    install -m 0644 "${UNPACKED_DIR}/newspot-relay.service" /etc/systemd/system/newspot-relay.service
fi

# 安装分发给 Spot 节点的 agent 二进制
mkdir -p /var/lib/newspot-relay/bin
if [ -d "${UNPACKED_DIR}/bin" ]; then
    cp -r "${UNPACKED_DIR}/bin/"* /var/lib/newspot-relay/bin/ 2>/dev/null || true
fi

echo "[3/4] 初始化配置文件..."
mkdir -p /etc/newspot-relay
if [ ! -f /etc/newspot-relay/env ]; then
    if [ -f "${UNPACKED_DIR}/env.example" ]; then
        cp "${UNPACKED_DIR}/env.example" /etc/newspot-relay/env
    else
        cat > /etc/newspot-relay/env << 'EOF'
AGENT_TOKEN=change_this_to_agent_token
ADMIN_TOKEN=change_this_to_admin_token
VLESS_LISTEN=:443
TLS_MODE=acme
TLS_DOMAIN=tw.allplat.top
ACME_EMAIL=admin@example.com
ADMIN_LISTEN=0.0.0.0:9090
DATA_PATH=/var/lib/newspot-relay/state.json
AGENT_BINARY_DIR=/var/lib/newspot-relay/bin
EOF
    fi
    echo "已生成默认配置: /etc/newspot-relay/env"
fi

echo "[4/4] 重新加载 systemd..."
systemctl daemon-reload
systemctl enable newspot-relay

echo ""
echo "========================================="
echo "🎉 安装完成！"
echo "========================================="
echo "后续操作步骤："
echo "1. 编辑配置文件："
echo "   nano /etc/newspot-relay/env"
echo "2. 启动服务："
echo "   systemctl start newspot-relay"
echo "3. 检查状态："
echo "   systemctl status newspot-relay"
echo ""
echo "💡 以后只需运行以下命令即可随时一键更新："
echo "   relay update"
echo "========================================="

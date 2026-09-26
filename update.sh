#!/usr/bin/env bash
set -Eeuo pipefail

echo "========================================="
echo "⚡ gcp-spot Relay 一键更新脚本"
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

echo "[1/3] 从 GitHub 下载最新版本 (${ARCH_NAME})..."
TEMP_DIR="$(mktemp -d)"
trap 'rm -rf "${TEMP_DIR}"' EXIT

if ! curl -fsSL "${DOWNLOAD_URL}" -o "${TEMP_DIR}/${TAR_NAME}"; then
    echo "错误: 下载失败: ${DOWNLOAD_URL}"
    exit 1
fi

echo "[2/3] 停止当前服务并替换二进制..."
tar -xzf "${TEMP_DIR}/${TAR_NAME}" -C "${TEMP_DIR}"
UNPACKED_DIR="${TEMP_DIR}/newspot-relay-linux-${ARCH_NAME}"
if [ ! -d "${UNPACKED_DIR}" ]; then
    UNPACKED_DIR="${TEMP_DIR}"
fi

if systemctl is-active --quiet newspot-relay 2>/dev/null; then
    systemctl stop newspot-relay
fi

# 删除旧文件，防止运行中覆盖出现 text file busy
rm -f /usr/local/bin/newspot-relay /usr/local/bin/relay
install -m 0755 "${UNPACKED_DIR}/newspot-relay" /usr/local/bin/newspot-relay
ln -sf /usr/local/bin/newspot-relay /usr/local/bin/relay

if [ -d "${UNPACKED_DIR}/bin" ]; then
    mkdir -p /var/lib/newspot-relay/bin
    cp -r "${UNPACKED_DIR}/bin/"* /var/lib/newspot-relay/bin/ 2>/dev/null || true
fi

echo "[3/3] 重启 newspot-relay 服务..."
systemctl daemon-reload
systemctl restart newspot-relay
sleep 1

if systemctl is-active --quiet newspot-relay 2>/dev/null; then
    echo "🎉 更新成功！newspot-relay 服务已恢复运行。"
    systemctl status newspot-relay --no-pager -n 5
else
    echo "❌ 启动失败，查看日志: journalctl -u newspot-relay -n 20"
    exit 1
fi

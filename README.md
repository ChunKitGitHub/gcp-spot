# gcp-spot 极简代理中继系统

基于 GCP VPC 内网 IP 的动态 Spot 代理池中转系统。

## 🌟 核心特性

- **无需 Tailscale / WireGuard**：Relay 与 Spot 节点直接走 **GCP 内网私有 IPv4** 通信，零 VPN 封装损耗，低延迟高带宽。
- **Spot 极简单二进制**：Spot 机器无需 Docker、无需 gost，仅运行一个纯 Go 编写的单二进制 `spot-agent`（内置 SOCKS5 + GCP 元数据感知 + 定时心跳）。
- **内置 Web UI 控制台**：直观查看 Spot 节点、槽位接管状态，一键生成 VLESS 链接与二维码。
- **GCP 销毁自动接管与平滑回切**：Spot 被抢占或 IP 变化后，Relay 自动切换到健康备用节点；原节点恢复且稳定后自动回切，客户端 URI 无需修改。
- **极致简单的安装与更新体验**：
  - 首次安装：一条命令 `curl ... | bash` 搞定
  - 随时更新：终端输入 **`sudo relay update`** 秒级自动升级！

---

## 🛠️ 1. 中转服务器（VM A）部署

### 首次一键安装
在 Relay 虚拟机终端执行：
```bash
curl -fsSL https://raw.githubusercontent.com/ChunKitGitHub/gcp-spot/main/install.sh | sudo bash
```

安装脚本会自动检测架构、下载最新版本、安装 systemd 服务并生成默认配置文件 `/etc/newspot-relay/env`。

### 填写配置并启动
编辑配置文件：
```bash
sudo nano /etc/newspot-relay/env
```
配置说明：
```dotenv
# Spot Agent 心跳认证 Token
AGENT_TOKEN=your_secret_agent_token

# Web 控制台登录 Token
ADMIN_TOKEN=your_secret_admin_token

# VLESS 域名与证书配置
VLESS_LISTEN=:443
TLS_MODE=acme
TLS_DOMAIN=tw.allplat.top
ACME_EMAIL=admin@example.com

# 管理端口与 Web UI（同时支持 HTTP 与 HTTPS 访问）
ADMIN_LISTEN=0.0.0.0:9090
```

启动服务：
```bash
sudo systemctl start newspot-relay
sudo systemctl status newspot-relay
```

### 🚀 随时一键更新（推荐）
以后中转机要升级到最新版本，只需要在终端输入：
```bash
sudo relay update
```
或者运行：
```bash
curl -fsSL https://raw.githubusercontent.com/ChunKitGitHub/gcp-spot/main/update.sh | sudo bash
```
脚本会自动拉取最新版本覆盖并重启服务！

---

## ⚡ 2. Spot 节点自动入网

在 GCP 创建 Spot 虚拟机或配置 Instance Template 时，展开 **“高级选项 -> 管理 -> 自动化 -> 启动脚本 (Startup Script)”**，直接填入：

```bash
#!/bin/bash
curl -sSL http://<RELAY_内网_IP>:9090/bootstrap.sh | bash
```

> **说明**：启动脚本会自动向 Relay 请求对应架构的 `spot-agent`，自动配置并拉起 systemd 服务，随后自动上报内网 IP 至控制台。

---

## 🔒 3. GCP VPC 防火墙安全设置

在 GCP 控制台添加一条防火墙规则：
1. **名称**：`allow-relay-to-spot-socks`
2. **目标**：网络中的所有实例（或带特定标签的 Spot 实例）
3. **来源 IPv4 范围**：`<RELAY_内网_IP>/32`（仅允许 Relay 机器访问）
4. **协议和端口**：`tcp:1080`

以及确保 Relay 机器开放了入站端口：
- **TCP 80**：用于 Let's Encrypt 证书验证
- **TCP 443**：VLESS 客户端入站
- **TCP 9090**：Web 管理控制台与心跳接收

---

## 📱 4. 客户端配置与使用

1. 打开浏览器访问：`https://<你的域名>:9090/`。
2. 输入 `ADMIN_TOKEN` 登录。
3. 在 **“VLESS 用户管理”** 点击 **“+ 创建新用户”**。
4. 点击 **“📱 二维码”**，直接用手机端 **V2Box** 等软件扫码导入，也可点击输入框一键复制 URI。
5. 手机连接节点即可畅连！

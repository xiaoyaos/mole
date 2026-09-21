# Mole

双向对等内网穿透工具。两端客户端通过中继服务器建立 VPN 隧道，互相访问对方局域网资源，访问者以被访问端的身份出现（SNAT）。

## 架构

```
                    ┌──────────────────┐
                    │  中继服务器(公网)   │
                    │  :8080 WebSocket  │
                    │  :8081 TCP 中继   │
                    └──────┬───────────┘
                           │
              ┌────────────┴────────────┐
              │                         │
     ┌────────┴────────┐      ┌────────┴────────┐
     │  客户端 A       │      │  客户端 B       │
     │  tun0:10.0.0.1  │      │  tun0:10.0.0.2  │
     │  子网(可选)      │      │  子网(可选)      │
     └─────────────────┘      └─────────────────┘
```

- **控制面**: WebSocket 信令（注册、发现、连接、夺舍控制）
- **数据面**: TCP 隧道（中继转发）
- **网络层**: TUN/TAP 虚拟网卡 + 路由，应用无感
- **SNAT**: iptables / pfctl / New-NetNat，目标看到的是代理端 LAN IP

## 能力

| 能力 | 说明 |
|------|------|
| 双向穿透 | 任意两端均可主动连接对方 |
| 夺舍开关 | 每端独立 `possess on/off`，关闭后对方自动断开 |
| 子网共享 | 注册本地子网，对方自动添加路由直达 |
| SNAT 身份 | 以代理端的身份访问其局域网 |
| 透明接入 | 虚拟网卡 + 路由，无需应用层代理 |
| CLI 交互 | `list`、`connect`/`con`、`disconnect`/`disc`、`possess`、`status`、`exit`（↑↓ 历史、Tab 补全、序号选择） |
| 跨平台 | macOS / Linux / Windows（CLI 一致） |
| 自动清理 | 退出时自动删除路由、NAT 规则、关闭虚拟网卡 |
| 多子网 | 支持逗号分隔多个子网 |
| 隧道网段可配 | 服务端通过 `-tunnel-net` 指定 |
| 自动重连 | 连接断开后自动重建隧道（5 秒间隔） |
| 并发安全 | 所有共享资源互斥锁保护，无竞态 |

## 快速开始

### 下载客户端

GitHub Releases 是客户端的唯一分发入口：

| 平台 | 发布文件 | 使用方式 |
|------|----------|----------|
| Linux x86_64 | `mole-client-linux-amd64.tar.gz` | 解压后运行 `install.sh`，或使用下方一键安装命令 |
| Linux ARM64 | `mole-client-linux-arm64.tar.gz` | 解压后运行 `install.sh`，或使用下方一键安装命令 |
| macOS Apple Silicon | `mole-client-macos-arm64.dmg` | 打开 DMG，把 Mole 放入“应用程序”，通过最小启动器运行 |
| Windows x64 | `mole-client-windows-amd64-setup.exe` | 运行一个安装程序；其中已包含 Mole 客户端和 TAP 驱动 |

Linux 一键安装（自动识别 x86_64/ARM64，并校验 SHA256）：

```bash
curl -fsSL https://raw.githubusercontent.com/xiaoyaos/mole/main/packaging/linux/install-latest.sh | sh
```

macOS 与 Windows 启动器只需要服务器 IP/域名和连接端口。共享开关直接显示；数据转发端口（默认 `18081`）、认证令牌和共享网段位于高级选项。客户端名默认使用主机名。启动后继续在终端中使用 `list`、`connect`、`disconnect` 和 `exit`；启动器的“安全停止”会执行与 `exit` 相同的清理流程。

首版安装包使用临时签名或未签名方式发布。macOS 首次打开时可在 Finder 中按住 Control 点击应用并选择“打开”；Windows 可能显示 SmartScreen 提示。可使用 Release 中的 `SHA256SUMS` 核对下载文件。

### 启动中继服务器（公网机器）

```bash
# 前台运行
sudo ./mole-server -listen :8080

# 后台运行
sudo ./mole-server -listen :8080 &
sudo nohup ./mole-server -listen :8080 > /tmp/mole-server.log 2>&1 &

# 启用认证
sudo ./mole-server -listen :8080 -auth mytoken
```

Windows 后台运行：

```powershell
# 隐藏窗口后台运行
Start-Process -NoNewWindow -FilePath ".\mole-server-windows-amd64.exe" -ArgumentList "-listen :8080"

# 或作为后台作业
Start-Job -ScriptBlock { .\mole-server-windows-amd64.exe -listen :8080 }
```

服务端启动后会监听两个端口：
- `:8080` — WebSocket 信令
- `:8081` — TCP 数据中继（与 `-listen` 同主机）

如果 8081 已被 Docker、EMQX 等服务占用，可以更换数据中继端口。服务端和两个客户端使用相同的 `-relay-port`：

```bash
./mole-server -listen :8080 -relay-port 18081
sudo ./mole-client -server 公网IP:8080 -relay-port 18081 -name client-b -allow-possess
sudo ./mole-client -server 公网IP:8080 -relay-port 18081 -name client-a
```

信令和数据端口必须不同，且两者均需可达。任一端口无法监听时，服务端会退出并释放已经打开的监听端口。ToolHub 的参数表单默认使用 18081，以避开本机 EMQX 的 8081；已有命名预设继续保留旧端口。

### 启动客户端

**B 端（共享本地子网，允许被访问）**：

```bash
# 自动检测本地子网
sudo ./mole-client -server 公网IP:8080 -name client-b -allow-possess

# 手动指定子网
sudo ./mole-client -server 公网IP:8080 -name client-b \
  -local-subnet 192.168.2.0/24 -allow-possess

# 多子网
sudo ./mole-client -server 公网IP:8080 -name client-b \
  -local-subnet "192.168.2.0/24,10.0.1.0/24" -allow-possess
```

**A 端（不共享，主动连接）**：

```bash
sudo ./mole-client -server 公网IP:8080 -name client-a
```

### CLI 命令

```
> list                          # 查看在线对端（带序号）
  1. client-b (abcd1234) IP=10.0.0.2 [possess=allow], subnet=192.168.2.0/24

> connect 1                     # 按序号连接（自动添加路由）
> con abcd1234                  # 按 UUID 连接（con 是 connect 的别名）

> disconnect 1                  # 按序号断开
> disc abcd1234                 # 按 UUID 断开

> possess off                   # 关闭被夺舍（对方自动断开）
> possess on                    # 开启被夺舍
> status                        # 查看本端状态
> exit                          # 退出并清理

支持 ↑↓ 历史命令导航、Tab 命令补全、左右键编辑。
```

### 访问对方内网

```bash
# 连接后，在 A 的终端：
ping 192.168.2.100                    # 通，目标看到源 IP 是 B 的 LAN IP
ssh user@192.168.2.100                # SSH 到 B 局域网机器
curl http://192.168.2.100:8080        # 访问 B 内网服务
```

## 命令行选项

### mole-server

| 选项 | 默认 | 环境变量 | 说明 |
|------|------|---------|------|
| `-listen` | `:8080` | `NT_LISTEN` | WebSocket 信令监听地址 |
| `-relay-port` | `8081` | `NT_RELAY_PORT` | 同主机的 TCP 数据中继端口，须与客户端一致 |
| `-auth` | `""` | `NT_AUTH_TOKEN` | 客户端注册认证令牌 |
| `-tunnel-net` | `10.0.0.0/24` | — | 隧道虚拟网段 |
| `-version` | — | — | 显示版本号并退出 |

### mole-client

| 选项 | 默认 | 环境变量 | 说明 |
|------|------|---------|------|
| `-server` | `127.0.0.1:8080` | `NT_SERVER` | 中继服务器地址 |
| `-relay-port` | `8081` | `NT_RELAY_PORT` | TCP 数据中继端口，须与服务端一致 |
| `-name` | 本机主机名 | `NT_NAME` | 客户端标识名称 |
| `-auth` | `""` | `NT_AUTH_TOKEN` | 认证令牌 |
| `-allow-possess` | `false` | `NT_ALLOW_POSSESS` | 允许被对方主动连接 |
| `-local-subnet` | `""` | `NT_LOCAL_SUBNET` | 共享的本地子网，多个用逗号分隔（留空自动检测） |
| `-version` | — | — | 显示版本号并退出 |

## 工作原理

```
A 发起对 192.168.2.100 的访问
        │
   src=A_LAN_IP → dst=192.168.2.100
        │
  ┌─────┴─────┐
  │ A 内核     │ 路由表：192.168.2.0/24 → tun0
  └─────┬─────┘
        │
  ┌─────┴─────┐
  │ A 客户端   │ 从 tun0 读取 IP 包，经 TCP 中继发往 B
  └─────┬─────┘
        │
  ┌─────┴─────┐
  │ B 客户端   │ 写入 B 的虚拟网卡
  └─────┬─────┘
        │
  ┌─────┴─────┐
  │ B 内核     │ ip_forward + SNAT
  │           │ 源 IP 改写为 B 的 LAN IP
  └─────┬─────┘
        │
   src=B_LAN_IP → dst=192.168.2.100
        │
  ┌─────┴─────┐
  │ 目标机器   │ 看到 B 在访问自己，响应正常返回
  └───────────┘
        │
  B 收到响应 → 连接跟踪还原 → 经虚拟网卡回 A
```

## 权限要求

客户端需要 root / 管理员权限：
- 创建 TUN/TAP 虚拟网卡
- 修改系统路由表
- 配置 iptables / pfctl / New-NetNat NAT 规则
- 启用 IP 转发

## 平台适配

| 功能 | macOS | Linux | Windows |
|------|-------|-------|---------|
| 虚拟网卡 | utun (TUN) | tun (TUN) | TAP（需安装驱动） |
| IP 配置 | `ifconfig` | `ip addr` | PowerShell / netsh |
| 路由 | `route` | `ip route` | `route` |
| IP 转发 | `sysctl` | `sysctl` | 注册表 |
| SNAT | `pfctl` | `iptables` | `New-NetNat` |
| 数据格式 | 原始 IP 包 | 原始 IP 包 | 以太网帧（自动解包） |
| 子网接口检测 | 遍历网卡 IP | 遍历网卡 IP | 遍历网卡 IP |
| 额外处理 | TCP 时间戳剥离（SYN） | — | PnP 重置 TAP 驱动状态 |

## Windows 使用指南

### 安装 TAP 驱动

Windows 需要 TAP 虚拟网卡驱动：

- 专用驱动：[tap-windows-9.24.7-I601-Win10.exe](https://build.openvpn.net/downloads/releases/tap-windows-9.24.7-I601-Win10.exe)
- 或安装 [OpenVPN 客户端](https://openvpn.net/community-downloads/)（自带驱动）

验证：**设备管理器** → **网络适配器** → 看到 `TAP-Windows Adapter V9`。

### 运行

**右键 → 以管理员身份运行** PowerShell 或 CMD：

```powershell
# 目标端（共享子网，允许被访问）
.\mole-client-windows-amd64.exe -server 公网IP:8080 -name client-b -allow-possess

# 多网卡环境手动指定子网
.\mole-client-windows-amd64.exe -server 公网IP:8080 -name client-b `
  -local-subnet 192.168.2.0/24 -allow-possess

# 发起端
.\mole-client-windows-amd64.exe -server 公网IP:8080 -name client-a
```

CLI 交互与其他平台一致（↑↓ 历史、Tab 补全、序号选择均支持）：

```
> list
> connect 1
> ping 192.168.2.100
> disc 1
> possess off
> exit
```

### 注意事项

| 事项 | 说明 |
|------|------|
| 管理员权限 | 必须右键 → 以管理员身份运行 |
| TAP 网卡名 | 设备管理器可能显示为"以太网 X"，程序自动适配 |
| SNAT | 自动调用 PowerShell `New-NetNat` |
| IP 转发 | 自动设置注册表 `IPEnableRouter=1` |
| 防火墙 | 首次运行弹窗允许入站即可 |
| 启动延迟 | 每次启动时自动重置 TAP 驱动状态（PnP 禁用/启用），约 4 秒后才能开始使用 |
| 第二次运行 | PnP 重置解决了第二次运行转发失效问题，无需重装驱动 |

## 可靠性

| 场景 | 处理方式 |
|------|---------|
| WebSocket 断开 | 自动重连，全状态重建（TUN/NAT/路由/对端） |
| 重连期间用户操作 | 各操作互斥锁保护，不会并发冲突 |
| 对方主动断连 | 自动清理路由和对端连接 |
| 夺舍撤销 | 自动断开对方连接 |
| 并发写入 WebSocket | `wsMu` 互斥锁 + nil 检查 |
| 中继 TCP 读写死锁 | 双向 `Copy` 退出时关闭对方连接 |
| 中继阻塞 | `SetWriteDeadline(10s)` 避免永久卡住 |
| 竞态条件 | 所有共享状态互斥锁保护（`c.mu`、`pc.mu`、`c.wsMu`、`c.peerCacheMu`） |

## 开发

```bash
git clone <repo> mole
cd mole

make deps     # 下载依赖
make build    # 编译当前平台
make test     # 运行测试

make release  # 交叉编译全部 6 个平台
```

### 编译产物

`make release` 生成到 `bin/` 目录：

| 文件 | 平台 | 架构 |
|------|------|------|
| `bin/client/mole-client-darwin-amd64` | macOS | Intel |
| `bin/client/mole-client-darwin-arm64` | macOS | Apple Silicon |
| `bin/client/mole-client-linux-amd64` | Linux | x86_64 |
| `bin/client/mole-client-linux-arm64` | Linux | ARM64 |
| `bin/client/mole-client-windows-amd64.exe` | Windows | x86_64 |
| `bin/client/mole-client-windows-arm64.exe` | Windows | ARM64 |
| `bin/server/mole-server-darwin-amd64` / `arm64` | macOS | Intel / ARM |
| `bin/server/mole-server-linux-amd64` / `arm64` | Linux | x86_64 / ARM64 |
| `bin/server/mole-server-windows-amd64.exe` / `arm64.exe` | Windows | x86_64 / ARM64 |

## 协议

```
WebSocket 消息:
{
  "type": "register|list_peers|connect_request|possess_toggle|...",
  "payload": { ... }
}

TCP 数据中继帧:
[2字节大端长度] + [IP包数据]
```

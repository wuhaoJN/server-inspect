# 服务器巡检系统

基于 **Go + SSH** 的非侵入式 Linux 服务器巡检平台。部署在 Ubuntu / CentOS 等 Linux 服务器上，通过 Web 界面录入目标服务器账号密码，即可执行只读巡检，生成健康评分与完整报告，并保留历史记录。

> 目标主机 **无需安装 Agent**，巡检过程 **不修改任何系统配置**。

---

## 功能特性

- **非侵入式巡检**：全部通过 SSH 执行只读命令（`cat` / `ps` / `df` / `ss` / `systemctl status` 等），不写文件、不安装软件、不改配置、不提权。
- **多发行版适配**：自动识别 Ubuntu、Debian、CentOS、RHEL、Rocky、AlmaLinux、openEuler、麒麟、UOS、SUSE、Alpine 等系统；`ss/netstat`、`apt/dnf/yum`、`ufw/firewalld/iptables` 自动降级兼容，缺失项标记为「跳过」。
- **9 大类巡检项**：系统信息、CPU 与负载、内存、磁盘与 inode、进程、网络、服务、安全合规、内核日志。
- **健康评分**：按严重/警告项自动计算 0–100 分。
- **Web 管理界面**：服务器增删改查、节点职责、分组标签、IP 排序、单台/批量巡检、巡检历史、报告详情与搜索；巡检/配置登录保留在列表，编辑/删除收进「更多」菜单。
- **批量巡检结果分析**：批量结束后汇总状态分布、平均健康分、需优先处理的服务器和共性问题，直接链接到对应报告；每次批量结果持久化到「批量记录」，可随时回看。
- **定时巡检**：支持按间隔（分钟/小时）、每天固定时间、每周固定时间三种频率，范围可选全部服务器 / 分组 / 自选服务器；只巡检已配置登录方式的目标，全局并发 3，自动跳过重复执行。
- **维护状态**：可将人为关机维保的服务器标记为「维护中」（可设结束时间与原因）。维护中的服务器会被批量巡检和定时任务自动跳过，不计入异常统计，避免把计划内关机误判为故障。
- **服务器详情页**：资产信息、最新巡检摘要、近 7/30/90 天健康分与 CPU/内存/磁盘趋势、巡检历史；报告页提供与上一次巡检的对比（健康分、资源变化、新增/已消除问题）。
- **网络扫描发现**：手动指定 IPv4 网段与端口，扫描存活主机并读取 SSH 标识，多选后批量加入列表，再逐台配置登录方式。
- **密码安全存储**：服务器密码 **AES-256-GCM** 加密落盘；管理员密码 **bcrypt** 哈希；支持在界面修改管理员密码。
- **SSH 主机指纹（TOFU）**：首次连接记录 SHA256 指纹，后续严格校验；服务器重装后可手动重新信任。
- **单二进制交付**：前端页面、CSS、JS 全部 `go:embed` 内嵌，无需 Node.js、无需额外运行时。
- **纯 Go SQLite**：`modernc.org/sqlite`，`CGO_ENABLED=0` 静态编译，兼容 Ubuntu / CentOS 等主流发行版。

---

## 技术栈

| 层次 | 选型 |
| --- | --- |
| 后端 | Go 1.26+，标准库 `net/http`（Go 1.22 路由模式） |
| SSH | `golang.org/x/crypto/ssh` |
| 数据库 | SQLite（`modernc.org/sqlite`，纯 Go 实现） |
| 前端 | 原生 HTML / CSS / JavaScript（无构建步骤，`go:embed` 内嵌） |
| 部署 | Linux 静态二进制 + systemd |

---

## 一键部署到 Linux（推荐）

适用：Ubuntu 18.04+、Debian 10+、CentOS 7+、Rocky Linux、AlmaLinux、openEuler 等 **systemd** 系统。

### 1. 获取发布包

发布包位于项目 `dist/` 目录：

```
dist/
├── server-inspect-linux-amd64     # x86_64 服务器
├── server-inspect-linux-arm64     # ARM64 服务器
├── install.sh                     # 一键安装脚本
├── server-inspect.service         # systemd 单元参考
├── README.md                      # 部署与使用
├── DEVELOPMENT.md                 # 开发规范
├── ROADMAP.md                     # 后续计划
├── AGENTS.md                      # AI 编码代理入口
└── SHA256SUMS
```

如尚未构建，可在 Linux/macOS 或 Git Bash 中执行：

```bash
./release.sh
```

校验发布包完整性：

```bash
cd dist && sha256sum -c SHA256SUMS
```

### 2. 上传到服务器

先确认服务器架构（`uname -m`）：`x86_64` 使用 `linux-amd64`，`aarch64` 使用 `linux-arm64`。

```bash
# 以 x86_64 服务器为例
scp dist/server-inspect-linux-amd64 dist/install.sh user@server:/tmp/
```

### 3. 安装并启动

```bash
ssh user@server
sudo bash /tmp/install.sh --port 8080
```

脚本会自动完成：

1. 创建系统用户 `inspect`
2. 安装程序到 `/opt/server-inspect/`
3. 创建数据目录 `/var/lib/server-inspect/`（0700）
4. 写入并启用 `server-inspect.service`
5. 启动服务并打印**一次性初始密码**

安装完成后访问：

```
http://<服务器IP>:8080
```

首次登录后请立即点击右上角 **「修改密码」** 设置强密码。

### 4. 放行防火墙 / 安全组

```bash
# Ubuntu (UFW)
sudo ufw allow 8080/tcp

# CentOS / RHEL (firewalld)
sudo firewall-cmd --permanent --add-port=8080/tcp
sudo firewall-cmd --reload
```

云服务器还需在控制台安全组中放行对应端口。生产环境强烈建议只开放 443，由 Nginx/Caddy 反向代理（见下文）。

### 5. 常用运维命令

```bash
sudo systemctl status server-inspect       # 查看状态
sudo journalctl -u server-inspect -f       # 实时日志
sudo systemctl restart server-inspect      # 重启
curl http://127.0.0.1:8080/healthz         # 健康检查
/opt/server-inspect/server-inspect -version # 查看版本
```

### 升级

数据目录不受影响，管理员密码保持不变：

```bash
scp dist/server-inspect-linux-amd64 user@server:/tmp/
ssh user@server 'sudo bash /tmp/install.sh --bin /tmp/server-inspect-linux-amd64 --port 8080'
```

或手动替换：

```bash
sudo systemctl stop server-inspect
sudo install -m 0755 /tmp/server-inspect-linux-amd64 /opt/server-inspect/server-inspect
sudo systemctl start server-inspect
```

> 重新运行安装脚本会按参数重写 systemd 单元；如曾手动修改过单元（例如添加 `-cookie-secure`），请使用脚本参数重新传入。

### 卸载

```bash
sudo bash /tmp/install.sh --uninstall
```

默认保留 `/var/lib/server-inspect`（数据库与主密钥）。如需彻底删除：

```bash
sudo rm -rf /var/lib/server-inspect
```

### 安装脚本参数

| 参数 | 说明 |
| --- | --- |
| `-p, --port PORT` | 监听端口，默认 `8080`（绑定 `0.0.0.0`） |
| `--addr ADDR` | 完整监听地址，例如 `127.0.0.1:8080`（配合 Nginx 时使用） |
| `--data DIR` | 数据目录，默认 `/var/lib/server-inspect` |
| `--bin PATH` | 指定二进制文件路径 |
| `--cookie-secure` | 会话 Cookie 仅通过 HTTPS 发送（配置 HTTPS 后使用） |
| `--no-start` | 安装后不自动启动 |
| `--uninstall` | 卸载程序与服务（保留数据） |

---

## 手动部署（不使用安装脚本）

```bash
# 1. 创建用户与目录
sudo groupadd --system inspect
sudo useradd --system --home-dir /opt/server-inspect --shell /usr/sbin/nologin --gid inspect inspect
sudo mkdir -p /opt/server-inspect /var/lib/server-inspect
sudo chown -R inspect:inspect /var/lib/server-inspect

# 2. 安装二进制
sudo install -m 0755 server-inspect-linux-amd64 /opt/server-inspect/server-inspect

# 3. 安装 systemd 单元（按需修改监听地址）
sudo cp deploy/server-inspect.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now server-inspect

# 4. 查看初始密码
sudo journalctl -u server-inspect -n 50 --no-pager
```

> CentOS 用户请确认已关闭或放行 SELinux 对 8080 端口的限制；若服务启动失败可执行 `restorecon -Rv /opt/server-inspect` 并查看 `ausearch -m avc -ts recent`。

---

## 配置 HTTPS（推荐）

默认 HTTP 仅适合内网。生产环境建议由 Nginx 终止 TLS，并把应用限制在本机：

```bash
sudo bash install.sh --addr 127.0.0.1:8080 --cookie-secure
```

Nginx 示例：

```nginx
server {
    listen 80;
    server_name inspect.example.com;
    return 301 https://$host$request_uri;
}

server {
    listen 443 ssl http2;
    server_name inspect.example.com;

    ssl_certificate     /etc/nginx/ssl/inspect.crt;
    ssl_certificate_key /etc/nginx/ssl/inspect.key;

    location / {
        proxy_pass http://127.0.0.1:8080;
        proxy_set_header Host $host;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_read_timeout 300s;   # 批量/慢速巡检留足时间
    }
}
```

> 前端使用根路径资源，请部署在独立域名或根路径下，不支持 `/inspect/` 这类子路径。

---

## 数据与备份

Linux 默认数据目录：`/var/lib/server-inspect/`

```
/var/lib/server-inspect/
├── inspect.db       # 服务器、巡检记录、配置
├── inspect.db-wal   # WAL 日志（运行中）
├── inspect.db-shm
└── master.key       # AES-256 主密钥（0600）
```

**备份时必须同时复制 `inspect.db` 与 `master.key`**。主密钥丢失后，已保存的服务器密码将无法解密，需要重新录入。

```bash
sudo systemctl stop server-inspect
sudo tar czf /root/inspect-backup-$(date +%F).tar.gz -C /var/lib server-inspect
sudo systemctl start server-inspect
```

> 数据目录建议使用本地磁盘（ext4 / xfs）。SQLite 的 WAL 模式在 NFS/CIFS 等网络文件系统上可能不可靠。

---

## 命令行参数

| 参数 | 默认值 | 说明 |
| --- | --- | --- |
| `-addr` | `:8080` | HTTP 监听地址 |
| `-data` | `./data` | 数据目录 |
| `-admin-user` | `admin` | 管理员用户名 |
| `-admin-pass` | 空 | 管理员初始密码；留空则自动生成并打印一次 |
| `-reset-admin-pass` | 空 | 重置管理员密码后退出（不启动 Web 服务） |
| `-insecure-host-key` | `false` | 跳过 SSH 主机密钥校验（不推荐） |
| `-cookie-secure` | `false` | 会话 Cookie 仅通过 HTTPS 发送 |
| `-dial-timeout` | `10s` | SSH 连接超时 |
| `-inspect-timeout` | `3m` | 单次巡检总超时 |
| `-version` | — | 打印版本号后退出 |

对应环境变量：`INSPECT_ADDR`、`INSPECT_DATA`、`INSPECT_ADMIN_USER`、`INSPECT_ADMIN_PASS`。

**忘记管理员密码**：

```bash
sudo systemctl stop server-inspect
sudo -u inspect /opt/server-inspect/server-inspect -data /var/lib/server-inspect -reset-admin-pass 'NewStrongPassword'
sudo systemctl start server-inspect
```

> `-reset-admin-pass` 仅重置密码后退出，不会启动 Web 服务；请以服务用户 `inspect` 执行，避免数据库文件归属变成 root。
> 如系统没有 `sudo`，可使用：`sudo runuser -u inspect -- /opt/server-inspect/server-inspect ...`。

---

## 使用说明

> 网络要求：巡检中心需要能够通过 TCP 访问目标服务器的 SSH 端口（默认 22），且目标账号具备命令执行权限。请提前配置安全组与防火墙。

1. **网络扫描（可选）**：进入「服务器」页面 → 「网络扫描」，填写 CIDR 网段（如 `192.168.1.0/24`）和端口（默认 `22`，多个端口用逗号分隔），扫描完成后勾选可添加的 SSH 主机，一键加入服务器列表。
2. **配置登录方式**：扫描加入的服务器初始状态为「待配置」，点击「配置登录」填写登录用户名与密码后即可巡检。
3. **添加服务器**：也可以直接点击「＋ 添加服务器」手动录入。可填写「节点职责」（例如 Web / DB / Redis / 网关），列表中会展示，便于识别节点用途。
4. **测试连接**：表单中点击「测试连接」，验证网络、账号密码与命令执行权限，并显示主机密钥指纹。
5. **执行巡检**：点击服务器行的「巡检」，数秒后自动跳转到完整报告。
6. **批量巡检**：勾选已配置登录方式的多台服务器 → 「批量巡检」，前端以 3 并发执行并显示进度；未配置的服务器会自动跳过。完成后弹出**结果分析**：状态分布、平均健康分、需优先处理的服务器、共性问题分布，并可直接跳转到对应报告。每次结果同时写入「批量记录」，可随时回看。
7. **定时巡检**：进入「定时任务」→「新建计划」，选择范围（全部 / 分组 / 自选）、频率（每 N 分钟、每天 HH:MM、每周某天 HH:MM）。计划到点后由服务端自动执行并生成批量记录；可随时「立即执行」「启用/停用」「编辑」「删除」。

   > 定时任务按巡检服务进程的时区计算。若服务器系统时区为 UTC，需要给服务设置 `TZ=Asia/Shanghai`（systemd drop-in），否则“每天 08:00”会按 UTC 执行，页面显示为北京时间 16:00。
8. **查看历史**：进入「巡检记录」，按服务器、状态筛选，查看任意一次报告；报告页顶部展示与上一次巡检的对比。
9. **服务器详情与趋势**：点击服务器名称进入详情页，查看资产信息、最新巡检摘要、近 7/30/90 天健康分与 CPU/内存/磁盘趋势，以及最近巡检历史。
10. **维护状态**：对计划内关机维保的服务器，在「更多」菜单或详情页选择「标记维护中」，可填写结束时间与原因。维护中的服务器会显示紫色「维护中」标识，并被批量巡检与定时任务自动跳过；到达结束时间后自动恢复，也可手动「结束维护」。
11. **修改密码**：右上角「修改密码」。
12. **重新信任指纹**：服务器重装或更换后，编辑服务器时勾选「重新信任主机密钥」。

> 修改登录用户名、主机地址或 SSH 端口时，必须重新输入登录密码；系统不会把旧密码静默用于新的登录身份或目标。

> 服务器列表的操作列只保留高频的「巡检」/「配置登录」；「编辑服务器」「删除服务器」统一收在「更多」下拉菜单中。

### 网络扫描说明

- 扫描仅进行 **TCP 连接探测** 并读取 SSH 服务端主动发送的版本标识，**不会尝试登录**、不会发送口令或探测载荷。
- 支持 IPv4 CIDR（如 `10.0.0.0/24`）或单个 IP；单次最多 **4096 个地址**（/20），最多 **16 个端口**，同一时间只允许一个扫描任务。
- 扫描由巡检中心所在服务器发起，请确保该服务器到目标网段路由可达，并已放行相应出站端口。
- 默认扫描 22 端口；如果 SSH 使用自定义端口，请在「扫描端口」中一并填写（如 `22,2222`）。
- 已在服务器列表中的 `IP:端口` 会标记为「已在列表」，重复导入会自动跳过。
- 未检测到 SSH 标识的主机会显示为「未检测到 SSH」，不可勾选。
- 反向 DNS 解析为尽力而为（最多等待约 5 秒），解析不到时使用 IP 作为默认名称。
- **请仅扫描已获授权的网段**。端口扫描可能在目标防火墙、IDS 或安全设备上留下记录。

---

## 巡检项说明

| 分类 | 采集内容 | 默认阈值 |
| --- | --- | --- |
| 系统信息 | 发行版、内核、架构、主机名、运行时长、系统时间、时区、初始化系统 | — |
| CPU 与负载 | 处理器型号、逻辑核心数、CPU 使用率（`/proc/stat` 1 秒采样）、1/5/15 分钟负载、虚拟化类型 | 使用率 ≥85% 警告、≥95% 严重；负载 ≥1 倍核数警告、≥2 倍严重 |
| 内存 | 物理内存总量/已用/可用、缓存与缓冲、Swap 使用率 | ≥85% 警告、≥95% 严重 |
| 磁盘 | 各挂载点容量使用率、inode 使用率、可用空间 | ≥80% 警告、≥90% 严重 |
| 进程 | 进程总数、僵尸进程列表（PID/父进程/命令/状态）、CPU/内存占用 Top 进程 | 僵尸进程 >0 警告，并列出前 8 个明细 |
| 网络 | IPv4 地址、TCP/UDP 监听端口、已建立 TCP 连接数 | — |
| 服务 | systemd 失败单元、SSH/cron/rsyslog/docker/nginx/数据库等关键服务状态 | 失败单元/服务失败告警 |
| 安全合规 | `sshd_config` 关键项、防火墙状态、SELinux、登录失败次数、最近登录、待重启标记、可更新软件包数 | root 登录/空密码/防火墙关闭等告警 |
| 日志与其他 | `dmesg` 中 error/fail/oom/panic/segfault 关键字 | 存在异常记录时告警；虚拟化环境自动忽略 MMCONFIG、RAS Correctable Errors 等常见无害日志 |

**健康评分公式**：`100 - 25 × 严重项 - 6 × 警告项`（警告扣分最多 40 分，最低 0 分）。

---

## 非侵入式设计说明

巡检仅执行以下类型的**只读**命令，不会对目标系统产生副作用：

- 读取文件：`/etc/os-release`、`/proc/*`、`/etc/ssh/sshd_config`、`/var/log/auth.log`（仅读取）
- 查询状态：`uname`、`hostname`、`date`、`df`、`ps`、`/proc/meminfo`、`ss`/`netstat`、`ip`、`systemctl --failed`、`systemctl is-active`、`getenforce`、`ufw status`、`timedatectl`、`last`
- 软件包：`apt list --upgradable` 读取本地缓存；`dnf -C` / `yum -C` 仅使用缓存，**不联网、不执行更新**

明确**不会**执行：安装/卸载软件、修改配置、重启服务、删除文件、修改用户或权限、`sudo` 提权、上传脚本。

> 普通账号即可完成大部分检查；`sshd_config`、认证日志、`dmesg` 等需要 root 或相应读权限。无权限的项目标记为「跳过」，不影响其他检查项。防火墙状态在无权限时会尝试读取 `/etc/ufw/ufw.conf` 和 systemd 服务状态兜底判断。

**网络扫描**与巡检相互独立：扫描阶段仅做 TCP 连接探测并读取 SSH 服务端标识，不进行认证尝试、不执行远程命令；扫描完成后由管理员手动选择并配置登录凭据。

---

## 支持的发行版

**部署巡检中心（运行本系统）**

- Ubuntu 18.04+ / Debian 10+（systemd）
- CentOS 7+ / RHEL 7+ / Rocky Linux 8+ / AlmaLinux 8+
- openEuler 20.03+、Anolis OS、OpenCloudOS、麒麟、UOS
- SLES 12+ / openSUSE Leap 15+

**被巡检目标服务器**

- Debian 系：Ubuntu、Debian、Linux Mint、Kali、UOS 等
- RHEL 系：CentOS、RHEL、Rocky Linux、AlmaLinux、Fedora、openEuler、Anolis OS、OpenCloudOS、麒麟等
- SUSE 系：SLES、openSUSE
- 其他：Alpine Linux（busybox 环境部分项自动跳过）、Arch Linux

> CentOS 6 及更早版本使用 sysvinit，不在支持范围内；请升级到 CentOS 7+ 或 Rocky/Alma。

---

## 安全设计

1. **服务器密码加密**：AES-256-GCM，随机 nonce；API 永不返回密码字段。
2. **主密钥**：首次启动生成 32 字节随机密钥，保存为 `master.key`（0600）。**主密钥与数据库需一同备份**。
3. **管理员口令**：bcrypt 哈希；支持界面修改；登录失败固定延时，降低暴力破解与用户名枚举风险。安装脚本首次启动时通过 0600 环境文件传入初始密码，启动成功后立即删除，不写入日志。
4. **会话**：HMAC-SHA256 签名 Cookie（12 小时有效），`HttpOnly` + `SameSite=Lax`。
5. **CSRF 防护**：写操作校验 `Origin` 与 `Host` 同源。
6. **SSH 主机指纹**：首次连接记录（TOFU），之后严格比对 SHA256 指纹；`-insecure-host-key` 仅建议测试环境。
7. **安全响应头**：CSP、`X-Frame-Options: DENY`、`X-Content-Type-Options: nosniff` 等。
8. **最小权限运行**：systemd 服务以 `inspect` 系统用户运行，配合 `ProtectSystem` 等加固选项。

### 平台边界与巡检账号权限

- **只读巡检**：巡检平台只负责采集与呈现结果，不修改目标系统配置、不安装软件、不启停服务、不修复问题、不开启防火墙。
- **巡检账号最小权限**：巡检账号必须是普通用户，禁止加入 `sudo`/`wheel`；禁止通过 `sudo`、ACL、`setcap`、Agent 或特权 helper 扩大采集范围。
- **跳过即未知**：权限不足的检查项如实显示「跳过」，不伪装成「正常」；平台不会为了提升覆盖率去修改目标文件权限或安全基线。
- **合规边界**：等保/合规结论由目标主机实际配置和独立合规检查保证；巡检报告中的「跳过」只是覆盖范围说明，不是主机不合规的结论。
- **迭代边界**：聚焦巡检项准确性、解析、报告/趋势/对比、批量/定时稳定性与告警通知；不引入提权、ACL、Agent、自动修复等发散能力。甲方确有额外覆盖需求时，走独立特权合规检查与变更审批，不并入日常巡检账号。

> 生产环境务必配置 HTTPS。系统默认 HTTP 仅适合内网或受控环境。

---

## 开发与构建

环境要求：**Go 1.26+**（开发验证版本 Go 1.27.3）。

```bash
go mod download
go test ./...        # 单元测试 + 内存 SSH 服务端端到端测试
go vet ./...
go run . -addr :8080 -data ./data
```

### 构建 Linux 发布包

```bash
./release.sh                     # 输出 dist/（amd64 + arm64）
VERSION=1.1.0 ./release.sh       # 指定版本
```

### 单独构建

```bash
# Linux amd64
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "-s -w" -o server-inspect-linux-amd64 .

# Linux arm64
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags "-s -w" -o server-inspect-linux-arm64 .
```

### Windows 本地开发（可选）

```powershell
.\build.ps1
.\server-inspect.exe -addr :8080 -data .\data
```

Windows 仅用于本地开发调试。正式运行请使用 Linux 发布包。

---

## 项目结构

```
.
├── main.go                          # 入口、配置、优雅退出
├── internal/
│   ├── batch/                       # 批量巡检结果聚合与分析摘要
│   ├── compare/                     # 相邻两次巡检报告对比
│   ├── console/                     # Windows 控制台 UTF-8 适配（Linux 为空实现）
│   ├── inspect/                     # 巡检引擎（命令集、解析器、报告、评分）
│   ├── netscan/                     # IPv4 网段扫描（TCP 探测 + SSH 标识）
│   ├── runner/                      # 单台/批量巡检执行与结果持久化
│   ├── scheduler/                   # 定时计划调度与下次执行时间计算
│   ├── secret/                      # AES-GCM、bcrypt、随机口令/令牌
│   ├── sshx/                        # SSH 连接、指纹校验、命令执行
│   ├── store/                       # SQLite 持久化层
│   └── web/                         # HTTP 路由、鉴权、API、内嵌前端
│       └── static/                  # index.html / app.css / app.js
├── deploy/
│   ├── install.sh                   # Linux 一键安装脚本
│   └── server-inspect.service       # systemd 单元参考
├── dist/                            # 发布包（Linux 二进制 + 安装脚本）
├── release.sh                       # 构建 Linux 发布包
├── build.ps1 / build.sh             # 本地构建
├── go.mod / go.sum
├── README.md                        # 部署与使用
├── DEVELOPMENT.md                   # 开发规范
├── ROADMAP.md                       # 后续计划
├── AGENTS.md                        # AI 编码代理入口
├── CLAUDE.md / GEMINI.md            # AI 工具兼容入口
├── .github/copilot-instructions.md
├── .cursor/rules/project.mdc
└── .windsurfrules
```

---

## HTTP API 一览

所有接口返回 `{"ok":true,"data":...}` 或 `{"ok":false,"error":"..."}`。除登录、会话查询与健康检查外均需登录。

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET | `/healthz` | 健康检查（无需登录） |
| POST | `/api/login` | 登录 `{username,password}` |
| POST | `/api/logout` | 退出登录 |
| GET | `/api/session` | 查询当前会话 |
| POST | `/api/password` | 修改管理员密码 `{currentPassword,newPassword}` |
| GET | `/api/dashboard` | 首页概览 |
| GET | `/api/servers` | 服务器列表（含最近巡检摘要） |
| GET | `/api/servers/{id}` | 服务器详情（资产信息 + 最近巡检摘要） |
| GET | `/api/servers/{id}/trends?days=30` | 单台服务器趋势点（1–365 天） |
| POST | `/api/servers` | 新增服务器 |
| POST | `/api/servers/bulk` | 批量加入扫描发现的主机（初始为待配置） |
| POST | `/api/scan` | 扫描指定 IPv4 网段 `{cidr,ports}` |
| PUT | `/api/servers/{id}` | 修改服务器 / 配置登录方式（密码留空表示不变） |
| DELETE | `/api/servers/{id}` | 删除服务器（保留历史记录） |
| POST | `/api/servers/test` | 测试连接 |
| POST | `/api/servers/{id}/inspect` | 执行巡检并返回完整报告 |
| POST | `/api/servers/{id}/maintenance` | 标记/结束维护状态 `{enabled,until,note}` |
| POST | `/api/servers/{id}/trust` | 重新信任主机密钥指纹 |
| GET | `/api/inspections` | 巡检记录分页（`serverId`、`status`、`limit`、`offset`） |
| GET | `/api/inspections/{id}` | 巡检详情与完整报告 |
| GET | `/api/inspections/{id}/compare` | 与同一服务器上一次巡检对比 |
| DELETE | `/api/inspections/{id}` | 删除巡检记录 |
| GET | `/api/batches` | 批量记录分页（`limit`、`offset`） |
| POST | `/api/batches` | 保存一次手动批量巡检结果 `{inspectionIds,failures,skipped,maintenanceSkipped,elapsedMs}`；`skipped` 为未配置数，`maintenanceSkipped` 为维护跳过数 |
| GET | `/api/batches/{id}` | 批量记录详情与完整分析 |
| GET | `/api/schedules` | 定时计划列表（含目标数量） |
| POST | `/api/schedules` | 新建定时计划 |
| PUT | `/api/schedules/{id}` | 修改 / 启用停用定时计划 |
| DELETE | `/api/schedules/{id}` | 删除定时计划（保留已生成的批量记录） |
| POST | `/api/schedules/{id}/run` | 立即执行定时计划 |

---

## 常见问题

**Q：安装脚本提示「未检测到 systemd」？**
本系统通过 systemd 托管服务，需要 CentOS 7+、Ubuntu 16.04+ 等使用 systemd 的系统。CentOS 6 及更早版本不支持。

**Q：服务启动失败（status=203/EXEC）？**
通常是二进制权限或 SELinux 问题：

```bash
sudo ls -l /opt/server-inspect/server-inspect
sudo journalctl -u server-inspect -n 50 --no-pager
sudo restorecon -Rv /opt/server-inspect
```

**Q：浏览器无法访问？**
依次检查：服务是否运行（`systemctl status`）、监听地址（`ss -lntp | grep 8080`）、本机防火墙（ufw/firewalld）、云安全组。

**Q：扫描不到主机，或全部显示「未检测到 SSH」？**
确认巡检中心到目标网段三层可达、目标 SSH 端口已开放、防火墙未拦截；自定义 SSH 端口需在「扫描端口」中填写。部分安全设备会限制端口扫描，可改用「＋ 添加服务器」手动录入。

**Q：扫描加入的服务器为什么不能巡检？**
扫描只负责发现主机，不保存任何凭据。列表中的「待配置」服务器需要点击「配置登录」填写用户名和密码后才能巡检，批量巡检会自动跳过未配置的服务器。

**Q：测试连接提示「主机密钥指纹不一致」？**
服务器重装、更换 SSH 主机密钥或 IP 指向了另一台机器。确认无误后，在编辑服务器时勾选「重新信任主机密钥」，或点击测试失败提示中的按钮。

**Q：巡检报告里很多项目显示「跳过」？**
通常是登录账号权限不足（无法读取 `sshd_config`、认证日志、防火墙状态或 `dmesg`），或目标系统缺少对应命令。跳过项不参与评分。

**Q：忘记管理员密码？**
使用 `-reset-admin-pass '新密码'` 启动一次，然后正常启动服务（见上文示例）。

**Q：历史记录太多如何清理？**
在「巡检记录」页面单条删除，或对 SQLite 执行 `DELETE FROM inspections WHERE started_at < '...'`（建议先备份）。

---

## 开发文档

- [AGENTS.md](AGENTS.md)：AI 编码代理入口规范（切换模型/新会话先读）
- [DEVELOPMENT.md](DEVELOPMENT.md)：目录结构、编码/安全/测试/发布规范、常见坑
- [ROADMAP.md](ROADMAP.md)：下一步功能规划与优先级

> 使用 Claude Code / Gemini CLI / Cursor / GitHub Copilot / Windsurf 时，项目根目录已提供对应兼容入口，均指向 `AGENTS.md`。

## 已知限制

- 单管理员模式，暂未提供 RBAC、多用户与操作审计日志。
- 网络扫描目前仅支持 IPv4，单次最多 4096 个地址（/20）、16 个端口；不支持 IPv6 与端口范围语法。
- 巡检需在界面手动或批量触发，暂未内置定时巡检调度（可通过 cron 调用 API 扩展）。
- 巡检同步执行；批量巡检由前端以 3 并发发起（可调整 `app.js` 中的并发数）。
- 暂未支持 SSH 私钥登录（当前按需求使用账号密码）。
- 不采集 GPU、RAID 卡、硬件传感器等需要厂商专用工具的数据。

---

## License

本项目为内部运维工具示例，可按需修改与二次分发。

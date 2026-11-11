#!/usr/bin/env sh
# ============================================================
# 服务器巡检系统 - Linux 一键安装脚本
# 适用：Ubuntu 18.04+ / Debian 10+ / CentOS 7+ / Rocky / Alma / openEuler 等 systemd 系统
# 用法：sudo bash install.sh [选项]
# ============================================================
set -eu

APP_NAME="server-inspect"
SERVICE_USER="inspect"
APP_DIR="/opt/server-inspect"
DATA_DIR="/var/lib/server-inspect"
UNIT_FILE="/etc/systemd/system/server-inspect.service"
ENV_FILE="/etc/server-inspect.env"
ADDR="0.0.0.0:8080"
BIN=""
AUTO_START=1
UNINSTALL=0
COOKIE_SECURE=0
ENV_FILE_CREATED=0
ADMIN_PASS=""
SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)

cleanup_env() {
    if [ "$ENV_FILE_CREATED" -eq 1 ]; then
        rm -f "$ENV_FILE"
    fi
}
trap cleanup_env EXIT INT TERM

generate_password() {
    if command -v openssl >/dev/null 2>&1; then
        openssl rand -base64 32 | tr -dc 'A-Za-z0-9' | cut -c1-20
    else
        head -c 64 /dev/urandom | base64 | tr -dc 'A-Za-z0-9' | cut -c1-20
    fi
}

usage() {
    cat <<'EOF'
服务器巡检系统安装脚本（Linux / systemd）

用法:
  sudo bash install.sh [选项]
  sudo bash install.sh --uninstall

选项:
  -p, --port PORT     对外监听端口（默认 8080，自动绑定 0.0.0.0）
      --addr ADDR     完整监听地址，例如 127.0.0.1:8080
      --data DIR      数据目录（默认 /var/lib/server-inspect）
      --bin PATH      指定二进制文件（默认自动匹配同目录 linux-amd64 / linux-arm64）
      --cookie-secure 会话 Cookie 仅通过 HTTPS 发送（配置 HTTPS 后使用）
      --no-start      安装后不启动服务
      --uninstall     卸载程序与服务（保留数据目录）
  -h, --help          显示帮助
EOF
}

log()  { printf '\033[1;34m[INFO]\033[0m %s\n' "$*"; }
warn() { printf '\033[1;33m[WARN]\033[0m %s\n' "$*"; }
err()  { printf '\033[1;31m[ERR ]\033[0m %s\n' "$*" >&2; }

while [ $# -gt 0 ]; do
    case "$1" in
        -p|--port)
            [ $# -ge 2 ] || { err "--port 需要端口参数"; exit 1; }
            ADDR="0.0.0.0:$2"
            shift 2
            ;;
        --addr)
            [ $# -ge 2 ] || { err "--addr 需要地址参数"; exit 1; }
            ADDR="$2"
            shift 2
            ;;
        --data)
            [ $# -ge 2 ] || { err "--data 需要目录参数"; exit 1; }
            DATA_DIR="$2"
            shift 2
            ;;
        --bin)
            [ $# -ge 2 ] || { err "--bin 需要文件路径"; exit 1; }
            BIN="$2"
            shift 2
            ;;
        --no-start)
            AUTO_START=0
            shift
            ;;
        --cookie-secure)
            COOKIE_SECURE=1
            shift
            ;;
        --uninstall)
            UNINSTALL=1
            shift
            ;;
        -h|--help)
            usage
            exit 0
            ;;
        *)
            err "未知参数: $1"
            usage
            exit 1
            ;;
    esac
done

if [ "$(id -u)" -ne 0 ]; then
    err "请使用 root 权限运行：sudo bash install.sh"
    exit 1
fi

if ! command -v systemctl >/dev/null 2>&1; then
    err "未检测到 systemd，本脚本仅支持 systemd 系统（Ubuntu 16.04+/CentOS 7+）"
    exit 1
fi

if [ "$UNINSTALL" -eq 1 ]; then
    log "正在卸载 ${APP_NAME} ..."
    systemctl stop "$APP_NAME" 2>/dev/null || true
    systemctl disable "$APP_NAME" 2>/dev/null || true
    rm -f "$UNIT_FILE"
    systemctl daemon-reload
    rm -rf "$APP_DIR"
    log "程序与服务已移除。数据目录保留：$DATA_DIR"
    log "如需彻底删除数据（含数据库与主密钥）：rm -rf $DATA_DIR"
    exit 0
fi

# 定位二进制文件
if [ -z "$BIN" ]; then
    ARCH=$(uname -m)
    case "$ARCH" in
        x86_64|amd64)
            BIN="$SCRIPT_DIR/server-inspect-linux-amd64"
            ;;
        aarch64|arm64)
            BIN="$SCRIPT_DIR/server-inspect-linux-arm64"
            ;;
        *)
            err "暂不支持的 CPU 架构：$ARCH，请使用 --bin 手动指定二进制文件"
            exit 1
            ;;
    esac
    if [ ! -f "$BIN" ] && [ -f "$SCRIPT_DIR/server-inspect" ]; then
        BIN="$SCRIPT_DIR/server-inspect"
    fi
fi
if [ ! -f "$BIN" ]; then
    err "未找到二进制文件：$BIN"
    err "请将 server-inspect-linux-amd64（或 arm64）与 install.sh 放在同一目录，或使用 --bin 指定路径。"
    exit 1
fi
BIN=$(CDPATH= cd -- "$(dirname -- "$BIN")" && pwd)/$(basename -- "$BIN")
if [ ! -x "$BIN" ]; then
    # 通过 scp/pscp 上传的文件通常不带执行位，这里自动补上。
    chmod +x "$BIN" 2>/dev/null || true
fi
if [ ! -x "$BIN" ]; then
    err "二进制文件没有执行权限：$BIN"
    exit 1
fi

PORT=${ADDR##*:}
case "$PORT" in
    ''|*[!0-9]*)
        err "监听地址无效：$ADDR（端口部分必须是数字）"
        exit 1
        ;;
esac
case "$ADDR" in
    *[[:space:]]*)
        err "监听地址不能包含空白字符：$ADDR"
        exit 1
        ;;
esac
case "$DATA_DIR" in
    *[[:space:]]*)
        err "数据目录不能包含空白字符：$DATA_DIR"
        exit 1
        ;;
    /*)
        ;;
    *)
        err "数据目录必须是绝对路径：$DATA_DIR"
        exit 1
        ;;
esac
case "$DATA_DIR" in
    /home/*)
        err "数据目录不能放在 /home 下（systemd ProtectHome=true 会屏蔽），请使用 /var/lib 或 /data 等路径"
        exit 1
        ;;
esac
if [ "$PORT" -lt 1 ] || [ "$PORT" -gt 65535 ]; then
    err "端口必须在 1-65535 之间：$PORT"
    exit 1
fi
if [ "$PORT" -lt 1024 ]; then
    warn "端口 $PORT 小于 1024，非 root 服务通常无法绑定。建议使用 8080 并通过 Nginx 反向代理。"
fi

BIN_VERSION=$("$BIN" -version 2>/dev/null || true)
if [ -z "$BIN_VERSION" ]; then
    err "二进制无法在当前系统执行：$BIN（可能是架构不匹配或文件损坏）"
    err "请根据 uname -m 选择 server-inspect-linux-amd64 或 server-inspect-linux-arm64。"
    exit 1
fi
log "版本信息：$BIN_VERSION"

# 创建运行用户
if ! getent group "$SERVICE_USER" >/dev/null 2>&1; then
    groupadd --system "$SERVICE_USER"
    log "已创建用户组：$SERVICE_USER"
fi
if ! id -u "$SERVICE_USER" >/dev/null 2>&1; then
    NOLOGIN=$(command -v nologin 2>/dev/null || echo /sbin/nologin)
    useradd --system --home-dir "$APP_DIR" --shell "$NOLOGIN" --gid "$SERVICE_USER" "$SERVICE_USER"
    log "已创建系统用户：$SERVICE_USER"
fi

# 停止旧服务
systemctl stop "$APP_NAME" 2>/dev/null || true

# 安装程序与数据目录
install -d -m 0755 -o root -g root "$APP_DIR"
install -m 0755 "$BIN" "$APP_DIR/$APP_NAME"
install -d -m 0700 -o "$SERVICE_USER" -g "$SERVICE_USER" "$DATA_DIR"
chown -R "$SERVICE_USER:$SERVICE_USER" "$DATA_DIR"
log "程序已安装：$APP_DIR/$APP_NAME"
log "数据目录：$DATA_DIR（0700，仅 $SERVICE_USER 可访问）"

# 首次安装：生成随机管理员密码，仅在首次启动时通过 root-only 环境文件传入，避免写入日志。
rm -f "$ENV_FILE"
if [ "$AUTO_START" -eq 1 ] && [ ! -f "$DATA_DIR/inspect.db" ]; then
    ADMIN_PASS=$(generate_password)
    if [ ${#ADMIN_PASS} -lt 16 ]; then
        err "生成管理员初始密码失败"
        exit 1
    fi
    ( umask 077; printf 'INSPECT_ADMIN_PASS=%s\n' "$ADMIN_PASS" >"$ENV_FILE" )
    chmod 600 "$ENV_FILE"
    ENV_FILE_CREATED=1
fi

# 根据 systemd 版本生成兼容的加固配置
SYSTEMD_VER=$(systemctl --version 2>/dev/null | awk 'NR==1 {print $2}' | tr -dc '0-9')
[ -n "$SYSTEMD_VER" ] || SYSTEMD_VER=0
if [ "$SYSTEMD_VER" -ge 231 ] 2>/dev/null; then
    HARDENING="PrivateTmp=true
ProtectHome=true
ProtectSystem=strict
ReadWritePaths=$DATA_DIR"
else
    # CentOS 7 (systemd 219) 等旧版本：ReadWritePaths 不被识别，
    # 使用 full，保证 /var/lib 数据目录可写。
    HARDENING="PrivateTmp=true
ProtectHome=true
ProtectSystem=full"
fi
if [ "$SYSTEMD_VER" -ge 229 ] 2>/dev/null; then
    HARDENING="$HARDENING
NoNewPrivileges=true"
fi

EXTRA_ARGS=""
if [ "$COOKIE_SECURE" -eq 1 ]; then
    EXTRA_ARGS="-cookie-secure"
fi

cat > "$UNIT_FILE" <<EOF
[Unit]
Description=Server Inspector (服务器巡检系统)
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=$SERVICE_USER
Group=$SERVICE_USER
WorkingDirectory=$APP_DIR
EnvironmentFile=-$ENV_FILE
ExecStart=$APP_DIR/$APP_NAME -addr $ADDR -data $DATA_DIR $EXTRA_ARGS
Restart=on-failure
RestartSec=3s
KillSignal=SIGTERM
TimeoutStopSec=15s
LimitNOFILE=65535
$HARDENING

[Install]
WantedBy=multi-user.target
EOF

systemctl daemon-reload
systemctl enable "$APP_NAME" >/dev/null 2>&1
log "systemd 服务已安装：$UNIT_FILE"

if [ "$AUTO_START" -eq 0 ]; then
    log "已跳过启动。可执行：systemctl start $APP_NAME"
    log "查看初始密码：journalctl -u $APP_NAME -n 50 --no-pager"
    exit 0
fi

log "正在启动服务 ..."
if [ -n "$ADMIN_PASS" ]; then
    printf '\n  管理员初始密码：%s\n\n' "$ADMIN_PASS"
fi
if ! systemctl restart "$APP_NAME"; then
    err "服务启动失败，最近日志："
    journalctl -u "$APP_NAME" -n 40 --no-pager || true
    exit 1
fi

# 等待服务就绪
i=0
while [ "$i" -lt 15 ]; do
    if systemctl is-active --quiet "$APP_NAME"; then
        break
    fi
    i=$((i + 1))
    sleep 1
done
if ! systemctl is-active --quiet "$APP_NAME"; then
    err "服务未能正常启动，最近日志："
    journalctl -u "$APP_NAME" -n 40 --no-pager || true
    exit 1
fi

sleep 1

HOST_IP=$(hostname -I 2>/dev/null | awk '{print $1}')
[ -n "$HOST_IP" ] || HOST_IP="<服务器IP>"

printf '\n'
printf '============================================================\n'
printf '  服务器巡检系统安装完成\n'
printf '  程序路径  : %s\n' "$APP_DIR/$APP_NAME"
printf '  数据目录  : %s\n' "$DATA_DIR"
printf '  服务名称  : %s.service\n' "$APP_NAME"
printf '  访问地址  : http://%s:%s\n' "$HOST_IP" "$PORT"
if [ -n "$ADMIN_PASS" ]; then
    printf '  管理员账号: admin\n'
    printf '  初始密码  : %s\n' "$ADMIN_PASS"
    printf '  ⚠ 初始密码仅显示本次，请登录后点击右上角「修改密码」。\n'
    rm -f "$ENV_FILE"
    ENV_FILE_CREATED=0
else
    printf '  管理员账号: 已存在（升级安装，密码保持不变）\n'
    rm -f "$ENV_FILE"
fi
printf '============================================================\n'
printf '\n'
printf '常用命令：\n'
printf '  查看状态: systemctl status %s\n' "$APP_NAME"
printf '  查看日志: journalctl -u %s -f\n' "$APP_NAME"
printf '  重启服务: systemctl restart %s\n' "$APP_NAME"
printf '  健康检查: curl http://127.0.0.1:%s/healthz\n' "$PORT"
printf '  卸载程序: sudo bash install.sh --uninstall\n'
printf '\n'

if command -v ufw >/dev/null 2>&1 && ufw status 2>/dev/null | grep -q 'Status: active'; then
    warn "检测到 UFW 防火墙已启用，如无法访问请执行：ufw allow $PORT/tcp"
fi
if command -v firewall-cmd >/dev/null 2>&1 && systemctl is-active --quiet firewalld; then
    warn "检测到 firewalld 已启用，如无法访问请执行："
    warn "  firewall-cmd --permanent --add-port=$PORT/tcp && firewall-cmd --reload"
fi
if command -v getenforce >/dev/null 2>&1 && [ "$(getenforce 2>/dev/null)" = "Enforcing" ]; then
    warn "SELinux 处于 Enforcing 模式，若服务无法启动可尝试：restorecon -Rv $APP_DIR"
fi
warn "公网环境请务必通过 Nginx/Caddy 配置 HTTPS，并使用 --addr 127.0.0.1:8080 仅监听本机。"

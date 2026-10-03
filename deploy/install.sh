#!/usr/bin/env bash
# merlin-mcp 安装/升级脚本。
# 以普通用户身份运行，不需要 sudo；不会启动或重启服务，不会删除任何文件。
# 用法：bash install.sh [--prefix 安装目录]，默认安装到 /opt/merlin-mcp
set -euo pipefail

DEFAULT_PREFIX=/opt/merlin-mcp
PREFIX="$DEFAULT_PREFIX"
usage() { echo "用法: bash install.sh [--prefix 安装目录]（默认 $DEFAULT_PREFIX）"; }

while [ $# -gt 0 ]; do
  case "$1" in
    --prefix)
      [ $# -ge 2 ] || { usage >&2; exit 2; }
      PREFIX="$2"
      shift 2
      ;;
    --prefix=*)
      PREFIX="${1#--prefix=}"
      shift
      ;;
    -h | --help)
      usage
      exit 0
      ;;
    *)
      echo "未知参数: $1" >&2
      usage >&2
      exit 2
      ;;
  esac
done

if [ "$(id -u)" -eq 0 ]; then
  echo "请不要用 root 运行：merlin-mcp 以当前用户的 systemd 用户服务运行。" >&2
  exit 1
fi

# 安装目录会写进服务文件和配置文件，只允许安全字符
if ! printf '%s' "$PREFIX" | grep -qE '^/[A-Za-z0-9._/-]+$'; then
  echo "安装目录必须是只含字母、数字和 ._/- 的绝对路径: $PREFIX" >&2
  exit 2
fi
PREFIX="${PREFIX%/}"

if [ ! -d "$PREFIX" ] || [ ! -w "$PREFIX" ]; then
  cat >&2 <<EOF
安装目录 $PREFIX 不存在，或当前用户 $(id -un) 没有写权限。
请先执行一次（需要 sudo）:
  sudo install -d -o $(id -un) -g $(id -gn) -m 0755 $PREFIX
EOF
  exit 1
fi

SRC_DIR="$(cd "$(dirname "$0")" && pwd)"
BIN_DIR="$PREFIX/bin"
CONF_DIR="$PREFIX/config"
STATE_DIR="$PREFIX/state"
UNIT_DIR="$HOME/.config/systemd/user"

mkdir -p "$BIN_DIR" "$UNIT_DIR" "$CONF_DIR" "$STATE_DIR"
chmod 0755 "$BIN_DIR"
chmod 0700 "$CONF_DIR" "$STATE_DIR"

# render 把模板中的 @PREFIX@ 替换为实际安装目录
render() { sed "s#@PREFIX@#$PREFIX#g" "$1"; }

if [ -f "$BIN_DIR/merlin-mcp" ]; then
  cp -p "$BIN_DIR/merlin-mcp" "$BIN_DIR/merlin-mcp.prev"
  echo "已备份旧版本: $BIN_DIR/merlin-mcp.prev"
fi
cp "$SRC_DIR/merlin-mcp" "$BIN_DIR/merlin-mcp.new"
chmod 0755 "$BIN_DIR/merlin-mcp.new"
mv -f "$BIN_DIR/merlin-mcp.new" "$BIN_DIR/merlin-mcp"

# systemd 只从用户目录加载用户服务，服务文件放在 ~/.config/systemd/user
render "$SRC_DIR/merlin-mcp.service" >"$UNIT_DIR/merlin-mcp.service.new"
chmod 0644 "$UNIT_DIR/merlin-mcp.service.new"
mv -f "$UNIT_DIR/merlin-mcp.service.new" "$UNIT_DIR/merlin-mcp.service"

if [ -f "$CONF_DIR/config.yaml" ]; then
  echo "配置文件已存在，未覆盖: $CONF_DIR/config.yaml"
else
  (umask 077 && render "$SRC_DIR/config.example.yaml" >"$CONF_DIR/config.yaml")
  chmod 0600 "$CONF_DIR/config.yaml"
  echo "已创建配置文件，请编辑: $CONF_DIR/config.yaml"
fi

systemctl --user daemon-reload
echo "已安装版本: $("$BIN_DIR/merlin-mcp" version)"

cat <<EOF

安装目录: $PREFIX
后续步骤（请手动执行）:
  1. 首次安装时执行一次，让服务开机自动启动（需要 sudo）:
       sudo loginctl enable-linger $(id -un)
  2. 编辑配置:            \$EDITOR $CONF_DIR/config.yaml
  3. 检查配置和路由器连接: $BIN_DIR/merlin-mcp check --config $CONF_DIR/config.yaml
  4. 首次安装:            systemctl --user enable --now merlin-mcp
     升级:                systemctl --user restart merlin-mcp
  5. 查看日志:            journalctl --user -u merlin-mcp -f
  回滚:                   mv $BIN_DIR/merlin-mcp.prev $BIN_DIR/merlin-mcp && systemctl --user restart merlin-mcp
EOF

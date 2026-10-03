#!/usr/bin/env bash
# merlin-mcp 安装/升级脚本。
# 以普通用户身份运行，不需要 sudo；不会启动或重启服务，不会删除任何文件。
set -euo pipefail

if [ "$(id -u)" -eq 0 ]; then
  echo "请不要用 root 运行：merlin-mcp 以当前用户的 systemd 用户服务运行。" >&2
  exit 1
fi

SRC_DIR="$(cd "$(dirname "$0")" && pwd)"
BIN_DIR="$HOME/.local/bin"
CONF_DIR="$HOME/.config/merlin-mcp"
STATE_DIR="$HOME/.local/state/merlin-mcp"
UNIT_DIR="$HOME/.config/systemd/user"

install -d -m 0755 "$BIN_DIR" "$UNIT_DIR"
install -d -m 0700 "$CONF_DIR" "$STATE_DIR"

if [ -f "$BIN_DIR/merlin-mcp" ]; then
  cp -p "$BIN_DIR/merlin-mcp" "$BIN_DIR/merlin-mcp.prev"
  echo "已备份旧版本: $BIN_DIR/merlin-mcp.prev"
fi
install -m 0755 "$SRC_DIR/merlin-mcp" "$BIN_DIR/merlin-mcp.new"
mv -f "$BIN_DIR/merlin-mcp.new" "$BIN_DIR/merlin-mcp"

install -m 0644 "$SRC_DIR/merlin-mcp.service" "$UNIT_DIR/merlin-mcp.service"

if [ -f "$CONF_DIR/config.yaml" ]; then
  echo "配置文件已存在，未覆盖: $CONF_DIR/config.yaml"
else
  install -m 0600 "$SRC_DIR/config.example.yaml" "$CONF_DIR/config.yaml"
  echo "已创建配置文件，请编辑: $CONF_DIR/config.yaml"
fi

systemctl --user daemon-reload
echo "已安装版本: $("$BIN_DIR/merlin-mcp" version)"

cat <<EOF

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

#!/usr/bin/env bash
# install.sh 的测试：在临时目录中模拟安装，systemctl 用假命令代替。
# 用法：bash scripts/test_install.sh（Linux 和 Git Bash 都能运行；权限位只在 Linux 上检查）
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
T="$(mktemp -d)"
trap 'rm -rf "$T"' EXIT # 只清理本脚本创建的临时目录

fail() {
  echo "FAIL: $*" >&2
  exit 1
}

# 模拟发布包：真实的 install.sh、服务文件和示例配置，加一个假的二进制
PKG="$T/pkg"
mkdir -p "$PKG"
cp "$ROOT/deploy/install.sh" "$ROOT/deploy/merlin-mcp.service" "$ROOT/deploy/config.example.yaml" "$PKG/"
printf '#!/bin/sh\necho v-test\n' >"$PKG/merlin-mcp"
chmod +x "$PKG/merlin-mcp"

# 假的 systemctl：只记录调用
mkdir -p "$T/fakebin"
printf '#!/bin/sh\necho "systemctl $*" >> "%s/systemctl.log"\n' "$T" >"$T/fakebin/systemctl"
chmod +x "$T/fakebin/systemctl"
export PATH="$T/fakebin:$PATH"
export HOME="$T/home"
mkdir -p "$HOME"
P="$T/opt/merlin-mcp"
UNIT="$HOME/.config/systemd/user/merlin-mcp.service"

# 1. 安装目录不存在时失败，并提示需要用 sudo 执行的命令
if out=$(bash "$PKG/install.sh" --prefix "$P" 2>&1); then
  fail "安装目录不存在时应失败"
fi
echo "$out" | grep -q "sudo install -d" || fail "应提示 sudo 创建目录的命令，实际输出: $out"
[ ! -e "$UNIT" ] || fail "失败时不应写入服务文件"

# 2. 正常安装
mkdir -p "$P"
bash "$PKG/install.sh" --prefix "$P" >"$T/install.log"
[ -x "$P/bin/merlin-mcp" ] || fail "二进制未安装到 $P/bin"
[ -f "$P/config/config.yaml" ] || fail "配置文件未生成"
[ -d "$P/state" ] || fail "状态目录未创建"
grep -qF "ExecStart=$P/bin/merlin-mcp serve --config $P/config/config.yaml" "$UNIT" || fail "服务文件路径不正确: $(cat "$UNIT")"
grep -qF "state_dir: $P/state" "$P/config/config.yaml" || fail "配置中的 state_dir 不正确"
grep -qF "audit_log: $P/state/audit.jsonl" "$P/config/config.yaml" || fail "配置中的 audit_log 不正确"
if grep -rq "@PREFIX@" "$P" "$UNIT"; then fail "占位符 @PREFIX@ 未替换"; fi
grep -q "daemon-reload" "$T/systemctl.log" || fail "应执行 systemctl --user daemon-reload"
grep -q "v-test" "$T/install.log" || fail "应输出已安装的版本"

# 3. 升级：保留旧版本，不覆盖用户修改过的配置
echo "# 用户修改" >>"$P/config/config.yaml"
bash "$PKG/install.sh" --prefix "$P" >/dev/null
[ -x "$P/bin/merlin-mcp.prev" ] || fail "升级时应保留旧版本 merlin-mcp.prev"
grep -q "# 用户修改" "$P/config/config.yaml" || fail "升级时不应覆盖已有配置"

# 4. 权限（Windows 上权限位是模拟的，只在 Linux 上检查）
if [ "$(uname -s)" = Linux ]; then
  [ "$(stat -c %a "$P/bin/merlin-mcp")" = 755 ] || fail "二进制权限应为 755"
  [ "$(stat -c %a "$P/config")" = 700 ] || fail "config 目录权限应为 700"
  [ "$(stat -c %a "$P/config/config.yaml")" = 600 ] || fail "config.yaml 权限应为 600"
  [ "$(stat -c %a "$P/state")" = 700 ] || fail "state 目录权限应为 700"
fi

# 5. 非法的安装目录被拒绝，且不产生任何文件
for bad in "relative/path" "/opt/a b" "/opt/x;rm"; do
  if bash "$PKG/install.sh" --prefix "$bad" >/dev/null 2>&1; then
    fail "非法安装目录应被拒绝: $bad"
  fi
done

# 6. 默认安装目录是 /opt/merlin-mcp
bash "$PKG/install.sh" --help | grep -q "/opt/merlin-mcp" || fail "帮助信息应说明默认目录 /opt/merlin-mcp"

echo "install.sh 测试全部通过"

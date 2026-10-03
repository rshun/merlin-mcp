# merlin-mcp

管理 Asuswrt-Merlin 路由器的 MCP Server。运行在与路由器同一网段的 Debian 上，通过 SSH 连接路由器，供本机的 Claude Code 使用。

## 功能

| 工具 | 类型 | 说明 |
|---|---|---|
| `syslog_read` | 只读 | 读取 syslog，支持按时间、进程、关键字过滤 |
| `kernel_log_read` | 只读 | 读取 dmesg |
| `system_status` | 只读 | 型号、固件、负载、CPU、内存、温度、今日重启额度 |
| `wan_status` | 只读 | WAN 状态和最近的 WAN 事件 |
| `clients_list` | 只读 | 客户端列表（租约 + ARP + 静态分配 + 无线关联） |
| `conntrack_status` | 只读 | 连接跟踪表使用率 |
| `network_diagnose` | 只读 | 从路由器发起 ping / nslookup |
| `dnsmasq_addfile_read` | 只读 | 读取 dnsmasq.conf.add |
| `dnsmasq_effective_config` | 只读 | 读取实际生效的 /etc/dnsmasq.conf |
| `dnsmasq_addfile_add` / `remove` | 修改 | 按行增删，自动备份，支持 dry_run |
| `dnsmasq_apply` | 修改 | 校验 → 重启 dnsmasq → 检查，失败自动回滚 |
| `router_reboot` | 破坏性 | 重启路由器，每天最多 1 次 |

修改类工具只有在配置 `allow_mutations: true` 时才会注册。

## 安装

1. 在 GitHub 仓库的 [Releases](https://github.com/rshun/merlin-mcp/releases) 页面下载 `merlin-mcp_<版本>_linux_amd64.tar.gz` 和 `SHA256SUMS`
2. 上传到 Debian，例如：

   ```bash
   scp merlin-mcp_v0.2.0_linux_amd64.tar.gz SHA256SUMS your_user@your_debian_host:~/
   ```

   如果 Debian 能访问 GitHub，也可以在 Debian 上直接下载：

   ```bash
   curl -LO https://github.com/rshun/merlin-mcp/releases/download/v0.2.0/merlin-mcp_v0.2.0_linux_amd64.tar.gz
   curl -LO https://github.com/rshun/merlin-mcp/releases/download/v0.2.0/SHA256SUMS
   ```

3. 首次安装前，用 sudo 创建安装目录并交给 MCP 的运行用户（只需执行一次）：

   ```bash
   sudo install -d -o rshun -g rshun -m 0755 /opt/merlin-mcp
   ```

4. 在 Debian 上，以 MCP 的运行用户校验、解压并安装（不需要 sudo）：

   ```bash
   sha256sum -c SHA256SUMS
   tar -xzf merlin-mcp_v0.2.0_linux_amd64.tar.gz
   bash merlin-mcp_v0.2.0_linux_amd64/install.sh
   ```

   默认安装到 `/opt/merlin-mcp`，可用 `--prefix <目录>` 指定其他目录：

   ```
   /opt/merlin-mcp/
   ├── bin/merlin-mcp          程序（升级时保留 merlin-mcp.prev）
   ├── config/config.yaml      配置
   └── state/                  状态文件与审计日志 audit.jsonl
   ```

   systemd 用户服务文件放在 `~/.config/systemd/user/merlin-mcp.service`。

5. 按脚本最后打印的步骤完成配置

## 发布新版本（维护者）

推送 `v*` tag 后，GitHub Actions（`.github/workflows/release.yml`）会运行检查和测试、构建发布包并创建 Release：

```bash
git tag v0.2.0
git push origin v0.2.0
```

本地构建（开发测试用，需要 Go 1.26）：`bash scripts/build.sh v0.2.0-dev`

## 接入 Claude Code

```bash
claude mcp add --scope user --transport http merlin http://127.0.0.1:8765/mcp
```

## 安全说明

- 只监听回环地址，没有鉴权
- 不提供任意命令执行；所有命令都是代码中写死的模板
- 状态类工具不读取任何密码类 nvram 字段
- 修改类操作写入审计日志 `/opt/merlin-mcp/state/audit.jsonl`
- MCP 的限制用于防止误操作，不能防止 AI 绕过 MCP，详见 `docs/superpowers/specs/2026-10-03-merlin-mcp-design.md` 第 12 节

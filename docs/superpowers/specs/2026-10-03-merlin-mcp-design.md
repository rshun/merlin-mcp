# merlin-mcp 设计文档

- 日期：2026-10-03
- 状态：待审阅
- 分支：`dev`

## 1. 目标与背景

开发一个 MCP Server，让运行在 Debian 服务器上的 Claude Code 能够管理一台运行 Asuswrt-Merlin 固件的 Asus 路由器，用于：

1. 读取日志，辅助 AI 分析路由器问题
2. 查看系统、WAN、客户端等运行状态
3. 管理 `/jffs/configs/dnsmasq.conf.add`（读取、添加、删除、生效）
4. 重启路由器（每天最多 1 次）

### 1.1 已确认的约束

| 约束 | 内容 |
|---|---|
| 运行位置 | Debian 服务器（amd64），与路由器同一网段 |
| 客户端 | Claude Code，运行在同一台 Debian 上 |
| 传输方式 | MCP Streamable HTTP，只监听 `127.0.0.1` |
| 连接路由器 | SSH，密钥文件认证 |
| 路由器侧 | 不安装任何东西 |
| 交付形式 | 不做源码部署。交付 Go 静态单文件二进制 + systemd 服务 |
| 管理对象 | 一台路由器，不考虑 AiMesh 节点或多台路由器 |

### 1.2 成功标准

- Claude Code 能通过 13 个工具完成上述四类工作
- 任何情况下，同一个自然日内由 MCP 发起的重启不超过 1 次
- dnsmasq 配置生效失败时自动回滚，不会让局域网 DNS 长时间不可用
- AI 无法通过本 MCP 在路由器上执行任意命令
- 代码、配置样例、文档、日志、审计日志中不出现任何密钥或密码

## 2. 方案选择

参考了以下同类项目：kcsoukup/asus-merlin-mcp、X1pheR/asuswrt-mcp、stanislav-testhub/openwrt-mcp、Sogl/mikrotik-rest-mcp、paulomac1000/openwrt-mcp、beck-8/istoreos-mcp。

采用**方案 A：固定工具集 + SSH 长连接**。

- 只提供预先定义的工具，命令由代码中写死的模板生成
- **不提供**任意命令执行、`nvram set`、文件上传/下载、通用文件读取工具（kcsoukup 的项目因为提供了这些能力，被第三方安全评分评为 19/100）

从同类项目中借鉴的设计：

1. 不提供任意命令执行（X1pheR）
2. 修改类工具双重保护：配置开关 + `confirm: true`（X1pheR）
3. 修改支持 `dry_run`，返回 diff（X1pheR）
4. 强制校验 SSH host key（X1pheR）
5. JSONL 审计日志，敏感字段脱敏（stanislav、paulomac1000）
6. 只读模式下修改类工具不注册；工具带 `readOnlyHint` / `destructiveHint` 注解（Sogl）
7. 读取类工具支持过滤和条数限制，节省 token（Sogl、paulomac1000）
8. 状态类工具只读取白名单内的 nvram 键，不读取密码类字段（X1pheR）

明确不借鉴：90 秒确认窗口式的自动回滚（stanislav），以及几十个工具的大而全设计。

## 3. 架构

```
Claude Code
   │  HTTP POST http://127.0.0.1:<port>/mcp   （MCP Streamable HTTP）
   ▼
merlin-mcp（Debian，systemd 服务，用户 merlin-mcp）
   ├─ tools     参数校验、权限检查（allow_mutations / confirm）、审计日志
   ├─ 业务模块   syslog / status / clients / diagnose / dnsmasq / reboot
   └─ sshx      SSH 长连接、host key 校验、命令执行、stdin 传输文件内容
   │  SSH（密钥认证）
   ▼
Asus Merlin 路由器
```

### 3.1 目录结构

```
merlin-mcp/
├── cmd/merlin-mcp/main.go     # 子命令 serve / check / version；组装模块；信号处理
├── internal/
│   ├── config/    # 加载与校验 YAML 配置
│   ├── sshx/      # SSH 客户端，实现 Runner 接口
│   ├── shell/     # Quote() 单引号转义；域名/IP/进程名/关键字校验
│   ├── syslog/    # 日志解析与过滤（纯函数）
│   ├── status/    # 系统、WAN、conntrack 状态采集与解析
│   ├── clients/   # 合并租约、ARP、静态分配、无线关联列表（纯函数）
│   ├── diagnose/  # ping / nslookup
│   ├── dnsmasq/   # 按行增删与 diff（纯函数）+ apply/回滚流程
│   ├── reboot/    # 每日重启额度：状态文件 + 可注入时钟
│   ├── state/     # Debian 侧状态文件读写（原子写入 + fsync）
│   ├── audit/     # JSONL 审计日志，敏感字段脱敏
│   └── tools/     # MCP 工具注册：schema、注解、调用业务模块、错误映射
├── deploy/        # merlin-mcp.service、config.example.yaml、install.sh
├── scripts/build.sh
└── testdata/      # 真实路由器输出样本（已脱敏）
```

### 3.2 核心接口

所有业务模块只依赖 `Runner` 接口，测试时用假实现替换：

```go
type Result struct {
    Stdout   []byte
    Stderr   []byte
    ExitCode int
}

type Runner interface {
    Run(ctx context.Context, cmd string, stdin []byte) (Result, error)
}
```

解析逻辑与执行逻辑分离：解析函数输入原始文本、输出结构体，可直接用 `testdata/` 中的真实样本做单元测试。

### 3.3 依赖

| 依赖 | 用途 |
|---|---|
| `github.com/modelcontextprotocol/go-sdk` | 官方 Go MCP SDK，Streamable HTTP |
| `golang.org/x/crypto/ssh`、`golang.org/x/crypto/ssh/knownhosts` | SSH 客户端与 host key 校验 |
| `gopkg.in/yaml.v3` | 配置文件 |

其余使用 Go 标准库。依赖的 `go get` 命令在实施计划中列出，由用户确认后执行。

## 4. 工具清单

共 13 个工具：9 个只读，3 个修改类，1 个破坏性。

### 4.1 日志分析（只读）

#### `syslog_read`

| 参数 | 类型 | 默认 | 说明 |
|---|---|---|---|
| `lines` | int | 200 | 返回的最大行数，范围 1–2000 |
| `keyword` | string | 空 | 不区分大小写的子串匹配 |
| `process` | string | 空 | 进程名，匹配 `name:` 或 `name[pid]:`，只允许 `[A-Za-z0-9_.-]` |
| `since` | string | 空 | 相对时间：`<数字><m\|h\|d>`，例如 `30m`、`2h`、`1d`，最大 `7d` |
| `include_rotated` | bool | false | 是否同时读取轮转出去的 `<syslog>-1` 文件 |

- 日志路径：配置为 `auto` 时，先找 `/jffs/syslog.log`，不存在再找 `/tmp/syslog.log`
- 文件在 Debian 侧过滤：最多读取 4MB（超出时只读取文件末尾 4MB）
- 过滤顺序：时间 → 进程 → 关键字，按时间顺序取最后 `lines` 行
- `since` 相对于**路由器当前时间**计算（通过 `date +%s` 获取）。syslog 时间戳没有年份，按"不晚于路由器当前时间的最近一个日期"推断
- 无法解析时间戳的行，保留并归入前一行的时间
- 返回内容上限 64KB，超出时截断，并在结果中注明 `truncated: true`

#### `kernel_log_read`

| 参数 | 类型 | 默认 | 说明 |
|---|---|---|---|
| `lines` | int | 200 | 范围 1–2000 |
| `keyword` | string | 空 | 不区分大小写的子串匹配 |

执行 `dmesg`，在 Debian 侧过滤后取最后 `lines` 行。返回上限同为 64KB。

### 4.2 状态监控（只读）

#### `system_status`

无参数。返回：

- 型号、固件版本（nvram 白名单键：`productid`、`firmver`、`buildno`、`extendno`）
- 运行时长、1/5/15 分钟负载（`/proc/uptime`、`/proc/loadavg`）
- CPU 使用率：间隔 1 秒采样两次 `/proc/stat` 计算
- 内存：总量、可用、已用百分比（`/proc/meminfo`）
- CPU 温度：按顺序尝试已知来源，全部失败则返回 `null`，不报错
- 重启额度：`reboot_allowed_today`、`last_mcp_reboot_at`（来自 Debian 状态文件）

#### `wan_status`

无参数。返回：

- 连接状态、协议类型、WAN IP、网关、DNS（只读取白名单 nvram 键，例如 `wan0_state_t`、`wan0_proto`、`wan0_ipaddr`、`wan0_gateway`、`wan0_dns`）
- 最近 20 条 WAN 连接事件：从 syslog 中按 WAN 相关模式匹配得到。匹配模式在代码中定义，根据真实日志样本确定

**禁止读取**：`wan0_pppoe_passwd`、`wan_pppoe_passwd` 以及任何名称含 `passwd`、`password`、`key`、`psk`、`secret` 的键。

#### `clients_list`

无参数。以 MAC 地址为主键合并以下数据：

| 来源 | 位置 |
|---|---|
| DHCP 租约 | `/var/lib/misc/dnsmasq.leases` |
| ARP 表 | `/proc/net/arp` |
| 静态 IP 分配 | nvram `dhcp_staticlist` |
| 无线关联 | 对 nvram `wl_ifnames` 中的每个接口执行 `wl -i <if> assoclist` |

每台设备返回：MAC、IP、主机名、连接方式（`wired` / `2.4G` / `5G` / `6G` / `unknown`）、是否静态分配、租约剩余秒数、是否在 ARP 表中。

连接方式判断：在某个无线接口的关联列表中出现 → 该接口对应的频段；不在任何无线列表但在 ARP 中 → `wired`；其他情况 → `unknown`。

#### `conntrack_status`

无参数。读取 `/proc/sys/net/netfilter/nf_conntrack_count` 和 `nf_conntrack_max`，返回当前连接数、最大连接数、使用率。使用率 ≥ 80% 时附加 `warning`。

### 4.3 网络诊断（只读）

#### `network_diagnose`

| 参数 | 类型 | 默认 | 说明 |
|---|---|---|---|
| `action` | enum | 必填 | `ping` 或 `nslookup` |
| `target` | string | 必填 | 只接受合法域名（RFC 1123，最长 253 字符）、IPv4 或 IPv6 |
| `count` | int | 3 | 仅 ping 使用，范围 1–5 |
| `server` | string | `127.0.0.1` | 仅 nslookup 使用，只接受 IPv4 或 IPv6 |

- ping：`ping -c <count> -W 2 <target>`
- nslookup：`nslookup <target> <server>`
- 返回原始输出，以及解析出的摘要（丢包率、平均延迟 / 解析得到的地址）

### 4.4 dnsmasq 管理

文件路径由配置 `paths.dnsmasq_add` 指定，默认 `/jffs/configs/dnsmasq.conf.add`。

#### 行的规范化规则（add 与 remove 共用）

- 去除每行末尾的空白和 `\r`
- 行内包含 `\n`、`\r` 或 NUL 字符时拒绝（`INVALID_ARGUMENT`）
- 空行拒绝
- 允许 `#` 开头的注释行
- 判断"相同"时，比较规范化之后的行内容是否完全一致
- 写入时保证文件以换行符结尾

#### `dnsmasq_addfile_read`（只读）

无参数。返回 `exists`、`sha256`、带行号的内容。文件不存在时返回 `exists: false` 和空内容，不报错。

#### `dnsmasq_effective_config`（只读）

| 参数 | 类型 | 默认 | 说明 |
|---|---|---|---|
| `keyword` | string | 空 | 不区分大小写的子串匹配 |

读取 `/etc/dnsmasq.conf`（Merlin 合并 `.add` 之后实际生成的配置），返回带行号的内容。

#### `dnsmasq_addfile_add`（修改类）

| 参数 | 类型 | 默认 | 说明 |
|---|---|---|---|
| `lines` | string[] | 必填 | 1–100 行 |
| `dry_run` | bool | false | 为 true 时只返回 diff，不写入 |

- 已存在的行跳过，结果中列出 `skipped`
- 文件不存在时创建
- 写入前把当前内容备份到 `backup_dir`
- 返回 `added`、`skipped`、diff、新文件的 `sha256`、`pending_apply: true`
- **只修改文件，不会生效**

#### `dnsmasq_addfile_remove`（修改类）

| 参数 | 类型 | 默认 | 说明 |
|---|---|---|---|
| `lines` | string[] | 必填 | 1–100 行 |
| `dry_run` | bool | false | 为 true 时只返回 diff，不写入 |

- 删除所有完全匹配的行；找不到的行在结果中列为 `not_found`，不报错
- 其余行为与 `dnsmasq_addfile_add` 相同

#### `dnsmasq_apply`（修改类）

| 参数 | 类型 | 默认 | 说明 |
|---|---|---|---|
| `confirm` | bool | 必填 | 必须为 true |
| `accept_external_changes` | bool | false | 检测到外部修改时，是否仍然继续 |

流程见 5.3 节。

### 4.5 重启

#### `router_reboot`（破坏性）

| 参数 | 类型 | 默认 | 说明 |
|---|---|---|---|
| `confirm` | bool | 必填 | 必须为 true |

流程见 5.4 节。

### 4.6 通用规则

- `allow_mutations: false` 时，`dnsmasq_addfile_add`、`dnsmasq_addfile_remove`、`dnsmasq_apply`、`router_reboot` 四个工具不注册
- 只读工具加 `readOnlyHint: true`；`router_reboot` 加 `destructiveHint: true`；`dnsmasq_apply` 加 `destructiveHint: false`、`idempotentHint: false`
- `dry_run` 不需要 `confirm`；`dnsmasq_addfile_add` / `remove` 不需要 `confirm`（因为它们不会生效）；`dnsmasq_apply` 和 `router_reboot` 必须传 `confirm: true`
- 所有修改类和破坏性工具的调用（包括被拒绝的）都写入审计日志；`dry_run` 不写

## 5. 关键流程

### 5.1 命令安全

- 所有命令都是代码中写死的模板
- 用户传入的值必须先经过 `shell` 模块校验，再经 `shell.Quote()` 转成单引号字符串后才能拼入命令。`Quote()` 将 `'` 替换为 `'\''`
- 写文件时，内容通过 stdin 传输，不出现在命令行中：

  ```sh
  cat > '<path>.mcp-tmp' && mv '<path>.mcp-tmp' '<path>'
  ```

- `keyword`、`process` 等过滤参数只在 Debian 侧使用，不进入命令行

### 5.2 SSH 连接管理

- 第一次调用工具时建立连接，之后复用；每 30 秒发送一次 `keepalive@openssh.com`
- 每次调用开一个新的 session；用信号量限制同时最多 4 个 session
- 连接断开时：只读工具自动重连并重试 1 次；修改类和破坏性工具**不自动重试**，直接返回错误
- 超时：普通命令使用 `router.command_timeout`（默认 15s）；ping 30s；`dnsmasq_apply` 整体 60s
- host key：只接受 `router.known_hosts` 中已有的记录；不存在或不一致都拒绝连接

### 5.3 `dnsmasq_apply` 流程

**Debian 侧状态**（`state_dir/state.json` 中的 `dnsmasq` 部分）：

| 字段 | 含义 |
|---|---|
| `known_good_backup` | 已知可用版本的备份文件名，用于回滚 |
| `last_mcp_sha256` | MCP 最后一次写入或成功 apply 后文件的 sha256 |

**`known_good_backup` 的维护：**

- MCP 第一次接触该文件（第一次 add / remove / apply）时，把当前文件备份一份，作为初始的 known good（假设当前文件就是正在生效的版本）
- 每次 apply 成功后，把刚刚生效的内容备份一份，作为新的 known good
- 备份清理时，`known_good_backup` 指向的文件永远不删除

**外部修改检测：**

- 如果当前文件的 sha256 与 `last_mcp_sha256` 不一致，说明有人绕过 MCP 修改过文件
- add / remove 遇到这种情况继续执行，但在结果中返回 `external_change_detected: true`
- apply 遇到这种情况时，`accept_external_changes` 不为 true 就返回 `ADDFILE_CHANGED_EXTERNALLY`，不做任何操作

**apply 步骤：**

```
加锁（与 add / remove 共用一把锁）
 0. 检查外部修改（见上）
 1. 语法校验：把当前 .add 内容写到 /tmp/merlin-mcp-test.conf，执行
       dnsmasq --test -C /tmp/merlin-mcp-test.conf
    失败 → 返回 DNSMASQ_VALIDATION_FAILED（不重启，线上配置不受影响）
 2. service restart_dnsmasq
 3. 每 0.5 秒执行一次 pidof dnsmasq，最多等待 5 秒
 4. nslookup <health_check_domain> 127.0.0.1（默认 router.asus.com）
 ✔ 全部成功 → 备份当前内容作为新的 known good，更新 last_mcp_sha256，写审计日志
 ✘ 第 2–4 步任一失败：
      a. 把 known_good_backup 的内容写回 .add 文件
      b. service restart_dnsmasq，并重复第 3–4 步检查
      c. 成功 → 返回 DNSMASQ_ROLLED_BACK，附原始错误
         失败 → 返回 DNSMASQ_ROLLBACK_FAILED，附原始错误、回滚错误、备份文件路径
```

第 1 步只校验 `.add` 本身的语法，无法发现它与主配置之间的冲突；这类问题由第 2–4 步的运行时检查兜底。

**备份：**

- 目录：`paths.backup_dir`，默认 `/jffs/merlin-mcp/backups/`，不存在时自动创建
- 文件名：`dnsmasq.conf.add.<YYYYMMDD-HHMMSS>`（路由器本地时间，同一秒内有多份时追加 `-2`、`-3`）
- 保留最近 `dnsmasq.backup_keep`（默认 20）份；`known_good_backup` 不计入清理

### 5.4 `router_reboot` 流程

```
加锁
 1. confirm 不为 true → CONFIRM_REQUIRED
 2. 读取 state.json 中的 reboot 记录；按 reboot.timezone 计算"今天"的日期
    今天由 MCP 发起的重启次数 ≥ max_per_day → REBOOT_QUOTA_EXCEEDED（附上次重启时间）
 3. 写入重启记录（原子写入 + fsync），写审计日志
 4. 在路由器上执行：( sleep 2; reboot ) >/dev/null 2>&1 &
 5. 主动关闭 SSH 连接，返回"已发出重启指令"
```

- 重启记录在执行命令**之前**落盘：即使第 4 步结果不明确，当天的额度也视为已用
- 只统计由 MCP 发起的重启；在路由器网页上手动重启、断电等情况不计入
- 时区使用配置中的 `reboot.timezone`（例如 `Asia/Shanghai`），不依赖 Debian 系统时区

### 5.5 状态文件

- 路径：`state_dir/state.json`（默认 `/var/lib/merlin-mcp/state.json`）
- 写入方式：写临时文件 → fsync → rename
- 进程内用互斥锁保护；只运行一个服务实例
- 文件损坏或无法解析时：服务启动失败并给出明确原因，不会静默重置（防止重启额度被意外清零）

### 5.6 审计日志

- 路径：`audit_log`（默认 `/var/log/merlin-mcp/audit.jsonl`），每行一个 JSON 对象
- 字段：`ts`、`tool`、`args`、`outcome`（`ok` / `rejected` / `error`）、`error_code`、`duration_ms`、`summary`
- `args` 中名称匹配 `passwd`、`password`、`token`、`secret`、`private_key`、`api_key`（不区分大小写）的字段替换为 `****`。不使用单独的 `key` 作为匹配词，避免误伤 `keyword` 这类参数

## 6. 配置

`/etc/merlin-mcp/config.yaml`（示例只使用占位符）：

```yaml
listen: "127.0.0.1:8765"

router:
  host: your_router_ip
  port: 22
  user: your_router_user
  key_file: /etc/merlin-mcp/id_ed25519
  known_hosts: /etc/merlin-mcp/known_hosts
  command_timeout: 15s

allow_mutations: true

paths:
  dnsmasq_add: /jffs/configs/dnsmasq.conf.add
  backup_dir: /jffs/merlin-mcp/backups
  syslog: auto

dnsmasq:
  health_check_domain: router.asus.com
  backup_keep: 20

reboot:
  timezone: Asia/Shanghai
  max_per_day: 1

state_dir: /var/lib/merlin-mcp
audit_log: /var/log/merlin-mcp/audit.jsonl
```

启动时校验，以下情况直接退出并给出原因：

- 必填项缺失、格式错误
- `listen` 不是回环地址（`127.0.0.0/8` 或 `::1`）。服务没有鉴权，只允许本机访问
- `key_file` 或 `known_hosts` 不存在；`key_file` 的权限宽于 0600
- `reboot.timezone` 无法加载

路由器连不上不影响启动。

## 7. 部署

### 7.1 构建与发布

- 在开发机（Windows，Go 1.26.3）上交叉编译：

  ```sh
  CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "-s -w -X main.version=<版本>" ./cmd/merlin-mcp
  ```

- `scripts/build.sh`（bash，Git Bash 和 Linux 都能运行）生成 `dist/merlin-mcp_<版本>_linux_amd64.tar.gz`，内容如下：

  ```
  merlin-mcp
  config.example.yaml
  merlin-mcp.service
  install.sh
  ```

### 7.2 命令行

| 命令 | 作用 |
|---|---|
| `merlin-mcp serve --config <path>` | 启动 MCP 服务 |
| `merlin-mcp check --config <path>` | 只读检查：配置、SSH 连接、host key、syslog 路径、dnsmasq 文件、`dnsmasq --test` 是否可用 |
| `merlin-mcp version` | 输出版本号 |

HTTP 路由：`/mcp`（MCP Streamable HTTP）、`/healthz`（只返回服务自身状态，不访问路由器）。

### 7.3 systemd

- 专用系统用户 `merlin-mcp`，不能登录
- 密钥复制到 `/etc/merlin-mcp/id_ed25519`，属主 `merlin-mcp`，权限 0600
- `known_hosts` 用 `ssh-keyscan` 生成，需要人工核对指纹
- 服务加固：`NoNewPrivileges=true`、`ProtectSystem=strict`、`ProtectHome=true`、`PrivateTmp=true`，并通过 `StateDirectory=merlin-mcp`、`LogsDirectory=merlin-mcp` 开放写权限
- 运行日志输出到 journald（`journalctl -u merlin-mcp`），不记录密钥、密码或 token

### 7.4 install.sh

由用户审阅后手动执行。负责：创建用户、创建目录、复制二进制和 unit 文件。首次安装时复制示例配置；升级时**不覆盖**已有配置，并把旧二进制保存为 `merlin-mcp.prev`。脚本不删除任何文件。

回滚：把 `merlin-mcp.prev` 换回 `merlin-mcp`，然后执行 `systemctl restart merlin-mcp`。

### 7.5 接入 Claude Code

```bash
claude mcp add --scope user --transport http merlin http://127.0.0.1:8765/mcp
```

## 8. 错误处理

工具失败时返回 `isError: true`，内容为 JSON：`{"code": "...", "message": "<中文说明>", "hint": "<建议的下一步>"}`。

| 错误码 | 场景 |
|---|---|
| `INVALID_ARGUMENT` | 参数校验失败 |
| `CONFIRM_REQUIRED` | 需要 `confirm: true` 但没有传 |
| `SSH_UNREACHABLE` | 无法建立 TCP 连接或连接中断 |
| `SSH_AUTH_FAILED` | 密钥认证失败 |
| `HOSTKEY_MISMATCH` | host key 不在 `known_hosts` 中或与记录不一致 |
| `TIMEOUT` | 命令执行超时 |
| `COMMAND_FAILED` | 路由器上的命令返回非 0，并附 stderr 摘要 |
| `REBOOT_QUOTA_EXCEEDED` | 今天已重启过，附上次重启时间 |
| `ADDFILE_CHANGED_EXTERNALLY` | `.add` 文件被外部修改 |
| `DNSMASQ_VALIDATION_FAILED` | 语法校验失败，未重启 |
| `DNSMASQ_ROLLED_BACK` | 生效失败，已自动回滚 |
| `DNSMASQ_ROLLBACK_FAILED` | 回滚失败，需要人工处理，附备份文件路径 |
| `INTERNAL` | 其他内部错误（如状态文件写入失败） |

错误信息中最多出现密钥文件的路径，不包含任何密钥、密码或 token 的内容。

## 9. 测试

采用 TDD。

1. **单元测试（纯函数）**
   - syslog 解析与过滤：进程、关键字、`since`、跨年推断、无法解析时间戳的行
   - `/proc` 与 nvram 输出解析；客户端合并与连接方式判断
   - dnsmasq 行规范化、增删、diff
   - `shell.Quote` 与参数校验，包括注入用例：`;`、`$()`、反引号、`'`、换行、NUL
   - 重启额度：用假时钟测试跨午夜、时区差异、状态文件损坏
   - 配置校验：非回环 `listen`、密钥权限
2. **流程测试**（用假 Runner 按脚本返回结果）
   - apply：成功；校验失败；重启失败后回滚成功；回滚失败；外部修改检测
   - reboot：额度用完被拒绝；记录在执行命令之前落盘；命令失败仍计入额度
   - 修改类工具在连接断开时不重试；只读工具重试 1 次
   - `allow_mutations: false` 时 4 个工具不注册
3. **真实样本**：由用户在路由器上执行一组只读命令，把输出脱敏（MAC、主机名打码）后提供，存入 `testdata/`，解析器基于真实样本编写
4. **真机验收**
   - 部署后执行 `merlin-mcp check`
   - 在 Claude Code 中逐个调用 9 个只读工具
   - 选一个合适的时间，完整走一遍 `dnsmasq_addfile_add`（加一行注释）→ `dnsmasq_apply` → `dnsmasq_addfile_remove` → `dnsmasq_apply`
   - `router_reboot` 只验证拒绝路径（不传 `confirm`）；是否真实重启由用户决定

## 10. 需要在实施时验证的假设

以下内容依赖具体固件版本，在收集真实样本时确认，每项都有备选方案：

| 假设 | 不成立时的处理 |
|---|---|
| 路由器上的 dnsmasq 支持 `--test` | 跳过第 1 步语法校验，只依赖运行时检查和回滚；`check` 命令会报告这一点 |
| `router.asus.com` 能被本机 dnsmasq 解析 | 改用配置中的其他域名 |
| `wl -i <if> assoclist` 可用 | 连接方式统一返回 `unknown` |
| CPU 温度可从已知路径读取 | 返回 `null` |
| syslog 位于 `/jffs/syslog.log` 或 `/tmp/syslog.log` | 在配置中显式指定路径 |
| busybox `ping` 支持 `-W` | 去掉 `-W`，依靠命令超时 |

## 11. 不在本期范围内

- 流量统计、Wi-Fi 频段状态、存储空间、进程列表、定时任务、VPN、AiMesh、固件更新检查
- 任何其他修改类功能（Wi-Fi 开关、端口转发、nvram 修改、通用服务重启）
- 从备份恢复的工具（手工恢复即可）
- 多台路由器
- `.deb` 打包、Docker 镜像
- 鉴权（只监听回环地址）

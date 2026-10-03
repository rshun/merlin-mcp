# merlin-mcp 实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 用 Go 实现一个 MCP Server（单个静态二进制 + systemd 用户服务），让 Debian 上的 Claude Code 通过 SSH 管理 Asuswrt-Merlin 路由器：读日志、查状态、管理 dnsmasq.conf.add、每天最多重启一次。

**Architecture:** 业务模块只依赖 `runner.Runner` 接口（在路由器上执行一条 shell 命令），`sshx` 用 SSH 长连接实现它，测试用 `runnertest.Fake` 按操作名分发。解析逻辑都是纯函数，单独测试。`tools` 包把业务模块注册成 13 个 MCP 工具，统一处理参数校验、错误 JSON 和审计日志；`cmd/merlin-mcp` 提供 `serve` / `check` / `version` 子命令，通过 Streamable HTTP 监听回环地址。

**Tech Stack:** Go 1.26；`github.com/modelcontextprotocol/go-sdk` v1.8.0；`golang.org/x/crypto/ssh`（含 `knownhosts`）；`gopkg.in/yaml.v3`；其余为标准库。

**Spec:** `docs/superpowers/specs/2026-10-03-merlin-mcp-design.md`

## Global Constraints

- Go 版本：`go.mod` 中写 `go 1.26`；构建使用 `CGO_ENABLED=0 GOOS=linux GOARCH=amd64`
- 依赖仅限 `github.com/modelcontextprotocol/go-sdk@v1.8.0`、`golang.org/x/crypto`、`gopkg.in/yaml.v3@v3.0.1`，其余只用标准库。**执行任何 `go get` 之前必须获得用户确认**（Task 1 第 1 步统一确认）
- 只在 `dev` 分支开发；不删除任何文件；不执行 `git reset --hard`、`git clean`、`git push --force`、`git rebase`、`git checkout .`、`git restore .`
- 所有面向用户或 AI 的文本（错误 message/hint、工具描述、日志、CLI 输出）使用中文，文件一律 UTF-8；`.sh`、`.service`、`.yaml`、`.go`、`.md` 使用 LF 换行（由 `.gitattributes` 保证）
- 代码、测试、文档中不出现真实密钥、真实路由器 IP、真实 MAC。测试数据统一使用文档地址段 `192.0.2.0/24` 和形如 `AA:BB:CC:00:00:01` 的假 MAC
- `listen` 只允许回环地址（`127.0.0.0/8` 或 `::1`）
- 所有远程命令都用 `runner.Op(<操作名>, <脚本>)` 包装；用户传入的值必须先通过 `shell` 包的校验，再经 `shell.Quote` 转义后才能拼进命令；文件内容只通过 stdin 传输
- 错误统一使用 `apperr` 包的错误码（spec 第 8 节）
- 每个任务结束前运行 `go vet ./...` 和 `gofmt -l .`，`gofmt -l .` 输出必须为空
- 每次提交前运行敏感信息检查（见下方"提交步骤模板"）
- 提交信息末尾带 `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`

### 提交步骤模板

每个任务最后一步都按以下方式提交（`<files>` 和提交说明见各任务）：

```bash
git branch --show-current            # 必须输出 dev
git add <files>
git diff --cached | grep -nEi "password|passwd|secret|token|api[_-]?key|private[_-]?key|bearer|cookie|credential"
```

命中的行只允许是标识符或说明文字（例如 nvram 黑名单里的 `passwd`、审计脱敏规则里的 `token`）。出现任何真实密钥立刻停止并报告用户。然后：

```bash
git commit -F - <<'EOF'
<提交说明>

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
EOF
```

## Review Focus

spec 没有逐条写明、但最容易在真实使用中出问题的 5 类输入，每一类都在对应任务里加了测试：

1. **已有的 `.add` 文件是 CRLF 换行**（在 Windows 上编辑过）：`remove` 仍应能匹配到行，写回后统一为 LF → Task 10 `TestRemoveMatchesCRLFFile`
2. **dnsmasq 配置行里有 `$(...)`、反引号、单引号**：内容只能通过 stdin 传输，绝不出现在命令行里 → Task 11 `TestContentTravelsOnlyViaStdin`
3. **路由器时钟未同步或跨年**（1970 年、12 月 31 日的日志在 1 月 1 日读取）：syslog 解析不崩溃，年份推断合理 → Task 6 `TestParseInfersPreviousYear`、`TestParseWithUnsyncedClock`
4. **单条超长日志、多字节字符、非法 UTF-8**：截断后仍是合法 UTF-8 且不超过 64KB → Task 6 `TestRenderKeepsValidUTF8`
5. **租约主机名为 `*`、行格式残缺、`wl` 命令失败**：`clients_list` 正常返回，连接方式降级为 `unknown` → Task 8 `TestParseLeasesSkipsMalformed`、`TestFetchDegradesWhenWlFails`

## 文件结构

```
merlin-mcp/
├── .gitattributes / .gitignore / go.mod / go.sum / README.md
├── cmd/merlin-mcp/main.go, main_test.go          # Task 15
├── internal/
│   ├── apperr/       apperr.go                   # Task 1：错误码与错误结构
│   ├── runner/       runner.go                   # Task 1：Runner 接口、Op 标记、Output、WithRetry
│   │   └── runnertest/fake.go                    # Task 1：按操作名分发的假 Runner
│   ├── shell/        shell.go                    # Task 2：Quote 与参数校验
│   ├── routercmd/    routercmd.go                # Task 2：分段标记、nvram 白名单脚本、路由器时间
│   ├── config/       config.go                   # Task 3：YAML 加载与校验
│   ├── state/        state.go                    # Task 4：state.json 原子读写
│   ├── audit/        audit.go                    # Task 4：JSONL 审计日志
│   ├── sshx/         sshx.go                     # Task 5：SSH 客户端
│   ├── syslog/       parse.go, fetch.go          # Task 6：syslog / dmesg
│   ├── status/       system.go, wan.go, conntrack.go   # Task 7
│   ├── clients/      clients.go                  # Task 8
│   ├── diagnose/     diagnose.go                 # Task 9
│   ├── dnsmasq/      lines.go                    # Task 10：按行增删、diff（纯函数）
│   │                 manager.go                  # Task 11：读、改、备份、清理
│   │                 apply.go                    # Task 12：生效与回滚
│   ├── reboot/       reboot.go                   # Task 13：每日额度与重启
│   └── tools/        tools.go, readonly.go, mutations.go   # Task 14
├── deploy/           merlin-mcp.service, config.example.yaml, install.sh   # Task 16
└── scripts/build.sh                              # Task 16
```

每个包的测试文件与源文件同目录，命名为 `<源文件>_test.go`。

---

### Task 1: 项目骨架、错误码、Runner 接口

**Files:**
- Create: `.gitignore`、`.gitattributes`、`go.mod`
- Create: `internal/apperr/apperr.go`、`internal/apperr/apperr_test.go`
- Create: `internal/runner/runner.go`、`internal/runner/runner_test.go`
- Create: `internal/runner/runnertest/fake.go`

**Interfaces:**
- Consumes: 无
- Produces:
  - `apperr.Code`（string）及常量 `InvalidArgument`、`ConfirmRequired`、`SSHUnreachable`、`SSHAuthFailed`、`HostKeyMismatch`、`Timeout`、`CommandFailed`、`RebootQuotaExceeded`、`AddFileChangedExternally`、`DnsmasqValidationFailed`、`DnsmasqRolledBack`、`DnsmasqRollbackFailed`、`Internal`
  - `apperr.Error{Code Code; Message, Hint string; Details map[string]any}`，`apperr.New(code, message, hint) *Error`，`(*Error).With(key string, value any) *Error`，`apperr.From(err) *Error`，`apperr.CodeOf(err) Code`
  - `runner.Result{Stdout, Stderr []byte; ExitCode int}`，`runner.Runner` 接口 `Run(ctx, cmd string, stdin []byte) (Result, error)`
  - `runner.Op(name, script string) string`，`runner.OpOf(cmd string) string`，`runner.Output(ctx, r, cmd) (string, error)`，`runner.Failed(res Result) *apperr.Error`，`runner.WithRetry(r Runner) Runner`
  - `runnertest.New() *Fake`，`(*Fake).On(op string, h Handler) *Fake`，`(*Fake).Run`，`(*Fake).Calls() []Call`，`(*Fake).CallsFor(op string) []Call`，`runnertest.Stdout(s)`、`runnertest.Exit(code, stdout, stderr)`、`runnertest.Fail(err)`；`Handler func(cmd string, stdin []byte) (runner.Result, error)`；`Call{Cmd string; Stdin []byte}`

- [ ] **Step 1: 向用户确认依赖安装（红线：未经确认不得执行）**

向用户展示以下命令，说明用途，并等待明确同意。同意后**不要现在执行**，它们会在 Task 3、5、14 首次需要时分别执行：

```bash
go get gopkg.in/yaml.v3@v3.0.1                               # Task 3：解析配置文件
go get golang.org/x/crypto@latest                             # Task 5：SSH 客户端与 known_hosts 校验
go get github.com/modelcontextprotocol/go-sdk@v1.8.0          # Task 14：官方 MCP Go SDK
```

用户不同意时停止执行本计划，并报告用户。

- [ ] **Step 2: 创建 `.gitignore`、`.gitattributes`、`go.mod`**

`.gitignore`：

```
dist/
*.test
*.out
```

`.gitattributes`：

```
* text=auto
*.go text eol=lf
*.sh text eol=lf
*.service text eol=lf
*.yaml text eol=lf
*.md text eol=lf
```

`go.mod`：

```
module github.com/rshun/merlin-mcp

go 1.26
```

- [ ] **Step 3: 写 apperr 的失败测试**

`internal/apperr/apperr_test.go`：

```go
package apperr

import (
	"errors"
	"fmt"
	"testing"
)

func TestFromWrapsUnknownErrorAsInternal(t *testing.T) {
	e := From(errors.New("boom"))
	if e.Code != Internal || e.Message != "boom" {
		t.Fatalf("got %+v", e)
	}
}

func TestFromUnwrapsWrappedError(t *testing.T) {
	orig := New(Timeout, "命令执行超时", "")
	wrapped := fmt.Errorf("调用失败: %w", orig)
	if CodeOf(wrapped) != Timeout {
		t.Fatalf("CodeOf = %q", CodeOf(wrapped))
	}
	if From(wrapped) != orig {
		t.Fatal("From 应返回原始 *Error")
	}
}

func TestWithAddsDetails(t *testing.T) {
	e := New(CommandFailed, "失败", "").With("stderr", "bad").With("code", 2)
	if e.Details["stderr"] != "bad" || e.Details["code"] != 2 {
		t.Fatalf("details = %v", e.Details)
	}
}

func TestCodeOfNil(t *testing.T) {
	if CodeOf(nil) != "" {
		t.Fatal("nil 错误应返回空错误码")
	}
}

func TestErrorString(t *testing.T) {
	if got := New(Timeout, "慢", "").Error(); got != "TIMEOUT: 慢" {
		t.Fatalf("Error() = %q", got)
	}
}
```

- [ ] **Step 4: 运行测试，确认失败**

Run: `go test ./internal/apperr/`
Expected: FAIL，编译错误 `undefined: From` 等

- [ ] **Step 5: 实现 apperr**

`internal/apperr/apperr.go`：

```go
// Package apperr 定义工具返回给 AI 的错误码和错误结构。
package apperr

import (
	"errors"
	"fmt"
)

// Code 是返回给 AI 的错误码，取值见 spec 第 8 节。
type Code string

const (
	InvalidArgument          Code = "INVALID_ARGUMENT"
	ConfirmRequired          Code = "CONFIRM_REQUIRED"
	SSHUnreachable           Code = "SSH_UNREACHABLE"
	SSHAuthFailed            Code = "SSH_AUTH_FAILED"
	HostKeyMismatch          Code = "HOSTKEY_MISMATCH"
	Timeout                  Code = "TIMEOUT"
	CommandFailed            Code = "COMMAND_FAILED"
	RebootQuotaExceeded      Code = "REBOOT_QUOTA_EXCEEDED"
	AddFileChangedExternally Code = "ADDFILE_CHANGED_EXTERNALLY"
	DnsmasqValidationFailed  Code = "DNSMASQ_VALIDATION_FAILED"
	DnsmasqRolledBack        Code = "DNSMASQ_ROLLED_BACK"
	DnsmasqRollbackFailed    Code = "DNSMASQ_ROLLBACK_FAILED"
	Internal                 Code = "INTERNAL"
)

// Error 是工具失败时返回的结构，序列化后放进 MCP 结果的文本内容中。
type Error struct {
	Code    Code           `json:"code"`
	Message string         `json:"message"`
	Hint    string         `json:"hint,omitempty"`
	Details map[string]any `json:"details,omitempty"`
}

func (e *Error) Error() string { return fmt.Sprintf("%s: %s", e.Code, e.Message) }

// New 创建一个错误。message 和 hint 使用中文。
func New(code Code, message, hint string) *Error {
	return &Error{Code: code, Message: message, Hint: hint}
}

// With 附加一项细节，返回自身以便链式调用。
func (e *Error) With(key string, value any) *Error {
	if e.Details == nil {
		e.Details = map[string]any{}
	}
	e.Details[key] = value
	return e
}

// From 把任意错误转换成 *Error；不是 *Error 的错误归为 INTERNAL。
func From(err error) *Error {
	var e *Error
	if errors.As(err, &e) {
		return e
	}
	return New(Internal, err.Error(), "")
}

// CodeOf 返回错误码；err 为 nil 时返回空字符串。
func CodeOf(err error) Code {
	if err == nil {
		return ""
	}
	return From(err).Code
}
```

- [ ] **Step 6: 运行测试，确认通过**

Run: `go test ./internal/apperr/`
Expected: PASS

- [ ] **Step 7: 写 runner 的失败测试**

`internal/runner/runner_test.go`：

```go
package runner_test

import (
	"context"
	"testing"

	"github.com/rshun/merlin-mcp/internal/apperr"
	"github.com/rshun/merlin-mcp/internal/runner"
	"github.com/rshun/merlin-mcp/internal/runner/runnertest"
)

func TestOpRoundTrip(t *testing.T) {
	cmd := runner.Op("syslog_fetch", "cat /tmp/syslog.log")
	if cmd != ": mcp-op=syslog_fetch; cat /tmp/syslog.log" {
		t.Fatalf("Op = %q", cmd)
	}
	if got := runner.OpOf(cmd); got != "syslog_fetch" {
		t.Fatalf("OpOf = %q", got)
	}
	if got := runner.OpOf("cat /etc/hosts"); got != "" {
		t.Fatalf("无标记的命令应返回空字符串，得到 %q", got)
	}
}

func TestOutputReturnsStdout(t *testing.T) {
	f := runnertest.New().On("x", runnertest.Stdout("hello\n"))
	out, err := runner.Output(context.Background(), f, runner.Op("x", "echo hello"))
	if err != nil || out != "hello\n" {
		t.Fatalf("out=%q err=%v", out, err)
	}
}

func TestOutputReturnsCommandFailedOnNonZeroExit(t *testing.T) {
	f := runnertest.New().On("x", runnertest.Exit(2, "", "boom"))
	_, err := runner.Output(context.Background(), f, runner.Op("x", "false"))
	if apperr.CodeOf(err) != apperr.CommandFailed {
		t.Fatalf("code = %q", apperr.CodeOf(err))
	}
	if apperr.From(err).Details["stderr"] != "boom" {
		t.Fatalf("details = %v", apperr.From(err).Details)
	}
}

func TestWithRetryRetriesOnceOnUnreachable(t *testing.T) {
	n := 0
	f := runnertest.New().On("x", func(string, []byte) (runner.Result, error) {
		n++
		if n == 1 {
			return runner.Result{}, apperr.New(apperr.SSHUnreachable, "连接断开", "")
		}
		return runner.Result{Stdout: []byte("ok")}, nil
	})
	out, err := runner.Output(context.Background(), runner.WithRetry(f), runner.Op("x", "true"))
	if err != nil || out != "ok" || n != 2 {
		t.Fatalf("out=%q err=%v n=%d", out, err, n)
	}
}

func TestWithRetryDoesNotRetryOtherErrors(t *testing.T) {
	n := 0
	f := runnertest.New().On("x", func(string, []byte) (runner.Result, error) {
		n++
		return runner.Result{}, apperr.New(apperr.Timeout, "超时", "")
	})
	_, err := runner.WithRetry(f).Run(context.Background(), runner.Op("x", "true"), nil)
	if apperr.CodeOf(err) != apperr.Timeout || n != 1 {
		t.Fatalf("err=%v n=%d", err, n)
	}
}

func TestFakeRejectsUnknownOp(t *testing.T) {
	_, err := runnertest.New().Run(context.Background(), "ls", nil)
	if err == nil {
		t.Fatal("未注册的操作应返回错误")
	}
}

func TestFakeRecordsCallsAndStdin(t *testing.T) {
	f := runnertest.New().On("w", runnertest.Stdout(""))
	_, _ = f.Run(context.Background(), runner.Op("w", "cat > /tmp/x"), []byte("data"))
	calls := f.CallsFor("w")
	if len(calls) != 1 || string(calls[0].Stdin) != "data" {
		t.Fatalf("calls = %+v", calls)
	}
}
```

- [ ] **Step 8: 运行测试，确认失败**

Run: `go test ./internal/runner/...`
Expected: FAIL，编译错误（`runner`、`runnertest` 包不存在）

- [ ] **Step 9: 实现 runner**

`internal/runner/runner.go`：

```go
// Package runner 定义在路由器上执行命令的抽象。
package runner

import (
	"context"
	"fmt"
	"strings"

	"github.com/rshun/merlin-mcp/internal/apperr"
)

// Result 是一次远程命令的执行结果。
type Result struct {
	Stdout   []byte
	Stderr   []byte
	ExitCode int
}

// Runner 在路由器上执行一条 shell 命令。stdin 为 nil 时不提供输入。
type Runner interface {
	Run(ctx context.Context, cmd string, stdin []byte) (Result, error)
}

const opPrefix = ": mcp-op="

// Op 给命令加上操作名标记。`:` 是 shell 的空操作，不影响执行；
// 测试用的假 Runner 按操作名分发调用。
func Op(name, script string) string { return opPrefix + name + "; " + script }

// OpOf 取出命令的操作名，没有标记时返回空字符串。
func OpOf(cmd string) string {
	if !strings.HasPrefix(cmd, opPrefix) {
		return ""
	}
	rest := cmd[len(opPrefix):]
	if i := strings.IndexByte(rest, ';'); i >= 0 {
		return rest[:i]
	}
	return rest
}

// Output 执行命令并返回 stdout；退出码非 0 时返回 COMMAND_FAILED。
func Output(ctx context.Context, r Runner, cmd string) (string, error) {
	res, err := r.Run(ctx, cmd, nil)
	if err != nil {
		return "", err
	}
	if res.ExitCode != 0 {
		return "", Failed(res)
	}
	return string(res.Stdout), nil
}

// Failed 把非 0 退出码转换成 COMMAND_FAILED 错误，附带 stderr 末尾 2KB。
func Failed(res Result) *apperr.Error {
	stderr := string(res.Stderr)
	if len(stderr) > 2048 {
		stderr = stderr[len(stderr)-2048:]
	}
	return apperr.New(apperr.CommandFailed,
		fmt.Sprintf("路由器命令执行失败，退出码 %d", res.ExitCode),
		"查看 details.stderr 了解原因").
		With("stderr", strings.ToValidUTF8(strings.TrimSpace(stderr), ""))
}

// WithRetry 返回在 SSH_UNREACHABLE 时自动重试一次的 Runner。
// 只能给只读操作使用：修改类操作重试可能导致重复执行。
func WithRetry(r Runner) Runner { return retryRunner{inner: r} }

type retryRunner struct{ inner Runner }

func (x retryRunner) Run(ctx context.Context, cmd string, stdin []byte) (Result, error) {
	res, err := x.inner.Run(ctx, cmd, stdin)
	if apperr.CodeOf(err) == apperr.SSHUnreachable && ctx.Err() == nil {
		return x.inner.Run(ctx, cmd, stdin)
	}
	return res, err
}
```

`internal/runner/runnertest/fake.go`：

```go
// Package runnertest 提供按操作名分发的假 Runner，供各模块测试使用。
package runnertest

import (
	"context"
	"fmt"
	"sync"

	"github.com/rshun/merlin-mcp/internal/runner"
)

// Call 记录一次调用。
type Call struct {
	Cmd   string
	Stdin []byte
}

// Handler 处理一次调用。
type Handler func(cmd string, stdin []byte) (runner.Result, error)

// Fake 按 runner.OpOf(cmd) 把调用分发给注册的 Handler；未注册的操作返回错误。
type Fake struct {
	mu       sync.Mutex
	calls    []Call
	handlers map[string]Handler
}

// New 创建一个没有任何处理函数的 Fake。
func New() *Fake { return &Fake{handlers: map[string]Handler{}} }

// On 为操作名注册处理函数，返回自身以便链式调用。
func (f *Fake) On(op string, h Handler) *Fake {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.handlers[op] = h
	return f
}

// Run 实现 runner.Runner。
func (f *Fake) Run(_ context.Context, cmd string, stdin []byte) (runner.Result, error) {
	f.mu.Lock()
	f.calls = append(f.calls, Call{Cmd: cmd, Stdin: append([]byte(nil), stdin...)})
	h := f.handlers[runner.OpOf(cmd)]
	f.mu.Unlock()
	if h == nil {
		return runner.Result{}, fmt.Errorf("runnertest: 未注册的操作 %q，命令: %s", runner.OpOf(cmd), cmd)
	}
	return h(cmd, stdin)
}

// Calls 返回所有调用记录的副本。
func (f *Fake) Calls() []Call {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Call(nil), f.calls...)
}

// CallsFor 返回指定操作名的调用记录。
func (f *Fake) CallsFor(op string) []Call {
	var out []Call
	for _, c := range f.Calls() {
		if runner.OpOf(c.Cmd) == op {
			out = append(out, c)
		}
	}
	return out
}

// Stdout 返回一个输出 s、退出码为 0 的处理函数。
func Stdout(s string) Handler { return Exit(0, s, "") }

// Exit 返回一个固定输出和退出码的处理函数。
func Exit(code int, stdout, stderr string) Handler {
	return func(string, []byte) (runner.Result, error) {
		return runner.Result{Stdout: []byte(stdout), Stderr: []byte(stderr), ExitCode: code}, nil
	}
}

// Fail 返回一个总是返回 err 的处理函数（模拟连接错误等）。
func Fail(err error) Handler {
	return func(string, []byte) (runner.Result, error) { return runner.Result{}, err }
}
```

- [ ] **Step 10: 运行全部测试和检查**

Run: `go test ./... && go vet ./... && gofmt -l .`
Expected: 测试 PASS；`go vet` 无输出；`gofmt -l .` 无输出

- [ ] **Step 11: 提交**

按"提交步骤模板"执行，`<files>` 为 `.gitignore .gitattributes go.mod internal/apperr internal/runner`，提交说明：`feat: add project skeleton, error codes and runner interface`

---

### Task 2: shell 转义与参数校验、routercmd 公共工具

**Files:**
- Create: `internal/shell/shell.go`、`internal/shell/shell_test.go`
- Create: `internal/routercmd/routercmd.go`、`internal/routercmd/routercmd_test.go`

**Interfaces:**
- Consumes: 无
- Produces:
  - `shell.Quote(s string) string`
  - `shell.ValidDomain(s) bool`、`shell.ValidIP(s) bool`、`shell.ValidHost(s) bool`、`shell.ValidProcessName(s) bool`、`shell.ValidIfName(s) bool`
  - `routercmd.Marker(name string) string`（返回 `echo '@@MERLINMCP:<name>@@'`）
  - `routercmd.Sections(out string) map[string]string`
  - `routercmd.NvramScript(keys ...string) (string, error)`、`routercmd.MustNvramScript(keys ...string) string`
  - `routercmd.ParseKV(s string) map[string]string`
  - `routercmd.DateCmd`（常量 `date '+%s %z'`）、`routercmd.ParseDate(s string) (time.Time, error)`

- [ ] **Step 1: 写 shell 的失败测试**

`internal/shell/shell_test.go`：

```go
package shell

import (
	"os/exec"
	"strings"
	"testing"
)

func TestQuote(t *testing.T) {
	cases := map[string]string{
		"abc":  "'abc'",
		"":     "''",
		"it's": `'it'\''s'`,
	}
	for in, want := range cases {
		if got := Quote(in); got != want {
			t.Errorf("Quote(%q) = %q, want %q", in, got, want)
		}
	}
}

// 通过真实的 sh 验证转义后的字符串能原样还原，命令通过 stdin 传入，避免 Windows 命令行转义干扰。
func TestQuoteRoundTripThroughSh(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("没有 sh，跳过")
	}
	inputs := []string{"plain", "it's", "$(echo pwned)", "`id`", "a;b|c&d", `back\slash`, "多字节 中文", "'''", "a\tb"}
	for _, s := range inputs {
		cmd := exec.Command(sh)
		cmd.Stdin = strings.NewReader("printf %s " + Quote(s))
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("sh 执行失败 %q: %v", s, err)
		}
		if string(out) != s {
			t.Errorf("Quote(%q) 经 sh 还原为 %q", s, out)
		}
	}
}

func TestValidHost(t *testing.T) {
	good := []string{"example.com", "router.asus.com", "localhost", "a-b.example.", "192.0.2.1", "2001:db8::1"}
	bad := []string{"", "-oProxyCommand=x", "a b", "$(id)", "a;b", "exa_mple.com", "a..b", "-a.com", "fe80::1%eth0", strings.Repeat("a", 64) + ".com"}
	for _, s := range good {
		if !ValidHost(s) {
			t.Errorf("ValidHost(%q) 应为 true", s)
		}
	}
	for _, s := range bad {
		if ValidHost(s) {
			t.Errorf("ValidHost(%q) 应为 false", s)
		}
	}
}

func TestValidIP(t *testing.T) {
	if !ValidIP("127.0.0.1") || !ValidIP("::1") || ValidIP("example.com") || ValidIP("1.2.3") {
		t.Fatal("ValidIP 结果不正确")
	}
}

func TestValidProcessName(t *testing.T) {
	for _, s := range []string{"dnsmasq", "dnsmasq-dhcp", "rc_service", "kernel"} {
		if !ValidProcessName(s) {
			t.Errorf("%q 应合法", s)
		}
	}
	for _, s := range []string{"", "a b", "x;y", "$(id)", strings.Repeat("a", 65)} {
		if ValidProcessName(s) {
			t.Errorf("%q 应非法", s)
		}
	}
}

func TestValidIfName(t *testing.T) {
	for _, s := range []string{"eth6", "wl0.1", "br0", "eth7"} {
		if !ValidIfName(s) {
			t.Errorf("%q 应合法", s)
		}
	}
	for _, s := range []string{"", "-i", "eth 0", "eth0;reboot", "abcdefghijklmnop"} {
		if ValidIfName(s) {
			t.Errorf("%q 应非法", s)
		}
	}
}
```

- [ ] **Step 2: 运行测试，确认失败**

Run: `go test ./internal/shell/`
Expected: FAIL，编译错误 `undefined: Quote`

- [ ] **Step 3: 实现 shell**

`internal/shell/shell.go`：

```go
// Package shell 提供拼接远程命令时使用的转义和参数校验。
// 规则：用户传入的值必须先通过这里的校验，再经 Quote 转义后才能拼进命令。
package shell

import (
	"net/netip"
	"regexp"
	"strings"
)

// Quote 把 s 转成 POSIX shell 单引号字符串，内部的 ' 替换为 '\''。
func Quote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

var (
	labelRe   = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?$`)
	processRe = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,64}$`)
	ifnameRe  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,14}$`)
)

// ValidDomain 判断 s 是否为合法域名（RFC 1123，最长 253 字符，允许末尾的点）。
func ValidDomain(s string) bool {
	s = strings.TrimSuffix(s, ".")
	if s == "" || len(s) > 253 {
		return false
	}
	for _, label := range strings.Split(s, ".") {
		if !labelRe.MatchString(label) {
			return false
		}
	}
	return true
}

// ValidIP 判断 s 是否为不带 zone 的 IPv4 或 IPv6 地址。
func ValidIP(s string) bool {
	a, err := netip.ParseAddr(s)
	return err == nil && a.Zone() == ""
}

// ValidHost 判断 s 是否为合法域名或 IP。
func ValidHost(s string) bool { return ValidIP(s) || ValidDomain(s) }

// ValidProcessName 判断 s 是否为合法的进程名（用于 syslog 过滤）。
func ValidProcessName(s string) bool { return processRe.MatchString(s) }

// ValidIfName 判断 s 是否为合法的网络接口名（Linux 接口名最长 15 字符）。
func ValidIfName(s string) bool { return ifnameRe.MatchString(s) }
```

- [ ] **Step 4: 运行测试，确认通过**

Run: `go test ./internal/shell/`
Expected: PASS（Windows 上 Git Bash 提供 `sh`，`TestQuoteRoundTripThroughSh` 会实际执行）

- [ ] **Step 5: 写 routercmd 的失败测试**

`internal/routercmd/routercmd_test.go`：

```go
package routercmd

import (
	"strings"
	"testing"
)

func TestMarker(t *testing.T) {
	if got := Marker("date"); got != "echo '@@MERLINMCP:date@@'" {
		t.Fatalf("Marker = %q", got)
	}
}

func TestSections(t *testing.T) {
	out := "噪音\n@@MERLINMCP:a@@\nline1\nline2\n@@MERLINMCP:empty@@\n@@MERLINMCP:b@@\nlast-no-newline"
	sec := Sections(out)
	if sec["a"] != "line1\nline2\n" {
		t.Errorf("a = %q", sec["a"])
	}
	if v, ok := sec["empty"]; !ok || v != "" {
		t.Errorf("empty = %q, ok=%v", v, ok)
	}
	if sec["b"] != "last-no-newline" {
		t.Errorf("b = %q", sec["b"])
	}
	if _, ok := sec[""]; ok {
		t.Error("标记之前的内容不应成为一个分段")
	}
}

func TestSectionsHandlesCRLF(t *testing.T) {
	sec := Sections("@@MERLINMCP:a@@\r\nx\r\n")
	if sec["a"] != "x\r\n" {
		t.Fatalf("a = %q", sec["a"])
	}
}

func TestNvramScript(t *testing.T) {
	s, err := NvramScript("productid", "buildno")
	if err != nil {
		t.Fatal(err)
	}
	want := `printf '%s=%s\n' productid "$(nvram get productid)"; printf '%s=%s\n' buildno "$(nvram get buildno)"`
	if s != want {
		t.Fatalf("got  %s\nwant %s", s, want)
	}
}

func TestNvramScriptRejectsSensitiveOrInvalidKeys(t *testing.T) {
	for _, k := range []string{"wan0_pppoe_passwd", "http_passwd", "wl0_wpa_psk", "vpn_secret", "wl0_key1", "Upper", "a;b", ""} {
		if _, err := NvramScript(k); err == nil {
			t.Errorf("键 %q 应被拒绝", k)
		}
	}
}

func TestParseKV(t *testing.T) {
	kv := ParseKV("productid=RT-AX86U\r\ndhcp_staticlist=<AA:BB:CC:00:00:01>192.0.2.10>>\nempty=\nnoequals\n")
	if kv["productid"] != "RT-AX86U" || kv["dhcp_staticlist"] != "<AA:BB:CC:00:00:01>192.0.2.10>>" {
		t.Fatalf("kv = %v", kv)
	}
	if v, ok := kv["empty"]; !ok || v != "" {
		t.Fatalf("empty = %q ok=%v", v, ok)
	}
	if _, ok := kv["noequals"]; ok {
		t.Fatal("没有等号的行应忽略")
	}
}

func TestParseDate(t *testing.T) {
	got, err := ParseDate("1791000000 +0800\n")
	if err != nil {
		t.Fatal(err)
	}
	if got.Unix() != 1791000000 {
		t.Fatalf("unix = %d", got.Unix())
	}
	if _, off := got.Zone(); off != 8*3600 {
		t.Fatalf("offset = %d", off)
	}
	got, err = ParseDate("0 -0330")
	if err != nil {
		t.Fatal(err)
	}
	if _, off := got.Zone(); off != -(3*3600 + 30*60) {
		t.Fatalf("offset = %d", off)
	}
	for _, bad := range []string{"", "abc +0800", "1 0800", "1 +08", strings.Repeat("9", 30) + " +0000"} {
		if _, err := ParseDate(bad); err == nil {
			t.Errorf("ParseDate(%q) 应失败", bad)
		}
	}
}
```

- [ ] **Step 6: 运行测试，确认失败**

Run: `go test ./internal/routercmd/`
Expected: FAIL，编译错误

- [ ] **Step 7: 实现 routercmd**

`internal/routercmd/routercmd.go`：

```go
// Package routercmd 提供组装路由器命令和解析其输出的公共工具。
package routercmd

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const markerPrefix = "@@MERLINMCP:"

// Marker 返回输出一个分段标记的命令。多条命令的输出用标记分隔，一次 SSH 调用即可全部取回。
func Marker(name string) string { return "echo '" + markerPrefix + name + "@@'" }

// Sections 按 Marker 输出的标记把 out 切分成各段；第一个标记之前的内容被忽略。
func Sections(out string) map[string]string {
	res := map[string]string{}
	var cur string
	var b strings.Builder
	have := false
	for _, line := range strings.SplitAfter(out, "\n") {
		t := strings.TrimRight(line, "\r\n")
		if strings.HasPrefix(t, markerPrefix) && strings.HasSuffix(t, "@@") && len(t) > len(markerPrefix)+2 {
			if have {
				res[cur] = b.String()
			}
			cur = strings.TrimSuffix(strings.TrimPrefix(t, markerPrefix), "@@")
			b.Reset()
			have = true
			continue
		}
		if have {
			b.WriteString(line)
		}
	}
	if have {
		res[cur] = b.String()
	}
	return res
}

var (
	nvramKeyRe  = regexp.MustCompile(`^[a-z0-9_]{1,64}$`)
	nvramDenyRe = regexp.MustCompile(`(?i)(passwd|password|psk|secret|key)`)
)

// NvramScript 生成读取一组 nvram 键的脚本，输出为 key=value 行。
// 名称含 passwd/password/psk/secret/key 的键一律拒绝，防止把密码类字段交给 AI。
func NvramScript(keys ...string) (string, error) {
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		if !nvramKeyRe.MatchString(k) || nvramDenyRe.MatchString(k) {
			return "", fmt.Errorf("不允许读取的 nvram 键: %q", k)
		}
		parts = append(parts, fmt.Sprintf(`printf '%%s=%%s\n' %s "$(nvram get %s)"`, k, k))
	}
	return strings.Join(parts, "; "), nil
}

// MustNvramScript 与 NvramScript 相同，键非法时 panic。只用于代码中写死的键列表。
func MustNvramScript(keys ...string) string {
	s, err := NvramScript(keys...)
	if err != nil {
		panic(err)
	}
	return s
}

// ParseKV 解析 key=value 行，忽略没有等号的行。
func ParseKV(s string) map[string]string {
	m := map[string]string{}
	for _, line := range strings.Split(s, "\n") {
		k, v, ok := strings.Cut(strings.TrimRight(line, "\r"), "=")
		if ok && k != "" {
			m[k] = v
		}
	}
	return m
}

// DateCmd 输出路由器当前的 Unix 时间和时区偏移，例如 "1791000000 +0800"。
const DateCmd = "date '+%s %z'"

// ParseDate 解析 DateCmd 的输出，返回带路由器时区偏移的时间。
func ParseDate(s string) (time.Time, error) {
	f := strings.Fields(s)
	if len(f) != 2 {
		return time.Time{}, fmt.Errorf("无法解析路由器时间 %q", s)
	}
	sec, err := strconv.ParseInt(f[0], 10, 64)
	if err != nil {
		return time.Time{}, fmt.Errorf("无法解析路由器时间 %q: %w", s, err)
	}
	z := f[1]
	if len(z) != 5 || (z[0] != '+' && z[0] != '-') {
		return time.Time{}, fmt.Errorf("无法解析路由器时区 %q", z)
	}
	hh, err1 := strconv.Atoi(z[1:3])
	mm, err2 := strconv.Atoi(z[3:5])
	if err1 != nil || err2 != nil {
		return time.Time{}, fmt.Errorf("无法解析路由器时区 %q", z)
	}
	off := hh*3600 + mm*60
	if z[0] == '-' {
		off = -off
	}
	return time.Unix(sec, 0).In(time.FixedZone("router", off)), nil
}
```

- [ ] **Step 8: 运行全部测试和检查**

Run: `go test ./... && go vet ./... && gofmt -l .`
Expected: PASS，`gofmt -l .` 无输出

- [ ] **Step 9: 提交**

按"提交步骤模板"执行，`<files>` 为 `internal/shell internal/routercmd`，提交说明：`feat: add shell quoting, input validation and router command helpers`

---

### Task 3: 配置加载与校验

**Files:**
- Create: `internal/config/config.go`、`internal/config/config_test.go`
- Modify: `go.mod`、`go.sum`（由 `go get` 修改）

**Interfaces:**
- Consumes: `shell.ValidHost`、`shell.ValidDomain`（Task 2）
- Produces:
  - `config.Config{Listen string; Router Router; AllowMutations bool; Paths Paths; Dnsmasq Dnsmasq; Reboot Reboot; StateDir, AuditLog string; CommandTimeout time.Duration; Location *time.Location}`
  - `config.Router{Host string; Port int; User, KeyFile, KnownHosts, CommandTimeout string}`
  - `config.Paths{DnsmasqAdd, BackupDir, Syslog string}`
  - `config.Dnsmasq{HealthCheckDomain string; BackupKeep int}`
  - `config.Reboot{Timezone string; MaxPerDay int}`
  - `config.Load(path string) (*Config, error)`：校验通过后 `CommandTimeout` 和 `Location` 已填好

- [ ] **Step 1: 安装 yaml 依赖（Task 1 已获用户确认）**

Run: `go get gopkg.in/yaml.v3@v3.0.1`
Expected: `go.mod` 中出现 `gopkg.in/yaml.v3 v3.0.1`

- [ ] **Step 2: 写失败测试**

`internal/config/config_test.go`：

```go
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

type fixture struct{ dir, key, kh string }

func newFixture(t *testing.T) fixture {
	t.Helper()
	dir := t.TempDir()
	f := fixture{dir: dir, key: filepath.Join(dir, "id_ed25519"), kh: filepath.Join(dir, "known_hosts")}
	if err := os.WriteFile(f.key, []byte("dummy"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.kh, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	return f
}

// yaml 返回只包含必填项的配置，extra 追加在末尾（只能追加顶层键）。
func (f fixture) yaml(extra string) string {
	return fmt.Sprintf(`router:
  host: 192.0.2.1
  user: admin
  key_file: %s
  known_hosts: %s
reboot:
  timezone: Asia/Shanghai
state_dir: %s
audit_log: %s
%s`, filepath.ToSlash(f.key), filepath.ToSlash(f.kh), filepath.ToSlash(f.dir),
		filepath.ToSlash(filepath.Join(f.dir, "audit.jsonl")), extra)
}

func load(t *testing.T, content string) (*Config, error) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return Load(p)
}

func TestLoadAppliesDefaults(t *testing.T) {
	f := newFixture(t)
	c, err := load(t, f.yaml(""))
	if err != nil {
		t.Fatal(err)
	}
	if c.Listen != "127.0.0.1:8765" || c.Router.Port != 22 || c.CommandTimeout != 15*time.Second {
		t.Errorf("默认值不正确: %+v", c)
	}
	if c.Paths.DnsmasqAdd != "/jffs/configs/dnsmasq.conf.add" || c.Paths.BackupDir != "/jffs/merlin-mcp/backups" || c.Paths.Syslog != "auto" {
		t.Errorf("paths 默认值不正确: %+v", c.Paths)
	}
	if c.Dnsmasq.HealthCheckDomain != "router.asus.com" || c.Dnsmasq.BackupKeep != 20 || c.Reboot.MaxPerDay != 1 {
		t.Errorf("默认值不正确: %+v %+v", c.Dnsmasq, c.Reboot)
	}
	if c.AllowMutations {
		t.Error("allow_mutations 默认应为 false")
	}
	if c.Location == nil || c.Location.String() != "Asia/Shanghai" {
		t.Errorf("Location = %v", c.Location)
	}
}

func TestLoadRejectsNonLoopbackListen(t *testing.T) {
	f := newFixture(t)
	for _, l := range []string{"0.0.0.0:8765", "192.0.2.5:8765", "localhost:8765"} {
		_, err := load(t, f.yaml("listen: \""+l+"\"\n"))
		if err == nil || !strings.Contains(err.Error(), "回环地址") {
			t.Errorf("listen=%s 应被拒绝，err=%v", l, err)
		}
	}
	if _, err := load(t, f.yaml("listen: \"[::1]:8765\"\n")); err != nil {
		t.Errorf("::1 应被接受: %v", err)
	}
}

func TestLoadRejectsUnknownField(t *testing.T) {
	f := newFixture(t)
	if _, err := load(t, f.yaml("allow_mutation: true\n")); err == nil {
		t.Fatal("拼错的字段应被拒绝")
	}
}

func TestLoadRequiresTimezone(t *testing.T) {
	f := newFixture(t)
	content := strings.Replace(f.yaml(""), "  timezone: Asia/Shanghai\n", "  max_per_day: 1\n", 1)
	_, err := load(t, content)
	if err == nil || !strings.Contains(err.Error(), "reboot.timezone") {
		t.Fatalf("缺少时区应报错，err=%v", err)
	}
}

func TestLoadRejectsInvalidTimezone(t *testing.T) {
	f := newFixture(t)
	content := strings.Replace(f.yaml(""), "Asia/Shanghai", "Mars/Olympus", 1)
	if _, err := load(t, content); err == nil {
		t.Fatal("无效时区应报错")
	}
}

func TestLoadRejectsUnsafeRouterPaths(t *testing.T) {
	f := newFixture(t)
	for _, p := range []string{"/jffs/configs/x;reboot", "relative/path", "/jffs/../etc/passwd", "/jffs/a b"} {
		if _, err := load(t, f.yaml("paths:\n  dnsmasq_add: \""+p+"\"\n")); err == nil {
			t.Errorf("路径 %q 应被拒绝", p)
		}
	}
}

func TestLoadRejectsMissingKeyFile(t *testing.T) {
	f := newFixture(t)
	content := strings.Replace(f.yaml(""), filepath.ToSlash(f.key), filepath.ToSlash(f.key)+".missing", 1)
	_, err := load(t, content)
	if err == nil || !strings.Contains(err.Error(), "router.key_file") {
		t.Fatalf("err = %v", err)
	}
}

func TestLoadRejectsKeyFileTooOpen(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows 不支持 Unix 权限位")
	}
	f := newFixture(t)
	if err := os.Chmod(f.key, 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := load(t, f.yaml(""))
	if err == nil || !strings.Contains(err.Error(), "权限过宽") {
		t.Fatalf("err = %v", err)
	}
}

func TestLoadRejectsBadHealthDomainAndKeep(t *testing.T) {
	f := newFixture(t)
	_, err := load(t, f.yaml("dnsmasq:\n  health_check_domain: \"a b\"\n  backup_keep: 0\n"))
	if err == nil || !strings.Contains(err.Error(), "health_check_domain") || !strings.Contains(err.Error(), "backup_keep") {
		t.Fatalf("err = %v", err)
	}
}
```

- [ ] **Step 3: 运行测试，确认失败**

Run: `go test ./internal/config/`
Expected: FAIL，编译错误 `undefined: Load`

- [ ] **Step 4: 实现 config**

`internal/config/config.go`：

```go
// Package config 加载并校验 merlin-mcp 的 YAML 配置文件。
package config

import (
	"bytes"
	"fmt"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
	_ "time/tzdata" // 内置时区数据，不依赖系统是否安装 tzdata

	"gopkg.in/yaml.v3"

	"github.com/rshun/merlin-mcp/internal/shell"
)

// Config 是完整的配置。CommandTimeout 和 Location 由校验步骤根据原始字段计算得出。
type Config struct {
	Listen         string  `yaml:"listen"`
	Router         Router  `yaml:"router"`
	AllowMutations bool    `yaml:"allow_mutations"`
	Paths          Paths   `yaml:"paths"`
	Dnsmasq        Dnsmasq `yaml:"dnsmasq"`
	Reboot         Reboot  `yaml:"reboot"`
	StateDir       string  `yaml:"state_dir"`
	AuditLog       string  `yaml:"audit_log"`

	CommandTimeout time.Duration  `yaml:"-"`
	Location       *time.Location `yaml:"-"`
}

type Router struct {
	Host           string `yaml:"host"`
	Port           int    `yaml:"port"`
	User           string `yaml:"user"`
	KeyFile        string `yaml:"key_file"`
	KnownHosts     string `yaml:"known_hosts"`
	CommandTimeout string `yaml:"command_timeout"`
}

type Paths struct {
	DnsmasqAdd string `yaml:"dnsmasq_add"`
	BackupDir  string `yaml:"backup_dir"`
	Syslog     string `yaml:"syslog"`
}

type Dnsmasq struct {
	HealthCheckDomain string `yaml:"health_check_domain"`
	BackupKeep        int    `yaml:"backup_keep"`
}

type Reboot struct {
	Timezone  string `yaml:"timezone"`
	MaxPerDay int    `yaml:"max_per_day"`
}

func defaults() Config {
	return Config{
		Listen:  "127.0.0.1:8765",
		Router:  Router{Port: 22, CommandTimeout: "15s"},
		Paths:   Paths{DnsmasqAdd: "/jffs/configs/dnsmasq.conf.add", BackupDir: "/jffs/merlin-mcp/backups", Syslog: "auto"},
		Dnsmasq: Dnsmasq{HealthCheckDomain: "router.asus.com", BackupKeep: 20},
		Reboot:  Reboot{MaxPerDay: 1},
	}
}

// Load 读取并校验配置文件。未知字段视为错误，避免拼写错误被静默忽略。
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读取配置文件失败: %w", err)
	}
	cfg := defaults()
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("解析配置文件失败: %w", err)
	}
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

var (
	routerPathRe = regexp.MustCompile(`^/[A-Za-z0-9._/-]+$`)
	userRe       = regexp.MustCompile(`^[A-Za-z0-9._-]{1,32}$`)
)

func isAbs(p string) bool { return p != "" && (strings.HasPrefix(p, "/") || filepath.IsAbs(p)) }

func validRouterPath(p string) bool { return routerPathRe.MatchString(p) && !strings.Contains(p, "..") }

func (c *Config) validate() error {
	var errs []string
	add := func(format string, a ...any) { errs = append(errs, fmt.Sprintf(format, a...)) }

	if host, _, err := net.SplitHostPort(c.Listen); err != nil {
		add("listen 格式错误: %v", err)
	} else if ip, err := netip.ParseAddr(host); err != nil || !ip.IsLoopback() {
		add("listen 必须是回环地址（127.0.0.0/8 或 ::1），当前为 %q", c.Listen)
	}

	if !shell.ValidHost(c.Router.Host) {
		add("router.host 无效: %q", c.Router.Host)
	}
	if c.Router.Port < 1 || c.Router.Port > 65535 {
		add("router.port 无效: %d", c.Router.Port)
	}
	if !userRe.MatchString(c.Router.User) {
		add("router.user 无效: %q", c.Router.User)
	}
	checkFile := func(name, p string, private bool) {
		if !isAbs(p) {
			add("%s 必须是绝对路径，当前为 %q", name, p)
			return
		}
		info, err := os.Stat(p)
		if err != nil {
			add("%s 无法访问: %v", name, err)
			return
		}
		if private && runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
			add("%s 权限过宽（%o），请执行 chmod 600 %s", name, info.Mode().Perm(), p)
		}
	}
	checkFile("router.key_file", c.Router.KeyFile, true)
	checkFile("router.known_hosts", c.Router.KnownHosts, false)
	if d, err := time.ParseDuration(c.Router.CommandTimeout); err != nil || d <= 0 {
		add("router.command_timeout 无效: %q", c.Router.CommandTimeout)
	} else {
		c.CommandTimeout = d
	}

	if !validRouterPath(c.Paths.DnsmasqAdd) {
		add("paths.dnsmasq_add 必须是只含字母、数字和 ._/- 的绝对路径: %q", c.Paths.DnsmasqAdd)
	}
	if !validRouterPath(c.Paths.BackupDir) {
		add("paths.backup_dir 必须是只含字母、数字和 ._/- 的绝对路径: %q", c.Paths.BackupDir)
	}
	if c.Paths.Syslog != "auto" && !validRouterPath(c.Paths.Syslog) {
		add("paths.syslog 必须是 auto 或绝对路径: %q", c.Paths.Syslog)
	}

	if !shell.ValidDomain(c.Dnsmasq.HealthCheckDomain) {
		add("dnsmasq.health_check_domain 不是合法域名: %q", c.Dnsmasq.HealthCheckDomain)
	}
	if c.Dnsmasq.BackupKeep < 1 || c.Dnsmasq.BackupKeep > 1000 {
		add("dnsmasq.backup_keep 必须在 1-1000 之间: %d", c.Dnsmasq.BackupKeep)
	}

	if c.Reboot.MaxPerDay < 1 {
		add("reboot.max_per_day 必须 >= 1: %d", c.Reboot.MaxPerDay)
	}
	if c.Reboot.Timezone == "" {
		add("reboot.timezone 必填，例如 Asia/Shanghai")
	} else if loc, err := time.LoadLocation(c.Reboot.Timezone); err != nil {
		add("reboot.timezone 无法加载 %q: %v", c.Reboot.Timezone, err)
	} else {
		c.Location = loc
	}

	if !isAbs(c.StateDir) {
		add("state_dir 必须是绝对路径: %q", c.StateDir)
	}
	if !isAbs(c.AuditLog) {
		add("audit_log 必须是绝对路径: %q", c.AuditLog)
	}

	if len(errs) > 0 {
		return fmt.Errorf("配置校验失败:\n  - %s", strings.Join(errs, "\n  - "))
	}
	return nil
}
```

- [ ] **Step 5: 运行测试，确认通过**

Run: `go test ./internal/config/`
Expected: PASS（Windows 上 `TestLoadRejectsKeyFileTooOpen` 显示 SKIP）

- [ ] **Step 6: 全量检查**

Run: `go test ./... && go vet ./... && gofmt -l .`
Expected: PASS，`gofmt -l .` 无输出

- [ ] **Step 7: 提交**

按"提交步骤模板"执行，`<files>` 为 `go.mod go.sum internal/config`，提交说明：`feat: add config loading and validation`

---

### Task 4: 状态文件与审计日志

**Files:**
- Create: `internal/state/state.go`、`internal/state/state_test.go`
- Create: `internal/audit/audit.go`、`internal/audit/audit_test.go`

**Interfaces:**
- Consumes: 无
- Produces:
  - `state.State{Reboot RebootState; Dnsmasq DnsmasqState}`、`state.RebootState{History []time.Time}`、`state.DnsmasqState{KnownGoodBackup, LastMCPSHA256 string}`
  - `state.Open(dir string) (*Store, error)`、`(*Store).Get() State`（返回深拷贝）、`(*Store).Update(fn func(*State) error) error`（`fn` 返回错误时原样返回，不落盘）
  - `audit.Entry{TS time.Time; Tool string; Args map[string]any; Outcome, ErrorCode string; DurationMS int64; Summary string}`
  - `audit.New(path string) *Logger`、`(*Logger).Write(e Entry) error`、`audit.Redact(args map[string]any) map[string]any`

- [ ] **Step 1: 写 state 的失败测试**

`internal/state/state_test.go`：

```go
package state

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestOpenEmptyDirGivesZeroState(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "sub"))
	if err != nil {
		t.Fatal(err)
	}
	st := s.Get()
	if len(st.Reboot.History) != 0 || st.Dnsmasq.KnownGoodBackup != "" {
		t.Fatalf("state = %+v", st)
	}
}

func TestUpdatePersistsAcrossReopen(t *testing.T) {
	dir := t.TempDir()
	s, _ := Open(dir)
	at := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	err := s.Update(func(st *State) error {
		st.Reboot.History = append(st.Reboot.History, at)
		st.Dnsmasq.KnownGoodBackup = "dnsmasq.conf.add.20261003-120000"
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	s2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	st := s2.Get()
	if len(st.Reboot.History) != 1 || !st.Reboot.History[0].Equal(at) || st.Dnsmasq.KnownGoodBackup != "dnsmasq.conf.add.20261003-120000" {
		t.Fatalf("reopened state = %+v", st)
	}
}

func TestUpdateErrorKeepsState(t *testing.T) {
	dir := t.TempDir()
	s, _ := Open(dir)
	boom := errors.New("boom")
	err := s.Update(func(st *State) error {
		st.Dnsmasq.LastMCPSHA256 = "changed"
		return boom
	})
	if err != boom {
		t.Fatalf("err = %v", err)
	}
	if s.Get().Dnsmasq.LastMCPSHA256 != "" {
		t.Fatal("fn 返回错误时内存状态不应改变")
	}
	if _, err := os.Stat(filepath.Join(dir, "state.json")); !os.IsNotExist(err) {
		t.Fatal("fn 返回错误时不应写文件")
	}
}

func TestOpenCorruptFileFails(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "state.json"), []byte("{broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Open(dir)
	if err == nil || !strings.Contains(err.Error(), "已损坏") {
		t.Fatalf("err = %v", err)
	}
}

func TestGetReturnsDeepCopy(t *testing.T) {
	s, _ := Open(t.TempDir())
	_ = s.Update(func(st *State) error {
		st.Reboot.History = []time.Time{time.Unix(1, 0)}
		return nil
	})
	got := s.Get()
	got.Reboot.History[0] = time.Unix(2, 0)
	if s.Get().Reboot.History[0].Unix() != 1 {
		t.Fatal("修改 Get 的返回值不应影响内部状态")
	}
}

func TestStateFilePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows 不支持 Unix 权限位")
	}
	dir := t.TempDir()
	s, _ := Open(dir)
	_ = s.Update(func(*State) error { return nil })
	info, err := os.Stat(filepath.Join(dir, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("perm = %o", info.Mode().Perm())
	}
}
```

- [ ] **Step 2: 运行测试，确认失败**

Run: `go test ./internal/state/`
Expected: FAIL，编译错误 `undefined: Open`

- [ ] **Step 3: 实现 state**

`internal/state/state.go`：

```go
// Package state 管理 Debian 侧的状态文件（重启记录、dnsmasq known good 指针）。
package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// State 是 state.json 的完整内容。
type State struct {
	Reboot  RebootState  `json:"reboot"`
	Dnsmasq DnsmasqState `json:"dnsmasq"`
}

// RebootState 记录由 MCP 发起的重启时间（只保留最近若干条）。
type RebootState struct {
	History []time.Time `json:"history"`
}

// DnsmasqState 记录回滚目标和 MCP 最后一次写入后的文件指纹。
type DnsmasqState struct {
	KnownGoodBackup string `json:"known_good_backup"`
	LastMCPSHA256   string `json:"last_mcp_sha256"`
}

func (st State) clone() State {
	c := st
	c.Reboot.History = append([]time.Time(nil), st.Reboot.History...)
	return c
}

// Store 在进程内用互斥锁保护状态，每次修改都原子写入磁盘。
type Store struct {
	mu   sync.Mutex
	path string
	cur  State
}

// Open 打开 dir/state.json；文件不存在时返回空状态，文件损坏时返回错误而不是重置。
func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("创建状态目录失败: %w", err)
	}
	s := &Store{path: filepath.Join(dir, "state.json")}
	data, err := os.ReadFile(s.path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return s, nil
	case err != nil:
		return nil, fmt.Errorf("读取状态文件失败: %w", err)
	}
	if err := json.Unmarshal(data, &s.cur); err != nil {
		return nil, fmt.Errorf("状态文件 %s 已损坏，请人工检查后修复或移走（不会自动重置，以免清零重启额度）: %w", s.path, err)
	}
	return s, nil
}

// Get 返回当前状态的深拷贝。
func (s *Store) Get() State {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cur.clone()
}

// Update 在锁内修改状态并落盘。fn 返回错误时原样返回该错误，内存和磁盘都不变。
func (s *Store) Update(fn func(*State) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := s.cur.clone()
	if err := fn(&next); err != nil {
		return err
	}
	data, err := json.MarshalIndent(next, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化状态失败: %w", err)
	}
	if err := writeAtomic(s.path, data); err != nil {
		return fmt.Errorf("写入状态文件失败: %w", err)
	}
	s.cur = next
	return nil
}

// writeAtomic 先写临时文件并 fsync，再 rename 覆盖目标文件。
func writeAtomic(path string, data []byte) error {
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
```

- [ ] **Step 4: 运行测试，确认通过**

Run: `go test ./internal/state/`
Expected: PASS

- [ ] **Step 5: 写 audit 的失败测试**

`internal/audit/audit_test.go`：

```go
package audit

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWriteAppendsJSONLines(t *testing.T) {
	p := filepath.Join(t.TempDir(), "logs", "audit.jsonl")
	l := New(p)
	e := Entry{TS: time.Unix(0, 0).UTC(), Tool: "router_reboot", Args: map[string]any{"confirm": true}, Outcome: "ok", DurationMS: 12}
	if err := l.Write(e); err != nil {
		t.Fatal(err)
	}
	e.Outcome = "rejected"
	e.ErrorCode = "REBOOT_QUOTA_EXCEEDED"
	if err := l.Write(e); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2 {
		t.Fatalf("lines = %d", len(lines))
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(lines[1]), &got); err != nil {
		t.Fatal(err)
	}
	if got["tool"] != "router_reboot" || got["outcome"] != "rejected" || got["error_code"] != "REBOOT_QUOTA_EXCEEDED" {
		t.Fatalf("got = %v", got)
	}
}

func TestRedactMasksSensitiveKeysButNotKeyword(t *testing.T) {
	in := map[string]any{"password": "x", "api_key": "y", "Token": "z", "keyword": "dnsmasq", "lines": []string{"a"}}
	out := Redact(in)
	for _, k := range []string{"password", "api_key", "Token"} {
		if out[k] != "****" {
			t.Errorf("%s 应被脱敏，得到 %v", k, out[k])
		}
	}
	if out["keyword"] != "dnsmasq" {
		t.Errorf("keyword 不应被脱敏，得到 %v", out["keyword"])
	}
	if in["password"] != "x" {
		t.Error("Redact 不应修改输入")
	}
}
```

- [ ] **Step 6: 运行测试，确认失败**

Run: `go test ./internal/audit/`
Expected: FAIL，编译错误 `undefined: New`

- [ ] **Step 7: 实现 audit**

`internal/audit/audit.go`：

```go
// Package audit 以 JSONL 格式记录修改类工具的调用。
package audit

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"time"
)

// Entry 是一条审计记录。
type Entry struct {
	TS         time.Time      `json:"ts"`
	Tool       string         `json:"tool"`
	Args       map[string]any `json:"args,omitempty"`
	Outcome    string         `json:"outcome"` // ok / rejected / error
	ErrorCode  string         `json:"error_code,omitempty"`
	DurationMS int64          `json:"duration_ms"`
	Summary    string         `json:"summary,omitempty"`
}

// Logger 追加写入审计日志文件，并发安全。
type Logger struct {
	mu   sync.Mutex
	path string
}

// New 创建写入 path 的 Logger；目录在第一次写入时创建。
func New(path string) *Logger { return &Logger{path: path} }

// Write 脱敏后追加一行 JSON。
func (l *Logger) Write(e Entry) error {
	e.Args = Redact(e.Args)
	data, err := json.Marshal(e)
	if err != nil {
		return err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := os.MkdirAll(filepath.Dir(l.path), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(l.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(data, '\n'))
	return err
}

// 不使用单独的 key 作为匹配词，避免误伤 keyword 这类参数。
var sensitiveRe = regexp.MustCompile(`(?i)(passwd|password|token|secret|private_key|api_key)`)

// Redact 返回 args 的副本，名称敏感的字段替换为 ****。
func Redact(args map[string]any) map[string]any {
	if args == nil {
		return nil
	}
	out := make(map[string]any, len(args))
	for k, v := range args {
		if sensitiveRe.MatchString(k) {
			out[k] = "****"
		} else {
			out[k] = v
		}
	}
	return out
}
```

- [ ] **Step 8: 全量检查**

Run: `go test ./... && go vet ./... && gofmt -l .`
Expected: PASS，`gofmt -l .` 无输出

- [ ] **Step 9: 提交**

按"提交步骤模板"执行，`<files>` 为 `internal/state internal/audit`，提交说明：`feat: add state store and audit log`

---

### Task 5: SSH 客户端（sshx）

**Files:**
- Create: `internal/sshx/sshx.go`、`internal/sshx/sshx_test.go`
- Modify: `go.mod`、`go.sum`

**Interfaces:**
- Consumes: `runner.Runner`、`runner.Result`（Task 1）；`apperr`（Task 1）
- Produces:
  - `sshx.Options{Addr, User, KeyFile, KnownHosts string; Timeout, DialTimeout time.Duration; MaxSessions int; KeepAlive time.Duration}`
  - `sshx.New(opts Options) (*Client, error)`：读取私钥和 known_hosts；known_hosts 中没有 `Addr` 的记录时返回错误
  - `(*Client).Run(ctx, cmd, stdin) (runner.Result, error)`：实现 `runner.Runner`；错误码为 `SSH_UNREACHABLE` / `SSH_AUTH_FAILED` / `HOSTKEY_MISMATCH` / `TIMEOUT`；远程命令退出码非 0 **不算错误**，体现在 `Result.ExitCode`
  - `(*Client).Reset()`：关闭当前连接，下次调用时重连（重启路由器后使用）
  - `(*Client).Close()`：同 `Reset`

- [ ] **Step 1: 安装 x/crypto 依赖（Task 1 已获用户确认）**

Run: `go get golang.org/x/crypto@latest`
Expected: `go.mod` 中出现 `golang.org/x/crypto`

- [ ] **Step 2: 写失败测试（进程内 SSH 测试服务器）**

`internal/sshx/sshx_test.go`：

```go
package sshx

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"

	"github.com/rshun/merlin-mcp/internal/apperr"
)

type execHandler func(cmd string, stdin []byte) (stdout, stderr string, code int)

type testServer struct {
	addr    string
	hostKey ssh.Signer
	ln      net.Listener
}

func newKey(t *testing.T) (ssh.Signer, ed25519.PrivateKey) {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	s, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	return s, priv
}

// startServer 启动只接受 authorized 公钥、只支持 exec 请求的 SSH 服务器。
func startServer(t *testing.T, authorized ssh.PublicKey, h execHandler) *testServer {
	t.Helper()
	hostKey, _ := newKey(t)
	cfg := &ssh.ServerConfig{
		PublicKeyCallback: func(_ ssh.ConnMetadata, k ssh.PublicKey) (*ssh.Permissions, error) {
			if bytes.Equal(k.Marshal(), authorized.Marshal()) {
				return &ssh.Permissions{}, nil
			}
			return nil, fmt.Errorf("unauthorized")
		},
	}
	cfg.AddHostKey(hostKey)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			nc, err := ln.Accept()
			if err != nil {
				return
			}
			go serveConn(nc, cfg, h)
		}
	}()
	return &testServer{addr: ln.Addr().String(), hostKey: hostKey, ln: ln}
}

func serveConn(nc net.Conn, cfg *ssh.ServerConfig, h execHandler) {
	sc, chans, reqs, err := ssh.NewServerConn(nc, cfg)
	if err != nil {
		nc.Close()
		return
	}
	defer sc.Close()
	go ssh.DiscardRequests(reqs)
	for nch := range chans {
		if nch.ChannelType() != "session" {
			nch.Reject(ssh.UnknownChannelType, "only session")
			continue
		}
		ch, creqs, err := nch.Accept()
		if err != nil {
			continue
		}
		go func() {
			for req := range creqs {
				if req.Type != "exec" {
					req.Reply(false, nil)
					continue
				}
				var p struct{ Command string }
				_ = ssh.Unmarshal(req.Payload, &p)
				req.Reply(true, nil)
				go func(cmd string) {
					stdin, _ := io.ReadAll(ch)
					out, errOut, code := h(cmd, stdin)
					io.WriteString(ch, out)
					io.WriteString(ch.Stderr(), errOut)
					ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{uint32(code)}))
					ch.Close()
				}(p.Command)
			}
		}()
	}
}

// clientOptions 写出客户端私钥和 known_hosts（记录 knownKey 作为服务器的 host key）。
func clientOptions(t *testing.T, addr string, clientPriv ed25519.PrivateKey, knownKey ssh.PublicKey) Options {
	t.Helper()
	dir := t.TempDir()
	block, err := ssh.MarshalPrivateKey(clientPriv, "")
	if err != nil {
		t.Fatal(err)
	}
	keyPath := filepath.Join(dir, "id")
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(block), 0o600); err != nil {
		t.Fatal(err)
	}
	khPath := filepath.Join(dir, "known_hosts")
	line := knownhosts.Line([]string{knownhosts.Normalize(addr)}, knownKey)
	if err := os.WriteFile(khPath, []byte(line+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return Options{Addr: addr, User: "admin", KeyFile: keyPath, KnownHosts: khPath, Timeout: 2 * time.Second, DialTimeout: 2 * time.Second}
}

func echoHandler(cmd string, stdin []byte) (string, string, int) {
	return "cmd=" + cmd + "|stdin=" + string(stdin), "warn", 3
}

func TestRunReturnsOutputAndExitCode(t *testing.T) {
	clientKey, clientPriv := newKey(t)
	srv := startServer(t, clientKey.PublicKey(), echoHandler)
	c, err := New(clientOptions(t, srv.addr, clientPriv, srv.hostKey.PublicKey()))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	res, err := c.Run(context.Background(), "echo hi", []byte("data"))
	if err != nil {
		t.Fatal(err)
	}
	if string(res.Stdout) != "cmd=echo hi|stdin=data" || string(res.Stderr) != "warn" || res.ExitCode != 3 {
		t.Fatalf("res = %+v", res)
	}
	res, err = c.Run(context.Background(), "again", nil)
	if err != nil || string(res.Stdout) != "cmd=again|stdin=" {
		t.Fatalf("复用连接失败: res=%q err=%v", res.Stdout, err)
	}
}

func TestNewRejectsHostMissingFromKnownHosts(t *testing.T) {
	clientKey, clientPriv := newKey(t)
	srv := startServer(t, clientKey.PublicKey(), echoHandler)
	opts := clientOptions(t, "192.0.2.99:22", clientPriv, srv.hostKey.PublicKey())
	opts.Addr = srv.addr
	_, err := New(opts)
	if err == nil || !strings.Contains(err.Error(), "known_hosts 中没有") {
		t.Fatalf("err = %v", err)
	}
}

func TestRunDetectsHostKeyMismatch(t *testing.T) {
	clientKey, clientPriv := newKey(t)
	srv := startServer(t, clientKey.PublicKey(), echoHandler)
	other, _ := newKey(t)
	c, err := New(clientOptions(t, srv.addr, clientPriv, other.PublicKey()))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_, err = c.Run(context.Background(), "x", nil)
	if apperr.CodeOf(err) != apperr.HostKeyMismatch {
		t.Fatalf("err = %v", err)
	}
}

func TestRunDetectsAuthFailure(t *testing.T) {
	authorized, _ := newKey(t)
	_, clientPriv := newKey(t)
	srv := startServer(t, authorized.PublicKey(), echoHandler)
	c, err := New(clientOptions(t, srv.addr, clientPriv, srv.hostKey.PublicKey()))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_, err = c.Run(context.Background(), "x", nil)
	if apperr.CodeOf(err) != apperr.SSHAuthFailed {
		t.Fatalf("err = %v", err)
	}
}

func TestRunTimesOut(t *testing.T) {
	clientKey, clientPriv := newKey(t)
	srv := startServer(t, clientKey.PublicKey(), func(string, []byte) (string, string, int) {
		time.Sleep(time.Second)
		return "", "", 0
	})
	opts := clientOptions(t, srv.addr, clientPriv, srv.hostKey.PublicKey())
	opts.Timeout = 100 * time.Millisecond
	c, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_, err = c.Run(context.Background(), "sleep", nil)
	if apperr.CodeOf(err) != apperr.Timeout {
		t.Fatalf("err = %v", err)
	}
}

func TestRunReportsUnreachable(t *testing.T) {
	clientKey, clientPriv := newKey(t)
	srv := startServer(t, clientKey.PublicKey(), echoHandler)
	c, err := New(clientOptions(t, srv.addr, clientPriv, srv.hostKey.PublicKey()))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	srv.ln.Close()
	_, err = c.Run(context.Background(), "x", nil)
	if apperr.CodeOf(err) != apperr.SSHUnreachable {
		t.Fatalf("err = %v", err)
	}
}

func TestResetReconnects(t *testing.T) {
	clientKey, clientPriv := newKey(t)
	srv := startServer(t, clientKey.PublicKey(), echoHandler)
	c, err := New(clientOptions(t, srv.addr, clientPriv, srv.hostKey.PublicKey()))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if _, err := c.Run(context.Background(), "a", nil); err != nil {
		t.Fatal(err)
	}
	c.Reset()
	if _, err := c.Run(context.Background(), "b", nil); err != nil {
		t.Fatalf("Reset 后应能重连: %v", err)
	}
}
```

- [ ] **Step 3: 运行测试，确认失败**

Run: `go test ./internal/sshx/`
Expected: FAIL，编译错误 `undefined: New`

- [ ] **Step 4: 实现 sshx**

`internal/sshx/sshx.go`：

```go
// Package sshx 实现基于 SSH 的 runner.Runner：长连接复用、host key 校验、超时与并发控制。
package sshx

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"

	"github.com/rshun/merlin-mcp/internal/apperr"
	"github.com/rshun/merlin-mcp/internal/runner"
)

// Options 是 SSH 客户端的配置。
type Options struct {
	Addr        string        // host:port
	User        string        // 路由器用户名
	KeyFile     string        // 私钥路径（不支持带口令的私钥）
	KnownHosts  string        // known_hosts 路径
	Timeout     time.Duration // 调用方没有设置 deadline 时使用的默认命令超时
	DialTimeout time.Duration // 建立 TCP 连接和握手的超时，默认 10s
	MaxSessions int           // 同时打开的 session 上限，默认 4
	KeepAlive   time.Duration // keepalive 间隔，0 表示不发送
}

// Client 维护一条到路由器的 SSH 长连接，每次 Run 打开一个新的 session。
type Client struct {
	opts   Options
	config *ssh.ClientConfig
	sem    chan struct{}

	mu   sync.Mutex
	conn *ssh.Client
}

var _ runner.Runner = (*Client)(nil)

const hintCheckRouter = "检查路由器是否在线、SSH 是否开启，然后重试"

// New 读取私钥和 known_hosts 并完成校验，但不立即连接。
func New(opts Options) (*Client, error) {
	if opts.DialTimeout <= 0 {
		opts.DialTimeout = 10 * time.Second
	}
	if opts.MaxSessions <= 0 {
		opts.MaxSessions = 4
	}
	keyData, err := os.ReadFile(opts.KeyFile)
	if err != nil {
		return nil, fmt.Errorf("读取私钥失败: %w", err)
	}
	signer, err := ssh.ParsePrivateKey(keyData)
	if err != nil {
		var pe *ssh.PassphraseMissingError
		if errors.As(err, &pe) {
			return nil, fmt.Errorf("私钥 %s 设置了口令，暂不支持带口令的私钥", opts.KeyFile)
		}
		return nil, fmt.Errorf("解析私钥失败: %w", err)
	}
	cb, err := knownhosts.New(opts.KnownHosts)
	if err != nil {
		return nil, fmt.Errorf("读取 known_hosts 失败: %w", err)
	}
	algos, err := hostKeyAlgorithms(cb, opts.Addr)
	if err != nil {
		return nil, err
	}
	return &Client{
		opts: opts,
		config: &ssh.ClientConfig{
			User:              opts.User,
			Auth:              []ssh.AuthMethod{ssh.PublicKeys(signer)},
			HostKeyCallback:   cb,
			HostKeyAlgorithms: algos,
			Timeout:           opts.DialTimeout,
		},
		sem: make(chan struct{}, opts.MaxSessions),
	}, nil
}

// hostKeyAlgorithms 用一个随机探测公钥查询 known_hosts，取出该主机已登记的 key 类型。
// 这样握手时只协商 known_hosts 里有的算法，避免服务器优先提供其他类型的 key 导致误报不匹配。
func hostKeyAlgorithms(cb ssh.HostKeyCallback, addr string) ([]string, error) {
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	probe, err := ssh.NewPublicKey(pub)
	if err != nil {
		return nil, err
	}
	err = cb(addr, &net.TCPAddr{IP: net.IPv4zero}, probe)
	var ke *knownhosts.KeyError
	if !errors.As(err, &ke) || len(ke.Want) == 0 {
		return nil, fmt.Errorf("known_hosts 中没有 %s 的记录，请先用 ssh 登录一次或用 ssh-keyscan 添加，并核对指纹", addr)
	}
	seen := map[string]bool{}
	var algos []string
	addAlgo := func(a string) {
		if !seen[a] {
			seen[a] = true
			algos = append(algos, a)
		}
	}
	for _, w := range ke.Want {
		if t := w.Key.Type(); t == ssh.KeyAlgoRSA {
			addAlgo(ssh.KeyAlgoRSASHA512)
			addAlgo(ssh.KeyAlgoRSASHA256)
			addAlgo(ssh.KeyAlgoRSA)
		} else {
			addAlgo(t)
		}
	}
	return algos, nil
}

// Run 实现 runner.Runner。远程退出码非 0 不视为错误。
func (c *Client) Run(ctx context.Context, cmd string, stdin []byte) (runner.Result, error) {
	if _, ok := ctx.Deadline(); !ok && c.opts.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.opts.Timeout)
		defer cancel()
	}
	select {
	case c.sem <- struct{}{}:
		defer func() { <-c.sem }()
	case <-ctx.Done():
		return runner.Result{}, timeoutErr()
	}

	conn, err := c.connect(ctx)
	if err != nil {
		return runner.Result{}, err
	}
	sess, err := conn.NewSession()
	if err != nil {
		c.drop(conn)
		return runner.Result{}, apperr.New(apperr.SSHUnreachable, "无法创建 SSH 会话: "+err.Error(), hintCheckRouter)
	}
	defer sess.Close()

	var stdout, stderr bytes.Buffer
	sess.Stdout = &stdout
	sess.Stderr = &stderr
	if stdin != nil {
		sess.Stdin = bytes.NewReader(stdin)
	}
	done := make(chan error, 1)
	go func() { done <- sess.Run(cmd) }()

	select {
	case err = <-done:
	case <-ctx.Done():
		_ = sess.Signal(ssh.SIGKILL)
		_ = sess.Close()
		return runner.Result{}, timeoutErr()
	}

	res := runner.Result{Stdout: stdout.Bytes(), Stderr: stderr.Bytes()}
	if err == nil {
		return res, nil
	}
	var ee *ssh.ExitError
	if errors.As(err, &ee) {
		res.ExitCode = ee.ExitStatus()
		return res, nil
	}
	c.drop(conn)
	return res, apperr.New(apperr.SSHUnreachable, "SSH 连接中断: "+err.Error(), hintCheckRouter)
}

func timeoutErr() error {
	return apperr.New(apperr.Timeout, "命令执行超时", "可以缩小查询范围后重试，或检查路由器负载")
}

// connect 返回当前连接，没有时建立新连接。
func (c *Client) connect(ctx context.Context) (*ssh.Client, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn != nil {
		return c.conn, nil
	}
	d := net.Dialer{Timeout: c.opts.DialTimeout}
	nc, err := d.DialContext(ctx, "tcp", c.opts.Addr)
	if err != nil {
		return nil, apperr.New(apperr.SSHUnreachable, fmt.Sprintf("无法连接路由器 %s: %v", c.opts.Addr, err), hintCheckRouter)
	}
	_ = nc.SetDeadline(time.Now().Add(c.opts.DialTimeout))
	sc, chans, reqs, err := ssh.NewClientConn(nc, c.opts.Addr, c.config)
	if err != nil {
		nc.Close()
		return nil, classifyHandshake(err)
	}
	_ = nc.SetDeadline(time.Time{})
	conn := ssh.NewClient(sc, chans, reqs)
	c.conn = conn
	if c.opts.KeepAlive > 0 {
		go c.keepAlive(conn)
	}
	return conn, nil
}

func classifyHandshake(err error) error {
	var ke *knownhosts.KeyError
	switch {
	case errors.As(err, &ke) && len(ke.Want) > 0, strings.Contains(err.Error(), "knownhosts: key mismatch"):
		return apperr.New(apperr.HostKeyMismatch, "路由器的 host key 与 known_hosts 记录不一致", "如果路由器刚重置或更换过固件，请人工核对指纹后更新 known_hosts；否则可能存在中间人攻击")
	case errors.As(err, &ke), strings.Contains(err.Error(), "knownhosts: key is unknown"):
		return apperr.New(apperr.HostKeyMismatch, "known_hosts 中没有路由器的记录", "用 ssh 登录一次并核对指纹，或用 ssh-keyscan 添加")
	case strings.Contains(err.Error(), "unable to authenticate"):
		return apperr.New(apperr.SSHAuthFailed, "SSH 密钥认证失败", "确认公钥已添加到路由器的 Authorized Keys，且 router.user 正确")
	default:
		return apperr.New(apperr.SSHUnreachable, "SSH 握手失败: "+err.Error(), hintCheckRouter)
	}
}

func (c *Client) keepAlive(conn *ssh.Client) {
	t := time.NewTicker(c.opts.KeepAlive)
	defer t.Stop()
	for range t.C {
		c.mu.Lock()
		current := c.conn == conn
		c.mu.Unlock()
		if !current {
			return
		}
		if _, _, err := conn.SendRequest("keepalive@openssh.com", true, nil); err != nil {
			c.drop(conn)
			return
		}
	}
}

// drop 在 conn 仍是当前连接时丢弃它，并关闭连接。
func (c *Client) drop(conn *ssh.Client) {
	c.mu.Lock()
	if c.conn == conn {
		c.conn = nil
	}
	c.mu.Unlock()
	conn.Close()
}

// Reset 关闭当前连接，下次 Run 时重新连接。
func (c *Client) Reset() {
	c.mu.Lock()
	conn := c.conn
	c.conn = nil
	c.mu.Unlock()
	if conn != nil {
		conn.Close()
	}
}

// Close 关闭连接。
func (c *Client) Close() { c.Reset() }
```

- [ ] **Step 5: 运行测试，确认通过**

Run: `go test -race ./internal/sshx/`
Expected: PASS。Windows 上如果没有 cgo 导致 `-race` 不可用，改用 `go test ./internal/sshx/`

如果 `TestRunDetectsHostKeyMismatch` 返回的是 `SSH_UNREACHABLE`，打印 `err` 查看握手错误的实际文字，并把 `classifyHandshake` 里的字符串匹配改成实际文字（x/crypto 不同版本的措辞可能不同）。

- [ ] **Step 6: 全量检查**

Run: `go test ./... && go vet ./... && gofmt -l .`
Expected: PASS，`gofmt -l .` 无输出

- [ ] **Step 7: 提交**

按"提交步骤模板"执行，`<files>` 为 `go.mod go.sum internal/sshx`，提交说明：`feat: add SSH runner with host key verification and timeouts`

---

### Task 6: syslog 与 dmesg 读取、解析和过滤

**Files:**
- Create: `internal/syslog/parse.go`、`internal/syslog/parse_test.go`
- Create: `internal/syslog/fetch.go`、`internal/syslog/fetch_test.go`

**Interfaces:**
- Consumes: `runner`、`runnertest`、`apperr`（Task 1）；`shell.Quote`、`routercmd.Marker/Sections/DateCmd/ParseDate`（Task 2）
- Produces:
  - `syslog.Line{Time time.Time; HasTime bool; Process string; Raw string}`
  - `syslog.Parse(raw string, now time.Time) []Line`
  - `syslog.Filter{Since time.Duration; Process, Keyword string; Lines int}`、`syslog.Apply(lines []Line, f Filter, now time.Time) []Line`
  - `syslog.ParseSince(s string) (time.Duration, error)`
  - `syslog.Raws(lines []Line) []string`、`syslog.FilterText(lines []string, keyword string, n int) []string`
  - `syslog.Render(lines []string, maxBytes int) (text string, truncated bool)`、常量 `syslog.MaxOutputBytes = 64 * 1024`
  - `syslog.DetectScript(pathSetting string) string`：执行后 shell 变量 `p` 为 syslog 路径，找不到时退出码 3
  - `syslog.Snapshot{Path string; Now time.Time; Lines []Line}`、`syslog.Fetch(ctx, r, pathSetting string, includeRotated bool) (Snapshot, error)`（操作名 `syslog_fetch`）
  - `syslog.FetchKernel(ctx, r) ([]string, error)`（操作名 `dmesg`）

- [ ] **Step 1: 写解析和过滤的失败测试**

`internal/syslog/parse_test.go`：

```go
package syslog

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

var cst = time.FixedZone("router", 8*3600)

func TestParseBusyboxFormat(t *testing.T) {
	now := time.Date(2026, 10, 3, 12, 0, 0, 0, cst)
	raw := "Oct  3 11:58:01 dnsmasq-dhcp[1234]: DHCPACK(br0) 192.0.2.10 AA:BB:CC:00:00:01 phone\n" +
		"Oct  3 11:59:00 kernel: eth6: link up\n" +
		"Oct  3 11:59:30 RT-AX86U-1234 rc_service: httpd 1234:notify_rc restart_dnsmasq\n"
	lines := Parse(raw, now)
	if len(lines) != 3 {
		t.Fatalf("len = %d", len(lines))
	}
	if lines[0].Process != "dnsmasq-dhcp" || !lines[0].HasTime || !lines[0].Time.Equal(time.Date(2026, 10, 3, 11, 58, 1, 0, cst)) {
		t.Errorf("line0 = %+v", lines[0])
	}
	if lines[1].Process != "kernel" {
		t.Errorf("line1 process = %q", lines[1].Process)
	}
	if lines[2].Process != "rc_service" {
		t.Errorf("带主机名的行 process = %q", lines[2].Process)
	}
}

func TestParseISOFormat(t *testing.T) {
	now := time.Date(2026, 10, 3, 12, 0, 0, 0, cst)
	lines := Parse("2026-10-03T11:00:00+08:00 RT-AX86U dnsmasq[99]: started\n", now)
	if len(lines) != 1 || lines[0].Process != "dnsmasq" || lines[0].Time.Hour() != 11 {
		t.Fatalf("lines = %+v", lines)
	}
}

func TestParseInfersPreviousYear(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 10, 0, 0, cst)
	lines := Parse("Dec 31 23:59:00 kernel: old\nJan  1 00:05:00 kernel: new\n", now)
	if lines[0].Time.Year() != 2025 || lines[1].Time.Year() != 2026 {
		t.Fatalf("years = %d %d", lines[0].Time.Year(), lines[1].Time.Year())
	}
}

func TestParseWithUnsyncedClock(t *testing.T) {
	now := time.Unix(300, 0).In(cst) // 1970-01-01，路由器还没同步 NTP
	lines := Parse("Jan  1 08:01:00 kernel: boot\nOct  3 11:00:00 kernel: future\n", now)
	if len(lines) != 2 || !lines[0].HasTime || !lines[1].HasTime {
		t.Fatalf("lines = %+v", lines)
	}
	if lines[1].Time.After(now) {
		t.Fatal("推断出的时间不应晚于路由器当前时间")
	}
}

func TestParseLineWithoutTimestampInheritsPrevious(t *testing.T) {
	now := time.Date(2026, 10, 3, 12, 0, 0, 0, cst)
	lines := Parse("orphan before any time\nOct  3 11:00:00 kernel: a\ncontinuation line\n", now)
	if lines[0].HasTime {
		t.Error("第一行之前没有时间，HasTime 应为 false")
	}
	if !lines[2].HasTime || !lines[2].Time.Equal(lines[1].Time) {
		t.Errorf("续行应继承前一行时间: %+v", lines[2])
	}
}

func TestApplyFilters(t *testing.T) {
	now := time.Date(2026, 10, 3, 12, 0, 0, 0, cst)
	raw := "Oct  3 10:00:00 dnsmasq[1]: old\n" +
		"Oct  3 11:30:00 dnsmasq[1]: Query Example.com\n" +
		"Oct  3 11:40:00 dnsmasq-dhcp[2]: DHCPACK example\n" +
		"Oct  3 11:50:00 kernel: EXAMPLE kernel\n"
	lines := Parse(raw, now)

	got := Apply(lines, Filter{Since: time.Hour}, now)
	if len(got) != 3 {
		t.Errorf("since=1h 应得到 3 行，得到 %d", len(got))
	}
	got = Apply(lines, Filter{Process: "dnsmasq"}, now)
	if len(got) != 2 {
		t.Errorf("process=dnsmasq 应精确匹配 2 行，得到 %d", len(got))
	}
	got = Apply(lines, Filter{Keyword: "example"}, now)
	if len(got) != 3 {
		t.Errorf("keyword 不区分大小写应得到 3 行，得到 %d", len(got))
	}
	got = Apply(lines, Filter{Lines: 2}, now)
	if len(got) != 2 || !strings.Contains(got[1].Raw, "kernel") {
		t.Errorf("lines=2 应保留最新的 2 行: %+v", got)
	}
}

func TestApplySinceDropsLinesWithoutTime(t *testing.T) {
	now := time.Date(2026, 10, 3, 12, 0, 0, 0, cst)
	lines := Parse("no time at all\n", now)
	if got := Apply(lines, Filter{Since: time.Hour}, now); len(got) != 0 {
		t.Fatalf("没有时间的行在 since 过滤下应被排除: %+v", got)
	}
}

func TestParseSince(t *testing.T) {
	ok := map[string]time.Duration{"": 0, "30m": 30 * time.Minute, "2h": 2 * time.Hour, "1d": 24 * time.Hour, "7d": 7 * 24 * time.Hour}
	for in, want := range ok {
		got, err := ParseSince(in)
		if err != nil || got != want {
			t.Errorf("ParseSince(%q) = %v, %v", in, got, err)
		}
	}
	for _, in := range []string{"0m", "8d", "5x", "1w", "-1h", "1.5h", "h"} {
		if _, err := ParseSince(in); err == nil {
			t.Errorf("ParseSince(%q) 应失败", in)
		}
	}
}

func TestRenderKeepsNewestLines(t *testing.T) {
	lines := []string{"aaaa", "bbbb", "cccc"}
	text, truncated := Render(lines, 10)
	if text != "bbbb\ncccc\n" || !truncated {
		t.Fatalf("text=%q truncated=%v", text, truncated)
	}
	text, truncated = Render(lines, 100)
	if text != "aaaa\nbbbb\ncccc\n" || truncated {
		t.Fatalf("text=%q truncated=%v", text, truncated)
	}
	if text, truncated := Render(nil, 10); text != "" || truncated {
		t.Fatalf("空输入: %q %v", text, truncated)
	}
}

func TestRenderKeepsValidUTF8(t *testing.T) {
	huge := strings.Repeat("中", 30000) // 90000 字节的单行
	text, truncated := Render([]string{huge}, MaxOutputBytes)
	if !truncated || !utf8.ValidString(text) || len(text) > MaxOutputBytes {
		t.Fatalf("truncated=%v valid=%v len=%d", truncated, utf8.ValidString(text), len(text))
	}
	text, _ = Render([]string{"abc\xff\xfedef"}, MaxOutputBytes)
	if !utf8.ValidString(text) {
		t.Fatal("非法 UTF-8 应被替换")
	}
}

func TestFilterText(t *testing.T) {
	lines := []string{"[1.0] eth6: link up", "[2.0] Out of memory: Kill process", "[3.0] ETH6 down"}
	got := FilterText(lines, "eth6", 0)
	if len(got) != 2 {
		t.Errorf("got %v", got)
	}
	got = FilterText(lines, "", 1)
	if len(got) != 1 || got[0] != "[3.0] ETH6 down" {
		t.Errorf("got %v", got)
	}
}
```

- [ ] **Step 2: 运行测试，确认失败**

Run: `go test ./internal/syslog/`
Expected: FAIL，编译错误 `undefined: Parse`

- [ ] **Step 3: 实现解析和过滤**

`internal/syslog/parse.go`：

```go
// Package syslog 解析和过滤 Merlin 的 syslog 与 dmesg 输出。
package syslog

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// MaxOutputBytes 是单次返回给 AI 的日志上限。
const MaxOutputBytes = 64 * 1024

// Line 是解析后的一行 syslog。
type Line struct {
	Time    time.Time
	HasTime bool   // 本行或之前的行带有可解析的时间戳
	Process string // 进程名（不含 [pid]），无法识别时为空
	Raw     string // 原始文本
}

const bsdLayout = "Jan _2 15:04:05"

// Parse 解析 syslog 文本。now 是路由器当前时间（带路由器时区），用于推断年份。
// 无法解析时间戳的行沿用前一行的时间。
func Parse(raw string, now time.Time) []Line {
	var out []Line
	var last time.Time
	have := false
	for _, s := range strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n") {
		if s == "" {
			continue
		}
		l := Line{Raw: s}
		if t, rest, ok := parseTime(s, now); ok {
			last, have = t, true
			l.Process = processOf(rest)
		}
		l.Time, l.HasTime = last, have
		out = append(out, l)
	}
	return out
}

func parseTime(s string, now time.Time) (time.Time, string, bool) {
	if len(s) >= 15 {
		if t, err := time.ParseInLocation(bsdLayout, s[:15], now.Location()); err == nil {
			t = time.Date(now.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), t.Second(), 0, now.Location())
			// 时间戳没有年份：晚于"现在"（允许 1 分钟误差）的说明是去年的日志
			if t.After(now.Add(time.Minute)) {
				t = t.AddDate(-1, 0, 0)
			}
			return t, strings.TrimSpace(s[15:]), true
		}
	}
	if i := strings.IndexByte(s, ' '); i > 0 {
		if t, err := time.Parse(time.RFC3339, s[:i]); err == nil {
			return t, strings.TrimSpace(s[i+1:]), true
		}
	}
	return time.Time{}, "", false
}

// processOf 从时间戳之后的内容中取进程名：第一个或第二个（前面有主机名时）以冒号结尾的词。
func processOf(rest string) string {
	f := strings.Fields(rest)
	for i := 0; i < len(f) && i < 2; i++ {
		if strings.HasSuffix(f[i], ":") {
			name := strings.TrimSuffix(f[i], ":")
			if j := strings.IndexByte(name, '['); j >= 0 {
				name = name[:j]
			}
			return name
		}
	}
	return ""
}

// Filter 是 syslog_read 的过滤条件，零值表示不过滤。
type Filter struct {
	Since   time.Duration
	Process string
	Keyword string
	Lines   int
}

// Apply 按 时间 → 进程 → 关键字 的顺序过滤，最后保留最新的 Lines 行。
func Apply(lines []Line, f Filter, now time.Time) []Line {
	cut := now.Add(-f.Since)
	kw := strings.ToLower(f.Keyword)
	var out []Line
	for _, l := range lines {
		if f.Since > 0 && (!l.HasTime || l.Time.Before(cut)) {
			continue
		}
		if f.Process != "" && l.Process != f.Process {
			continue
		}
		if kw != "" && !strings.Contains(strings.ToLower(l.Raw), kw) {
			continue
		}
		out = append(out, l)
	}
	if f.Lines > 0 && len(out) > f.Lines {
		out = out[len(out)-f.Lines:]
	}
	return out
}

var sinceRe = regexp.MustCompile(`^([1-9][0-9]{0,4})([mhd])$`)

// ParseSince 解析 30m、2h、1d 这类相对时间，最大 7d；空字符串返回 0。
func ParseSince(s string) (time.Duration, error) {
	if s == "" {
		return 0, nil
	}
	m := sinceRe.FindStringSubmatch(s)
	if m == nil {
		return 0, fmt.Errorf("since 格式错误 %q，应为 <数字><m|h|d>", s)
	}
	n, _ := strconv.Atoi(m[1])
	unit := map[string]time.Duration{"m": time.Minute, "h": time.Hour, "d": 24 * time.Hour}[m[2]]
	d := time.Duration(n) * unit
	if d > 7*24*time.Hour {
		return 0, fmt.Errorf("since 最大为 7d，当前为 %q", s)
	}
	return d, nil
}

// Raws 取出原始文本。
func Raws(lines []Line) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = l.Raw
	}
	return out
}

// FilterText 按关键字（不区分大小写）过滤纯文本行，n > 0 时保留最后 n 行。
func FilterText(lines []string, keyword string, n int) []string {
	kw := strings.ToLower(keyword)
	var out []string
	for _, l := range lines {
		if kw == "" || strings.Contains(strings.ToLower(l), kw) {
			out = append(out, l)
		}
	}
	if n > 0 && len(out) > n {
		out = out[len(out)-n:]
	}
	return out
}

// Render 拼接日志行。超过 maxBytes 时从前面丢弃整行、保留最新的内容；
// 最新的一行本身就超长时只保留它的末尾。结果总是合法的 UTF-8。
func Render(lines []string, maxBytes int) (string, bool) {
	total, start := 0, len(lines)
	for i := len(lines) - 1; i >= 0; i-- {
		n := len(lines[i]) + 1
		if total+n > maxBytes {
			break
		}
		total += n
		start = i
	}
	if start == len(lines) && len(lines) > 0 {
		last := lines[len(lines)-1]
		tail := last[len(last)-(maxBytes-1):]
		return strings.ToValidUTF8(tail, "") + "\n", true
	}
	text := strings.Join(lines[start:], "\n")
	if text != "" {
		text += "\n"
	}
	return strings.ToValidUTF8(text, "�"), start > 0
}
```

- [ ] **Step 4: 运行测试，确认通过**

Run: `go test ./internal/syslog/`
Expected: PASS

- [ ] **Step 5: 写读取的失败测试**

`internal/syslog/fetch_test.go`：

```go
package syslog

import (
	"context"
	"strings"
	"testing"

	"github.com/rshun/merlin-mcp/internal/apperr"
	"github.com/rshun/merlin-mcp/internal/runner/runnertest"
)

const fetchOutput = "@@MERLINMCP:path@@\n/jffs/syslog.log\n" +
	"@@MERLINMCP:date@@\n1791000000 +0800\n" +
	"@@MERLINMCP:rotated@@\nOct  3 09:00:00 kernel: from rotated\n" +
	"@@MERLINMCP:current@@\nOct  3 10:00:00 dnsmasq[1]: from current\n"

func TestFetchAutoDetectAndRotated(t *testing.T) {
	f := runnertest.New().On("syslog_fetch", runnertest.Stdout(fetchOutput))
	snap, err := Fetch(context.Background(), f, "auto", true)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Path != "/jffs/syslog.log" || snap.Now.Unix() != 1791000000 {
		t.Fatalf("snap = %+v", snap)
	}
	if len(snap.Lines) != 2 || !strings.Contains(snap.Lines[0].Raw, "rotated") || !strings.Contains(snap.Lines[1].Raw, "current") {
		t.Fatalf("lines = %+v", snap.Lines)
	}
	cmd := f.CallsFor("syslog_fetch")[0].Cmd
	if !strings.Contains(cmd, "/jffs/syslog.log /tmp/syslog.log") || !strings.Contains(cmd, `"$p-1"`) {
		t.Fatalf("cmd = %s", cmd)
	}
}

func TestFetchWithoutRotatedDoesNotReadIt(t *testing.T) {
	f := runnertest.New().On("syslog_fetch", runnertest.Stdout(fetchOutput))
	_, _ = Fetch(context.Background(), f, "auto", false)
	if strings.Contains(f.CallsFor("syslog_fetch")[0].Cmd, `"$p-1"`) {
		t.Fatal("include_rotated=false 时不应读取轮转文件")
	}
}

func TestFetchExplicitPathIsQuoted(t *testing.T) {
	f := runnertest.New().On("syslog_fetch", runnertest.Stdout(fetchOutput))
	_, _ = Fetch(context.Background(), f, "/opt/var/log/messages", false)
	if !strings.Contains(f.CallsFor("syslog_fetch")[0].Cmd, "p='/opt/var/log/messages'") {
		t.Fatalf("cmd = %s", f.CallsFor("syslog_fetch")[0].Cmd)
	}
}

func TestFetchMissingFile(t *testing.T) {
	f := runnertest.New().On("syslog_fetch", runnertest.Exit(3, "", "找不到 syslog 文件"))
	_, err := Fetch(context.Background(), f, "auto", false)
	e := apperr.From(err)
	if e.Code != apperr.CommandFailed || !strings.Contains(e.Hint, "paths.syslog") {
		t.Fatalf("err = %+v", e)
	}
}

func TestFetchKernel(t *testing.T) {
	f := runnertest.New().On("dmesg", runnertest.Stdout("[1.0] a\n\n[2.0] b\n"))
	lines, err := FetchKernel(context.Background(), f)
	if err != nil || len(lines) != 2 || lines[1] != "[2.0] b" {
		t.Fatalf("lines=%v err=%v", lines, err)
	}
}
```

- [ ] **Step 6: 运行测试，确认失败**

Run: `go test ./internal/syslog/`
Expected: FAIL，编译错误 `undefined: Fetch`

- [ ] **Step 7: 实现读取**

`internal/syslog/fetch.go`：

```go
package syslog

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/rshun/merlin-mcp/internal/apperr"
	"github.com/rshun/merlin-mcp/internal/routercmd"
	"github.com/rshun/merlin-mcp/internal/runner"
	"github.com/rshun/merlin-mcp/internal/shell"
)

// 每个日志文件最多读取末尾 4MB。
const maxReadBytes = 4 << 20

// DetectScript 返回一段 shell：执行后变量 p 为 syslog 文件路径；找不到时以退出码 3 结束。
// pathSetting 为 "auto" 时依次尝试 /jffs/syslog.log 和 /tmp/syslog.log。
func DetectScript(pathSetting string) string {
	if pathSetting != "auto" {
		return "p=" + shell.Quote(pathSetting) + `; [ -f "$p" ] || { echo "syslog 文件不存在: $p" >&2; exit 3; }`
	}
	return `p=''; for f in /jffs/syslog.log /tmp/syslog.log; do if [ -f "$f" ]; then p="$f"; break; fi; done; ` +
		`[ -n "$p" ] || { echo "找不到 syslog 文件（已尝试 /jffs/syslog.log 和 /tmp/syslog.log）" >&2; exit 3; }`
}

// Snapshot 是一次读取的结果。
type Snapshot struct {
	Path  string
	Now   time.Time
	Lines []Line
}

// Fetch 一次 SSH 调用取回路由器时间和日志内容，在本地解析。
func Fetch(ctx context.Context, r runner.Runner, pathSetting string, includeRotated bool) (Snapshot, error) {
	parts := []string{
		DetectScript(pathSetting),
		routercmd.Marker("path"), `echo "$p"`,
		routercmd.Marker("date"), routercmd.DateCmd,
	}
	if includeRotated {
		parts = append(parts, routercmd.Marker("rotated"), fmt.Sprintf(`tail -c %d "$p-1" 2>/dev/null`, maxReadBytes))
	}
	parts = append(parts, routercmd.Marker("current"), fmt.Sprintf(`tail -c %d "$p"`, maxReadBytes))

	res, err := r.Run(ctx, runner.Op("syslog_fetch", strings.Join(parts, "; ")), nil)
	if err != nil {
		return Snapshot{}, err
	}
	if res.ExitCode == 3 {
		return Snapshot{}, apperr.New(apperr.CommandFailed, strings.TrimSpace(string(res.Stderr)), "在配置文件的 paths.syslog 中指定 syslog 的实际路径")
	}
	if res.ExitCode != 0 {
		return Snapshot{}, runner.Failed(res)
	}
	sec := routercmd.Sections(string(res.Stdout))
	now, err := routercmd.ParseDate(sec["date"])
	if err != nil {
		return Snapshot{}, apperr.New(apperr.Internal, err.Error(), "")
	}
	var b strings.Builder
	for _, name := range []string{"rotated", "current"} {
		s := dropPartial(sec[name])
		b.WriteString(s)
		if s != "" && !strings.HasSuffix(s, "\n") {
			b.WriteByte('\n')
		}
	}
	return Snapshot{Path: strings.TrimSpace(sec["path"]), Now: now, Lines: Parse(b.String(), now)}, nil
}

// dropPartial 在内容达到读取上限时丢弃第一行（tail -c 可能从一行的中间开始）。
func dropPartial(s string) string {
	if len(s) < maxReadBytes {
		return s
	}
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[i+1:]
	}
	return s
}

// FetchKernel 读取 dmesg，返回非空行。
func FetchKernel(ctx context.Context, r runner.Runner) ([]string, error) {
	out, err := runner.Output(ctx, r, runner.Op("dmesg", "dmesg"))
	if err != nil {
		return nil, err
	}
	var lines []string
	for _, l := range strings.Split(strings.ReplaceAll(out, "\r\n", "\n"), "\n") {
		if l != "" {
			lines = append(lines, l)
		}
	}
	return lines, nil
}
```

- [ ] **Step 8: 全量检查**

Run: `go test ./... && go vet ./... && gofmt -l .`
Expected: PASS，`gofmt -l .` 无输出

- [ ] **Step 9: 提交**

按"提交步骤模板"执行，`<files>` 为 `internal/syslog`，提交说明：`feat: add syslog and dmesg fetching, parsing and filtering`

---

### Task 7: 系统、WAN、conntrack 状态

**Files:**
- Create: `internal/status/system.go`、`internal/status/system_test.go`
- Create: `internal/status/wan.go`、`internal/status/wan_test.go`
- Create: `internal/status/conntrack.go`、`internal/status/conntrack_test.go`

**Interfaces:**
- Consumes: `runner`、`runnertest`、`apperr`（Task 1）；`routercmd`（Task 2）；`syslog.Fetch`、`syslog.Line`（Task 6）
- Produces:
  - `status.System{Model, Firmware string; UptimeSeconds int64; Load [3]float64; CPUPercent float64; MemTotalKB, MemAvailableKB int64; MemUsedPercent float64; TemperatureC *float64}`
  - `status.ParseSystem(out string) (System, error)`、`status.FetchSystem(ctx, r) (System, error)`（操作名 `system_status`）
  - `status.WAN{State, StateCode, SubState, AuxState, Proto, IP, Gateway string; DNS, Events []string; EventsError string}`
  - `status.ParseWAN(out string) WAN`、`status.WANEvents(lines []syslog.Line, n int) []string`、`status.FetchWAN(ctx, r, syslogPath string) (WAN, error)`（操作名 `wan_status`，事件读取使用 `syslog_fetch`）
  - `status.Conntrack{Count, Max int; UsagePercent float64; Warning string}`
  - `status.ParseConntrack(out string) (Conntrack, error)`、`status.FetchConntrack(ctx, r) (Conntrack, error)`（操作名 `conntrack`）

- [ ] **Step 1: 写系统状态的失败测试**

`internal/status/system_test.go`：

```go
package status

import (
	"context"
	"strings"
	"testing"

	"github.com/rshun/merlin-mcp/internal/runner/runnertest"
)

const systemSample = `@@MERLINMCP:uptime@@
12345.67 40000.00
@@MERLINMCP:loadavg@@
0.52 0.40 0.35 2/150 1234
@@MERLINMCP:meminfo@@
MemTotal:         524288 kB
MemFree:          100000 kB
MemAvailable:     262144 kB
Buffers:           10000 kB
Cached:            50000 kB
@@MERLINMCP:stat1@@
cpu  1000 0 1000 8000 0 0 0 0 0 0
@@MERLINMCP:stat2@@
cpu  1100 0 1100 8800 0 0 0 0 0 0
@@MERLINMCP:temp@@
68123
@@MERLINMCP:nvram@@
productid=RT-AX86U
firmver=3.0.0.4
buildno=388.8
extendno=4
`

func TestParseSystem(t *testing.T) {
	s, err := ParseSystem(systemSample)
	if err != nil {
		t.Fatal(err)
	}
	if s.Model != "RT-AX86U" || s.Firmware != "3.0.0.4.388.8_4" {
		t.Errorf("model/firmware = %q %q", s.Model, s.Firmware)
	}
	if s.UptimeSeconds != 12345 || s.Load != [3]float64{0.52, 0.40, 0.35} {
		t.Errorf("uptime/load = %d %v", s.UptimeSeconds, s.Load)
	}
	if s.CPUPercent != 20 {
		t.Errorf("cpu = %v，期望 20", s.CPUPercent)
	}
	if s.MemTotalKB != 524288 || s.MemAvailableKB != 262144 || s.MemUsedPercent != 50 {
		t.Errorf("mem = %d %d %v", s.MemTotalKB, s.MemAvailableKB, s.MemUsedPercent)
	}
	if s.TemperatureC == nil || *s.TemperatureC != 68.1 {
		t.Errorf("temp = %v", s.TemperatureC)
	}
}

func TestParseSystemTemperatureVariants(t *testing.T) {
	out := strings.Replace(systemSample, "68123", "CPU temperature : 71°C", 1)
	s, _ := ParseSystem(out)
	if s.TemperatureC == nil || *s.TemperatureC != 71 {
		t.Errorf("dmu 格式温度 = %v", s.TemperatureC)
	}
	out = strings.Replace(systemSample, "68123\n", "", 1)
	s, _ = ParseSystem(out)
	if s.TemperatureC != nil {
		t.Errorf("没有温度时应为 nil，得到 %v", *s.TemperatureC)
	}
}

func TestParseSystemWithoutMemAvailable(t *testing.T) {
	out := strings.Replace(systemSample, "MemAvailable:     262144 kB\n", "", 1)
	s, err := ParseSystem(out)
	if err != nil {
		t.Fatal(err)
	}
	if s.MemAvailableKB != 160000 {
		t.Errorf("回退计算 MemFree+Buffers+Cached = %d，期望 160000", s.MemAvailableKB)
	}
}

func TestParseSystemMissingSectionFails(t *testing.T) {
	if _, err := ParseSystem("@@MERLINMCP:loadavg@@\n0 0 0\n"); err == nil {
		t.Fatal("缺少 uptime 应报错")
	}
}

func TestFetchSystemUsesWhitelistedNvramOnly(t *testing.T) {
	f := runnertest.New().On("system_status", runnertest.Stdout(systemSample))
	if _, err := FetchSystem(context.Background(), f); err != nil {
		t.Fatal(err)
	}
	cmd := f.CallsFor("system_status")[0].Cmd
	if strings.Contains(cmd, "passwd") || !strings.Contains(cmd, "nvram get productid") {
		t.Fatalf("cmd = %s", cmd)
	}
}
```

- [ ] **Step 2: 运行测试，确认失败**

Run: `go test ./internal/status/`
Expected: FAIL，编译错误 `undefined: ParseSystem`

- [ ] **Step 3: 实现系统状态**

`internal/status/system.go`：

```go
// Package status 采集并解析路由器的系统、WAN 和连接跟踪状态。
package status

import (
	"context"
	"math"
	"regexp"
	"strconv"
	"strings"

	"github.com/rshun/merlin-mcp/internal/apperr"
	"github.com/rshun/merlin-mcp/internal/routercmd"
	"github.com/rshun/merlin-mcp/internal/runner"
)

// System 是 system_status 的返回内容（不含重启额度，由工具层补充）。
type System struct {
	Model          string     `json:"model"`
	Firmware       string     `json:"firmware"`
	UptimeSeconds  int64      `json:"uptime_seconds"`
	Load           [3]float64 `json:"load_average"`
	CPUPercent     float64    `json:"cpu_percent"`
	MemTotalKB     int64      `json:"mem_total_kb"`
	MemAvailableKB int64      `json:"mem_available_kb"`
	MemUsedPercent float64    `json:"mem_used_percent"`
	TemperatureC   *float64   `json:"temperature_c"`
}

var systemKeys = []string{"productid", "firmver", "buildno", "extendno"}

func systemCmd() string {
	return runner.Op("system_status", strings.Join([]string{
		routercmd.Marker("uptime"), "cat /proc/uptime",
		routercmd.Marker("loadavg"), "cat /proc/loadavg",
		routercmd.Marker("meminfo"), "cat /proc/meminfo",
		routercmd.Marker("stat1"), "head -n 1 /proc/stat",
		"sleep 1",
		routercmd.Marker("stat2"), "head -n 1 /proc/stat",
		routercmd.Marker("temp"), "(cat /sys/class/thermal/thermal_zone0/temp || cat /proc/dmu/temperature) 2>/dev/null",
		routercmd.Marker("nvram"), routercmd.MustNvramScript(systemKeys...),
	}, "; "))
}

// FetchSystem 一次 SSH 调用采集全部系统状态（其中 CPU 采样间隔 1 秒）。
func FetchSystem(ctx context.Context, r runner.Runner) (System, error) {
	out, err := runner.Output(ctx, r, systemCmd())
	if err != nil {
		return System{}, err
	}
	return ParseSystem(out)
}

// ParseSystem 解析 systemCmd 的输出。
func ParseSystem(out string) (System, error) {
	sec := routercmd.Sections(out)
	var s System

	up := strings.Fields(sec["uptime"])
	if len(up) == 0 {
		return s, parseErr("/proc/uptime")
	}
	f, err := strconv.ParseFloat(up[0], 64)
	if err != nil {
		return s, parseErr("/proc/uptime")
	}
	s.UptimeSeconds = int64(f)

	la := strings.Fields(sec["loadavg"])
	if len(la) < 3 {
		return s, parseErr("/proc/loadavg")
	}
	for i := 0; i < 3; i++ {
		s.Load[i], _ = strconv.ParseFloat(la[i], 64)
	}

	mem := parseMeminfo(sec["meminfo"])
	total := mem["MemTotal"]
	if total == 0 {
		return s, parseErr("/proc/meminfo")
	}
	avail, ok := mem["MemAvailable"]
	if !ok {
		avail = mem["MemFree"] + mem["Buffers"] + mem["Cached"]
	}
	s.MemTotalKB, s.MemAvailableKB = total, avail
	s.MemUsedPercent = round1(100 * float64(total-avail) / float64(total))

	s.CPUPercent = cpuPercent(sec["stat1"], sec["stat2"])
	s.TemperatureC = parseTemp(sec["temp"])

	kv := routercmd.ParseKV(sec["nvram"])
	s.Model = kv["productid"]
	s.Firmware = firmware(kv)
	return s, nil
}

func parseErr(what string) error {
	return apperr.New(apperr.Internal, "无法解析路由器的 "+what+" 输出", "可能是固件输出格式不同，请把原始输出提供给开发者")
}

func parseMeminfo(s string) map[string]int64 {
	m := map[string]int64{}
	for _, line := range strings.Split(s, "\n") {
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		f := strings.Fields(v)
		if len(f) == 0 {
			continue
		}
		if n, err := strconv.ParseInt(f[0], 10, 64); err == nil {
			m[strings.TrimSpace(k)] = n
		}
	}
	return m
}

// cpuTimes 解析 /proc/stat 的 cpu 行，idle 包含 iowait。
func cpuTimes(line string) (idle, total uint64, ok bool) {
	f := strings.Fields(line)
	if len(f) < 5 || f[0] != "cpu" {
		return 0, 0, false
	}
	for i, v := range f[1:] {
		n, err := strconv.ParseUint(v, 10, 64)
		if err != nil {
			return 0, 0, false
		}
		total += n
		if i == 3 || i == 4 {
			idle += n
		}
	}
	return idle, total, true
}

func cpuPercent(a, b string) float64 {
	i1, t1, ok1 := cpuTimes(strings.TrimSpace(a))
	i2, t2, ok2 := cpuTimes(strings.TrimSpace(b))
	if !ok1 || !ok2 || t2 <= t1 || i2 < i1 {
		return 0
	}
	busy := float64((t2 - t1) - (i2 - i1))
	return round1(100 * busy / float64(t2-t1))
}

var numRe = regexp.MustCompile(`-?\d+(\.\d+)?`)

// parseTemp 支持 thermal_zone 的毫摄氏度和 /proc/dmu/temperature 的文字格式，读不到返回 nil。
func parseTemp(s string) *float64 {
	m := numRe.FindString(s)
	if m == "" {
		return nil
	}
	v, err := strconv.ParseFloat(m, 64)
	if err != nil {
		return nil
	}
	if v > 1000 {
		v /= 1000
	}
	v = round1(v)
	return &v
}

func firmware(kv map[string]string) string {
	var parts []string
	for _, k := range []string{"firmver", "buildno"} {
		if kv[k] != "" {
			parts = append(parts, kv[k])
		}
	}
	fw := strings.Join(parts, ".")
	if e := kv["extendno"]; e != "" && fw != "" {
		fw += "_" + e
	}
	return fw
}

func round1(f float64) float64 { return math.Round(f*10) / 10 }
```

- [ ] **Step 4: 运行测试，确认通过**

Run: `go test ./internal/status/`
Expected: PASS

- [ ] **Step 5: 写 WAN 和 conntrack 的失败测试**

`internal/status/wan_test.go`：

```go
package status

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/rshun/merlin-mcp/internal/runner/runnertest"
	"github.com/rshun/merlin-mcp/internal/syslog"
)

const wanSample = "wan0_state_t=2\nwan0_sbstate_t=0\nwan0_auxstate_t=0\nwan0_proto=dhcp\nwan0_ipaddr=198.51.100.7\nwan0_gateway=198.51.100.1\nwan0_dns=198.51.100.53 198.51.100.54\n"

func TestParseWAN(t *testing.T) {
	w := ParseWAN(wanSample)
	if w.State != "connected" || w.StateCode != "2" || w.Proto != "dhcp" || w.IP != "198.51.100.7" || w.Gateway != "198.51.100.1" {
		t.Fatalf("w = %+v", w)
	}
	if len(w.DNS) != 2 || w.DNS[1] != "198.51.100.54" {
		t.Fatalf("dns = %v", w.DNS)
	}
	if ParseWAN("wan0_state_t=9\n").State != "unknown" {
		t.Fatal("未知状态码应为 unknown")
	}
	if d := ParseWAN("").DNS; d == nil || len(d) != 0 {
		t.Fatal("没有 DNS 时应返回空数组而不是 nil")
	}
}

func TestWANEvents(t *testing.T) {
	now := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	lines := syslog.Parse("Oct  3 10:00:00 WAN(0) Connection: WAN was restored.\n"+
		"Oct  3 10:01:00 dnsmasq[1]: query\n"+
		"Oct  3 10:02:00 wan: finish adding multi routes\n"+
		"Oct  3 10:03:00 udhcpc: bound to 198.51.100.7\n"+
		"Oct  3 10:04:00 kernel: swan is not wan\n", now)
	ev := WANEvents(lines, 2)
	// 匹配到 4 条（WAN(0)、wan:、udhcpc、末尾的单词 wan），保留最后 2 条
	if len(ev) != 2 || !strings.Contains(ev[0], "udhcpc") || !strings.Contains(ev[1], "kernel: swan") {
		t.Fatalf("events = %v", ev)
	}
	if e := WANEvents(nil, 20); e == nil || len(e) != 0 {
		t.Fatal("没有事件时应返回空数组")
	}
}

func TestFetchWAN(t *testing.T) {
	f := runnertest.New().
		On("wan_status", runnertest.Stdout(wanSample)).
		On("syslog_fetch", runnertest.Stdout("@@MERLINMCP:path@@\n/jffs/syslog.log\n@@MERLINMCP:date@@\n1791000000 +0800\n@@MERLINMCP:current@@\nOct  3 10:00:00 WAN(0) Connection: WAN was restored.\n"))
	w, err := FetchWAN(context.Background(), f, "auto")
	if err != nil {
		t.Fatal(err)
	}
	if w.State != "connected" || len(w.Events) != 1 || w.EventsError != "" {
		t.Fatalf("w = %+v", w)
	}
}

func TestFetchWANToleratesSyslogFailure(t *testing.T) {
	f := runnertest.New().
		On("wan_status", runnertest.Stdout(wanSample)).
		On("syslog_fetch", runnertest.Fail(errors.New("boom")))
	w, err := FetchWAN(context.Background(), f, "auto")
	if err != nil {
		t.Fatal(err)
	}
	if w.EventsError == "" || len(w.Events) != 0 {
		t.Fatalf("w = %+v", w)
	}
}
```

`internal/status/conntrack_test.go`：

```go
package status

import (
	"context"
	"testing"

	"github.com/rshun/merlin-mcp/internal/runner/runnertest"
)

func TestParseConntrack(t *testing.T) {
	c, err := ParseConntrack("@@MERLINMCP:count@@\n1500\n@@MERLINMCP:max@@\n30000\n")
	if err != nil {
		t.Fatal(err)
	}
	if c.Count != 1500 || c.Max != 30000 || c.UsagePercent != 5 || c.Warning != "" {
		t.Fatalf("c = %+v", c)
	}
	c, _ = ParseConntrack("@@MERLINMCP:count@@\n25000\n@@MERLINMCP:max@@\n30000\n")
	if c.Warning == "" {
		t.Fatal("使用率 >= 80% 应给出提示")
	}
	if _, err := ParseConntrack("@@MERLINMCP:count@@\n\n@@MERLINMCP:max@@\n0\n"); err == nil {
		t.Fatal("无法解析时应报错")
	}
}

func TestFetchConntrack(t *testing.T) {
	f := runnertest.New().On("conntrack", runnertest.Stdout("@@MERLINMCP:count@@\n10\n@@MERLINMCP:max@@\n100\n"))
	c, err := FetchConntrack(context.Background(), f)
	if err != nil || c.UsagePercent != 10 {
		t.Fatalf("c=%+v err=%v", c, err)
	}
}
```

- [ ] **Step 6: 运行测试，确认失败**

Run: `go test ./internal/status/`
Expected: FAIL，编译错误 `undefined: ParseWAN`、`undefined: ParseConntrack`

- [ ] **Step 7: 实现 WAN 和 conntrack**

`internal/status/wan.go`：

```go
package status

import (
	"context"
	"regexp"
	"strings"

	"github.com/rshun/merlin-mcp/internal/apperr"
	"github.com/rshun/merlin-mcp/internal/routercmd"
	"github.com/rshun/merlin-mcp/internal/runner"
	"github.com/rshun/merlin-mcp/internal/syslog"
)

// WAN 是 wan_status 的返回内容。
type WAN struct {
	State       string   `json:"state"`
	StateCode   string   `json:"state_code"`
	SubState    string   `json:"sub_state_code"`
	AuxState    string   `json:"aux_state_code"`
	Proto       string   `json:"proto"`
	IP          string   `json:"ip"`
	Gateway     string   `json:"gateway"`
	DNS         []string `json:"dns"`
	Events      []string `json:"recent_events"`
	EventsError string   `json:"events_error,omitempty"`
}

// 只读白名单内的键，不读取 PPPoE 密码等字段。
var wanKeys = []string{"wan0_state_t", "wan0_sbstate_t", "wan0_auxstate_t", "wan0_proto", "wan0_ipaddr", "wan0_gateway", "wan0_dns"}

var wanStates = map[string]string{
	"0": "initializing", "1": "connecting", "2": "connected", "3": "disconnected",
	"4": "stopped", "5": "disabled", "6": "stopping",
}

// ParseWAN 解析 nvram 输出。
func ParseWAN(out string) WAN {
	kv := routercmd.ParseKV(out)
	w := WAN{
		StateCode: kv["wan0_state_t"],
		SubState:  kv["wan0_sbstate_t"],
		AuxState:  kv["wan0_auxstate_t"],
		Proto:     kv["wan0_proto"],
		IP:        kv["wan0_ipaddr"],
		Gateway:   kv["wan0_gateway"],
		DNS:       strings.Fields(kv["wan0_dns"]),
	}
	if w.State = wanStates[w.StateCode]; w.State == "" {
		w.State = "unknown"
	}
	if w.DNS == nil {
		w.DNS = []string{}
	}
	return w
}

// WAN 相关日志的匹配模式。根据真实日志样本（Task 17）可能需要调整。
var wanEventRe = regexp.MustCompile(`(?i)(\bwan[0-9]*\b|wan_|wan connection|\bppp|udhcpc|dhcp client)`)

// WANEvents 返回最近 n 条 WAN 相关日志。
func WANEvents(lines []syslog.Line, n int) []string {
	ev := []string{}
	for _, l := range lines {
		if wanEventRe.MatchString(l.Raw) {
			ev = append(ev, l.Raw)
		}
	}
	if len(ev) > n {
		ev = ev[len(ev)-n:]
	}
	return ev
}

// FetchWAN 读取 WAN 状态和最近 20 条 WAN 事件。日志读取失败不影响状态返回。
func FetchWAN(ctx context.Context, r runner.Runner, syslogPath string) (WAN, error) {
	out, err := runner.Output(ctx, r, runner.Op("wan_status", routercmd.MustNvramScript(wanKeys...)))
	if err != nil {
		return WAN{}, err
	}
	w := ParseWAN(out)
	snap, err := syslog.Fetch(ctx, r, syslogPath, false)
	if err != nil {
		w.Events = []string{}
		w.EventsError = apperr.From(err).Message
		return w, nil
	}
	w.Events = WANEvents(snap.Lines, 20)
	return w, nil
}
```

注意：`TestWANEvents` 中 `kernel: swan is not wan` 会因为结尾的独立单词 `wan` 被匹配，这是预期行为，测试也这样断言。

`internal/status/conntrack.go`：

```go
package status

import (
	"context"
	"strconv"
	"strings"

	"github.com/rshun/merlin-mcp/internal/routercmd"
	"github.com/rshun/merlin-mcp/internal/runner"
)

// Conntrack 是 conntrack_status 的返回内容。
type Conntrack struct {
	Count        int     `json:"count"`
	Max          int     `json:"max"`
	UsagePercent float64 `json:"usage_percent"`
	Warning      string  `json:"warning,omitempty"`
}

func conntrackCmd() string {
	return runner.Op("conntrack", strings.Join([]string{
		routercmd.Marker("count"), "(cat /proc/sys/net/netfilter/nf_conntrack_count || cat /proc/sys/net/ipv4/netfilter/ip_conntrack_count) 2>/dev/null",
		routercmd.Marker("max"), "(cat /proc/sys/net/netfilter/nf_conntrack_max || cat /proc/sys/net/ipv4/netfilter/ip_conntrack_max) 2>/dev/null",
	}, "; "))
}

// FetchConntrack 读取连接跟踪表使用情况。
func FetchConntrack(ctx context.Context, r runner.Runner) (Conntrack, error) {
	out, err := runner.Output(ctx, r, conntrackCmd())
	if err != nil {
		return Conntrack{}, err
	}
	return ParseConntrack(out)
}

// ParseConntrack 解析 conntrackCmd 的输出，使用率 >= 80% 时附加提示。
func ParseConntrack(out string) (Conntrack, error) {
	sec := routercmd.Sections(out)
	count, err1 := strconv.Atoi(strings.TrimSpace(sec["count"]))
	limit, err2 := strconv.Atoi(strings.TrimSpace(sec["max"]))
	if err1 != nil || err2 != nil || limit <= 0 {
		return Conntrack{}, parseErr("conntrack")
	}
	c := Conntrack{Count: count, Max: limit, UsagePercent: round1(100 * float64(count) / float64(limit))}
	if c.UsagePercent >= 80 {
		c.Warning = "连接跟踪表使用率已超过 80%，接近上限时新连接会被丢弃，表现为部分网站打不开或时断时续"
	}
	return c, nil
}
```

- [ ] **Step 8: 全量检查**

Run: `go test ./... && go vet ./... && gofmt -l .`
Expected: PASS，`gofmt -l .` 无输出

- [ ] **Step 9: 提交**

按"提交步骤模板"执行，`<files>` 为 `internal/status`，提交说明：`feat: add system, WAN and conntrack status`

---

### Task 8: 客户端列表

**Files:**
- Create: `internal/clients/clients.go`、`internal/clients/clients_test.go`

**Interfaces:**
- Consumes: `runner`、`runnertest`（Task 1）；`shell.Quote`、`shell.ValidIfName`、`routercmd`（Task 2）
- Produces:
  - `clients.Lease{Expiry int64; MAC, IP, Hostname string}`、`clients.ARPEntry{IP, MAC, Device string}`、`clients.Static{IP, Hostname string}`
  - `clients.ParseLeases(s) []Lease`、`clients.ParseARP(s) []ARPEntry`、`clients.ParseStaticList(staticList, hostnames string) map[string]Static`、`clients.ParseAssoc(s) []string`
  - `clients.Inputs{Now time.Time; Leases []Lease; ARP []ARPEntry; Static map[string]Static; Wireless map[string]string}`
  - `clients.Client{MAC, IP, Hostname, Connection string; Static bool; LeaseRemainingSec *int64; LeaseInfinite, InARP bool}`
  - `clients.Merge(in Inputs) []Client`、`clients.Fetch(ctx, r) ([]Client, error)`（操作名 `clients_base`、`clients_assoc`）

- [ ] **Step 1: 写失败测试**

`internal/clients/clients_test.go`：

```go
package clients

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/rshun/merlin-mcp/internal/runner/runnertest"
)

func TestParseLeasesSkipsMalformed(t *testing.T) {
	s := "1791003600 aa:bb:cc:00:00:01 192.0.2.10 phone 01:aa:bb:cc:00:00:01\n" +
		"0 AA:BB:CC:00:00:02 192.0.2.11 * *\n" +
		"garbage line\n" +
		"notanumber AA:BB:CC:00:00:03 192.0.2.12 x\n" +
		"1791003600 not-a-mac 192.0.2.13 y\n"
	got := ParseLeases(s)
	if len(got) != 2 {
		t.Fatalf("len = %d: %+v", len(got), got)
	}
	if got[0].MAC != "AA:BB:CC:00:00:01" || got[0].Hostname != "phone" || got[0].Expiry != 1791003600 {
		t.Errorf("got[0] = %+v", got[0])
	}
	if got[1].Hostname != "" || got[1].Expiry != 0 {
		t.Errorf("主机名 * 应转为空: %+v", got[1])
	}
}

func TestParseARP(t *testing.T) {
	s := "IP address       HW type     Flags       HW address            Mask     Device\n" +
		"192.0.2.10       0x1         0x2         aa:bb:cc:00:00:01     *        br0\n" +
		"192.0.2.99       0x1         0x0         00:00:00:00:00:00     *        br0\n"
	got := ParseARP(s)
	if len(got) != 1 || got[0].MAC != "AA:BB:CC:00:00:01" || got[0].Device != "br0" {
		t.Fatalf("got = %+v", got)
	}
}

func TestParseStaticList(t *testing.T) {
	// 386 格式：<MAC>IP>>，主机名在 dhcp_hostnames；388 格式：<MAC>IP>DNS>主机名
	m := ParseStaticList("<AA:BB:CC:00:00:01>192.0.2.10>><aa:bb:cc:00:00:02>192.0.2.11>>nas", "<AA:BB:CC:00:00:01>phone<AA:BB:CC:00:00:09>ghost")
	if m["AA:BB:CC:00:00:01"] != (Static{IP: "192.0.2.10", Hostname: "phone"}) {
		t.Errorf("01 = %+v", m["AA:BB:CC:00:00:01"])
	}
	if m["AA:BB:CC:00:00:02"] != (Static{IP: "192.0.2.11", Hostname: "nas"}) {
		t.Errorf("02 = %+v", m["AA:BB:CC:00:00:02"])
	}
	if _, ok := m["AA:BB:CC:00:00:09"]; ok {
		t.Error("只在 dhcp_hostnames 中出现的 MAC 不算静态分配")
	}
}

func TestParseAssoc(t *testing.T) {
	got := ParseAssoc("assoclist AA:BB:CC:00:00:01\nassoclist aa:bb:cc:00:00:02\nwl: error\n")
	if len(got) != 2 || got[1] != "AA:BB:CC:00:00:02" {
		t.Fatalf("got = %v", got)
	}
}

func TestMerge(t *testing.T) {
	now := time.Unix(1791000000, 0)
	in := Inputs{
		Now: now,
		Leases: []Lease{
			{Expiry: 1791000600, MAC: "AA:BB:CC:00:00:01", IP: "192.0.2.10", Hostname: "phone"},
			{Expiry: 0, MAC: "AA:BB:CC:00:00:03", IP: "192.0.2.30", Hostname: "printer"},
		},
		ARP: []ARPEntry{
			{IP: "192.0.2.10", MAC: "AA:BB:CC:00:00:01", Device: "br0"},
			{IP: "192.0.2.20", MAC: "AA:BB:CC:00:00:02", Device: "br0"},
		},
		Static:   map[string]Static{"AA:BB:CC:00:00:04": {IP: "192.0.2.5", Hostname: "offline-nas"}, "AA:BB:CC:00:00:02": {IP: "192.0.2.20"}},
		Wireless: map[string]string{"AA:BB:CC:00:00:01": "5G"},
	}
	got := Merge(in)
	if len(got) != 4 {
		t.Fatalf("len = %d", len(got))
	}
	// 按 IP 排序：.5 .10 .20 .30
	order := []string{"AA:BB:CC:00:00:04", "AA:BB:CC:00:00:01", "AA:BB:CC:00:00:02", "AA:BB:CC:00:00:03"}
	for i, mac := range order {
		if got[i].MAC != mac {
			t.Fatalf("第 %d 个应为 %s，得到 %s", i, mac, got[i].MAC)
		}
	}
	phone := got[1]
	if phone.Connection != "5G" || phone.Hostname != "phone" || phone.LeaseRemainingSec == nil || *phone.LeaseRemainingSec != 600 || !phone.InARP {
		t.Errorf("phone = %+v", phone)
	}
	if got[2].Connection != "wired" || !got[2].Static {
		t.Errorf("有线静态设备 = %+v", got[2])
	}
	if got[3].Connection != "unknown" || !got[3].LeaseInfinite || got[3].LeaseRemainingSec != nil {
		t.Errorf("无限租约设备 = %+v", got[3])
	}
	if got[0].Connection != "unknown" || got[0].Hostname != "offline-nas" || !got[0].Static {
		t.Errorf("离线静态设备 = %+v", got[0])
	}
}

const baseOutput = "@@MERLINMCP:date@@\n1791000000 +0800\n" +
	"@@MERLINMCP:nvram@@\ndhcp_staticlist=\ndhcp_hostnames=\nwl0_ifname=eth6\nwl0_nband=2\nwl1_ifname=eth7\nwl1_nband=1\nwl2_ifname=\nwl2_nband=\nwl3_ifname=bad;name\nwl3_nband=4\n" +
	"@@MERLINMCP:leases@@\n1791000600 AA:BB:CC:00:00:01 192.0.2.10 phone *\n" +
	"@@MERLINMCP:arp@@\nIP address HW type Flags HW address Mask Device\n192.0.2.10 0x1 0x2 AA:BB:CC:00:00:01 * br0\n192.0.2.20 0x1 0x2 AA:BB:CC:00:00:02 * br0\n"

func TestFetchMergesWirelessBands(t *testing.T) {
	f := runnertest.New().
		On("clients_base", runnertest.Stdout(baseOutput)).
		On("clients_assoc", runnertest.Stdout("@@MERLINMCP:eth6@@\n@@MERLINMCP:eth7@@\nassoclist AA:BB:CC:00:00:01\n"))
	got, err := Fetch(context.Background(), f)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Connection != "5G" || got[1].Connection != "wired" {
		t.Fatalf("got = %+v", got)
	}
	cmd := f.CallsFor("clients_assoc")[0].Cmd
	if !strings.Contains(cmd, "wl -i 'eth6' assoclist") || strings.Contains(cmd, "bad;name") {
		t.Fatalf("非法接口名不应进入命令: %s", cmd)
	}
}

func TestFetchDegradesWhenWlFails(t *testing.T) {
	f := runnertest.New().
		On("clients_base", runnertest.Stdout(baseOutput)).
		On("clients_assoc", runnertest.Exit(127, "", "wl: not found"))
	got, err := Fetch(context.Background(), f)
	if err != nil {
		t.Fatal(err)
	}
	if got[0].Connection != "wired" || got[1].Connection != "wired" {
		t.Fatalf("wl 失败时在 ARP 中的设备应为 wired: %+v", got)
	}
}
```

- [ ] **Step 2: 运行测试，确认失败**

Run: `go test ./internal/clients/`
Expected: FAIL，编译错误

- [ ] **Step 3: 实现 clients**

`internal/clients/clients.go`：

```go
// Package clients 合并 DHCP 租约、ARP 表、静态 IP 分配和无线关联列表，生成客户端列表。
package clients

import (
	"context"
	"fmt"
	"net/netip"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/rshun/merlin-mcp/internal/routercmd"
	"github.com/rshun/merlin-mcp/internal/runner"
	"github.com/rshun/merlin-mcp/internal/shell"
)

// Lease 是 dnsmasq.leases 的一行。Expiry 为 0 表示永不过期。
type Lease struct {
	Expiry   int64
	MAC      string
	IP       string
	Hostname string
}

// ARPEntry 是 /proc/net/arp 的一行。
type ARPEntry struct {
	IP     string
	MAC    string
	Device string
}

// Static 是一条静态 IP 分配。
type Static struct {
	IP       string
	Hostname string
}

// Inputs 是 Merge 的全部输入。MAC 一律为大写。
type Inputs struct {
	Now      time.Time
	Leases   []Lease
	ARP      []ARPEntry
	Static   map[string]Static
	Wireless map[string]string // MAC → 频段（2.4G/5G/6G/unknown）
}

// Client 是 clients_list 返回的一台设备。
type Client struct {
	MAC               string `json:"mac"`
	IP                string `json:"ip,omitempty"`
	Hostname          string `json:"hostname,omitempty"`
	Connection        string `json:"connection"` // wired / 2.4G / 5G / 6G / unknown
	Static            bool   `json:"static"`
	LeaseRemainingSec *int64 `json:"lease_remaining_sec,omitempty"`
	LeaseInfinite     bool   `json:"lease_infinite,omitempty"`
	InARP             bool   `json:"in_arp"`
}

var macRe = regexp.MustCompile(`^[0-9A-F]{2}(:[0-9A-F]{2}){5}$`)

func normMAC(s string) string { return strings.ToUpper(strings.TrimSpace(s)) }

// ParseLeases 解析 dnsmasq.leases（格式：过期时间 MAC IP 主机名 客户端ID），跳过格式不对的行。
func ParseLeases(s string) []Lease {
	var out []Lease
	for _, line := range strings.Split(s, "\n") {
		f := strings.Fields(line)
		if len(f) < 4 {
			continue
		}
		exp, err := strconv.ParseInt(f[0], 10, 64)
		if err != nil {
			continue
		}
		mac := normMAC(f[1])
		if !macRe.MatchString(mac) {
			continue
		}
		host := f[3]
		if host == "*" {
			host = ""
		}
		out = append(out, Lease{Expiry: exp, MAC: mac, IP: f[2], Hostname: host})
	}
	return out
}

// ParseARP 解析 /proc/net/arp，跳过表头和未完成解析（flags 0x0）的条目。
func ParseARP(s string) []ARPEntry {
	var out []ARPEntry
	for _, line := range strings.Split(s, "\n") {
		f := strings.Fields(line)
		if len(f) < 6 || f[0] == "IP" {
			continue
		}
		mac := normMAC(f[3])
		if !macRe.MatchString(mac) || mac == "00:00:00:00:00:00" || f[2] == "0x0" {
			continue
		}
		out = append(out, ARPEntry{IP: f[0], MAC: mac, Device: f[5]})
	}
	return out
}

// ParseStaticList 解析 nvram dhcp_staticlist（<MAC>IP>DNS>主机名，不同固件版本字段数量不同）
// 和 dhcp_hostnames（<MAC>主机名，只用来给已有的静态分配补充主机名）。
func ParseStaticList(staticList, hostnames string) map[string]Static {
	m := map[string]Static{}
	for _, e := range strings.Split(staticList, "<") {
		parts := strings.Split(e, ">")
		mac := normMAC(parts[0])
		if len(parts) < 2 || !macRe.MatchString(mac) {
			continue
		}
		st := Static{IP: strings.TrimSpace(parts[1])}
		if len(parts) >= 4 {
			st.Hostname = strings.TrimSpace(parts[3])
		}
		m[mac] = st
	}
	for _, e := range strings.Split(hostnames, "<") {
		mac, name, ok := strings.Cut(e, ">")
		mac = normMAC(mac)
		st, exists := m[mac]
		if !ok || !exists || name == "" || st.Hostname != "" {
			continue
		}
		st.Hostname = strings.TrimSpace(name)
		m[mac] = st
	}
	return m
}

// ParseAssoc 解析 `wl -i <if> assoclist` 的输出（每行 "assoclist <MAC>"）。
func ParseAssoc(s string) []string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		f := strings.Fields(line)
		if len(f) == 0 {
			continue
		}
		if mac := normMAC(f[len(f)-1]); macRe.MatchString(mac) {
			out = append(out, mac)
		}
	}
	return out
}

func bandOf(nband string) string {
	switch strings.TrimSpace(nband) {
	case "2":
		return "2.4G"
	case "1":
		return "5G"
	case "4":
		return "6G"
	}
	return "unknown"
}

// Merge 以 MAC 为主键合并数据。
// IP 优先级：ARP > 租约 > 静态分配；主机名优先级：租约 > 静态分配。
func Merge(in Inputs) []Client {
	byMAC := map[string]*Client{}
	get := func(mac string) *Client {
		c, ok := byMAC[mac]
		if !ok {
			c = &Client{MAC: mac}
			byMAC[mac] = c
		}
		return c
	}
	for mac, st := range in.Static {
		c := get(mac)
		c.Static, c.IP, c.Hostname = true, st.IP, st.Hostname
	}
	for _, l := range in.Leases {
		c := get(l.MAC)
		c.IP = l.IP
		if l.Hostname != "" {
			c.Hostname = l.Hostname
		}
		if l.Expiry == 0 {
			c.LeaseInfinite = true
		} else {
			rem := l.Expiry - in.Now.Unix()
			if rem < 0 {
				rem = 0
			}
			c.LeaseRemainingSec = &rem
		}
	}
	for _, a := range in.ARP {
		c := get(a.MAC)
		c.IP, c.InARP = a.IP, true
	}
	for mac := range in.Wireless {
		get(mac)
	}

	out := make([]Client, 0, len(byMAC))
	for _, c := range byMAC {
		switch {
		case in.Wireless[c.MAC] != "":
			c.Connection = in.Wireless[c.MAC]
		case c.InARP:
			c.Connection = "wired"
		default:
			c.Connection = "unknown"
		}
		out = append(out, *c)
	}
	sort.Slice(out, func(i, j int) bool { return less(out[i], out[j]) })
	return out
}

// less 按 IP 排序，没有 IP 的排在最后，IP 相同时按 MAC。
func less(a, b Client) bool {
	ai, aerr := netip.ParseAddr(a.IP)
	bi, berr := netip.ParseAddr(b.IP)
	switch {
	case aerr == nil && berr == nil:
		if c := ai.Compare(bi); c != 0 {
			return c < 0
		}
	case aerr == nil:
		return true
	case berr == nil:
		return false
	}
	return a.MAC < b.MAC
}

var baseKeys = []string{
	"dhcp_staticlist", "dhcp_hostnames",
	"wl0_ifname", "wl0_nband", "wl1_ifname", "wl1_nband",
	"wl2_ifname", "wl2_nband", "wl3_ifname", "wl3_nband",
}

// Fetch 两次 SSH 调用：先取租约、ARP、nvram，再按无线接口查询关联列表。
// wl 命令失败时不报错，无线设备的连接方式降级为 wired/unknown。
func Fetch(ctx context.Context, r runner.Runner) ([]Client, error) {
	base := runner.Op("clients_base", strings.Join([]string{
		routercmd.Marker("date"), routercmd.DateCmd,
		routercmd.Marker("nvram"), routercmd.MustNvramScript(baseKeys...),
		routercmd.Marker("leases"), "cat /var/lib/misc/dnsmasq.leases 2>/dev/null",
		routercmd.Marker("arp"), "cat /proc/net/arp",
	}, "; "))
	out, err := runner.Output(ctx, r, base)
	if err != nil {
		return nil, err
	}
	sec := routercmd.Sections(out)
	now, err := routercmd.ParseDate(sec["date"])
	if err != nil {
		now = time.Now()
	}
	kv := routercmd.ParseKV(sec["nvram"])
	in := Inputs{
		Now:      now,
		Leases:   ParseLeases(sec["leases"]),
		ARP:      ParseARP(sec["arp"]),
		Static:   ParseStaticList(kv["dhcp_staticlist"], kv["dhcp_hostnames"]),
		Wireless: map[string]string{},
	}

	bands := map[string]string{}
	var parts []string
	for i := 0; i < 4; i++ {
		ifname := strings.TrimSpace(kv[fmt.Sprintf("wl%d_ifname", i)])
		if ifname == "" || !shell.ValidIfName(ifname) {
			continue
		}
		bands[ifname] = bandOf(kv[fmt.Sprintf("wl%d_nband", i)])
		parts = append(parts, routercmd.Marker(ifname), "wl -i "+shell.Quote(ifname)+" assoclist 2>/dev/null")
	}
	if len(parts) > 0 {
		res, err := r.Run(ctx, runner.Op("clients_assoc", strings.Join(parts, "; ")), nil)
		if err != nil {
			return nil, err
		}
		asec := routercmd.Sections(string(res.Stdout))
		for ifname, band := range bands {
			for _, mac := range ParseAssoc(asec[ifname]) {
				in.Wireless[mac] = band
			}
		}
	}
	return Merge(in), nil
}
```

- [ ] **Step 4: 运行测试，确认通过**

Run: `go test ./internal/clients/`
Expected: PASS

- [ ] **Step 5: 全量检查**

Run: `go test ./... && go vet ./... && gofmt -l .`
Expected: PASS，`gofmt -l .` 无输出

- [ ] **Step 6: 提交**

按"提交步骤模板"执行，`<files>` 为 `internal/clients`，提交说明：`feat: add client list merging leases, ARP, static and wireless data`

---

### Task 9: 网络诊断（ping / nslookup）

**Files:**
- Create: `internal/diagnose/diagnose.go`、`internal/diagnose/diagnose_test.go`

**Interfaces:**
- Consumes: `runner`、`runnertest`、`apperr`（Task 1）；`shell`（Task 2）
- Produces:
  - `diagnose.Request{Action, Target string; Count int; Server string}`
  - `diagnose.Result{Action, Target, Raw string; ExitCode int; PacketLossPercent, AvgRTTMs *float64; Resolved []string}`
  - `diagnose.Run(ctx, r, req Request) (Result, error)`（操作名 `ping`、`nslookup`）
  - `diagnose.ParsePing(out string) (loss, avg *float64)`、`diagnose.ParseNslookup(out string) []string`（Task 12 和 Task 15 也会用到）

- [ ] **Step 1: 写失败测试**

`internal/diagnose/diagnose_test.go`：

```go
package diagnose

import (
	"context"
	"strings"
	"testing"

	"github.com/rshun/merlin-mcp/internal/apperr"
	"github.com/rshun/merlin-mcp/internal/runner/runnertest"
)

const pingOut = `PING example.com (192.0.2.80): 56 data bytes
64 bytes from 192.0.2.80: seq=0 ttl=55 time=12.345 ms

--- example.com ping statistics ---
3 packets transmitted, 2 packets received, 33% packet loss
round-trip min/avg/max = 10.000/12.500/15.000 ms`

func TestRunRejectsInvalidInput(t *testing.T) {
	f := runnertest.New()
	cases := []Request{
		{Action: "ping", Target: "-c 100 example.com"},
		{Action: "ping", Target: "$(reboot)"},
		{Action: "ping", Target: "example.com", Count: 6},
		{Action: "nslookup", Target: "example.com", Server: "dns.example"},
		{Action: "traceroute", Target: "example.com"},
	}
	for _, c := range cases {
		_, err := Run(context.Background(), f, c)
		if apperr.CodeOf(err) != apperr.InvalidArgument {
			t.Errorf("%+v 应返回 INVALID_ARGUMENT，得到 %v", c, err)
		}
	}
	if len(f.Calls()) != 0 {
		t.Fatal("参数非法时不应执行任何命令")
	}
}

func TestRunPing(t *testing.T) {
	f := runnertest.New().On("ping", runnertest.Stdout(pingOut))
	res, err := Run(context.Background(), f, Request{Action: "ping", Target: "example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if *res.PacketLossPercent != 33 || *res.AvgRTTMs != 12.5 {
		t.Fatalf("res = %+v", res)
	}
	if cmd := f.CallsFor("ping")[0].Cmd; !strings.Contains(cmd, "ping -c 3 -W 2 'example.com'") {
		t.Fatalf("cmd = %s", cmd)
	}
}

func TestRunPingTotalLossIsNotError(t *testing.T) {
	f := runnertest.New().On("ping", runnertest.Exit(1, "3 packets transmitted, 0 packets received, 100% packet loss\n", ""))
	res, err := Run(context.Background(), f, Request{Action: "ping", Target: "192.0.2.1", Count: 3})
	if err != nil || res.ExitCode != 1 || *res.PacketLossPercent != 100 || res.AvgRTTMs != nil {
		t.Fatalf("res=%+v err=%v", res, err)
	}
}

func TestRunNslookupDefaultsToLocalServer(t *testing.T) {
	out := "Server:    127.0.0.1\nAddress 1: 127.0.0.1 localhost\n\nName:      example.com\nAddress 1: 192.0.2.80\nAddress 2: 2001:db8::80\n"
	f := runnertest.New().On("nslookup", runnertest.Stdout(out))
	res, err := Run(context.Background(), f, Request{Action: "nslookup", Target: "example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Resolved) != 2 || res.Resolved[1] != "2001:db8::80" {
		t.Fatalf("resolved = %v", res.Resolved)
	}
	if cmd := f.CallsFor("nslookup")[0].Cmd; !strings.Contains(cmd, "nslookup 'example.com' '127.0.0.1'") {
		t.Fatalf("cmd = %s", cmd)
	}
}

func TestParseNslookupNewBusyboxFormat(t *testing.T) {
	out := "Server:\t\t127.0.0.1\nAddress:\t127.0.0.1:53\n\nName:\trouter.asus.com\nAddress: 192.0.2.1\n"
	if got := ParseNslookup(out); len(got) != 1 || got[0] != "192.0.2.1" {
		t.Fatalf("got = %v", got)
	}
	if got := ParseNslookup("** server can't find nope.example: NXDOMAIN\n"); len(got) != 0 {
		t.Fatalf("解析失败时应为空: %v", got)
	}
}
```

- [ ] **Step 2: 运行测试，确认失败**

Run: `go test ./internal/diagnose/`
Expected: FAIL，编译错误

- [ ] **Step 3: 实现 diagnose**

`internal/diagnose/diagnose.go`：

```go
// Package diagnose 实现从路由器发起的 ping 和 nslookup。
package diagnose

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/rshun/merlin-mcp/internal/apperr"
	"github.com/rshun/merlin-mcp/internal/runner"
	"github.com/rshun/merlin-mcp/internal/shell"
)

// Request 是 network_diagnose 的参数。
type Request struct {
	Action string // ping 或 nslookup
	Target string
	Count  int    // ping 次数，默认 3，范围 1-5
	Server string // nslookup 使用的 DNS 服务器，默认 127.0.0.1
}

// Result 是诊断结果。ping 全部丢包、nslookup 解析失败都不算错误，体现在字段里。
type Result struct {
	Action            string   `json:"action"`
	Target            string   `json:"target"`
	Raw               string   `json:"raw"`
	ExitCode          int      `json:"exit_code"`
	PacketLossPercent *float64 `json:"packet_loss_percent,omitempty"`
	AvgRTTMs          *float64 `json:"avg_rtt_ms,omitempty"`
	Resolved          []string `json:"resolved,omitempty"`
}

func invalid(msg string) error { return apperr.New(apperr.InvalidArgument, msg, "") }

// Run 校验参数后在路由器上执行诊断命令。
func Run(ctx context.Context, r runner.Runner, req Request) (Result, error) {
	if !shell.ValidHost(req.Target) {
		return Result{}, invalid("target 必须是合法的域名或 IP 地址")
	}
	var cmd string
	switch req.Action {
	case "ping":
		if req.Count == 0 {
			req.Count = 3
		}
		if req.Count < 1 || req.Count > 5 {
			return Result{}, invalid("count 必须在 1-5 之间")
		}
		cmd = runner.Op("ping", fmt.Sprintf("ping -c %d -W 2 %s 2>&1", req.Count, shell.Quote(req.Target)))
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
	case "nslookup":
		if req.Server == "" {
			req.Server = "127.0.0.1"
		}
		if !shell.ValidIP(req.Server) {
			return Result{}, invalid("server 必须是 IP 地址")
		}
		cmd = runner.Op("nslookup", fmt.Sprintf("nslookup %s %s 2>&1", shell.Quote(req.Target), shell.Quote(req.Server)))
	default:
		return Result{}, invalid("action 只能是 ping 或 nslookup")
	}

	res, err := r.Run(ctx, cmd, nil)
	if err != nil {
		return Result{}, err
	}
	out := Result{Action: req.Action, Target: req.Target, Raw: strings.TrimSpace(string(res.Stdout)), ExitCode: res.ExitCode}
	if req.Action == "ping" {
		out.PacketLossPercent, out.AvgRTTMs = ParsePing(out.Raw)
	} else {
		out.Resolved = ParseNslookup(out.Raw)
	}
	return out, nil
}

var (
	lossRe = regexp.MustCompile(`([\d.]+)% packet loss`)
	rttRe  = regexp.MustCompile(`= [\d.]+/([\d.]+)/[\d.]+`)
)

// ParsePing 从 busybox ping 输出中取丢包率和平均延迟。
func ParsePing(out string) (loss, avg *float64) {
	if m := lossRe.FindStringSubmatch(out); m != nil {
		if v, err := strconv.ParseFloat(m[1], 64); err == nil {
			loss = &v
		}
	}
	if m := rttRe.FindStringSubmatch(out); m != nil {
		if v, err := strconv.ParseFloat(m[1], 64); err == nil {
			avg = &v
		}
	}
	return loss, avg
}

// ParseNslookup 取出 "Name:" 之后各 "Address" 行中的 IP，兼容新旧两种 busybox 输出格式。
func ParseNslookup(out string) []string {
	var ips []string
	afterName := false
	for _, line := range strings.Split(out, "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "Name:") {
			afterName = true
			continue
		}
		if !afterName || !strings.HasPrefix(t, "Address") {
			continue
		}
		_, rest, ok := strings.Cut(t, ":")
		if !ok {
			continue
		}
		if f := strings.Fields(rest); len(f) > 0 && shell.ValidIP(f[0]) {
			ips = append(ips, f[0])
		}
	}
	return ips
}
```

- [ ] **Step 4: 运行测试，确认通过**

Run: `go test ./internal/diagnose/`
Expected: PASS

- [ ] **Step 5: 全量检查**

Run: `go test ./... && go vet ./... && gofmt -l .`
Expected: PASS，`gofmt -l .` 无输出

- [ ] **Step 6: 提交**

按"提交步骤模板"执行，`<files>` 为 `internal/diagnose`，提交说明：`feat: add ping and nslookup diagnostics`

---

### Task 10: dnsmasq 按行增删与 diff（纯函数）

**Files:**
- Create: `internal/dnsmasq/lines.go`、`internal/dnsmasq/lines_test.go`

**Interfaces:**
- Consumes: `apperr`（Task 1）
- Produces:
  - `dnsmasq.NormalizeInput(lines []string) ([]string, error)`
  - `dnsmasq.Add(content string, add []string) (next string, added, skipped []string)`
  - `dnsmasq.Remove(content string, rm []string) (next string, removed, notFound []string)`
  - `dnsmasq.Diff(oldContent, newContent string) string`（每行以 `" "`、`"-"`、`"+"` 开头）
  - `dnsmasq.SHA256(content string) string`
  - 包内函数 `splitContent(content string) []string`、`join(lines []string) string`（Task 11 使用）

- [ ] **Step 1: 写失败测试**

`internal/dnsmasq/lines_test.go`：

```go
package dnsmasq

import (
	"strings"
	"testing"

	"github.com/rshun/merlin-mcp/internal/apperr"
)

func TestNormalizeInput(t *testing.T) {
	got, err := NormalizeInput([]string{"address=/a.example/0.0.0.0  ", "address=/a.example/0.0.0.0", "# 注释"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != "address=/a.example/0.0.0.0" || got[1] != "# 注释" {
		t.Fatalf("got = %q", got)
	}
	bad := [][]string{
		nil,
		{"a\nb"},
		{"a\rb"},
		{"a\x00b"},
		{"   "},
		make([]string, 101),
	}
	for _, in := range bad {
		if _, err := NormalizeInput(in); apperr.CodeOf(err) != apperr.InvalidArgument {
			t.Errorf("NormalizeInput(%q) 应返回 INVALID_ARGUMENT，得到 %v", in, err)
		}
	}
}

func TestAddAppendsAndSkips(t *testing.T) {
	next, added, skipped := Add("dhcp-mac=set:openwrt,AA:BB:CC:00:00:01\n", []string{"dhcp-mac=set:openwrt,AA:BB:CC:00:00:01", "address=/a.example/0.0.0.0"})
	if next != "dhcp-mac=set:openwrt,AA:BB:CC:00:00:01\naddress=/a.example/0.0.0.0\n" {
		t.Fatalf("next = %q", next)
	}
	if len(added) != 1 || len(skipped) != 1 {
		t.Fatalf("added=%v skipped=%v", added, skipped)
	}
	if next, _, _ := Add("", []string{"x=1"}); next != "x=1\n" {
		t.Fatalf("空文件添加 = %q", next)
	}
}

func TestRemove(t *testing.T) {
	next, removed, notFound := Remove("a=1\nb=2\na=1\nc=3\n", []string{"a=1", "z=9"})
	if next != "b=2\nc=3\n" {
		t.Fatalf("应删除所有匹配的行，next = %q", next)
	}
	if len(removed) != 1 || removed[0] != "a=1" || len(notFound) != 1 || notFound[0] != "z=9" {
		t.Fatalf("removed=%v notFound=%v", removed, notFound)
	}
	if next, _, _ := Remove("a=1\n", []string{"a=1"}); next != "" {
		t.Fatalf("删光后应为空，得到 %q", next)
	}
}

func TestRemoveMatchesCRLFFile(t *testing.T) {
	next, removed, _ := Remove("a=1\r\nb=2  \r\n\r\n", []string{"b=2"})
	if next != "a=1\n" || len(removed) != 1 {
		t.Fatalf("next=%q removed=%v", next, removed)
	}
}

func TestBlankLinesInMiddleArePreserved(t *testing.T) {
	next, _, _ := Add("a=1\n\nb=2\n\n\n", []string{"c=3"})
	if next != "a=1\n\nb=2\nc=3\n" {
		t.Fatalf("next = %q", next)
	}
}

func TestDiff(t *testing.T) {
	d := Diff("a\nb\nc\n", "a\nc\nd\n")
	want := " a\n-b\n c\n+d\n"
	if d != want {
		t.Fatalf("diff =\n%s\nwant\n%s", d, want)
	}
	if Diff("a\r\n", "a\n") != " a\n" {
		t.Fatal("只有换行符不同时不应产生差异")
	}
}

func TestSHA256(t *testing.T) {
	if got := SHA256(""); got != "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855" {
		t.Fatalf("got = %s", got)
	}
	if strings.ToLower(SHA256("x")) != SHA256("x") {
		t.Fatal("应为小写十六进制")
	}
}
```

- [ ] **Step 2: 运行测试，确认失败**

Run: `go test ./internal/dnsmasq/`
Expected: FAIL，编译错误

- [ ] **Step 3: 实现**

`internal/dnsmasq/lines.go`：

```go
// Package dnsmasq 管理 Merlin 的 dnsmasq.conf.add：按行增删、备份、生效与回滚。
package dnsmasq

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/rshun/merlin-mcp/internal/apperr"
)

// NormalizeInput 校验并规范化要添加/删除的行：去掉行尾空白，拒绝空行和含换行/NUL 的行，
// 输入中重复的行只保留一个。
func NormalizeInput(lines []string) ([]string, error) {
	if len(lines) == 0 || len(lines) > 100 {
		return nil, apperr.New(apperr.InvalidArgument, "lines 必须包含 1-100 行", "")
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(lines))
	for i, l := range lines {
		if strings.ContainsAny(l, "\n\r\x00") {
			return nil, apperr.New(apperr.InvalidArgument, fmt.Sprintf("第 %d 行包含换行符或 NUL 字符，每个元素只能是一行配置", i+1), "")
		}
		n := strings.TrimRight(l, " \t")
		if strings.TrimSpace(n) == "" {
			return nil, apperr.New(apperr.InvalidArgument, fmt.Sprintf("第 %d 行为空", i+1), "")
		}
		if !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	return out, nil
}

// splitContent 把文件内容拆成行：统一 CRLF，去掉行尾空白，去掉末尾的空行（中间的空行保留）。
func splitContent(content string) []string {
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, " \t\r")
	}
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// join 把行拼回文件内容，非空时以换行结尾。
func join(lines []string) string {
	if len(lines) == 0 {
		return ""
	}
	return strings.Join(lines, "\n") + "\n"
}

// Add 追加文件中还没有的行。
func Add(content string, add []string) (next string, added, skipped []string) {
	cur := splitContent(content)
	have := map[string]bool{}
	for _, l := range cur {
		have[l] = true
	}
	for _, a := range add {
		if have[a] {
			skipped = append(skipped, a)
			continue
		}
		have[a] = true
		cur = append(cur, a)
		added = append(added, a)
	}
	return join(cur), added, skipped
}

// Remove 删除所有与 rm 中某一项完全相同的行。
func Remove(content string, rm []string) (next string, removed, notFound []string) {
	want := map[string]bool{}
	for _, r := range rm {
		want[r] = true
	}
	found := map[string]bool{}
	var kept []string
	for _, l := range splitContent(content) {
		if want[l] {
			found[l] = true
			continue
		}
		kept = append(kept, l)
	}
	for _, r := range rm {
		if found[r] {
			removed = append(removed, r)
		} else {
			notFound = append(notFound, r)
		}
	}
	return join(kept), removed, notFound
}

// SHA256 返回内容的小写十六进制 sha256。
func SHA256(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}

// Diff 基于最长公共子序列生成逐行差异，行首为 " "（不变）、"-"（删除）、"+"（新增）。
func Diff(oldContent, newContent string) string {
	a, b := splitContent(oldContent), splitContent(newContent)
	n, m := len(a), len(b)
	dp := make([][]int, n+1)
	for i := range dp {
		dp[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[i] == b[j] {
				dp[i][j] = dp[i+1][j+1] + 1
			} else {
				dp[i][j] = max(dp[i+1][j], dp[i][j+1])
			}
		}
	}
	var sb strings.Builder
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case a[i] == b[j]:
			sb.WriteString(" " + a[i] + "\n")
			i++
			j++
		case dp[i+1][j] >= dp[i][j+1]:
			sb.WriteString("-" + a[i] + "\n")
			i++
		default:
			sb.WriteString("+" + b[j] + "\n")
			j++
		}
	}
	for ; i < n; i++ {
		sb.WriteString("-" + a[i] + "\n")
	}
	for ; j < m; j++ {
		sb.WriteString("+" + b[j] + "\n")
	}
	return sb.String()
}
```

- [ ] **Step 4: 运行测试，确认通过**

Run: `go test ./internal/dnsmasq/`
Expected: PASS

- [ ] **Step 5: 全量检查**

Run: `go test ./... && go vet ./... && gofmt -l .`
Expected: PASS，`gofmt -l .` 无输出

- [ ] **Step 6: 提交**

按"提交步骤模板"执行，`<files>` 为 `internal/dnsmasq`，提交说明：`feat: add dnsmasq line editing and diff`

---

### Task 11: dnsmasq 文件管理（读取、修改、备份、清理）

**Files:**
- Create: `internal/dnsmasq/manager.go`、`internal/dnsmasq/manager_test.go`
- Create: `internal/dnsmasq/fakerouter_test.go`（Task 11 和 Task 12 共用的模拟路由器）

**Interfaces:**
- Consumes: Task 10 的全部函数；`runner`、`runnertest`、`apperr`（Task 1）；`shell`（Task 2）；`state.Store`（Task 4）
- Produces:
  - `dnsmasq.Options{AddPath, BackupDir, HealthDomain string; Keep int; Store *state.Store; Sleep func(time.Duration); PIDWait time.Duration}`
  - `dnsmasq.New(r runner.Runner, o Options) *Manager`
  - `dnsmasq.NumberedLine{N int; Text string}`、`dnsmasq.FileView{Path string; Exists bool; SHA256 string; Lines []NumberedLine}`
  - `(*Manager).Read(ctx) (FileView, error)`（操作名 `addfile_read`）
  - `dnsmasq.EditOp`（`OpAdd`、`OpRemove`）、`dnsmasq.EditResult{DryRun, Changed bool; Added, Skipped, Removed, NotFound []string; Diff, SHA256, Backup string; ExternalChangeDetected, PendingApply bool; Warning string}`
  - `(*Manager).Edit(ctx, op EditOp, lines []string, dryRun bool) (EditResult, error)`
  - `(*Manager).Effective(ctx, keyword string) ([]NumberedLine, error)`（操作名 `dnsmasq_effective`）
  - `dnsmasq.PruneList(names []string, keep int, protect string) []string`
  - 包内方法供 Task 12 使用：`m.read`、`m.write`（`addfile_write`）、`m.backup`（`addfile_backup`）、`m.readBackup`（`backup_read`）、`m.prune`（`backup_list`、`backup_prune`）

- [ ] **Step 1: 写模拟路由器（测试辅助代码）**

`internal/dnsmasq/fakerouter_test.go`：

```go
package dnsmasq

import (
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/rshun/merlin-mcp/internal/runner"
	"github.com/rshun/merlin-mcp/internal/runner/runnertest"
	"github.com/rshun/merlin-mcp/internal/state"
)

// fakeRouter 在内存中模拟路由器上与 dnsmasq 相关的文件和进程。
// 内容包含 "bad-option" 时，重启后 dnsmasq 进程不会运行。
type fakeRouter struct {
	*runnertest.Fake
	exists         bool
	content        string
	backups        map[string]string
	seq            int
	pid            int
	running        bool
	testExit       int
	testOutput     string
	healthOK       bool
	restartIgnored bool // service 命令返回成功，但 dnsmasq 实际没有重启
	effective      string
}

var nameInCmd = regexp.MustCompile(`dnsmasq\.conf\.add\.\d{8}-\d{6}(-\d+)?`)

func out(s string) runner.Result { return runner.Result{Stdout: []byte(s)} }

func newFakeRouter() *fakeRouter {
	f := &fakeRouter{Fake: runnertest.New(), backups: map[string]string{}, pid: 100, running: true, healthOK: true}
	f.On("addfile_read", func(string, []byte) (runner.Result, error) {
		if !f.exists {
			return out("MISSING\n"), nil
		}
		return out("EXISTS\n" + f.content), nil
	})
	f.On("addfile_write", func(_ string, stdin []byte) (runner.Result, error) {
		f.exists, f.content = true, string(stdin)
		return out(""), nil
	})
	f.On("addfile_backup", func(_ string, stdin []byte) (runner.Result, error) {
		f.seq++
		name := fmt.Sprintf("dnsmasq.conf.add.20261003-1200%02d", f.seq)
		f.backups[name] = string(stdin)
		return out("/jffs/merlin-mcp/backups/" + name + "\n"), nil
	})
	f.On("backup_read", func(cmd string, _ []byte) (runner.Result, error) {
		c, ok := f.backups[nameInCmd.FindString(cmd)]
		if !ok {
			return runner.Result{ExitCode: 1, Stderr: []byte("No such file")}, nil
		}
		return out(c), nil
	})
	f.On("backup_list", func(string, []byte) (runner.Result, error) {
		var names []string
		for n := range f.backups {
			names = append(names, n)
		}
		sort.Strings(names)
		return out(strings.Join(names, "\n") + "\n"), nil
	})
	f.On("backup_prune", func(cmd string, _ []byte) (runner.Result, error) {
		for _, n := range nameInCmd.FindAllString(cmd, -1) {
			delete(f.backups, n)
		}
		return out(""), nil
	})
	f.On("dnsmasq_test", func(string, []byte) (runner.Result, error) {
		return runner.Result{ExitCode: f.testExit, Stdout: []byte(f.testOutput)}, nil
	})
	f.On("pidof_dnsmasq", func(string, []byte) (runner.Result, error) {
		if !f.running {
			return out(""), nil
		}
		return out(fmt.Sprintf("%d\n", f.pid)), nil
	})
	f.On("restart_dnsmasq", func(string, []byte) (runner.Result, error) {
		if !f.restartIgnored {
			f.pid++
			f.running = !strings.Contains(f.content, "bad-option")
		}
		return out(""), nil
	})
	f.On("dnsmasq_health", func(string, []byte) (runner.Result, error) {
		if f.running && f.healthOK {
			return out("Server: 127.0.0.1\nAddress 1: 127.0.0.1\n\nName: router.asus.com\nAddress 1: 192.0.2.1\n"), nil
		}
		return runner.Result{ExitCode: 1, Stdout: []byte("nslookup: can't resolve 'router.asus.com'")}, nil
	})
	f.On("dnsmasq_effective", func(string, []byte) (runner.Result, error) { return out(f.effective), nil })
	return f
}

func newManager(t *testing.T, f *fakeRouter) (*Manager, *state.Store) {
	t.Helper()
	store, err := state.Open(filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatal(err)
	}
	m := New(f, Options{
		AddPath:      "/jffs/configs/dnsmasq.conf.add",
		BackupDir:    "/jffs/merlin-mcp/backups",
		HealthDomain: "router.asus.com",
		Keep:         3,
		Store:        store,
		Sleep:        func(time.Duration) {},
	})
	return m, store
}
```

- [ ] **Step 2: 写失败测试**

`internal/dnsmasq/manager_test.go`：

```go
package dnsmasq

import (
	"context"
	"strings"
	"testing"
)

const original = "dhcp-mac=set:openwrt,AA:BB:CC:00:00:01\n"

func TestReadMissingFile(t *testing.T) {
	f := newFakeRouter()
	m, _ := newManager(t, f)
	v, err := m.Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if v.Exists || len(v.Lines) != 0 || v.SHA256 != SHA256("") {
		t.Fatalf("v = %+v", v)
	}
}

func TestReadNumbersLines(t *testing.T) {
	f := newFakeRouter()
	f.exists, f.content = true, original+"address=/a.example/0.0.0.0\n"
	m, _ := newManager(t, f)
	v, _ := m.Read(context.Background())
	if !v.Exists || len(v.Lines) != 2 || v.Lines[1].N != 2 || v.Lines[1].Text != "address=/a.example/0.0.0.0" {
		t.Fatalf("v = %+v", v)
	}
}

func TestAddWritesBackupAndSetsBaseline(t *testing.T) {
	f := newFakeRouter()
	f.exists, f.content = true, original
	m, store := newManager(t, f)
	res, err := m.Edit(context.Background(), OpAdd, []string{"address=/a.example/0.0.0.0"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if f.content != original+"address=/a.example/0.0.0.0\n" {
		t.Fatalf("content = %q", f.content)
	}
	if !res.Changed || !res.PendingApply || res.Backup == "" || f.backups[res.Backup] != original {
		t.Fatalf("res = %+v", res)
	}
	st := store.Get().Dnsmasq
	if st.KnownGoodBackup != res.Backup || st.LastMCPSHA256 != SHA256(f.content) || res.SHA256 != st.LastMCPSHA256 {
		t.Fatalf("state = %+v", st)
	}
	if !strings.Contains(res.Diff, "+address=/a.example/0.0.0.0") {
		t.Fatalf("diff = %s", res.Diff)
	}
}

func TestAddExistingLineDoesNotWrite(t *testing.T) {
	f := newFakeRouter()
	f.exists, f.content = true, original
	m, _ := newManager(t, f)
	res, err := m.Edit(context.Background(), OpAdd, []string{strings.TrimSpace(original)}, false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Changed || len(res.Skipped) != 1 || len(f.CallsFor("addfile_write")) != 0 || len(f.CallsFor("addfile_backup")) != 0 {
		t.Fatalf("res = %+v", res)
	}
}

func TestDryRunDoesNotWrite(t *testing.T) {
	f := newFakeRouter()
	f.exists, f.content = true, original
	m, store := newManager(t, f)
	res, err := m.Edit(context.Background(), OpAdd, []string{"x=1"}, true)
	if err != nil {
		t.Fatal(err)
	}
	if !res.DryRun || !res.Changed || res.PendingApply || !strings.Contains(res.Diff, "+x=1") {
		t.Fatalf("res = %+v", res)
	}
	if f.content != original || len(f.CallsFor("addfile_write")) != 0 || store.Get().Dnsmasq.KnownGoodBackup != "" {
		t.Fatal("dry_run 不应写文件、备份或修改状态")
	}
}

func TestRemoveReportsNotFound(t *testing.T) {
	f := newFakeRouter()
	f.exists, f.content = true, original+"x=1\n"
	m, _ := newManager(t, f)
	res, err := m.Edit(context.Background(), OpRemove, []string{"x=1", "y=2"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if f.content != original || len(res.Removed) != 1 || len(res.NotFound) != 1 || res.NotFound[0] != "y=2" {
		t.Fatalf("content=%q res=%+v", f.content, res)
	}
}

func TestExternalChangeDetectedOnEdit(t *testing.T) {
	f := newFakeRouter()
	f.exists, f.content = true, original
	m, _ := newManager(t, f)
	if _, err := m.Edit(context.Background(), OpAdd, []string{"x=1"}, false); err != nil {
		t.Fatal(err)
	}
	f.content += "manual=1\n" // 有人绕过 MCP 修改了文件
	res, err := m.Edit(context.Background(), OpAdd, []string{"y=2"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if !res.ExternalChangeDetected {
		t.Fatal("应检测到外部修改")
	}
}

func TestContentTravelsOnlyViaStdin(t *testing.T) {
	f := newFakeRouter()
	f.exists, f.content = true, original
	m, _ := newManager(t, f)
	evil := "address=/$(reboot)/`id`/'x'"
	if _, err := m.Edit(context.Background(), OpAdd, []string{evil}, false); err != nil {
		t.Fatal(err)
	}
	for _, c := range f.Calls() {
		if strings.Contains(c.Cmd, "reboot") || strings.Contains(c.Cmd, "`id`") {
			t.Fatalf("配置内容出现在命令行中: %s", c.Cmd)
		}
	}
	w := f.CallsFor("addfile_write")
	if len(w) != 1 || !strings.Contains(string(w[0].Stdin), evil) {
		t.Fatalf("写入内容应通过 stdin 传输: %+v", w)
	}
}

func TestEditPrunesOldBackupsButKeepsKnownGood(t *testing.T) {
	f := newFakeRouter()
	f.exists, f.content = true, original
	m, store := newManager(t, f) // Keep = 3
	for i := 0; i < 6; i++ {
		if _, err := m.Edit(context.Background(), OpAdd, []string{strings.Repeat("x", i+1) + "=1"}, false); err != nil {
			t.Fatal(err)
		}
	}
	kg := store.Get().Dnsmasq.KnownGoodBackup
	if _, ok := f.backups[kg]; !ok {
		t.Fatal("known good 备份不能被清理")
	}
	if len(f.backups) != 4 { // 3 份最新的 + 1 份 known good
		t.Fatalf("backups = %d: %v", len(f.backups), f.backups)
	}
}

func TestPruneList(t *testing.T) {
	names := []string{
		"dnsmasq.conf.add.20261001-000000",
		"dnsmasq.conf.add.20261002-000000",
		"dnsmasq.conf.add.20261002-000000-2",
		"dnsmasq.conf.add.20261003-000000",
		"other-file",
	}
	got := PruneList(names, 2, "dnsmasq.conf.add.20261001-000000")
	if len(got) != 1 || got[0] != "dnsmasq.conf.add.20261002-000000" {
		t.Fatalf("got = %v", got)
	}
	if PruneList(names, 10, "") != nil {
		t.Fatal("数量未超过 keep 时不应清理")
	}
}

func TestEffectiveFiltersByKeyword(t *testing.T) {
	f := newFakeRouter()
	f.effective = "pid-file=/var/run/dnsmasq.pid\nDHCP-MAC=set:openwrt,x\nport=53\n"
	m, _ := newManager(t, f)
	got, err := m.Effective(context.Background(), "dhcp-mac")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].N != 2 {
		t.Fatalf("got = %+v", got)
	}
	all, _ := m.Effective(context.Background(), "")
	if len(all) != 3 {
		t.Fatalf("all = %+v", all)
	}
}
```

- [ ] **Step 3: 运行测试，确认失败**

Run: `go test ./internal/dnsmasq/`
Expected: FAIL，编译错误 `undefined: New`

- [ ] **Step 4: 实现 manager**

`internal/dnsmasq/manager.go`：

```go
package dnsmasq

import (
	"context"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/rshun/merlin-mcp/internal/apperr"
	"github.com/rshun/merlin-mcp/internal/runner"
	"github.com/rshun/merlin-mcp/internal/shell"
	"github.com/rshun/merlin-mcp/internal/state"
)

// Options 是 Manager 的配置。路径都是路由器上的路径。
type Options struct {
	AddPath      string
	BackupDir    string
	HealthDomain string
	Keep         int
	Store        *state.Store
	Sleep        func(time.Duration) // 测试时替换为空函数
	PIDWait      time.Duration       // 等待 dnsmasq 新进程出现的上限，默认 10s
}

// Manager 管理 dnsmasq.conf.add。所有读改写操作共用一把锁。
type Manager struct {
	r  runner.Runner
	o  Options
	mu sync.Mutex
}

// New 创建 Manager。
func New(r runner.Runner, o Options) *Manager {
	if o.Sleep == nil {
		o.Sleep = time.Sleep
	}
	if o.PIDWait <= 0 {
		o.PIDWait = 10 * time.Second
	}
	return &Manager{r: r, o: o}
}

// NumberedLine 是带行号的一行。
type NumberedLine struct {
	N    int    `json:"n"`
	Text string `json:"text"`
}

// FileView 是 dnsmasq_addfile_read 的返回内容。
type FileView struct {
	Path    string         `json:"path"`
	Exists  bool           `json:"exists"`
	SHA256  string         `json:"sha256"`
	Lines   []NumberedLine `json:"lines"`
	content string
}

func number(content string) []NumberedLine {
	lines := splitContent(content)
	out := make([]NumberedLine, 0, len(lines))
	for i, l := range lines {
		out = append(out, NumberedLine{N: i + 1, Text: l})
	}
	return out
}

// Read 读取 .add 文件；文件不存在时 Exists 为 false，不报错。
func (m *Manager) Read(ctx context.Context) (FileView, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.read(ctx)
}

func (m *Manager) read(ctx context.Context) (FileView, error) {
	p := shell.Quote(m.o.AddPath)
	out, err := runner.Output(ctx, m.r, runner.Op("addfile_read",
		fmt.Sprintf("if [ -f %s ]; then echo EXISTS; cat %s; else echo MISSING; fi", p, p)))
	if err != nil {
		return FileView{}, err
	}
	head, body, _ := strings.Cut(out, "\n")
	v := FileView{Path: m.o.AddPath, Exists: strings.TrimSpace(head) == "EXISTS"}
	if v.Exists {
		v.content = body
	}
	v.SHA256 = SHA256(v.content)
	v.Lines = number(v.content)
	return v, nil
}

// write 通过 stdin 写入内容，先写临时文件再 mv，保证原子替换。
func (m *Manager) write(ctx context.Context, content string) error {
	tmp := shell.Quote(m.o.AddPath + ".mcp-tmp")
	cmd := fmt.Sprintf("mkdir -p %s && cat > %s && mv %s %s",
		shell.Quote(path.Dir(m.o.AddPath)), tmp, tmp, shell.Quote(m.o.AddPath))
	res, err := m.r.Run(ctx, runner.Op("addfile_write", cmd), []byte(content))
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		return runner.Failed(res)
	}
	return nil
}

var backupNameRe = regexp.MustCompile(`^dnsmasq\.conf\.add\.(\d{8}-\d{6})(?:-(\d+))?$`)

// backup 把 content 写成一份新的备份，返回备份文件名（不含目录）。
// 文件名使用路由器本地时间，同一秒内重复时追加 -2、-3。
func (m *Manager) backup(ctx context.Context, content string) (string, error) {
	script := fmt.Sprintf(`d=%s; mkdir -p "$d" && ts=$(date +%%Y%%m%%d-%%H%%M%%S) && f="$d/dnsmasq.conf.add.$ts" && n=2 && `+
		`while [ -e "$f" ]; do f="$d/dnsmasq.conf.add.$ts-$n"; n=$((n+1)); done && cat > "$f" && echo "$f"`,
		shell.Quote(m.o.BackupDir))
	res, err := m.r.Run(ctx, runner.Op("addfile_backup", script), []byte(content))
	if err != nil {
		return "", err
	}
	if res.ExitCode != 0 {
		return "", runner.Failed(res)
	}
	name := path.Base(strings.TrimSpace(string(res.Stdout)))
	if !backupNameRe.MatchString(name) {
		return "", apperr.New(apperr.Internal, "备份文件名异常: "+name, "")
	}
	return name, nil
}

func (m *Manager) readBackup(ctx context.Context, name string) (string, error) {
	if !backupNameRe.MatchString(name) {
		return "", fmt.Errorf("非法的备份文件名 %q", name)
	}
	return runner.Output(ctx, m.r, runner.Op("backup_read", "cat "+shell.Quote(path.Join(m.o.BackupDir, name))))
}

// prune 只保留最新的 Keep 份备份，protect 指向的备份（known good）永不删除。
func (m *Manager) prune(ctx context.Context, protect string) error {
	out, err := runner.Output(ctx, m.r, runner.Op("backup_list", fmt.Sprintf("ls -1 %s 2>/dev/null || true", shell.Quote(m.o.BackupDir))))
	if err != nil {
		return err
	}
	victims := PruneList(strings.Fields(out), m.o.Keep, protect)
	if len(victims) == 0 {
		return nil
	}
	quoted := make([]string, len(victims))
	for i, v := range victims {
		quoted[i] = shell.Quote(v)
	}
	_, err = runner.Output(ctx, m.r, runner.Op("backup_prune",
		fmt.Sprintf("cd %s && rm -f -- %s", shell.Quote(m.o.BackupDir), strings.Join(quoted, " "))))
	return err
}

// PruneList 计算需要删除的备份：只考虑符合命名规则的文件，排除 protect，保留最新的 keep 份。
func PruneList(names []string, keep int, protect string) []string {
	type item struct {
		name, ts string
		seq      int
	}
	var items []item
	for _, n := range names {
		mm := backupNameRe.FindStringSubmatch(n)
		if mm == nil || n == protect {
			continue
		}
		seq := 1
		if mm[2] != "" {
			seq, _ = strconv.Atoi(mm[2])
		}
		items = append(items, item{n, mm[1], seq})
	}
	if len(items) <= keep {
		return nil
	}
	sort.Slice(items, func(i, j int) bool { // 新的在前
		if items[i].ts != items[j].ts {
			return items[i].ts > items[j].ts
		}
		return items[i].seq > items[j].seq
	})
	var out []string
	for _, it := range items[keep:] {
		out = append(out, it.name)
	}
	sort.Strings(out)
	return out
}

// EditOp 是 Edit 的操作类型。
type EditOp string

const (
	OpAdd    EditOp = "add"
	OpRemove EditOp = "remove"
)

// EditResult 是 dnsmasq_addfile_add / remove 的返回内容。
type EditResult struct {
	DryRun                 bool     `json:"dry_run"`
	Changed                bool     `json:"changed"`
	Added                  []string `json:"added,omitempty"`
	Skipped                []string `json:"skipped,omitempty"`
	Removed                []string `json:"removed,omitempty"`
	NotFound               []string `json:"not_found,omitempty"`
	Diff                   string   `json:"diff"`
	SHA256                 string   `json:"sha256"`
	Backup                 string   `json:"backup,omitempty"`
	ExternalChangeDetected bool     `json:"external_change_detected"`
	PendingApply           bool     `json:"pending_apply"`
	Warning                string   `json:"warning,omitempty"`
}

// Edit 添加或删除行。只修改文件，不让 dnsmasq 生效。
// 第一次修改时，修改前的内容同时作为初始的 known good（假设它就是当前生效的版本）。
func (m *Manager) Edit(ctx context.Context, op EditOp, lines []string, dryRun bool) (EditResult, error) {
	norm, err := NormalizeInput(lines)
	if err != nil {
		return EditResult{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	view, err := m.read(ctx)
	if err != nil {
		return EditResult{}, err
	}
	st := m.o.Store.Get().Dnsmasq
	res := EditResult{DryRun: dryRun, ExternalChangeDetected: st.LastMCPSHA256 != "" && st.LastMCPSHA256 != view.SHA256}

	var next string
	switch op {
	case OpAdd:
		next, res.Added, res.Skipped = Add(view.content, norm)
	case OpRemove:
		next, res.Removed, res.NotFound = Remove(view.content, norm)
	default:
		return EditResult{}, apperr.New(apperr.InvalidArgument, "未知操作: "+string(op), "")
	}
	res.Changed = next != join(splitContent(view.content))
	res.Diff = Diff(view.content, next)
	res.SHA256 = view.SHA256
	if res.Changed {
		res.SHA256 = SHA256(next)
	}
	if dryRun || !res.Changed {
		return res, nil
	}

	name, err := m.backup(ctx, view.content)
	if err != nil {
		return EditResult{}, err
	}
	if err := m.write(ctx, next); err != nil {
		return EditResult{}, err
	}
	err = m.o.Store.Update(func(s *state.State) error {
		if s.Dnsmasq.KnownGoodBackup == "" {
			s.Dnsmasq.KnownGoodBackup = name
		}
		s.Dnsmasq.LastMCPSHA256 = res.SHA256
		return nil
	})
	if err != nil {
		return EditResult{}, apperr.New(apperr.Internal, "文件已修改，但状态文件写入失败: "+err.Error(), "检查 state_dir 是否可写")
	}
	res.Backup, res.PendingApply = name, true
	if err := m.prune(ctx, m.o.Store.Get().Dnsmasq.KnownGoodBackup); err != nil {
		res.Warning = "清理旧备份失败: " + apperr.From(err).Message
	}
	return res, nil
}

// Effective 读取 /etc/dnsmasq.conf（Merlin 合并 .add 后实际生成的配置），可按关键字过滤，保留原始行号。
func (m *Manager) Effective(ctx context.Context, keyword string) ([]NumberedLine, error) {
	out, err := runner.Output(ctx, m.r, runner.Op("dnsmasq_effective", "cat /etc/dnsmasq.conf"))
	if err != nil {
		return nil, err
	}
	all := number(out)
	if keyword == "" {
		return all, nil
	}
	kw := strings.ToLower(keyword)
	var hits []NumberedLine
	for _, l := range all {
		if strings.Contains(strings.ToLower(l.Text), kw) {
			hits = append(hits, l)
		}
	}
	return hits, nil
}
```

- [ ] **Step 5: 运行测试，确认通过**

Run: `go test ./internal/dnsmasq/`
Expected: PASS

- [ ] **Step 6: 全量检查**

Run: `go test ./... && go vet ./... && gofmt -l .`
Expected: PASS，`gofmt -l .` 无输出

- [ ] **Step 7: 提交**

按"提交步骤模板"执行，`<files>` 为 `internal/dnsmasq`，提交说明：`feat: add dnsmasq.conf.add read, edit, backup and prune`

---

### Task 12: dnsmasq 生效与自动回滚

**Files:**
- Create: `internal/dnsmasq/apply.go`、`internal/dnsmasq/apply_test.go`

**Interfaces:**
- Consumes: Task 11 的 `Manager` 及其包内方法；`diagnose.ParseNslookup`（Task 9）；`state`（Task 4）
- Produces:
  - `dnsmasq.ApplyResult{ValidationSkipped, OldPID, NewPID, HealthCheck, KnownGoodBackup, SHA256 string}`
  - `(*Manager).Apply(ctx, acceptExternal bool) (ApplyResult, error)`；失败时的错误码：`ADDFILE_CHANGED_EXTERNALLY`、`DNSMASQ_VALIDATION_FAILED`、`DNSMASQ_ROLLED_BACK`、`DNSMASQ_ROLLBACK_FAILED`
  - 使用的操作名：`dnsmasq_test`、`pidof_dnsmasq`、`restart_dnsmasq`、`dnsmasq_health`

- [ ] **Step 1: 写失败测试**

`internal/dnsmasq/apply_test.go`：

```go
package dnsmasq

import (
	"context"
	"strings"
	"testing"

	"github.com/rshun/merlin-mcp/internal/apperr"
)

func TestApplySuccessUpdatesKnownGood(t *testing.T) {
	f := newFakeRouter()
	f.exists, f.content = true, original
	m, store := newManager(t, f)
	if _, err := m.Edit(context.Background(), OpAdd, []string{"address=/a.example/0.0.0.0"}, false); err != nil {
		t.Fatal(err)
	}
	res, err := m.Apply(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	if res.OldPID == res.NewPID || res.HealthCheck != "ok" || res.ValidationSkipped != "" {
		t.Fatalf("res = %+v", res)
	}
	st := store.Get().Dnsmasq
	if st.KnownGoodBackup != res.KnownGoodBackup || f.backups[res.KnownGoodBackup] != f.content || st.LastMCPSHA256 != SHA256(f.content) {
		t.Fatalf("state=%+v res=%+v", st, res)
	}
}

func TestApplyValidationFailureDoesNotRestart(t *testing.T) {
	f := newFakeRouter()
	f.exists, f.content = true, original
	f.testExit, f.testOutput = 1, "dnsmasq: bad option at line 1 of /jffs/configs/dnsmasq.conf.add"
	m, _ := newManager(t, f)
	_, err := m.Apply(context.Background(), false)
	e := apperr.From(err)
	if e.Code != apperr.DnsmasqValidationFailed || !strings.Contains(e.Details["output"].(string), "bad option") {
		t.Fatalf("err = %+v", e)
	}
	if len(f.CallsFor("restart_dnsmasq")) != 0 {
		t.Fatal("校验失败时不应重启 dnsmasq")
	}
}

func TestApplySkipsValidationWhenUnsupported(t *testing.T) {
	f := newFakeRouter()
	f.exists, f.content = true, original
	f.testExit, f.testOutput = 1, "dnsmasq: unrecognized option '--test'"
	m, _ := newManager(t, f)
	res, err := m.Apply(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	if res.ValidationSkipped == "" {
		t.Fatal("不支持 --test 时应跳过校验并注明")
	}
}

func TestApplyRollsBackWhenDnsmasqDies(t *testing.T) {
	f := newFakeRouter()
	f.exists, f.content = true, original
	m, store := newManager(t, f)
	if _, err := m.Edit(context.Background(), OpAdd, []string{"bad-option=1"}, false); err != nil {
		t.Fatal(err)
	}
	_, err := m.Apply(context.Background(), false)
	e := apperr.From(err)
	if e.Code != apperr.DnsmasqRolledBack || e.Details["cause"] == nil {
		t.Fatalf("err = %+v", e)
	}
	if f.content != original || !f.running {
		t.Fatalf("应回滚到原始内容并恢复运行: content=%q running=%v", f.content, f.running)
	}
	if store.Get().Dnsmasq.LastMCPSHA256 != SHA256(original) {
		t.Fatal("回滚后 last_mcp_sha256 应为 known good 的 sha256")
	}
}

func TestApplyReportsRollbackFailure(t *testing.T) {
	f := newFakeRouter()
	f.exists, f.content = true, original
	f.healthOK = false
	m, _ := newManager(t, f)
	_, err := m.Apply(context.Background(), false)
	e := apperr.From(err)
	if e.Code != apperr.DnsmasqRollbackFailed || e.Details["backup_path"] == nil || e.Details["rollback_error"] == nil {
		t.Fatalf("err = %+v", e)
	}
}

func TestApplyDetectsRestartThatDidNotHappen(t *testing.T) {
	f := newFakeRouter()
	f.exists, f.content = true, original
	f.restartIgnored = true
	m, _ := newManager(t, f)
	_, err := m.Apply(context.Background(), false)
	e := apperr.From(err)
	if e.Code != apperr.DnsmasqRollbackFailed || !strings.Contains(e.Details["cause"].(string), "没有以新进程重新启动") {
		t.Fatalf("err = %+v", e)
	}
}

func TestApplyRefusesExternalChangeUnlessAccepted(t *testing.T) {
	f := newFakeRouter()
	f.exists, f.content = true, original
	m, _ := newManager(t, f)
	if _, err := m.Edit(context.Background(), OpAdd, []string{"x=1"}, false); err != nil {
		t.Fatal(err)
	}
	f.content += "manual=1\n"
	_, err := m.Apply(context.Background(), false)
	if apperr.CodeOf(err) != apperr.AddFileChangedExternally {
		t.Fatalf("err = %v", err)
	}
	if len(f.CallsFor("restart_dnsmasq")) != 0 {
		t.Fatal("拒绝时不应重启")
	}
	if _, err := m.Apply(context.Background(), true); err != nil {
		t.Fatalf("accept_external_changes=true 应继续: %v", err)
	}
}

func TestApplyWithoutPriorEditCreatesBaseline(t *testing.T) {
	f := newFakeRouter()
	f.exists, f.content = true, original
	m, store := newManager(t, f)
	if _, err := m.Apply(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	if store.Get().Dnsmasq.KnownGoodBackup == "" {
		t.Fatal("应记录 known good")
	}
}
```

- [ ] **Step 2: 运行测试，确认失败**

Run: `go test ./internal/dnsmasq/`
Expected: FAIL，编译错误 `m.Apply undefined`

- [ ] **Step 3: 实现 apply**

`internal/dnsmasq/apply.go`：

```go
package dnsmasq

import (
	"context"
	"errors"
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/rshun/merlin-mcp/internal/apperr"
	"github.com/rshun/merlin-mcp/internal/diagnose"
	"github.com/rshun/merlin-mcp/internal/runner"
	"github.com/rshun/merlin-mcp/internal/shell"
	"github.com/rshun/merlin-mcp/internal/state"
)

// ApplyResult 是 dnsmasq_apply 成功时的返回内容。
type ApplyResult struct {
	ValidationSkipped string `json:"validation_skipped,omitempty"`
	OldPID            string `json:"old_pid"`
	NewPID            string `json:"new_pid"`
	HealthCheck       string `json:"health_check"`
	KnownGoodBackup   string `json:"known_good_backup"`
	SHA256            string `json:"sha256"`
}

// Apply 让 .add 文件生效：检查外部修改 → 语法校验 → 重启 dnsmasq → 等待新进程 → 解析检查。
// 重启后的任一步失败都会恢复 known good 版本并再次重启（流程见 spec 5.3）。
func (m *Manager) Apply(ctx context.Context, acceptExternal bool) (ApplyResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	view, err := m.read(ctx)
	if err != nil {
		return ApplyResult{}, err
	}
	st := m.o.Store.Get().Dnsmasq
	if st.LastMCPSHA256 != "" && view.SHA256 != st.LastMCPSHA256 && !acceptExternal {
		return ApplyResult{}, apperr.New(apperr.AddFileChangedExternally, "dnsmasq.conf.add 在 MCP 之外被修改过",
			"先用 dnsmasq_addfile_read 查看当前内容，向用户确认后带 accept_external_changes: true 再次调用").
			With("expected_sha256", st.LastMCPSHA256).With("current_sha256", view.SHA256)
	}
	if st.KnownGoodBackup == "" {
		name, err := m.backup(ctx, view.content)
		if err != nil {
			return ApplyResult{}, err
		}
		if err := m.setState(name, view.SHA256); err != nil {
			return ApplyResult{}, err
		}
		st.KnownGoodBackup = name
	}

	var res ApplyResult
	if view.Exists {
		skipped, err := m.validate(ctx)
		if err != nil {
			return ApplyResult{}, err
		}
		res.ValidationSkipped = skipped
	}

	oldPID, newPID, cause := m.restartAndCheck(ctx)
	if cause != nil {
		return ApplyResult{}, m.rollback(ctx, st.KnownGoodBackup, cause)
	}
	name, err := m.backup(ctx, view.content)
	if err != nil {
		return ApplyResult{}, apperr.New(apperr.Internal, "dnsmasq 已生效，但保存 known good 备份失败: "+apperr.From(err).Message, "")
	}
	if err := m.setState(name, view.SHA256); err != nil {
		return ApplyResult{}, err
	}
	_ = m.prune(ctx, name)
	res.OldPID, res.NewPID, res.HealthCheck, res.KnownGoodBackup, res.SHA256 = oldPID, newPID, "ok", name, view.SHA256
	return res, nil
}

func (m *Manager) setState(knownGood, sha string) error {
	err := m.o.Store.Update(func(s *state.State) error {
		s.Dnsmasq.KnownGoodBackup = knownGood
		s.Dnsmasq.LastMCPSHA256 = sha
		return nil
	})
	if err != nil {
		return apperr.New(apperr.Internal, "状态文件写入失败: "+err.Error(), "检查 state_dir 是否可写")
	}
	return nil
}

// validate 执行 dnsmasq --test；路由器的 dnsmasq 不支持 --test 时返回跳过说明。
func (m *Manager) validate(ctx context.Context) (string, error) {
	res, err := m.r.Run(ctx, runner.Op("dnsmasq_test", "dnsmasq --test -C "+shell.Quote(m.o.AddPath)+" 2>&1"), nil)
	if err != nil {
		return "", err
	}
	out := strings.TrimSpace(string(res.Stdout))
	lower := strings.ToLower(out)
	switch {
	case res.ExitCode == 0:
		return "", nil
	case strings.Contains(lower, "--test") && (strings.Contains(lower, "unrecognized") || strings.Contains(lower, "invalid") || strings.Contains(lower, "illegal")):
		return "路由器上的 dnsmasq 不支持 --test，已跳过语法校验", nil
	default:
		return "", apperr.New(apperr.DnsmasqValidationFailed, "dnsmasq 语法校验未通过，没有重启 dnsmasq，线上配置不受影响",
			"根据 details.output 修正配置后重试").With("output", out)
	}
}

func (m *Manager) pid(ctx context.Context) (string, error) {
	out, err := runner.Output(ctx, m.r, runner.Op("pidof_dnsmasq", "pidof dnsmasq || true"))
	return strings.TrimSpace(out), err
}

// restartAndCheck 重启 dnsmasq，等待出现新的 PID，再做一次解析检查。
// Asus 的 service 命令只是异步通知 rc，所以必须等 PID 变化，不能只看命令返回。
func (m *Manager) restartAndCheck(ctx context.Context) (oldPID, newPID string, err error) {
	if oldPID, err = m.pid(ctx); err != nil {
		return "", "", err
	}
	if _, err = runner.Output(ctx, m.r, runner.Op("restart_dnsmasq", "service restart_dnsmasq")); err != nil {
		return oldPID, "", fmt.Errorf("执行 service restart_dnsmasq 失败: %w", err)
	}
	const step = 500 * time.Millisecond
	for waited := time.Duration(0); waited < m.o.PIDWait; waited += step {
		m.o.Sleep(step)
		cur, err := m.pid(ctx)
		if err != nil {
			return oldPID, "", err
		}
		if cur != "" && cur != oldPID {
			newPID = cur
			break
		}
	}
	if newPID == "" {
		return oldPID, "", fmt.Errorf("dnsmasq 在 %s 内没有以新进程重新启动（重启前 PID: %q）", m.o.PIDWait, oldPID)
	}
	if err := m.health(ctx); err != nil {
		return oldPID, newPID, err
	}
	return oldPID, newPID, nil
}

func (m *Manager) health(ctx context.Context) error {
	res, err := m.r.Run(ctx, runner.Op("dnsmasq_health",
		fmt.Sprintf("nslookup %s 127.0.0.1 2>&1", shell.Quote(m.o.HealthDomain))), nil)
	if err != nil {
		return err
	}
	if res.ExitCode != 0 || len(diagnose.ParseNslookup(string(res.Stdout))) == 0 {
		return fmt.Errorf("健康检查失败：dnsmasq 无法解析 %s，输出: %s", m.o.HealthDomain, strings.TrimSpace(string(res.Stdout)))
	}
	return nil
}

// rollback 恢复 known good 版本并重启，返回 DNSMASQ_ROLLED_BACK 或 DNSMASQ_ROLLBACK_FAILED。
func (m *Manager) rollback(ctx context.Context, knownGood string, cause error) error {
	backupPath := path.Join(m.o.BackupDir, knownGood)
	fail := func(rbErr error) error {
		return apperr.New(apperr.DnsmasqRollbackFailed, "dnsmasq 生效失败，自动回滚也失败了，需要人工处理",
			"登录路由器，把 details.backup_path 的内容复制回 dnsmasq.conf.add，然后执行 service restart_dnsmasq").
			With("cause", errText(cause)).With("rollback_error", errText(rbErr)).With("backup_path", backupPath)
	}
	content, err := m.readBackup(ctx, knownGood)
	if err != nil {
		return fail(err)
	}
	if err := m.write(ctx, content); err != nil {
		return fail(err)
	}
	if _, _, err := m.restartAndCheck(ctx); err != nil {
		return fail(err)
	}
	if err := m.o.Store.Update(func(s *state.State) error {
		s.Dnsmasq.LastMCPSHA256 = SHA256(content)
		return nil
	}); err != nil {
		return fail(err)
	}
	return apperr.New(apperr.DnsmasqRolledBack, "新配置生效失败，已自动回滚到上一个可用版本",
		"根据 details.cause 修正配置；未生效的修改可以在 backup_dir 的备份中找到").
		With("cause", errText(cause)).With("restored_backup", backupPath)
}

// errText 优先使用 apperr 的中文 message，其他错误用 Error()。
func errText(err error) string {
	var e *apperr.Error
	if errors.As(err, &e) {
		return e.Message
	}
	return err.Error()
}
```

- [ ] **Step 4: 运行测试，确认通过**

Run: `go test ./internal/dnsmasq/`
Expected: PASS

- [ ] **Step 5: 全量检查**

Run: `go test ./... && go vet ./... && gofmt -l .`
Expected: PASS，`gofmt -l .` 无输出

- [ ] **Step 6: 提交**

按"提交步骤模板"执行，`<files>` 为 `internal/dnsmasq`，提交说明：`feat: add dnsmasq apply with validation, health check and rollback`

---

### Task 13: 每日重启额度与重启

**Files:**
- Create: `internal/reboot/reboot.go`、`internal/reboot/reboot_test.go`

**Interfaces:**
- Consumes: `runner`、`runnertest`、`apperr`（Task 1）；`state.Store`（Task 4）
- Produces:
  - `reboot.NewQuota(store *state.Store, loc *time.Location, maxPerDay int, now func() time.Time) *Quota`
  - `reboot.Status{AllowedToday bool; UsedToday, MaxPerDay int; LastMCPRebootAt *string}`、`(*Quota).Status() Status`
  - `(*Quota).Reserve() (time.Time, error)`：额度用完时返回 `REBOOT_QUOTA_EXCEEDED`，否则先落盘再返回
  - `reboot.NewRebooter(q *Quota, r runner.Runner) *Rebooter`、`reboot.Result{RequestedAt, Message string}`、`(*Rebooter).Reboot(ctx) (Result, error)`（操作名 `reboot`；Runner 实现了 `Reset()` 时调用后会执行 `Reset()`）

- [ ] **Step 1: 写失败测试**

`internal/reboot/reboot_test.go`：

```go
package reboot

import (
	"context"
	"path/filepath"
	"testing"
	"time"
	_ "time/tzdata"

	"github.com/rshun/merlin-mcp/internal/apperr"
	"github.com/rshun/merlin-mcp/internal/runner"
	"github.com/rshun/merlin-mcp/internal/runner/runnertest"
	"github.com/rshun/merlin-mcp/internal/state"
)

var shanghai, _ = time.LoadLocation("Asia/Shanghai")

type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

func newQuota(t *testing.T, start time.Time) (*Quota, *state.Store, *clock) {
	t.Helper()
	store, err := state.Open(filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatal(err)
	}
	c := &clock{t: start}
	return NewQuota(store, shanghai, 1, c.now), store, c
}

func TestQuotaAllowsFirstAndRejectsSecondSameDay(t *testing.T) {
	q, store, c := newQuota(t, time.Date(2026, 10, 3, 9, 0, 0, 0, shanghai))
	if !q.Status().AllowedToday {
		t.Fatal("一开始应允许重启")
	}
	if _, err := q.Reserve(); err != nil {
		t.Fatal(err)
	}
	if len(store.Get().Reboot.History) != 1 {
		t.Fatal("Reserve 应落盘")
	}
	c.t = time.Date(2026, 10, 3, 23, 59, 0, 0, shanghai)
	_, err := q.Reserve()
	e := apperr.From(err)
	if e.Code != apperr.RebootQuotaExceeded || e.Details["last_reboot_at"] != "2026-10-03T09:00:00+08:00" {
		t.Fatalf("err = %+v", e)
	}
	s := q.Status()
	if s.AllowedToday || s.UsedToday != 1 || s.LastMCPRebootAt == nil {
		t.Fatalf("status = %+v", s)
	}
}

func TestQuotaResetsAtMidnightInConfiguredTimezone(t *testing.T) {
	q, _, c := newQuota(t, time.Date(2026, 10, 3, 23, 30, 0, 0, shanghai))
	if _, err := q.Reserve(); err != nil {
		t.Fatal(err)
	}
	c.t = time.Date(2026, 10, 4, 0, 10, 0, 0, shanghai)
	if _, err := q.Reserve(); err != nil {
		t.Fatalf("上海时间第二天应允许: %v", err)
	}
}

func TestQuotaUsesConfiguredTimezoneNotUTC(t *testing.T) {
	// 两次都是上海时间 10 月 4 日，但分属 UTC 的 10 月 3 日和 10 月 4 日
	q, _, c := newQuota(t, time.Date(2026, 10, 3, 20, 0, 0, 0, time.UTC))
	if _, err := q.Reserve(); err != nil {
		t.Fatal(err)
	}
	c.t = time.Date(2026, 10, 4, 1, 0, 0, 0, time.UTC)
	if _, err := q.Reserve(); apperr.CodeOf(err) != apperr.RebootQuotaExceeded {
		t.Fatalf("按上海时间是同一天，应拒绝: %v", err)
	}
}

func TestHistoryIsTrimmed(t *testing.T) {
	q, store, c := newQuota(t, time.Date(2026, 1, 1, 12, 0, 0, 0, shanghai))
	for i := 0; i < 40; i++ {
		c.t = c.t.AddDate(0, 0, 1)
		if _, err := q.Reserve(); err != nil {
			t.Fatal(err)
		}
	}
	if n := len(store.Get().Reboot.History); n != 30 {
		t.Fatalf("history = %d，应只保留 30 条", n)
	}
}

type resettableFake struct {
	*runnertest.Fake
	resets int
}

func (r *resettableFake) Reset() { r.resets++ }

func TestRebooterRecordsBeforeRunningAndResets(t *testing.T) {
	q, store, _ := newQuota(t, time.Date(2026, 10, 3, 9, 0, 0, 0, shanghai))
	f := &resettableFake{Fake: runnertest.New()}
	recordedBeforeRun := false
	f.On("reboot", func(string, []byte) (runner.Result, error) {
		recordedBeforeRun = len(store.Get().Reboot.History) == 1
		return runner.Result{}, nil
	})
	res, err := NewRebooter(q, f).Reboot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !recordedBeforeRun {
		t.Fatal("执行 reboot 命令前必须已经记录")
	}
	if f.resets != 1 || res.RequestedAt != "2026-10-03T09:00:00+08:00" {
		t.Fatalf("resets=%d res=%+v", f.resets, res)
	}
}

func TestRebooterCountsQuotaEvenIfCommandFails(t *testing.T) {
	q, _, _ := newQuota(t, time.Date(2026, 10, 3, 9, 0, 0, 0, shanghai))
	f := runnertest.New().On("reboot", runnertest.Fail(apperr.New(apperr.SSHUnreachable, "断开", "")))
	_, err := NewRebooter(q, f).Reboot(context.Background())
	e := apperr.From(err)
	if e.Code != apperr.SSHUnreachable || e.Details["quota_consumed"] != true {
		t.Fatalf("err = %+v", e)
	}
	if q.Status().AllowedToday {
		t.Fatal("命令失败也应计入额度")
	}
}

func TestRebooterDoesNotRunWhenQuotaExceeded(t *testing.T) {
	q, _, _ := newQuota(t, time.Date(2026, 10, 3, 9, 0, 0, 0, shanghai))
	f := runnertest.New().On("reboot", runnertest.Stdout(""))
	b := NewRebooter(q, f)
	if _, err := b.Reboot(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Reboot(context.Background()); apperr.CodeOf(err) != apperr.RebootQuotaExceeded {
		t.Fatalf("err = %v", err)
	}
	if n := len(f.CallsFor("reboot")); n != 1 {
		t.Fatalf("reboot 命令执行了 %d 次", n)
	}
}
```

- [ ] **Step 2: 运行测试，确认失败**

Run: `go test ./internal/reboot/`
Expected: FAIL，编译错误

- [ ] **Step 3: 实现 reboot**

`internal/reboot/reboot.go`：

```go
// Package reboot 实现每日重启额度和重启操作。
package reboot

import (
	"context"
	"fmt"
	"time"

	"github.com/rshun/merlin-mcp/internal/apperr"
	"github.com/rshun/merlin-mcp/internal/runner"
	"github.com/rshun/merlin-mcp/internal/state"
)

const historyLimit = 30

// 后台延迟 2 秒执行，让 SSH 会话先正常返回。
const rebootScript = "nohup sh -c 'sleep 2; reboot' >/dev/null 2>&1 </dev/null &"

// Quota 按配置时区的自然日统计由 MCP 发起的重启次数。
type Quota struct {
	store *state.Store
	loc   *time.Location
	max   int
	now   func() time.Time
}

// NewQuota 创建额度检查器。now 在测试中可替换。
func NewQuota(store *state.Store, loc *time.Location, maxPerDay int, now func() time.Time) *Quota {
	return &Quota{store: store, loc: loc, max: maxPerDay, now: now}
}

// Status 是 system_status 中的重启额度信息。
type Status struct {
	AllowedToday    bool    `json:"reboot_allowed_today"`
	UsedToday       int     `json:"used_today"`
	MaxPerDay       int     `json:"max_per_day"`
	LastMCPRebootAt *string `json:"last_mcp_reboot_at"`
}

func (q *Quota) usedOn(history []time.Time, now time.Time) int {
	y, m, d := now.In(q.loc).Date()
	n := 0
	for _, t := range history {
		ty, tm, td := t.In(q.loc).Date()
		if ty == y && tm == m && td == d {
			n++
		}
	}
	return n
}

// Status 返回今天的额度使用情况。
func (q *Quota) Status() Status {
	h := q.store.Get().Reboot.History
	used := q.usedOn(h, q.now())
	s := Status{AllowedToday: used < q.max, UsedToday: used, MaxPerDay: q.max}
	if len(h) > 0 {
		v := h[len(h)-1].In(q.loc).Format(time.RFC3339)
		s.LastMCPRebootAt = &v
	}
	return s
}

// Reserve 检查额度，未超限时立即记录一次重启并落盘，返回记录的时间。
func (q *Quota) Reserve() (time.Time, error) {
	now := q.now()
	err := q.store.Update(func(s *state.State) error {
		h := s.Reboot.History
		if used := q.usedOn(h, now); used >= q.max {
			return apperr.New(apperr.RebootQuotaExceeded,
				fmt.Sprintf("今天（%s）已由 MCP 发起 %d 次重启，达到每日上限 %d 次", now.In(q.loc).Format("2006-01-02"), used, q.max),
				"如确需重启，请人工在路由器管理界面操作").
				With("last_reboot_at", h[len(h)-1].In(q.loc).Format(time.RFC3339))
		}
		h = append(h, now)
		if len(h) > historyLimit {
			h = h[len(h)-historyLimit:]
		}
		s.Reboot.History = h
		return nil
	})
	return now, err
}

// Rebooter 先占用额度，再在路由器上执行重启。
type Rebooter struct {
	quota *Quota
	r     runner.Runner
}

// NewRebooter 创建 Rebooter。r 必须是原始 Runner（不能带重试）。
func NewRebooter(q *Quota, r runner.Runner) *Rebooter { return &Rebooter{quota: q, r: r} }

// Result 是 router_reboot 成功时的返回内容。
type Result struct {
	RequestedAt string `json:"requested_at"`
	Message     string `json:"message"`
}

// Reboot 记录落盘后才执行命令；命令失败也计入额度。完成后断开 SSH 连接。
func (b *Rebooter) Reboot(ctx context.Context) (Result, error) {
	at, err := b.quota.Reserve()
	if err != nil {
		return Result{}, err
	}
	if rs, ok := b.r.(interface{ Reset() }); ok {
		defer rs.Reset()
	}
	res, err := b.r.Run(ctx, runner.Op("reboot", rebootScript), nil)
	if err == nil && res.ExitCode != 0 {
		err = runner.Failed(res)
	}
	if err != nil {
		return Result{}, apperr.From(err).With("quota_consumed", true).With("note", "重启额度已计入今天，即使命令执行结果不明确")
	}
	return Result{
		RequestedAt: at.In(b.quota.loc).Format(time.RFC3339),
		Message:     "已发出重启指令，路由器约 2 秒后重启，通常 1-3 分钟后恢复",
	}, nil
}
```

- [ ] **Step 4: 运行测试，确认通过**

Run: `go test ./internal/reboot/`
Expected: PASS

- [ ] **Step 5: 全量检查**

Run: `go test ./... && go vet ./... && gofmt -l .`
Expected: PASS，`gofmt -l .` 无输出

- [ ] **Step 6: 提交**

按"提交步骤模板"执行，`<files>` 为 `internal/reboot`，提交说明：`feat: add daily reboot quota and reboot command`

---

### Task 14: MCP 工具注册

**Files:**
- Create: `internal/tools/tools.go`、`internal/tools/readonly.go`、`internal/tools/mutations.go`
- Create: `internal/tools/tools_test.go`
- Modify: `go.mod`、`go.sum`

**Interfaces:**
- Consumes: Task 1–13 的全部公开接口
- Produces:
  - `tools.Deps{Cfg *config.Config; Runner runner.Runner; Quota *reboot.Quota; Rebooter *reboot.Rebooter; Dnsmasq *dnsmasq.Manager; Audit *audit.Logger}`
  - `tools.Register(s *mcp.Server, d Deps)`：注册 9 个只读工具；`d.Cfg.AllowMutations` 为 true 时再注册 4 个修改类工具
  - 工具名：`syslog_read`、`kernel_log_read`、`system_status`、`wan_status`、`clients_list`、`conntrack_status`、`network_diagnose`、`dnsmasq_addfile_read`、`dnsmasq_effective_config`、`dnsmasq_addfile_add`、`dnsmasq_addfile_remove`、`dnsmasq_apply`、`router_reboot`
  - 成功：`CallToolResult.Content[0]` 是缩进 JSON；失败：`IsError: true`，内容是 `apperr.Error` 的 JSON

- [ ] **Step 1: 安装 MCP SDK（Task 1 已获用户确认）**

Run: `go get github.com/modelcontextprotocol/go-sdk@v1.8.0`
Expected: `go.mod` 中出现 `github.com/modelcontextprotocol/go-sdk v1.8.0`

- [ ] **Step 2: 写失败测试**

`internal/tools/tools_test.go`：

```go
package tools_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	_ "time/tzdata"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/rshun/merlin-mcp/internal/apperr"
	"github.com/rshun/merlin-mcp/internal/audit"
	"github.com/rshun/merlin-mcp/internal/config"
	"github.com/rshun/merlin-mcp/internal/dnsmasq"
	"github.com/rshun/merlin-mcp/internal/reboot"
	"github.com/rshun/merlin-mcp/internal/runner"
	"github.com/rshun/merlin-mcp/internal/runner/runnertest"
	"github.com/rshun/merlin-mcp/internal/state"
	"github.com/rshun/merlin-mcp/internal/tools"
)

type env struct {
	deps      tools.Deps
	auditPath string
}

func newEnv(t *testing.T, r runner.Runner, allowMutations bool) env {
	t.Helper()
	dir := t.TempDir()
	store, err := state.Open(filepath.Join(dir, "state"))
	if err != nil {
		t.Fatal(err)
	}
	loc, _ := time.LoadLocation("Asia/Shanghai")
	cfg := &config.Config{
		AllowMutations: allowMutations,
		Paths:          config.Paths{DnsmasqAdd: "/jffs/configs/dnsmasq.conf.add", BackupDir: "/jffs/merlin-mcp/backups", Syslog: "auto"},
	}
	now := func() time.Time { return time.Date(2026, 10, 3, 12, 0, 0, 0, loc) }
	quota := reboot.NewQuota(store, loc, 1, now)
	auditPath := filepath.Join(dir, "audit.jsonl")
	return env{
		auditPath: auditPath,
		deps: tools.Deps{
			Cfg:      cfg,
			Runner:   r,
			Quota:    quota,
			Rebooter: reboot.NewRebooter(quota, r),
			Dnsmasq: dnsmasq.New(r, dnsmasq.Options{
				AddPath: cfg.Paths.DnsmasqAdd, BackupDir: cfg.Paths.BackupDir, HealthDomain: "router.asus.com",
				Keep: 20, Store: store, Sleep: func(time.Duration) {},
			}),
			Audit: audit.New(auditPath),
		},
	}
}

func connect(t *testing.T, d tools.Deps) *mcp.ClientSession {
	t.Helper()
	ctx := context.Background()
	server := mcp.NewServer(&mcp.Implementation{Name: "merlin-mcp", Version: "test"}, nil)
	tools.Register(server, d)
	serverT, clientT := mcp.NewInMemoryTransports()
	if _, err := server.Connect(ctx, serverT, nil); err != nil {
		t.Fatal(err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "test"}, nil)
	cs, err := client.Connect(ctx, clientT, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cs.Close() })
	return cs
}

func call(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any) (*mcp.CallToolResult, string) {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("CallTool(%s): %v", name, err)
	}
	if len(res.Content) == 0 {
		t.Fatalf("%s 没有返回内容", name)
	}
	return res, res.Content[0].(*mcp.TextContent).Text
}

func listTools(t *testing.T, cs *mcp.ClientSession) map[string]*mcp.Tool {
	t.Helper()
	res, err := cs.ListTools(context.Background(), &mcp.ListToolsParams{})
	if err != nil {
		t.Fatal(err)
	}
	m := map[string]*mcp.Tool{}
	for _, tl := range res.Tools {
		m[tl.Name] = tl
	}
	return m
}

var readOnlyNames = []string{"syslog_read", "kernel_log_read", "system_status", "wan_status", "clients_list", "conntrack_status", "network_diagnose", "dnsmasq_addfile_read", "dnsmasq_effective_config"}
var mutationNames = []string{"dnsmasq_addfile_add", "dnsmasq_addfile_remove", "dnsmasq_apply", "router_reboot"}

func TestRegistersAllToolsWhenMutationsAllowed(t *testing.T) {
	got := listTools(t, connect(t, newEnv(t, runnertest.New(), true).deps))
	if len(got) != 13 {
		t.Fatalf("工具数量 = %d", len(got))
	}
	for _, n := range append(append([]string{}, readOnlyNames...), mutationNames...) {
		if got[n] == nil {
			t.Errorf("缺少工具 %s", n)
		}
	}
	for _, n := range readOnlyNames {
		if !got[n].Annotations.ReadOnlyHint {
			t.Errorf("%s 应标记 readOnlyHint", n)
		}
	}
	if dh := got["router_reboot"].Annotations.DestructiveHint; dh == nil || !*dh {
		t.Error("router_reboot 应标记 destructiveHint")
	}
}

func TestReadOnlyModeRegistersNineTools(t *testing.T) {
	got := listTools(t, connect(t, newEnv(t, runnertest.New(), false).deps))
	if len(got) != 9 {
		t.Fatalf("工具数量 = %d", len(got))
	}
	for _, n := range mutationNames {
		if got[n] != nil {
			t.Errorf("只读模式不应注册 %s", n)
		}
	}
}

func TestRebootRequiresConfirm(t *testing.T) {
	f := runnertest.New()
	e := newEnv(t, f, true)
	res, text := call(t, connect(t, e.deps), "router_reboot", map[string]any{})
	if !res.IsError || !strings.Contains(text, string(apperr.ConfirmRequired)) {
		t.Fatalf("res=%v text=%s", res.IsError, text)
	}
	if len(f.Calls()) != 0 {
		t.Fatal("没有 confirm 时不应执行任何命令")
	}
	data, _ := os.ReadFile(e.auditPath)
	if !strings.Contains(string(data), `"outcome":"rejected"`) {
		t.Fatalf("应记录被拒绝的调用: %s", data)
	}
}

func TestRebootQuotaEnforced(t *testing.T) {
	f := runnertest.New().On("reboot", runnertest.Stdout(""))
	cs := connect(t, newEnv(t, f, true).deps)
	if res, text := call(t, cs, "router_reboot", map[string]any{"confirm": true}); res.IsError {
		t.Fatalf("第一次应成功: %s", text)
	}
	res, text := call(t, cs, "router_reboot", map[string]any{"confirm": true})
	if !res.IsError || !strings.Contains(text, string(apperr.RebootQuotaExceeded)) {
		t.Fatalf("第二次应被拒绝: %s", text)
	}
	if n := len(f.CallsFor("reboot")); n != 1 {
		t.Fatalf("reboot 执行了 %d 次", n)
	}
}

func TestReadOnlyToolRetriesOnDisconnect(t *testing.T) {
	n := 0
	f := runnertest.New().On("conntrack", func(string, []byte) (runner.Result, error) {
		n++
		if n == 1 {
			return runner.Result{}, apperr.New(apperr.SSHUnreachable, "断开", "")
		}
		return runner.Result{Stdout: []byte("@@MERLINMCP:count@@\n100\n@@MERLINMCP:max@@\n1000\n")}, nil
	})
	res, text := call(t, connect(t, newEnv(t, f, true).deps), "conntrack_status", map[string]any{})
	if res.IsError || !strings.Contains(text, `"count": 100`) || n != 2 {
		t.Fatalf("isError=%v n=%d text=%s", res.IsError, n, text)
	}
}

func TestMutationDoesNotRetryOnDisconnect(t *testing.T) {
	n := 0
	f := runnertest.New().On("addfile_read", func(string, []byte) (runner.Result, error) {
		n++
		return runner.Result{}, apperr.New(apperr.SSHUnreachable, "断开", "")
	})
	res, text := call(t, connect(t, newEnv(t, f, true).deps), "dnsmasq_addfile_add", map[string]any{"lines": []any{"address=/a.example/0.0.0.0"}})
	if !res.IsError || !strings.Contains(text, string(apperr.SSHUnreachable)) || n != 1 {
		t.Fatalf("isError=%v n=%d text=%s", res.IsError, n, text)
	}
}

func TestSyslogRejectsBadArguments(t *testing.T) {
	cs := connect(t, newEnv(t, runnertest.New(), true).deps)
	for _, args := range []map[string]any{{"since": "1w"}, {"lines": 5000}, {"process": "a b"}} {
		res, text := call(t, cs, "syslog_read", args)
		if !res.IsError || !strings.Contains(text, string(apperr.InvalidArgument)) {
			t.Errorf("args=%v 应返回 INVALID_ARGUMENT: %s", args, text)
		}
	}
}

func TestDryRunIsNotAudited(t *testing.T) {
	f := runnertest.New().On("addfile_read", runnertest.Stdout("MISSING\n"))
	e := newEnv(t, f, true)
	res, text := call(t, connect(t, e.deps), "dnsmasq_addfile_add", map[string]any{"lines": []any{"x=1"}, "dry_run": true})
	if res.IsError || !strings.Contains(text, "+x=1") {
		t.Fatalf("text = %s", text)
	}
	if _, err := os.Stat(e.auditPath); !os.IsNotExist(err) {
		t.Fatal("dry_run 不应写审计日志")
	}
}

func TestSystemStatusIncludesRebootQuota(t *testing.T) {
	out := "@@MERLINMCP:uptime@@\n100.0 0\n@@MERLINMCP:loadavg@@\n0 0 0 1/1 1\n@@MERLINMCP:meminfo@@\nMemTotal: 100 kB\nMemAvailable: 50 kB\n" +
		"@@MERLINMCP:stat1@@\ncpu 1 0 1 8 0 0 0\n@@MERLINMCP:stat2@@\ncpu 2 0 2 16 0 0 0\n@@MERLINMCP:temp@@\n@@MERLINMCP:nvram@@\nproductid=RT-TEST\n"
	f := runnertest.New().On("system_status", runnertest.Stdout(out))
	res, text := call(t, connect(t, newEnv(t, f, true).deps), "system_status", map[string]any{})
	if res.IsError || !strings.Contains(text, `"reboot_allowed_today": true`) || !strings.Contains(text, "RT-TEST") {
		t.Fatalf("text = %s", text)
	}
}
```

- [ ] **Step 3: 运行测试，确认失败**

Run: `go test ./internal/tools/`
Expected: FAIL，编译错误 `undefined: tools.Register`

- [ ] **Step 4: 实现公共部分**

`internal/tools/tools.go`：

```go
// Package tools 把各业务模块注册为 MCP 工具，统一处理参数校验、错误格式和审计日志。
package tools

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/rshun/merlin-mcp/internal/apperr"
	"github.com/rshun/merlin-mcp/internal/audit"
	"github.com/rshun/merlin-mcp/internal/config"
	"github.com/rshun/merlin-mcp/internal/dnsmasq"
	"github.com/rshun/merlin-mcp/internal/reboot"
	"github.com/rshun/merlin-mcp/internal/runner"
)

// Deps 是工具层依赖的全部组件。Runner 必须是原始 Runner（不带重试），只读工具内部自行包装。
type Deps struct {
	Cfg      *config.Config
	Runner   runner.Runner
	Quota    *reboot.Quota
	Rebooter *reboot.Rebooter
	Dnsmasq  *dnsmasq.Manager
	Audit    *audit.Logger
}

// Register 注册全部工具。allow_mutations 为 false 时修改类工具不注册。
func Register(s *mcp.Server, d Deps) {
	registerReadOnly(s, d)
	if d.Cfg.AllowMutations {
		registerMutations(s, d)
	}
}

func boolPtr(b bool) *bool { return &b }

type toolFunc[In any] func(ctx context.Context, in In) (any, error)

// dryRunner 由支持 dry_run 的参数结构实现；dry_run 调用不写审计日志。
type dryRunner interface{ isDryRun() bool }

// add 注册一个工具。audited 为 true 时，调用结束后写审计日志（dry_run 除外）。
func add[In any](s *mcp.Server, d Deps, tool *mcp.Tool, audited bool, fn toolFunc[In]) {
	mcp.AddTool[In, any](s, tool, func(ctx context.Context, _ *mcp.CallToolRequest, in In) (*mcp.CallToolResult, any, error) {
		start := time.Now()
		out, err := fn(ctx, in)
		if dr, ok := any(in).(dryRunner); audited && !(ok && dr.isDryRun()) {
			d.audit(tool.Name, in, out, err, time.Since(start))
		}
		if err != nil {
			return errorResult(err), nil, nil
		}
		return jsonResult(out), nil, nil
	})
}

func jsonResult(v any) *mcp.CallToolResult {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return errorResult(apperr.New(apperr.Internal, "序列化结果失败: "+err.Error(), ""))
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(data)}}}
}

func errorResult(err error) *mcp.CallToolResult {
	data, _ := json.MarshalIndent(apperr.From(err), "", "  ")
	return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: string(data)}}}
}

// 这些错误码表示请求被规则拒绝，而不是执行出错。
var rejectedCodes = map[apperr.Code]bool{
	apperr.InvalidArgument:          true,
	apperr.ConfirmRequired:          true,
	apperr.RebootQuotaExceeded:      true,
	apperr.AddFileChangedExternally: true,
}

func (d Deps) audit(tool string, in, out any, err error, dur time.Duration) {
	e := audit.Entry{TS: time.Now(), Tool: tool, Args: toMap(in), DurationMS: dur.Milliseconds(), Outcome: "ok"}
	if err != nil {
		ae := apperr.From(err)
		e.ErrorCode, e.Summary, e.Outcome = string(ae.Code), ae.Message, "error"
		if rejectedCodes[ae.Code] {
			e.Outcome = "rejected"
		}
	} else if data, mErr := json.Marshal(out); mErr == nil {
		e.Summary = truncate(string(data), 300)
	}
	if werr := d.Audit.Write(e); werr != nil {
		slog.Warn("写入审计日志失败", "err", werr)
	}
}

func toMap(v any) map[string]any {
	data, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	var m map[string]any
	_ = json.Unmarshal(data, &m)
	return m
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return strings.ToValidUTF8(s[:n], "") + "…"
}

func invalid(msg, hint string) error { return apperr.New(apperr.InvalidArgument, msg, hint) }

func requireConfirm(confirm bool) error {
	if !confirm {
		return apperr.New(apperr.ConfirmRequired, "该操作需要 confirm: true", "先向用户说明影响并获得同意，再带 confirm: true 重新调用")
	}
	return nil
}

// lineLimit 处理 lines 参数：0 表示默认 200，范围 1-2000。
func lineLimit(n int) (int, error) {
	if n == 0 {
		return 200, nil
	}
	if n < 1 || n > 2000 {
		return 0, invalid("lines 必须在 1-2000 之间", "")
	}
	return n, nil
}
```

- [ ] **Step 5: 实现只读工具**

`internal/tools/readonly.go`：

```go
package tools

import (
	"context"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/rshun/merlin-mcp/internal/clients"
	"github.com/rshun/merlin-mcp/internal/diagnose"
	"github.com/rshun/merlin-mcp/internal/reboot"
	"github.com/rshun/merlin-mcp/internal/runner"
	"github.com/rshun/merlin-mcp/internal/shell"
	"github.com/rshun/merlin-mcp/internal/status"
	"github.com/rshun/merlin-mcp/internal/syslog"
)

type SyslogArgs struct {
	Lines          int    `json:"lines,omitempty" jsonschema:"返回的最大行数，1-2000，默认 200"`
	Keyword        string `json:"keyword,omitempty" jsonschema:"不区分大小写的子串过滤"`
	Process        string `json:"process,omitempty" jsonschema:"进程名，例如 dnsmasq，精确匹配 name: 或 name[pid]:"`
	Since          string `json:"since,omitempty" jsonschema:"只看最近一段时间，例如 30m、2h、1d，最大 7d"`
	IncludeRotated bool   `json:"include_rotated,omitempty" jsonschema:"是否同时读取轮转出去的旧日志"`
}

type KernelLogArgs struct {
	Lines   int    `json:"lines,omitempty" jsonschema:"返回的最大行数，1-2000，默认 200"`
	Keyword string `json:"keyword,omitempty" jsonschema:"不区分大小写的子串过滤"`
}

type NoArgs struct{}

type DiagnoseArgs struct {
	Action string `json:"action" jsonschema:"ping 或 nslookup"`
	Target string `json:"target" jsonschema:"目标域名或 IP 地址"`
	Count  int    `json:"count,omitempty" jsonschema:"ping 次数，1-5，默认 3"`
	Server string `json:"server,omitempty" jsonschema:"nslookup 使用的 DNS 服务器 IP，默认 127.0.0.1（路由器自己的 dnsmasq）"`
}

type KeywordArgs struct {
	Keyword string `json:"keyword,omitempty" jsonschema:"不区分大小写的子串过滤"`
}

type logResult struct {
	Path       string `json:"path,omitempty"`
	RouterTime string `json:"router_time,omitempty"`
	Returned   int    `json:"returned_lines"`
	Truncated  bool   `json:"truncated"`
	Log        string `json:"log"`
}

var readOnlyAnn = &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: boolPtr(false)}

func registerReadOnly(s *mcp.Server, d Deps) {
	ro := runner.WithRetry(d.Runner)

	add(s, d, &mcp.Tool{
		Name:        "syslog_read",
		Description: "读取路由器系统日志（syslog），用于分析问题。可按时间（since）、进程名（process）、关键字（keyword）过滤，返回最新的 lines 行，单次返回不超过 64KB。",
		Annotations: readOnlyAnn,
	}, false, func(ctx context.Context, in SyslogArgs) (any, error) {
		n, err := lineLimit(in.Lines)
		if err != nil {
			return nil, err
		}
		since, err := syslog.ParseSince(in.Since)
		if err != nil {
			return nil, invalid(err.Error(), "since 示例：30m、2h、1d")
		}
		if in.Process != "" && !shell.ValidProcessName(in.Process) {
			return nil, invalid("process 只能包含字母、数字和 _ . -", "")
		}
		snap, err := syslog.Fetch(ctx, ro, d.Cfg.Paths.Syslog, in.IncludeRotated)
		if err != nil {
			return nil, err
		}
		lines := syslog.Apply(snap.Lines, syslog.Filter{Since: since, Process: in.Process, Keyword: in.Keyword, Lines: n}, snap.Now)
		text, truncated := syslog.Render(syslog.Raws(lines), syslog.MaxOutputBytes)
		return logResult{Path: snap.Path, RouterTime: snap.Now.Format(time.RFC3339), Returned: len(lines), Truncated: truncated, Log: text}, nil
	})

	add(s, d, &mcp.Tool{
		Name:        "kernel_log_read",
		Description: "读取路由器内核日志（dmesg），用于排查驱动报错、Wi-Fi 芯片异常、内存耗尽（OOM）等 syslog 中看不到的问题。",
		Annotations: readOnlyAnn,
	}, false, func(ctx context.Context, in KernelLogArgs) (any, error) {
		n, err := lineLimit(in.Lines)
		if err != nil {
			return nil, err
		}
		lines, err := syslog.FetchKernel(ctx, ro)
		if err != nil {
			return nil, err
		}
		lines = syslog.FilterText(lines, in.Keyword, n)
		text, truncated := syslog.Render(lines, syslog.MaxOutputBytes)
		return logResult{Returned: len(lines), Truncated: truncated, Log: text}, nil
	})

	add(s, d, &mcp.Tool{
		Name:        "system_status",
		Description: "查看路由器系统状态：型号、固件版本、运行时长、负载、CPU 使用率、内存、CPU 温度，以及今天是否还能通过 MCP 重启。",
		Annotations: readOnlyAnn,
	}, false, func(ctx context.Context, _ NoArgs) (any, error) {
		sys, err := status.FetchSystem(ctx, ro)
		if err != nil {
			return nil, err
		}
		return struct {
			status.System
			Reboot reboot.Status `json:"reboot"`
		}{sys, d.Quota.Status()}, nil
	})

	add(s, d, &mcp.Tool{
		Name:        "wan_status",
		Description: "查看 WAN 状态：连接状态、协议、WAN IP、网关、DNS，以及 syslog 中最近 20 条 WAN 相关事件（掉线、重连等）。",
		Annotations: readOnlyAnn,
	}, false, func(ctx context.Context, _ NoArgs) (any, error) {
		return status.FetchWAN(ctx, ro, d.Cfg.Paths.Syslog)
	})

	add(s, d, &mcp.Tool{
		Name:        "clients_list",
		Description: "列出客户端：合并 DHCP 租约、ARP 表、静态 IP 分配和无线关联列表，给出每台设备的 IP、主机名、连接方式（wired/2.4G/5G/6G/unknown）、是否静态分配和租约剩余时间。",
		Annotations: readOnlyAnn,
	}, false, func(ctx context.Context, _ NoArgs) (any, error) {
		list, err := clients.Fetch(ctx, ro)
		if err != nil {
			return nil, err
		}
		return struct {
			Count   int              `json:"count"`
			Clients []clients.Client `json:"clients"`
		}{len(list), list}, nil
	})

	add(s, d, &mcp.Tool{
		Name:        "conntrack_status",
		Description: "查看连接跟踪表（conntrack）使用率。使用率过高时新连接会被丢弃，表现为部分网站打不开或时断时续。",
		Annotations: readOnlyAnn,
	}, false, func(ctx context.Context, _ NoArgs) (any, error) {
		return status.FetchConntrack(ctx, ro)
	})

	add(s, d, &mcp.Tool{
		Name:        "network_diagnose",
		Description: "从路由器上发起 ping 或 nslookup，用于区分是路由器本身的网络问题还是某个客户端的问题。",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: boolPtr(true)},
	}, false, func(ctx context.Context, in DiagnoseArgs) (any, error) {
		return diagnose.Run(ctx, ro, diagnose.Request{Action: in.Action, Target: in.Target, Count: in.Count, Server: in.Server})
	})

	add(s, d, &mcp.Tool{
		Name:        "dnsmasq_addfile_read",
		Description: "读取 dnsmasq.conf.add（Merlin 追加到 dnsmasq 主配置后面的自定义配置），每行带行号，并返回文件 sha256。",
		Annotations: readOnlyAnn,
	}, false, func(ctx context.Context, _ NoArgs) (any, error) {
		return d.Dnsmasq.Read(ctx)
	})

	add(s, d, &mcp.Tool{
		Name:        "dnsmasq_effective_config",
		Description: "读取 /etc/dnsmasq.conf，即 Merlin 合并 dnsmasq.conf.add 之后实际生效的 dnsmasq 配置，可按关键字过滤，用于确认自定义配置是否已加载。",
		Annotations: readOnlyAnn,
	}, false, func(ctx context.Context, in KeywordArgs) (any, error) {
		lines, err := d.Dnsmasq.Effective(ctx, in.Keyword)
		if err != nil {
			return nil, err
		}
		return struct {
			Lines any `json:"lines"`
		}{lines}, nil
	})
}
```

- [ ] **Step 6: 实现修改类工具**

`internal/tools/mutations.go`：

```go
package tools

import (
	"context"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/rshun/merlin-mcp/internal/dnsmasq"
)

type EditArgs struct {
	Lines  []string `json:"lines" jsonschema:"要添加或删除的配置行，每个元素一行，1-100 行"`
	DryRun bool     `json:"dry_run,omitempty" jsonschema:"为 true 时只返回 diff，不修改文件"`
}

func (a EditArgs) isDryRun() bool { return a.DryRun }

type ApplyArgs struct {
	Confirm               bool `json:"confirm,omitempty" jsonschema:"必须为 true；调用前需向用户确认"`
	AcceptExternalChanges bool `json:"accept_external_changes,omitempty" jsonschema:"检测到文件在 MCP 之外被修改时，是否仍然继续"`
}

type RebootArgs struct {
	Confirm bool `json:"confirm,omitempty" jsonschema:"必须为 true；调用前需向用户确认"`
}

func registerMutations(s *mcp.Server, d Deps) {
	editAnn := &mcp.ToolAnnotations{DestructiveHint: boolPtr(false), IdempotentHint: true, OpenWorldHint: boolPtr(false)}

	add(s, d, &mcp.Tool{
		Name:        "dnsmasq_addfile_add",
		Description: "向 dnsmasq.conf.add 追加配置行（已存在的行会跳过），写入前自动备份。只修改文件，不会生效；修改完成后需要调用 dnsmasq_apply。可先用 dry_run 预览 diff。",
		Annotations: editAnn,
	}, true, func(ctx context.Context, in EditArgs) (any, error) {
		return d.Dnsmasq.Edit(ctx, dnsmasq.OpAdd, in.Lines, in.DryRun)
	})

	add(s, d, &mcp.Tool{
		Name:        "dnsmasq_addfile_remove",
		Description: "从 dnsmasq.conf.add 删除与给定内容完全一致的行，找不到的行会在结果中列出。写入前自动备份。只修改文件，不会生效；修改完成后需要调用 dnsmasq_apply。可先用 dry_run 预览 diff。",
		Annotations: editAnn,
	}, true, func(ctx context.Context, in EditArgs) (any, error) {
		return d.Dnsmasq.Edit(ctx, dnsmasq.OpRemove, in.Lines, in.DryRun)
	})

	add(s, d, &mcp.Tool{
		Name:        "dnsmasq_apply",
		Description: "让 dnsmasq.conf.add 的修改生效：语法校验 → 重启 dnsmasq → 等待新进程 → 解析检查。任一步失败会自动回滚到上一个可用版本。重启期间局域网 DNS 会中断几秒。必须先向用户确认，再带 confirm: true 调用。",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: boolPtr(false), IdempotentHint: false, OpenWorldHint: boolPtr(false)},
	}, true, func(ctx context.Context, in ApplyArgs) (any, error) {
		if err := requireConfirm(in.Confirm); err != nil {
			return nil, err
		}
		ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
		defer cancel()
		return d.Dnsmasq.Apply(ctx, in.AcceptExternalChanges)
	})

	add(s, d, &mcp.Tool{
		Name:        "router_reboot",
		Description: "重启路由器。按配置的时区，每个自然日最多通过 MCP 重启 1 次。重启期间网络中断约 1-3 分钟。必须先向用户确认，再带 confirm: true 调用。",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: boolPtr(true), OpenWorldHint: boolPtr(false)},
	}, true, func(ctx context.Context, in RebootArgs) (any, error) {
		if err := requireConfirm(in.Confirm); err != nil {
			return nil, err
		}
		return d.Rebooter.Reboot(ctx)
	})
}
```

- [ ] **Step 7: 运行测试，确认通过**

Run: `go test ./internal/tools/`
Expected: PASS

如果编译报错与 SDK 的 API 不一致（例如 `NewInMemoryTransports` 返回值顺序、`ToolAnnotations` 字段类型），以 `go doc github.com/modelcontextprotocol/go-sdk/mcp <符号>` 的输出为准修正，不要改变工具名、参数和返回格式。

- [ ] **Step 8: 全量检查**

Run: `go test ./... && go vet ./... && gofmt -l .`
Expected: PASS，`gofmt -l .` 无输出

- [ ] **Step 9: 提交**

按"提交步骤模板"执行，`<files>` 为 `go.mod go.sum internal/tools`，提交说明：`feat: register MCP tools with error mapping and audit logging`

---

### Task 15: 命令行入口（serve / check / version）

**Files:**
- Create: `cmd/merlin-mcp/main.go`、`cmd/merlin-mcp/main_test.go`

**Interfaces:**
- Consumes: 全部 internal 包
- Produces:
  - 可执行文件 `merlin-mcp`；`main.version`（构建时通过 `-ldflags "-X main.version=..."` 注入，默认 `dev`）
  - `run(args []string, stdout, stderr io.Writer) int`：退出码 0 成功、1 运行失败、2 用法错误
  - HTTP 路由：`/mcp`（Streamable HTTP）、`/healthz`（返回 `{"status":"ok","version":"..."}`，不访问路由器）

- [ ] **Step 1: 写失败测试**

`cmd/merlin-mcp/main_test.go`：

```go
package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVersion(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"version"}, &out, &errb); code != 0 || strings.TrimSpace(out.String()) != "dev" {
		t.Fatalf("code=%d out=%q", code, out.String())
	}
}

func TestUsageErrors(t *testing.T) {
	for _, args := range [][]string{nil, {"unknown"}, {"serve"}, {"check"}} {
		var out, errb bytes.Buffer
		if code := run(args, &out, &errb); code != 2 {
			t.Errorf("args=%v code=%d，期望 2", args, code)
		}
	}
}

func TestServeRejectsInvalidConfig(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(p, []byte("listen: \"0.0.0.0:8765\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	if code := run([]string{"serve", "--config", p}, &out, &errb); code != 1 {
		t.Fatalf("code=%d stderr=%s", code, errb.String())
	}
	if !strings.Contains(errb.String(), "回环地址") {
		t.Fatalf("stderr 应说明原因: %s", errb.String())
	}
}
```

- [ ] **Step 2: 运行测试，确认失败**

Run: `go test ./cmd/merlin-mcp/`
Expected: FAIL，编译错误 `undefined: run`

- [ ] **Step 3: 实现**

`cmd/merlin-mcp/main.go`：

```go
// merlin-mcp 是管理 Asuswrt-Merlin 路由器的 MCP Server。
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/rshun/merlin-mcp/internal/audit"
	"github.com/rshun/merlin-mcp/internal/config"
	"github.com/rshun/merlin-mcp/internal/diagnose"
	"github.com/rshun/merlin-mcp/internal/dnsmasq"
	"github.com/rshun/merlin-mcp/internal/reboot"
	"github.com/rshun/merlin-mcp/internal/runner"
	"github.com/rshun/merlin-mcp/internal/shell"
	"github.com/rshun/merlin-mcp/internal/sshx"
	"github.com/rshun/merlin-mcp/internal/state"
	"github.com/rshun/merlin-mcp/internal/syslog"
	"github.com/rshun/merlin-mcp/internal/tools"
)

var version = "dev"

const usage = `用法:
  merlin-mcp serve   --config <配置文件>   启动 MCP 服务
  merlin-mcp check   --config <配置文件>   只读检查配置和路由器连接
  merlin-mcp version                       输出版本号`

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, usage)
		return 2
	}
	switch args[0] {
	case "version":
		fmt.Fprintln(stdout, version)
		return 0
	case "serve":
		return serve(args[1:], stderr)
	case "check":
		return check(args[1:], stdout, stderr)
	default:
		fmt.Fprintln(stderr, usage)
		return 2
	}
}

// configPath 解析 --config；缺失时返回空字符串。
func configPath(name string, args []string, stderr io.Writer) (string, bool) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	p := fs.String("config", "", "配置文件路径")
	if err := fs.Parse(args); err != nil || *p == "" {
		fmt.Fprintln(stderr, usage)
		return "", false
	}
	return *p, true
}

type app struct {
	ssh  *sshx.Client
	deps tools.Deps
}

func build(cfg *config.Config) (*app, error) {
	store, err := state.Open(cfg.StateDir)
	if err != nil {
		return nil, err
	}
	client, err := sshx.New(sshx.Options{
		Addr:        net.JoinHostPort(cfg.Router.Host, strconv.Itoa(cfg.Router.Port)),
		User:        cfg.Router.User,
		KeyFile:     cfg.Router.KeyFile,
		KnownHosts:  cfg.Router.KnownHosts,
		Timeout:     cfg.CommandTimeout,
		DialTimeout: 10 * time.Second,
		MaxSessions: 4,
		KeepAlive:   30 * time.Second,
	})
	if err != nil {
		return nil, err
	}
	quota := reboot.NewQuota(store, cfg.Location, cfg.Reboot.MaxPerDay, time.Now)
	return &app{ssh: client, deps: tools.Deps{
		Cfg:      cfg,
		Runner:   client,
		Quota:    quota,
		Rebooter: reboot.NewRebooter(quota, client),
		Dnsmasq: dnsmasq.New(client, dnsmasq.Options{
			AddPath:      cfg.Paths.DnsmasqAdd,
			BackupDir:    cfg.Paths.BackupDir,
			HealthDomain: cfg.Dnsmasq.HealthCheckDomain,
			Keep:         cfg.Dnsmasq.BackupKeep,
			Store:        store,
		}),
		Audit: audit.New(cfg.AuditLog),
	}}, nil
}

func serve(args []string, stderr io.Writer) int {
	logger := slog.New(slog.NewTextHandler(stderr, nil))
	slog.SetDefault(logger)
	p, ok := configPath("serve", args, stderr)
	if !ok {
		return 2
	}
	cfg, err := config.Load(p)
	if err != nil {
		logger.Error("配置无效", "err", err)
		return 1
	}
	a, err := build(cfg)
	if err != nil {
		logger.Error("初始化失败", "err", err)
		return 1
	}
	defer a.ssh.Close()

	server := mcp.NewServer(&mcp.Implementation{Name: "merlin-mcp", Version: version}, nil)
	tools.Register(server, a.deps)

	mux := http.NewServeMux()
	mux.Handle("/mcp", mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil))
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"status":"ok","version":%q}`, version)
	})
	srv := &http.Server{Addr: cfg.Listen, Handler: mux, ReadHeaderTimeout: 10 * time.Second}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	errCh := make(chan error, 1)
	go func() { errCh <- srv.ListenAndServe() }()
	logger.Info("merlin-mcp 已启动", "listen", cfg.Listen, "version", version, "allow_mutations", cfg.AllowMutations)

	select {
	case err := <-errCh:
		if !errors.Is(err, http.ErrServerClosed) {
			logger.Error("HTTP 服务异常退出", "err", err)
			return 1
		}
	case <-ctx.Done():
		logger.Info("收到退出信号，正在关闭")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}
	return 0
}

// check 依次执行只读检查。SSH 失败时立即退出；其余项目失败只给出警告。
func check(args []string, stdout, stderr io.Writer) int {
	p, ok := configPath("check", args, stderr)
	if !ok {
		return 2
	}
	cfg, err := config.Load(p)
	if err != nil {
		fmt.Fprintln(stderr, "✗ 配置:", err)
		return 1
	}
	fmt.Fprintln(stdout, "✓ 配置文件校验通过")
	a, err := build(cfg)
	if err != nil {
		fmt.Fprintln(stderr, "✗ 初始化:", err)
		return 1
	}
	defer a.ssh.Close()
	fmt.Fprintln(stdout, "✓ 私钥可读取，known_hosts 中有路由器的记录")

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	step := func(name, cmd string, judge func(runner.Result) (string, bool)) (bool, error) {
		res, err := a.ssh.Run(ctx, cmd, nil)
		if err != nil {
			fmt.Fprintf(stdout, "✗ %s: %v\n", name, err)
			return false, err
		}
		msg, good := judge(res)
		mark := "✓"
		if !good {
			mark = "!"
		}
		fmt.Fprintf(stdout, "%s %s: %s\n", mark, name, msg)
		return good, nil
	}
	trim := func(b []byte) string { return strings.TrimSpace(string(b)) }

	if _, err := step("SSH 连接与认证", runner.Op("check_echo", "echo ok"), func(r runner.Result) (string, bool) {
		return trim(r.Stdout), r.ExitCode == 0
	}); err != nil {
		return 1
	}
	_, _ = step("syslog 路径", runner.Op("check_syslog", syslog.DetectScript(cfg.Paths.Syslog)+`; echo "$p"`), func(r runner.Result) (string, bool) {
		if r.ExitCode != 0 {
			return trim(r.Stderr), false
		}
		return trim(r.Stdout), true
	})
	add := shell.Quote(cfg.Paths.DnsmasqAdd)
	_, _ = step("dnsmasq.conf.add", runner.Op("check_addfile", fmt.Sprintf("if [ -f %s ]; then echo 存在; else echo 不存在（第一次 add 时会自动创建）; fi", add)), func(r runner.Result) (string, bool) {
		return trim(r.Stdout), true
	})
	_, _ = step("dnsmasq --test 支持", runner.Op("check_dnsmasq_test", "dnsmasq --help 2>&1 | grep -c -- '--test' || true"), func(r runner.Result) (string, bool) {
		if n := trim(r.Stdout); n != "" && n != "0" {
			return "支持", true
		}
		return "不支持，dnsmasq_apply 将跳过语法校验", false
	})
	_, _ = step("健康检查域名", runner.Op("check_health", fmt.Sprintf("nslookup %s 127.0.0.1 2>&1", shell.Quote(cfg.Dnsmasq.HealthCheckDomain))), func(r runner.Result) (string, bool) {
		if ips := diagnose.ParseNslookup(string(r.Stdout)); r.ExitCode == 0 && len(ips) > 0 {
			return cfg.Dnsmasq.HealthCheckDomain + " → " + strings.Join(ips, ", "), true
		}
		return "无法解析 " + cfg.Dnsmasq.HealthCheckDomain + "，请在配置中更换 dnsmasq.health_check_domain", false
	})
	_, _ = step("路由器时间", runner.Op("check_date", "date '+%s %z'"), func(r runner.Result) (string, bool) {
		return trim(r.Stdout), r.ExitCode == 0
	})
	return 0
}
```

- [ ] **Step 4: 运行测试，确认通过**

Run: `go test ./cmd/merlin-mcp/`
Expected: PASS

- [ ] **Step 5: 交叉编译验证**

Run（Git Bash）：`CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "-s -w -X main.version=test" -o /tmp/merlin-mcp-test ./cmd/merlin-mcp && file /tmp/merlin-mcp-test`
Expected: 编译成功；如果有 `file` 命令，输出包含 `ELF 64-bit LSB executable, x86-64` 和 `statically linked`

- [ ] **Step 6: 全量检查**

Run: `go test ./... && go vet ./... && gofmt -l .`
Expected: PASS，`gofmt -l .` 无输出

- [ ] **Step 7: 提交**

按"提交步骤模板"执行，`<files>` 为 `cmd/merlin-mcp`，提交说明：`feat: add serve, check and version commands`

---

### Task 16: 部署文件、构建脚本和 README

**Files:**
- Create: `deploy/merlin-mcp.service`、`deploy/config.example.yaml`、`deploy/install.sh`
- Create: `scripts/build.sh`
- Modify: `README.md`（先读取现有内容，在其基础上补充）

**Interfaces:**
- Consumes: Task 15 的可执行文件和子命令
- Produces: `bash scripts/build.sh <版本>` 生成 `dist/merlin-mcp_<版本>_linux_amd64.tar.gz`，包含 `merlin-mcp`、`config.example.yaml`、`merlin-mcp.service`、`install.sh`

- [ ] **Step 1: 创建 systemd 用户服务文件**

`deploy/merlin-mcp.service`：

```ini
[Unit]
Description=merlin-mcp: MCP server for Asuswrt-Merlin router

[Service]
Type=simple
ExecStart=%h/.local/bin/merlin-mcp serve --config %h/.config/merlin-mcp/config.yaml
Restart=on-failure
RestartSec=5
NoNewPrivileges=true

[Install]
WantedBy=default.target
```

- [ ] **Step 2: 创建示例配置（只含占位符）**

`deploy/config.example.yaml`：

```yaml
# merlin-mcp 配置文件。所有路径必须写绝对路径，不支持 ~。
# 只能监听回环地址；服务没有鉴权。
listen: "127.0.0.1:8765"

router:
  host: your_router_ip                       # 路由器局域网 IP
  port: 22
  user: your_router_user                     # 路由器管理员用户名
  key_file: /home/rshun/.ssh/your_key_file   # 登录路由器使用的私钥（权限必须为 600，不支持带口令的私钥）
  known_hosts: /home/rshun/.ssh/known_hosts  # 必须已包含路由器的记录
  command_timeout: 15s

# 设为 true 才会注册 dnsmasq_addfile_add/remove、dnsmasq_apply、router_reboot。
# 未配置时默认为 false（只读）。
allow_mutations: false

paths:
  dnsmasq_add: /jffs/configs/dnsmasq.conf.add
  backup_dir: /jffs/merlin-mcp/backups
  syslog: auto                               # auto = 依次尝试 /jffs/syslog.log、/tmp/syslog.log

dnsmasq:
  health_check_domain: router.asus.com       # apply 后用来验证 dnsmasq 正常工作的域名
  backup_keep: 20

reboot:
  timezone: Asia/Shanghai                    # 按这个时区的自然日计算"每天"
  max_per_day: 1

state_dir: /home/rshun/.local/state/merlin-mcp
audit_log: /home/rshun/.local/state/merlin-mcp/audit.jsonl
```

- [ ] **Step 3: 创建安装脚本**

`deploy/install.sh`：

```bash
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
```

- [ ] **Step 4: 创建构建脚本**

`scripts/build.sh`：

```bash
#!/usr/bin/env bash
# 构建 linux/amd64 发布包：bash scripts/build.sh [版本]
set -euo pipefail
cd "$(dirname "$0")/.."

VERSION="${1:-$(git describe --tags --always --dirty)}"
NAME="merlin-mcp_${VERSION}_linux_amd64"
OUT="dist/$NAME"

mkdir -p "$OUT"
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath \
  -ldflags "-s -w -X main.version=${VERSION}" \
  -o "$OUT/merlin-mcp" ./cmd/merlin-mcp
cp deploy/config.example.yaml deploy/merlin-mcp.service deploy/install.sh "$OUT/"
tar -C dist -czf "dist/$NAME.tar.gz" "$NAME"
echo "dist/$NAME.tar.gz"
```

- [ ] **Step 5: 验证构建和脚本语法**

Run: `bash scripts/build.sh v0.1.0-test && tar -tzf dist/merlin-mcp_v0.1.0-test_linux_amd64.tar.gz && bash -n deploy/install.sh`
Expected: 输出 tar 包路径；列出 4 个文件；`bash -n` 无输出（语法正确）。`dist/` 已被 `.gitignore` 忽略，不要提交。

确认 LF 换行：`file deploy/install.sh scripts/build.sh`，输出中不应出现 `CRLF`。

- [ ] **Step 6: 更新 README**

先读取 `README.md`，保留原有标题，然后替换为下面的内容（中文）：

````markdown
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

在开发机上构建（需要 Go 1.26）：

```bash
bash scripts/build.sh v0.1.0
```

把 `dist/merlin-mcp_v0.1.0_linux_amd64.tar.gz` 复制到 Debian，以 MCP 的运行用户解压并执行：

```bash
tar -xzf merlin-mcp_v0.1.0_linux_amd64.tar.gz
bash merlin-mcp_v0.1.0_linux_amd64/install.sh
```

然后按脚本最后打印的步骤完成配置。

## 接入 Claude Code

```bash
claude mcp add --scope user --transport http merlin http://127.0.0.1:8765/mcp
```

## 安全说明

- 只监听回环地址，没有鉴权
- 不提供任意命令执行；所有命令都是代码中写死的模板
- 状态类工具不读取任何密码类 nvram 字段
- 修改类操作写入审计日志 `~/.local/state/merlin-mcp/audit.jsonl`
- MCP 的限制用于防止误操作，不能防止 AI 绕过 MCP，详见 `docs/superpowers/specs/2026-10-03-merlin-mcp-design.md` 第 12 节
````

- [ ] **Step 7: 提交**

按"提交步骤模板"执行，`<files>` 为 `deploy scripts README.md`，提交说明：`build: add systemd user unit, installer, build script and README`

---

### Task 17: 真实路由器样本与验收

这个任务需要用户在路由器和 Debian 上操作。执行者负责给出命令、分析用户提供的输出、根据结果补充测试和修复解析器。

**Files:**
- Create: `testdata/real/<名称>.txt`（用户提供并已脱敏的真实输出）
- Modify: 解析不正确的模块及其测试（根据样本确定）

**Interfaces:**
- Consumes: 全部模块
- Produces: 基于真实样本的回归测试

- [ ] **Step 1: 请用户在路由器上执行只读命令并提供输出**

向用户提供以下命令（全部只读），请用户 SSH 登录路由器执行，并在发回之前**把 MAC 地址、主机名、公网 IP 打码**：

```sh
cat /proc/uptime; cat /proc/loadavg; head -n 8 /proc/meminfo; head -n 1 /proc/stat
cat /sys/class/thermal/thermal_zone0/temp; cat /proc/dmu/temperature
for k in productid firmver buildno extendno wan0_state_t wan0_sbstate_t wan0_auxstate_t wan0_proto; do printf '%s=%s\n' $k "$(nvram get $k)"; done
for i in 0 1 2 3; do printf 'wl%s_ifname=%s wl%s_nband=%s\n' $i "$(nvram get wl${i}_ifname)" $i "$(nvram get wl${i}_nband)"; done
nvram get dhcp_staticlist; nvram get dhcp_hostnames
ls -l /jffs/syslog.log /tmp/syslog.log /jffs/syslog.log-1 /tmp/syslog.log-1
tail -n 30 /jffs/syslog.log 2>/dev/null || tail -n 30 /tmp/syslog.log
grep -iE 'wan|ppp|udhcpc|dhcp client' /jffs/syslog.log 2>/dev/null | tail -n 20
dmesg | tail -n 20
head -n 5 /var/lib/misc/dnsmasq.leases
cat /proc/net/arp
wl -i "$(nvram get wl0_ifname)" assoclist
cat /proc/sys/net/netfilter/nf_conntrack_count /proc/sys/net/netfilter/nf_conntrack_max
dnsmasq --help 2>&1 | grep -- --test; dnsmasq --test -C /jffs/configs/dnsmasq.conf.add; echo "exit=$?"
pidof dnsmasq
nslookup router.asus.com 127.0.0.1; echo "exit=$?"
ping -c 1 -W 2 127.0.0.1; echo "exit=$?"
date '+%s %z'
```

- [ ] **Step 2: 保存样本并逐项核对**

把用户提供的输出按类别保存到 `testdata/real/`（例如 `meminfo.txt`、`syslog.txt`、`leases.txt`、`arp.txt`、`assoclist.txt`、`nslookup.txt`、`ping.txt`），确认其中没有未打码的 MAC、主机名或公网 IP。

逐项核对 spec 第 10 节的假设，并记录结论：

| 假设 | 核对方法 | 不成立时 |
|---|---|---|
| dnsmasq 支持 `--test` | `dnsmasq --help` 是否包含 `--test` | 无需改代码，`validate` 已自动跳过 |
| `router.asus.com` 可解析 | nslookup 退出码为 0 且有 Address | 告诉用户在配置中更换 `health_check_domain` |
| `wl ... assoclist` 可用 | 输出包含 `assoclist <MAC>` | 无需改代码，已降级为 unknown |
| 温度可读 | 两条 cat 至少一条有数字 | 无需改代码，返回 null |
| syslog 路径 | `ls -l` 结果 | 告诉用户在配置中设置 `paths.syslog` |
| busybox ping 支持 `-W` | ping 退出码与输出 | 删除 `diagnose.go` 中的 `-W 2` 并更新测试 |
| `date '+%s %z'` 输出两段 | 输出形如 `1791000000 +0800` | 修改 `routercmd.ParseDate` 并补测试 |

- [ ] **Step 3: 为每类样本补充回归测试**

在对应包的测试文件中，为每个样本各加一个测试，读取 `testdata/real/` 下的文件（路径用 `filepath.Join("..", "..", "testdata", "real", "<文件>")`），调用对应的解析函数（`syslog.Parse`、`status.ParseSystem`、`clients.ParseLeases`、`clients.ParseARP`、`clients.ParseStaticList`、`clients.ParseAssoc`、`diagnose.ParsePing`、`diagnose.ParseNslookup`、`status.WANEvents`），断言关键字段非空且数值合理。

例如 `internal/clients/clients_test.go` 增加：

```go
func TestParseLeasesRealSample(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "testdata", "real", "leases.txt"))
	if err != nil {
		t.Skip("没有真实样本")
	}
	got := ParseLeases(string(data))
	if len(got) == 0 {
		t.Fatal("真实样本应至少解析出一条租约")
	}
	for _, l := range got {
		if l.IP == "" || l.MAC == "" {
			t.Errorf("字段为空: %+v", l)
		}
	}
}
```

（需要在该测试文件的 import 中加入 `os` 和 `path/filepath`。）

如果某个测试失败，使用 superpowers:systematic-debugging 定位原因，修改解析器，直到全部通过。`status.wanEventRe` 根据真实的 WAN 日志调整：保证真实样本中的 WAN 上下线事件都能匹配，同时不匹配 dnsmasq 的普通查询日志。

- [ ] **Step 4: 全量检查并提交**

Run: `go test ./... && go vet ./... && gofmt -l .`
Expected: PASS，`gofmt -l .` 无输出

按"提交步骤模板"执行，`<files>` 为 `testdata internal`，提交说明：`test: add regression tests from real router samples`。提交前再确认一遍 `testdata/real/` 中没有未打码的 MAC 和主机名：

```bash
git diff --cached -- testdata | grep -nE "([0-9a-fA-F]{2}:){5}[0-9a-fA-F]{2}"
```

命中的只能是 `AA:BB:CC:` 开头的打码 MAC，或者用户明确确认可以保留的值。

- [ ] **Step 5: 在 Debian 上验收（用户执行，执行者给出命令并核对结果）**

1. 开发机构建：`bash scripts/build.sh v0.1.0`，用户把 tar 包复制到 Debian
2. 以 `rshun` 用户安装：`bash merlin-mcp_v0.1.0_linux_amd64/install.sh`
3. 首次安装执行一次：`sudo loginctl enable-linger rshun`
4. 编辑 `~/.config/merlin-mcp/config.yaml`：填写路由器地址、用户名、私钥路径；先保持 `allow_mutations: false`
5. 运行 `~/.local/bin/merlin-mcp check --config ~/.config/merlin-mcp/config.yaml`：所有 ✓/! 项的结论与 Step 2 一致
6. 启动服务：`systemctl --user enable --now merlin-mcp`，然后 `curl -s http://127.0.0.1:8765/healthz` 返回 `{"status":"ok",...}`
7. 以 `claude` 用户执行：`claude mcp add --scope user --transport http merlin http://127.0.0.1:8765/mcp`
8. 在 Claude Code 中逐个调用 9 个只读工具，确认结果合理
9. 把 `allow_mutations` 改为 `true`，执行 `systemctl --user restart merlin-mcp`
10. 选一个合适的时间，在 Claude Code 中完整走一遍：`dnsmasq_addfile_add`（添加一行注释，例如 `# merlin-mcp test`）→ `dnsmasq_apply` → `dnsmasq_addfile_remove`（删除这行）→ `dnsmasq_apply`，确认每一步结果和 `~/.local/state/merlin-mcp/audit.jsonl` 中的记录
11. 调用 `router_reboot` 时不传 `confirm`，确认返回 `CONFIRM_REQUIRED`。是否真实重启由用户决定

验收结果（每一项通过/不通过及原因）如实报告给用户。
